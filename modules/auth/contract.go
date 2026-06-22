package auth

// HTTP wire contracts for /v1/auth/verify* endpoints. These DTOs are the
// in-tree mirror of github.com/Mininglamp-OSS/octo-auth/sdk-go's
// contract/auth-v1.yaml; field naming and JSON tags must stay in sync
// with the SDK or callers will silently parse stale shapes.
//
// Backward compatibility: every field that the pre-modules/auth handlers
// returned (uid/name/role/owned_bots for verify; bot_uid/bot_name/
// owner_uid/owner_name/space_id for verify-bot) is preserved at the
// SAME wire location with the SAME JSON tag. New fields are added
// additively (schema_version, bot_kind, scope). Old SDKs / existing
// matter/fleet callers ignore unknown fields and continue to work.

// VerifyUserReq is the request body for POST /v1/auth/verify.
type VerifyUserReq struct {
	Token string `json:"token"`
}

// OwnedBot mirrors the existing inline shape on the verify response.
type OwnedBot struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// VerifyUserResp is the response body for POST /v1/auth/verify. The
// legacy fields (UID/Name/Role/OwnedBots) are preserved exactly; new
// fields (SchemaVersion, Kind) are additive.
type VerifyUserResp struct {
	SchemaVersion int        `json:"schema_version"`
	Kind          string     `json:"kind"` // always "user" for this endpoint
	UID           string     `json:"uid"`
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	OwnedBots     []OwnedBot `json:"owned_bots"`
}

// VerifyBotReq is the request body for POST /v1/auth/verify-bot.
type VerifyBotReq struct {
	BotToken string `json:"bot_token"`
}

// VerifyBotResp is the response body for POST /v1/auth/verify-bot.
//
// BotKind is "user" for User Bot tokens (bf_ prefix or legacy unprefixed
// hitting the robot table) and "app" for App Bot tokens (app_ prefix
// hitting the app_bot table).
//
// Scope and SpaceID are populated only for App Bots; Scope="space" means
// SpaceID is the binding space, Scope="platform" means cross-space access
// is allowed. For User Bots both are empty (the legacy handler's
// space_id field — the first active space_member row — is preserved
// at the same JSON location for User Bots but its semantics are
// "current visible space" rather than "binding").
type VerifyBotResp struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"` // "bot"
	BotUID        string `json:"bot_uid"`
	BotName       string `json:"bot_name"`
	BotKind       string `json:"bot_kind"` // "user" | "app"
	OwnerUID      string `json:"owner_uid"`
	OwnerName     string `json:"owner_name"`
	Scope         string `json:"scope,omitempty"`
	SpaceID       string `json:"space_id"`
}

// VerifyAPIKeyReq is the request body for POST /v1/auth/verify-api-key.
// Daemon-side callers (e.g. octo-fleet runtime daemon) send their `uk_`
// API key here to resolve their owner identity. Until real `uk_` storage
// lands, every call resolves to "no match" → 401 ErrAuthTokenInvalid; the
// contract is in place so daemons stop seeing a 404 ghost endpoint.
type VerifyAPIKeyReq struct {
	APIKey string `json:"api_key"`
}

// VerifyAPIKeyResp is the response body for POST /v1/auth/verify-api-key.
// SpaceID and OwnedBotsBySpace are optional — empty when the key carries
// no context binding (the only path that exists today since the lookup
// is a stub).
type VerifyAPIKeyResp struct {
	SchemaVersion    int                 `json:"schema_version"`
	Kind             string              `json:"kind"` // "apikey"
	UID              string              `json:"uid"`
	KeyID            string              `json:"key_id"`
	SpaceID          string              `json:"space_id,omitempty"`
	OwnedBotsBySpace map[string][]string `json:"owned_bots_by_space,omitempty"`
}
