package flow

import (
	"database/sql"
	"errors"
	"time"

	"github.com/gocraft/dbr/v2"
)

// DB 封装 flow 模块的数据访问
type DB struct {
	session *dbr.Session
}

// NewDB 创建 DB
func NewDB(session *dbr.Session) *DB {
	return &DB{session: session}
}

var (
	// ErrNotFound 资源不存在
	ErrNotFound = errors.New("flow: not found")
)

// ---------------- Flow ----------------

// InsertFlow 写入一个 flow
func (d *DB) InsertFlow(f *Flow) error {
	now := time.Now()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	_, err := d.session.InsertInto("flows").
		Columns("id", "space_id", "name", "description", "definition", "version",
			"status", "created_by", "created_at", "updated_at").
		Values(f.ID, f.SpaceID, f.Name, f.Description, f.Definition, f.Version,
			f.Status, f.CreatedBy, f.CreatedAt, f.UpdatedAt).
		Exec()
	return err
}

// UpdateFlow 更新 flow（definition / status / version / name / description）
func (d *DB) UpdateFlow(f *Flow) error {
	f.UpdatedAt = time.Now()
	_, err := d.session.Update("flows").
		Set("name", f.Name).
		Set("description", f.Description).
		Set("definition", f.Definition).
		Set("version", f.Version).
		Set("status", f.Status).
		Set("updated_at", f.UpdatedAt).
		Where("id = ?", f.ID).
		Exec()
	return err
}

// UpdateFlowStatus 仅更新 flow 状态
func (d *DB) UpdateFlowStatus(id, status string) error {
	_, err := d.session.Update("flows").
		Set("status", status).
		Set("updated_at", time.Now()).
		Where("id = ?", id).
		Exec()
	return err
}

// GetFlow 按 id 查询
func (d *DB) GetFlow(id string) (*Flow, error) {
	f := &Flow{}
	err := d.session.Select("*").From("flows").Where("id = ?", id).LoadOne(f)
	if err == dbr.ErrNotFound {
		return nil, nil
	}
	return f, err
}

// DeleteFlow 删除一个 flow（同时删除 triggers / executions / node_executions / versions）
func (d *DB) DeleteFlow(id string) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()
	if _, err := tx.DeleteFrom("flow_triggers").Where("flow_id = ?", id).Exec(); err != nil {
		return err
	}
	if _, err := tx.DeleteFrom("flow_versions").Where("flow_id = ?", id).Exec(); err != nil {
		return err
	}
	// Node executions 先删；execution 后删（FK 不强制，但语义上先 child）
	if _, err := tx.DeleteFrom("flow_node_executions").
		Where("execution_id IN ?",
			dbr.Select("id").From("flow_executions").Where("flow_id = ?", id)).
		Exec(); err != nil {
		return err
	}
	if _, err := tx.DeleteFrom("flow_executions").Where("flow_id = ?", id).Exec(); err != nil {
		return err
	}
	if _, err := tx.DeleteFrom("flows").Where("id = ?", id).Exec(); err != nil {
		return err
	}
	return tx.Commit()
}

