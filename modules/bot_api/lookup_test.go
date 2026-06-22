package bot_api

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-server/modules/auth"
)

// installRegistry replaces the global App Bot registry with a fresh
// AppBotRegistryAdapter pre-populated with the given specs. The
// production concrete type is reused (rather than a test-local fake)
// because `appBotRegistryValue` is a `sync/atomic.Value`, which panics
// if a subsequent Store passes a different concrete type than the
// first — and other tests in the suite (or main_test bootstraps) may
// already have stored a real *AppBotRegistryAdapter.
//
// On cleanup we restore the prior value if there was one, else leave a
// fresh empty *AppBotRegistryAdapter behind (atomic.Value can't store
// nil interface). FindByToken on an empty adapter returns nil, which
// matches the "no registry installed" contract from LookupAppBot's
// callers' perspective.
func installRegistry(t *testing.T, specs map[string]*AppBotRegistrySpec) {
	t.Helper()
	adapter := NewAppBotRegistryAdapter()
	for tok, sp := range specs {
		adapter.Add(tok, sp)
	}
	prev := GetAppBotRegistry()
	SetAppBotRegistry(adapter)
	t.Cleanup(func() {
		if prev == nil {
			SetAppBotRegistry(NewAppBotRegistryAdapter())
			return
		}
		SetAppBotRegistry(prev)
	})
}

// TestLookupAppBot_RegistryHit_CompleteIdentity is the seatbelt
// OctoBoooot asked for on octo-server #430: assert the registry-hit
// fast path returns a fully-populated AppBotIdentity. A future
// refactor of either AppBotRegistrySpec (field rename / removal) or
// the spec→identity copy in LookupAppBot must fail this test rather
// than silently dropping BotName / OwnerUID and re-introducing the
// bug Jerry-Xin originally flagged on the prior review round.
func TestLookupAppBot_RegistryHit_CompleteIdentity(t *testing.T) {
	installRegistry(t, map[string]*AppBotRegistrySpec{
		"app_complete_token_____________1": {
			UID:         "ab_uid_1",
			DisplayName: "Complete Bot",
			Scope:       "platform",
			CreatedBy:   "u_owner_1",
		},
	})
	ba := &BotAPI{}
	id, err := ba.LookupAppBot("app_complete_token_____________1")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if id == nil {
		t.Fatal("nil identity on registry hit")
	}
	if id.BotUID != "ab_uid_1" {
		t.Errorf("BotUID = %q want %q", id.BotUID, "ab_uid_1")
	}
	if id.BotName != "Complete Bot" {
		t.Errorf("BotName = %q want %q (DisplayName→BotName must be wired)", id.BotName, "Complete Bot")
	}
	if id.OwnerUID != "u_owner_1" {
		t.Errorf("OwnerUID = %q want %q (CreatedBy→OwnerUID must be wired)", id.OwnerUID, "u_owner_1")
	}
	if id.Scope != "platform" {
		t.Errorf("Scope = %q want platform", id.Scope)
	}
}

// TestLookupAppBot_RegistryHit_ScopeSpace_PopulatesSpaceID asserts
// that for Scope="space" the SpaceID is copied from the registry spec
// to the identity. This is the contract verify-bot handlers rely on
// to enforce per-space binding without re-querying the DB.
func TestLookupAppBot_RegistryHit_ScopeSpace_PopulatesSpaceID(t *testing.T) {
	installRegistry(t, map[string]*AppBotRegistrySpec{
		"app_space_scoped_token_________2": {
			UID:         "ab_uid_2",
			DisplayName: "Space Bot",
			Scope:       "space",
			SpaceID:     "sp_bound",
			CreatedBy:   "u_owner_2",
		},
	})
	ba := &BotAPI{}
	id, err := ba.LookupAppBot("app_space_scoped_token_________2")
	if err != nil || id == nil {
		t.Fatalf("lookup err=%v id=%v", err, id)
	}
	if id.SpaceID != "sp_bound" {
		t.Errorf("SpaceID = %q want sp_bound (Scope=space MUST surface SpaceID)", id.SpaceID)
	}
}

