package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.notify.* — modules/notify internal notification API error codes
// (api.go). These endpoints (/v1/internal/notify[/batch]) are service-to-service
// (X-Internal-Token auth), not the dmwork front-end, so the helpers render via
// httperr.ResponseErrorLWithStatus to PRESERVE the real wire status (the calling
// services branch on HTTP status, never on the D14 fixed 400). DefaultMessage
// holds the en-US source (D4); zh-CN lives in pkg/i18n/locales/active.zh-CN.toml.
var (
	// ErrNotifyUnauthorized is the SINGLE anti-enumeration 401 for the internal
	// auth middleware: a missing/misconfigured server token and a wrong caller
	// token both collapse here so a caller cannot tell the two apart. The
	// "token not configured" server-side misconfiguration is logged distinctly.
	ErrNotifyUnauthorized = register(codes.Code{
		ID:             "err.server.notify.unauthorized",
		HTTPStatus:     http.StatusUnauthorized,
		DefaultMessage: "Unauthorized.",
	})
	// ErrNotifyRequestInvalid covers a malformed request body (BindJSON failure)
	// and the empty-notifications batch case. The offending field is surfaced via
	// Details when the caller can identify it.
	ErrNotifyRequestInvalid = register(codes.Code{
		ID:             "err.server.notify.request_invalid",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid request.",
		SafeDetailKeys: []string{"field"},
	})
	// ErrNotifyBatchLimitExceeded covers a batch request over the per-call cap.
	// The cap is surfaced so the caller can chunk without hard-coding the limit.
	ErrNotifyBatchLimitExceeded = register(codes.Code{
		ID:             "err.server.notify.batch_limit_exceeded",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "The batch size exceeds the maximum allowed.",
		SafeDetailKeys: []string{"max"},
	})
	// ErrNotifyDeliverFailed covers a delivery-path failure (member verification,
	// bot provisioning, or send dispatch). Internal=true → log the underlying err
	// before responding; the wire carries no message.
	ErrNotifyDeliverFailed = register(codes.Code{
		ID:             "err.server.notify.deliver_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to deliver the notification.",
		Internal:       true,
	})
)
