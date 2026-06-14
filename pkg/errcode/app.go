package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.app.* — modules/base/app business error codes (api.go). The
// GET /v1/apps/:app_id lookup is a small public endpoint; DefaultMessage holds
// the en-US source (D4), the zh-CN runtime translation lives in
// pkg/i18n/locales/active.zh-CN.toml.
var (
	// ErrAppNotFound covers a GetApp failure — the app row does not exist or the
	// read errored. Both collapse to one 404 so the raw SQL / "app[x]不存在！"
	// error string never reaches the wire (the legacy c.ResponseError(err) leaked
	// it). 404 (not 5xx) is intentional: this is a read-only existence probe and
	// a missing app is the dominant, non-internal case.
	ErrAppNotFound = register(codes.Code{
		ID:             "err.server.app.not_found",
		HTTPStatus:     http.StatusNotFound,
		DefaultMessage: "Application not found.",
	})
	// ErrAppDisabled covers a lookup that resolved an app whose status is
	// disabled — the app exists but is not currently serviceable.
	ErrAppDisabled = register(codes.Code{
		ID:             "err.server.app.disabled",
		HTTPStatus:     http.StatusForbidden,
		DefaultMessage: "This application is disabled.",
	})
)
