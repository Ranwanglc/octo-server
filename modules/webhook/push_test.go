package webhook

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/stretchr/testify/assert"
)

func TestHMSPush(t *testing.T) {
	hms := NewHMSPush("101827411", "649dedfb617dbd699715c05b9b430ce54013ad404cea0a5a2a16302fb01911a2", "com.xinbida.wukongchat")
	accessToken, _, err := hms.GetHMSAccessToken()
	assert.NoError(t, err)
	payloadInfo := &PayloadInfo{
		Title:   "title",
		Content: "content2222",
		Badge:   1,
	}
	err = hms.Push("ANqYJlGemvmj_H5U8L3629mb-OT7slBYJTdB8-vfpveu-oQzsJH8qtxCmEfzEiUemP1Gc7KV5M32rbiuhafNaZSu2VRPxAASLp3c_1_Ky-kUPN8FU06fZWHxLlA-6tJjCg", NewHMSPayload(payloadInfo, accessToken))
	assert.NoError(t, err)
}

func TestMIPush(t *testing.T) {
	mi := NewMIPush("2882303761519001722", "XIf41QWNIBRZPJVKUOOoYQ==", "com.xinbida.wukongchat", "")

	payloadInfo := &PayloadInfo{
		Title:   "title",
		Content: "content",
		Badge:   1,
	}

	err := mi.Push("deviceToken", NewMIPayload(payloadInfo, "11"))
	assert.NoError(t, err)
}

func TestOPPOPush(t *testing.T) {
	oppo := NewOPPOPush("30755393", "aece2f965eb64a9a82e01db87b23030e", "d7205515e1ab4fe6ace46f0f5df1105f", "dd6e2ec2e89e4669bb4afe4433b28ac1", &config.Context{})
	payloadInfo := &PayloadInfo{
		Title:   "标题",
		Content: "内容",
		Badge:   1,
	}
	err := oppo.Push("OPPO_CN_5831bbbefd00814c2bd82dbd40382869", NewOPPOPayload(payloadInfo, "11"))
	assert.NoError(t, err)
}

func TestVIVOPush(t *testing.T) {
	vivo := NewVIVOPush("105542118", "d7aacd9d36621e75a9efb7ce69b5c567", "be82d800-0078-42cf-91d2-4127781361a9", &config.Context{})
	payloadInfo := &PayloadInfo{
		Title:   "标题",
		Content: "内容",
		Badge:   1,
	}
	err := vivo.Push("16569158930074211800064", NewVIVOPayload(payloadInfo, "11"))
	assert.NoError(t, err)
}

func TestFirebasePush(t *testing.T) {
	// 请使用你本地的绝对路径进行测试
	mi := NewFIREBASEPush("service_Account_json_Path", "bobo", "这个值请从json里面获取", "")

	payloadInfo := &PayloadInfo{
		Title:   "title",
		Content: "content",
		Badge:   1,
	}
	// 这个device token是 firebase的token 不是app的device token，请前端老师帮忙提供即可。
	err := mi.Push("请前端开发给你提供这个值", NewFIREBASEPayload(payloadInfo, "11"))
	assert.NoError(t, err)
}

func TestParseOPPOAuthResponse(t *testing.T) {
	tests := []struct {
		name        string
		resp        map[string]interface{}
		wantToken   string
		wantErr     bool
		errContains string
	}{
		{
			name:        "nil response",
			resp:        nil,
			wantErr:     true,
			errContains: "empty response",
		},
		{
			name:        "missing code",
			resp:        map[string]interface{}{},
			wantErr:     true,
			errContains: "empty response",
		},
		{
			name: "code wrong type",
			resp: map[string]interface{}{
				"code": "not_a_number",
			},
			wantErr:     true,
			errContains: "unexpected code type",
		},
		{
			name: "auth failed with error code",
			resp: map[string]interface{}{
				"code":    json.Number("1001"),
				"message": "invalid credentials",
			},
			wantErr:     true,
			errContains: "auth failed",
		},
		{
			name: "data wrong type",
			resp: map[string]interface{}{
				"code": json.Number("0"),
				"data": "not_a_map",
			},
			wantErr:     true,
			errContains: "unexpected data type",
		},
		{
			name: "auth_token wrong type",
			resp: map[string]interface{}{
				"code": json.Number("0"),
				"data": map[string]interface{}{
					"auth_token": 12345,
				},
			},
			wantErr:     true,
			errContains: "unexpected auth_token type",
		},
		{
			name: "valid response",
			resp: map[string]interface{}{
				"code": json.Number("0"),
				"data": map[string]interface{}{
					"auth_token": "oppo_token_abc123",
				},
			},
			wantToken: "oppo_token_abc123",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := parseOPPOAuthResponse(tt.resp)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantToken, token)
			}
		})
	}
}

