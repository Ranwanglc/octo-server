package oidc

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// 本文件登记 OIDC 模块的全部 Prometheus 指标(7 项)。
//
// 设计取舍:
//   - 注册到全局默认 Registry(promauto.New*),由后续基础设施 PR 暴露
//     `/metrics` 端点。本 PR 不引入 HTTP 端点,避免越界。
//   - Counter 用 *Vec 带 result 维度,便于 Grafana 切分成功/失败比例。
//   - Histogram 仅 callback 一项,buckets 覆盖 50ms ~ 5s,匹配 Aegis Discovery +
//     token exchange + 可能的 /userinfo 拉取的真实 P99。
//   - **不**给 metric 加 issuer/uid 这类高基维 label —— 单 issuer(Aegis)够用,
//     uid 会爆炸 Prometheus 内存。

const metricNamespace = "oidc"

// callbackResultLabels callback handler 的全部出口标签,审计图表枚举用。
//
// 任何分支引入新失败原因都应回到这里加常量,Grafana dashboard 才能稳定。
//
// init 时会用每个 label 调一次 Add(0),保证未触发的 result 也作为零值序列出现,
// dashboard 上"零次"和"不存在"得以区分。
func callbackResultLabels() []string {
	return []string{
		"ok",
		"state_invalid",
		"idp_error",
		"missing_code",
		"exchange_fail",
		"verify_fail",
		"nonce_mismatch",
		"resolve_fail",
		"issue_fail",
		"identity_insert_fail",
		"race_recovered",
		"set_authcode_fail",
		"rate_limited",
		"other_fail",
	}
}

func stateConsumeResultLabels() []string  { return []string{"ok", "miss"} }
func logoutResultLabels() []string        { return []string{"ok", "kick_fail", "revoke_fail"} }
func syncTickResultLabels() []string      { return []string{"ran", "lock_held", "lock_err"} }
func syncProcessedResultLabels() []string { return []string{"ok", "invalid_grant", "transient", "panic"} }
func syncVerificationSyncedResultLabels() []string {
	// YUJ-405:SyncWorker rotate 后 /userinfo → upsert 的结果维度。
	// YUJ-409 Round 2:新增 sub_mismatch(ownership 校验失败)和 fetch_nil
	// (/userinfo 返 (nil,nil) 防御)两个分支。
	return []string{"upserted", "skipped_unverified", "fetch_failed", "upsert_failed", "sub_mismatch", "fetch_nil"}
}

// init 把每个声明的 label 都预热成 0 值序列。Prometheus 在没观察到样本前不会
// 暴露 series,导致 Grafana"区分不出零次"和"未注册"两种状态。
func init() {
	for _, l := range callbackResultLabels() {
		metricCallbackTotal.WithLabelValues(l).Add(0)
	}
	for _, l := range stateConsumeResultLabels() {
		metricStateConsumeTotal.WithLabelValues(l).Add(0)
	}
	for _, l := range logoutResultLabels() {
		metricLogoutTotal.WithLabelValues(l).Add(0)
	}
	for _, l := range syncTickResultLabels() {
		metricSyncTickTotal.WithLabelValues(l).Add(0)
	}
	for _, l := range syncProcessedResultLabels() {
		metricSyncProcessedTotal.WithLabelValues(l).Add(0)
	}
	for _, l := range syncVerificationSyncedResultLabels() {
		metricSyncVerificationSyncedTotal.WithLabelValues(l).Add(0)
	}
}

var (
	metricAuthorizeTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: metricNamespace,
		Name:      "authorize_total",
		Help:      "Total number of /authorize requests entering the OIDC handler.",
	})

	metricCallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace,
		Name:      "callback_total",
		Help:      "Total number of /callback requests by terminal result.",
	}, []string{"result"})

	metricCallbackDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricNamespace,
		Name:      "callback_duration_seconds",
		Help:      "End-to-end /callback handler latency in seconds.",
		Buckets:   []float64{.05, .1, .25, .5, 1, 2, 5},
	})

	metricStateConsumeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace,
		Name:      "state_consume_total",
		Help:      "OIDC state-store consume outcomes (ok|miss). miss includes both expired and never-existed.",
	}, []string{"result"})

	metricLogoutTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace,
		Name:      "logout_total",
		Help:      "POST /logout outcomes (ok|kick_fail|revoke_fail).",
	}, []string{"result"})

	metricSyncTickTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace,
		Name:      "sync_tick_total",
		Help:      "SyncWorker tick outcomes (ran|lock_held|lock_err).",
	}, []string{"result"})

	metricSyncProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace,
		Name:      "sync_processed_total",
		Help:      "SyncWorker per-RT processing outcomes (ok|invalid_grant|transient|panic).",
	}, []string{"result"})

	// metricSyncVerificationSyncedTotal SyncWorker 在 rotate 成功后调 /userinfo
	// → UpsertVerificationFromOIDC 的出口分布(YUJ-405)。用于运维观察实名同步
	// 命中率 + 排查 Aegis /userinfo 异常。
	metricSyncVerificationSyncedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace,
		Name:      "sync_verification_synced_total",
		Help:      "SyncWorker verification sync outcomes after RT rotate (upserted|skipped_unverified|fetch_failed|upsert_failed).",
	}, []string{"status"})
)
