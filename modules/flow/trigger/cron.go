package trigger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// FireFunc 由调用方提供：cron tick 时由 scheduler 调用，参数是触发器 id。
type FireFunc func(triggerID string, scheduledAt time.Time)

// CronScheduler 管理基于 robfig/cron 的定时触发。
type CronScheduler struct {
	mu       sync.Mutex
	cron     *cron.Cron
	fire     FireFunc
	registry map[string]cron.EntryID // triggerID → entryID
}

// NewCronScheduler 构造。fire 必须非 nil。
func NewCronScheduler(fire FireFunc) (*CronScheduler, error) {
	if fire == nil {
		return nil, errors.New("cron: fire callback is required")
	}
	c := cron.New(cron.WithSeconds(), cron.WithChain(cron.Recover(cron.DefaultLogger)))
	return &CronScheduler{
		cron:     c,
		fire:     fire,
		registry: map[string]cron.EntryID{},
	}, nil
}

// Start 启动调度
func (s *CronScheduler) Start() { s.cron.Start() }

// Stop 停止调度（等待 in-flight job 完成）
func (s *CronScheduler) Stop(ctx context.Context) error {
	stopCtx := s.cron.Stop()
	select {
	case <-stopCtx.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Add 注册一个 cron 表达式。expression 接受 5 字段（"m h dom mon dow"）或
// 6 字段（带秒）。timezone 为空时使用 Local。
func (s *CronScheduler) Add(triggerID, expression, timezone string) error {
	if triggerID == "" {
		return errors.New("cron: triggerID required")
	}
	if expression == "" {
		return errors.New("cron: expression required")
	}
	loc := time.Local
	if timezone != "" {
		l, err := time.LoadLocation(timezone)
		if err != nil {
			return fmt.Errorf("cron: invalid timezone %q: %w", timezone, err)
		}
		loc = l
	}
	// 自动判断是否带秒
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour |
		cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	schedule, err := parser.Parse(expression)
	if err != nil {
		return fmt.Errorf("cron: parse %q: %w", expression, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.registry[triggerID]; ok {
		s.cron.Remove(old)
		delete(s.registry, triggerID)
	}
	id := s.cron.Schedule(scheduleInLocation{Schedule: schedule, loc: loc},
		cron.FuncJob(func() {
			s.fire(triggerID, time.Now().In(loc))
		}))
	s.registry[triggerID] = id
	return nil
}

// Remove 取消注册
func (s *CronScheduler) Remove(triggerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.registry[triggerID]; ok {
		s.cron.Remove(id)
		delete(s.registry, triggerID)
	}
}

// Count 当前注册的 trigger 数量
func (s *CronScheduler) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.registry)
}

// Next 返回 triggerID 对应的下次触发时间。未注册时返回零值 time.Time。
func (s *CronScheduler) Next(triggerID string) time.Time {
	s.mu.Lock()
	id, ok := s.registry[triggerID]
	s.mu.Unlock()
	if !ok {
		return time.Time{}
	}
	entry := s.cron.Entry(id)
	return entry.Next
}

// ValidateExpression 校验 cron 表达式（5/6 字段均允许）以及可选 timezone。
// 用于 flow 创建/更新前置校验，不会向调度器注册任何 job。
func ValidateExpression(expression, timezone string) error {
	if expression == "" {
		return errors.New("cron: expression required")
	}
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return fmt.Errorf("cron: invalid timezone %q: %w", timezone, err)
		}
	}
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour |
		cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(expression); err != nil {
		return fmt.Errorf("cron: parse %q: %w", expression, err)
	}
	return nil
}

// scheduleInLocation 让 schedule 在指定时区下计算 Next
type scheduleInLocation struct {
	cron.Schedule
	loc *time.Location
}

func (s scheduleInLocation) Next(t time.Time) time.Time {
	return s.Schedule.Next(t.In(s.loc))
}
