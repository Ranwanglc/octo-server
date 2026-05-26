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

func TestCronScheduler_Next(t *testing.T) {
	s, _ := NewCronScheduler(func(string, time.Time) {})
	s.Start()
	defer s.Stop(context.Background())

	// 未注册的 trigger 返回零值
	if got := s.Next("missing"); !got.IsZero() {
		t.Fatalf("expected zero time for unknown trigger, got %v", got)
	}

	// 注册后应该能拿到未来的下次时间
	if err := s.Add("trg", "*/1 * * * *", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := s.Next("trg")
	if got.IsZero() {
		t.Fatalf("expected non-zero next time")
	}
	if !got.After(time.Now()) {
		t.Fatalf("expected next time in the future, got %v", got)
	}
}

func TestValidateExpression(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		tz      string
		wantErr bool
	}{
		{"empty", "", "", true},
		{"valid 5-field", "*/1 * * * *", "", false},
		{"valid 6-field", "*/30 * * * * *", "", false},
		{"valid timezone", "0 0 * * *", "UTC", false},
		{"invalid expr", "not-a-cron", "", true},
		{"too many fields", "1 2 3 4 5 6 7 8", "", true},
		{"invalid timezone", "* * * * *", "Mars/Olympus", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateExpression(tc.expr, tc.tz)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
