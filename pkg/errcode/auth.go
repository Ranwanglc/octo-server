package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// Auth module error codes. Used by modules/auth's HTTP verify endpoints
// (/v1/auth/verify, /v1/auth/verify-bot, /v1/auth/verify-api-key) and the
// SDK middleware in github.com/Mininglamp-OSS/octo-auth/sdk-go.
//
// Anti-enumeration: all "token missing / token expired / token decode
// failed / no such bot / wrong token kind" outcomes collapse to a SINGLE
// 401 code (ErrAuthTokenInvalid). The specific reason goes to zap logs
// only. ErrAuthBotUnpublished is the one exception — a 503 distinguishing
// "bot exists but currently unpublished" from "no such token" — because
// the bot owner can fix it and the client needs to know that's the case.
var (
	// ErrAuthTokenInvalid is the single 401 that all verify failures collapse to:
	// missing token, malformed payload, cache miss, decode error, no matching
	// bot — all surface here. The specific reason is in the logs.
	ErrAuthTokenInvalid = register(codes.Code{
		ID:             "err.server.auth.token_invalid",
		HTTPStatus:     http.StatusUnauthorized,
		DefaultMessage: "Invalid or expired token.",
	})

	// ErrAuthBotUnpublished signals that an App Bot exists but its status is
	// not 1 (published). 503 because it's a transient state the bot owner can
	// fix; the client should retry after publication, not assume the token
	// is bad.
	ErrAuthBotUnpublished = register(codes.Code{
		ID:             "err.server.auth.bot_unavailable",
		HTTPStatus:     http.StatusServiceUnavailable,
		DefaultMessage: "Bot is currently unavailable.",
		Internal:       false,
	})

	// ErrAuthUpstreamFailed signals that a downstream dependency (DB, cache)
	// failed during verification. Internal=true so the renderer hides the
	// concrete cause from the wire; operators get the underlying error via
	// zap.Error in the handler.
	ErrAuthUpstreamFailed = register(codes.Code{
		ID:             "err.server.auth.upstream_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Authentication service temporarily unavailable.",
		Internal:       true,
	})
)
