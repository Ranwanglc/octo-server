package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/config"
)

// Service implements the business logic for the /v1/auth/verify* endpoints.
// It depends on the in-tree Lookup registries (BotLookup, APIKeyLookup) and
// the shared config.Context for Cache + DB access — it does NOT import
// modules/{user,bot_api,usersecret,oidc} implementation packages, per the
// dependency-direction invariant pinned by imports_test.go.
//
// HTTP handlers in api.go are thin shims over these methods so the
// business logic stays testable without a Gin context.
type Service struct {
	ctx *config.Context
}

// NewService constructs the Service. ctx must be non-nil; the Lookup
// implementations are pulled from the package-level registry at request
// time so the providers (bot_api / usersecret) can register
// asynchronously during their own module init.
func NewService(ctx *config.Context) *Service {
	if ctx == nil {
		panic("auth: NewService requires non-nil config.Context")
	}
	return &Service{ctx: ctx}
}

// VerifyUser implements POST /v1/auth/verify business logic.
//
// 1. Lookup the token in Redis via the shared TokenCachePrefix (same
//    code path AuthMiddleware uses, via the migrated CacheTokenParser).
// 2. Decode the cached value (v2 JSON envelope or legacy uid@name[@role]).
// 3. Hydrate the response with owned_bots (the legacy
//    SELECT-from-robot-join-user query, preserved verbatim).
//
// All "token missing / cache miss / decode failure" outcomes return
// ErrInvalidUserToken — a single anti-enumeration sentinel that the
// HTTP layer maps to a single 401 errcode.ErrAuthTokenInvalid.
func (s *Service) VerifyUser(ctx context.Context, req VerifyUserReq) (*VerifyUserResp, error) {
	token := strings.TrimSpace(req.Token)
	if token == "" {
		return nil, ErrInvalidUserToken
	}
	raw, cacheErr := s.ctx.Cache().Get(s.ctx.GetConfig().Cache.TokenCachePrefix + token)
	if cacheErr != nil {
		// Cache error must not masquerade as "no such token" — the SDK
		// distinguishes session-expired (re-login) from infrastructure
		// failure (retry). Wrap with a typed sentinel.
		return nil, fmt.Errorf("%w: %v", ErrUpstreamFailure, cacheErr)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, ErrInvalidUserToken
	}
	info, decodeErr := Decode(raw)
	if decodeErr != nil {
		return nil, ErrInvalidUserToken
	}

	resp := &VerifyUserResp{
		SchemaVersion: 1,
		Kind:          "user",
		UID:           info.UID,
		Name:          info.Name,
		Role:          info.Role,
		OwnedBots:     make([]OwnedBot, 0),
	}

	// Owned bots: robot rows whose creator_uid matches, joined to user
	// for display name. Preserved verbatim from the legacy
	// authVerifyToken in modules/user/api.go.
	type botRow struct {
		RobotID string `db:"robot_id"`
		Name    string `db:"name"`
	}
	var bots []botRow
	if _, err := s.ctx.DB().SelectBySql(
		"SELECT r.robot_id, IFNULL(u.name,'') as name FROM robot r "+
			"INNER JOIN `user` u ON r.robot_id = u.uid "+
			"WHERE r.creator_uid = ? AND r.status = 1", info.UID,
	).Load(&bots); err != nil {
		// owned_bots failure is non-fatal — return identity with empty
		// list rather than failing the whole verification. Matches the
		// legacy handler's err==nil-only-then-populate behaviour
		// (modules/user/api.go authVerifyToken lines ~4030-4040).
		return resp, nil
	}
	for _, b := range bots {
		resp.OwnedBots = append(resp.OwnedBots, OwnedBot{UID: b.RobotID, Name: b.Name})
	}
	return resp, nil
}

