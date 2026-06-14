package authz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"

	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

// serve runs one request through a real wkhttp router: a fake-auth handler that
// seeds the resolved role on the context (mirroring AuthMiddleware), then the
// authz middleware under test, then a terminal handler that records whether it
// was reached. Returns the status code and whether the handler ran.
func serve(t *testing.T, mw wkhttp.HandlerFunc, role string) (status int, reached bool, body map[string]any) {
	t.Helper()
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.SourceLanguage)))
	r.GET("/v1/manager/x",
		func(c *wkhttp.Context) {
			if role != "" {
				c.Set("role", role) // CheckLoginRole reads c.GetString("role")
			}
			c.Next()
		},
		mw,
		func(c *wkhttp.Context) {
			reached = true
			c.ResponseOK()
		},
	)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/manager/x", nil))
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec.Code, reached, body
}

func TestRequireAdmin(t *testing.T) {
	cases := []struct {
		role      string
		wantAllow bool
	}{
		{string(wkhttp.SuperAdmin), true},
		{string(wkhttp.Admin), true},
		{"user", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run("role="+tc.role, func(t *testing.T) {
			status, reached, body := serve(t, RequireAdmin(), tc.role)
			assertGate(t, tc.wantAllow, status, reached, body)
		})
	}
}

func TestRequireSuperAdmin(t *testing.T) {
	cases := []struct {
		role      string
		wantAllow bool
	}{
		{string(wkhttp.SuperAdmin), true},
		{string(wkhttp.Admin), false}, // admin must NOT pass the superAdmin gate
		{"user", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run("role="+tc.role, func(t *testing.T) {
			status, reached, body := serve(t, RequireSuperAdmin(), tc.role)
			assertGate(t, tc.wantAllow, status, reached, body)
		})
	}
}

// assertGate checks the allow/deny contract: on allow the handler runs and
// returns 200; on deny the handler is skipped (Abort) and the response is the
// generic forbidden envelope — wire 400 (D14), real 403 inside, anti-enumeration
// code, no tier leak.
func assertGate(t *testing.T, wantAllow bool, status int, reached bool, body map[string]any) {
	t.Helper()
	if wantAllow {
		if status != http.StatusOK || !reached {
			t.Fatalf("expected allow: status=%d reached=%v, want 200/true", status, reached)
		}
		return
	}
	if reached {
		t.Fatalf("expected deny but handler ran (missing Abort?)")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("deny wire status = %d, want 400 (ResponseErrorL D14 pinned)", status)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("deny body missing error object: %v", body)
	}
	if got := errObj["code"]; got != errcode.ErrSharedForbidden.ID {
		t.Fatalf("deny error.code = %v, want %q (generic, anti-enumeration)", got, errcode.ErrSharedForbidden.ID)
	}
	if got := errObj["http_status"]; got != float64(http.StatusForbidden) {
		t.Fatalf("deny error.http_status = %v, want 403", got)
	}
}
