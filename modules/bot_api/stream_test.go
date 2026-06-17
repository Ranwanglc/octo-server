// Package bot_api · OCT-31: tests for the public /v1/bot/stream/* routes.
//
// Exercises the streamStart / streamEnd HTTP handlers end-to-end with the
// IMStreamStart / IMStreamEnd RPCs intercepted via streamStartOverride /
// streamEndOverride, so no live WuKongIM is required.
//
// Permission paths used to avoid DB dependencies:
//   - allowed:  BotKindUser DM where CreatorUID == channel_id (creator-bypass
//     branch of checkSendPermission — no IsFriend / group_member query).
//   - denied:   BotKindApp with a GROUP channel (App bots are DM-only, so
//     checkSendPermission rejects before any DB call).
package bot_api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// streamStartCapture records the MessageStreamStartReq passed to the override.
type streamStartCapture struct {
	mu       sync.Mutex
	captured *config.MessageStreamStartReq
	streamNo string
}

func (s *streamStartCapture) hook(req config.MessageStreamStartReq) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := req
	s.captured = &clone
	return s.streamNo, nil
}

// streamEndCapture records the MessageStreamEndReq passed to the override.
type streamEndCapture struct {
	mu       sync.Mutex
	captured *config.MessageStreamEndReq
}

func (s *streamEndCapture) hook(req config.MessageStreamEndReq) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := req
	s.captured = &clone
	return nil
}

func newStreamTestContext(rec *httptest.ResponseRecorder, path string, body []byte, robotID, creatorUID, botKind string) *wkhttp.Context {
	httpReq := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	gc, _ := gin.CreateTestContext(rec)
	gc.Request = httpReq
	c := &wkhttp.Context{Context: gc}
	c.Set(CtxKeyRobotID, robotID)
	c.Set(CtxKeyBotKind, botKind)
	// CreatorUID == DM peer exercises the creator-bypass branch of
	// checkSendPermission (no DB). Harmless for the App-bot denial path.
	c.Set(CtxKeyRobot, &robotModel{RobotID: robotID, CreatorUID: creatorUID})
	return c
}

