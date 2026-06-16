package messages_search

import (
	"testing"
	"time"
)

func TestParseSentAt_RFC3339(t *testing.T) {
	ts, ok := parseSentAt("2026-06-01T08:00:00Z", true)
	if !ok {
		t.Fatalf("RFC3339 should parse")
	}
	want := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC).Unix()
	if ts != want {
		t.Fatalf("ts mismatch: got %d want %d", ts, want)
	}
}

func TestParseSentAt_DateStartOfDay(t *testing.T) {
	ts, ok := parseSentAt("2026-06-01", true)
	if !ok {
		t.Fatalf("YYYY-MM-DD should parse")
	}
	want := time.Date(2026, 6, 1, 0, 0, 0, 0, userTimeZone).Unix()
	if ts != want {
		t.Fatalf("startOfDay mismatch: got %d want %d", ts, want)
	}
}

func TestParseSentAt_DateEndOfDay(t *testing.T) {
	ts, ok := parseSentAt("2026-06-01", false)
	if !ok {
		t.Fatalf("YYYY-MM-DD should parse")
	}
	want := time.Date(2026, 6, 1, 23, 59, 59, 0, userTimeZone).Unix()
	if ts != want {
		t.Fatalf("endOfDay mismatch: got %d want %d", ts, want)
	}
}

func TestParseSentAt_Invalid(t *testing.T) {
	if _, ok := parseSentAt("nonsense", true); ok {
		t.Fatalf("should reject garbage")
	}
	if _, ok := parseSentAt("", true); ok {
		t.Fatalf("should reject empty")
	}
}

func TestMsToRFC3339(t *testing.T) {
	got := msToRFC3339(time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC).Unix())
	if got != "2026-06-01T08:00:00Z" {
		t.Fatalf("unexpected RFC3339: %q", got)
	}
}

func TestMonthBucket(t *testing.T) {
	// Asia/Shanghai is UTC+8: a 2026-01-01 00:30 +08:00 timestamp should
	// land in bucket "2026-01" even though UTC has it on 2025-12-31.
	cnLoc, _ := time.LoadLocation("Asia/Shanghai")
	ts := time.Date(2026, 1, 1, 0, 30, 0, 0, cnLoc).Unix()
	if got := monthBucket(ts); got != "2026-01" {
		t.Fatalf("month bucket: got %q want 2026-01", got)
	}
}
