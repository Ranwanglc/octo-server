package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
)

// HTTP-level wire-status regression tests addressing yujiawei +
// Jerry-Xin review on #431: the legacy authVerifyToken / authVerifyBot
// returned real HTTP statuses (401 for bad token, 503 for unpublished
// App Bot, 500 for upstream failure). The i18n-envelope migration
// must preserve that wire-status contract via ResponseErrorLWithStatus
// — Gateway / matter / fleet callers branch on the HTTP status line.

// httpTestHarness wires API → fake gin engine + serves through the
// real wkhttp.Context so the response-writing path matches production.
type httpTestHarness struct {
	api    *API
	engine *gin.Engine
}

func newHTTPHarness(svc *Service) *httpTestHarness {
	gin.SetMode(gin.TestMode)
	api := NewAPI(svc, nil)
	r := gin.New()
	// Mount the handlers via a tiny adapter that mirrors what
	// modules/auth/1module.go does at startup (without the rate-limit
	// middleware — we're testing the handler-response shape only).
	r.POST("/v1/auth/verify", func(c *gin.Context) {
		api.verifyUserHTTP(&wkhttp.Context{Context: c})
	})
	r.POST("/v1/auth/verify-bot", func(c *gin.Context) {
		api.verifyBotHTTP(&wkhttp.Context{Context: c})
	})
	return &httpTestHarness{api: api, engine: r}
}

func (h *httpTestHarness) post(path string, body any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	h.engine.ServeHTTP(w, req)
	return w
}

// TestHTTPStatusVerifyInvalidToken pins: empty/whitespace user token →
// 401 on the wire (NOT 400 which ResponseErrorL would pin).
func TestHTTPStatusVerifyInvalidToken(t *testing.T) {
	// Don't t.Parallel — registry singletons.
	prev := GetBotLookup()
	t.Cleanup(func() {
		if prev != nil {
			SetBotLookup(prev)
		}
	})

	h := newHTTPHarness(&Service{}) // nil ctx tolerated by VerifyUser's empty-token short-circuit
	w := h.post("/v1/auth/verify", VerifyUserReq{Token: ""})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("verify(empty) HTTP status = %d, want 401 (body: %s)", w.Code, w.Body.String())
	}
}

// TestHTTPStatusVerifyBotInvalidToken pins: empty bot token → 401.
func TestHTTPStatusVerifyBotInvalidToken(t *testing.T) {
	prev := GetBotLookup()
	t.Cleanup(func() {
		if prev != nil {
			SetBotLookup(prev)
		}
	})

	h := newHTTPHarness(&Service{})
	w := h.post("/v1/auth/verify-bot", VerifyBotReq{BotToken: ""})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("verify-bot(empty) HTTP status = %d, want 401 (body: %s)", w.Code, w.Body.String())
	}
}

// TestHTTPStatusVerifyBotAppUnpublished pins: ErrAppBotUnpublished →
// 503 on the wire (matches plan §4.2 mapping and legacy
// authVerifyBot's "bot is unpublished" semantic).
func TestHTTPStatusVerifyBotAppUnpublished(t *testing.T) {
	prev := GetBotLookup()
	t.Cleanup(func() {
		if prev != nil {
			SetBotLookup(prev)
		}
	})
	// Sticky-error fake bot lookup returning ErrAppBotUnpublished from
	// every LookupAppBot call.
	SetBotLookup(&fakeBotLookup{err: ErrAppBotUnpublished})

	h := newHTTPHarness(&Service{})
	w := h.post("/v1/auth/verify-bot", VerifyBotReq{BotToken: "app_unpublished"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("verify-bot(unpublished) HTTP status = %d, want 503 (body: %s)", w.Code, w.Body.String())
	}
}

// TestHTTPStatusVerifyBotUpstreamFailure pins: no BotLookup provider
// registered → 500 on the wire (treated as ErrUpstreamFailure per
// service.go contract).
func TestHTTPStatusVerifyBotUpstreamFailure(t *testing.T) {
	prev := GetBotLookup()
	t.Cleanup(func() {
		if prev != nil {
			SetBotLookup(prev)
		}
	})
	// Clear the registry by storing a holder wrapping nil.
	botLookupValue.Store(&botLookupHolder{v: nil})

	h := newHTTPHarness(&Service{})
	w := h.post("/v1/auth/verify-bot", VerifyBotReq{BotToken: "bf_no_provider"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("verify-bot(no-provider) HTTP status = %d, want 500 (body: %s)", w.Code, w.Body.String())
	}
}
