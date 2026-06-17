package bot_api

import (
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OCT-42: the /v1/bot send surface must enforce a per-UID (per-bot) cap, not
// just the global per-IP DDoS floor. authBot() now mirrors robotID onto the
// "uid" context key and the group mounts SharedUIDRateLimiter after it, so a
// single bot token hammering an authenticated route trips its own Redis bucket
// (ratelimit:uid:{robotID}) and gets the shared rate.limited response.
//
// This is the regression guard for the stream-amplification gap: stream/start
// rides the same authBot group, so this proves it is now per-bot capped.

const (
	rlBotID    = "bot_ratelimit_oct42"
	rlBotToken = "bf_ratelimit_oct42_token"
)

func setupBotRateLimitEnv(t *testing.T) (http.Handler, func()) {
	t.Helper()
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, status, creator_uid, bot_token) VALUES (?, 1, ?, ?)",
		rlBotID, "owner_oct42", rlBotToken).Exec()
	require.NoError(t, err)

	// The UID bucket lives in Redis (ratelimit:uid:*) and persists across tests;
	// CleanAllTables does NOT clear it. Reset so we start from a full bucket.
	rds := redis.NewClient(&redis.Options{
		Addr:     ctx.GetConfig().DB.RedisAddr,
		Password: ctx.GetConfig().DB.RedisPass,
	})
	resetBucket := func() {
		if keys, err := rds.Keys("ratelimit:uid:*").Result(); err == nil && len(keys) > 0 {
			_ = rds.Del(keys...).Err()
		}
	}
	resetBucket()

	return s.GetRoute(), func() {
		resetBucket()
		_ = rds.Close()
	}
}

// TestBotAPI_PerUIDRateLimit_TripsBucket hammers an authenticated /v1/bot route
// with one bot token until the per-UID bucket is exhausted, then asserts the
// limiter returned the shared rate.limited response scoped to "uid".
func TestBotAPI_PerUIDRateLimit_TripsBucket(t *testing.T) {
	handler, cleanup := setupBotRateLimitEnv(t)
	defer cleanup()

	// /v1/bot/heartbeat is the lightest authenticated route: it only needs a
	// valid bot token and Redis, returns 200, and sits on the same authBot group
	// as the send/stream endpoints — so it exercises the exact middleware chain.
	const path = "/v1/bot/heartbeat"

	// First call: bucket full → passes. The UID limiter still annotates the
	// response, which proves it is actually reading "uid" (not silently failing
	// open because uid was unset — the bug this issue fixes).
	first := doBot(handler, botReq(t, "POST", path, rlBotToken, nil))
	require.Equalf(t, http.StatusOK, first.Code, "baseline heartbeat should pass; body: %s", first.Body.String())
	require.Equalf(t, "uid", first.Header().Get("X-RateLimit-Scope"),
		"limiter must be scoped to uid (else it failed open / not mounted)")

	// Hammer until the bucket trips. Default burst is 60 (DM_API_UID_RATELIMIT_BURST);
	// 200 rapid in-process requests comfortably exhaust it even with refill.
	var tripped *struct{ scope, retryAfter string }
	for i := 0; i < 200; i++ {
		w := doBot(handler, botReq(t, "POST", path, rlBotToken, nil))
		if w.Code == http.StatusTooManyRequests {
			tripped = &struct{ scope, retryAfter string }{
				scope:      w.Header().Get("X-RateLimit-Scope"),
				retryAfter: w.Header().Get("Retry-After"),
			}
			break
		}
	}

	require.NotNil(t, tripped, "per-UID bucket never tripped after 200 requests — limiter not enforcing")
	assert.Equal(t, "uid", tripped.scope, "rate-limited response must be uid-scoped (per-bot), not ip-scoped")
	assert.NotEmpty(t, tripped.retryAfter, "rate.limited response must carry Retry-After")
}
