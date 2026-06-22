// Package auth owns the Resource-Server-facing token contract for octo-server:
// the canonical [TokenInfo] payload stored under TokenCachePrefix+token, the
// versioned encode/decode codec, and [CacheTokenParser] which hydrates
// [wkhttp.UserInfo] for every authenticated request.
//
// # Why this package exists
//
// octo-server is the de-facto Identity Provider for the Octo ecosystem
// (octo-matter, octo-fleet, future modules). Downstream services consume
// authentication through the verify / verify-bot / verify-api-key HTTP
// endpoints — historically embedded inside modules/user — and re-export this
// same TokenInfo shape via the [octo-auth] SDK. modules/auth is the in-tree
// home for both the token codec used at issuance time and (in later PRs) the
// verify endpoints themselves, so the wire contract has one source of truth
// instead of being spread across modules/user, modules/bot_api, and
// modules/usersecret.
//
// # Dependency direction (load-bearing invariant)
//
// The dependency arrow points INTO modules/auth, never out:
//
//	modules/user        → modules/auth   (Encode at login)
//	modules/bot_api     → modules/auth   (registers LookupUserBot/LookupAppBot)
//	modules/usersecret  → modules/auth   (registers LookupAPIKey)
//	modules/auth        ↛ modules/{user,bot_api,usersecret}   (forbidden)
//
// This mirrors the OAuth2 Resource-Server / Authorization-Server split: the
// signing/issuance code (login flows, OIDC, Web3, password) stays in modules/
// user; the verification surface consumed by downstream services lives here
// and stays free of user-table or bot-table imports so the public verify
// contract can evolve independently of internal user lifecycle code.
//
// A CI depguard rule (added alongside PR-A2 when the Lookup interfaces are
// introduced) enforces this — do not weaken it by importing user / bot_api /
// usersecret implementation packages from within modules/auth.
//
// # Migration note
//
// This package supersedes pkg/auth, which now exists only as a Deprecated
// alias shim and is scheduled for removal six months after the alias was
// introduced. New code MUST import this package directly.
package auth
