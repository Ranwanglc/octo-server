package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

// TestNotifyNoLegacyResponseError pins that modules/notify/api.go does not
// regress to legacy octo-lib error responses. The `c.Response(resp)` success
// shape (no quote after the paren) and the `c.JSON(http.StatusMultiStatus, ...)`
// partial-success aggregate are intentionally allowed — only error responses
// must go through the i18n envelope. Comments are stripped first so commented-
// out breadcrumbs do not trip the guard.
func TestNotifyNoLegacyResponseError(t *testing.T) {
	data, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatalf("read api.go: %v", err)
	}
	var clean strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		clean.WriteString(line)
		clean.WriteByte('\n')
	}
	cleaned := clean.String()
	banned := []string{
		".ResponseError(",
		".ResponseErrorf(",
		".ResponseErrorWithStatus(",
		".AbortWithStatusJSON(",
		".AbortWithStatus(",
		"c.Response(\"",
	}
	for _, b := range banned {
		if strings.Contains(cleaned, b) {
			t.Fatalf("modules/notify/api.go must use httperr.ResponseErrorLWithStatus via respondNotify* helpers instead of legacy %s", b)
		}
	}
}

type notifyErrEnvelope struct {
	Error struct {
		Code       string         `json:"code"`
		Message    string         `json:"message"`
		Details    map[string]any `json:"details"`
		HTTPStatus int            `json:"http_status"`
	} `json:"error"`
	Msg    string `json:"msg"`
	Status int    `json:"status"`
}

func notifyHelperHarness(probe func(c *wkhttp.Context)) *wkhttp.WKHttp {
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
	r.GET("/probe", probe)
	return r
}

func TestRespondNotifyHelpers(t *testing.T) {
	cases := []struct {
		name         string
		probe        func(c *wkhttp.Context)
		wantCodeID   string
		wantStatus   int // real wire status (ResponseErrorLWithStatus preserves it)
		wantContains string
		wantNotHas   string
		wantDetails  map[string]any
	}{
		{
			name:         "respondNotifyUnauthorized preserves 401",
			probe:        respondNotifyUnauthorized,
			wantCodeID:   "err.server.notify.unauthorized",
			wantStatus:   http.StatusUnauthorized,
			wantContains: "未授权",
		},
		{
			name:         "respondNotifyRequestInvalid carries the field detail",
			probe:        func(c *wkhttp.Context) { respondNotifyRequestInvalid(c, "notifications") },
			wantCodeID:   "err.server.notify.request_invalid",
			wantStatus:   http.StatusBadRequest,
			wantContains: "请求参数",
			wantDetails:  map[string]any{"field": "notifications"},
		},
		{
			name:         "respondNotifyRequestInvalid drops empty field key",
			probe:        func(c *wkhttp.Context) { respondNotifyRequestInvalid(c, "") },
			wantCodeID:   "err.server.notify.request_invalid",
			wantStatus:   http.StatusBadRequest,
			wantContains: "请求参数",
			wantDetails:  map[string]any{},
		},
		{
			name:         "respondNotifyBatchLimitExceeded surfaces the cap",
			probe:        func(c *wkhttp.Context) { respondNotifyBatchLimitExceeded(c, 50) },
			wantCodeID:   "err.server.notify.batch_limit_exceeded",
			wantStatus:   http.StatusBadRequest,
			wantContains: "批量",
			wantDetails:  map[string]any{"max": float64(50)},
		},
		{
			name:         "respondNotifyDeliverFailed preserves 500 and hides internal copy",
			probe:        respondNotifyDeliverFailed,
			wantCodeID:   "err.server.notify.deliver_failed",
			wantStatus:   http.StatusInternalServerError,
			wantContains: "服务器内部错误",
			wantNotHas:   "deliver the notification",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := notifyHelperHarness(tc.probe)
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			req.Header.Set("Accept-Language", "zh-CN")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("HTTP status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var env notifyErrEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v\nbody: %s", err, rec.Body.String())
			}
			if env.Error.Code != tc.wantCodeID {
				t.Fatalf("error.code = %q, want %q", env.Error.Code, tc.wantCodeID)
			}
			if env.Error.HTTPStatus != tc.wantStatus {
				t.Fatalf("error.http_status = %d, want %d", env.Error.HTTPStatus, tc.wantStatus)
			}
			if env.Status != tc.wantStatus {
				t.Fatalf("legacy status = %d, want %d", env.Status, tc.wantStatus)
			}
			if env.Msg != env.Error.Message {
				t.Fatalf("legacy msg %q != error.message %q (dual envelope must agree)", env.Msg, env.Error.Message)
			}
			if !strings.Contains(env.Error.Message, tc.wantContains) {
				t.Fatalf("error.message = %q, want substring %q", env.Error.Message, tc.wantContains)
			}
			if tc.wantNotHas != "" && strings.Contains(env.Error.Message, tc.wantNotHas) {
				t.Fatalf("error.message = %q must not contain %q (Internal leak)", env.Error.Message, tc.wantNotHas)
			}
			if tc.wantDetails != nil {
				got := env.Error.Details
				if got == nil {
					got = map[string]any{}
				}
				if len(got) != len(tc.wantDetails) {
					t.Fatalf("error.details = %#v, want %#v", got, tc.wantDetails)
				}
				for k, v := range tc.wantDetails {
					if got[k] != v {
						t.Fatalf("error.details[%q] = %#v, want %#v", k, got[k], v)
					}
				}
			}
		})
	}
}
