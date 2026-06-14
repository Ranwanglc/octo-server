package file

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

// respond helpers for modules/file. Most migrated sites call
// httperr.ResponseErrorL(c, errcode.ErrFileXxx, nil, nil) directly; the helpers
// below exist only for the shapes that carry a Detail field, so the
// SafeDetailKeys contract stays in one place.
//
// Internal=true codes (ErrFileReadFailed / ErrFileProcessFailed /
// ErrFileImageComposeFailed / ErrFileUploadFailed / ErrFilePresignFailed) are
// intentionally NOT wrapped here: each call site keeps its existing
// f.Error(..., zap.Error(err)) log so ops can debug from logs, and the renderer
// carries no message on the wire.

// respondFileRequestInvalid covers the common BindJSON-failure / "X 不能为空" /
// invalid-param shape — one code, one optional field detail. An empty field is
// omitted so the renderer does not surface a noisy empty key to clients.
func respondFileRequestInvalid(c *wkhttp.Context, field string) {
	details := i18n.Details{}
	if field != "" {
		details["field"] = field
	}
	httperr.ResponseErrorL(c, errcode.ErrFileRequestInvalid, nil, details)
}

// respondFileImageCountExceeded surfaces the compose-image count cap.
func respondFileImageCountExceeded(c *wkhttp.Context, max int) {
	httperr.ResponseErrorL(c, errcode.ErrFileImageCountExceeded, nil, i18n.Details{
		"max": max,
	})
}

// respondFileTooLarge surfaces the upload size cap (in MB) so the client can
// render a localized hint without hard-coding the limit.
func respondFileTooLarge(c *wkhttp.Context, maxMB int64) {
	httperr.ResponseErrorL(c, errcode.ErrFileTooLarge, nil, i18n.Details{
		"max_mb": maxMB,
	})
}

// respondFileTypeUnsupported surfaces the rejected extension (when known). An
// empty ext is omitted so the renderer does not surface a noisy empty key.
func respondFileTypeUnsupported(c *wkhttp.Context, ext string) {
	details := i18n.Details{}
	if ext != "" {
		details["ext"] = ext
	}
	httperr.ResponseErrorL(c, errcode.ErrFileTypeUnsupported, nil, details)
}
