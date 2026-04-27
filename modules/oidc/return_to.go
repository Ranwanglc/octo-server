package oidc

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrReturnToRejected return_to 校验失败(空、非法 URL、host 不在白名单)
var ErrReturnToRejected = errors.New("oidc: return_to rejected")

// ValidateReturnTo 校验 callback 完成后跳转地址是否合法。
//
// 规则(防开放重定向):
//  1. 空字符串 -> 视为合法,调用方应回退到默认页(返 "" 表示无跳转)
//  2. 相对路径(以 / 开头,不含 //)-> 合法,host 由前端域名决定
//  3. 绝对 URL -> 必须是 http/https,且 host 命中白名单
//
// 任何其他形态(scheme-less // 形式、javascript:、data: 等)一律拒绝。
func ValidateReturnTo(raw string, allowedHosts []string) (string, error) {
	if raw == "" {
		return "", nil
	}

	// 相对路径(以单斜杠开头但不是 // 协议相对路径)
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return raw, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: parse: %v", ErrReturnToRejected, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: scheme %q", ErrReturnToRejected, u.Scheme)
	}
	host := u.Hostname() // 剥端口,allowlist 用 bare hostname 比对
	if host == "" {
		return "", fmt.Errorf("%w: empty host", ErrReturnToRejected)
	}
	for _, h := range allowedHosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return raw, nil
		}
	}
	return "", fmt.Errorf("%w: host %q not in whitelist", ErrReturnToRejected, host)
}
