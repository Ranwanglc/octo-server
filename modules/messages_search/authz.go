package messages_search

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	"go.uber.org/zap"
)

// checkChannelAccess enforces the channel-membership gate shared by all four
// /_search* endpoints: a caller may only search conversations they can
// already read. The four-step parity goal here is "search must not return
// hits the ordinary read path would have hidden", so the checks below
// mirror the gates in modules/message/api_channel_files.go (group + p2p)
// and modules/thread (thread parent + status).
//
//   - p2p (1)   — caller must be friends with the peer AND neither side may
//     have blacklisted the other. The fakeChannelID alone is not enough:
//     blacklist hides past messages and an attacker's "search through a
//     historical DM" needs the same gate as a read.
//
//   - group (2) — group must exist AND not be disbanded AND caller must be
//     an *active* member. Disband is checked BEFORE membership because
//     bookkeeping bugs (or a race during the disband flow) could leave a
//     group_member row pointing at a disbanded group; gating on membership
//     alone would leak history of a disbanded group.
//
//   - thread (5) — channel_id must parse, the thread must still exist
//     (GetThread maps "not found" + "deleted" to err), AND caller must be
//     an active member of the parent group. Archived threads (status=2)
//     remain readable, matching the read path.
//
// Non-members get NOT_FOUND with resource=channel (anti-enumeration: the
// response must not reveal whether the group / thread / peer exists).
// Lookup errors fail closed with INTERNAL_ERROR for the friend/blacklist
// and group lookups; thread GetThread errors collapse with the existence
// check into NOT_FOUND so we don't leak whether the thread row is present
// or only the DB happened to be down (anti-enumeration over operational
// signal).
func (h *Handler) checkChannelAccess(c *wkhttp.Context, channelType uint8, channelID, loginUID string) bool {
	switch channelType {
	case channelTypePerson:
		return h.checkP2PAccess(c, channelID, loginUID)
	case channelTypeGroup:
		return h.checkGroupAccess(c, channelID, loginUID)
	case channelTypeThread:
		return h.checkThreadAccess(c, channelID, loginUID)
	default:
		// Unreachable in practice: validate.go rejects unknown channel
		// types before this check runs. Kept fail-closed (defense in
		// depth) so a future caller that bypasses validation can never
		// inherit implicit access.
		h.Warn("checkChannelAccess: unexpected channel_type",
			zap.Uint8("channel_type", channelType),
			zap.String("channel_id", channelID),
			zap.String("uid", loginUID))
		respondNotFound(c, "channel")
		return false
	}
}

// checkP2PAccess gates DM search behind the friend + blacklist relationship.
// Pattern mirrors modules/message/api_channel_files.go:195-211, with the
// added bidirectional blacklist check (PR #361 review): the read path's
// blacklist enforcement happens in IM kernel, but search bypasses IM and
// must apply both directions explicitly.
func (h *Handler) checkP2PAccess(c *wkhttp.Context, peerUID, loginUID string) bool {
	if peerUID == loginUID {
		// "Notes-to-self" channel; mirrors the read path's `if peer != self`
		// guard. Friend / blacklist checks are not meaningful here.
		return true
	}
	isFriend, err := h.userService.IsFriend(loginUID, peerUID)
	if err != nil {
		h.Error("p2p access check failed: IsFriend",
			zap.Error(err),
			zap.String("uid", loginUID),
			zap.String("peer", peerUID))
		respondInternal(c)
		return false
	}
	if !isFriend {
		respondNotFound(c, "channel")
		return false
	}
	// Bidirectional blacklist: either side blocking the other hides DM
	// history both for the blocker (their preference) and the blocked
	// party (anti-harassment). Search must respect both.
	blockedByMe, err := h.userService.ExistBlacklist(loginUID, peerUID)
	if err != nil {
		h.Error("p2p access check failed: ExistBlacklist (me→peer)",
			zap.Error(err),
			zap.String("uid", loginUID),
			zap.String("peer", peerUID))
		respondInternal(c)
		return false
	}
	if blockedByMe {
		respondNotFound(c, "channel")
		return false
	}
	blockedByPeer, err := h.userService.ExistBlacklist(peerUID, loginUID)
	if err != nil {
		h.Error("p2p access check failed: ExistBlacklist (peer→me)",
			zap.Error(err),
			zap.String("uid", loginUID),
			zap.String("peer", peerUID))
		respondInternal(c)
		return false
	}
	if blockedByPeer {
		respondNotFound(c, "channel")
		return false
	}
	return true
}

// checkGroupAccess fail-closes if the group is missing or disbanded BEFORE
// consulting membership, so leftover group_member rows on a disbanded
// group cannot hand back read access. Status check matches the
// fail-closed templates in group/service.go:1327, :1553, :1764.
func (h *Handler) checkGroupAccess(c *wkhttp.Context, groupNo, loginUID string) bool {
	groupModel, err := h.groupService.GetGroupWithGroupNo(groupNo)
	if err != nil {
		h.Error("group access check failed: GetGroupWithGroupNo",
			zap.Error(err),
			zap.String("group_no", groupNo))
		respondInternal(c)
		return false
	}
	if groupModel == nil || groupModel.Status == group.GroupStatusDisband {
		respondNotFound(c, "channel")
		return false
	}
	active, err := h.groupService.ExistMemberActive(groupNo, loginUID)
	if err != nil {
		h.Error("group access check failed: ExistMemberActive",
			zap.Error(err),
			zap.String("group_no", groupNo))
		respondInternal(c)
		return false
	}
	if !active {
		respondNotFound(c, "channel")
		return false
	}
	return true
}

// checkThreadAccess parses the composite `{group_no}____{short_id}` and
// gates on (a) the thread row still existing (GetThread collapses
// not-found / deleted / underlying DB error into err), (b) caller being
// an active member of the parent group. Archived threads are still
// searchable because the read path still surfaces them.
//
// GetThread error → NOT_FOUND is intentional even on transient DB
// failure: leaking "the thread exists but DB is down" (vs "thread does
// not exist") gives an enumeration oracle. Operators see the cause in
// the upstream (group / thread service) logs.
func (h *Handler) checkThreadAccess(c *wkhttp.Context, channelID, loginUID string) bool {
	parsedGroup, shortID, err := thread.ParseChannelID(channelID)
	if err != nil {
		respondNotFound(c, "channel")
		return false
	}
	if _, err := h.threadService.GetThread(parsedGroup, shortID, loginUID); err != nil {
		// Three-way collapse (not-found / deleted / DB error) per the
		// thread.IService contract — see thread/service.go::GetThread.
		// We also want anti-enumeration over operational signal here, so
		// keep all three on the NOT_FOUND surface even though the DB
		// case is technically a transient infra failure.
		respondNotFound(c, "channel")
		return false
	}
	active, err := h.groupService.ExistMemberActive(parsedGroup, loginUID)
	if err != nil {
		h.Error("thread access check failed: ExistMemberActive",
			zap.Error(err),
			zap.String("group_no", parsedGroup))
		respondInternal(c)
		return false
	}
	if !active {
		respondNotFound(c, "channel")
		return false
	}
	return true
}
