package user

// Phase 0 §0.9 验证用例: "多端会话: A 端切语言, B 端下次请求即生效"。
//
// 这一项不能拆到 pkg/i18n,因为 LanguageService.SetLanguage 是 cross-device
// invalidation 的写入端,实现细节(DB UPDATE + Redis DEL)归 modules/user。
// 这里以 fakeLangDB + fakeLangCache(与既有 service/handler 测试同一组 stub)
// 串起最小链路: 一次 SetLanguage → 后续任意 device 的 token 解析必须命中
// 新语言,而非 token cache 里冻结的旧 snapshot。
//
// 验证手段: 直接驱动 pkg/auth.CacheTokenParser.Parse,这是 octo-lib
// AuthMiddleware 真正调用的入口;parser 解码 token 后会调用 LanguageResolver
// (= LanguageService) 取真相源。如果 SetLanguage 没正确 invalidate cache,
// 或 resolver 没读 DB,该用例必失败。
//
// 不通过 testutil.NewTestServer 起真实 MySQL/Redis: 仓内现有测试基础设施
// 受 migration drift 阻塞(参见 api_language_test.go 头部说明 + memory
// local_test_infra.md)。fake DB/cache 已能覆盖语义契约。

import (
	"context"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/auth"
)

// liveLangDB wraps fakeLangDB so UpdateLanguageByUID immediately reflects
// into the read map — the production *DB writes one row that subsequent
// SELECTs see, but the bundled fake keeps writes in a separate `updates` map
// for assertion convenience. For multi-device sync the read-after-write
// observability is the contract under test, so we bridge them here.
type liveLangDB struct{ *fakeLangDB }

func (d *liveLangDB) UpdateLanguageByUID(uid, lang string) error {
	if err := d.fakeLangDB.UpdateLanguageByUID(uid, lang); err != nil {
		return err
	}
	d.lang[uid] = lang
	return nil
}

func newLiveLangDB() *liveLangDB { return &liveLangDB{fakeLangDB: newFakeLangDB()} }

// encodeToken produces a cache value identical to what production write
// paths emit (auth.Encode → "v2:{json}"). Using the real codec keeps the
// test honest: if the envelope format ever changes, this test catches the
// drift instead of silently testing a synthetic shape.
func encodeToken(t *testing.T, uid, name, lang string) string {
	t.Helper()
	raw, err := auth.Encode(auth.TokenInfo{UID: uid, Name: name, Language: lang})
	if err != nil {
		t.Fatalf("auth.Encode: %v", err)
	}
	return raw
}

// TestPhase0_MultiDeviceLanguageSyncOnSetLanguage is the headline Phase 0
// scenario: device A flips the user's language, device B's very next request
// must see the new value — even though B's cached token still carries the
// stale snapshot. The chain we exercise:
//
//  1. Two devices each have an auth token in cache; both encode a stale
//     language ("zh-CN") in the token envelope.
//  2. DB starts with "zh-CN" too — consistent steady state.
//  3. Device A's PUT /v1/user/language flow (here: direct LanguageService.
//     SetLanguage call) UPDATEs DB and DELs Redis hot key.
//  4. Device B's next request triggers CacheTokenParser.Parse. The parser
//     decodes the stale snapshot, then asks LanguageResolver. Resolver finds
//     no Redis hot key (DEL above), reads DB, returns "en-US".
//  5. UserInfo.Language returned to AuthMiddleware is "en-US" — the snapshot
//     does NOT leak.
//
// Failure modes this test traps:
//   - SetLanguage forgets to DEL → resolver returns stale Redis value
//   - Resolver skipped on parser path → snapshot wins
//   - Resolver error swallowed silently → wrong fallback
//   - Cache DEL races with re-populate (verified by inspecting cache state)
func TestPhase0_MultiDeviceLanguageSyncOnSetLanguage(t *testing.T) {
	const uid = "user-multi-device"
	const tokenA = "token-device-A"
	const tokenB = "token-device-B"
	const tokenCachePrefix = "tk:"

	cacheStore := newFakeLangCache()
	db := newLiveLangDB()
	db.lang[uid] = "zh-CN"

	// Seed both device token cache entries with the stale language snapshot.
	if err := cacheStore.Set(tokenCachePrefix+tokenA, encodeToken(t, uid, "alice", "zh-CN")); err != nil {
		t.Fatalf("seed token A: %v", err)
	}
	if err := cacheStore.Set(tokenCachePrefix+tokenB, encodeToken(t, uid, "alice", "zh-CN")); err != nil {
		t.Fatalf("seed token B: %v", err)
	}
	// Warm the language hot cache to mirror a steady state where prior reads
	// have already populated it. SetLanguage must actively invalidate this.
	if err := cacheStore.Set(LanguageCacheKeyPrefix+uid, "zh-CN"); err != nil {
		t.Fatalf("warm language cache: %v", err)
	}

	svc := NewLanguageService(db, cacheStore)
	parser := auth.NewCacheTokenParser(cacheStore, tokenCachePrefix, auth.WithLanguageResolver(svc))

	ctx := context.Background()

	// --- Baseline: both devices read zh-CN, no spurious resolver→DB hits
	// because the hot cache satisfies the resolver. ---
	assertParseLang(t, parser, ctx, tokenA, "zh-CN")
	assertParseLang(t, parser, ctx, tokenB, "zh-CN")
	if db.queryCalls != 0 {
		t.Fatalf("steady-state cache hit must skip DB; queryCalls=%d", db.queryCalls)
	}

	// --- Device A: PUT /v1/user/language equivalent. SetLanguage MUST
	// UPDATE DB and DEL the hot cache key.
	preDeletes := len(cacheStore.deletes)
	if err := svc.SetLanguage(ctx, uid, "en-US"); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	if got := db.updates[uid]; got != "en-US" {
		t.Fatalf("DB update[%q] = %q, want en-US", uid, got)
	}
	delKey := LanguageCacheKeyPrefix + uid
	if !sliceContainsAfter(cacheStore.deletes, preDeletes, delKey) {
		t.Fatalf("SetLanguage did not DEL %q; deletes=%v", delKey, cacheStore.deletes)
	}

	// --- Device B's next request: parser must see en-US ---
	dbCallsBefore := db.queryCalls
	info := assertParseLang(t, parser, ctx, tokenB, "en-US")
	if db.queryCalls != dbCallsBefore+1 {
		t.Fatalf("expected exactly one DB query for resolver re-fetch; got %d→%d",
			dbCallsBefore, db.queryCalls)
	}
	// UID/Name from the snapshot must still flow — only Language is overridden.
	if info.UID != uid || info.Name != "alice" {
		t.Fatalf("UserInfo metadata lost: %+v", info)
	}

	// --- Subsequent reads (either device) must come from re-populated cache,
	// not hammer DB again. This is the convergence guarantee.
	dbCallsBefore = db.queryCalls
	assertParseLang(t, parser, ctx, tokenA, "en-US")
	assertParseLang(t, parser, ctx, tokenB, "en-US")
	if db.queryCalls != dbCallsBefore {
		t.Fatalf("post-convergence reads must hit cache; queryCalls grew by %d", db.queryCalls-dbCallsBefore)
	}
}

