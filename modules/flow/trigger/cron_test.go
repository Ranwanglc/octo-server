package trigger

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCronScheduler_Fire(t *testing.T) {
	var n int32
	s, err := NewCronScheduler(func(triggerID string, scheduledAt time.Time) {
		atomic.AddInt32(&n, 1)
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s.Start()
	defer s.Stop(context.Background())

	// 每秒触发
	if err := s.Add("trg1", "* * * * * *", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	if s.Count() != 1 {
		t.Fatalf("count=%d", s.Count())
	}
	time.Sleep(2200 * time.Millisecond)
	got := atomic.LoadInt32(&n)
	if got < 1 {
		t.Fatalf("expected >=1 fires, got %d", got)
	}
}

func TestCronScheduler_Replace(t *testing.T) {
	s, _ := NewCronScheduler(func(string, time.Time) {})
	s.Start()
	defer s.Stop(context.Background())
	if err := s.Add("t", "* * * * *", ""); err != nil {
		t.Fatalf("add1: %v", err)
	}
	// 再次 Add 同一 id 应该替换而不是新增
	if err := s.Add("t", "0 */5 * * * *", ""); err != nil {
		t.Fatalf("add2: %v", err)
	}
	if s.Count() != 1 {
		t.Fatalf("count=%d", s.Count())
	}
	s.Remove("t")
	if s.Count() != 0 {
		t.Fatalf("count after remove=%d", s.Count())
	}
}

func TestCronScheduler_InvalidExpression(t *testing.T) {
	s, _ := NewCronScheduler(func(string, time.Time) {})
	if err := s.Add("x", "not-a-cron", ""); err == nil {
		t.Fatalf("expected err")
	}
}

func TestCronScheduler_InvalidTimezone(t *testing.T) {
	s, _ := NewCronScheduler(func(string, time.Time) {})
	if err := s.Add("x", "* * * * *", "Mars/Olympus"); err == nil {
		t.Fatalf("expected tz err")
	}
}