// TestLookupAppBot_RegistryHit_ScopePlatform_NoSpaceID asserts the
// inverse: Scope="platform" must NOT populate SpaceID even if the
// registry spec carries one. Defence against a future spec drift
// where SpaceID is set for platform-scope bots and silently leaks
// across the scope boundary as if it were a binding.
func TestLookupAppBot_RegistryHit_ScopePlatform_NoSpaceID(t *testing.T) {
	installRegistry(t, map[string]*AppBotRegistrySpec{
		"app_platform_with_stray_sp_____3": {
			UID:         "ab_uid_3",
			DisplayName: "Platform Bot",
			Scope:       "platform",
			SpaceID:     "sp_should_be_ignored",
			CreatedBy:   "u_owner_3",
		},
	})
	ba := &BotAPI{}
	id, err := ba.LookupAppBot("app_platform_with_stray_sp_____3")
	if err != nil || id == nil {
		t.Fatalf("lookup err=%v id=%v", err, id)
	}
	if id.SpaceID != "" {
		t.Errorf("SpaceID = %q want empty (Scope=platform MUST NOT surface SpaceID)", id.SpaceID)
	}
}

// TestLookupAppBot_EmptyToken_NoMatch pins the (nil,nil) early-return
// contract documented in lookup.go: an empty token is a no-match (not
// an error, not a panic, no registry lookup performed).
func TestLookupAppBot_EmptyToken_NoMatch(t *testing.T) {
	// Install an empty registry so we don't depend on prior global
	// state; the empty-token guard must short-circuit before the
	// registry lookup either way.
	installRegistry(t, nil)
	ba := &BotAPI{}
	id, err := ba.LookupAppBot("")
	if err != nil {
		t.Fatalf("empty token must be no-match, got err=%v", err)
	}
	if id != nil {
		t.Errorf("empty token must return nil identity, got %+v", id)
	}
}

// TestLookupAppBot_RegistryMiss_FallsThroughToDB_NilBA confirms the
// registry-miss path's first action is the DB call. We don't have a
// DB injection seam in this PR, so we use a nil ba.db to assert "the
// code reached the DB-call line" via a nil-deref panic. Negative-
// control test: if a future change short-circuited a registry-miss
// to (nil,nil) without consulting the DB, it would silently change
// the semantic and this test would stop panicking.
func TestLookupAppBot_RegistryMiss_FallsThroughToDB_NilBA(t *testing.T) {
	installRegistry(t, nil) // empty registry — every token misses
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected nil-deref panic from DB fallthrough on registry miss; got nil — registry-miss may be short-circuiting without consulting the DB")
		}
	}()
	ba := &BotAPI{} // ba.db is nil — DB call must panic to prove we got there
	_, _ = ba.LookupAppBot("app_unknown_token______________X")
}

// TestLookupUserBot_EmptyToken_NoMatch pins the same (nil,nil)
// early-return contract for the User Bot path.
func TestLookupUserBot_EmptyToken_NoMatch(t *testing.T) {
	ba := &BotAPI{}
	id, err := ba.LookupUserBot("")
	if err != nil {
		t.Fatalf("empty token must be no-match, got err=%v", err)
	}
	if id != nil {
		t.Errorf("empty token must return nil identity, got %+v", id)
	}
}

// _ = auth.ErrAppBotUnpublished keeps the symbol reachable from this
// test file; the DB-path status!=1 → ErrAppBotUnpublished branch is
// covered by integration tests in modules/auth/api_*_test.go (verify-
// bot handler with a real unpublished bot row), since the bot_api db
// layer uses a concrete *gocraft/dbr session and isn't unit-mockable
// without a wider refactor.
var _ = auth.ErrAppBotUnpublished
