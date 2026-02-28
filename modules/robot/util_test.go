package robot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomHexStr(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		wantLen int
	}{
		{"1 byte", 1, 2},
		{"4 bytes", 4, 8},
		{"8 bytes", 8, 16},
		{"16 bytes", 16, 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hex := randomHexStr(tt.n)
			assert.Len(t, hex, tt.wantLen, "randomHexStr(%d) should return %d chars", tt.n, tt.wantLen)

			// 验证所有字符是合法的十六进制字符
			for _, c := range hex {
				assert.True(t,
					(c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
					"invalid hex char: %c", c)
			}
		})
	}
}

func TestRandomHexStr_Uniqueness(t *testing.T) {
	// 生成多个随机字符串，验证不全相同
	results := make(map[string]bool)
	for i := 0; i < 50; i++ {
		hex := randomHexStr(16)
		results[hex] = true
	}
	assert.Greater(t, len(results), 1, "should generate different hex strings")
}

func TestRobotEvent_Fields(t *testing.T) {
	evt := robotEvent{
		EventID: 42,
		Expire:  3600,
	}

	assert.Equal(t, int64(42), evt.EventID)
	assert.Equal(t, int64(3600), evt.Expire)
	assert.Nil(t, evt.Message)
	assert.Nil(t, evt.InlineQuery)
}

func TestRobotConst(t *testing.T) {
	assert.Equal(t, ResultType("gif"), ResultTypeGIF)
}
