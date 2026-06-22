package auth

import (
	"context"
	"errors"
	"testing"
)

// fakeBotLookup is an in-memory BotLookup for service tests.
type fakeBotLookup struct {
	user map[string]*UserBotIdentity
	app  map[string]*AppBotIdentity
	err  error
}

func (f *fakeBotLookup) LookupUserBot(token string) (*UserBotIdentity, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.user[token], nil
}

func (f *fakeBotLookup) LookupAppBot(token string) (*AppBotIdentity, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.app[token], nil
}

// TestRegistrySetGetRoundTrip pins the BotLookup / APIKeyLookup
// registry contract used by bot_api / usersecret to expose themselves
// to modules/auth at SetupAPI time.
func TestRegistrySetGetRoundTrip(t *testing.T) {
	// Don't use t.Parallel() — the registry is process-global; parallel
	// tests would race the singleton.
	prevBot := GetBotLookup()
	prevKey := GetAPIKeyLookup()
	t.Cleanup(func() {
		// restore whatever was set before this test (in case another
		// test depends on a particular value)
		if prevBot != nil {
			SetBotLookup(prevBot)
		}
		if prevKey != nil {
			SetAPIKeyLookup(prevKey)
		}
	})

	bl := &fakeBotLookup{}
	SetBotLookup(bl)
	if got := GetBotLookup(); got != bl {
		t.Fatalf("GetBotLookup after set = %v, want fake instance", got)
	}

	// SetBotLookup(nil) is documented as no-op (does not clear).
	SetBotLookup(nil)
	if got := GetBotLookup(); got != bl {
		t.Fatalf("Set(nil) must not clear; got %v want previous fake", got)
	}
}

// TestServiceVerifyBotRoutesByPrefix covers the App Bot vs User Bot
// routing in VerifyBot. We use a Service constructed with a nil ctx
// — that's fine because we only exercise the prefix-routing paths
// where the only ctx use is via lookupUserName (DB call) which we
// avoid by giving the bot an empty owner name source (the test asserts
// the routing + identity-struct shape, not DB-hydrated owner_name).
func TestServiceVerifyBotRoutesByPrefix(t *testing.T) {
	prev := GetBotLookup()
	t.Cleanup(func() {
		if prev != nil {
			SetBotLookup(prev)
		}
	})

	bl := &fakeBotLookup{
		user: map[string]*UserBotIdentity{
			"bf_user_token": {BotUID: "b1", BotName: "Bot One", OwnerUID: "u1"},
		},
		app: map[string]*AppBotIdentity{
			"app_token": {BotUID: "a1", BotName: "App One", OwnerUID: "u2", Scope: "space", SpaceID: "sp_x"},
		},
	}
	SetBotLookup(bl)

	// Service.VerifyBot for App Bot path — ctx is nil safe because we
	// don't reach the DB path; lookupUserName tolerates nil session
	// (returns "" on error). Use a zero-value Service.
	s := &Service{}

	// User Bot path: bf_ prefix → LookupUserBot
	resp, err := s.VerifyBot(context.Background(), VerifyBotReq{BotToken: "bf_user_token"})
	if err != nil {
		t.Fatalf("VerifyBot(user): %v", err)
	}
	if resp.BotKind != "user" || resp.BotUID != "b1" || resp.OwnerUID != "u1" {
		t.Fatalf("user bot identity mismatch: %+v", resp)
	}
	if resp.SchemaVersion != 1 || resp.Kind != "bot" {
		t.Fatalf("envelope fields wrong: %+v", resp)
	}

	// App Bot path: app_ prefix → LookupAppBot, scope+space preserved
	resp, err = s.VerifyBot(context.Background(), VerifyBotReq{BotToken: "app_token"})
	if err != nil {
		t.Fatalf("VerifyBot(app): %v", err)
	}
	if resp.BotKind != "app" || resp.BotUID != "a1" || resp.Scope != "space" || resp.SpaceID != "sp_x" {
		t.Fatalf("app bot identity mismatch: %+v", resp)
	}

	// Unknown token → ErrInvalidBotToken
	_, err = s.VerifyBot(context.Background(), VerifyBotReq{BotToken: "bf_does_not_exist"})
	if !errors.Is(err, ErrInvalidBotToken) {
		t.Fatalf("expected ErrInvalidBotToken for unknown token, got %v", err)
	}
}

// TestServiceVerifyBotEmptyToken pins the empty/whitespace-token
// short-circuit.
func TestServiceVerifyBotEmptyToken(t *testing.T) {
	s := &Service{}
	for _, tok := range []string{"", "   ", "\t\n"} {
		if _, err := s.VerifyBot(context.Background(), VerifyBotReq{BotToken: tok}); !errors.Is(err, ErrInvalidBotToken) {
			t.Fatalf("VerifyBot(%q): want ErrInvalidBotToken, got %v", tok, err)
		}
	}
}

// TestServiceVerifyBotAppUnpublished pins the ErrAppBotUnpublished
// → ErrBotUnavailable mapping that drives the 503 response.
func TestServiceVerifyBotAppUnpublished(t *testing.T) {
	prev := GetBotLookup()
	t.Cleanup(func() {
		if prev != nil {
			SetBotLookup(prev)
		}
	})
	bl := &fakeBotLookup{err: ErrAppBotUnpublished}
	// err is sticky, returned by both LookupUserBot and LookupAppBot
	SetBotLookup(bl)

	s := &Service{}
	_, err := s.VerifyBot(context.Background(), VerifyBotReq{BotToken: "app_x"})
	if !errors.Is(err, ErrBotUnavailable) {
		t.Fatalf("ErrAppBotUnpublished must map to ErrBotUnavailable, got %v", err)
	}
}

// TestServiceVerifyBotNoLookupRegistered pins the no-provider →
// ErrUpstreamFailure path (boot race / misconfigured DI).
func TestServiceVerifyBotNoLookupRegistered(t *testing.T) {
	prev := GetBotLookup()
	t.Cleanup(func() {
		if prev != nil {
			SetBotLookup(prev)
		}
	})
	// Reset the registry value to zero by storing an empty holder.
	// (atomic.Value has no documented way to clear; we replace with a
	// holder wrapping a nil interface, which GetBotLookup unwraps to nil.)
	botLookupValue.Store(&botLookupHolder{v: nil})

	s := &Service{}
	_, err := s.VerifyBot(context.Background(), VerifyBotReq{BotToken: "bf_x"})
	if !errors.Is(err, ErrUpstreamFailure) {
		t.Fatalf("no-provider must return ErrUpstreamFailure, got %v", err)
	}
}
