package messages_search

import "testing"

func TestHashKeyword(t *testing.T) {
	if got := hashKeyword(""); got != "" {
		t.Fatalf("empty keyword should yield empty hash, got %q", got)
	}
	if got := hashKeyword("hello"); len(got) != 16 {
		t.Fatalf("hash length should be 16 hex chars, got %d (%q)", len(got), got)
	}
	// Same keyword always hashes to the same prefix (deterministic).
	if hashKeyword("hello") != hashKeyword("hello") {
		t.Fatalf("hash should be deterministic")
	}
	if hashKeyword("hello") == hashKeyword("hellp") {
		t.Fatalf("different inputs should produce different hashes")
	}
}

func TestUniqUIDs(t *testing.T) {
	in := []string{"a", "b", "a", "", "c", "b"}
	out := uniqUIDs(in)
	want := []string{"a", "b", "c"}
	if len(out) != len(want) {
		t.Fatalf("len: got %d want %d", len(out), len(want))
	}
	for i, v := range want {
		if out[i] != v {
			t.Errorf("at %d: got %q want %q", i, out[i], v)
		}
	}
}

func TestSenderCache_TTLZeroMeansForever(t *testing.T) {
	// TTL=0 disables expiry — entries live until evicted by capacity.
	c := newSenderCache(8, 0)
	c.Put("u1", senderInfo{Name: "Alice"})
	if got, ok := c.Get("u1"); !ok || got.Name != "Alice" {
		t.Fatalf("ttl=0 should keep the entry forever, got ok=%v info=%+v", ok, got)
	}
}

func TestSenderCache_Hit(t *testing.T) {
	c := newSenderCache(8, 60_000_000_000) // 60s in nanoseconds (matches time.Duration)
	c.Put("u1", senderInfo{Name: "Alice", Avatar: "users/u1/avatar"})
	got, ok := c.Get("u1")
	if !ok {
		t.Fatalf("entry should be cached")
	}
	if got.Name != "Alice" || got.Avatar != "users/u1/avatar" {
		t.Fatalf("cached info wrong: %+v", got)
	}
}

// TestSenderCache_ScopedKeys verifies that the senderCacheKey scoping prevents
// G1's per-group remark from leaking into G2 / DM rendering for the same uid.
// Regression guard for the P0-2 finding in REVIEW-2026-06-12.md (line 79–123).
func TestSenderCache_ScopedKeys(t *testing.T) {
	const uid = "u1"
	keyG1 := senderCacheKey(channelTypeGroup, "G1", uid)
	keyG2 := senderCacheKey(channelTypeGroup, "G2", uid)
	keyDM := senderCacheKey(channelTypePerson, "peer", uid)
	keyThread := senderCacheKey(channelTypeThread, "G1____abcd1234", uid)

	if keyG1 == keyG2 {
		t.Fatalf("G1 and G2 must produce different cache keys: both=%q", keyG1)
	}
	if keyG1 == keyDM {
		t.Fatalf("group and DM must produce different cache keys: g=%q dm=%q", keyG1, keyDM)
	}
	if keyThread != "g:G1:"+uid {
		t.Fatalf("thread key should reuse parent group_no: got %q", keyThread)
	}
	if keyDM != "u:"+uid {
		t.Fatalf("DM key should be uid-scoped: got %q", keyDM)
	}

	// Cross-scope writes must not stomp each other.
	c := newSenderCache(8, 60_000_000_000)
	c.Put(keyG1, senderInfo{Name: "Boss", Avatar: "users/u1/avatar"})
	c.Put(keyG2, senderInfo{Name: "Mom", Avatar: "users/u1/avatar"})
	c.Put(keyDM, senderInfo{Name: "Alice", Avatar: "users/u1/avatar"})

	got, _ := c.Get(keyG1)
	if got.Name != "Boss" {
		t.Fatalf("G1 name leak: got %q want Boss", got.Name)
	}
	got, _ = c.Get(keyG2)
	if got.Name != "Mom" {
		t.Fatalf("G2 name leak: got %q want Mom", got.Name)
	}
	got, _ = c.Get(keyDM)
	if got.Name != "Alice" {
		t.Fatalf("DM name leak: got %q want Alice", got.Name)
	}
}
