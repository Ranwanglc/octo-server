package group

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	octoredis "github.com/Mininglamp-OSS/octo-server/pkg/redis"
	rd "github.com/go-redis/redis"
	"go.uber.org/zap"
)

// octo-server #394：建群与 IM 频道创建非原子，commit 与 IM 创建之间任何失败/崩溃都会
// 留下「可查询但无 IM 频道」的孤儿群。CreateGroup 已把待确认的群标记成
// channel_synced=0，本 worker 周期性地把超过 grace 窗口仍为 0 的群找出来，幂等重建
// IM 频道（IMCreateOrUpdateChannel 本身是 create-or-update，可安全重复调用）并翻转
// channel_synced=1，实现最终一致——孤儿可被找回，而非永久残留。

const (
	// reconcileTickLockKey reconcile worker tick 级分布式互斥的 Redis key。
	// 多实例部署时同一时刻只允许一个实例扫描，避免 N×IM 流量。锁不可用时降级为
	// 无锁运行——因为重建/翻转都是幂等的，无锁只会多打几次 IM，不影响正确性。
	reconcileTickLockKey = "group:channel_reconcile:tick"

	defaultReconcileInterval = 120 * time.Second
	defaultReconcileGraceSec = 120
	defaultReconcileBatch    = 200
)

// ReconcileConfig 控制 reconcile worker 的调度参数。
type ReconcileConfig struct {
	Interval  time.Duration // ≤ 0 视为禁用，Start 直接返回
	GraceSec  int           // 孤儿判定的 grace 窗口（秒）：仅处理创建超过该时长仍未同步的群
	BatchSize int           // 单 tick 处理的孤儿上限
	LockTTL   time.Duration // 分布式锁持有期，默认 = Interval
}

func (c *ReconcileConfig) withDefaults() {
	if c.GraceSec <= 0 {
		c.GraceSec = defaultReconcileGraceSec
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultReconcileBatch
	}
	if c.LockTTL <= 0 {
		c.LockTTL = c.Interval
	}
}

// reconcileTickLock tick 级分布式互斥锁的最小接口（与 oidc.tickLock 同语义：
// Acquire 必须原子 SET NX EX，Release 必须 token-aware CAS-DEL）。nil = 不互斥。
type reconcileTickLock interface {
	Acquire(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, key, token string) (bool, error)
}

