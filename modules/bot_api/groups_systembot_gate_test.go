// Package bot_api — PR#483 (OCT-5) 能力门 A 测试：GET /v1/bot/space/members 对系统 bot
// （如 summary_notification）必须拒绝枚举 Space 成员，即使该 bot 有 space_member 行。
package bot_api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	pkgspace "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBotSpaceMembers_SystemBotEnumerationDenied(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	const (
		sysBotUID   = pkgspace.SummaryNotificationBotUID // summary_notification (system bot)
		sysBotToken = "bf_systembot_gateA_token"
		spaceID     = "space_gateA_1"
		memberUID   = "u_gateA_member"
	)

	// 系统 bot 的 robot 行。
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, status, creator_uid, bot_token, version) VALUES (?, 1, ?, ?, 1)",
		sysBotUID, "u_gateA_creator", sysBotToken).Exec()
	require.NoError(t, err)

	for _, uid := range []string{sysBotUID, memberUID} {
		_, err = ctx.DB().InsertBySql(
			"INSERT INTO `user` (uid, name, username, short_no, vercode, version) VALUES (?, ?, ?, ?, ?, 1)",
			uid, uid, uid, uid, util.GenerUUID()).Exec()
		require.NoError(t, err)
	}

	_, err = ctx.DB().InsertBySql(
		"INSERT INTO `space` (space_id, name, status, version) VALUES (?, ?, 1, 1)",
		spaceID, "gateA space").Exec()
	require.NoError(t, err)
	// 关键：即使系统 bot + 一个真实成员都有 space_member 行……
	for _, uid := range []string{sysBotUID, memberUID} {
		_, err = ctx.DB().InsertBySql(
			"INSERT INTO space_member (space_id, uid, role, status, version) VALUES (?, ?, 0, 1, 1)",
			spaceID, uid).Exec()
		require.NoError(t, err)
	}

	handler := s.GetRoute()

	// 系统 bot 调用枚举：必须返回空列表（被能力门 A 拒绝），不得据 space_member 行枚举出成员。
	w := doBot(handler, botReq(t, "GET", "/v1/bot/space/members?space_id="+spaceID, sysBotToken, nil))
	require.Equalf(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var members []map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &members))
	assert.Empty(t, members, "system bot must NOT be able to enumerate Space members even with a space_member row (PR#483 gate A)")
}
