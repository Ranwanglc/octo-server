package wkhttp

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	headerOrigin = "Origin"
	headerACAO   = "Access-Control-Allow-Origin"
	headerACAC   = "Access-Control-Allow-Credentials"
	headerVary   = "Vary"
)

// ParseAllowedOrigins 解析逗号分隔的来源白名单。空项被忽略，前后空白被裁剪。
func ParseAllowedOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// addVaryOrigin 幂等追加 Vary: Origin，避免与同链路其他中间件产生重复值。
func addVaryOrigin(h http.Header) {
	for _, v := range h.Values(headerVary) {
		if v == headerOrigin {
			return
		}
	}
	h.Add(headerVary, headerOrigin)
}

// IsOriginAllowed 判断 origin 是否命中白名单。
// 支持精确匹配以及 "*.host" / "scheme://*.host" 的严格子域通配（不匹配裸主机）。
// 注意：不带 scheme 的通配（"*.host"）会同时匹配 http 与 https 来源；
// 要限制为 https，请使用 "https://*.host"。
func IsOriginAllowed(origin string, allowed []string) bool {
	if origin == "" {
		return false
	}
	for _, entry := range allowed {
		if entry == "" {
			continue
		}
		if entry == origin {
			return true
		}
		if matchWildcardOrigin(entry, origin) {
			return true
		}
	}
	return false
}

func matchWildcardOrigin(pattern, origin string) bool {
	patScheme, patHost := splitScheme(pattern)
	oriScheme, oriHost := splitScheme(origin)
	if patScheme != "" && patScheme != oriScheme {
		return false
	}
	if !strings.HasPrefix(patHost, "*.") {
		return false
	}
	suffix := patHost[1:]
	return len(oriHost) > len(suffix) && strings.HasSuffix(oriHost, suffix)
}

func splitScheme(s string) (scheme, host string) {
	if i := strings.Index(s, "://"); i >= 0 {
		return s[:i], s[i+3:]
	}
	return "", s
}

// SecureCORSOverrideMiddleware 返回一个 gin 中间件，用于在上游 CORS 中间件
// 已写入响应头之后，按白名单重写/剥离 Access-Control-Allow-Origin 与
// Access-Control-Allow-Credentials，并追加 Vary: Origin。
//
// 行为：
//   - 请求无 Origin：删除两个 CORS 头，保持同源语义。
//   - Origin 命中白名单：反射该 Origin，Allow-Credentials: true，Vary: Origin。
//   - Origin 未命中：删除两个 CORS 头，仅追加 Vary: Origin。
//
// 注意：预检（OPTIONS）通常被上游中间件提前 Abort，不会进入本中间件。
// 当上游发出的是规范不合法的组合（如 "*" + credentials=true），浏览器本身
// 会拒绝跨域 credentialed 预检，因此攻击路径依然被封堵。
func SecureCORSOverrideMiddleware(allowed []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get(headerOrigin)
		h := c.Writer.Header()
		if origin == "" {
			h.Del(headerACAO)
			h.Del(headerACAC)
			c.Next()
			return
		}
		addVaryOrigin(h)
		if IsOriginAllowed(origin, allowed) {
			h.Set(headerACAO, origin)
			h.Set(headerACAC, "true")
		} else {
			h.Del(headerACAO)
			h.Del(headerACAC)
		}
		c.Next()
	}
}
