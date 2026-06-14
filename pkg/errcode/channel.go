package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.channel.* — modules/channel business error codes (api.go /
// api_storyline.go). DefaultMessage holds the en-US source (D4); the zh-CN
// runtime translation lives in pkg/i18n/locales/active.zh-CN.toml. Internal=true
// codes never surface their message on the wire — callers MUST log the
// underlying err with full context (zap.Error) before responding.
var (
	// ---- validation (400) ----------------------------------------------------

	// ErrChannelRequestInvalid is the catch-all for missing/malformed request
	// input ("频道Id不能为空", "频道ID不合法", BindJSON failure / "参数错误",
	// "with_user 过滤器缺少目标用户", target user not a group member). The
	// offending field is surfaced via Details when the caller can identify it.
	ErrChannelRequestInvalid = register(codes.Code{
		ID:             "err.server.channel.request_invalid",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid request.",
		SafeDetailKeys: []string{"field"},
	})
	// ErrChannelStorylineGroupOnly covers the storyline-only-for-group guard
	// ("故事线功能仅支持群聊").
	ErrChannelStorylineGroupOnly = register(codes.Code{
		ID:             "err.server.channel.storyline_group_only",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Storyline is only available for group chats.",
	})

	// ---- permission / authorization (403) ------------------------------------

	// ErrChannelForbidden collapses the channel access/permission guards
	// ("没有权限操作此频道", "没有权限设置", "非群成员无法查询群状态",
	// "非群成员无法查询故事线") into a single forbidden code — no enumeration of
	// which specific guard failed.
	ErrChannelForbidden = register(codes.Code{
		ID:             "err.server.channel.forbidden",
		HTTPStatus:     http.StatusForbidden,
		DefaultMessage: "You do not have permission to operate this channel.",
	})

	// ---- not found (404) -----------------------------------------------------

	// ErrChannelNotFound covers a missing channel ("频道不存在！").
	ErrChannelNotFound = register(codes.Code{
		ID:             "err.server.channel.not_found",
		HTTPStatus:     http.StatusNotFound,
		DefaultMessage: "Channel not found.",
	})

	// ---- internal (500, Internal=true) ---------------------------------------

	// ErrChannelQueryFailed covers read-path failures (channel datasource lookup,
	// friend-relation / group creator-or-manager / group-member / online-count /
	// channel-setting / channel-max-seq SELECTs). Log the underlying err before
	// responding.
	ErrChannelQueryFailed = register(codes.Code{
		ID:             "err.server.channel.query_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to query channel data.",
		Internal:       true,
	})
	// ErrChannelStoreFailed covers mutation-path failures (set channel offset
	// message seq, set message auto-delete). Log the underlying err before
	// responding.
	ErrChannelStoreFailed = register(codes.Code{
		ID:             "err.server.channel.store_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to update channel data.",
		Internal:       true,
	})
	// ErrChannelSendFailed covers WuKongIM dispatch / sync failures (send
	// clear-channel CMD, send message, sync channel messages). Log the underlying
	// err before responding.
	ErrChannelSendFailed = register(codes.Code{
		ID:             "err.server.channel.send_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to send the message.",
		Internal:       true,
	})
)
