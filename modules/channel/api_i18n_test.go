package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// channelHTTPErrL is a terse test shim for the no-params/no-details
// ResponseErrorL call shape exercised by the direct-code cases below.
func channelHTTPErrL(c *wkhttp.Context, code codes.Code) {
	httperr.ResponseErrorL(c, code, nil, nil)
}

// TestChannelNoLegacyResponseError pins the contract that the migrated
// modules/channel handlers do not regress to legacy octo-lib error responses.
// Comments are stripped first so commented-out breadcrumbs do not trip the
// guard. The ch.Error(...) zap LOG calls are not responses and are intentionally
// allowed (they match none of the banned tokens).
func TestChannelNoLegacyResponseError(t *testing.T) {
	files := []string{"api.go", "api_storyline.go"}
	banned := []string{
		".ResponseError(",
		".ResponseErrorf(",
		".ResponseErrorWithStatus(",
		".AbortWithStatusJSON(",
		".AbortWithStatus(",
		"c.Response(\"",
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
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
			for _, b := range banned {
				if strings.Contains(cleaned, b) {
					t.Fatalf("modules/channel/%s must use httperr.ResponseErrorL via respondChannel* helpers / errcode.ErrChannel* instead of legacy %s", f, b)
				}
			}
		})
	}
}

// channelErrEnvelope is the partial shape of an httperr.ResponseErrorL response.
// The renderer emits both the legacy {msg,status} and the v2 {error.{...}} blocks
// unconditionally (dual-envelope contract).
type channelErrEnvelope struct {
	Error struct {
		Code       string         `json:"code"`
		Message    string         `json:"message"`
		Details    map[string]any `json:"details"`
		HTTPStatus int            `json:"http_status"`
	} `json:"error"`
	Msg    string `json:"msg"`
	Status int    `json:"status"`
}

func decodeChannelEnvelope(t *testing.T, body []byte) channelErrEnvelope {
	t.Helper()
	var env channelErrEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, body)
	}
	return env
}

// channelHelperHarness mounts a single GET /probe route that invokes the supplied
// helper with the i18n renderer wired, so tests can assert the rendered envelope
// without paying the DB / auth setup cost.
func channelHelperHarness(probe func(c *wkhttp.Context)) *wkhttp.WKHttp {
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
	r.GET("/probe", probe)
	return r
}

func TestRespondChannelHelpers(t *testing.T) {
	cases := []struct {
		name            string
		probe           func(c *wkhttp.Context)
		wantCodeID      string
		wantSemStatus   int
		wantTransStatus int // 400 for D14 ResponseErrorL
		wantDetails     map[string]any
	}{
		// ---- validation helper (400, D14) ------------------------------------
		{
			name:            "respondChannelRequestInvalid carries the field detail",
			probe:           func(c *wkhttp.Context) { respondChannelRequestInvalid(c, "channel_id") },
			wantCodeID:      "err.server.channel.request_invalid",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{"field": "channel_id"},
		},
		{
			name:            "respondChannelRequestInvalid drops empty field key",
			probe:           func(c *wkhttp.Context) { respondChannelRequestInvalid(c, "") },
			wantCodeID:      "err.server.channel.request_invalid",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{},
		},
		// ---- direct codes: 400 / 403 / 404 (D14) -----------------------------
		{
			name:            "ErrChannelStorylineGroupOnly surfaces 400",
			probe:           func(c *wkhttp.Context) { channelHTTPErrL(c, errcode.ErrChannelStorylineGroupOnly) },
			wantCodeID:      "err.server.channel.storyline_group_only",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrChannelForbidden surfaces 403",
			probe:           func(c *wkhttp.Context) { channelHTTPErrL(c, errcode.ErrChannelForbidden) },
			wantCodeID:      "err.server.channel.forbidden",
			wantSemStatus:   http.StatusForbidden,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrChannelNotFound surfaces 404",
			probe:           func(c *wkhttp.Context) { channelHTTPErrL(c, errcode.ErrChannelNotFound) },
			wantCodeID:      "err.server.channel.not_found",
			wantSemStatus:   http.StatusNotFound,
			wantTransStatus: http.StatusBadRequest,
		},
		// ---- internal codes (500, Internal=true) -----------------------------
		{
			name:            "ErrChannelQueryFailed surfaces 500",
			probe:           func(c *wkhttp.Context) { channelHTTPErrL(c, errcode.ErrChannelQueryFailed) },
			wantCodeID:      "err.server.channel.query_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrChannelStoreFailed surfaces 500",
			probe:           func(c *wkhttp.Context) { channelHTTPErrL(c, errcode.ErrChannelStoreFailed) },
			wantCodeID:      "err.server.channel.store_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrChannelSendFailed surfaces 500",
			probe:           func(c *wkhttp.Context) { channelHTTPErrL(c, errcode.ErrChannelSendFailed) },
			wantCodeID:      "err.server.channel.send_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := channelHelperHarness(tc.probe)
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			req.Header.Set("Accept-Language", "zh-CN")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantTransStatus {
				t.Fatalf("HTTP status = %d, want %d; body=%s", rec.Code, tc.wantTransStatus, rec.Body.String())
			}
			env := decodeChannelEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tc.wantCodeID {
				t.Fatalf("error.code = %q, want %q", env.Error.Code, tc.wantCodeID)
			}
			if env.Error.HTTPStatus != tc.wantSemStatus {
				t.Fatalf("error.http_status = %d, want %d", env.Error.HTTPStatus, tc.wantSemStatus)
			}
			if env.Status != tc.wantTransStatus {
				t.Fatalf("legacy status = %d, want %d", env.Status, tc.wantTransStatus)
			}
			if env.Msg != env.Error.Message {
				t.Fatalf("legacy msg %q != error.message %q (dual envelope must agree)", env.Msg, env.Error.Message)
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
