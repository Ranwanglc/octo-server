package user

import (
	"os"
	"runtime"
	"strconv"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-server/pkg/avatarrender"
	"github.com/Mininglamp-OSS/octo-server/pkg/metrics"
	"go.uber.org/zap"
)

// newAvatarCache 构造头像渲染缓存(LRU + singleflight + 渲染信号量),把可观测性
// hooks 接到 pkg/metrics 的头像指标上(issue#480)。容量与渲染并发上限可经环境
// 变量调整;构造失败时退化为 nil(GetOrRender 对 nil 接收者安全,直接渲染),不阻断启动。
func newAvatarCache() *avatarrender.Cache {
	cfg := avatarrender.Config{
		Size:                 avatarCacheSizeFromEnv(),
		MaxConcurrentRenders: avatarRenderConcurrencyFromEnv(),
		Hooks: avatarrender.Hooks{
			OnHit:           metrics.ObserveAvatarCacheHit,
			OnMiss:          metrics.ObserveAvatarCacheMiss,
			OnShared:        metrics.ObserveAvatarSingleflightShared,
			OnRender:        metrics.ObserveAvatarRender,
			OnSemaphoreWait: metrics.ObserveAvatarSemaphoreWait,
			OnInflight:      metrics.AddAvatarRenderInflight,
		},
	}
	c, err := avatarrender.NewCache(cfg)
	if err != nil {
		log.Warn("构造头像渲染缓存失败,退化为无缓存", zap.Error(err))
		return nil
	}
	return c
}

// avatarCacheSizeFromEnv 读取 DM_AVATAR_CACHE_SIZE(LRU 条目数),缺省/非法返回 0
// (NewCache 用 DefaultCacheSize)。
func avatarCacheSizeFromEnv() int {
	if v := os.Getenv("DM_AVATAR_CACHE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// avatarRenderConcurrencyFromEnv 读取 DM_AVATAR_RENDER_MAX_CONCURRENCY(最大并发
// 真实渲染数),缺省/非法时返回 max(1, GOMAXPROCS-1):给非头像流量至少留一个 P,
// 防止冷头像洪峰占满所有核饿死其它请求。
func avatarRenderConcurrencyFromEnv() int {
	if v := os.Getenv("DM_AVATAR_RENDER_MAX_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if n := runtime.GOMAXPROCS(0) - 1; n > 0 {
		return n
	}
	return 1
}