// ListFlows 按 space 列出（可选状态过滤）
func (d *DB) ListFlows(spaceID, status string, limit, offset int) ([]*Flow, error) {
	q := d.session.Select("*").From("flows")
	if spaceID != "" {
		q = q.Where("space_id = ?", spaceID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if limit <= 0 {
		limit = 50
	}
	q = q.OrderDir("updated_at", false).Limit(uint64(limit)).Offset(uint64(offset))
	var out []*Flow
	_, err := q.Load(&out)
	return out, err
}

// ---------------- Flow Versions ----------------

// InsertFlowVersion 写入一个 flow 版本
func (d *DB) InsertFlowVersion(v *FlowVersionRow) error {
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	_, err := d.session.InsertInto("flow_versions").
		Columns("id", "flow_id", "version", "definition", "changelog", "created_by", "created_at").
		Values(v.ID, v.FlowID, v.Version, v.Definition, v.Changelog, v.CreatedBy, v.CreatedAt).
		Exec()
	return err
}

// ListFlowVersions 列出某个 flow 的版本
func (d *DB) ListFlowVersions(flowID string, limit int) ([]*FlowVersionRow, error) {
	if limit <= 0 {
		limit = 50
	}
	var out []*FlowVersionRow
	_, err := d.session.Select("*").From("flow_versions").
		Where("flow_id = ?", flowID).
		OrderDir("version", false).
		Limit(uint64(limit)).
		Load(&out)
	return out, err
}

// FlowVersionRow 是 flow_versions 表的实体
type FlowVersionRow struct {
	ID         string    `json:"id" db:"id"`
	FlowID     string    `json:"flow_id" db:"flow_id"`
	Version    int       `json:"version" db:"version"`
	Definition string    `json:"definition" db:"definition"`
	Changelog  string    `json:"changelog" db:"changelog"`
	CreatedBy  string    `json:"created_by" db:"created_by"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

// ---------------- Triggers ----------------

// InsertTrigger 写入触发器
func (d *DB) InsertTrigger(t *Trigger) error {
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	_, err := d.session.InsertInto("flow_triggers").
		Columns("id", "flow_id", "type", "config", "webhook_path", "status", "created_at", "updated_at").
		Values(t.ID, t.FlowID, t.Type, t.Config, t.WebhookPath, t.Status, t.CreatedAt, t.UpdatedAt).
		Exec()
	return err
}

// DeleteTriggersByFlow 删除 flow 的所有触发器
func (d *DB) DeleteTriggersByFlow(flowID string) error {
	_, err := d.session.DeleteFrom("flow_triggers").Where("flow_id = ?", flowID).Exec()
	return err
}

// ListTriggersByFlow 列出 flow 的触发器
func (d *DB) ListTriggersByFlow(flowID string) ([]*Trigger, error) {
	var out []*Trigger
	_, err := d.session.Select("*").From("flow_triggers").
		Where("flow_id = ?", flowID).Load(&out)
	return out, err
}

// ListTriggersByType 按类型列出活跃触发器
func (d *DB) ListTriggersByType(t string) ([]*Trigger, error) {
	var out []*Trigger
	_, err := d.session.Select("*").From("flow_triggers").
		Where("type = ? AND status = ?", t, "active").
		Load(&out)
	return out, err
}

// GetTriggerByWebhookPath 按 webhook path 查询
func (d *DB) GetTriggerByWebhookPath(path string) (*Trigger, error) {
	t := &Trigger{}
	err := d.session.Select("*").From("flow_triggers").
		Where("webhook_path = ? AND status = ?", path, "active").LoadOne(t)
	if err == dbr.ErrNotFound {
		return nil, nil
	}
	return t, err
}

// ---------------- Executions ----------------

// InsertExecution 写入一个执行实例
func (d *DB) InsertExecution(e *Execution) error {
	now := time.Now()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.UpdatedAt = now
	_, err := d.session.InsertInto("flow_executions").
		Columns("id", "flow_id", "trigger_id", "status", "context", "scope_key",
			"started_at", "finished_at", "error", "created_at", "updated_at").
		Values(e.ID, e.FlowID, e.TriggerID, e.Status, e.Context, e.ScopeKey,
			nullTime(e.StartedAt), nullTime(e.FinishedAt), e.Error, e.CreatedAt, e.UpdatedAt).
		Exec()
	return err
}

// UpdateExecution 更新执行实例（status / context / started_at / finished_at / error）
func (d *DB) UpdateExecution(e *Execution) error {
	e.UpdatedAt = time.Now()
	_, err := d.session.Update("flow_executions").
		Set("status", e.Status).
		Set("context", e.Context).
		Set("started_at", nullTime(e.StartedAt)).
		Set("finished_at", nullTime(e.FinishedAt)).
		Set("error", e.Error).
		Set("updated_at", e.UpdatedAt).
		Where("id = ?", e.ID).
		Exec()
	return err
}

// UpdateExecutionStatus 仅更新 status / error / finished_at
func (d *DB) UpdateExecutionStatus(id, status, errMsg string, finishedAt *time.Time) error {
	_, err := d.session.Update("flow_executions").
		Set("status", status).
		Set("error", errMsg).
		Set("finished_at", nullTime(finishedAt)).
		Set("updated_at", time.Now()).
		Where("id = ?", id).
		Exec()
	return err
}

// GetExecution 按 id 查询
func (d *DB) GetExecution(id string) (*Execution, error) {
	e := &Execution{}
	err := d.session.Select("*").From("flow_executions").Where("id = ?", id).LoadOne(e)
	if err == dbr.ErrNotFound {
		return nil, nil
	}
	return e, err
}

// ListExecutionsByFlow 按 flow 列出执行
func (d *DB) ListExecutionsByFlow(flowID string, limit, offset int) ([]*Execution, error) {
	if limit <= 0 {
		limit = 50
	}
	var out []*Execution
	_, err := d.session.Select("*").From("flow_executions").
		Where("flow_id = ?", flowID).
		OrderDir("created_at", false).
		Limit(uint64(limit)).
		Offset(uint64(offset)).
		Load(&out)
	return out, err
}

// ListRunningByScope 列出同 scope 仍在跑的执行（用于并发控制）
func (d *DB) ListRunningByScope(flowID, scopeKey string) ([]*Execution, error) {
	var out []*Execution
	_, err := d.session.Select("*").From("flow_executions").
		Where("flow_id = ? AND scope_key = ? AND status IN ?",
			flowID, scopeKey, []string{ExecutionStatusPending, ExecutionStatusRunning, ExecutionStatusWaiting}).
		Load(&out)
	return out, err
}

// ---------------- Node Executions ----------------

// InsertNodeExecution 写入节点执行
func (d *DB) InsertNodeExecution(n *NodeExecution) error {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	_, err := d.session.InsertInto("flow_node_executions").
		Columns("id", "execution_id", "node_id", "node_type", "status",
			"input", "output", "error", "started_at", "finished_at", "created_at").
		Values(n.ID, n.ExecutionID, n.NodeID, n.NodeType, n.Status,
			n.Input, n.Output, n.Error, nullTime(n.StartedAt), nullTime(n.FinishedAt), n.CreatedAt).
		Exec()
	return err
}

// UpdateNodeExecution 更新节点执行（status / output / error / finished_at）
func (d *DB) UpdateNodeExecution(n *NodeExecution) error {
	_, err := d.session.Update("flow_node_executions").
		Set("status", n.Status).
		Set("output", n.Output).
		Set("error", n.Error).
		Set("finished_at", nullTime(n.FinishedAt)).
		Where("id = ?", n.ID).
		Exec()
	return err
}

// ListNodeExecutions 按 execution_id 列出
func (d *DB) ListNodeExecutions(executionID string) ([]*NodeExecution, error) {
	var out []*NodeExecution
	_, err := d.session.Select("*").From("flow_node_executions").
		Where("execution_id = ?", executionID).
		OrderAsc("created_at").
		Load(&out)
	return out, err
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{Valid: false}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
