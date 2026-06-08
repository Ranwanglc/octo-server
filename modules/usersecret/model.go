package usersecret

import (
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/db"
)

// Kind 别名分类,仅用于过滤展示,不参与鉴权/解析。
const (
	KindLLM      = "llm"
	KindExternal = "external"
)

// aliasModel 对应 user_secret_alias 表。CipherText/Masked 永不出现在任何 API 响应。
type aliasModel struct {
	SecretID        string
	OwnerUID        string
	DisplayName     string
	DisplayNameNorm string
	Kind            string
	CipherText      []byte
	Masked          string
	LastUsedAt      *time.Time
	db.BaseModel
}

// resolveAuditModel 对应 user_secret_resolve_audit 表。
type resolveAuditModel struct {
	OwnerUID   string
	CallerKind string
	CallerID   string
	Query      string
	SecretID   string
	Result     string
	Candidates int
	IP         string
	db.BaseModel
}

// resolve 审计结果枚举。
const (
	resultOK             = "ok"
	resultNotFound       = "not_found"
	resultAmbiguous      = "ambiguous"
	resultDecryptFail    = "decrypt_fail"
	resultUnauthorized   = "unauthorized"
	resultRequestInvalid = "request_invalid" // 已鉴权 caller 发了坏请求(空/坏 body)
	resultInternalError  = "internal_error"  // DB/基础设施异常,区别于真实 not_found(P1.5)
)
