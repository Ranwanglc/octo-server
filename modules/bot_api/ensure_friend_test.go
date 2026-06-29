// Package bot_api - OCT-5 PR#483 review: security tests for POST /v1/bot/ensureFriend.
package bot_api

import (
	"net/http"
	"os"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
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

// TestEnsureFriend_E2E_SpaceMemberLanded 闭环 PR#483 Jerry-Xin 提的 🔴 blocker：
// ensureFriend(space_id) 通过 target 成员校验后，必须为 summary bot 在该 Space
// 落下 space_member 行（role=0 最小权限，status=1 active）。这是后续
// /v1/bot/sendMessage 的 resolveBotActiveSpaceID 能解析到权威 Space 上下文的
// 唯一前提（querySpaceIDsByRobotID 走的就是这张表）。
func TestEnsureFriend_E2E_SpaceMemberLanded(t *testing.T) {
	t.Setenv("SUMMARY_BOT_UID", efSummaryBotUID)
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	require.Equalf(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var count int
	err := ctx.DB().SelectBySql(
		"SELECT COUNT(*) FROM space_member WHERE space_id=? AND uid=? AND status=1",
		efSpaceID, efSummaryBotUID,
	).LoadOne(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "summary bot must have a space_member row in the ensureFriend-validated Space (PR#483 blocker fix)")

	// target 行不受影响（ensureFriend 不动 user 的 space_member）。
	err = ctx.DB().SelectBySql(
		"SELECT COUNT(*) FROM space_member WHERE space_id=? AND uid=? AND status=1",
		efSpaceID, efTargetUID,
	).LoadOne(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "target user's pre-existing space_member row must remain untouched")

	// PR#483 Jerry-Xin 提的契约（B1）：space_member 落库后 isBotSpaceAuthorized
	// 必须直接对 (summary_bot, space) 返回 true —— 这是 X-Space-ID 头被采纳的前置
	// 条件，也是 send 路径"载 payload.space_id 之前要查的那一步"。
	db := newBotAPIDB(ctx)
	authorized, err := db.isBotSpaceAuthorized(efSummaryBotUID, efSpaceID)
	require.NoError(t, err)
	assert.True(t, authorized, "isBotSpaceAuthorized must return true for the summary bot after ensureFriend lands the space_member row (PR#483 B1 contract)")
}

// TestEnsureFriend_E2E_SpaceMemberIdempotent 重复 ensureFriend 不重复插入也不
// 把已被运维移除的成员行复活（INSERT IGNORE 语义）。
func TestEnsureFriend_E2E_SpaceMemberIdempotent(t *testing.T) {
	t.Setenv("SUMMARY_BOT_UID", efSummaryBotUID)
	handler, ctx := setupEnsureFriendEnv(t)

	for i := 0; i < 3; i++ {
		w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
			map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
		require.Equalf(t, http.StatusOK, w.Code, "call %d body: %s", i, w.Body.String())
	}

	var count int
	err := ctx.DB().SelectBySql(
		"SELECT COUNT(*) FROM space_member WHERE space_id=? AND uid=?",
		efSpaceID, efSummaryBotUID,
	).LoadOne(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "repeated ensureFriend calls must not duplicate the summary bot's space_member row")
}

// TestEnsureFriend_E2E_SendPathPreservesAuthoritativeSpaceID is the contract test
// Jerry-Xin asked for: ensureFriend(space_id) followed by the bot_api send-path
// space-injection helpers MUST preserve the authoritative space_id, instead of
// stripping it because the summary bot has no app_bot row and (before this fix)
// no space_member row either.
//
// We exercise the real resolver + enricher on a real DB session (no stubs) so
// the test fails exactly the way production would have: stripped payload →
// missing Space attribution. Both paths are checked:
//   - X-Space-ID header → isBotSpaceAuthorized (uses space_member query 1)
//   - no header → querySpaceIDsByRobotID fallback
func TestEnsureFriend_E2E_SendPathPreservesAuthoritativeSpaceID(t *testing.T) {
	t.Setenv("SUMMARY_BOT_UID", efSummaryBotUID)
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	require.Equalf(t, http.StatusOK, w.Code, "ensureFriend pre-step body: %s", w.Body.String())

	// Build a real BotAPI bound to the same test DB so resolveBotActiveSpaceID
	// and enrichBotPayloadWithSpaceID exercise production SQL — not a fake.
	ba := &BotAPI{
		ctx: ctx,
		db:  newBotAPIDB(ctx),
		Log: log.NewTLog("BotAPI-ef-e2e"),
	}

	// Path A: X-Space-ID header (the most likely smart-summary send shape).
	cHeader := fakeWkContextWithHeader("X-Space-ID", efSpaceID)
	gotHeader := ba.resolveBotActiveSpaceID(cHeader, efSummaryBotUID)
	assert.Equalf(t, efSpaceID, gotHeader,
		"X-Space-ID header path must be honored after ensureFriend lands the space_member row (PR#483 blocker)")

	payloadHeader := map[string]interface{}{"content": "hi", "space_id": "spaceForged"}
	enrichedHeader := ba.enrichBotPayloadWithSpaceID(cHeader, efSummaryBotUID, payloadHeader)
	assert.Equalf(t, efSpaceID, enrichedHeader["space_id"],
		"enrich must overwrite any client-supplied space_id with the server-authoritative one")

	// Path B: no header → fallback to querySpaceIDsByRobotID (the bare send path).
	cNoHeader := fakeWkContext()
	gotFallback := ba.resolveBotActiveSpaceID(cNoHeader, efSummaryBotUID)
	assert.Equalf(t, efSpaceID, gotFallback,
		"DB fallback path must also resolve the Space after ensureFriend lands the space_member row")

	payloadFallback := map[string]interface{}{"content": "hi"}
	enrichedFallback := ba.enrichBotPayloadWithSpaceID(cNoHeader, efSummaryBotUID, payloadFallback)
	assert.Equalf(t, efSpaceID, enrichedFallback["space_id"],
		"enrich must inject the resolved Space on the no-header path (no fail-closed strip)")
}

