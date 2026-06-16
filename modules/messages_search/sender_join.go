package messages_search

import (
	"context"
	"fmt"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

const (
	// senderCacheCapacity bounds the in-process LRU. 10K entries × ~256 bytes
	// per entry is well under 5 MB; the working set across our largest tenant
	// fits inside this budget for hot Search queries.
	senderCacheCapacity = 10_000
	// senderCacheTTL is the soft-expiry window. Hits past this age count as
	// misses, falling back to the DB; nothing actively evicts on TTL — entries
	// are overwritten on the next miss.
	senderCacheTTL = 5 * time.Minute
)

// senderInfo is the cached projection: display name + avatar URL. We avoid
// caching the full user struct because (a) the cached fields are the only
// ones the search responses surface, and (b) compact entries lift our
// effective cache hit rate.
type senderInfo struct {
	Name   string
	Avatar string
	Cached time.Time
}

// senderCache wraps a typed LRU with a TTL check on Get. golang-lru/v2 has no
// per-entry TTL of its own; we layer one on top so we can keep the dependency
// surface small.
type senderCache struct {
	lru *lru.Cache[string, senderInfo]
	ttl time.Duration
}

func newSenderCache(capacity int, ttl time.Duration) *senderCache {
	c, err := lru.New[string, senderInfo](capacity)
	if err != nil {
		// lru.New only errors on capacity <= 0; we control the input so
		// this is unreachable in practice. Fail loudly during init.
		panic(fmt.Sprintf("messages_search: senderCache init: %v", err))
	}
	return &senderCache{lru: c, ttl: ttl}
}

// Get returns the cached entry only if it is within TTL. The key is the
// scoped key returned by senderCacheKey; passing a bare uid would mix DM
// display names with per-group remarks for the same user.
func (c *senderCache) Get(key string) (senderInfo, bool) {
	if c == nil {
		return senderInfo{}, false
	}
	v, ok := c.lru.Get(key)
	if !ok {
		return senderInfo{}, false
	}
	if c.ttl > 0 && time.Since(v.Cached) > c.ttl {
		return senderInfo{}, false
	}
	return v, true
}

// Put stamps the entry's Cached field and stores it. The key must already
// be a scoped key (see senderCacheKey).
func (c *senderCache) Put(key string, v senderInfo) {
	if c == nil {
		return
	}
	v.Cached = time.Now()
	c.lru.Add(key, v)
}

// senderCacheKey returns the scope-qualified cache key for a uid. DM hits and
// each group/thread room get a separate slot so that per-group remarks (which
// override user.Name) cannot leak across rooms or into DM rendering.
//
//   - DM (channelType=1)        → "u:" + uid
//   - group/thread (2/5)        → "g:" + groupNo + ":" + uid
//   - unknown / groupNo missing → "u:" + uid (treat as un-scoped, safe default)
func senderCacheKey(channelType uint8, channelID, uid string) string {
	groupNo := groupNoFromChannel(channelType, channelID)
	if groupNo == "" {
		return "u:" + uid
	}
	return "g:" + groupNo + ":" + uid
}

// senderJoinResult bundles the two return maps used by the response builders.
type senderJoinResult struct {
	Names   map[string]string
	Avatars map[string]string
}

// senderJoin batch-resolves display names and avatar URLs for the senders of
// a single search response page. The lookup branches on channel type:
//
//   - DM (1) — calls userService.GetUsers and uses the user's display name
//     directly. Avatar URL is built from the standard `users/{uid}/avatar`
//     template (mirrored from modules/user/1module.go:191).
//   - Group / Thread (2 / 5) — calls groupService.GetMembers(groupNo) once and
//     prefers the per-group `remark` over the user's display name. Falls back
//     to userService.GetUsers for any senders not on the group roster
//     (e.g. recently kicked members whose old messages are still indexed).
//
// Cache misses repopulate the LRU; the function is allocation-friendly enough
// that callers can invoke it once per page without paginated memoisation.
func (h *Handler) senderJoin(ctx context.Context, uids []string, channelType uint8, channelID string) senderJoinResult {
	out := senderJoinResult{
		Names:   make(map[string]string, len(uids)),
		Avatars: make(map[string]string, len(uids)),
	}
	if len(uids) == 0 {
		return out
	}

	miss := make([]string, 0, len(uids))
	for _, uid := range uids {
		key := senderCacheKey(channelType, channelID, uid)
		if v, ok := h.cache.Get(key); ok {
			out.Names[uid] = v.Name
			out.Avatars[uid] = v.Avatar
			continue
		}
		miss = append(miss, uid)
	}
	if len(miss) == 0 {
		return out
	}

	groupNo := groupNoFromChannel(channelType, channelID)
	remarkByUID := map[string]string{}
	if groupNo != "" {
		// userService.GetUsers / groupService.GetMembers don't take ctx;
		// short-circuit here so a tripped timeout doesn't fire a fresh DB
		// round trip whose result we'd just discard.
		if err := ctx.Err(); err != nil {
			h.Warn("sender_join: ctx cancelled before GetMembers", zap.Error(err))
			return out
		}
		members, err := h.groupService.GetMembers(groupNo)
		if err != nil {
			// Soft-fail: name falls back to user.Name. Log so ops can
			// answer "why didn't my group remark show up?" without having
			// to inspect the OS payload.
			h.Warn("sender_join: GetMembers failed",
				zap.String("group_no", groupNo),
				zap.Error(err))
		} else {
			for _, m := range members {
				if m == nil {
					continue
				}
				if m.Remark != "" {
					remarkByUID[m.UID] = m.Remark
				}
			}
		}
	}

	if err := ctx.Err(); err != nil {
		h.Warn("sender_join: ctx cancelled before GetUsers", zap.Error(err))
		return out
	}
	users, err := h.userService.GetUsers(miss)
	if err != nil {
		// Soft-fail: surface zero-name entries upstream rather than 500.
		// Only the requested page is affected; the cache remains empty so
		// the next request retries.
		h.Warn("sender_join: GetUsers failed",
			zap.Int("missing", len(miss)),
			zap.Error(err))
		return out
	}
	for _, u := range users {
		if u == nil {
			continue
		}
		name := u.Name
		if remark, ok := remarkByUID[u.UID]; ok && remark != "" {
			name = remark
		}
		info := senderInfo{Name: name, Avatar: buildUserAvatarURL(h.cfg, u.UID)}
		h.cache.Put(senderCacheKey(channelType, channelID, u.UID), info)
		out.Names[u.UID] = info.Name
		out.Avatars[u.UID] = info.Avatar
	}
	return out
}

// buildUserAvatarURL produces the avatar URL for a uid. When the deployment
// has set OCTO_USER_AVATAR_BASE_URL the relative `users/{uid}/avatar` template
// (mirrored from modules/user/1module.go:191) is joined to that base so the
// wire value is an absolute URL (spec v4.2 §2.1 / R8). With no base configured
// we return the relative path unchanged for the frontend to join.
func buildUserAvatarURL(cfg SearchConfig, uid string) string {
	if cfg.UserAvatarBaseURL != "" {
		return fmt.Sprintf("%s/users/%s/avatar", cfg.UserAvatarBaseURL, uid)
	}
	return fmt.Sprintf("users/%s/avatar", uid)
}

// uniqUIDs strips duplicate strings while preserving first-seen order. Used
// before sender JOIN so we don't bill cache lookups for the same uid twice.
func uniqUIDs(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
