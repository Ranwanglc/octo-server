package oidc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"golang.org/x/oauth2"
)

// fakeUserInfo 内存版 userInfoFetcher,记录调用序列并按需返 err / 结果。
type fakeUserInfo struct {
	mu          sync.Mutex
	calls       int32
	tokens      []*oauth2.Token
	resp        *UserInfoClaims
	err         error
}

func (f *fakeUserInfo) UserInfo(_ context.Context, tok *oauth2.Token) (*UserInfoClaims, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	f.tokens = append(f.tokens, tok)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// fakeVerif 内存版 verificationUpserter,记录每次 upsert 的 (uid, claims),按需注错误。
type fakeVerif struct {
	mu     sync.Mutex
	calls  int32
	got    []verifCall
	err    error
}

type verifCall struct {
	uid    string
	claims user.OIDCVerificationClaims
}

func (f *fakeVerif) UpsertVerificationFromOIDC(_ context.Context, uid string, claims user.OIDCVerificationClaims) error {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	f.got = append(f.got, verifCall{uid: uid, claims: claims})
	f.mu.Unlock()
	return f.err
}

// newVerifHarness 扩 newWorkerHarness:额外装上 ui/verif 并返回它们。
func newVerifHarness(t *testing.T, specs []dueSpec) (
	*SyncWorker, *fakeSyncStore, *scriptedRefresher, *fakeAudit,
	*fakeUserInfo, *fakeVerif, []string,
) {
	t.Helper()
	w, store, _, rfsh, audit, plain := newWorkerHarness(t, specs)
	ui := &fakeUserInfo{}
	vf := &fakeVerif{}
	w.WithVerificationSync(ui, vf)
	return w, store, rfsh, audit, ui, vf, plain
}

// happy path:RT 轮转成功 + /userinfo 返回完整实名 claims → 调一次 upsert,
// 入参与 claims 一致。RT 轮转本身的成功语义不受影响。
func TestSyncWorker_AfterRotate_UpsertsVerificationWhenVerified(t *testing.T) {
	w, store, rfsh, audit, ui, vf, plain := newVerifHarness(t, []dueSpec{
		{id: 11, identityID: 100, uid: "u-verif", plain: "rt-old", subject: "sub-aegis-123"},
	})
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken:  "at-new",
		RefreshToken: "rt-new",
		ExpiresAt:    time.Now().Add(2 * time.Hour),
	}
	ui.resp = &UserInfoClaims{
		Subject:          "sub-aegis-123",
		IsVerified:       IsVerifiedClaim(true),
		VerifiedAt:       VerifiedAtClaim(1715300000),
		VerifiedProvider: "cas.example.com",
		LegalName:        "张三",
		LegalEmail:       "z@example.com",
	}

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// RT 轮转本身完成
	if len(store.rotated) != 1 || store.rotated[0] != 11 {
		t.Errorf("rotated = %v, want [11]", store.rotated)
	}
	// /userinfo 调了 1 次,带上新 access_token
	if got := atomic.LoadInt32(&ui.calls); got != 1 {
		t.Fatalf("UserInfo calls = %d, want 1", got)
	}
	ui.mu.Lock()
	if len(ui.tokens) != 1 || ui.tokens[0].AccessToken != "at-new" {
		t.Errorf("UserInfo called with token %+v, want AccessToken=at-new", ui.tokens)
	}
	ui.mu.Unlock()
	// upsert 调了 1 次,uid/claims 正确
	if got := atomic.LoadInt32(&vf.calls); got != 1 {
		t.Fatalf("Upsert calls = %d, want 1", got)
	}
	got := vf.got[0]
	if got.uid != "u-verif" {
		t.Errorf("upsert uid = %q, want u-verif", got.uid)
	}
	if got.claims.Subject != "sub-aegis-123" ||
		got.claims.VerifiedProvider != "cas.example.com" ||
		got.claims.VerifiedAt != 1715300000 ||
		got.claims.LegalName != "张三" ||
		got.claims.LegalEmail != "z@example.com" {
		t.Errorf("upsert claims = %+v, unexpected", got.claims)
	}
	// refresh_ok 审计必须还在
	events := audit.events()
	if len(events) != 1 || events[0] != EventRefreshOK {
		t.Errorf("audit = %v, want [refresh_ok]", events)
	}
}

