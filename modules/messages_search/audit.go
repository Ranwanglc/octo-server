package messages_search

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"go.uber.org/zap"
)

// auditMiddleware emits a structured audit log line per request after the
// handler chain returns. Tracks PRM-02 fields:
//   - kind          (search_messages | search_media | search_files | search_all)
//   - login_uid
//   - channel_type / channel_id
//   - keyword_hash  (first 16 hex chars of SHA-256 — keeps the keyword opaque
//     while still allowing post-hoc deduplication)
//   - took_ms
//
// We intentionally do NOT log the keyword in clear: search queries can carry
// PII (names, IDs, sensitive search terms) and the audit channel is shared
// with other ops use-cases that should not see them.
func (h *Handler) auditMiddleware() wkhttp.HandlerFunc {
	return func(c *wkhttp.Context) {
		start := time.Now()
		c.Next()
		took := time.Since(start)

		fields := []zap.Field{
			zap.String("path", c.FullPath()),
			zap.String("login_uid", c.GetLoginUID()),
			zap.Int64("took_ms", took.Milliseconds()),
			zap.Int("status", c.Writer.Status()),
		}
		if v, ok := c.Get(auditFieldKindKey); ok {
			if s, _ := v.(string); s != "" {
				fields = append(fields, zap.String("kind", s))
			}
		}
		if v, ok := c.Get(auditFieldChannelTypeKey); ok {
			if t, _ := v.(uint8); t != 0 {
				fields = append(fields, zap.Uint8("channel_type", t))
			}
		}
		if v, ok := c.Get(auditFieldChannelIDKey); ok {
			if s, _ := v.(string); s != "" {
				fields = append(fields, zap.String("channel_id", s))
			}
		}
		if v, ok := c.Get(auditFieldKeywordHashKey); ok {
			if s, _ := v.(string); s != "" {
				fields = append(fields, zap.String("keyword_hash", s))
			}
		}
		if v, ok := c.Get(auditFieldHitsKey); ok {
			if n, _ := v.(int); n >= 0 {
				fields = append(fields, zap.Int("hits", n))
			}
		}
		h.Info("messages_search.audit", fields...)
	}
}

// hashKeyword renders a keyword as the audit-friendly opaque hash (first 16
// hex chars of SHA-256). Empty keywords produce an empty string.
func hashKeyword(keyword string) string {
	if keyword == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(keyword))
	return hex.EncodeToString(sum[:])[:16]
}

// gin context keys used to ferry per-request audit fields out of the handler
// and into the trailing middleware.
const (
	auditFieldKindKey        = "messages_search.audit.kind"
	auditFieldChannelTypeKey = "messages_search.audit.channel_type"
	auditFieldChannelIDKey   = "messages_search.audit.channel_id"
	auditFieldKeywordHashKey = "messages_search.audit.keyword_hash"
	auditFieldHitsKey        = "messages_search.audit.hits"
)

// recordAudit stores the per-request audit fields the middleware will pick up.
// Called by every handler exactly once per request.
func recordAudit(c *wkhttp.Context, kind string, channelType uint8, channelID, keyword string, hits int) {
	c.Set(auditFieldKindKey, kind)
	c.Set(auditFieldChannelTypeKey, channelType)
	c.Set(auditFieldChannelIDKey, channelID)
	c.Set(auditFieldKeywordHashKey, hashKeyword(keyword))
	c.Set(auditFieldHitsKey, hits)
}
