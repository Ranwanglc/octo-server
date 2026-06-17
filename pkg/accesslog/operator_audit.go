package accesslog

import (
	"net/http"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Operator-audit logging (octo-server #367 Phase 1).
//
// Goal: a "who did what" forensic trail for admin/management writes. Every
// mutating request to the /v1/manager surface emits exactly one structured log
// line carrying the acting operator's id, the action, the target, the response
// status and a timestamp. This is logging-only — the centralized audit table
// (Phase 2, SecurityEngineer) consumes these lines later; see the format note
// in this file's doc comments below.
//
// Security-by-default: the line is built from the request method, the
// URL *path* (scrubbed via ScrubPath, never the query string) and the operator
// id set by AuthMiddleware. Request bodies are never read, so secrets that ride
// in bodies (e.g. /v1/manager/user/resetpassword, .../updatepassword) cannot
// leak into logs. The path of a manager route names the target resource and is
// itself the action metadata Phase 2 needs.

const (
	// managerPrefix is the admin/management write surface being audited.
	managerPrefix = "/v1/manager"

	// AuditLogMessage is the stable zap message (and "audit" field value) on
	// every admin-action line. Phase 2 keys its audit-table ingest off this
	// exact discriminator, so it must not change without coordinating the
	// consumer.
	AuditLogMessage = "manager_admin_action"
)

// OperatorAuditMiddleware returns a gin middleware that emits one structured
// audit log line per mutating (POST/PUT/PATCH/DELETE) request under
// /v1/manager.
//
// Field schema of each emitted line (in addition to the logger's own ts/level):
//
//   - audit:       constant "manager_admin_action" (stable discriminator)
//   - operator_id: acting user/agent id (empty string => unauthenticated or
//     auth-rejected attempt; pair with a 4xx status to read it that way)
//   - action:      HTTP method (POST | PUT | PATCH | DELETE)
//   - target:      scrubbed request path identifying the resource acted on
//   - status:      final HTTP response status code
//   - ts:          RFC3339 UTC timestamp of when the line was emitted
//
// The operator id is read after c.Next() because AuthMiddleware (which runs in
// the matched route group, i.e. during c.Next()) is what sets it via
// c.Set("uid", ...). Logging post-handler also lets us record the real
// response status, so rejected/forbidden attempts are captured too.
func OperatorAuditMiddleware(lg log.Log) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isManagerWrite(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		c.Next()
		lg.Info(AuditLogMessage,
			zap.String("audit", AuditLogMessage),
			zap.String("operator_id", c.GetString("uid")),
			zap.String("action", c.Request.Method),
			zap.String("target", ScrubPath(c.Request.URL.Path)),
			zap.Int("status", c.Writer.Status()),
			zap.String("ts", time.Now().UTC().Format(time.RFC3339)),
		)
	}
}

// isManagerWrite reports whether (method, path) is a mutating action on the
// /v1/manager admin surface. Match is on the raw request path (not the matched
// route template) so that attempts which 404 under the manager prefix are still
// audited.
func isManagerWrite(method, path string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	return path == managerPrefix || strings.HasPrefix(path, managerPrefix+"/")
}
