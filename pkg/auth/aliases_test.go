// Guard tests for the Deprecated pkg/auth alias shim. These tests are
// intentionally small — they do NOT re-test the underlying codec / parser
// (those tests live with the canonical implementation under modules/auth).
// Their job is to fail loud if an alias drifts: missing symbol, mismatched
// signature, or broken sentinel-error identity that would silently break
// errors.Is for the six existing pkg/auth call sites during the
// six-month deprecation window.

package auth

import (
	"errors"
	"testing"

	modulesauth "github.com/Mininglamp-OSS/octo-server/modules/auth"
)

// TestAliasRoundTrip ensures Encode and Decode reachable through the shim
// produce an identical TokenInfo to a round-trip through the canonical
// package — i.e. the var-aliases really point at the same function values.
func TestAliasRoundTrip(t *testing.T) {
	t.Parallel()
	in := TokenInfo{UID: "u1", Name: "alice", Role: "admin", Language: "zh-CN"}
	encoded, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode via shim: %v", err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode via shim: %v", err)
	}
	if got != in {
		t.Fatalf("round trip via shim mismatch: got %+v want %+v", got, in)
	}

	// Cross-check: a value encoded by the canonical package must decode via
	// the shim — this catches the case where Decode is silently replaced by
	// a divergent implementation.
	encodedCanonical, err := modulesauth.Encode(in)
	if err != nil {
		t.Fatalf("Encode via canonical: %v", err)
	}
	if encodedCanonical != encoded {
		t.Fatalf("shim Encode != canonical Encode: %q vs %q", encoded, encodedCanonical)
	}
}

// TestAliasSentinelIdentity pins the errors.Is contract for the re-exported
// sentinel errors. Existing callers do `errors.Is(err, auth.ErrInvalidToken)`
// where err originates from the canonical modules/auth package; this only
// works if the shim's var holds the same error value, not a copy.
func TestAliasSentinelIdentity(t *testing.T) {
	t.Parallel()
	if ErrEmptyToken != modulesauth.ErrEmptyToken {
		t.Fatal("ErrEmptyToken alias is not identical to the canonical sentinel — errors.Is will diverge")
	}
	if ErrInvalidToken != modulesauth.ErrInvalidToken {
		t.Fatal("ErrInvalidToken alias is not identical to the canonical sentinel — errors.Is will diverge")
	}

	// Functional check: an error produced by Decode (which lives in
	// modules/auth) must still match the shim's sentinel via errors.Is.
	if _, err := Decode(""); !errors.Is(err, ErrEmptyToken) {
		t.Fatalf("errors.Is via shim failed for canonical sentinel: %v", err)
	}
	if _, err := Decode("u1"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("errors.Is via shim failed for canonical sentinel: %v", err)
	}
}