// TestStreamStart_ScopesFromUIDToBot is the OCT-31 security acceptance test:
// a forged client FromUID MUST be overwritten with the authenticated robotID,
// and the channel send-permission gate MUST run before the stream opens.
func TestStreamStart_ScopesFromUIDToBot(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		botRobotID = "bot_X"
		creatorUID = "user_creator" // == channel_id → creator-bypass branch
		forgedFrom = "victim_user"  // attacker-supplied FromUID
		wantStream = "stream_42"
	)

	sc := &streamStartCapture{streamNo: wantStream}
	ba := &BotAPI{
		Log:                 log.NewTLog("BotAPI-stream-it"),
		streamStartOverride: sc.hook,
	}

	body, _ := json.Marshal(config.MessageStreamStartReq{
		FromUID:     forgedFrom, // must be ignored by the server
		ChannelID:   creatorUID,
		ChannelType: common.ChannelTypePerson.Uint8(),
	})

	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/start", body, botRobotID, creatorUID, BotKindUser)
	ba.streamStart(c)

	assert.Equalf(t, http.StatusOK, rec.Code,
		"streamStart should respond 200, got %d body=%s", rec.Code, rec.Body.String())

	sc.mu.Lock()
	defer sc.mu.Unlock()
	if !assert.NotNil(t, sc.captured, "IMStreamStart must have been invoked") {
		return
	}
	assert.Equal(t, botRobotID, sc.captured.FromUID,
		"FromUID MUST be forced to the authenticated robotID, not the forged client value")
	assert.NotEqual(t, forgedFrom, sc.captured.FromUID)
	assert.Equal(t, creatorUID, sc.captured.ChannelID)

	// Response body carries the server-assigned stream_no.
	var resp struct {
		Data struct {
			StreamNo string `json:"stream_no"`
		} `json:"data"`
		StreamNo string `json:"stream_no"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	got := resp.StreamNo
	if got == "" {
		got = resp.Data.StreamNo
	}
	assert.Equal(t, wantStream, got, "response must return the stream_no from IMStreamStart")
}

// TestStreamStart_PermissionDenied: an App bot may only DM, so a GROUP stream
// must be rejected and IMStreamStart must NOT be called.
func TestStreamStart_PermissionDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sc := &streamStartCapture{streamNo: "should_not_be_used"}
	ba := &BotAPI{
		Log:                 log.NewTLog("BotAPI-stream-it"),
		streamStartOverride: sc.hook,
	}

	body, _ := json.Marshal(config.MessageStreamStartReq{
		ChannelID:   "group_123",
		ChannelType: common.ChannelTypeGroup.Uint8(),
	})

	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/start", body, "app_bot", "", BotKindApp)
	ba.streamStart(c)

	assert.NotEqual(t, http.StatusOK, rec.Code, "App bot GROUP stream must be denied")
	sc.mu.Lock()
	defer sc.mu.Unlock()
	assert.Nil(t, sc.captured, "IMStreamStart must NOT run when permission check fails")
}

func TestStreamStart_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		req  config.MessageStreamStartReq
	}{
		{"missing channel_id", config.MessageStreamStartReq{ChannelType: common.ChannelTypePerson.Uint8()}},
		{"missing channel_type", config.MessageStreamStartReq{ChannelID: "user_creator"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &streamStartCapture{streamNo: "x"}
			ba := &BotAPI{Log: log.NewTLog("BotAPI-stream-it"), streamStartOverride: sc.hook}
			body, _ := json.Marshal(tc.req)
			rec := httptest.NewRecorder()
			c := newStreamTestContext(rec, "/v1/bot/stream/start", body, "bot_X", "user_creator", BotKindUser)
			ba.streamStart(c)
			assert.NotEqual(t, http.StatusOK, rec.Code, "missing required field must be rejected")
			sc.mu.Lock()
			assert.Nil(t, sc.captured, "IMStreamStart must NOT run on a malformed request")
			sc.mu.Unlock()
		})
	}
}

// TestStreamEnd_EmitsTerminalEnd is the OCT-31 terminal-END guarantee: a valid
// stream/end call MUST reach IMStreamEnd with the supplied stream_no. A missing
// END would leave the octo-web bubble stuck "streaming".
func TestStreamEnd_EmitsTerminalEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		botRobotID = "bot_X"
		creatorUID = "user_creator"
		streamNo   = "stream_42"
	)

	ec := &streamEndCapture{}
	ba := &BotAPI{
		Log:               log.NewTLog("BotAPI-stream-it"),
		streamEndOverride: ec.hook,
	}

	body, _ := json.Marshal(config.MessageStreamEndReq{
		StreamNo:    streamNo,
		ChannelID:   creatorUID,
		ChannelType: common.ChannelTypePerson.Uint8(),
	})

	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/end", body, botRobotID, creatorUID, BotKindUser)
	ba.streamEnd(c)

	assert.Equalf(t, http.StatusOK, rec.Code,
		"streamEnd should respond 200, got %d body=%s", rec.Code, rec.Body.String())
	ec.mu.Lock()
	defer ec.mu.Unlock()
	if !assert.NotNil(t, ec.captured, "IMStreamEnd MUST be invoked (terminal END guarantee)") {
		return
	}
	assert.Equal(t, streamNo, ec.captured.StreamNo)
	assert.Equal(t, creatorUID, ec.captured.ChannelID)
}

func TestStreamEnd_MissingStreamNo(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ec := &streamEndCapture{}
	ba := &BotAPI{Log: log.NewTLog("BotAPI-stream-it"), streamEndOverride: ec.hook}
	body, _ := json.Marshal(config.MessageStreamEndReq{
		ChannelID:   "user_creator",
		ChannelType: common.ChannelTypePerson.Uint8(),
	})
	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/end", body, "bot_X", "user_creator", BotKindUser)
	ba.streamEnd(c)

	assert.NotEqual(t, http.StatusOK, rec.Code, "missing stream_no must be rejected")
	ec.mu.Lock()
	assert.Nil(t, ec.captured, "IMStreamEnd must NOT run on a malformed request")
	ec.mu.Unlock()
}

func TestStreamEnd_PermissionDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ec := &streamEndCapture{}
	ba := &BotAPI{Log: log.NewTLog("BotAPI-stream-it"), streamEndOverride: ec.hook}
	body, _ := json.Marshal(config.MessageStreamEndReq{
		StreamNo:    "stream_42",
		ChannelID:   "group_123",
		ChannelType: common.ChannelTypeGroup.Uint8(),
	})
	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/end", body, "app_bot", "", BotKindApp)
	ba.streamEnd(c)

	assert.NotEqual(t, http.StatusOK, rec.Code, "App bot GROUP stream/end must be denied")
	ec.mu.Lock()
	assert.Nil(t, ec.captured, "IMStreamEnd must NOT run when permission check fails")
	ec.mu.Unlock()
}
