package group

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readChannelSynced 直接读 group.channel_synced（Model 结构体不含该列，故走原始查询）。
func readChannelSynced(t *testing.T, ctx *config.Context, groupNo string) (int, bool) {
	t.Helper()
	var vals []int
	_, err := ctx.DB().Select("channel_synced").From("`group`").Where("group_no=?", groupNo).Load(&vals)
	require.NoError(t, err)
	if len(vals) == 0 {
		return 0, false
	}
	return vals[0], true
}

// seedOrphanGroup 直接落一个「已提交但无 IM 频道」的孤儿群：channel_synced=0，
// created_at 设为 ageAgo 之前。这正是「commit 与 IM 创建之间崩溃」或「IM 失败且补偿
// 删除也失败」之后 DB 里残留的状态（octo-server #394）。
func seedOrphanGroup(t *testing.T, ctx *config.Context, groupNo string, status int, ageAgo time.Duration, memberUIDs ...string) {
	t.Helper()
	createdAt := time.Now().Add(-ageAgo)
	_, err := ctx.DB().InsertInto("group").
		Columns("group_no", "name", "creator", "status", "version", "channel_synced", "created_at", "updated_at").
		Values(groupNo, "orphan-"+groupNo, memberUIDs[0], status, 1, 0, createdAt, createdAt).Exec()
	require.NoError(t, err)
	for _, uid := range memberUIDs {
		_, err := ctx.DB().InsertInto("group_member").
			Columns("group_no", "uid", "role", "version", "status", "is_deleted").
			Values(groupNo, uid, MemberRoleCommon, 1, int(common.GroupMemberStatusNormal), 0).Exec()
		require.NoError(t, err)
	}
}

// TestCreateGroup_Success_MarksChannelSynced 成功路径：IM 频道确认后 channel_synced=1。
func TestCreateGroup_Success_MarksChannelSynced(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1")

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1"},
		Name:    "synced-ok",
	})
	require.NoError(t, err)

	got, ok := readChannelSynced(t, svc.(*Service).ctx, resp.GroupNo)
	assert.True(t, ok, "group row must exist")
	assert.Equal(t, 1, got, "channel_synced must be flipped to 1 after IM channel confirmed")
}

// TestCreateGroup_IMFail_CompensatingDeleteRemovesGroup IM 创建失败 + 补偿删除成功：
// 无孤儿残留（窄窗口路径）。注入失败的 imCreateChannel，避免依赖真实 WuKongIM 故障注入。
func TestCreateGroup_IMFail_CompensatingDeleteRemovesGroup(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	insertTestUsers(t, userDB, testutil.UID, "m1")

	s := svc.(*Service)
	s.imCreateChannel = func(*config.ChannelCreateReq) error {
		return errors.New("simulated IM channel create failure")
	}

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: testutil.UID,
		Members: []string{"m1"},
		Name:    "im-fail",
	})
	require.Error(t, err, "CreateGroup must surface IM failure")
	assert.Nil(t, resp, "resp must be nil on IM failure")

	// 补偿删除成功路径：group 行已被清除，没有孤儿。
	var count int
	_, qerr := s.ctx.DB().Select("count(*)").From("`group`").Where("creator=? AND name=?", testutil.UID, "im-fail").Load(&count)
	require.NoError(t, qerr)
	assert.Equal(t, 0, count, "compensating delete should remove the group row")
}

// TestChannelReconcile_RecreatesChannelAndFlipsFlag 直接构造 reconciler 处理孤儿群：
// 重建 IM 频道（捕获调用）+ 翻转 channel_synced=1。覆盖「crash-between-commit-and-IM」
// 与「IM-fail + delete-fail」两种失败模式留下的残留状态。
func TestChannelReconcile_RecreatesChannelAndFlipsFlag(t *testing.T) {
	svc, userDB, ctx := setupServiceTestWithCtx(t)
	insertTestUsers(t, userDB, "ou", "om1")

	groupNo := "orphan-grp-1"
	seedOrphanGroup(t, ctx, groupNo, GroupStatusNormal, 10*time.Minute, "ou", "om1")

	s := svc.(*Service)
	var gotReq *config.ChannelCreateReq
	s.imCreateChannel = func(req *config.ChannelCreateReq) error {
		gotReq = req
		return nil
	}

	r := NewChannelReconciler(s, ReconcileConfig{Interval: time.Minute, GraceSec: 60, BatchSize: 50}, nil)
	require.NoError(t, r.RunOnce(context.Background()))

	require.NotNil(t, gotReq, "reconcile must recreate the IM channel")
	assert.Equal(t, groupNo, gotReq.ChannelID)
	assert.Equal(t, common.ChannelTypeGroup.Uint8(), gotReq.ChannelType)
	assert.ElementsMatch(t, []string{"ou", "om1"}, gotReq.Subscribers)

	got, ok := readChannelSynced(t, ctx, groupNo)
	assert.True(t, ok)
	assert.Equal(t, 1, got, "channel_synced must be flipped to 1 after reconcile")
}

