package auth

// Lookup interfaces let modules/auth's HTTP verify handlers resolve a Bot
// token or API Key into a typed identity without importing the
// implementation packages (modules/bot_api, modules/usersecret). The
// interfaces are declared HERE, on the consumer side, per the Go-idiomatic
// "consumer-defined interfaces" pattern; the implementer packages import
// modules/auth and satisfy the interface (one-way dependency:
// bot_api/usersecret → auth, never the reverse).
//
// See doc.go for the dependency-direction invariant and
// imports_test.go for the in-tree guard that enforces it.

// UserBotIdentity is the result of a successful User Bot
// (`bf_` token, robot table) lookup. Fields map directly onto the
// SDK contract's verify-bot response shape for `bot_kind: "user"`
// (see plan §4.1.2). OwnerName and Language are NOT present here —
// resolving those requires a join with the user table; PR-A3 will
// compose this lookup with a separate user-name / user-language
// hydration step at the verify handler.
type UserBotIdentity struct {
	BotUID   string
	BotName  string
	OwnerUID string
}

// AppBotIdentity is the result of a successful App Bot (`app_` token,
// app_bot table) lookup. The Scope / SpaceID pair drives Space
// fail-closed checks downstream — scope="space" means the bot is
// bound to a single SpaceID and verify responses must propagate
// that binding so the SDK can enforce X-Space-Id matching at the
// caller side (plan §6.3 RequireSpaceMember).
type AppBotIdentity struct {
	BotUID   string
	BotName  string
	OwnerUID string
	Scope    string // "platform" | "space"
	SpaceID  string // populated only when Scope == "space"
}

// APIKeyIdentity is the result of a successful `uk_` API Key lookup.
// SpaceID and OwnedBotsBySpace may be empty when the key has no
// explicit context binding. Fields match plan §4.1.3 verify-api-key
// response shape.
type APIKeyIdentity struct {
	UID              string
	KeyID            string
	SpaceID          string              // optional
	OwnedBotsBySpace map[string][]string // optional, keyed by space_id
}

// BotLookup is the interface modules/auth uses to resolve a Bot
// token. Implementations live in modules/bot_api.
//
// Both methods follow the same contract: a (nil, nil) return is the
// "no match" signal — i.e. the token is well-formed but not recognised
// as a current bot. A non-nil error indicates an infrastructure
// failure (DB / cache unreachable) that should NOT be reported to
// the client as "token invalid"; the verify handler must surface it
// as `AUTH_UPSTREAM_UNAVAILABLE` (per plan §4.2).
//
// LookupAppBot is the one method allowed to return the
// [ErrAppBotUnpublished] sentinel: bot exists in the DB but
// `status != 1`. PR-A3's verify handler will map that to
// `AUTH_BOT_UNAVAILABLE` (503).
type BotLookup interface {
	LookupUserBot(token string) (*UserBotIdentity, error)
	LookupAppBot(token string) (*AppBotIdentity, error)
}

// APIKeyLookup is the interface modules/auth uses to resolve a `uk_`
// API Key. Implementation lives in modules/usersecret.
//
// Same contract as BotLookup: (nil, nil) means "no match"; non-nil
// error means infrastructure failure (NOT "invalid key").
//
// NOTE for Stage A: the current modules/usersecret implementation is
// a stub that returns (nil, nil) for any input. Real `uk_` API Key
// storage does not yet exist in octo-server; the interface is in
// place so PR-A4's verify-api-key handler can be wired correctly
// and so a future PR that adds real key storage only needs to
// replace the stub body, not the contract.
type APIKeyLookup interface {
	LookupAPIKey(apiKey string) (*APIKeyIdentity, error)
}
