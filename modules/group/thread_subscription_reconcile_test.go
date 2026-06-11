package group

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// imCall 记录一次 removeFn 调用，供测试断言 dry-run 不写 / apply 实际摘除 / 幂等重跑。
type imCall struct {
	channelID string
	uids      []string
}

// newTestReconciler 构造一个用假 removeFn 的对账器：把每次摘订阅调用录进 calls，
// 无需真实 WuKongIM 即可验证扫描 / dry-run / apply / 幂等逻辑。
func newTestReconciler(t *testing.T) (*ThreadSubscriptionReconciler, *[]imCall) {
	t.Helper()
	svc, _ := setupServiceTest(t)
	s := svc.(*Service)
	f := New(s.ctx)
	ensureThreadTables(t, f)

	calls := &[]imCall{}
	r := NewThreadSubscriptionReconciler(s.ctx, s.Log)
	r.removeFn = func(channelID string, uids []string) error {
		*calls = append(*calls, imCall{channelID: channelID, uids: append([]string(nil), uids...)})
		return nil
	}
	return r, calls
}

func insertThread(t *testing.T, r *ThreadSubscriptionReconciler, shortID, groupNo string, status int) {
	t.Helper()
	_, err := r.ctx.DB().InsertInto("thread").
		Columns("short_id", "group_no", "name", "creator_uid", "status", "version").
		Values(shortID, groupNo, shortID, "owner", status, 1).Exec()
	require.NoError(t, err)
}

func insertMember(t *testing.T, r *ThreadSubscriptionReconciler, groupNo, uid string, isDeleted, status int) {
	t.Helper()
	_, err := r.ctx.DB().InsertInto("group_member").
		Columns("group_no", "uid", "is_deleted", "status", "version").
		Values(groupNo, uid, isDeleted, status, 1).Exec()
	require.NoError(t, err)
}

// TestReconcile_ScansLeakedMembersAndThreads 覆盖核心扫描逻辑：
// is_deleted=1 与 status=blacklist 的成员都要被识别为泄漏，正常成员被排除；
// 计划摘除对数 = 泄漏成员数 × 该群非 deleted 子区数。
func TestReconcile_ScansLeakedMembersAndThreads(t *testing.T) {
	r, calls := newTestReconciler(t)
	const groupNo = "g_scan"

	// 两个非 deleted 子区（active + archived）+ 一个 deleted（必须排除）
	insertThread(t, r, "th_active", groupNo, 1)
	insertThread(t, r, "th_archived", groupNo, 2)
	insertThread(t, r, "th_deleted", groupNo, 3)

	// 泄漏成员：被踢(is_deleted=1) + 被拉黑(status=blacklist)
	insertMember(t, r, groupNo, "u_kicked", 1, int(common.GroupMemberStatusNormal))
	insertMember(t, r, groupNo, "u_black", 0, int(common.GroupMemberStatusBlacklist))
	// 正常成员：不应被摘
	insertMember(t, r, groupNo, "u_normal", 0, int(common.GroupMemberStatusNormal))

	report, err := r.Run(ReconcileOptions{Apply: true})
	require.NoError(t, err)

	assert.Equal(t, 1, report.GroupsAffected)
	assert.Equal(t, 2, report.LeakedMembers)
	assert.Equal(t, 2, report.ThreadsScanned)
	assert.Equal(t, 4, report.PairsPlanned, "2 泄漏成员 × 2 非 deleted 子区")
	assert.Equal(t, 4, report.PairsRemoved)
	assert.Empty(t, report.Failures)

	// 断言只摘了泄漏成员、且只在非 deleted 子区频道
	removed := map[string]map[string]bool{}
	for _, c := range *calls {
		if removed[c.channelID] == nil {
			removed[c.channelID] = map[string]bool{}
		}
		for _, u := range c.uids {
			removed[c.channelID][u] = true
		}
	}
	for _, ch := range []string{groupNo + "____th_active", groupNo + "____th_archived"} {
		assert.True(t, removed[ch]["u_kicked"], "被踢成员应在 %s 被摘", ch)
		assert.True(t, removed[ch]["u_black"], "被拉黑成员应在 %s 被摘", ch)
		assert.False(t, removed[ch]["u_normal"], "正常成员不应被摘")
	}
	assert.Nil(t, removed[groupNo+"____th_deleted"], "deleted 子区频道不应被触碰")
}

// TestReconcile_DryRunDoesNotWrite 是验收要求的核心：默认 dry-run 只统计，绝不调用 removeFn。
func TestReconcile_DryRunDoesNotWrite(t *testing.T) {
	r, calls := newTestReconciler(t)
	const groupNo = "g_dryrun"

	insertThread(t, r, "th1", groupNo, 1)
	insertThread(t, r, "th2", groupNo, 1)
	insertMember(t, r, groupNo, "u_kicked", 1, int(common.GroupMemberStatusNormal))
	insertMember(t, r, groupNo, "u_black", 0, int(common.GroupMemberStatusBlacklist))

	report, err := r.Run(ReconcileOptions{Apply: false})
	require.NoError(t, err)

	assert.True(t, report.DryRun)
	assert.Equal(t, 4, report.PairsPlanned, "dry-run 仍要算出将摘除多少对")
	assert.Equal(t, 0, report.PairsRemoved, "dry-run 不得实际摘除")
	assert.Equal(t, 0, report.IMCalls)
	assert.Empty(t, *calls, "dry-run 绝不能调用 IMRemoveSubscriber")
}

