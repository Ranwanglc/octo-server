// Package displayname 统一解析用户对外展示名（issue #344）。
//
// 背景：user.name 在 RegisterUserMustCompleteInfoOn==1 时允许为空（本地注册与
// OIDC 建号同走 createUserWithRespAndTx 的空名分支，见 modules/user/api.go），
// 列表类端点直接渲染 user.name 会出现空名。所有"他人视图"的读路径用同一条
// 解析链兜底：
//
//	name（用户自取名）→ real_name（仅已实名用户）→ "用户"+uid 后 4 位（占位名）
//
// 适用边界：
//   - 只用于响应组装，不回写 user.name —— 写时不变量是独立决策（issue #344 Phase 2）；
//   - 自我视图（/v1/user/current）不适用 —— name 为空是客户端"完善资料"流程的信号；
//   - 把 real_name 下发/展示给同空间、同群成员沿用 modules/group YUJ-413 的既有口径。
//
// 占位名固定中文（产品主用户群）：响应数据层目前没有按请求语言渲染的先例
// （pkg/i18n 只覆盖错误信息与邮件模板），引入会把语言协商拉进所有列表热路径。
package displayname

import "strings"

const (
	placeholderPrefix    = "用户"
	placeholderSuffixLen = 4
)

// Resolve 返回用户的对外展示名，保证非空。
//
// name 非空时原样返回（不做 trim 改写）；空白串视为空。realName 仅在已实名
// 用户处非空，未实名调用方应传 ""。占位名后缀取 uid 末 4 位（uid 为 ASCII
// 短编码，按字节切安全），uid 不足 4 位时整个 uid 作为后缀。
func Resolve(name, realName, uid string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	if rn := strings.TrimSpace(realName); rn != "" {
		return rn
	}
	suffix := uid
	if len(uid) > placeholderSuffixLen {
		suffix = uid[len(uid)-placeholderSuffixLen:]
	}
	return placeholderPrefix + suffix
}
