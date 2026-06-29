// Package bot_api - OCT-5 PR#483 review: security tests for POST /v1/bot/ensureFriend.
//
// PR#483 Boss 定稿后的状态：
//   - summary bot UID 改为固定常量 summary_notification（pkg/space.SummaryNotificationBotUID，
//     同时在 SystemBots 中）。SummaryBotUID() 返回该常量，不再读 env。
//   - ensureFriend 不再为 summary bot 插 space_member 行（step4 narrow path）。DM 的
//     Space 归属靠发送路径 X-Space-ID 头 + isBotSpaceAuthorized 的 IsSystemBot 分支解析，
//     bot 无 space_member 行 → 无 Space 能力面 / 不污染 member_count。
package bot_api

import (
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	pkgspace "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// 固定常量 UID（单一真源）。
	efSummaryBotUID   = pkgspace.SummaryNotificationBotUID
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
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	require.Equalf(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	assert.True(t, friendExists(t, ctx, efTargetUID, efSummaryBotUID), "user->bot friend row must exist")
	assert.True(t, friendExists(t, ctx, efSummaryBotUID, efTargetUID), "bot->user friend row must exist")
}

func TestEnsureFriend_SummaryBotMember_Idempotent(t *testing.T) {
	handler, _ := setupEnsureFriendEnv(t)

	for i := 0; i < 3; i++ {
		w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
			map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
		require.Equalf(t, http.StatusOK, w.Code, "call %d body: %s", i, w.Body.String())
	}
}

func TestEnsureFriend_OtherBotForbidden(t *testing.T) {
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efOtherBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	assert.Equalf(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "not allowed to ensure friendships")
	assert.False(t, friendExists(t, ctx, efOtherBotUID, efTargetUID), "rejected bot must not create a friend row")
}

func TestEnsureFriend_NonSpaceMemberForbidden(t *testing.T) {
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efOutsiderUID, "space_id": efSpaceID}))
	assert.Equalf(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "Not a member of this space")
	assert.False(t, friendExists(t, ctx, efSummaryBotUID, efOutsiderUID), "non-member must not be force-friended")
}

