package group

import (
	"fmt"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"go.uber.org/zap"
)

// 存量子区订阅泄漏一次性对账工具（Issue YUJ-4186，承接 #27/#332/YUJ-4185 根因调查）。
//
// 背景：#27/#332 + YUJ-4185 都是“事件驱动”修复，只在“未来发生移除”时摘子区订阅。代码库里
// 没有任何 backfill/reconcile migration，所以 bug 期间已经泄漏的存量订阅会原地留存：
//   - 已被踢出/退群的人(is_deleted=1)：当年入群时挂进 WuKongIM 的子区订阅从没被摘。
//   - 被拉黑的人(status=blacklist, is_deleted=0)：GetMembers 不过滤 status，即使 WuKongIM
//     重载也永不自愈。
//
// 本工具扫所有群的 group_member，找出 is_deleted=1 OR status=blacklist 的成员，对每个
// (group_no, uid) 复用 queryThreadShortIDsForCleanup 查该群所有非 deleted 子区，逐个对子区
// 频道 IMRemoveSubscriber 该 uid。
//
// 设计约束（验收要求“只摘订阅不删数据，无需回滚”）：本工具刻意只调 IMRemoveSubscriber，
// 不删 thread_member / thread_setting 行、不动 pinned / conversation_ext —— 与被踢/退群路径上
// 的 removeUserFromGroupThreadsCleanup（会删 DB 行）区别开。订阅泄漏是“多挂了订阅”，对账只需
// 摘掉这份越权订阅；DB 行是否残留属于另一类问题，不在本单范围内，也避免误删带来的回滚风险。
//
// 幂等：IMRemoveSubscriber 对不存在的订阅是 no-op，重复执行安全。
// dry-run：默认只统计将摘除多少 (uid, 子区) 订阅对，不实际调用；ReconcileOptions.Apply=true 才执行。

// ReconcileOptions 控制对账工具的执行模式与限速。
type ReconcileOptions struct {
	// Apply=false（默认）为 dry-run：只扫描统计，绝不调用 IMRemoveSubscriber。
	// Apply=true 才真正摘订阅。
	Apply bool
	// BatchSize 单次 IMRemoveSubscriber 调用最多携带多少个 uid（同一子区频道下分批），
	// 用于避免单次请求 body 过大。<=0 时回退到 defaultReconcileBatchSize。
	BatchSize int
	// Interval 每次 IMRemoveSubscriber 调用之间的休眠，用于限速避免打爆 WuKongIM。
	// <=0 时不休眠。
	Interval time.Duration
}

const defaultReconcileBatchSize = 100

// ReconcileFailure 记录单次摘订阅调用失败（失败只记录不中断）。
type ReconcileFailure struct {
	GroupNo   string
	ChannelID string
	UIDs      []string
	Err       string
}

// ReconcileReport 对账执行报告。
type ReconcileReport struct {
	DryRun         bool
	GroupsAffected int                // 含至少一个泄漏成员的群数
	LeakedMembers  int                // 泄漏的 (group, uid) 去重对数
	ThreadsScanned int                // 受影响群下非 deleted 子区总数
	PairsPlanned   int                // 计划摘除的 (uid, 子区) 订阅对总数
	PairsRemoved   int                // 实际摘除成功的订阅对数（dry-run 恒为 0）
	IMCalls        int                // 实际发起的 IMRemoveSubscriber 调用次数
	Failures       []ReconcileFailure // 失败项（不中断）
}

