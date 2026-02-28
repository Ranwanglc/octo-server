package qrcode

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForwardConstants(t *testing.T) {
	assert.Equal(t, Forward("native"), ForwardNative)
	assert.Equal(t, Forward("h5"), ForwardH5)
}

func TestForward_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		f    Forward
		want string
	}{
		{"native", ForwardNative, `"native"`},
		{"h5", ForwardH5, `"h5"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.f)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, string(data))
		})
	}
}

func TestForward_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Forward
	}{
		{"native", `"native"`, ForwardNative},
		{"h5", `"h5"`, ForwardH5},
		{"custom", `"custom"`, Forward("custom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f Forward
			err := json.Unmarshal([]byte(tt.input), &f)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, f)
		})
	}
}

func TestForward_UnmarshalJSON_Invalid(t *testing.T) {
	var f Forward
	err := json.Unmarshal([]byte(`123`), &f)
	assert.Error(t, err)
}

func TestForward_RoundTrip(t *testing.T) {
	original := ForwardNative
	data, err := json.Marshal(original)
	assert.NoError(t, err)

	var decoded Forward
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestHandlerTypeConstants(t *testing.T) {
	assert.Equal(t, HandlerType("webview"), HandlerTypeWebView)
	assert.Equal(t, HandlerType("group"), HandlerTypeGroup)
	assert.Equal(t, HandlerType("loginConfirm"), HandlerTypeLoginConfirm)
	assert.Equal(t, HandlerType("userInfo"), HandlerTypeUserInfo)
}

func TestHandlerType_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		ht   HandlerType
		want string
	}{
		{"webview", HandlerTypeWebView, `"webview"`},
		{"group", HandlerTypeGroup, `"group"`},
		{"loginConfirm", HandlerTypeLoginConfirm, `"loginConfirm"`},
		{"userInfo", HandlerTypeUserInfo, `"userInfo"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.ht)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, string(data))
		})
	}
}

func TestHandlerType_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  HandlerType
	}{
		{"webview", `"webview"`, HandlerTypeWebView},
		{"group", `"group"`, HandlerTypeGroup},
		{"loginConfirm", `"loginConfirm"`, HandlerTypeLoginConfirm},
		{"userInfo", `"userInfo"`, HandlerTypeUserInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ht HandlerType
			err := json.Unmarshal([]byte(tt.input), &ht)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, ht)
		})
	}
}

func TestHandlerType_UnmarshalJSON_Invalid(t *testing.T) {
	var ht HandlerType
	err := json.Unmarshal([]byte(`true`), &ht)
	assert.Error(t, err)
}

func TestHandlerType_RoundTrip(t *testing.T) {
	original := HandlerTypeLoginConfirm
	data, err := json.Marshal(original)
	assert.NoError(t, err)

	var decoded HandlerType
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestForwardInStruct(t *testing.T) {
	type testStruct struct {
		Forward Forward     `json:"forward"`
		Handler HandlerType `json:"handler"`
	}

	original := testStruct{
		Forward: ForwardH5,
		Handler: HandlerTypeGroup,
	}

	data, err := json.Marshal(original)
	assert.NoError(t, err)

	var decoded testStruct
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, original, decoded)
}
