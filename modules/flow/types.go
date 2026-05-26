package flow

import (
	"encoding/json"
	"time"
)

// Status 常量
const (
	// Flow status
	FlowStatusDraft    = "draft"
	FlowStatusActive   = "active"
	FlowStatusArchived = "archived"

	// Execution status
	ExecutionStatusPending   = "pending"
	ExecutionStatusRunning   = "running"
	ExecutionStatusWaiting   = "waiting"
	ExecutionStatusSuccess   = "success"
	ExecutionStatusFailed    = "failed"
	ExecutionStatusCancelled = "cancelled"

	// Node execution status
	NodeStatusPending   = "pending"
	NodeStatusRunning   = "running"
	NodeStatusSuccess   = "success"
	NodeStatusFailed    = "failed"
	NodeStatusSkipped   = "skipped"
	NodeStatusCancelled = "cancelled"

	// Trigger types
	TriggerTypeWebhook = "webhook"
	TriggerTypeCron    = "cron"
	TriggerTypeManual  = "manual"

	// Node types
	NodeTypeScript    = "script"
	NodeTypeHTTP      = "http"
	NodeTypeCondition = "condition"

	// Concurrency strategy
	ConcurrencyCancelPrevious = "cancel_previous"
	ConcurrencyQueue          = "queue"
	ConcurrencyRejectNew      = "reject_new"
)

// Definition 是一个 flow 的完整定义
type Definition struct {
	Triggers    []TriggerDef        `json:"triggers,omitempty"`
	Variables   map[string]any      `json:"variables,omitempty"`
	Nodes       []NodeDef           `json:"nodes"`
	Edges       []EdgeDef           `json:"edges"`
	Concurrency *ConcurrencyConfig  `json:"concurrency,omitempty"`
}

// TriggerDef 触发器定义
type TriggerDef struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

// NodeDef 节点定义
type NodeDef struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

// EdgeDef 边定义
type EdgeDef struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
	Branch    string `json:"branch,omitempty"` // 用于 condition 节点的分支命名
	Label     string `json:"label,omitempty"`
}

// ConcurrencyConfig 并发控制配置
type ConcurrencyConfig struct {
	Scope    string `json:"scope"`              // 模板表达式：例如 {{trigger.payload.pull_request.number}}
	Strategy string `json:"strategy,omitempty"` // cancel_previous | queue | reject_new
}

// Flow 数据库实体
type Flow struct {
	ID          string    `json:"id" db:"id"`
	SpaceID     string    `json:"space_id" db:"space_id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	Definition  string    `json:"-" db:"definition"`
	Version     int       `json:"version" db:"version"`
	Status      string    `json:"status" db:"status"`
	CreatedBy   string    `json:"created_by" db:"created_by"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// DecodeDefinition 解析 Flow.Definition JSON 为结构体
func (f *Flow) DecodeDefinition() (*Definition, error) {
	def := &Definition{}
	if f.Definition == "" {
		return def, nil
	}
	if err := json.Unmarshal([]byte(f.Definition), def); err != nil {
		return nil, err
	}
	return def, nil
}

// Trigger 数据库实体
type Trigger struct {
	ID          string    `json:"id" db:"id"`
	FlowID      string    `json:"flow_id" db:"flow_id"`
	Type        string    `json:"type" db:"type"`
	Config      string    `json:"-" db:"config"`
	WebhookPath string    `json:"webhook_path" db:"webhook_path"`
	Status      string    `json:"status" db:"status"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// DecodeConfig 解析 Trigger.Config JSON
func (t *Trigger) DecodeConfig() (map[string]any, error) {
	cfg := map[string]any{}
	if t.Config == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(t.Config), &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Execution 数据库实体
type Execution struct {
	ID         string     `json:"id" db:"id"`
	FlowID     string     `json:"flow_id" db:"flow_id"`
	TriggerID  string     `json:"trigger_id" db:"trigger_id"`
	Status     string     `json:"status" db:"status"`
	Context    string     `json:"-" db:"context"`
	ScopeKey   string     `json:"scope_key" db:"scope_key"`
	StartedAt  *time.Time `json:"started_at,omitempty" db:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty" db:"finished_at"`
	Error      string     `json:"error" db:"error"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
}

// DecodeContext 解析 Execution.Context JSON 为结构体
func (e *Execution) DecodeContext() (*ExecutionContext, error) {
	c := &ExecutionContext{
		Trigger: TriggerData{},
		Nodes:   map[string]NodeContext{},
		Vars:    map[string]any{},
	}
	if e.Context == "" {
		return c, nil
	}
	if err := json.Unmarshal([]byte(e.Context), c); err != nil {
		return nil, err
	}
	if c.Nodes == nil {
		c.Nodes = map[string]NodeContext{}
	}
	if c.Vars == nil {
		c.Vars = map[string]any{}
	}
	return c, nil
}

// NodeExecution 数据库实体
type NodeExecution struct {
	ID           string     `json:"id" db:"id"`
	ExecutionID  string     `json:"execution_id" db:"execution_id"`
	NodeID       string     `json:"node_id" db:"node_id"`
	NodeType     string     `json:"node_type" db:"node_type"`
	Status       string     `json:"status" db:"status"`
	Input        string     `json:"-" db:"input"`
	Output       string     `json:"-" db:"output"`
	Error        string     `json:"error" db:"error"`
	StartedAt    *time.Time `json:"started_at,omitempty" db:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty" db:"finished_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}

// ExecutionContext 是执行期间的上下文，会被 marshal 为 Execution.Context
type ExecutionContext struct {
	ExecutionID string                 `json:"execution_id"`
	FlowID      string                 `json:"flow_id"`
	Trigger     TriggerData            `json:"trigger"`
	Nodes       map[string]NodeContext `json:"nodes"`
	Vars        map[string]any         `json:"vars"`
}

// TriggerData 触发器输入数据
type TriggerData struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
}

// NodeContext 节点执行结果
type NodeContext struct {
	Status     string         `json:"status"`
	Input      map[string]any `json:"input,omitempty"`
	Output     map[string]any `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}
