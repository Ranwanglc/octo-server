package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mininglamp-OSS/octo-server/modules/flow/trigger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Service 是 flow 模块对外的高层服务。
//
// 它把 DB / Engine / 触发器调度串起来：
//   - CRUD：创建 / 更新 / 列表 / 删除（同时 GC 触发器）
//   - Activate/Deactivate：注册/反注册 webhook + cron
//   - Execute：手动触发
//   - HandleWebhook：接收 HTTP webhook 入口
//
// 触发器持久化在 flow_triggers 表；启动时（Start）从 DB 加载所有 cron。
type Service struct {
	db     *DB
	engine *Engine
	cron   *trigger.CronScheduler
	log    *zap.Logger
}

// NewService 构造。log 为 nil 时使用 nop。
func NewService(db *DB, engine *Engine, log *zap.Logger) (*Service, error) {
	if db == nil || engine == nil {
		return nil, errors.New("flow service: db and engine are required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	s := &Service{db: db, engine: engine, log: log}
	cron, err := trigger.NewCronScheduler(func(triggerID string, scheduledAt time.Time) {
		s.fireCron(triggerID, scheduledAt)
	})
	if err != nil {
		return nil, err
	}
	s.cron = cron
	return s, nil
}

// Start 启动 cron 调度并加载活跃的 cron 触发器
func (s *Service) Start() error {
	s.cron.Start()
	cronTriggers, err := s.db.ListTriggersByType(TriggerTypeCron)
	if err != nil {
		return fmt.Errorf("flow service: load cron triggers: %w", err)
	}
	for _, t := range cronTriggers {
		cfg, err := t.DecodeConfig()
		if err != nil {
			s.log.Warn("decode cron trigger config", zap.String("trigger_id", t.ID), zap.Error(err))
			continue
		}
		expr, _ := cfg["expression"].(string)
		tz, _ := cfg["timezone"].(string)
		if err := s.cron.Add(t.ID, expr, tz); err != nil {
			s.log.Warn("add cron trigger failed", zap.String("trigger_id", t.ID), zap.Error(err))
		}
	}
	s.log.Info("flow service started",
		zap.Int("cron_triggers", s.cron.Count()))
	return nil
}

// Stop 停止 cron 调度
func (s *Service) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.cron.Stop(ctx)
}

// CreateFlow 创建 flow（draft 状态，不会注册触发器）。
//
// definition 是已 decode 的 Definition；caller 负责把 client 的 JSON 解析过来。
func (s *Service) CreateFlow(spaceID, name, description string, definition *Definition, createdBy string) (*Flow, error) {
	if name == "" {
		return nil, errors.New("flow: name required")
	}
	if definition == nil {
		definition = &Definition{}
	}
	if err := ValidateDefinitionTriggers(definition); err != nil {
		return nil, err
	}
	defJSON, err := json.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("marshal definition: %w", err)
	}
	now := time.Now()
	f := &Flow{
		ID:          uuid.NewString(),
		SpaceID:     spaceID,
		Name:        name,
		Description: description,
		Definition:  string(defJSON),
		Version:     1,
		Status:      FlowStatusDraft,
		CreatedBy:   createdBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.db.InsertFlow(f); err != nil {
		return nil, err
	}
	// 同步写一个版本快照
	_ = s.db.InsertFlowVersion(&FlowVersionRow{
		ID:         uuid.NewString(),
		FlowID:     f.ID,
		Version:    f.Version,
		Definition: f.Definition,
		Changelog:  "initial",
		CreatedBy:  createdBy,
		CreatedAt:  now,
	})
	return f, nil
}

// UpdateFlow 更新 flow definition。会写一个新版本快照，version + 1。
//
// 如果 flow 当前是 active 状态，会先反注册再以新 definition 重新注册触发器。
func (s *Service) UpdateFlow(id, name, description string, definition *Definition, changelog, createdBy string) (*Flow, error) {
	f, err := s.db.GetFlow(id)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrNotFound
	}
	wasActive := f.Status == FlowStatusActive
	if wasActive {
		if err := s.deactivateTriggers(f); err != nil {
			s.log.Warn("deactivate triggers", zap.String("flow_id", f.ID), zap.Error(err))
		}
	}
	if name != "" {
		f.Name = name
	}
	if description != "" {
		f.Description = description
	}
	if definition != nil {
		if err := ValidateDefinitionTriggers(definition); err != nil {
			// 校验失败但状态可能已 deactivate；尝试还原
			if wasActive {
				if rerr := s.activateTriggers(f); rerr != nil {
					s.log.Warn("rollback re-activate after validation error",
						zap.String("flow_id", f.ID), zap.Error(rerr))
				}
			}
			return nil, err
		}
		defJSON, err := json.Marshal(definition)
		if err != nil {
			return nil, err
		}
		f.Definition = string(defJSON)
		f.Version++
	}
	if err := s.db.UpdateFlow(f); err != nil {
		return nil, err
	}
	_ = s.db.InsertFlowVersion(&FlowVersionRow{
		ID:         uuid.NewString(),
		FlowID:     f.ID,
		Version:    f.Version,
		Definition: f.Definition,
		Changelog:  changelog,
		CreatedBy:  createdBy,
		CreatedAt:  time.Now(),
	})
	if wasActive {
		if err := s.activateTriggers(f); err != nil {
			s.log.Warn("re-activate after update", zap.String("flow_id", f.ID), zap.Error(err))
		}
	}
	return f, nil
}

