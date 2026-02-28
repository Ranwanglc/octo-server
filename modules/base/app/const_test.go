package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_Int(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   int
	}{
		{"disable", StatusDisable, 0},
		{"enable", StatusEnable, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.Int())
		})
	}
}

func TestStatus_String(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   string
	}{
		{"disable", StatusDisable, "disable"},
		{"enable", StatusEnable, "enable"},
		{"unknown", Status(99), "unknown"},
		{"negative", Status(-1), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.String())
		})
	}
}

func TestStatusIota(t *testing.T) {
	// 验证 iota 顺序
	assert.Equal(t, Status(0), StatusDisable)
	assert.Equal(t, Status(1), StatusEnable)
}

func TestReq_Check(t *testing.T) {
	tests := []struct {
		name    string
		req     Req
		wantErr bool
	}{
		{"valid appID", Req{AppID: "app_001"}, false},
		{"empty appID", Req{AppID: ""}, true},
		{"whitespace appID", Req{AppID: "  "}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Check()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "appID不能为空")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
