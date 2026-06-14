package notify

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

// respond helpers for modules/notify. The internal notification API is
// service-to-service, so every helper renders via ResponseErrorLWithStatus to
// preserve the real wire status (callers branch on HTTP status, not the D14
// fixed 400).

// respondNotifyUnauthorized renders the single anti-enumeration 401 for the
// internal auth middleware and aborts the gin chain so the protected handler
// never runs. The specific reason (token not configured vs. mismatch) is logged
// at the call site, never returned.
func respondNotifyUnauthorized(c *wkhttp.Context) {
	httperr.ResponseErrorLWithStatus(c, errcode.ErrNotifyUnauthorized, nil, nil)
	c.Abort()
}

// respondNotifyRequestInvalid renders the 400 for a malformed / incomplete
// request. An empty field is omitted so the renderer does not surface a noisy
// empty key.
func respondNotifyRequestInvalid(c *wkhttp.Context, field string) {
	details := i18n.Details{}
	if field != "" {
		details["field"] = field
	}
	httperr.ResponseErrorLWithStatus(c, errcode.ErrNotifyRequestInvalid, nil, details)
}

// respondNotifyBatchLimitExceeded surfaces the per-call batch cap.
func respondNotifyBatchLimitExceeded(c *wkhttp.Context, max int) {
	httperr.ResponseErrorLWithStatus(c, errcode.ErrNotifyBatchLimitExceeded, nil, i18n.Details{"max": max})
}

// respondNotifyDeliverFailed renders the 500 for a delivery failure. Internal=
// true → the caller MUST log the underlying err before invoking this helper.
func respondNotifyDeliverFailed(c *wkhttp.Context) {
	httperr.ResponseErrorLWithStatus(c, errcode.ErrNotifyDeliverFailed, nil, nil)
}