// GetFlow 按 id 取
func (s *Service) GetFlow(id string) (*Flow, error) { return s.db.GetFlow(id) }

// ListFlows 列出
func (s *Service) ListFlows(spaceID, status string, limit, offset int) ([]*Flow, error) {
	return s.db.ListFlows(spaceID, status, limit, offset)
}

// DeleteFlow 删除。会先反注册触发器
func (s *Service) DeleteFlow(id string) error {
	f, err := s.db.GetFlow(id)
	if err != nil {
		return err
	}
	if f == nil {
		return nil
	}
	_ = s.deactivateTriggers(f)
	return s.db.DeleteFlow(id)
}

// Activate 把 flow 状态置为 active 并注册触发器
func (s *Service) Activate(id string) error {
	f, err := s.db.GetFlow(id)
	if err != nil {
		return err
	}
	if f == nil {
		return ErrNotFound
	}
	def, err := f.DecodeDefinition()
	if err != nil {
		return fmt.Errorf("decode definition: %w", err)
	}
	if err := ValidateDefinitionTriggers(def); err != nil {
		return err
	}
	if err := s.activateTriggers(f); err != nil {
		return err
	}
	return s.db.UpdateFlowStatus(id, FlowStatusActive)
}

// Deactivate 反注册触发器并把状态置为 draft
func (s *Service) Deactivate(id string) error {
	f, err := s.db.GetFlow(id)
	if err != nil {
		return err
	}
	if f == nil {
		return ErrNotFound
	}
	if err := s.deactivateTriggers(f); err != nil {
		return err
	}
	return s.db.UpdateFlowStatus(id, FlowStatusDraft)
}

// Execute 手动触发
func (s *Service) Execute(ctx context.Context, flowID string, input map[string]any) (*Execution, error) {
	f, err := s.db.GetFlow(flowID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrNotFound
	}
	return s.engine.StartExecution(ctx, f, "", TriggerData{
		Type:  TriggerTypeManual,
		Input: input,
	})
}

// HandleWebhook 处理外部 webhook：path 命中 → 校验签名 → 启动执行
func (s *Service) HandleWebhook(ctx context.Context, path string, body []byte, headers map[string]string) (*Execution, error) {
	t, err := s.db.GetTriggerByWebhookPath(path)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNotFound
	}
	cfg, err := t.DecodeConfig()
	if err != nil {
		return nil, fmt.Errorf("decode trigger config: %w", err)
	}
	secret, _ := cfg["secret"].(string)
	sigHeader, _ := cfg["signature_header"].(string)
	algo, _ := cfg["signature_algo"].(string)
	headerVal := ""
	if sigHeader != "" {
		headerVal = headers[sigHeader]
	}
	if err := trigger.VerifyWebhookSignature(body, secret, headerVal, algo); err != nil {
		return nil, err
	}
	var payload map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}
	f, err := s.db.GetFlow(t.FlowID)
	if err != nil {
		return nil, err
	}
	if f == nil || f.Status != FlowStatusActive {
		return nil, fmt.Errorf("flow %s is not active", t.FlowID)
	}
	return s.engine.StartExecution(ctx, f, t.ID, TriggerData{
		Type:    TriggerTypeWebhook,
		Payload: payload,
	})
}

// ListExecutions 列出
func (s *Service) ListExecutions(flowID string, limit, offset int) ([]*Execution, error) {
	return s.db.ListExecutionsByFlow(flowID, limit, offset)
}

// GetExecution 详情（含节点）
func (s *Service) GetExecution(id string) (*Execution, []*NodeExecution, error) {
	exec, err := s.db.GetExecution(id)
	if err != nil {
		return nil, nil, err
	}
	if exec == nil {
		return nil, nil, ErrNotFound
	}
	nodes, err := s.db.ListNodeExecutions(id)
	return exec, nodes, err
}

// CancelExecution 取消
func (s *Service) CancelExecution(id string) error {
	return s.engine.CancelExecution(id)
}

