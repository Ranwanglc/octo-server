package bot_api

// 本文件登记 agent-turn(人机协作回合)在 server↔channel(WuKongIM)边界上的
// 可观测性指标 —— 即 OCT-17 要求的"至少一条延迟 + 一条错误"指标。
//
// 观测点: bot 回复投递腿 (sendMessage -> dispatchMsgSendReq)。这是 agent turn
// 在服务端能可靠测到的那一段:人类 @mention 入站后,bot 经 /v1/bot/sendMessage
// 把回复交给 WuKongIM 的耗时与成败。入站派发→bot 回复的端到端时延跨两个独立
// HTTP 请求且经 Redis 事件队列异步轮询,服务端无可靠关联键,故不在此度量(避免
// 引入不可信的关联假设)。
//
// 设计取舍(对齐 modules/oidc/metrics.go):
//   - 注册到全局默认 Registry(promauto.New*),与 pkg/metrics 暴露的 /metrics
//     scrape 端点(DefaultGatherer)一次抓取即可拿到。
//   - namespace 用 "dmwork"(与 pkg/metrics/http.go 的进程级指标同命名空间),
//     subsystem 用 "agent_turn",最终指标名 dmwork_agent_turn_*。
//   - label 维度刻意保持低基数: channel_type(person|group|topic|other)+
//     result(ok|error),避免 robotID / channelID 这类高基值打爆 Prometheus 内存。

import (
	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	agentTurnNamespace = "dmwork"
	agentTurnSubsystem = "agent_turn"

	agentTurnResultOK    = "ok"
	agentTurnResultError = "error"
)

// agentTurnChannelTypeLabels 是 channel_type label 的全部取值,init 时预热成 0
// 值序列,让 Grafana 能区分"零次投递"与"指标不存在"。
func agentTurnChannelTypeLabels() []string {
	return []string{"person", "group", "topic", "other"}
}

func agentTurnResultLabels() []string {
	return []string{agentTurnResultOK, agentTurnResultError}
}

var (
	// metricAgentTurnDeliveryDuration bot 回复投递到 WuKongIM 的延迟直方图。
	// Buckets 覆盖 5ms ~ 10s,与 pkg/metrics/http.go 的请求延迟桶一致,匹配 IM
	// 投递的真实 P99 区间。
	metricAgentTurnDeliveryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: agentTurnNamespace,
		Subsystem: agentTurnSubsystem,
		Name:      "delivery_duration_seconds",
		Help:      "Latency in seconds of an agent (bot) reply delivery to the channel (WuKongIM), labeled by channel_type and result (ok|error).",
		Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"channel_type", "result"})

	// metricAgentTurnDeliveryTotal agent 回复投递次数,按 channel_type / result
	// 切分。失败投递数 = delivery_total{result="error"} —— 即 OCT-17 要求的
	// "failed agent-turn deliveries" 计数器。
	metricAgentTurnDeliveryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: agentTurnNamespace,
		Subsystem: agentTurnSubsystem,
		Name:      "delivery_total",
		Help:      "Count of agent (bot) reply deliveries to the channel (WuKongIM) by channel_type and result (ok|error); result=error counts failed agent-turn deliveries.",
	}, []string{"channel_type", "result"})
)

// init 预热所有 label 组合为 0 值序列(channel_type × result = 8 条),
// 内存可忽略,但让 dashboard 在首次投递前就有稳定的零值曲线。
func init() {
	for _, ct := range agentTurnChannelTypeLabels() {
		for _, r := range agentTurnResultLabels() {
			metricAgentTurnDeliveryTotal.WithLabelValues(ct, r).Add(0)
		}
	}
}

// agentTurnChannelTypeLabel 把 WuKongIM 的 channel_type 数值收敛到低基数 label。
func agentTurnChannelTypeLabel(channelType uint8) string {
	switch channelType {
	case common.ChannelTypePerson.Uint8():
		return "person"
	case common.ChannelTypeGroup.Uint8():
		return "group"
	case common.ChannelTypeCommunityTopic.Uint8():
		return "topic"
	default:
		return "other"
	}
}

// observeAgentTurnDelivery 记录一次 bot 回复投递的延迟与成败。err != nil 记为
// result=error(失败投递),否则 result=ok。延迟与计数共用同一组 label,方便在
// Grafana 同图叠加成功/失败的延迟分布与速率。
func observeAgentTurnDelivery(channelType uint8, seconds float64, err error) {
	result := agentTurnResultOK
	if err != nil {
		result = agentTurnResultError
	}
	ct := agentTurnChannelTypeLabel(channelType)
	metricAgentTurnDeliveryDuration.WithLabelValues(ct, result).Observe(seconds)
	metricAgentTurnDeliveryTotal.WithLabelValues(ct, result).Inc()
}
