package conversation_ext

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

// respond helpers for modules/conversation_ext (the /v1/follow API). These are
// authenticated front-end endpoints with legacy clients keyed to a fixed HTTP
// 400, so every helper renders via httperr.ResponseErrorL (the D14-compat
// default): the wire status stays 400 and the real semantic status travels in
// error.http_status.
//
// Internal=true codes (ErrConvExtFollowFailed / ErrConvExtUnfollowFailed /
// ErrConvExtSortUpdateFailed) carry no message on the wire; each call site keeps
// its existing f.Error(..., zap.Error(err)) log so ops can debug from logs.

// respondConvExtRequestInvalid covers the common BindJSON-failure / "X 不能为空"
// / invalid-target_type shape — one code, one optional field detail. An empty
// field is omitted so the renderer does not surface a noisy empty key.
func respondConvExtRequestInvalid(c *wkhttp.Context, field string) {
	details := i18n.Details{}
	if field != "" {
		details["field"] = field
	}
	httperr.ResponseErrorL(c, errcode.ErrConvExtRequestInvalid, nil, details)
}

// respondConvExtItemsTooMany surfaces the per-request items cap.
func respondConvExtItemsTooMany(c *wkhttp.Context, max int) {
	httperr.ResponseErrorL(c, errcode.ErrConvExtItemsTooMany, nil, i18n.Details{
		"max": max,
	})
}

// respondConvExtDuplicateItem surfaces the offending (target_type, target_id)
// pair from the UpdateSort items array.
func respondConvExtDuplicateItem(c *wkhttp.Context, targetType uint8, targetID string) {
	httperr.ResponseErrorL(c, errcode.ErrConvExtDuplicateItem, nil, i18n.Details{
		"target_type": targetType,
		"target_id":   targetID,
	})
}