// NextTriggerAt 返回 flow 下一次定时触发的时间。
//
// 如果 flow 没有 cron 触发器，或当前未激活（cron 未注册到调度器），返回 nil。
// 如果有多个 cron 触发器，返回最早的一次。
func (s *Service) NextTriggerAt(flowID string) *time.Time {
	if flowID == "" {
		return nil
	}
	triggers, err := s.db.ListTriggersByFlow(flowID)
	if err != nil {
		s.log.Warn("NextTriggerAt: list triggers",
			zap.String("flow_id", flowID), zap.Error(err))
		return nil
	}
	var earliest time.Time
	for _, t := range triggers {
		if t.Type != TriggerTypeCron {
			continue
		}
		next := s.cron.Next(t.ID)
		if next.IsZero() {
			continue
		}
		if earliest.IsZero() || next.Before(earliest) {
			earliest = next
		}
	}
	if earliest.IsZero() {
		return nil
	}
	return &earliest
}

// ValidateDefinitionTriggers 校验 definition 中所有触发器的格式是否合法。
//
// 目前仅校验 cron 触发器：expression 必填、合法；timezone 可选但若提供必须有效。
// webhook / manual 类型不在这里强校验（webhook 的 path 校验在 activateTriggers 阶段做）。
func ValidateDefinitionTriggers(def *Definition) error {
	if def == nil {
		return nil
	}
	for i, td := range def.Triggers {
		switch td.Type {
		case TriggerTypeCron:
			expr, _ := td.Config["expression"].(string)
			tz, _ := td.Config["timezone"].(string)
			if err := trigger.ValidateExpression(expr, tz); err != nil {
				return fmt.Errorf("trigger[%d] (id=%s): %w", i, td.ID, err)
			}
		case TriggerTypeWebhook, TriggerTypeManual, "":
			// 不在此处强校验
		default:
			return fmt.Errorf("trigger[%d] (id=%s): unsupported type %q", i, td.ID, td.Type)
		}
	}
	return nil
}

// activateTriggers 根据 flow.definition 注册 webhook / cron 触发器
func (s *Service) activateTriggers(f *Flow) error {
	def, err := f.DecodeDefinition()
	if err != nil {
		return fmt.Errorf("decode definition: %w", err)
	}
	// 先清掉旧触发器（更新 / 重新激活）
	_ = s.deactivateTriggers(f)

	for _, td := range def.Triggers {
		cfgJSON, _ := json.Marshal(td.Config)
		t := &Trigger{
			ID:     uuid.NewString(),
			FlowID: f.ID,
			Type:   td.Type,
			Config: string(cfgJSON),
			Status: "active",
		}
		switch td.Type {
		case TriggerTypeWebhook:
			path, _ := td.Config["path"].(string)
			if path == "" {
				return fmt.Errorf("webhook trigger %s: path required", td.ID)
			}
			t.WebhookPath = path
		case TriggerTypeCron:
			expr, _ := td.Config["expression"].(string)
			tz, _ := td.Config["timezone"].(string)
			if err := s.cron.Add(t.ID, expr, tz); err != nil {
				return fmt.Errorf("cron trigger %s: %w", td.ID, err)
			}
		case TriggerTypeManual:
			// 不做事
		default:
			return fmt.Errorf("unsupported trigger type: %s", td.Type)
		}
		if err := s.db.InsertTrigger(t); err != nil {
			// cron 已经注册，回滚
			if td.Type == TriggerTypeCron {
				s.cron.Remove(t.ID)
			}
			return err
		}
	}
	return nil
}

// deactivateTriggers 反注册并删除 DB 中的触发器
func (s *Service) deactivateTriggers(f *Flow) error {
	triggers, err := s.db.ListTriggersByFlow(f.ID)
	if err != nil {
		return err
	}
	for _, t := range triggers {
		if t.Type == TriggerTypeCron {
			s.cron.Remove(t.ID)
		}
	}
	return s.db.DeleteTriggersByFlow(f.ID)
}

// fireCron 由 cron scheduler 回调
func (s *Service) fireCron(triggerID string, scheduledAt time.Time) {
	triggers, err := s.db.ListTriggersByType(TriggerTypeCron)
	if err != nil {
		s.log.Warn("fireCron: list triggers", zap.Error(err))
		return
	}
	var found *Trigger
	for _, t := range triggers {
		if t.ID == triggerID {
			found = t
			break
		}
	}
	if found == nil {
		s.log.Warn("fireCron: trigger gone", zap.String("trigger_id", triggerID))
		s.cron.Remove(triggerID)
		return
	}
	f, err := s.db.GetFlow(found.FlowID)
	if err != nil || f == nil || f.Status != FlowStatusActive {
		return
	}
	_, err = s.engine.StartExecution(context.Background(), f, found.ID, TriggerData{
		Type: TriggerTypeCron,
		Payload: map[string]any{
			"scheduled_at": scheduledAt.Format(time.RFC3339),
		},
	})
	if err != nil {
		s.log.Warn("fireCron: start execution",
			zap.String("trigger_id", triggerID), zap.Error(err))
	}
}