// is_verified=false(常见:未实名用户周期性 RT 轮转)→ 不 upsert,也不 delete,
// 不影响 RT 轮转成功。
func TestSyncWorker_AfterRotate_SkipsWhenUnverified(t *testing.T) {
	w, store, rfsh, _, ui, vf, plain := newVerifHarness(t, []dueSpec{
		{id: 12, identityID: 101, uid: "u-not-verif", plain: "rt-old-2", subject: "sub-X"},
	})
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken: "at-new", RefreshToken: "rt-new",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	ui.resp = &UserInfoClaims{
		Subject:    "sub-X",
		IsVerified: IsVerifiedClaim(false), // 关键
		LegalName:  "姓名",
		VerifiedAt: VerifiedAtClaim(1715300000),
	}

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(store.rotated) != 1 {
		t.Errorf("rotate should still succeed, rotated=%v", store.rotated)
	}
	if atomic.LoadInt32(&ui.calls) != 1 {
		t.Errorf("UserInfo should be called exactly once")
	}
	// 关键:未实名不 upsert
	if got := atomic.LoadInt32(&vf.calls); got != 0 {
		t.Errorf("Upsert calls = %d, want 0 for is_verified=false", got)
	}
}

// is_verified=true 但 legal_name 为空 → 跳过(保守策略,不让 Upsert 拒写导致告警噪声)。
func TestSyncWorker_AfterRotate_SkipsWhenLegalNameEmpty(t *testing.T) {
	w, _, rfsh, _, ui, vf, plain := newVerifHarness(t, []dueSpec{
		{id: 13, identityID: 102, uid: "u-empty-name", plain: "rt-old-3", subject: "sub-Y"},
	})
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken: "at-new", RefreshToken: "rt-new",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	ui.resp = &UserInfoClaims{
		Subject:          "sub-Y",
		IsVerified:       IsVerifiedClaim(true),
		VerifiedAt:       VerifiedAtClaim(1715300000),
		VerifiedProvider: "cas.example.com",
		LegalName:        "", // 空
	}

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if atomic.LoadInt32(&vf.calls) != 0 {
		t.Errorf("Upsert should be skipped when legal_name empty")
	}
}

// /userinfo 500 / 网络错 → RT 轮转仍记账 refresh_ok,tick 不中断。
func TestSyncWorker_AfterRotate_UserInfoFailure_DoesNotFailTick(t *testing.T) {
	w, store, rfsh, audit, ui, vf, plain := newVerifHarness(t, []dueSpec{
		{id: 14, identityID: 103, uid: "u-uifail", plain: "rt-old-4"},
	})
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken: "at-new", RefreshToken: "rt-new",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	ui.err = errors.New("userinfo: http 500")

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce must not propagate /userinfo error: %v", err)
	}

	// RT 轮转成功
	if len(store.rotated) != 1 || store.rotated[0] != 14 {
		t.Errorf("rotate should succeed despite /userinfo error, rotated=%v", store.rotated)
	}
	// refresh_ok 审计照旧,未被污染为 refresh_fail
	events := audit.events()
	if len(events) != 1 || events[0] != EventRefreshOK {
		t.Errorf("audit = %v, want [refresh_ok] (/userinfo failure must not flip to fail)", events)
	}
	// upsert 绝对没调(连 claims 都没拿到)
	if atomic.LoadInt32(&vf.calls) != 0 {
		t.Errorf("Upsert should not be called when /userinfo failed")
	}
	// /userinfo 确实被调过
	if atomic.LoadInt32(&ui.calls) != 1 {
		t.Errorf("UserInfo should have been attempted exactly once")
	}
}

