package app

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
)

// respond helpers for modules/base/app. The GET /v1/apps/:app_id handler has
// exactly two failure shapes; both render the localized envelope via
// httperr.ResponseErrorL (pinned 400 wire status, real status in
// error.http_status) so the legacy raw c.ResponseError(err) string never leaks.

// respondAppNotFound renders the 404 for a missing / unreadable app. The
// underlying GetApp error (raw SQL or the "app[x]不存在！" sentinel) is
// intentionally not surfaced.
func respondAppNotFound(c *wkhttp.Context) {
	httperr.ResponseErrorL(c, errcode.ErrAppNotFound, nil, nil)
}

// respondAppDisabled renders the 403 for an app that exists but is disabled.
func respondAppDisabled(c *wkhttp.Context) {
	httperr.ResponseErrorL(c, errcode.ErrAppDisabled, nil, nil)
}
