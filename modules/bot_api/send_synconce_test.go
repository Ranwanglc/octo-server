// Package bot_api — PR#483 (OCT-5) send.go header.sync_once 透传测试。
//
// 根因：bot 消息 MsgHeader.SyncOnce=0（读扩散），PC/多端/离线看不到。本测试验证：
//   - 显式传 sync_once=1 → 派发的 MsgHeader.SyncOnce=1（写扩散，多端可见）；
//   - 不传 sync_once → 派发的 MsgHeader.SyncOnce=0（默认不变，不影响其它 bot）；
//   - 显式传 sync_once=0 → 仍为 0（指针区分“未传”与“显式传 0”）。
package bot_api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runSendForSyncOnce(t *testing.T, body []byte) *dispatchCapture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	const (
		botRobotID = "bot_synconce"
		creatorUID = "user_creator_synconce"
	)

	dc := &dispatchCapture{}
	q := &fakeSpaceQuerier{defaultSpace: "space_A"}
	ba := &BotAPI{
		Log:              log.NewTLog("BotAPI-synconce"),
		spaceQuerier:     q,
		dispatchOverride: dc.hook,
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/v1/bot/sendMessage", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(rec)
	gc.Request = httpReq
	c := &wkhttp.Context{Context: gc}
	c.Set(CtxKeyRobotID, botRobotID)
	c.Set(CtxKeyBotKind, BotKindUser)
	c.Set(CtxKeyRobot, &robotModel{RobotID: botRobotID, CreatorUID: creatorUID})

	ba.sendMessage(c)
	require.Equalf(t, http.StatusOK, rec.Code, "sendMessage should respond 200 OK, got %d body=%s", rec.Code, rec.Body.String())

	dc.mu.Lock()
	defer dc.mu.Unlock()
	require.NotNil(t, dc.captured, "dispatch hook must have been invoked")
	return dc
}

func TestSendMessage_SyncOnce_ExplicitOne_WritesFanout(t *testing.T) {
	one := 1
	body, _ := json.Marshal(BotSendMessageReq{
		ChannelID:   "user_creator_synconce",
		ChannelType: common.ChannelTypePerson.Uint8(),
		SyncOnce:    &one,
		Payload:     map[string]interface{}{"content": "hi", "type": 1},
	})
	dc := runSendForSyncOnce(t, body)
	assert.Equal(t, 1, dc.captured.Header.SyncOnce,
		"explicit sync_once=1 MUST set MsgHeader.SyncOnce=1 (写扩散，多端可见)")
}

func TestSendMessage_SyncOnce_Absent_DefaultsToZero(t *testing.T) {
	body, _ := json.Marshal(BotSendMessageReq{
		ChannelID:   "user_creator_synconce",
		ChannelType: common.ChannelTypePerson.Uint8(),
		Payload:     map[string]interface{}{"content": "hi", "type": 1},
	})
	dc := runSendForSyncOnce(t, body)
	assert.Equal(t, 0, dc.captured.Header.SyncOnce,
		"absent sync_once MUST keep MsgHeader.SyncOnce=0 (默认不变，不影响其它 bot)")
}

func TestSendMessage_SyncOnce_ExplicitZero_StaysZero(t *testing.T) {
	zero := 0
	body, _ := json.Marshal(BotSendMessageReq{
		ChannelID:   "user_creator_synconce",
		ChannelType: common.ChannelTypePerson.Uint8(),
		SyncOnce:    &zero,
		Payload:     map[string]interface{}{"content": "hi", "type": 1},
	})
	dc := runSendForSyncOnce(t, body)
	assert.Equal(t, 0, dc.captured.Header.SyncOnce,
		"explicit sync_once=0 stays 0 (读扩散)")
}
