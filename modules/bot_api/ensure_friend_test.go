// Package bot_api - OCT-5 PR#483 review: security tests for POST /v1/bot/ensureFriend.
package bot_api

import (
	"net/http"
	"os"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	efSummaryBotUID   = "u_summary_assistant"
	efSummaryBotToken = "bf_summary_ensure_friend_token"
	efOtherBotUID     = "u_other_bot"
	efOtherBotToken   = "bf_other_bot_token"
	efCreatorUID      = "u_ef_creator"
	efTargetUID       = "u_ef_target"
	efOutsiderUID     = "u_ef_outsider"
	efSpaceID         = "space_ef_1"
)

func setupEnsureFriendEnv(t *testing.T) (http.Handler, *config.Context) {
	t.Helper()
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, status, creator_uid, bot_token, version) VALUES (?, 1, ?, ?, 1)",
		efSummaryBotUID, efCreatorUID, efSummaryBotToken).Exec()
	require.NoError(t, err)

	_, err = ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, status, creator_uid, bot_token, version) VALUES (?, 1, ?, ?, 1)",
		efOtherBotUID, efCreatorUID, efOtherBotToken).Exec()
	require.NoError(t, err)

	for _, uid := range []string{efTargetUID, efOutsiderUID, efSummaryBotUID, efOtherBotUID} {
		_, err = ctx.DB().InsertBySql(
			"INSERT INTO `user` (uid, name, username, short_no, vercode, version) VALUES (?, ?, ?, ?, ?, 1)",
			uid, uid, uid, uid, util.GenerUUID()).Exec()
		require.NoError(t, err)
	}

	_, err = ctx.DB().InsertBySql(
		"INSERT INTO `space` (space_id, name, status, version) VALUES (?, ?, 1, 1)",
		efSpaceID, "ensure friend space").Exec()
	require.NoError(t, err)
	_, err = ctx.DB().InsertBySql(
		"INSERT INTO space_member (space_id, uid, role, status, version) VALUES (?, ?, 0, 1, 1)",
		efSpaceID, efTargetUID).Exec()
	require.NoError(t, err)

	return s.GetRoute(), ctx
}

func friendExists(t *testing.T, ctx *config.Context, uid, toUID string) bool {
	t.Helper()
	var count int
	err := ctx.DB().SelectBySql(
		"SELECT COUNT(*) FROM friend WHERE uid=? AND to_uid=? AND is_deleted=0", uid, toUID,
	).LoadOne(&count)
	require.NoError(t, err)
	return count > 0
}

func TestEnsureFriend_SummaryBotMember_OK(t *testing.T) {
	t.Setenv("SUMMARY_BOT_UID", efSummaryBotUID)
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	require.Equalf(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	assert.True(t, friendExists(t, ctx, efTargetUID, efSummaryBotUID), "user->bot friend row must exist")
	assert.True(t, friendExists(t, ctx, efSummaryBotUID, efTargetUID), "bot->user friend row must exist")
}

func TestEnsureFriend_SummaryBotMember_Idempotent(t *testing.T) {
	t.Setenv("SUMMARY_BOT_UID", efSummaryBotUID)
	handler, _ := setupEnsureFriendEnv(t)

	for i := 0; i < 3; i++ {
		w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
			map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
		require.Equalf(t, http.StatusOK, w.Code, "call %d body: %s", i, w.Body.String())
	}
}

func TestEnsureFriend_OtherBotForbidden(t *testing.T) {
	t.Setenv("SUMMARY_BOT_UID", efSummaryBotUID)
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efOtherBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	assert.Equalf(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "not allowed to ensure friendships")
	assert.False(t, friendExists(t, ctx, efOtherBotUID, efTargetUID), "rejected bot must not create a friend row")
}

func TestEnsureFriend_UnconfiguredForbidden(t *testing.T) {
	require.NoError(t, os.Unsetenv("SUMMARY_BOT_UID"))
	handler, _ := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	assert.Equalf(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "not allowed to ensure friendships")
}

func TestEnsureFriend_NonSpaceMemberForbidden(t *testing.T) {
	t.Setenv("SUMMARY_BOT_UID", efSummaryBotUID)
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efOutsiderUID, "space_id": efSpaceID}))
	assert.Equalf(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "Not a member of this space")
	assert.False(t, friendExists(t, ctx, efSummaryBotUID, efOutsiderUID), "non-member must not be force-friended")
}