// VerifyBot implements POST /v1/auth/verify-bot business logic.
//
// Token kind is routed by prefix:
//   - "app_": LookupAppBot (in-memory Registry → DB fallback);
//     ErrAppBotUnpublished surfaces as ErrBotUnavailable to the caller.
//   - else  : LookupUserBot (User Bot — bf_ prefix or legacy
//     unprefixed form hitting the robot table).
//
// Owner name and current-space hydration mirrors the legacy
// authVerifyBot in modules/user/api.go: owner name via the user table;
// User Bot space_id via the first active space_member row. App Bot's
// SpaceID comes directly from the bot row (Scope="space" binding) or
// is empty (Scope="platform").
func (s *Service) VerifyBot(ctx context.Context, req VerifyBotReq) (*VerifyBotResp, error) {
	token := strings.TrimSpace(req.BotToken)
	if token == "" {
		return nil, ErrInvalidBotToken
	}
	lookup := GetBotLookup()
	if lookup == nil {
		// No provider registered. Treat as infra failure so callers
		// retry rather than assume the token is bad.
		return nil, ErrUpstreamFailure
	}

	if strings.HasPrefix(token, "app_") {
		id, err := lookup.LookupAppBot(token)
		if err != nil {
			if errors.Is(err, ErrAppBotUnpublished) {
				return nil, ErrBotUnavailable
			}
			return nil, fmt.Errorf("%w: %v", ErrUpstreamFailure, err)
		}
		if id == nil {
			return nil, ErrInvalidBotToken
		}
		ownerName := s.lookupUserName(id.OwnerUID)
		// App Bot's display name MAY come from registry-hit path empty;
		// fall back to DB user name lookup keyed by bot UID — matches
		// legacy authVerifyBot which always did the user-table lookup
		// for bot display name.
		botName := id.BotName
		if botName == "" {
			botName = s.lookupUserName(id.BotUID)
		}
		return &VerifyBotResp{
			SchemaVersion: 1,
			Kind:          "bot",
			BotUID:        id.BotUID,
			BotName:       botName,
			BotKind:       "app",
			OwnerUID:      id.OwnerUID,
			OwnerName:     ownerName,
			Scope:         id.Scope,
			SpaceID:       id.SpaceID,
		}, nil
	}

	// User Bot path (bf_ prefix or legacy unprefixed).
	id, err := lookup.LookupUserBot(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamFailure, err)
	}
	if id == nil {
		return nil, ErrInvalidBotToken
	}
	botName := id.BotName
	if botName == "" {
		botName = s.lookupUserName(id.BotUID)
	}
	ownerName := s.lookupUserName(id.OwnerUID)
	// User Bot "current space" — first active space_member row, matching
	// legacy authVerifyBot's space_id semantic (display hint, not a
	// binding). Empty when bot has no space membership. The nil-ctx
	// guard mirrors lookupUserName so unit tests can construct a Service
	// without a config.Context.
	var spaceID string
	if s.ctx != nil {
		_ = s.ctx.DB().Select("space_id").From("space_member").
			Where("uid = ? AND status = 1", id.BotUID).
			OrderDir("created_at", false).
			Limit(1).
			LoadOne(&spaceID)
	}

	return &VerifyBotResp{
		SchemaVersion: 1,
		Kind:          "bot",
		BotUID:        id.BotUID,
		BotName:       botName,
		BotKind:       "user",
		OwnerUID:      id.OwnerUID,
		OwnerName:     ownerName,
		SpaceID:       spaceID,
	}, nil
}

// lookupUserName resolves a uid to a display name from the user table.
// Returns "" on cache miss, error, or empty name — never panics. Matches
// the legacy "best-effort owner name" semantic from authVerifyBot.
//
// Safe to call with a Service whose ctx is nil (returns "") so unit tests
// can exercise the prefix-routing paths of VerifyBot without standing up
// a full DB; integration tests cover the real DB path.
func (s *Service) lookupUserName(uid string) string {
	if uid == "" || s == nil || s.ctx == nil {
		return ""
	}
	var name string
	if _, err := s.ctx.DB().Select("IFNULL(name,'') as name").From("`user`").
		Where("uid = ?", uid).
		Load(&name); err != nil {
		return ""
	}
	return name
}

// Sentinel errors returned by Service methods. The HTTP layer in api.go
// maps these to errcode.ErrAuth* codes — keeping the mapping in one
// place lets the Service be unit-tested without httperr import.
var (
	// ErrInvalidUserToken — anti-enumeration catch-all for user verify.
	// Maps to errcode.ErrAuthTokenInvalid (401).
	ErrInvalidUserToken = errors.New("auth: invalid or expired user token")
	// ErrInvalidBotToken — anti-enumeration catch-all for bot verify.
	// Maps to errcode.ErrAuthTokenInvalid (401).
	ErrInvalidBotToken = errors.New("auth: invalid or expired bot token")
	// ErrBotUnavailable — App Bot exists but is unpublished.
	// Maps to errcode.ErrAuthBotUnpublished (503).
	ErrBotUnavailable = errors.New("auth: bot is currently unavailable")
	// ErrUpstreamFailure — DB / cache failure during verification.
	// Maps to errcode.ErrAuthUpstreamFailed (500).
	ErrUpstreamFailure = errors.New("auth: upstream verification failure")
)
