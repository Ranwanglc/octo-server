package i18n

// Phase 0 退出条件验证用例 (TODOS §0.9)。
//
// 本文件不引入新功能,而是为 Phase 0 verification scenarios 提供一组**显式**
// 断言,与 0.9 checklist 1:1 对应。若已有 *_test.go 覆盖了某项契约,本文件
// 仍补一条以 TestPhase0_* 命名的检查,作为 Phase 0 readiness 的单一入口
// (CI 可用 `go test -run TestPhase0_` 聚焦运行)。
//
// 不覆盖项目(及理由):
//   - 公开未登录接口 (login/register/invite) 本地化: 需先把 modules/user 改造为
//     ResponseErrorL,属于 Phase 2 范围,Phase 0 退出条件标注"留 Phase 2 兜底"。
//   - 多端会话同步: 见 modules/user/language_multidevice_test.go,因 LanguageService
//     依赖 fake DB+Cache 那里更顺手。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
)

// withLangHandler returns a wkhttp router that injects a fixed language into
// the request context before invoking handler. Used to side-step
// EarlyMiddleware in tests that want to assert renderer behavior for a
// chosen lang without depending on Accept-Language parsing.
func withLangHandler(t *testing.T, lang string, handler func(*wkhttp.Context)) *wkhttp.WKHttp {
	t.Helper()
	r := wkhttp.New()
	r.SetErrorRenderer(NewErrorRenderer(NewLocalizer(SourceLanguage)))
	r.GET("/x", func(c *wkhttp.Context) {
		c.Request = c.Request.WithContext(WithLanguage(c.Request.Context(), LanguageDecision{
			Language: lang,
			Source:   LanguageSourceAccept,
		}))
		handler(c)
	})
	return r
}

func doGet(t *testing.T, r *wkhttp.WKHttp, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, body *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v\n%s", err, body.String())
	}
	return m
}

