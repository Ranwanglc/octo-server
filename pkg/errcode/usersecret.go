package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.usersecret.* — modules/usersecret 用户外部密钥别名表 CRUD + resolve
// 的业务错误码(YUJ-3538)。
//
// 这些端点是全新功能,无 legacy client 依赖固定 400,因此全部通过
// httperr.ResponseErrorLWithStatus 渲染,保留真实 HTTP 状态码
// (400/401/404/409/422/500),让 octo-web / channel 插件可直接按 wire status 分支。
//
// 安全约束:任何错误响应都不回显明文/密文。decrypt 失败统一归 5xx 内部错误
// (Internal=true),具体原因仅 zap 记录,不上 wire。
var (
	// ---- validation (400) ---------------------------------------------------

	// ErrUserSecretRequestInvalid 入参缺失/格式非法:BindJSON 失败、display_name
	// 或 key 为空、超长、kind 非法等。
	ErrUserSecretRequestInvalid = register(codes.Code{
		ID:             "err.server.usersecret.request_invalid",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid request.",
		SafeDetailKeys: []string{"field"},
	})

	// ---- auth (401) ---------------------------------------------------------

	// ErrUserSecretUnauthorized resolve 鉴权失败:bot 凭证无效 / 无法认定代表合法
	// 本用户的 channel 插件在取。anti-enumeration:不区分「凭证错」与「无此 owner」。
	ErrUserSecretUnauthorized = register(codes.Code{
		ID:             "err.server.usersecret.unauthorized",
		HTTPStatus:     http.StatusUnauthorized,
		DefaultMessage: "Authentication failed.",
	})

	// ---- not found (404) ----------------------------------------------------

	// ErrUserSecretNotFound 按 secret_id/display_name 未解到任何 key(CRUD 的目标
	// 不存在,或 resolve 零命中)。
	ErrUserSecretNotFound = register(codes.Code{
		ID:             "err.server.usersecret.not_found",
		HTTPStatus:     http.StatusNotFound,
		DefaultMessage: "Secret not found.",
	})

	// ---- conflict (409) -----------------------------------------------------

	// ErrUserSecretDuplicateName create/rename 时归一化别名撞已有别名,提示换名。
	ErrUserSecretDuplicateName = register(codes.Code{
		ID:             "err.server.usersecret.duplicate_name",
		HTTPStatus:     http.StatusConflict,
		DefaultMessage: "A secret with this name already exists. Please choose another name.",
	})

	// ---- ambiguous (422) ----------------------------------------------------

	// ErrUserSecretAmbiguous resolve 匹配到多个候选,需上层消歧。响应体携带候选
	// 列表(脱敏,不含明文),由 handler 以 details 形式走统一 i18n 错误信封返回
	// (error.details.candidates),保留 error.http_status 与本地化 message。
	ErrUserSecretAmbiguous = register(codes.Code{
		ID:             "err.server.usersecret.ambiguous",
		HTTPStatus:     http.StatusUnprocessableEntity,
		DefaultMessage: "The name matches multiple secrets. Please disambiguate.",
		SafeDetailKeys: []string{"candidates"},
	})

	// ---- internal (500) -----------------------------------------------------

	// ErrUserSecretResolveFailed 解引用失败:密文解密/认证失败等内部异常。
	// Internal=true,wire 上只暴露通用内部错误文案,真实原因仅 zap。
	ErrUserSecretResolveFailed = register(codes.Code{
		ID:             "err.server.usersecret.resolve_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to resolve the secret.",
		Internal:       true,
	})
)
