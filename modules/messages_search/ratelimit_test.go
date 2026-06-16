package messages_search

import (
	"strconv"
	"testing"
	"time"
)

func TestUIDLimiter_AllowsBurst(t *testing.T) {
	l := newUIDLimiter(5, 20)
	for i := 0; i < 20; i++ {
		if !l.Allow("uid-1") {
			t.Fatalf("burst should fit 20 immediate calls (i=%d)", i)
		}
	}
}

func TestUIDLimiter_ExhaustsAfterBurst(t *testing.T) {
	l := newUIDLimiter(5, 5)
	for i := 0; i < 5; i++ {
		if !l.Allow("uid-burst") {
			t.Fatalf("burst should fit 5 immediate calls (i=%d)", i)
		}
	}
	// Sixth call within the same instant must trip the limit.
	if l.Allow("uid-burst") {
		t.Fatalf("expected limiter to reject 6th call within burst window")
	}
}

func TestUIDLimiter_PerUIDIsolation(t *testing.T) {
	l := newUIDLimiter(1, 1)
	if !l.Allow("alice") {
		t.Fatalf("alice should pass first call")
	}
	if !l.Allow("bob") {
		t.Fatalf("bob should not be affected by alice's bucket")
	}
}

func TestUIDLimiter_EmptyKeyFailsClosed(t *testing.T) {
	// An unidentifiable caller must not be exempt from the search budget;
	// the middleware substitutes ip:{clientIP} before calling Allow, so an
	// empty key only ever means a wiring bug — reject it.
	l := newUIDLimiter(1, 1)
	if l.Allow("") {
		t.Fatalf("empty key must fail closed")
	}
}

// TestUIDLimiter_HardCapEvictsOldest guards P1-10: when the bucket map
// grows past uidLimiterMaxBuckets the limiter must evict oldest entries
// rather than letting a per-uid attacker grow memory unbounded.
func TestUIDLimiter_HardCapEvictsOldest(t *testing.T) {
	l := newUIDLimiter(5, 5)
	// Pre-seed 10K + 50 buckets with monotonically increasing `seen` so
	// oldest = lowest index. We mutate the map directly to skip the rate
	// limiter cost and isolate the eviction behaviour.
	l.mu.Lock()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < uidLimiterMaxBuckets+50; i++ {
		uid := "uid-" + strconv.Itoa(i)
		l.buckets[uid] = &uidBucket{seen: base.Add(time.Duration(i) * time.Second)}
	}
	l.mu.Unlock()

	// Trigger the sweep via Allow on a fresh uid; since `last` was set
	// long ago this enters the GC path.
	l.last = time.Now().Add(-10 * time.Minute)
	l.Allow("trigger")

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > uidLimiterMaxBuckets+1 {
		// +1 leeway for the freshly inserted "trigger" uid that may push
		// us back over by one before the next sweep.
		t.Fatalf("expected map to be <= %d after sweep, got %d",
			uidLimiterMaxBuckets+1, len(l.buckets))
	}
	// Oldest entry (uid-0) must have been evicted; freshest (the
	// uid-(N+49)) must still be there.
	if _, ok := l.buckets["uid-0"]; ok {
		t.Fatalf("oldest uid-0 should have been evicted")
	}
	freshest := "uid-" + strconv.Itoa(uidLimiterMaxBuckets+49)
	if _, ok := l.buckets[freshest]; !ok {
		t.Fatalf("freshest %s should remain", freshest)
	}
}
