package file

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

// fileHTTPErrL is a terse test shim for the no-params/no-details ResponseErrorL
// call shape exercised by the direct-code cases below.
func fileHTTPErrL(c *wkhttp.Context, code codes.Code) {
	httperr.ResponseErrorL(c, code, nil, nil)
}

// TestFileNoLegacyResponseError pins that the migrated modules/file handlers do
// not regress to legacy octo-lib error responses. Comments are stripped first so
// commented-out breadcrumbs do not trip the guard. The f.Error(...)/f.Warn(...)
// zap LOG calls are not responses and are intentionally allowed (they match none
// of the banned tokens).
func TestFileNoLegacyResponseError(t *testing.T) {
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
					t.Fatalf("modules/file/%s must use httperr.ResponseErrorL via respondFile* helpers / errcode.ErrFile* instead of legacy %s", f, b)
				}
			}
		})
	}
}

// fileErrEnvelope is the partial shape of an httperr.ResponseErrorL response. The
// renderer emits both the legacy {msg,status} and the v2 {error.{...}} blocks
// unconditionally (dual-envelope contract).
type fileErrEnvelope struct {
	Error struct {
		Code       string         `json:"code"`
		Message    string         `json:"message"`
		Details    map[string]any `json:"details"`
		HTTPStatus int            `json:"http_status"`
	} `json:"error"`
	Msg    string `json:"msg"`
	Status int    `json:"status"`
}

func decodeFileEnvelope(t *testing.T, body []byte) fileErrEnvelope {
	t.Helper()
	var env fileErrEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, body)
	}
	return env
}

// fileHelperHarness mounts a single GET /probe route that invokes the supplied
// helper with the i18n renderer wired, so tests can assert the rendered envelope
// without paying the DB / auth setup cost.
func fileHelperHarness(probe func(c *wkhttp.Context)) *wkhttp.WKHttp {
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
	r.GET("/probe", probe)
	return r
}

func TestRespondFileHelpers(t *testing.T) {
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
			name:            "respondFileRequestInvalid carries the field detail",
			probe:           func(c *wkhttp.Context) { respondFileRequestInvalid(c, "fileSize") },
			wantCodeID:      "err.server.file.request_invalid",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{"field": "fileSize"},
		},
		{
			name:            "respondFileRequestInvalid drops empty field key",
			probe:           func(c *wkhttp.Context) { respondFileRequestInvalid(c, "") },
			wantCodeID:      "err.server.file.request_invalid",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{},
		},
		{
			name:            "respondFileImageCountExceeded surfaces the count cap",
			probe:           func(c *wkhttp.Context) { respondFileImageCountExceeded(c, 9) },
			wantCodeID:      "err.server.file.image_count_exceeded",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{"max": float64(9)},
		},
		{
			name:            "respondFileTooLarge surfaces the size cap",
			probe:           func(c *wkhttp.Context) { respondFileTooLarge(c, 100) },
			wantCodeID:      "err.server.file.too_large",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{"max_mb": float64(100)},
		},
		{
			name:            "respondFileTypeUnsupported surfaces the rejected ext",
			probe:           func(c *wkhttp.Context) { respondFileTypeUnsupported(c, ".exe") },
			wantCodeID:      "err.server.file.type_unsupported",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{"ext": ".exe"},
		},
		{
			name:            "respondFileTypeUnsupported drops empty ext key",
			probe:           func(c *wkhttp.Context) { respondFileTypeUnsupported(c, "") },
			wantCodeID:      "err.server.file.type_unsupported",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
			wantDetails:     map[string]any{},
		},
		// ---- direct codes: 400 ------------------------------------------------
		{
			name:            "ErrFileInvalidPath surfaces 400",
			probe:           func(c *wkhttp.Context) { fileHTTPErrL(c, errcode.ErrFileInvalidPath) },
			wantCodeID:      "err.server.file.invalid_path",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrFileExtensionRequired surfaces 400",
			probe:           func(c *wkhttp.Context) { fileHTTPErrL(c, errcode.ErrFileExtensionRequired) },
			wantCodeID:      "err.server.file.extension_required",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrFileContentMismatch surfaces 400",
			probe:           func(c *wkhttp.Context) { fileHTTPErrL(c, errcode.ErrFileContentMismatch) },
			wantCodeID:      "err.server.file.content_mismatch",
			wantSemStatus:   http.StatusBadRequest,
			wantTransStatus: http.StatusBadRequest,
		},
		// ---- internal codes (500, Internal=true), still D14 400 wire ----------
		{
			name:            "ErrFileReadFailed surfaces 500 semantic, 400 wire",
			probe:           func(c *wkhttp.Context) { fileHTTPErrL(c, errcode.ErrFileReadFailed) },
			wantCodeID:      "err.server.file.read_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrFileProcessFailed surfaces 500 semantic, 400 wire",
			probe:           func(c *wkhttp.Context) { fileHTTPErrL(c, errcode.ErrFileProcessFailed) },
			wantCodeID:      "err.server.file.process_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrFileImageComposeFailed surfaces 500 semantic, 400 wire",
			probe:           func(c *wkhttp.Context) { fileHTTPErrL(c, errcode.ErrFileImageComposeFailed) },
			wantCodeID:      "err.server.file.image_compose_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrFileUploadFailed surfaces 500 semantic, 400 wire",
			probe:           func(c *wkhttp.Context) { fileHTTPErrL(c, errcode.ErrFileUploadFailed) },
			wantCodeID:      "err.server.file.upload_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
		{
			name:            "ErrFilePresignFailed surfaces 500 semantic, 400 wire",
			probe:           func(c *wkhttp.Context) { fileHTTPErrL(c, errcode.ErrFilePresignFailed) },
			wantCodeID:      "err.server.file.presign_failed",
			wantSemStatus:   http.StatusInternalServerError,
			wantTransStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := fileHelperHarness(tc.probe)
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			req.Header.Set("Accept-Language", "zh-CN")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantTransStatus {
				t.Fatalf("HTTP status = %d, want %d; body=%s", rec.Code, tc.wantTransStatus, rec.Body.String())
			}
			env := decodeFileEnvelope(t, rec.Body.Bytes())
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