// upsert DB 失败 → 只 warn,不影响 RT 轮转的 refresh_ok 语义。
func TestSyncWorker_AfterRotate_UpsertFailure_DoesNotFailTick(t *testing.T) {
	w, store, rfsh, audit, ui, vf, plain := newVerifHarness(t, []dueSpec{
		{id: 15, identityID: 104, uid: "u-upsertfail", plain: "rt-old-5", subject: "sub-Z"},
	})
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken: "at-new", RefreshToken: "rt-new",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	ui.resp = &UserInfoClaims{
		Subject:          "sub-Z",
		IsVerified:       IsVerifiedClaim(true),
		VerifiedAt:       VerifiedAtClaim(1715300000),
		VerifiedProvider: "cas",
		LegalName:        "有效姓名",
	}
	vf.err = errors.New("db: deadlock")

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce must not propagate upsert error: %v", err)
	}

	if len(store.rotated) != 1 {
		t.Errorf("rotate should succeed")
	}
	events := audit.events()
	if len(events) != 1 || events[0] != EventRefreshOK {
		t.Errorf("audit = %v, want [refresh_ok]", events)
	}
	// upsert 被尝试过
	if atomic.LoadInt32(&vf.calls) != 1 {
		t.Errorf("Upsert should be attempted once, got %d", vf.calls)
	}
}

// 向后兼容:ui=nil/verif=nil 的老路径 — 完全不碰 /userinfo / upsert,
// worker 表现与 YUJ-405 之前一致。
func TestSyncWorker_NoUIFetcher_WorkerRunsAsBefore(t *testing.T) {
	w, store, _, rfsh, audit, plain := newWorkerHarness(t, []dueSpec{
		{id: 16, identityID: 105, uid: "u-noui", plain: "rt-old-6"},
	})
	// 故意不调用 WithVerificationSync,ui/verif 保持 nil
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken: "at-new", RefreshToken: "rt-new",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(store.rotated) != 1 {
		t.Errorf("rotate should still succeed without ui/verif, rotated=%v", store.rotated)
	}
	events := audit.events()
	if len(events) != 1 || events[0] != EventRefreshOK {
		t.Errorf("audit = %v, want [refresh_ok]", events)
	}
	// sanity:worker 内部 ui/verif 字段确实为 nil
	if w.ui != nil || w.verif != nil {
		t.Errorf("ui/verif should remain nil in backward-compat path")
	}
}

// YUJ-409 Round 2 — Jerry R1 blocking: sub ownership 校验。
// /userinfo.sub 与 identity.subject 不一致时,视为 RT 串台 / IdP bug,跳过
// upsert(不 kick / 不 revoke — 后台 tick 不做踢线)。refresh_ok 仍记账。
func TestSyncWorker_AfterRotate_SubjectMismatch_DoesNotUpsert(t *testing.T) {
	w, store, rfsh, audit, ui, vf, plain := newVerifHarness(t, []dueSpec{
		{id: 21, identityID: 200, uid: "u-mismatch", plain: "rt-old-mm", subject: "sub-A"},
	})
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken: "at-new", RefreshToken: "rt-new",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	// IdP 返回的 /userinfo sub 与 DB 记录的 identity.subject 不一致
	ui.resp = &UserInfoClaims{
		Subject:          "sub-B",
		IsVerified:       IsVerifiedClaim(true),
		VerifiedAt:       VerifiedAtClaim(1715300000),
		VerifiedProvider: "cas.example.com",
		LegalName:        "别人的名字",
		LegalEmail:       "other@example.com",
	}

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// RT 轮转本身仍成功
	if len(store.rotated) != 1 || store.rotated[0] != 21 {
		t.Errorf("rotate should succeed, rotated=%v", store.rotated)
	}
	// /userinfo 被调了一次
	if atomic.LoadInt32(&ui.calls) != 1 {
		t.Errorf("UserInfo should be called exactly once, got %d", ui.calls)
	}
	// 关键:upsert 不能被调(别人的 claims 不能写到本 uid)
	if got := atomic.LoadInt32(&vf.calls); got != 0 {
		t.Errorf("Upsert MUST NOT be called on sub mismatch, got %d calls", got)
	}
	// refresh_ok 仍然记账(sub mismatch 不翻为 refresh_fail)
	events := audit.events()
	if len(events) != 1 || events[0] != EventRefreshOK {
		t.Errorf("audit = %v, want [refresh_ok] (sub mismatch must not flip to fail)", events)
	}
}

