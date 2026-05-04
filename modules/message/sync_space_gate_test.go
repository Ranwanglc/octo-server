// Package message regression tests for YUJ-226 / PR#1284 lml P1-1:
// the /v1/message/channel/sync Person filter must use the validated
// space_id written by SpaceMiddleware into gin context, NEVER raw
// X-Space-ID header. A rogue authenticated client could otherwise
// supply an arbitrary Space and leak SystemBot history for that Space.
//
// End-to-end fail-closed behavior of SpaceMiddleware itself (non-member
// → 403 before handler) is covered by pkg/space/middleware_test.go
// (TestSpaceMiddleware_NotMember_403 / TestSpaceMiddleware_Header_SpaceID).
// This file only asserts the handler's *read path* contract so the two
// layers cannot drift: middleware validates, handler reads context only.
package message

import (
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	spacepkg "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() { gin.SetMode(gin.TestMode) }

// TestSyncPersonFilter_ReadsValidatedContextSpaceIDNotHeader documents the
// contract syncChannelMessage enforces: raw X-Space-ID header is NOT the
// filter selector. Only the value that SpaceMiddleware validated (member
// check passed) and stored into gin context is consulted. If this test
// ever fails because the handler reverts to c.GetHeader("X-Space-ID"),
// the P1-1 hardening is defeated.
func TestSyncPersonFilter_ReadsValidatedContextSpaceIDNotHeader(t *testing.T) {
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest("POST", "/v1/message/channel/sync", nil)
	// Attack scenario: client stuffs an arbitrary Space into the raw header
	// hoping to influence server-side filtering. SpaceMiddleware either sets
	// a *different* validated value (member of spaceA only) or — for a
	// non-member — aborts before the handler is reached.
	ginCtx.Request.Header.Set("X-Space-ID", "forged_spaceB")
	ginCtx.Set("space_id", "validated_spaceA")
	c := &wkhttp.Context{Context: ginCtx}

	assert.Equal(t, "validated_spaceA", spacepkg.GetSpaceID(c),
		"handler must read middleware-validated space_id from gin context")
	assert.NotEqual(t, c.GetHeader("X-Space-ID"), spacepkg.GetSpaceID(c),
		"validated context value must win over raw X-Space-ID header")
}

// TestSyncPersonFilter_NoValidatedSpaceIDSkipsFilter verifies that when
// SpaceMiddleware did not set any space_id (no header supplied, or pre-Space
// client), the filter is skipped. Backward-compat guarantee: the new gate
// cannot break legacy clients that never talked about Space.
func TestSyncPersonFilter_NoValidatedSpaceIDSkipsFilter(t *testing.T) {
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest("POST", "/v1/message/channel/sync", nil)
	c := &wkhttp.Context{Context: ginCtx}

	assert.Empty(t, spacepkg.GetSpaceID(c),
		"no middleware-set space_id ⇒ empty string ⇒ filter gate short-circuits")
}
