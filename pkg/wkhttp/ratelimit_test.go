package wkhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"testing"
	"time"

	libwkhttp "github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	t.Run("allows requests within limit", func(t *testing.T) {
		r := gin.New()
		r.Use(RateLimitMiddleware(ctx, 10, 10))
		r.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})

		for i := 0; i < 10; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "1.2.3.4:1234"
			r.ServeHTTP(w, req)
			assert.Equal(t, 200, w.Code)
		}
	})

	t.Run("blocks requests exceeding limit", func(t *testing.T) {
		r := gin.New()
		r.Use(RateLimitMiddleware(ctx, 1, 2))
		r.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})

		blocked := 0
		for i := 0; i < 10; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "5.6.7.8:1234"
			r.ServeHTTP(w, req)
			if w.Code == http.StatusTooManyRequests {
				blocked++
			}
		}
		assert.Greater(t, blocked, 0)
	})

	t.Run("excludes configured paths", func(t *testing.T) {
		r := gin.New()
		r.Use(RateLimitMiddleware(ctx, 1, 1, "/health"))
		r.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})

		for i := 0; i < 20; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/health", nil)
			req.RemoteAddr = "9.9.9.9:1234"
			r.ServeHTTP(w, req)
			assert.Equal(t, 200, w.Code)
		}
	})

	t.Run("isolates rate limits per IP", func(t *testing.T) {
		r := gin.New()
		r.Use(RateLimitMiddleware(ctx, 1, 2))
		r.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})

		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "10.0.0.1:1234"
			r.ServeHTTP(w, req)
		}

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "10.0.0.2:1234"
		r.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code)
	})

	t.Run("X-Real-Ip takes priority over X-Forwarded-For", func(t *testing.T) {
		ip := getClientIP(&http.Request{
			Header: http.Header{
				"X-Real-Ip":       {"3.3.3.3"},
				"X-Forwarded-For": {"spoofed, 1.1.1.1, 2.2.2.2"},
			},
			RemoteAddr: "127.0.0.1:80",
		})
		assert.Equal(t, "3.3.3.3", ip)
	})

	t.Run("falls back to X-Forwarded-For rightmost when no X-Real-Ip", func(t *testing.T) {
		ip := getClientIP(&http.Request{
			Header:     http.Header{"X-Forwarded-For": {"spoofed, 1.1.1.1, 2.2.2.2"}},
			RemoteAddr: "127.0.0.1:80",
		})
		assert.Equal(t, "2.2.2.2", ip)
	})

	t.Run("fail-closed when no IP available", func(t *testing.T) {
		r := gin.New()
		r.Use(RateLimitMiddleware(ctx, 1, 1))
		r.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})

		blocked := 0
		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = ""
			r.ServeHTTP(w, req)
			if w.Code == http.StatusTooManyRequests {
				blocked++
			}
		}
		assert.Greater(t, blocked, 0)
	})

	t.Run("sets X-RateLimit headers on successful request", func(t *testing.T) {
		r := gin.New()
		r.Use(RateLimitMiddleware(ctx, 10, 20))
		r.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "1.1.1.1:1234"
		r.ServeHTTP(w, req)

		assert.Equal(t, "20", w.Header().Get("X-RateLimit-Limit"))
		remaining, err := strconv.Atoi(w.Header().Get("X-RateLimit-Remaining"))
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, remaining, 0)
		assert.Less(t, remaining, 20)
	})

	t.Run("sets Retry-After header on 429", func(t *testing.T) {
		r := gin.New()
		r.Use(RateLimitMiddleware(ctx, 1, 1))
		r.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})

		got429 := false
		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "2.2.2.2:1234"
			r.ServeHTTP(w, req)
			if w.Code == http.StatusTooManyRequests {
				retryAfter, err := strconv.Atoi(w.Header().Get("Retry-After"))
				assert.NoError(t, err)
				assert.GreaterOrEqual(t, retryAfter, 1)
				got429 = true
				break
			}
		}
		assert.True(t, got429, "expected at least one 429 response")
	})
}

func TestUIDRateLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	// newTestRouter 将 libwkhttp 中间件桥接到 gin engine。
	// 注意：测试中刻意在多个 router 上复用同一个 mw 实例（见 "isolates rate limits per uid"），
	// 以验证限流状态是挂在 mw 闭包的 keyedLimiter 上、不随 router 变化。
	// 若改成每个 router 自建 mw，将失去隔离性验证的意义。
	newTestRouter := func(mw libwkhttp.HandlerFunc, uid string) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			if uid != "" {
				c.Set("uid", uid)
			}
			c.Next()
		})
		r.Use(func(c *gin.Context) {
			lc := &libwkhttp.Context{Context: c}
			mw(lc)
		})
		r.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})
		return r
	}

	t.Run("allows requests within limit", func(t *testing.T) {
		r := newTestRouter(UIDRateLimitMiddleware(ctx, 10, 10), "user1")
		for i := 0; i < 10; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			r.ServeHTTP(w, req)
			assert.Equal(t, 200, w.Code)
		}
	})

	t.Run("blocks requests exceeding limit", func(t *testing.T) {
		r := newTestRouter(UIDRateLimitMiddleware(ctx, 1, 2), "user2")
		blocked := 0
		for i := 0; i < 10; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			r.ServeHTTP(w, req)
			if w.Code == http.StatusTooManyRequests {
				blocked++
			}
		}
		assert.Greater(t, blocked, 0)
	})

	t.Run("isolates rate limits per uid", func(t *testing.T) {
		// 同一个 mw 实例，两个不同 uid 的 router：耗尽 user3 的额度后 user4 不应受影响。
		mw := UIDRateLimitMiddleware(ctx, 1, 2)

		r1 := newTestRouter(mw, "user3")
		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			r1.ServeHTTP(w, req)
		}

		r2 := newTestRouter(mw, "user4")
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		r2.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code)
	})

	t.Run("skips when uid is absent", func(t *testing.T) {
		r := newTestRouter(UIDRateLimitMiddleware(ctx, 1, 1), "")
		// Fail-open：未经 AuthMiddleware 时应放行，不施加任何限流。
		for i := 0; i < 10; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			r.ServeHTTP(w, req)
			assert.Equal(t, 200, w.Code)
		}
	})

	t.Run("sets X-RateLimit headers on successful request", func(t *testing.T) {
		r := newTestRouter(UIDRateLimitMiddleware(ctx, 10, 20), "user5")
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, "20", w.Header().Get("X-RateLimit-Limit"))
	})
}

func TestStrictIPRateLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	// newTestRouter 把 lib wkhttp 中间件桥接到 gin engine。
	// remoteAddr 决定客户端 IP，用于按 IP 隔离的测试。
	newTestRouter := func(mw libwkhttp.HandlerFunc) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			lc := &libwkhttp.Context{Context: c}
			mw(lc)
		})
		r.POST("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true})
		})
		return r
	}

	t.Run("allows requests within limit", func(t *testing.T) {
		r := newTestRouter(StrictIPRateLimitMiddleware(ctx, 10, 10))
		for i := 0; i < 10; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/test", nil)
			req.RemoteAddr = "1.1.1.1:1000"
			r.ServeHTTP(w, req)
			assert.Equal(t, 200, w.Code)
		}
	})

	t.Run("blocks requests exceeding limit", func(t *testing.T) {
		r := newTestRouter(StrictIPRateLimitMiddleware(ctx, 1, 2))
		blocked := 0
		for i := 0; i < 10; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/test", nil)
			req.RemoteAddr = "2.2.2.2:1000"
			r.ServeHTTP(w, req)
			if w.Code == http.StatusTooManyRequests {
				blocked++
			}
		}
		assert.Greater(t, blocked, 0)
	})

	t.Run("isolates rate limits per IP", func(t *testing.T) {
		// 同一个 mw 实例，两个不同 IP 各自独立配额。
		mw := StrictIPRateLimitMiddleware(ctx, 1, 2)
		r := newTestRouter(mw)

		// 耗尽 IP A
		for i := 0; i < 10; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/test", nil)
			req.RemoteAddr = "3.3.3.3:1000"
			r.ServeHTTP(w, req)
		}

		// IP B 应该还能成功
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/test", nil)
		req.RemoteAddr = "4.4.4.4:1000"
		r.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code)
	})

	t.Run("fail-closed when no IP available", func(t *testing.T) {
		r := newTestRouter(StrictIPRateLimitMiddleware(ctx, 1, 1))
		blocked := 0
		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/test", nil)
			req.RemoteAddr = ""
			r.ServeHTTP(w, req)
			if w.Code == http.StatusTooManyRequests {
				blocked++
			}
		}
		assert.Greater(t, blocked, 0)
	})

	t.Run("sets X-RateLimit and Retry-After headers", func(t *testing.T) {
		r := newTestRouter(StrictIPRateLimitMiddleware(ctx, 1, 1))

		// 第一次成功，设 X-RateLimit-Limit
		w1 := httptest.NewRecorder()
		req1 := httptest.NewRequest("POST", "/test", nil)
		req1.RemoteAddr = "5.5.5.5:1000"
		r.ServeHTTP(w1, req1)
		assert.Equal(t, 200, w1.Code)
		assert.Equal(t, "1", w1.Header().Get("X-RateLimit-Limit"))

		// 后续应触发 429 并设 Retry-After
		got429 := false
		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/test", nil)
			req.RemoteAddr = "5.5.5.5:1000"
			r.ServeHTTP(w, req)
			if w.Code == http.StatusTooManyRequests {
				retryAfter, err := strconv.Atoi(w.Header().Get("Retry-After"))
				assert.NoError(t, err)
				assert.GreaterOrEqual(t, retryAfter, 1)
				got429 = true
				break
			}
		}
		assert.True(t, got429, "expected at least one 429")
	})
}

func TestCleanupLoopExitsOnContextCancel(t *testing.T) {
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 20; i++ {
		_ = newKeyedLimiter(ctx, 100, 100)
	}

	after := runtime.NumGoroutine()
	assert.Greater(t, after, before, "expected goroutines to be spawned")

	cancel()

	// 等待 goroutine 退出（select 立即响应 ctx.Done，不需要等 ticker 触发）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 { // 容忍少量调度抖动
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutines did not exit after context cancel: before=%d now=%d", before, runtime.NumGoroutine())
}