// TestPhase0_MultiDeviceClearLanguageDropsSnapshot covers the inverse: a
// user wiping their preference (PUT with empty body) must make all device
// snapshots fall back to EarlyMiddleware's Accept-Language / default
// negotiation rather than perpetuating a stale Language=zh-CN forever.
// Documented contract: parser_test.go::TestCacheTokenParserResolverEmptyClearsSnapshot.
func TestPhase0_MultiDeviceClearLanguageDropsSnapshot(t *testing.T) {
	const uid = "user-multi-clear"
	const token = "token-clear"
	const tokenCachePrefix = "tk:"

	c := newFakeLangCache()
	db := newLiveLangDB()
	db.lang[uid] = "zh-CN"
	_ = c.Set(tokenCachePrefix+token, encodeToken(t, uid, "bob", "zh-CN"))

	svc := NewLanguageService(db, c)
	parser := auth.NewCacheTokenParser(c, tokenCachePrefix, auth.WithLanguageResolver(svc))

	// Steady state: snapshot wins via resolver hitting DB.
	assertParseLang(t, parser, context.Background(), token, "zh-CN")

	// Clear preference — equivalent to PUT {"language":""}.
	if err := svc.SetLanguage(context.Background(), uid, ""); err != nil {
		t.Fatalf("SetLanguage(empty): %v", err)
	}
	if got, ok := db.updates[uid]; !ok || got != "" {
		t.Fatalf("DB update[%q] = %q,present=%v; want empty string written", uid, got, ok)
	}

	// Next parse must NOT promote any language onto UserInfo — leaving it
	// empty lets pkg/i18n.LanguageFromContext fall back to negotiation /
	// default. (See parser.go comment on the empty-resolver branch.)
	info := assertParseLang(t, parser, context.Background(), token, "")
	if info.Language != "" {
		t.Fatalf("Language snapshot leaked after clear: %q", info.Language)
	}
}

func assertParseLang(t *testing.T, p *auth.CacheTokenParser, ctx context.Context, token, want string) wkhttp.UserInfo {
	t.Helper()
	info, err := p.Parse(ctx, token)
	if err != nil {
		t.Fatalf("Parse(%q): %v", token, err)
	}
	if info.Language != want {
		t.Fatalf("Parse(%q).Language = %q, want %q", token, info.Language, want)
	}
	return info
}

func sliceContainsAfter(deletes []string, startIdx int, want string) bool {
	for _, k := range deletes[startIdx:] {
		if k == want || strings.EqualFold(k, want) {
			return true
		}
	}
	return false
}
