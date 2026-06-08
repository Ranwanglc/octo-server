package usersecret

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// respondErr 渲染本地化业务错误并保留真实 HTTP 状态码,然后 abort gin 链。
// 与 modules/oidc 的 respondBindError 同模式:全新端点,无 legacy 固定 400 依赖。
func respondErr(c *wkhttp.Context, code codes.Code) {
	httperr.ResponseErrorLWithStatus(c, code, nil, nil)
	c.Abort()
}

// respondErrWithDetails 同上,但携带 details(用于 resolve 歧义时带候选数等元信息;
// 候选明细通过 data 走 c.JSON,这里仅传安全的标量 details)。
func respondErrWithDetails(c *wkhttp.Context, code codes.Code, details i18n.Details) {
	httperr.ResponseErrorLWithStatus(c, code, nil, details)
	c.Abort()
}
