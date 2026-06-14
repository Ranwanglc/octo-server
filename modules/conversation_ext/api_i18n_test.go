package conversation_ext

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

// convExtHTTPErrL is a terse test shim for the no-params/no-details
// ResponseErrorL call shape exercised by the direct-code cases below.
func convExtHTTPErrL(c *wkhttp.Context, code codes.Code) {
	httperr.ResponseErrorL(c, code, nil, nil)
}

// TestConvExtNoLegacyResponseError pins the contract that the migrated
// modules/conversation_ext handlers do not regress to legacy octo-lib error
// responses. Comments are stripped first so commented-out breadcrumbs do not
// trip the guard. The f.Error(...)/f.Warn(...) zap LOG calls are not responses
// and are intentionally allowed (they match none of the banned tokens).
func TestConvExtNoLegacyResponseError(t *testing.T) {
	files := []string{"api.go"}
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
					t.Fatalf("modules/conversation_ext/%s must use httperr.ResponseErrorL via respondConvExt* helpers / errcode.ErrConvExt* instead of legacy %s", f, b)
				}
			}
		})
	}
}

// convExtErrEnvelope is the partial shape of an httperr.ResponseErrorL response.
// The renderer emits both the legacy {msg,status} and the v2 {error.{...}} blocks
// unconditionally (dual-envelope contract).
type convExtErrEnvelope struct {
	Error struct {
		Code       string         `json:"code"`
		Message    string         `json:"message"`
		Details    map[string]any `json:"details"`
		HTTPStatus int            `json:"http_status"`
	} `json:"error"`
	Msg    string `json:"msg"`
	Status int    `json:"status"`
}

func decodeConvExtEnvelope(t *testing.T, body []byte) convExtErrEnvelope {
	t.Helper()
	var env convExtErrEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, body)
	}
	return env
}

// convExtHelperHarness mounts a single GET /probe route that invokes the
// supplied helper with the i18n renderer wired, so tests can assert the rendered
// envelope without paying the DB / auth setup cost.
func convExtHelperHarness(probe func(c *wkhttp.Context)) *wkhttp.WKHttp {
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
	r.GET("/probe", probe)
	return r
}

func TestRespondConvExtHelpers(t *testing.T) {
	cases := []struct {
		name            string
		probe           func(c *wkhttp.Context)
		wantCodeID      string
		wantSemStatus   int
		wantTransStatus int // 400 for D14 ResponseErrorL
		wantDetails     map[string]any
	}{
		// ---- validation helpers (400, D14) -----------------------------------
		{
			name:            "respondConvExtRequestInvalid carries the field detail",
			probe:           func(c *wkhttp.Context) { respondConvExtRequestInvalid(c, "peer_uid") },
			wantCodeID:      "err.server.conversation_ext.request_invalid",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{"field": "peer_uid"},
		},
		{
			name:            "respondConvExtRequestInvalid drops empty field key",
			probe:           func(c *wkhttp.Context) { respondConvExtRequestInvalid(c, "") },
			wantCodeID:      "err.server.conversation_ext.request_invalid",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{},
		},
		{
			name:            "respondConvExtItemsTooMany surfaces the cap",
			probe:           func(c *wkhttp.Context) { respondConvExtItemsTooMany(c, 500) },
			wantCodeID:      "err.server.conversation_ext.items_too_many",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{"max": float64(500)},
		},
		{
			name:            "respondConvExtDuplicateItem surfaces the offending pair",
			probe:           func(c *wkhttp.Context) { respondConvExtDuplicateItem(c, 2, "g123") },
			wantCodeID:      "err.server.conversation_ext.duplicate_item",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{"target_type": float64(2), "target_id": "g123"},
		},
		// ---- direct codes: 403 / 404 / 409 (D14) -----------------------------
		{
			name:            "ErrConvExtFollowForbidden surfaces 403",
			probe:           func(c *wkhttp.Context) { convExtHTTPErrL(c, errcode.ErrConvExtFollowForbidden) },
			wantCodeID:      "err.server.conversation_ext.follow_forbidden",
			wantSemStatus:   http.StatusForbidden,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrConvExtCategoryForbidden surfaces 403",
			probe:           func(c *wkhttp.Context) { convExtHTTPErrL(c, errcode.ErrConvExtCategoryForbidden) },
			wantCodeID:      "err.server.conversation_ext.category_forbidden",
			wantSemStatus:   http.StatusForbidden,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrConvExtSortTargetNotFound surfaces 404",
			probe:           func(c *wkhttp.Context) { convExtHTTPErrL(c, errcode.ErrConvExtSortTargetNotFound) },
			wantCodeID:      "err.server.conversation_ext.sort_target_not_found",
			wantSemStatus:   http.StatusNotFound,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrConvExtVersionConflict surfaces 409",
			probe:           func(c *wkhttp.Context) { convExtHTTPErrL(c, errcode.ErrConvExtVersionConflict) },
			wantCodeID:      "err.server.conversation_ext.version_conflict",
			wantSemStatus:   http.StatusConflict,
			wantTransStatus: http.StatusBadRequest,
		},
		// ---- internal codes (500, Internal=true), D14 ------------------------
		{
			name:            "ErrConvExtFollowFailed surfaces 500",
			probe:           func(c *wkhttp.Context) { convExtHTTPErrL(c, errcode.ErrConvExtFollowFailed) },
			wantCodeID:      "err.server.conversation_ext.follow_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrConvExtUnfollowFailed surfaces 500",
			probe:           func(c *wkhttp.Context) { convExtHTTPErrL(c, errcode.ErrConvExtUnfollowFailed) },
			wantCodeID:      "err.server.conversation_ext.unfollow_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrConvExtSortUpdateFailed surfaces 500",
			probe:           func(c *wkhttp.Context) { convExtHTTPErrL(c, errcode.ErrConvExtSortUpdateFailed) },
			wantCodeID:      "err.server.conversation_ext.sort_update_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := convExtHelperHarness(tc.probe)
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantTransStatus {
				t.Fatalf("HTTP status = %d, want %d; body=%s", rec.Code, tc.wantTransStatus, rec.Body.String())
			}
			env := decodeConvExtEnvelope(t, rec.Body.Bytes())
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
