package i18n

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// Details 是客户端可见的错误上下文字段。渲染前必须按 codes.Code.SafeDetailKeys
// 过滤，避免 uid/token/raw_err 等内部信息泄漏。
type Details map[string]any

var unsafeDetailsDroppedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "i18n_unsafe_details_dropped_total",
	Help: "Total number of i18n error detail keys dropped because they were not whitelisted.",
}, []string{"code", "key"})

// FilterBy 根据 Code.SafeDetailKeys 返回只包含白名单 key 的副本。
// 所有被丢弃的 key 都记录 i18n_unsafe_details_dropped_total{code,key}。
func (d Details) FilterBy(code codes.Code) Details {
	return d.FilterByKeys(code.ID, code.SafeDetailKeys)
}

// FilterByKeys 根据 safeKeys 返回只包含白名单 key 的副本。
func (d Details) FilterByKeys(codeID string, safeKeys []string) Details {
	if d == nil {
		return nil
	}

	allowed := make(map[string]struct{}, len(safeKeys))
	for _, key := range safeKeys {
		allowed[key] = struct{}{}
	}

	out := make(Details)
	for key, value := range d {
		if _, ok := allowed[key]; ok {
			out[key] = value
			continue
		}
		unsafeDetailsDroppedTotal.WithLabelValues(codeID, key).Inc()
	}
	return out
}
