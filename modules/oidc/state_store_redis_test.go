package oidc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rd "github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 集成测试:打通 redisStateStore 的全部读写路径,
// 重点验证 Lua getDel 的一次性消费语义(并发场景下唯一胜出)。
//
// 依赖 testenv-redis-1(127.0.0.1:6399),与 dmwork 测试约定一致。

const testRedisAddr = "127.0.0.1:6399"

// newRedisStoreForTest 直接构造 redisStateStore,绕开 *config.Context,
// 单测不需要 NewTestServer 全套迁移。
func newRedisStoreForTest(t *testing.T) *redisStateStore {
	t.Helper()
	client := rd.NewClient(&rd.Options{
		Addr:         testRedisAddr,
		MaxRetries:   1,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		DialTimeout:  2 * time.Second,
	})
	if err := client.Ping().Err(); err != nil {
		t.Skipf("testenv redis %s unavailable: %v", testRedisAddr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &redisStateStore{client: client}
}

func TestRedisStateStore_SaveAndConsume_Integration(t *testing.T) {
	s := newRedisStoreForTest(t)
	state := uniqueState(t)

	want := &StateData{
		Provider:       "aegis",
		CodeVerifier:   "verifier-abc",
		Nonce:          "nonce-xyz",
		IP:             "1.2.3.4",
		UserAgent:      "test-ua",
		ReturnTo:       "/home",
		ClientAuthcode: "front-1",
		DeviceFlag:     1,
	}
	require.NoError(t, s.Save(context.Background(), state, want, 30*time.Second))

	got, err := s.Consume(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.CodeVerifier, got.CodeVerifier)
	assert.Equal(t, want.Nonce, got.Nonce)
	assert.Equal(t, want.ClientAuthcode, got.ClientAuthcode)
	assert.Equal(t, want.DeviceFlag, got.DeviceFlag)
	assert.False(t, got.CreatedAt.IsZero(), "CreatedAt should be set")
}

func TestRedisStateStore_ConsumeIsOneShot_Integration(t *testing.T) {
	s := newRedisStoreForTest(t)
	state := uniqueState(t)

	require.NoError(t, s.Save(context.Background(), state,
		&StateData{Nonce: "n"}, 30*time.Second))

	// 第一次拿到
	first, err := s.Consume(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, first)

	// 第二次应找不到(已 GETDEL)
	_, err = s.Consume(context.Background(), state)
	assert.True(t, errors.Is(err, ErrStateNotFound), "second Consume should miss, got %v", err)
}

func TestRedisStateStore_ConsumeUnknown_Integration(t *testing.T) {
	s := newRedisStoreForTest(t)
	_, err := s.Consume(context.Background(), uniqueState(t))
	assert.True(t, errors.Is(err, ErrStateNotFound))
}

func TestRedisStateStore_Expire_Integration(t *testing.T) {
	s := newRedisStoreForTest(t)
	state := uniqueState(t)

	// 收紧到亚秒级,降低 CI 抖动窗口的同时仍给 Redis 写够缓冲。
	require.NoError(t, s.Save(context.Background(), state,
		&StateData{Nonce: "expire-test"}, 200*time.Millisecond))

	time.Sleep(500 * time.Millisecond)

	_, err := s.Consume(context.Background(), state)
	assert.True(t, errors.Is(err, ErrStateNotFound), "expired key should be ErrStateNotFound, got %v", err)
}

func TestRedisStateStore_SaveRejectsEmptyState_Integration(t *testing.T) {
	s := newRedisStoreForTest(t)
	err := s.Save(context.Background(), "", &StateData{}, time.Minute)
	assert.Error(t, err)
}

// Lua GETDEL 的并发原子性:N 个 goroutine 同时 Consume 同一 state,
// 必须且只能有 1 个拿到 StateData,其余拿 ErrStateNotFound。
//
// 这是 CSRF 一次性保护的核心不变量。GET+DEL 两步实现会在这里失败。
func TestRedisStateStore_ConsumeOneShotUnderConcurrency_Integration(t *testing.T) {
	s := newRedisStoreForTest(t)
	state := uniqueState(t)
	require.NoError(t, s.Save(context.Background(), state,
		&StateData{Nonce: "concurrency"}, 30*time.Second))

	const N = 50
	var wins int32
	var misses int32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			got, err := s.Consume(context.Background(), state)
			if err == nil && got != nil {
				atomic.AddInt32(&wins, 1)
				return
			}
			if errors.Is(err, ErrStateNotFound) {
				atomic.AddInt32(&misses, 1)
				return
			}
			t.Errorf("unexpected error from Consume: %v", err)
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), wins, "exactly one goroutine should consume the state")
	assert.Equal(t, int32(N-1), misses, "all others should miss")
}

func TestRedisStateStore_Close_Integration(t *testing.T) {
	s := newRedisStoreForTest(t)
	// 重复 Close 不应 panic
	assert.NoError(t, s.Close())
}

// uniqueState 给每个测试一个独立 key,避免并发跑测试时互相覆盖。
func uniqueState(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("oidc-test-%s-%d", t.Name(), time.Now().UnixNano())
}
