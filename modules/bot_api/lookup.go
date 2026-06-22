package bot_api

import (
	"fmt"

	"github.com/Mininglamp-OSS/octo-server/modules/auth"
)

// Compile-time assertion: BotAPI satisfies auth.BotLookup. If the
// interface in modules/auth/lookup.go grows or a method signature drifts,
// this fails at build time, not at runtime in main.go's DI wiring.
var _ auth.BotLookup = (*BotAPI)(nil)

// LookupUserBot resolves a User Bot (`bf_` token, robot table) to its
// identity. Implements [auth.BotLookup.LookupUserBot]. Returns:
//   - (nil, nil)  : token is well-formed but not a recognised User Bot
//   - (nil, err)  : infrastructure failure (DB unreachable); verify
//                   handler must surface as AUTH_UPSTREAM_UNAVAILABLE
//   - (id,  nil)  : matched bot row → identity struct
//
// The actual DB query (`queryRobotByBotToken`) is reused verbatim from
// the existing authUserBot middleware path in auth.go:46-62 — no SQL
// change, no semantic change. This method exists so modules/auth's
// verify-bot handler can call it without importing bot_api (the
// interface boundary lives in modules/auth; see auth/lookup.go).
func (ba *BotAPI) LookupUserBot(token string) (*auth.UserBotIdentity, error) {
	if token == "" {
		return nil, nil
	}
	r, err := ba.db.queryRobotByBotToken(token)
	if err != nil {
		return nil, fmt.Errorf("bot_api: lookup user bot: %w", err)
	}
	if r == nil {
		return nil, nil
	}
	return &auth.UserBotIdentity{
		BotUID:   r.RobotID,
		BotName:  r.Username,
		OwnerUID: r.CreatorUID,
	}, nil
}

// LookupAppBot resolves an App Bot (`app_` token, app_bot table) to its
// identity. Implements [auth.BotLookup.LookupAppBot]. Preserves the
// existing two-tier lookup from authAppBot:
//
//  1. O(1) in-memory Registry (via [lookupAppBotRegistry]) — primary
//     path; populated by modules/app_bot at boot.
//  2. DB fallback (`queryAppBotByToken`) — covers the startup race
//     window before the registry is loaded.
//
// Returns:
//   - (nil, nil)                        : no matching bot
//   - (nil, auth.ErrAppBotUnpublished)  : DB row exists but status != 1
//   - (nil, err)                        : DB query failure
//   - (id,  nil)                        : matched + published
//
// The status check is deliberately ONLY on the DB path — the in-memory
// Registry is populated at boot from published bots only, so a registry
// hit is always a published bot. This matches the existing
// authAppBot's behaviour (status check is between the registry-miss /
// DB-hit branches).
//
// The DB-row OwnerUID maps from app_bot.created_by; SpaceID is only
// populated when scope == "space" (matches authAppBot:73-74).
func (ba *BotAPI) LookupAppBot(token string) (*auth.AppBotIdentity, error) {
	if token == "" {
		return nil, nil
	}
	// In-memory registry first (O(1)).
	if spec := ba.lookupAppBotRegistry(token); spec != nil {
		id := &auth.AppBotIdentity{
			BotUID: spec.UID,
			Scope:  spec.Scope,
		}
		if spec.Scope == "space" {
			id.SpaceID = spec.SpaceID
		}
		// Registry spec is intentionally minimal (UID/Scope/SpaceID); name
		// and owner come from the DB row when needed. Verify-bot handler
		// can either accept that the registry-hit path returns these
		// fields empty, or do an additional name lookup. For PR-A2 we
		// surface what we have; PR-A3 composes the full response.
		return id, nil
	}

	// Fallback to DB.
	row, err := ba.db.queryAppBotByToken(token)
	if err != nil {
		return nil, fmt.Errorf("bot_api: lookup app bot: %w", err)
	}
	if row == nil {
		return nil, nil
	}
	// Status check matches authAppBot:93: status != 1 → AUTH_BOT_UNAVAILABLE.
	if row.Status != 1 {
		return nil, auth.ErrAppBotUnpublished
	}
	id := &auth.AppBotIdentity{
		BotUID:   row.UID,
		BotName:  row.DisplayName,
		OwnerUID: row.CreatedBy,
		Scope:    row.Scope,
	}
	if row.Scope == "space" {
		id.SpaceID = row.SpaceID
	}
	return id, nil
}