// String 渲染人类可读的报告。
func (r *ReconcileReport) String() string {
	mode := "APPLY"
	if r.DryRun {
		mode = "DRY-RUN (未实际摘除任何订阅；加 --apply 才执行)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "子区订阅泄漏对账报告 [%s]\n", mode)
	fmt.Fprintf(&b, "  受影响群数:        %d\n", r.GroupsAffected)
	fmt.Fprintf(&b, "  泄漏成员数(去重):  %d\n", r.LeakedMembers)
	fmt.Fprintf(&b, "  扫描子区数:        %d\n", r.ThreadsScanned)
	fmt.Fprintf(&b, "  计划摘除订阅对:    %d\n", r.PairsPlanned)
	if !r.DryRun {
		fmt.Fprintf(&b, "  实际摘除订阅对:    %d\n", r.PairsRemoved)
		fmt.Fprintf(&b, "  IM 调用次数:       %d\n", r.IMCalls)
	}
	fmt.Fprintf(&b, "  失败项:            %d\n", len(r.Failures))
	for _, f := range r.Failures {
		fmt.Fprintf(&b, "    - group=%s channel=%s uids=%d err=%s\n",
			f.GroupNo, f.ChannelID, len(f.UIDs), f.Err)
	}
	return b.String()
}

// ThreadSubscriptionReconciler 是存量子区订阅泄漏的一次性对账器。
type ThreadSubscriptionReconciler struct {
	ctx    *config.Context
	logger log.Log
	// removeFn 摘除某子区频道下一批 uid 的订阅。默认包一层 ctx.IMRemoveSubscriber，
	// 抽成字段是为了让单测无需真实 WuKongIM 也能验证“扫描 + 幂等 + dry-run 不写”逻辑。
	removeFn func(channelID string, uids []string) error
}

// RunThreadSubscriptionReconcile 是给 main.go 子命令用的导出入口：构造对账器并执行一次，
// 返回报告。把入口放在 group 包内是为了复用包内未导出的 queryThreadShortIDsForCleanup，
// 不把内部清理逻辑外泄到 cmd 层。
func RunThreadSubscriptionReconcile(ctx *config.Context, logger log.Log, opts ReconcileOptions) (*ReconcileReport, error) {
	return NewThreadSubscriptionReconciler(ctx, logger).Run(opts)
}

// NewThreadSubscriptionReconciler 构造对账器。logger 由调用方传入以保留 module 标签。
func NewThreadSubscriptionReconciler(ctx *config.Context, logger log.Log) *ThreadSubscriptionReconciler {
	r := &ThreadSubscriptionReconciler{ctx: ctx, logger: logger}
	r.removeFn = func(channelID string, uids []string) error {
		return ctx.IMRemoveSubscriber(&config.SubscriberRemoveReq{
			ChannelID:   channelID,
			ChannelType: common.ChannelTypeCommunityTopic.Uint8(),
			Subscribers: uids,
		})
	}
	return r
}

// groupPlan 是单个群的对账计划：哪些 uid（泄漏成员）要在哪些子区（shortID）摘订阅。
type groupPlan struct {
	groupNo  string
	uids     []string
	shortIDs []string
}

// scanLeakedMembers 扫 group_member，返回每个群的泄漏成员（按 group_no 聚合、uid 去重）。
// 泄漏判定：is_deleted=1 OR status=blacklist（与 Issue 描述一致）。
func (r *ThreadSubscriptionReconciler) scanLeakedMembers() (map[string][]string, error) {
	type leakedRow struct {
		GroupNo string `db:"group_no"`
		UID     string `db:"uid"`
	}
	var rows []leakedRow
	_, err := r.ctx.DB().Select("group_no", "uid").
		From("group_member").
		Where("is_deleted=1 OR status=?", int(common.GroupMemberStatusBlacklist)).
		OrderAsc("group_no").
		Load(&rows)
	if err != nil {
		return nil, err
	}
	// 按 group_no 聚合 + uid 去重（同一成员可能同时 is_deleted=1 且 blacklist）。
	byGroup := make(map[string][]string)
	seen := make(map[string]map[string]struct{})
	for _, row := range rows {
		if row.GroupNo == "" || row.UID == "" {
			continue
		}
		if seen[row.GroupNo] == nil {
			seen[row.GroupNo] = make(map[string]struct{})
		}
		if _, dup := seen[row.GroupNo][row.UID]; dup {
			continue
		}
		seen[row.GroupNo][row.UID] = struct{}{}
		byGroup[row.GroupNo] = append(byGroup[row.GroupNo], row.UID)
	}
	return byGroup, nil
}

// buildPlan 把泄漏成员 + 各群非 deleted 子区组合成对账计划，并填好统计字段。
// 查子区失败的群按失败记录、跳过（不中断整体对账）。
func (r *ThreadSubscriptionReconciler) buildPlan(report *ReconcileReport) ([]groupPlan, error) {
	byGroup, err := r.scanLeakedMembers()
	if err != nil {
		return nil, err
	}
	plans := make([]groupPlan, 0, len(byGroup))
	for groupNo, uids := range byGroup {
		report.GroupsAffected++
		report.LeakedMembers += len(uids)

		shortIDs, qerr := queryThreadShortIDsForCleanup(r.ctx, groupNo)
		if qerr != nil {
			r.logger.Error("对账查询群子区失败", zap.Error(qerr), zap.String("groupNo", groupNo))
			report.Failures = append(report.Failures, ReconcileFailure{
				GroupNo: groupNo,
				Err:     "query threads: " + qerr.Error(),
			})
			continue
		}
		if len(shortIDs) == 0 {
			continue
		}
		report.ThreadsScanned += len(shortIDs)
		report.PairsPlanned += len(uids) * len(shortIDs)
		plans = append(plans, groupPlan{groupNo: groupNo, uids: uids, shortIDs: shortIDs})
	}
	return plans, nil
}

// Run 执行对账。dry-run（Apply=false）只统计不写；Apply=true 才逐子区批量摘订阅。
func (r *ThreadSubscriptionReconciler) Run(opts ReconcileOptions) (*ReconcileReport, error) {
	report := &ReconcileReport{DryRun: !opts.Apply}

	plans, err := r.buildPlan(report)
	if err != nil {
		return report, err
	}
	if !opts.Apply {
		// dry-run：到此为止，绝不调用 removeFn。
		return report, nil
	}

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultReconcileBatchSize
	}

	for _, p := range plans {
		for _, shortID := range p.shortIDs {
			// 子区 channelID 格式: {groupNo}____{shortID}（与 thread.BuildChannelID 及
			// removeUserFromGroupThreadsCleanup 一致）。
			channelID := p.groupNo + "____" + shortID
			for start := 0; start < len(p.uids); start += batchSize {
				end := start + batchSize
				if end > len(p.uids) {
					end = len(p.uids)
				}
				chunk := p.uids[start:end]

				if opts.Interval > 0 {
					time.Sleep(opts.Interval)
				}
				report.IMCalls++
				if rmErr := r.removeFn(channelID, chunk); rmErr != nil {
					// 失败只记录不中断：单个子区/批次失败不应阻断其余存量对账。
					r.logger.Error("对账摘除子区IM订阅失败",
						zap.Error(rmErr), zap.String("channelID", channelID), zap.Int("uids", len(chunk)))
					report.Failures = append(report.Failures, ReconcileFailure{
						GroupNo:   p.groupNo,
						ChannelID: channelID,
						UIDs:      append([]string(nil), chunk...),
						Err:       rmErr.Error(),
					})
					continue
				}
				report.PairsRemoved += len(chunk)
			}
		}
	}
	return report, nil
}