// TestPhase0_HandlerErrorLocalization_SymmetricZhEn locks Phase 0 §0.9 item
// "handler 错误本地化". The same Code emitted under zh-CN vs en-US must produce
// the corresponding TOML translation, proving the localizer + bundle path is
// the actual source of the message (not a default-message fallback).
func TestPhase0_HandlerErrorLocalization_SymmetricZhEn(t *testing.T) {
	// Reset bundle so the assertion exercises real TOML load — not stale
	// state from an earlier test that may have injected a substitute.
	resetBundle()
	t.Cleanup(resetBundle)

	cases := []struct {
		lang    string
		wantMsg string
	}{
		{lang: "zh-CN", wantMsg: "请先登录！"},
		{lang: "en-US", wantMsg: "Please log in to continue."},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.lang, func(t *testing.T) {
			r := withLangHandler(t, tc.lang, func(c *wkhttp.Context) {
				c.RenderError(wkhttp.ErrorSpec{
					Code:            "err.shared.auth.required",
					TransportStatus: http.StatusUnauthorized,
					SemanticStatus:  http.StatusUnauthorized,
				})
			})
			rec := doGet(t, r, nil)
			if got := rec.Header().Get("Content-Language"); got != tc.lang {
				t.Fatalf("Content-Language = %q, want %q", got, tc.lang)
			}
			body := decodeBody(t, rec.Body)
			if got := body["msg"]; got != tc.wantMsg {
				t.Fatalf("msg = %q, want %q", got, tc.wantMsg)
			}
			errObj := body["error"].(map[string]any)
			if got := errObj["message"]; got != tc.wantMsg {
				t.Fatalf("error.message = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

// TestPhase0_HTTPStatusCompat_TransportDivergesFromSemantic locks D14:
// during the compatibility window the wire status is pinned to the value
// chosen by the caller (typically 400), but the body's error.http_status
// continues to expose the real semantic status. Tests where Transport ==
// Semantic don't prove the channel separation, so this case explicitly
// uses 400/500.
func TestPhase0_HTTPStatusCompat_TransportDivergesFromSemantic(t *testing.T) {
	r := withLangHandler(t, "en-US", func(c *wkhttp.Context) {
		c.RenderError(wkhttp.ErrorSpec{
			Code:            "err.shared.internal",
			TransportStatus: http.StatusBadRequest,
			SemanticStatus:  http.StatusInternalServerError,
			Internal:        true,
		})
	})
	rec := doGet(t, r, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wire status = %d, want 400 (D14 compat: transport pinned)", rec.Code)
	}
	body := decodeBody(t, rec.Body)
	if got := body["status"]; got != float64(http.StatusBadRequest) {
		t.Fatalf("body.status = %v, want 400 (legacy envelope mirrors wire)", got)
	}
	errObj := body["error"].(map[string]any)
	if got := errObj["http_status"]; got != float64(http.StatusInternalServerError) {
		t.Fatalf("error.http_status = %v, want 500 (semantic preserved)", got)
	}
}

// TestPhase0_DualEnvelopeAlwaysEmitted_ParityRegardlessOfV2Header locks the
// v7.2 decision that the renderer is **header-agnostic**: clients sending
// X-Octo-Error-Envelope: v2 receive the exact same bytes as clients that
// don't. Any future regression that branches on the header (e.g. omitting
// msg/status for v2 to shrink payload) breaks legacy clients silently;
// this parity test makes that regression a hard fail.
func TestPhase0_DualEnvelopeAlwaysEmitted_ParityRegardlessOfV2Header(t *testing.T) {
	build := func() *wkhttp.WKHttp {
		return withLangHandler(t, "en-US", func(c *wkhttp.Context) {
			c.RenderError(wkhttp.ErrorSpec{
				Code:            "err.shared.auth.required",
				TransportStatus: http.StatusUnauthorized,
				SemanticStatus:  http.StatusUnauthorized,
			})
		})
	}

	recOmit := doGet(t, build(), nil)
	recV2 := doGet(t, build(), map[string]string{"X-Octo-Error-Envelope": "v2"})

	if recOmit.Code != recV2.Code {
		t.Fatalf("wire status diverged: omit=%d v2=%d", recOmit.Code, recV2.Code)
	}
	if recOmit.Body.String() != recV2.Body.String() {
		t.Fatalf("body bytes diverged with X-Octo-Error-Envelope header:\nomit=%s\nv2=%s",
			recOmit.Body.String(), recV2.Body.String())
	}
	// Spot-check both envelopes still ship both shapes (msg/status AND error.{}).
	body := decodeBody(t, recOmit.Body)
	if _, ok := body["msg"]; !ok {
		t.Error("legacy msg field missing")
	}
	if _, ok := body["error"]; !ok {
		t.Error("v2 error field missing")
	}
}

// TestPhase0_MiddlewareAbortLocalization simulates the two octo-lib
// middleware abort branches that Phase 0 cares about (Auth token missing,
// rate limit) by composing EarlyMiddleware + a stand-in middleware that
// calls c.RenderError directly — exactly the call shape octo-lib's
// AuthMiddleware / RateLimitMiddleware use after PR #47. Verifies the
// abort body is localized end-to-end through Accept-Language negotiation,
// not by hand-injecting a language onto context.
func TestPhase0_MiddlewareAbortLocalization(t *testing.T) {
	cases := []struct {
		name            string
		acceptLang      string
		code            string
		transportStatus int
		details         map[string]any
		wantStatus      int
		wantMsg         string
		wantDetails     map[string]any
	}{
		{
			name:            "auth_token_missing_zh",
			acceptLang:      "zh-CN",
			code:            "err.shared.auth.token_missing",
			transportStatus: http.StatusUnauthorized,
			wantStatus:      http.StatusUnauthorized,
			wantMsg:         "token不能为空，请先登录！",
		},
		{
			name:            "auth_token_missing_en",
			acceptLang:      "en-US",
			code:            "err.shared.auth.token_missing",
			transportStatus: http.StatusUnauthorized,
			wantStatus:      http.StatusUnauthorized,
			wantMsg:         "Authentication token is required.",
		},
		{
			name:            "rate_limited_zh_with_retry_after",
			acceptLang:      "zh-CN",
			code:            "err.shared.rate.limited",
			transportStatus: http.StatusTooManyRequests,
			details:         map[string]any{"retry_after": 5, "raw_err": "MUST be dropped"},
			wantStatus:      http.StatusTooManyRequests,
			wantMsg:         "请求过于频繁，请稍后再试。",
			wantDetails:     map[string]any{"retry_after": float64(5)},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := wkhttp.New()
			r.SetErrorRenderer(NewErrorRenderer(NewLocalizer(SourceLanguage)))
			r.UseGin(EarlyMiddleware(MiddlewareOptions{DefaultLanguage: SourceLanguage}))
			// abortAsMiddleware mirrors the abort shape octo-lib uses: render
			// the spec then return without calling c.Next().
			r.GET("/protected", func(c *wkhttp.Context) {
				c.RenderError(wkhttp.ErrorSpec{
					Code:            tc.code,
					TransportStatus: tc.transportStatus,
					SemanticStatus:  tc.transportStatus,
					Details:         tc.details,
				})
			})

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Accept-Language", tc.acceptLang)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Content-Language"); got != tc.acceptLang {
				t.Fatalf("Content-Language = %q, want %q", got, tc.acceptLang)
			}
			body := decodeBody(t, rec.Body)
			if got := body["msg"]; got != tc.wantMsg {
				t.Fatalf("msg = %q, want %q", got, tc.wantMsg)
			}
			if tc.wantDetails != nil {
				errObj := body["error"].(map[string]any)
				gotDetails, ok := errObj["details"].(map[string]any)
				if !ok {
					t.Fatalf("error.details missing or wrong type: %#v", errObj["details"])
				}
				for k, want := range tc.wantDetails {
					if got := gotDetails[k]; got != want {
						t.Errorf("details[%q] = %v, want %v", k, got, want)
					}
				}
				if _, leaked := gotDetails["raw_err"]; leaked {
					t.Error("rate.limited details leaked raw_err (must be whitelist-dropped)")
				}
			}
		})
	}
}

// TestPhase0_DefaultLanguageSwitchPinsNegotiation locks Phase 0 §0.9 item
// "OCTO_DEFAULT_LANGUAGE zh-CN 与 en-US 切换": for a request that carries no
// language signal at all, the EarlyMiddleware-negotiated language MUST be
// whatever the operator configured as default — and that language MUST flow
// through to the rendered error body.
func TestPhase0_DefaultLanguageSwitchPinsNegotiation(t *testing.T) {
	resetBundle()
	t.Cleanup(resetBundle)

	cases := []struct {
		defaultLang string
		wantMsg     string
	}{
		{defaultLang: "zh-CN", wantMsg: "请先登录！"},
		{defaultLang: "en-US", wantMsg: "Please log in to continue."},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.defaultLang, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := wkhttp.New()
			r.SetErrorRenderer(NewErrorRenderer(NewLocalizer(tc.defaultLang)))
			r.UseGin(EarlyMiddleware(MiddlewareOptions{DefaultLanguage: tc.defaultLang}))
			r.GET("/x", func(c *wkhttp.Context) {
				c.RenderError(wkhttp.ErrorSpec{
					Code:            "err.shared.auth.required",
					TransportStatus: http.StatusUnauthorized,
					SemanticStatus:  http.StatusUnauthorized,
				})
			})

			// No Accept-Language, no cookie, no query — pure default path.
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if got := rec.Header().Get("Content-Language"); got != tc.defaultLang {
				t.Fatalf("Content-Language = %q, want default %q", got, tc.defaultLang)
			}
			body := decodeBody(t, rec.Body)
			if got := body["msg"]; got != tc.wantMsg {
				t.Fatalf("msg = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}
