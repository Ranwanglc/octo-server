package auth

import (
	"errors"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"go.uber.org/zap"

	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
)

// API hosts the HTTP handlers for /v1/auth/verify*. The Service does the
// business logic; this layer binds JSON, maps sentinel errors to
// localised error codes (via httperr.ResponseErrorL per the repo's i18n
// rule — see CLAUDE.md "Error Handling & i18n"), and writes the success
// response.
type API struct {
	svc *Service
	log *zap.Logger
}

// NewAPI constructs the HTTP API binding to the given Service.
func NewAPI(svc *Service, log *zap.Logger) *API {
	if svc == nil {
		panic("auth: NewAPI requires non-nil Service")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &API{svc: svc, log: log}
}

// verifyUserHTTP handles POST /v1/auth/verify.
func (a *API) verifyUserHTTP(c *wkhttp.Context) {
	var req VerifyUserReq
	if err := c.BindJSON(&req); err != nil {
		a.log.Warn("auth: verify request body malformed", zap.Error(err))
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAuthTokenInvalid, nil, nil)
		return
	}
	resp, err := a.svc.VerifyUser(c.Request.Context(), req)
	if err != nil {
		a.handleServiceError(c, "verify", err)
		return
	}
	c.Response(resp)
}

// verifyBotHTTP handles POST /v1/auth/verify-bot.
func (a *API) verifyBotHTTP(c *wkhttp.Context) {
	var req VerifyBotReq
	if err := c.BindJSON(&req); err != nil {
		a.log.Warn("auth: verify-bot request body malformed", zap.Error(err))
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAuthTokenInvalid, nil, nil)
		return
	}
	resp, err := a.svc.VerifyBot(c.Request.Context(), req)
	if err != nil {
		a.handleServiceError(c, "verify-bot", err)
		return
	}
	c.Response(resp)
}

// handleServiceError maps a sentinel error from the Service to the right
// errcode + log line. Anti-enumeration: all "token bad" reasons collapse
// to a single 401 (ErrAuthTokenInvalid) at the wire; the specific reason
// is only in the log.
//
// Uses ResponseErrorLWithStatus (not ResponseErrorL) so the **HTTP
// transport** status matches the semantic status — Gateway / SDK callers
// branch on the HTTP status line (401 → re-auth, 503 → retry-with-backoff,
// 500 → fatal) per the original modules/user/api.go contract. The legacy
// handlers used raw AbortWithStatusJSON(401,...) for the same reason; the
// i18n-envelope migration must preserve that wire-status contract
// (Jerry-Xin / yujiawei review on #431).
func (a *API) handleServiceError(c *wkhttp.Context, endpoint string, err error) {
	switch {
	case errors.Is(err, ErrInvalidUserToken), errors.Is(err, ErrInvalidBotToken):
		// Single anti-enumeration code; log the specific sentinel so
		// operators can still distinguish in audit.
		a.log.Info("auth: rejected", zap.String("endpoint", endpoint), zap.Error(err))
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAuthTokenInvalid, nil, nil)
	case errors.Is(err, ErrBotUnavailable):
		a.log.Info("auth: bot unpublished", zap.String("endpoint", endpoint))
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAuthBotUnpublished, nil, nil)
	default:
		// Treat anything else (incl. wrapped ErrUpstreamFailure) as 500.
		a.log.Error("auth: upstream failure", zap.String("endpoint", endpoint), zap.Error(err))
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAuthUpstreamFailed, nil, nil)
	}
}
