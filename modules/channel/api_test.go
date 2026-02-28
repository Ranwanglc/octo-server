package channel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatSecondToDisplayTime(t *testing.T) {
	tests := []struct {
		name   string
		second int64
		want   string
	}{
		// 秒级别
		{"0 seconds", 0, "0秒"},
		{"1 second", 1, "1秒"},
		{"30 seconds", 30, "30秒"},
		{"59 seconds", 59, "59秒"},

		// 分钟级别
		{"1 minute", 60, "1分钟"},
		{"2 minutes", 120, "2分钟"},
		{"30 minutes", 1800, "30分钟"},
		{"59 minutes", 3540, "59分钟"},

		// 小时级别
		{"1 hour", 3600, "1小时"},
		{"2 hours", 7200, "2小时"},
		{"23 hours", 82800, "23小时"},

		// 天级别
		{"1 day", 86400, "1天"},
		{"7 days", 604800, "7天"},
		{"29 days", 2505600, "29天"},

		// 月级别
		{"1 month", 2592000, "1月"},
		{"6 months", 15552000, "6月"},
		{"11 months", 28512000, "11月"},

		// 年级别
		{"1 year", 31104000, "1年"},
		{"2 years", 62208000, "2年"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSecondToDisplayTime(tt.second)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatSecondToDisplayTime_Boundaries(t *testing.T) {
	// 测试边界值
	// 59秒 → 秒
	assert.Equal(t, "59秒", formatSecondToDisplayTime(59))
	// 60秒 → 分钟
	assert.Equal(t, "1分钟", formatSecondToDisplayTime(60))
	// 3599秒 → 分钟
	assert.Equal(t, "59分钟", formatSecondToDisplayTime(3599))
	// 3600秒 → 小时
	assert.Equal(t, "1小时", formatSecondToDisplayTime(3600))
	// 86399秒 → 小时
	assert.Equal(t, "23小时", formatSecondToDisplayTime(86399))
	// 86400秒 → 天
	assert.Equal(t, "1天", formatSecondToDisplayTime(86400))
}
