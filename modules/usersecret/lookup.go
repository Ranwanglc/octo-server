package usersecret

import (
	"github.com/Mininglamp-OSS/octo-server/modules/auth"
)

// Compile-time assertion: API satisfies auth.APIKeyLookup.
var _ auth.APIKeyLookup = (*API)(nil)

// LookupAPIKey resolves a `uk_` API Key to its owner identity.
// Implements [auth.APIKeyLookup.LookupAPIKey].
//
// STUB IMPLEMENTATION (Stage A scope decision).
//
// Real `uk_` API Key storage does not yet exist in octo-server. The
// existing user_secret_alias table stores user-owned third-party
// secrets (encrypted blobs) accessed by the resolve endpoint via
// `bf_` bot token authentication — that is a different concept from
// "daemon API keys" that fleet's daemon flow expects. Fleet's
// existing call to /v1/auth/verify-api-key is a ghost endpoint that
// has always 404'd; PR-A4 will make it a real endpoint that
// returns AUTH_TOKEN_INVALID (401) until the storage layer ships.
//
// Contract: (nil, nil) is the documented "no match" signal — PR-A4's
// verify-api-key handler will map that to AUTH_TOKEN_INVALID (401).
//
// TODO(auth-api-key-storage): replace this stub when the user_api_key
// table / generation / encryption-at-rest design lands. Tracked under
// the Stage A epic (octo-server #428).
func (a *API) LookupAPIKey(apiKey string) (*auth.APIKeyIdentity, error) {
	_ = apiKey // stub: real lookup will key into user_api_key once the table exists
	return nil, nil
}