// TestEnsureFriend_E2E_NoSpaceMemberRowLanded 闭环 PR#483 Boss 定稿（step4 narrow path）：
// ensureFriend(space_id) 通过 target 成员校验后，**绝不能**为 summary bot 落 space_member
// 行。reviewer 的 🔴 P1 正是：插真实 space_member 行让 bot 获得 Space 能力面 + 污染
// member_count（member_count 是裸 COUNT，不走 SystemBots 过滤）。本测试是该 blocker 修复
// 的负向不变量守卫。
func TestEnsureFriend_E2E_NoSpaceMemberRowLanded(t *testing.T) {
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	require.Equalf(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var count int
	err := ctx.DB().SelectBySql(
		"SELECT COUNT(*) FROM space_member WHERE space_id=? AND uid=?",
		efSpaceID, efSummaryBotUID,
	).LoadOne(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "summary bot must NOT get a space_member row (PR#483 step4 narrow path: no Space capability surface, no member_count pollution)")

	// target 行不受影响（ensureFriend 不动 user 的 space_member）。
	err = ctx.DB().SelectBySql(
		"SELECT COUNT(*) FROM space_member WHERE space_id=? AND uid=? AND status=1",
		efSpaceID, efTargetUID,
	).LoadOne(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "target user's pre-existing space_member row must remain untouched")
}

// TestEnsureFriend_E2E_SystemBotDMAuthorizedBoundToRecipient 是 PR#483 第二轮
// BLOCKING 修复的核心不变量守卫：summary bot 没有 space_member 行，但
// isBotSpaceAuthorized 对 person-channel(DM) 的授权额外要求**接收人是该 Space
// 的活跃成员**（CheckMembership）。
//
//   - 接收人在 spaceA → 带 X-Space-ID=spaceA 授权（DM 不丢 Space 归属）；
//   - 接收人不在 spaceB（但 spaceB active）→ 带 X-Space-ID=spaceB **不授权**
//     （杜绝跨 Space DM 归属注入 — reviewer 🔴 BLOCKING）。
func TestEnsureFriend_E2E_SystemBotDMAuthorizedBoundToRecipient(t *testing.T) {
	_, ctx := setupEnsureFriendEnv(t)

	db := newBotAPIDB(ctx)

	// 无 space_member 行（summary bot 本身）
	var count int
	require.NoError(t, ctx.DB().SelectBySql(
		"SELECT COUNT(*) FROM space_member WHERE space_id=? AND uid=?", efSpaceID, efSummaryBotUID,
	).LoadOne(&count))
	require.Equal(t, 0, count, "precondition: summary bot has no space_member row")

	// efTargetUID 是 efSpaceID 的活跃成员（setupEnsureFriendEnv 已插入）。
	// 发给 target、带 X-Space-ID=efSpaceID、person channel → 授权。
	authorized, err := db.isBotSpaceAuthorized(efSummaryBotUID, efSpaceID, efTargetUID, common.ChannelTypePerson.Uint8())
	require.NoError(t, err)
	assert.True(t, authorized, "system bot DM to a recipient who IS a member of the claimed active Space must be authorized (DM keeps correct Space attribution)")

	// 跨 Space 注入漏洞的核心负向用例：另一个 active Space（spaceB），但 target
	// 不是 spaceB 成员 → 带 X-Space-ID=spaceB 发给 target **不**能授权。
	_, err = ctx.DB().InsertBySql(
		"INSERT INTO `space` (space_id, name, status, version) VALUES (?, ?, 1, 1)",
		"space_other_active_ef", "other active space").Exec()
	require.NoError(t, err)
	crossSpaceAuthorized, err := db.isBotSpaceAuthorized(efSummaryBotUID, "space_other_active_ef", efTargetUID, common.ChannelTypePerson.Uint8())
	require.NoError(t, err)
	assert.False(t, crossSpaceAuthorized, "🔴 BLOCKING: system bot DM must NOT be authorized to inject an active Space the recipient is NOT a member of (cross-Space DM attribution injection)")

	// 接收人不在该 Space（outsider，也不在任何 Space）→ 带 X-Space-ID=efSpaceID 不授权。
	outsiderAuthorized, err := db.isBotSpaceAuthorized(efSummaryBotUID, efSpaceID, efOutsiderUID, common.ChannelTypePerson.Uint8())
	require.NoError(t, err)
	assert.False(t, outsiderAuthorized, "system bot DM to a non-member recipient must NOT be authorized even if the claimed Space is active")

	// 非活跃 Space → 不授权（防越权到 disabled Space；CheckMembership 自身校 active）。
	_, err = ctx.DB().InsertBySql(
		"INSERT INTO `space` (space_id, name, status, version) VALUES (?, ?, 0, 1)",
		"space_disabled_ef", "disabled").Exec()
	require.NoError(t, err)
	_, err = ctx.DB().InsertBySql(
		"INSERT INTO space_member (space_id, uid, role, status, version) VALUES (?, ?, 0, 1, 1)",
		"space_disabled_ef", efTargetUID).Exec()
	require.NoError(t, err)
	authorizedDisabled, err := db.isBotSpaceAuthorized(efSummaryBotUID, "space_disabled_ef", efTargetUID, common.ChannelTypePerson.Uint8())
	require.NoError(t, err)
	assert.False(t, authorizedDisabled, "system bot must NOT be authorized into a disabled (status=0) Space even when the recipient has a space_member row there")

	// 非 person channel（群）：维持原 active 校验（不绑定接收人）。
	groupAuthorized, err := db.isBotSpaceAuthorized(efSummaryBotUID, efSpaceID, "", common.ChannelTypeGroup.Uint8())
	require.NoError(t, err)
	assert.True(t, groupAuthorized, "non-person channel keeps the original active-Space authorization for system bots")
}

// TestEnsureFriend_E2E_SendPathPreservesAuthoritativeSpaceID is the contract test
// Jerry-Xin asked for, updated for the step4 narrow path: ensureFriend(space_id)
// followed by the bot_api send-path space-injection helpers MUST preserve the
// authoritative space_id on the X-Space-ID header path, even though the summary
// bot has NO space_member row (the row was removed in step4). DM keeps its Space
// attribution via the IsSystemBot authorization branch.
//
// Note: the no-header bare-send fallback (querySpaceIDsByRobotID) now resolves
// "" for the summary bot (no space_member row) and therefore strips — this is
// acceptable because smart-summary's only send shape carries the X-Space-ID
// header (SpaceID from SummaryTask.SpaceID). See report step4 for the rationale.
func TestEnsureFriend_E2E_SendPathPreservesAuthoritativeSpaceID(t *testing.T) {
	handler, ctx := setupEnsureFriendEnv(t)

	w := doBot(handler, botReq(t, "POST", "/v1/bot/ensureFriend", efSummaryBotToken,
		map[string]interface{}{"target_uid": efTargetUID, "space_id": efSpaceID}))
	require.Equalf(t, http.StatusOK, w.Code, "ensureFriend pre-step body: %s", w.Body.String())

	ba := &BotAPI{
		ctx: ctx,
		db:  newBotAPIDB(ctx),
		Log: log.NewTLog("BotAPI-ef-e2e"),
	}

	// Path A: X-Space-ID header (the smart-summary send shape) — honored via the
	// IsSystemBot DM authorization branch because the recipient (efTargetUID) IS
	// a member of efSpaceID, even though the summary bot has no space_member row.
	cHeader := fakeWkContextWithHeader("X-Space-ID", efSpaceID)
	gotHeader := ba.resolveBotActiveSpaceID(cHeader, efSummaryBotUID, efTargetUID, common.ChannelTypePerson.Uint8())
	assert.Equalf(t, efSpaceID, gotHeader,
		"X-Space-ID header path must be honored for a system bot DM when the recipient is a member (PR#483 step4 narrow path + BLOCKING recipient binding)")

	payloadHeader := map[string]interface{}{"content": "hi", "space_id": "spaceForged"}
	enrichedHeader := ba.enrichBotPayloadWithSpaceID(cHeader, efSummaryBotUID, efTargetUID, common.ChannelTypePerson.Uint8(), payloadHeader)
	assert.Equalf(t, efSpaceID, enrichedHeader["space_id"],
		"enrich must overwrite any client-supplied space_id with the server-authoritative one")
}

// TestEnsureFriend_E2E_CrossSpaceDMAttributionInjectionBlocked 是 PR#483 第二轮
// 🔴 BLOCKING 的端到端负向守卫（跨 Space DM 归属注入）：
//
// 场景：userA 在 spaceA；spaceB active 但 userA 不在 spaceB。攻击者让
// summary_notification 带 X-Space-ID=spaceB 发 DM 给 userA，并在 payload 里伪造
// space_id。修复前：isBotSpaceAuthorized 系统 bot 分支只校 spaceB active → 采纳
// header → userA 的私信被错误归属到 spaceB。修复后：person channel 额外要求
// CheckMembership(spaceB, userA)=true，userA 不在 spaceB → header 不被采纳 →
// fall through 到 deterministic DB query（summary bot 无 space_member 行 → ""）→
// enrich strip（fail-closed），绝不注入 spaceB。
func TestEnsureFriend_E2E_CrossSpaceDMAttributionInjectionBlocked(t *testing.T) {
	_, ctx := setupEnsureFriendEnv(t)

	// spaceB active，但 efTargetUID(userA) 不是其成员。
	const spaceB = "space_b_cross_inject_ef"
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO `space` (space_id, name, status, version) VALUES (?, ?, 1, 1)",
		spaceB, "space B active").Exec()
	require.NoError(t, err)

	ba := &BotAPI{
		ctx: ctx,
		db:  newBotAPIDB(ctx),
		Log: log.NewTLog("BotAPI-ef-cross"),
	}

	// 带 X-Space-ID=spaceB 发给 userA（person channel，channel_id=userA）。
	cHeader := fakeWkContextWithHeader("X-Space-ID", spaceB)
	got := ba.resolveBotActiveSpaceID(cHeader, efSummaryBotUID, efTargetUID, common.ChannelTypePerson.Uint8())
	assert.NotEqualf(t, spaceB, got,
		"🔴 BLOCKING: cross-Space DM attribution injection — X-Space-ID=spaceB must NOT be honored for a DM to a recipient who is not a spaceB member")
	assert.Equalf(t, "", got,
		"summary bot has no space_member row, so after rejecting the forged header the resolver must fall through to \"\" (fail-closed strip)")

	// enrich 必须 strip 任何 client 伪造的 space_id，绝不注入 spaceB。
	payload := map[string]interface{}{"content": "hi", "space_id": spaceB}
	enriched := ba.enrichBotPayloadWithSpaceID(cHeader, efSummaryBotUID, efTargetUID, common.ChannelTypePerson.Uint8(), payload)
	_, ok := enriched["space_id"]
	assert.Falsef(t, ok,
		"🔴 BLOCKING: payload.space_id must be stripped (not set to spaceB) when the DM recipient is not a member of the claimed Space")
}
