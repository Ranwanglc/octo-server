package accesslog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// captureLog is a log.Log fake that records the fields of the most recent
// Info call so tests can assert on the emitted audit line.
type captureLog struct {
	calls []capturedLine
}

type capturedLine struct {
	msg    string
	fields map[string]zap.Field
}

func (c *captureLog) record(msg string, fields []zap.Field) {
	m := make(map[string]zap.Field, len(fields))
	for _, f := range fields {
		m[f.Key] = f
	}
	c.calls = append(c.calls, capturedLine{msg: msg, fields: m})
}

func (c *captureLog) Info(msg string, fields ...zap.Field)  { c.record(msg, fields) }
func (c *captureLog) Debug(msg string, fields ...zap.Field) {}
func (c *captureLog) Error(msg string, fields ...zap.Field) {}
func (c *captureLog) Warn(msg string, fields ...zap.Field)  {}

// newAuditRouter builds a gin router wired exactly like production: the audit
// middleware is global, and a fake AuthMiddleware (mirroring octo-lib's
// c.Set("uid", ...)) runs inside the matched /v1/manager route group. operator
// is the uid the fake auth sets; pass "" to simulate an unauthenticated actor.
func newAuditRouter(lg *captureLog, operator string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(OperatorAuditMiddleware(lg))

	mgr := r.Group("/v1/manager", func(c *gin.Context) {
		if operator != "" {
			c.Set("uid", operator)
		}
		c.Next()
	})
	mgr.POST("/user/admin", func(c *gin.Context) { c.Status(http.StatusOK) })
	mgr.GET("/user/list", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

// Representative admin mutation: the operator id must appear on the audit line.
func TestOperatorAuditMiddleware_LogsOperatorOnAdminWrite(t *testing.T) {
	lg := &captureLog{}
	r := newAuditRouter(lg, "u_admin_42")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/manager/user/admin", nil)
	r.ServeHTTP(w, req)

	if len(lg.calls) != 1 {
		t.Fatalf("expected exactly 1 audit line, got %d", len(lg.calls))
	}
	line := lg.calls[0]
	if line.msg != AuditLogMessage {
		t.Errorf("audit message = %q, want %q", line.msg, AuditLogMessage)
	}

	op, ok := line.fields["operator_id"]
	if !ok {
		t.Fatal("audit line missing operator_id field")
	}
	if op.String != "u_admin_42" {
		t.Errorf("operator_id = %q, want %q", op.String, "u_admin_42")
	}

	// Action, target and a timestamp must all be present for Phase 2 ingest.
	if got := line.fields["action"].String; got != http.MethodPost {
		t.Errorf("action = %q, want %q", got, http.MethodPost)
	}
	if got := line.fields["target"].String; got != "/v1/manager/user/admin" {
		t.Errorf("target = %q, want %q", got, "/v1/manager/user/admin")
	}
	if _, ok := line.fields["ts"]; !ok {
		t.Error("audit line missing ts field")
	}
	if got := line.fields["status"].Integer; got != int64(http.StatusOK) {
		t.Errorf("status = %d, want %d", got, http.StatusOK)
	}
}

// Read-only admin traffic must not be audited.
func TestOperatorAuditMiddleware_SkipsReads(t *testing.T) {
	lg := &captureLog{}
	r := newAuditRouter(lg, "u_admin_42")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/manager/user/list", nil)
	r.ServeHTTP(w, req)

	if len(lg.calls) != 0 {
		t.Fatalf("GET should not be audited, got %d lines", len(lg.calls))
	}
}

// Non-manager writes must not be audited.
func TestOperatorAuditMiddleware_SkipsNonManager(t *testing.T) {
	lg := &captureLog{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(OperatorAuditMiddleware(lg))
	r.POST("/v1/message/send", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/message/send", nil)
	r.ServeHTTP(w, req)

	if len(lg.calls) != 0 {
		t.Fatalf("non-manager write should not be audited, got %d lines", len(lg.calls))
	}
}

// An unauthenticated/rejected attempt is still audited, with an empty
// operator_id — useful forensic signal when paired with a 4xx status.
func TestOperatorAuditMiddleware_AuditsRejectedAttemptWithEmptyOperator(t *testing.T) {
	lg := &captureLog{}
	r := newAuditRouter(lg, "") // no uid set => unauthenticated

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/manager/user/admin", nil)
	r.ServeHTTP(w, req)

	if len(lg.calls) != 1 {
		t.Fatalf("expected 1 audit line for the attempt, got %d", len(lg.calls))
	}
	if got := lg.calls[0].fields["operator_id"].String; got != "" {
		t.Errorf("operator_id = %q, want empty for unauthenticated attempt", got)
	}
}

func TestIsManagerWrite(t *testing.T) {
	tests := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/v1/manager/user/admin", true},
		{http.MethodPut, "/v1/manager/group/liftban/g1/1", true},
		{http.MethodDelete, "/v1/manager/robots/r1", true},
		{http.MethodPatch, "/v1/manager/anything", true},
		{http.MethodPost, "/v1/manager/workplace/app", true},
		{http.MethodGet, "/v1/manager/user/list", false},
		{http.MethodHead, "/v1/manager/user/list", false},
		{http.MethodPost, "/v1/message/send", false},
		{http.MethodPost, "/v1/managerx/foo", false}, // prefix must be a path segment
		{http.MethodPost, "/v1/manager", true},       // bare prefix counts
	}
	for _, tt := range tests {
		if got := isManagerWrite(tt.method, tt.path); got != tt.want {
			t.Errorf("isManagerWrite(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
		}
	}
}