// YUJ-409 Round 2 — Jerry R1 Non-blocking: UserInfo 实现理论上可能返 (nil, nil),
// 直接 deref 会 panic。worker 必须 nil-guard 并优雅 skip,不影响 refresh_ok。
func TestSyncWorker_AfterRotate_UserInfoNilResponse_GracefullySkips(t *testing.T) {
	w, store, rfsh, audit, ui, vf, plain := newVerifHarness(t, []dueSpec{
		{id: 22, identityID: 201, uid: "u-ui-nil", plain: "rt-old-nil", subject: "sub-nil"},
	})
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken: "at-new", RefreshToken: "rt-new",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	// fake UserInfo 返 (nil, nil) — err=nil, resp=nil
	ui.resp = nil
	ui.err = nil

	// panic 捕获:nil guard 若丢失,此处会 panic → 用 defer recover 让测试失败而非崩进程
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RunOnce panicked on nil UserInfo response (missing nil guard): %v", r)
		}
	}()

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// RT 轮转仍成功
	if len(store.rotated) != 1 {
		t.Errorf("rotate should succeed, rotated=%v", store.rotated)
	}
	// /userinfo 调了一次(但返回 nil)
	if atomic.LoadInt32(&ui.calls) != 1 {
		t.Errorf("UserInfo should be called exactly once, got %d", ui.calls)
	}
	// upsert 必须不调(没 claims 可写)
	if got := atomic.LoadInt32(&vf.calls); got != 0 {
		t.Errorf("Upsert MUST NOT be called when /userinfo returns nil, got %d calls", got)
	}
	// refresh_ok 仍然记账
	events := audit.events()
	if len(events) != 1 || events[0] != EventRefreshOK {
		t.Errorf("audit = %v, want [refresh_ok]", events)
	}
}

// YUJ-409 Round 2 — DB 脏数据防御:identity.subject 为空时(历史数据缺失 /
// 迁移漏补),没法做 ownership 校验,保守 skip,不 upsert(防止把任意 sub
// 的 claims 写到 subject 为空的 identity 上)。
func TestSyncWorker_AfterRotate_EmptyExpectedSubject_SkipsUpsert(t *testing.T) {
	w, store, rfsh, audit, ui, vf, plain := newVerifHarness(t, []dueSpec{
		// 关键:dueSpec.subject = ""(DB 脏数据 / 历史 identity 无 subject)
		{id: 23, identityID: 202, uid: "u-empty-sub", plain: "rt-old-esub", subject: ""},
	})
	rfsh.ok[plain[0]] = &RefreshResult{
		AccessToken: "at-new", RefreshToken: "rt-new",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	ui.resp = &UserInfoClaims{
		Subject:          "sub-real",
		IsVerified:       IsVerifiedClaim(true),
		VerifiedAt:       VerifiedAtClaim(1715300000),
		VerifiedProvider: "cas.example.com",
		LegalName:        "合法姓名",
	}

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// RT 轮转仍成功
	if len(store.rotated) != 1 {
		t.Errorf("rotate should succeed, rotated=%v", store.rotated)
	}
	// 关键:expected subject 为空时不能 upsert
	if got := atomic.LoadInt32(&vf.calls); got != 0 {
		t.Errorf("Upsert MUST NOT be called when expected subject empty, got %d calls", got)
	}
	// refresh_ok 仍然记账
	events := audit.events()
	if len(events) != 1 || events[0] != EventRefreshOK {
		t.Errorf("audit = %v, want [refresh_ok]", events)
	}
}
