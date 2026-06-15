package conversation_ext

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

// respond helpers for modules/conversation_ext (the /v1/follow API). Most
// helpers render via httperr.ResponseErrorL (the D14-compat default): the wire
// status stays 400 and the real semantic status travels in error.http_status.
//
// EXCEPTION — respondConvExtFollowForbidden renders via
// ResponseErrorLWithStatus to PRESERVE the real HTTP 403 on the wire. The
// FollowChannel / FollowThread permission guards have an established 403 contract
// (TestFollow_FollowChannel_Forbidden_Returns403 /
// TestFollow_FollowThread_Forbidden_Returns403): clients branch on the 403 to
// take the "no access" path instead of the generic-400 retry path, so this must
// not collapse to 400.
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

// respondConvExtFollowForbidden renders the follow permission denial, preserving
// the real HTTP 403 on the wire (see the EXCEPTION note above). The specific
// reason is logged at the call site, never surfaced (anti-enumeration).
func respondConvExtFollowForbidden(c *wkhttp.Context) {
	httperr.ResponseErrorLWithStatus(c, errcode.ErrConvExtFollowForbidden, nil, nil)
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
