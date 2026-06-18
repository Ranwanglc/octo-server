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

// assertPerUIDBucketTrips drives one bot token against an authenticated /v1/bot
// route until its per-UID bucket is exhausted. It asserts (a) the first call
// passes AND carries X-RateLimit-Scope: uid — proving the limiter actually read
// "uid" rather than silently failing open (the bug OCT-42 fixes) — and (b) a
// later call trips with a uid-scoped 429 carrying Retry-After.
func assertPerUIDBucketTrips(t *testing.T, handler http.Handler, token string) {
	t.Helper()

	// /v1/bot/heartbeat is the lightest authenticated route: it only needs a
	// valid bot token and Redis, returns 200, and sits on the same authBot group
	// as the send/stream endpoints — so it exercises the exact middleware chain.
	const path = "/v1/bot/heartbeat"

	first := doBot(handler, botReq(t, "POST", path, token, nil))
	require.Equalf(t, http.StatusOK, first.Code, "baseline heartbeat should pass; body: %s", first.Body.String())
	require.Equalf(t, "uid", first.Header().Get("X-RateLimit-Scope"),
		"limiter must be scoped to uid (else it failed open / not mounted)")

	// Hammer until the bucket trips. Default burst is 60 (DM_API_UID_RATELIMIT_BURST);
	// 200 rapid in-process requests comfortably exhaust it even with refill.
	var tripped *struct{ scope, retryAfter string }
	for i := 0; i < 200; i++ {
		w := doBot(handler, botReq(t, "POST", path, token, nil))
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

// TestBotAPI_PerUIDRateLimit_TripsBucket proves the User Bot (bf_) path sets
// "uid" and is per-UID capped.
func TestBotAPI_PerUIDRateLimit_TripsBucket(t *testing.T) {
	handler, cleanup := setupBotRateLimitEnv(t)
	defer cleanup()
	assertPerUIDBucketTrips(t, handler, rlBotToken)
}

const (
	rlAppRegBotUID = "app_ratelimit_oct42_reg_bot"
	rlAppRegBotTok = "app_ratelimit_oct42_reg_token"
	rlAppDBBotUID  = "app_ratelimit_oct42_db_bot"
	rlAppDBBotTok  = "app_ratelimit_oct42_db_token"
)

// TestBotAPI_PerUIDRateLimit_AppBot_TripsBucket proves the App Bot (app_)
// principals — not just the User Bot path — also set "uid" and are per-UID
// capped. authBot resolves App Bots in two places (O(1) in-memory registry,
// then DB fallback) and BOTH must call setBotActorUID; otherwise app_ tokens
// silently fail open and re-introduce the OCT-42 amplification gap for App Bots.
// Each path is exercised with a distinct app_ token.
func TestBotAPI_PerUIDRateLimit_AppBot_TripsBucket(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	// Registry path: inject an adapter that resolves the registry token. Restore
	// the prior global registry afterwards so we don't leak state into other
	// tests in this package.
	prevReg := GetAppBotRegistry()
	reg := NewAppBotRegistryAdapter()
	reg.Add(rlAppRegBotTok, &AppBotRegistrySpec{UID: rlAppRegBotUID, Scope: "platform"})
	SetAppBotRegistry(reg)
	t.Cleanup(func() {
		if prevReg != nil {
			SetAppBotRegistry(prevReg)
			return
		}
		// atomic.Value cannot store an untyped nil; an empty adapter is
		// behaviourally identical to "no registry" for FindByToken.
		SetAppBotRegistry(NewAppBotRegistryAdapter())
	})

	// DB-fallback path: a published app_bot row whose token is NOT in the
	// registry, so authAppBot falls through to queryAppBotByToken. The app_bot
	// table is owned by the app_bot module, which this test binary does not
	// import; the shared CI schema has it, but a partial local DB may not — so
	// skip (not fail) the DB-fallback subtest if the row can't be inserted.
	dbBotReady := true
	if _, err := ctx.DB().InsertBySql(
		"INSERT INTO app_bot (id, uid, display_name, scope, status, token, created_by) VALUES (?, ?, ?, 'platform', 1, ?, ?)",
		rlAppDBBotUID, rlAppDBBotUID, "oct42 app db bot", rlAppDBBotTok, "owner_oct42").Exec(); err != nil {
		dbBotReady = false
		t.Logf("app_bot DB-fallback row not inserted (app_bot table likely absent in this binary's schema): %v", err)
	}

	rds := redis.NewClient(&redis.Options{
		Addr:     ctx.GetConfig().DB.RedisAddr,
		Password: ctx.GetConfig().DB.RedisPass,
	})
	resetBucket := func() {
		if keys, err := rds.Keys("ratelimit:uid:*").Result(); err == nil && len(keys) > 0 {
			_ = rds.Del(keys...).Err()
		}
	}
	t.Cleanup(func() { resetBucket(); _ = rds.Close() })

	handler := s.GetRoute()

	t.Run("registry path", func(t *testing.T) {
		resetBucket() // distinct UIDs use distinct buckets; reset keeps each subtest hermetic
		assertPerUIDBucketTrips(t, handler, rlAppRegBotTok)
	})

	t.Run("db fallback path", func(t *testing.T) {
		if !dbBotReady {
			t.Skip("app_bot table unavailable in this test binary's schema; DB-fallback path covered in CI")
		}
		resetBucket()
		assertPerUIDBucketTrips(t, handler, rlAppDBBotTok)
	})
}