// luaReconcileReleaseLock CAS-DEL：仅当 value==token 时 DEL，避免 lease 边界误删他人锁。
var luaReconcileReleaseLock = rd.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end
`)

// redisReconcileLock 用 Redis SET NX EX + Lua CAS-DEL 实现 reconcileTickLock。
type redisReconcileLock struct {
	client *rd.Client
}

func newRedisReconcileLock(ctx *config.Context) *redisReconcileLock {
	client := rd.NewClient(octoredis.MustBuildOptions(ctx.GetConfig(), func(o *rd.Options) {
		o.MaxRetries = 3
		o.ReadTimeout = 3 * time.Second
		o.WriteTimeout = 3 * time.Second
		o.DialTimeout = 3 * time.Second
	}))
	return &redisReconcileLock{client: client}
}

func (l *redisReconcileLock) Acquire(_ context.Context, key, token string, ttl time.Duration) (bool, error) {
	if key == "" || token == "" || ttl <= 0 {
		return false, fmt.Errorf("group: reconcile lock Acquire: key/token/ttl required")
	}
	ok, err := l.client.SetNX(key, token, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("group: reconcile lock Acquire %q: %w", key, err)
	}
	return ok, nil
}

func (l *redisReconcileLock) Release(_ context.Context, key, token string) (bool, error) {
	if key == "" || token == "" {
		return false, fmt.Errorf("group: reconcile lock Release: key/token required")
	}
	res, err := luaReconcileReleaseLock.Run(l.client, []string{key}, token).Result()
	if err != nil {
		if errors.Is(err, rd.Nil) {
			return false, nil
		}
		return false, fmt.Errorf("group: reconcile lock Release %q: %w", key, err)
	}
	n, ok := res.(int64)
	if !ok {
		return false, fmt.Errorf("group: reconcile lock Release %q: unexpected lua result type %T", key, res)
	}
	return n == 1, nil
}

func (l *redisReconcileLock) Close() error {
	if l.client == nil {
		return nil
	}
	return l.client.Close()
}

// ChannelReconciler 周期性找回 channel_synced=0 的孤儿群（octo-server #394）。
type ChannelReconciler struct {
	svc  *Service
	cfg  ReconcileConfig
	lock reconcileTickLock // nil = 单实例 / 测试，不做互斥
	log.Log

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewChannelReconciler 构造 reconciler。lock 传 nil 时退化为「每实例独立 tick」——
// 因重建/翻转幂等，无锁只是多打 IM，不影响正确性。
func NewChannelReconciler(svc *Service, cfg ReconcileConfig, lock reconcileTickLock) *ChannelReconciler {
	cfg.withDefaults()
	return &ChannelReconciler{
		svc:  svc,
		cfg:  cfg,
		lock: lock,
		Log:  log.NewTLog("groupChannelReconcile"),
	}
}

// Start 启动后台 ticker goroutine。Interval ≤ 0 视为禁用。
// 幂等：重复调用会先 Stop 旧 goroutine 再启动新的。
func (r *ChannelReconciler) Start(ctx context.Context) {
	if r.cfg.Interval <= 0 {
		return
	}
	if r.cancel != nil {
		r.cancel()
		r.wg.Wait()
	}
	rctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(r.cfg.Interval)
		defer t.Stop()
		for {
			select {
			case <-rctx.Done():
				return
			case <-t.C:
				if err := r.RunOnce(rctx); err != nil && !errors.Is(err, context.Canceled) {
					r.Error("channel reconcile tick failed", zap.Error(err))
				}
			}
		}
	}()
}

// Stop 通知 worker 退出并等待进行中的 tick 完成。
func (r *ChannelReconciler) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

// RunOnce 执行一轮 reconcile：抢锁（可选）→ 扫描孤儿 → 逐个幂等重建 IM 频道 + 翻转标记。
//
// 多实例：抢到 tick lock 才扫描，抢不到直接返回（本 tick 由别人跑）。锁故障降级无锁运行。
// 返回 ctx.Err() 让上层日志区分「正常停机」与「异常失败」。
func (r *ChannelReconciler) RunOnce(ctx context.Context) error {
	if r.lock != nil {
		token := util.GenerUUID()
		got, err := r.lock.Acquire(ctx, reconcileTickLockKey, token, r.cfg.LockTTL)
		if err != nil {
			// Redis 故障：降级到无锁路径而非阻塞。幂等保证正确性，仅多打 IM。
			metricReconcileTickTotal.WithLabelValues("lock_err").Inc()
			r.Warn("channel reconcile lock unavailable, running lock-free this tick", zap.Error(err))
		} else if !got {
			metricReconcileTickTotal.WithLabelValues("lock_held").Inc()
			return nil
		} else {
			defer func() {
				if _, rerr := r.lock.Release(ctx, reconcileTickLockKey, token); rerr != nil {
					r.Warn("channel reconcile lock release failed", zap.Error(rerr))
				}
			}()
		}
	}

	groupNos, err := r.svc.db.QueryReconcilableGroupNos(r.cfg.GraceSec, r.cfg.BatchSize)
	if err != nil {
		metricReconcileTickTotal.WithLabelValues("query_err").Inc()
		return fmt.Errorf("group: query reconcilable groups: %w", err)
	}
	metricReconcileTickTotal.WithLabelValues("ran").Inc()
	if len(groupNos) == 0 {
		return nil
	}
	metricReconcileDetected.Add(float64(len(groupNos)))
	r.Info("channel reconcile detected orphan groups", zap.Int("count", len(groupNos)))

	for _, groupNo := range groupNos {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		r.reconcileOne(groupNo)
	}
	return nil
}

// reconcileOne 找回单个孤儿群：重建 IM 频道（幂等）→ 翻转 channel_synced=1。
// 任何失败只记日志、不中断整批——下个 tick 会重试（全程幂等）。
func (r *ChannelReconciler) reconcileOne(groupNo string) {
	uids, err := r.svc.GetSubscribableMemberUIDs(groupNo)
	if err != nil {
		metricReconcileOutcomeTotal.WithLabelValues("im_fail").Inc()
		r.Error("channel reconcile query members failed", zap.Error(err), zap.String("groupNo", groupNo))
		return
	}
	if len(uids) == 0 {
		// 没有可订阅成员（全员退出/被删）：这种群无需 IM 频道，标记同步避免反复扫描。
		if _, ferr := r.svc.db.MarkChannelSynced(groupNo); ferr != nil {
			metricReconcileOutcomeTotal.WithLabelValues("flag_fail").Inc()
			r.Error("channel reconcile flag-only flip failed", zap.Error(ferr), zap.String("groupNo", groupNo))
			return
		}
		metricReconcileOutcomeTotal.WithLabelValues("skipped").Inc()
		return
	}

	if err := r.svc.imCreateChannel(&config.ChannelCreateReq{
		ChannelID:   groupNo,
		ChannelType: common.ChannelTypeGroup.Uint8(),
		Subscribers: uids,
	}); err != nil {
		metricReconcileOutcomeTotal.WithLabelValues("im_fail").Inc()
		r.Error("channel reconcile recreate IM channel failed", zap.Error(err), zap.String("groupNo", groupNo))
		return
	}

	if _, err := r.svc.db.MarkChannelSynced(groupNo); err != nil {
		metricReconcileOutcomeTotal.WithLabelValues("flag_fail").Inc()
		r.Error("channel reconcile flip flag failed (IM channel already recreated, will retry)", zap.Error(err), zap.String("groupNo", groupNo))
		return
	}
	metricReconcileOutcomeTotal.WithLabelValues("resolved").Inc()
	r.Info("channel reconcile resolved orphan group", zap.String("groupNo", groupNo))
}