// TestReconcile_Idempotent 验证重复执行安全：连续两次 apply，第二次行为与第一次一致
// （IMRemoveSubscriber 对不存在订阅是 no-op，故工具侧每次都照常下发、统计稳定）。
func TestReconcile_Idempotent(t *testing.T) {
	r, calls := newTestReconciler(t)
	const groupNo = "g_idem"

	insertThread(t, r, "th1", groupNo, 1)
	insertMember(t, r, groupNo, "u_black", 0, int(common.GroupMemberStatusBlacklist))

	r1, err := r.Run(ReconcileOptions{Apply: true})
	require.NoError(t, err)
	firstCalls := len(*calls)

	r2, err := r.Run(ReconcileOptions{Apply: true})
	require.NoError(t, err)

	assert.Equal(t, r1.PairsRemoved, r2.PairsRemoved, "重跑摘除对数应一致")
	assert.Equal(t, r1.PairsPlanned, r2.PairsPlanned)
	assert.Equal(t, firstCalls*2, len(*calls), "重跑安全：第二次照常下发，无异常")
}

// TestReconcile_DedupesMemberAcrossDeletedAndBlacklist 验证同一成员同时 is_deleted=1
// 且 blacklist 时只算一次（OR 条件可能让聚合前重复出现）。
func TestReconcile_DedupesMemberAcrossDeletedAndBlacklist(t *testing.T) {
	r, _ := newTestReconciler(t)
	const groupNo = "g_dedup"

	insertThread(t, r, "th1", groupNo, 1)
	// 一个成员既被踢又被拉黑
	insertMember(t, r, groupNo, "u_both", 1, int(common.GroupMemberStatusBlacklist))

	report, err := r.Run(ReconcileOptions{Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, report.LeakedMembers, "同一成员只算一次")
	assert.Equal(t, 1, report.PairsRemoved)
}

// TestReconcile_FailureRecordedNotAborted 验证“失败只记录不中断”：某次摘除报错时，
// 其余订阅对仍继续处理，失败被收进 report.Failures。
func TestReconcile_FailureRecordedNotAborted(t *testing.T) {
	r, _ := newTestReconciler(t)
	const groupNo = "g_fail"

	insertThread(t, r, "th_bad", groupNo, 1)
	insertThread(t, r, "th_ok", groupNo, 1)
	insertMember(t, r, groupNo, "u_black", 0, int(common.GroupMemberStatusBlacklist))

	badChannel := groupNo + "____th_bad"
	r.removeFn = func(channelID string, uids []string) error {
		if channelID == badChannel {
			return errors.New("boom")
		}
		return nil
	}

	report, err := r.Run(ReconcileOptions{Apply: true})
	require.NoError(t, err)
	require.Len(t, report.Failures, 1)
	assert.Equal(t, badChannel, report.Failures[0].ChannelID)
	assert.Equal(t, 1, report.PairsRemoved, "失败不影响其余子区继续摘除")
}

// TestReconcile_NoLeakedMembers 空结果：没有泄漏成员时报告全 0、无调用。
func TestReconcile_NoLeakedMembers(t *testing.T) {
	r, calls := newTestReconciler(t)
	const groupNo = "g_clean"

	insertThread(t, r, "th1", groupNo, 1)
	insertMember(t, r, groupNo, "u_normal", 0, int(common.GroupMemberStatusNormal))

	report, err := r.Run(ReconcileOptions{Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 0, report.GroupsAffected)
	assert.Equal(t, 0, report.PairsPlanned)
	assert.Empty(t, *calls)
}

// TestReconcile_BatchingSplitsUIDs 验证大群分批：batch-size 小于泄漏成员数时，
// 同一子区频道的摘除拆成多次调用，但摘除总对数不变。
func TestReconcile_BatchingSplitsUIDs(t *testing.T) {
	r, calls := newTestReconciler(t)
	const groupNo = "g_batch"

	insertThread(t, r, "th1", groupNo, 1)
	insertMember(t, r, groupNo, "u1", 1, int(common.GroupMemberStatusNormal))
	insertMember(t, r, groupNo, "u2", 1, int(common.GroupMemberStatusNormal))
	insertMember(t, r, groupNo, "u3", 1, int(common.GroupMemberStatusNormal))

	report, err := r.Run(ReconcileOptions{Apply: true, BatchSize: 2})
	require.NoError(t, err)
	assert.Equal(t, 3, report.PairsRemoved)
	assert.Equal(t, 2, report.IMCalls, "3 个 uid / 批大小 2 = 2 次调用")
	assert.Len(t, *calls, 2)
}