// TestChannelReconcile_Idempotent 第二次 RunOnce 不应再处理已修复的群（幂等 + 可观测）。
func TestChannelReconcile_Idempotent(t *testing.T) {
	svc, userDB, ctx := setupServiceTestWithCtx(t)
	insertTestUsers(t, userDB, "iu")

	groupNo := "orphan-idem"
	seedOrphanGroup(t, ctx, groupNo, GroupStatusNormal, 10*time.Minute, "iu")

	s := svc.(*Service)
	var calls int
	s.imCreateChannel = func(*config.ChannelCreateReq) error { calls++; return nil }

	r := NewChannelReconciler(s, ReconcileConfig{Interval: time.Minute, GraceSec: 60, BatchSize: 50}, nil)
	require.NoError(t, r.RunOnce(context.Background()))
	require.NoError(t, r.RunOnce(context.Background()))

	assert.Equal(t, 1, calls, "second reconcile run must be a no-op (flag already synced)")
}

// TestChannelReconcile_RespectsGraceWindow grace 窗口内的新建群不被找回，避免与正在进行
// 的建群/IM 确认竞争误删/误重建。
func TestChannelReconcile_RespectsGraceWindow(t *testing.T) {
	svc, userDB, ctx := setupServiceTestWithCtx(t)
	insertTestUsers(t, userDB, "gu")

	fresh := "orphan-fresh"
	seedOrphanGroup(t, ctx, fresh, GroupStatusNormal, 1*time.Second, "gu")

	s := svc.(*Service)
	var calls int
	s.imCreateChannel = func(*config.ChannelCreateReq) error { calls++; return nil }

	r := NewChannelReconciler(s, ReconcileConfig{Interval: time.Minute, GraceSec: 120, BatchSize: 50}, nil)
	require.NoError(t, r.RunOnce(context.Background()))

	assert.Equal(t, 0, calls, "groups within the grace window must not be reconciled")
	got, _ := readChannelSynced(t, ctx, fresh)
	assert.Equal(t, 0, got, "fresh pending group stays channel_synced=0 until past grace")
}

// TestChannelReconcile_SkipsDisbandedGroup 已解散群的 IM 频道是被有意销毁的，不应重建。
func TestChannelReconcile_SkipsDisbandedGroup(t *testing.T) {
	svc, userDB, ctx := setupServiceTestWithCtx(t)
	insertTestUsers(t, userDB, "du")

	groupNo := "orphan-disband"
	seedOrphanGroup(t, ctx, groupNo, GroupStatusDisband, 10*time.Minute, "du")

	s := svc.(*Service)
	var calls int
	s.imCreateChannel = func(*config.ChannelCreateReq) error { calls++; return nil }

	r := NewChannelReconciler(s, ReconcileConfig{Interval: time.Minute, GraceSec: 60, BatchSize: 50}, nil)
	require.NoError(t, r.RunOnce(context.Background()))

	assert.Equal(t, 0, calls, "disbanded groups must not be reconciled")
}

// TestChannelReconcile_IMFailLeavesOrphanForRetry 重建 IM 频道失败时保持 channel_synced=0，
// 下个 tick 重试——孤儿不会被错误地标记为已修复。
func TestChannelReconcile_IMFailLeavesOrphanForRetry(t *testing.T) {
	svc, userDB, ctx := setupServiceTestWithCtx(t)
	insertTestUsers(t, userDB, "fu")

	groupNo := "orphan-retry"
	seedOrphanGroup(t, ctx, groupNo, GroupStatusNormal, 10*time.Minute, "fu")

	s := svc.(*Service)
	s.imCreateChannel = func(*config.ChannelCreateReq) error { return errors.New("IM still down") }

	r := NewChannelReconciler(s, ReconcileConfig{Interval: time.Minute, GraceSec: 60, BatchSize: 50}, nil)
	require.NoError(t, r.RunOnce(context.Background()))

	got, ok := readChannelSynced(t, ctx, groupNo)
	assert.True(t, ok)
	assert.Equal(t, 0, got, "channel_synced must stay 0 when IM recreate fails, so next tick retries")
}
