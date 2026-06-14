package channel

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

// respond helpers for modules/channel. Most migrated sites call
// httperr.ResponseErrorL(c, errcode.ErrChannelXxx, nil, nil) directly; the
// helper below exists only for the validation shape that carries a Detail field
// (so the SafeDetailKeys contract stays in one place).
//
// Internal=true codes (ErrChannelQueryFailed / ErrChannelStoreFailed /
// ErrChannelSendFailed) are intentionally NOT wrapped here: each call site keeps
// its existing ch.Error(..., zap.Error(err)) log so ops can debug from logs, and
// the renderer carries no message on the wire.

// respondChannelRequestInvalid covers the common "X 不能为空" / invalid-param /
// BindJSON-failure shape — one code, one optional field detail. An empty field
// is omitted so the renderer does not surface a noisy empty key to clients.
func respondChannelRequestInvalid(c *wkhttp.Context, field string) {
	details := i18n.Details{}
	if field != "" {
		details["field"] = field
	}
	httperr.ResponseErrorL(c, errcode.ErrChannelRequestInvalid, nil, details)
}
