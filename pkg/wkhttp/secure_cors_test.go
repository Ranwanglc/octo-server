package wkhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestParseAllowedOrigins(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "https://a.com", []string{"https://a.com"}},
		{"multiple", "https://a.com,https://b.com", []string{"https://a.com", "https://b.com"}},
		{"trim spaces", "  https://a.com  ,  https://b.com  ", []string{"https://a.com", "https://b.com"}},
		{"skip empty", "https://a.com,,https://b.com,", []string{"https://a.com", "https://b.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ParseAllowedOrigins(tc.in))
		})
	}
}

func TestIsOriginAllowed(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		allowed []string
		want    bool
	}{
		{"empty origin", "", []string{"https://a.com"}, false},
		{"empty whitelist", "https://a.com", nil, false},
		{"exact match", "https://a.com", []string{"https://a.com"}, true},
		{"exact miss", "https://evil.com", []string{"https://a.com"}, false},
		{"wildcard sub", "https://api.a.com", []string{"https://*.a.com"}, true},
		{"wildcard nested sub", "https://v1.api.a.com", []string{"https://*.a.com"}, true},
		{"wildcard apex rejected", "https://a.com", []string{"https://*.a.com"}, false},
		{"wildcard scheme mismatch", "http://api.a.com", []string{"https://*.a.com"}, false},
		{"wildcard suffix spoof rejected", "https://xa.com", []string{"https://*.a.com"}, false},
		{"wildcard evil suffix rejected", "https://a.com.evil.com", []string{"https://*.a.com"}, false},
		{"multi entries", "https://b.com", []string{"https://a.com", "https://b.com"}, true},
		{"wildcard host only no scheme", "https://api.a.com", []string{"*.a.com"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsOriginAllowed(tc.origin, tc.allowed))
		})
	}
}

func TestSecureCORSOverrideMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 模拟 lib 侧 CORS：无条件写死 "*" + Credentials:true（与 dmwork-lib 当前行为一致）。
	upstreamLibCORS := func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Next()
	}

	newServer := func(allowed []string) *gin.Engine {
		r := gin.New()
		r.Use(upstreamLibCORS)
		r.Use(SecureCORSOverrideMiddleware(allowed))
		r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		return r
	}

	t.Run("origin absent strips ACAO and ACAC", func(t *testing.T) {
		r := newServer([]string{"https://a.com"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		r.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	})

	t.Run("allowed origin reflects with credentials and Vary", func(t *testing.T) {
		r := newServer([]string{"https://a.com"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("Origin", "https://a.com")
		r.ServeHTTP(w, req)
		assert.Equal(t, "https://a.com", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, w.Header().Values("Vary"), "Origin")
	})

	t.Run("disallowed origin strips ACAO ACAC and sets Vary", func(t *testing.T) {
		r := newServer([]string{"https://a.com"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("Origin", "https://evil.com")
		r.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, w.Header().Values("Vary"), "Origin")
	})

	t.Run("empty whitelist never reflects but still sets Vary", func(t *testing.T) {
		r := newServer(nil)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("Origin", "https://a.com")
		r.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, w.Header().Values("Vary"), "Origin")
	})

	t.Run("Vary Origin is not duplicated when upstream already set it", func(t *testing.T) {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Writer.Header().Add("Vary", "Origin")
			c.Next()
		})
		r.Use(SecureCORSOverrideMiddleware([]string{"https://a.com"}))
		r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("Origin", "https://a.com")
		r.ServeHTTP(w, req)
		vary := w.Header().Values("Vary")
		count := 0
		for _, v := range vary {
			if v == "Origin" {
				count++
			}
		}
		assert.Equal(t, 1, count, "Vary: Origin should appear exactly once")
	})

	t.Run("origin absent with non empty whitelist has no CORS headers", func(t *testing.T) {
		r := newServer([]string{"https://a.com"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		r.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
		assert.NotContains(t, w.Header().Values("Vary"), "Origin")
	})

	t.Run("wildcard whitelist match reflects subdomain", func(t *testing.T) {
		r := newServer([]string{"https://*.a.com"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("Origin", "https://api.a.com")
		r.ServeHTTP(w, req)
		assert.Equal(t, "https://api.a.com", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("body is not affected", func(t *testing.T) {
		r := newServer([]string{"https://a.com"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("Origin", "https://a.com")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "ok", w.Body.String())
	})
}