func TestMIPushResponseHandling(t *testing.T) {
	tests := []struct {
		name      string
		result    map[string]interface{}
		wantErr   bool
		errString string
	}{
		{
			name:    "nil result",
			result:  nil,
			wantErr: false,
		},
		{
			name: "result ok",
			result: map[string]interface{}{
				"result": "ok",
			},
			wantErr: false,
		},
		{
			name: "result not ok with reason",
			result: map[string]interface{}{
				"result": "error",
				"reason": "invalid token",
			},
			wantErr:   true,
			errString: "invalid token",
		},
		{
			name: "result not ok with description",
			result: map[string]interface{}{
				"result":      "error",
				"description": "server error",
			},
			wantErr:   true,
			errString: "server error",
		},
		{
			name: "result not ok without reason or description",
			result: map[string]interface{}{
				"result": "error",
			},
			wantErr:   true,
			errString: "MI push failed with unknown error",
		},
		{
			name: "result field missing (not string type)",
			result: map[string]interface{}{
				"result": 123,
			},
			wantErr: false,
		},
		{
			name: "reason wrong type should not panic",
			result: map[string]interface{}{
				"result": "error",
				"reason": 123,
			},
			wantErr:   true,
			errString: "MI push failed with unknown error",
		},
		{
			name: "description wrong type should not panic",
			result: map[string]interface{}{
				"result":      "error",
				"description": 456,
			},
			wantErr:   true,
			errString: "MI push failed with unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMIPushResult(tt.result)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errString != "" {
					assert.Equal(t, tt.errString, err.Error())
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseHMSAuthResponse(t *testing.T) {
	tests := []struct {
		name        string
		resultMap   map[string]interface{}
		wantToken   string
		wantExpire  time.Duration
		wantErr     bool
		errContains string
	}{
		{
			name:        "nil response",
			resultMap:   nil,
			wantErr:     true,
			errContains: "empty response",
		},
		{
			name:        "missing access_token",
			resultMap:   map[string]interface{}{},
			wantErr:     true,
			errContains: "unexpected access_token type",
		},
		{
			name: "access_token wrong type (int)",
			resultMap: map[string]interface{}{
				"access_token": 12345,
			},
			wantErr:     true,
			errContains: "unexpected access_token type",
		},
		{
			name: "valid response with expires_in",
			resultMap: map[string]interface{}{
				"access_token": "test_token_abc",
				"expires_in":   json.Number("7200"),
			},
			wantToken:  "test_token_abc",
			wantExpire: 7200 * time.Second,
			wantErr:    false,
		},
		{
			name: "valid response without expires_in (default 1h)",
			resultMap: map[string]interface{}{
				"access_token": "test_token_xyz",
			},
			wantToken:  "test_token_xyz",
			wantExpire: 3600 * time.Second,
			wantErr:    false,
		},
		{
			name: "expires_in wrong type (string) falls back to default",
			resultMap: map[string]interface{}{
				"access_token": "test_token_fallback",
				"expires_in":   "not_a_number",
			},
			wantToken:  "test_token_fallback",
			wantExpire: 3600 * time.Second,
			wantErr:    false,
		},
		{
			name: "expires_in zero falls back to default",
			resultMap: map[string]interface{}{
				"access_token": "test_token_zero",
				"expires_in":   json.Number("0"),
			},
			wantToken:  "test_token_zero",
			wantExpire: 3600 * time.Second,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, expire, err := parseHMSAuthResponse(tt.resultMap)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantToken, token)
				assert.Equal(t, tt.wantExpire, expire)
			}
		})
	}
}
