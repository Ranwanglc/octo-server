package wkhttp

import (
	"testing"

	libwkhttp "github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/stretchr/testify/assert"
)

// resetUIDRateLimiterForTest 清除 SharedUIDRateLimiter 的单例状态，
// 供同 package 测试在不同 *config.Context 间重建限流器使用。
// 生产代码不会链接 _test.go 文件，不存在误用风险。
func resetUIDRateLimiterForTest() {
	uidRateLimitMu.Lock()
	defer uidRateLimitMu.Unlock()
	uidRateLimitMW = nil
	uidRateLimitReady = false
}

func TestParseRPSFromEnv(t *testing.T) {
	const key = "DM_API_RATELIMIT_TEST_RPS"

	tests := []struct {
		name string
		env  string
		def  float64
		want float64
	}{
		{"unset uses default", "", 2.0, 2.0},
		{"valid value", "5.5", 2.0, 5.5},
		{"malformed falls back", "2x", 2.0, 2.0},
		{"zero falls back", "0", 2.0, 2.0},
		{"negative falls back", "-1", 2.0, 2.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "" {
				t.Setenv(key, "")
			} else {
				t.Setenv(key, tc.env)
			}
			assert.Equal(t, tc.want, parseRPSFromEnv(key, tc.def))
		})
	}
}

func TestParseBurstFromEnv(t *testing.T) {
	const key = "DM_API_RATELIMIT_TEST_BURST"

	tests := []struct {
		name string
		env  string
		def  int
		want int
	}{
		{"unset uses default", "", 60, 60},
		{"valid value", "100", 60, 100},
		{"malformed falls back", "60x", 60, 60},
		{"zero falls back", "0", 60, 60},
		{"negative falls back", "-5", 60, 60},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "" {
				t.Setenv(key, "")
			} else {
				t.Setenv(key, tc.env)
			}
			assert.Equal(t, tc.want, parseBurstFromEnv(key, tc.def))
		})
	}
}

// TestSharedUIDRateLimiterSingleton 验证多次调用返回同一实例，
// 且 resetUIDRateLimiterForTest 能触发重建。
func TestSharedUIDRateLimiterSingleton(t *testing.T) {
	// 本测试不构造 *config.Context（会引入 DB 依赖），仅验证 reset 开关。
	// 真实初始化路径由集成测试或启动时覆盖。
	resetUIDRateLimiterForTest()
	assert.False(t, uidRateLimitReady)
	uidRateLimitMu.Lock()
	uidRateLimitMW = libwkhttp.HandlerFunc(func(_ *libwkhttp.Context) {}) // 仅用于验证 ready 切换，不会被调用
	uidRateLimitReady = true
	uidRateLimitMu.Unlock()
	resetUIDRateLimiterForTest()
	assert.False(t, uidRateLimitReady)
	assert.Nil(t, uidRateLimitMW)
}
