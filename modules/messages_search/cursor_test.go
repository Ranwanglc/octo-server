package messages_search

import (
	"testing"
)

func TestCursor_RoundTrip(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "test-secret"}
	encoded := encodeCursor(cfg, 1717000000, 9876543210, nil)
	if encoded == "" {
		t.Fatalf("encoded cursor is empty")
	}
	ts, msgID, score, err := decodeCursor(cfg, encoded)
	if err != nil {
		t.Fatalf("decodeCursor unexpected error: %v", err)
	}
	if ts != 1717000000 || msgID != 9876543210 {
		t.Fatalf("decoded mismatch: ts=%d msgID=%d", ts, msgID)
	}
	if score != nil {
		t.Fatalf("legacy 2-tuple cursor should decode score=nil, got %v", *score)
	}
}

func TestCursor_RelevanceRoundTrip(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "test-secret"}
	want := 12.345
	encoded := encodeCursor(cfg, 1717000000, 9876543210, &want)
	if encoded == "" {
		t.Fatalf("encoded relevance cursor is empty")
	}
	ts, msgID, score, err := decodeCursor(cfg, encoded)
	if err != nil {
		t.Fatalf("decodeCursor unexpected error: %v", err)
	}
	if ts != 1717000000 || msgID != 9876543210 {
		t.Fatalf("decoded mismatch: ts=%d msgID=%d", ts, msgID)
	}
	if score == nil {
		t.Fatalf("relevance cursor should decode score non-nil")
	}
	if *score != want {
		t.Fatalf("score mismatch: got %v want %v", *score, want)
	}
}

func TestCursor_LegacyFormatBackCompat(t *testing.T) {
	// A cursor encoded with score=nil must decode back to score==nil so the
	// handler can detect a stale relevance cursor and fall back cleanly. This
	// also pins the wire format: omitempty keeps the "s" key out of the body.
	cfg := SearchConfig{CursorHMAC: "k"}
	enc := encodeCursor(cfg, 1, 2, nil)
	_, _, score, err := decodeCursor(cfg, enc)
	if err != nil {
		t.Fatalf("decode legacy: %v", err)
	}
	if score != nil {
		t.Fatalf("legacy cursor must decode score==nil, got %v", *score)
	}
}

func TestCursor_TamperRejected(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "test-secret"}
	enc := encodeCursor(cfg, 1717000000, 100, nil)
	tampered := enc[:len(enc)-2] + "AA"
	if _, _, _, err := decodeCursor(cfg, tampered); err == nil {
		t.Fatalf("expected tamper error, got nil")
	}
}

func TestCursor_DifferentKeyRejected(t *testing.T) {
	enc := encodeCursor(SearchConfig{CursorHMAC: "key-a"}, 1, 2, nil)
	if _, _, _, err := decodeCursor(SearchConfig{CursorHMAC: "key-b"}, enc); err == nil {
		t.Fatalf("expected sig mismatch, got nil")
	}
}

func TestCursor_EmptyAndMalformed(t *testing.T) {
	cfg := SearchConfig{CursorHMAC: "k"}
	if _, _, _, err := decodeCursor(cfg, ""); err == nil {
		t.Fatalf("expected empty cursor error")
	}
	if _, _, _, err := decodeCursor(cfg, "@@@@notbase64"); err == nil {
		t.Fatalf("expected malformed cursor error")
	}
	if _, _, _, err := decodeCursor(cfg, "AAAA"); err == nil {
		t.Fatalf("expected too-short cursor error")
	}
}
