package group

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// 本文件登记 group 模块 octo-server #394（建群原子性 / IM 频道孤儿找回）相关的
// Prometheus 指标。设计取舍与 modules/oidc/metrics.go 一致：
//   - 注册到全局默认 Registry(promauto.New*)，由基础设施侧暴露 /metrics 端点。
//   - Counter 用 *Vec 带 result/outcome 维度，便于 Grafana 切分成功/失败比例。
//   - 不加 group_no/uid 这类高基数 label —— 会爆 Prometheus 内存。
const groupMetricNamespace = "group"

// channelCreateResultLabels 建群路径上 IM 频道创建的结果维度。
//   - ok：IM 频道确认创建成功。
//   - im_fail：IM 创建失败，已走补偿删除（失败时留 channel_synced=0 兜底）。
func channelCreateResultLabels() []string { return []string{"ok", "im_fail"} }

// reconcileTickResultLabels reconcile worker 每个 tick 的出口维度。
//   - ran：本 tick 实际执行了一轮扫描。
//   - lock_held：未抢到分布式锁，本 tick 由其它实例执行（多实例去重）。
//   - lock_err：抢锁时 Redis 故障，已降级为无锁执行（幂等，安全）。
//   - query_err：扫描孤儿行失败。
func reconcileTickResultLabels() []string {
	return []string{"ran", "lock_held", "lock_err", "query_err"}
}

// reconcileOutcomeLabels 单个孤儿群被 reconcile 处理的结果维度。
//   - resolved：IM 频道幂等重建成功并翻转 channel_synced=1。
//   - im_fail：重建 IM 频道失败，下个 tick 重试。
//   - flag_fail：IM 频道已就绪但翻转标记失败，下个 tick 重试（幂等）。
//   - skipped：处理时群已不再满足条件（已被删除/已解散/已被它实例修复）。
func reconcileOutcomeLabels() []string {
	return []string{"resolved", "im_fail", "flag_fail", "skipped"}
}

func init() {
	for _, l := range channelCreateResultLabels() {
		metricChannelCreateTotal.WithLabelValues(l).Add(0)
	}
	for _, l := range reconcileTickResultLabels() {
		metricReconcileTickTotal.WithLabelValues(l).Add(0)
	}
	for _, l := range reconcileOutcomeLabels() {
		metricReconcileOutcomeTotal.WithLabelValues(l).Add(0)
	}
}

var (
	// metricChannelCreateTotal 建群路径 IM 频道创建结果计数（result=ok|im_fail）。
	metricChannelCreateTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: groupMetricNamespace,
		Name:      "channel_create_total",
		Help:      "IM channel create outcomes on the CreateGroup path (octo-server #394).",
	}, []string{"result"})

	// metricReconcileTickTotal reconcile worker tick 结果计数。
	metricReconcileTickTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: groupMetricNamespace,
		Name:      "channel_reconcile_tick_total",
		Help:      "Channel-sync reconcile worker tick outcomes (octo-server #394).",
	}, []string{"result"})

	// metricReconcileDetected 每个 tick 检测到的孤儿群数量（channel_synced=0 且超过 grace）。
	metricReconcileDetected = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: groupMetricNamespace,
		Name:      "channel_reconcile_detected_total",
		Help:      "Total orphan groups (channel_synced=0 past grace) detected by the reconcile worker (octo-server #394).",
	})

	// metricReconcileOutcomeTotal 单个孤儿群处理结果计数。
	metricReconcileOutcomeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: groupMetricNamespace,
		Name:      "channel_reconcile_outcome_total",
		Help:      "Per-orphan reconcile outcomes (octo-server #394).",
	}, []string{"outcome"})
)
