package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-server/modules/flow/nodes"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Engine 是 Flow 执行引擎。
//
// 职责：
//   - 加载 flow definition、解析 DAG
//   - 按拓扑序执行节点，维护 ExecutionContext
//   - 写入 executions / node_executions 持久化
//   - 处理并发控制（cancel_previous / queue / reject_new）
//   - 节点执行失败时设置 execution 状态
//
// 不负责：触发器（由 trigger 模块负责），它们调用 StartExecution。
type Engine struct {
	db       *DB
	registry *nodes.Registry
	log      *zap.Logger

	mu     sync.Mutex
	cancel map[string]context.CancelFunc // executionID → cancel
}

// NewEngine 构造引擎。registry 为 nil 时使用默认（script/http/condition）。
func NewEngine(db *DB, registry *nodes.Registry, log *zap.Logger) *Engine {
	if registry == nil {
		registry = nodes.DefaultRegistry()
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Engine{
		db:       db,
		registry: registry,
		log:      log,
		cancel:   map[string]context.CancelFunc{},
	}
}

// Registry 暴露 registry，方便注册自定义节点
func (e *Engine) Registry() *nodes.Registry { return e.registry }

// StartExecution 启动一次 flow 执行（异步）。
//
// trigger 数据通过 triggerData 传入；若是 manual / API，可以填 Trigger.Input。
//
// 返回新建的 execution（含 ID）。执行在 goroutine 中进行，调用方可
// 通过 GetExecution 轮询状态，或后续接入事件流。
func (e *Engine) StartExecution(
	ctx context.Context,
	flow *Flow,
	triggerID string,
	triggerData TriggerData,
) (*Execution, error) {
	if flow == nil {
		return nil, errors.New("flow is nil")
	}
	def, err := flow.DecodeDefinition()
	if err != nil {
		return nil, fmt.Errorf("decode flow definition: %w", err)
	}
	// 提前 build 一次，触发 schema 校验
	if _, err := BuildDAG(def); err != nil {
		return nil, fmt.Errorf("invalid flow graph: %w", err)
	}

	execCtx := &ExecutionContext{
		ExecutionID: uuid.NewString(),
		FlowID:      flow.ID,
		Trigger:     triggerData,
		Nodes:       map[string]NodeContext{},
		Vars:        copyVars(def.Variables),
	}
	scopeKey := ""
	if def.Concurrency != nil && def.Concurrency.Scope != "" {
		scopeKey = Render(def.Concurrency.Scope, execCtx)
	}

	// 并发控制：检查同 scope 还在跑的执行
	if scopeKey != "" && def.Concurrency != nil {
		running, err := e.db.ListRunningByScope(flow.ID, scopeKey)
		if err != nil {
			return nil, fmt.Errorf("list running: %w", err)
		}
		switch def.Concurrency.Strategy {
		case ConcurrencyRejectNew:
			if len(running) > 0 {
				return nil, fmt.Errorf("flow: concurrent execution rejected (scope=%s)", scopeKey)
			}
		case ConcurrencyCancelPrevious:
			for _, r := range running {
				if err := e.CancelExecution(r.ID); err != nil {
					e.log.Warn("cancel previous failed", zap.String("execution_id", r.ID), zap.Error(err))
				}
			}
		case ConcurrencyQueue, "":
			// 默认 / queue：当前实现简化为并发跑，留 TODO
		}
	}

	now := time.Now()
	ctxJSON, _ := json.Marshal(execCtx)
	exec := &Execution{
		ID:        execCtx.ExecutionID,
		FlowID:    flow.ID,
		TriggerID: triggerID,
		Status:    ExecutionStatusPending,
		Context:   string(ctxJSON),
		ScopeKey:  scopeKey,
		StartedAt: &now,
	}
	if err := e.db.InsertExecution(exec); err != nil {
		return nil, fmt.Errorf("insert execution: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancel[exec.ID] = cancel
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.cancel, exec.ID)
			e.mu.Unlock()
			if r := recover(); r != nil {
				e.log.Error("flow execution panic",
					zap.String("execution_id", exec.ID), zap.Any("recover", r))
				_ = e.db.UpdateExecutionStatus(exec.ID, ExecutionStatusFailed,
					fmt.Sprintf("panic: %v", r), ptrTime(time.Now()))
			}
		}()
		if err := e.run(runCtx, exec, def, execCtx); err != nil {
			e.log.Warn("flow execution failed",
				zap.String("execution_id", exec.ID), zap.Error(err))
		}
	}()
	return exec, nil
}

// CancelExecution 取消一个正在跑的执行
func (e *Engine) CancelExecution(executionID string) error {
	e.mu.Lock()
	cancel, ok := e.cancel[executionID]
	e.mu.Unlock()
	if ok {
		cancel()
	}
	now := time.Now()
	return e.db.UpdateExecutionStatus(executionID, ExecutionStatusCancelled, "cancelled", &now)
}

// run 是核心循环。简化模型：按拓扑序逐节点执行；condition 节点根据
// matched branch 过滤后续出边；其他节点的出边只在 edge.condition 为
// 空或求值为 true 时被激活。
func (e *Engine) run(ctx context.Context, exec *Execution, def *Definition, ec *ExecutionContext) error {
	dag, err := BuildDAG(def)
	if err != nil {
		return e.finishFailed(exec, ec, fmt.Sprintf("build dag: %v", err))
	}
	// 标记为 running
	exec.Status = ExecutionStatusRunning
	exec.Context = mustJSON(ec)
	if err := e.db.UpdateExecution(exec); err != nil {
		return err
	}

	// activated[nodeID] = true 表示已被前驱激活
	activated := map[string]bool{}
	for _, id := range dag.EntryNodes() {
		activated[id] = true
	}

	for _, nodeID := range dag.TopoSort {
		if ctx.Err() != nil {
			// 被 cancel
			now := time.Now()
			_ = e.db.UpdateExecutionStatus(exec.ID, ExecutionStatusCancelled, "cancelled", &now)
			return ctx.Err()
		}
		if !activated[nodeID] {
			// 上游未激活，跳过
			continue
		}
		ndef := dag.Nodes[nodeID]
		result, err := e.runNode(ctx, exec, ndef, ec)
		if err != nil {
			return e.finishFailed(exec, ec, fmt.Sprintf("node %s: %v", nodeID, err))
		}
		// 激活下游：edges 中 from == nodeID 的，根据 branch / condition 决定
		for _, edge := range dag.Outgoing[nodeID] {
			if edge.To == "__end__" || edge.To == "" {
				continue
			}
			if !edgeMatches(edge, result, ec) {
				continue
			}
			activated[edge.To] = true
		}
	}

	return e.finishSuccess(exec, ec)
}

func (e *Engine) runNode(ctx context.Context, exec *Execution, ndef NodeDef, ec *ExecutionContext) (*nodes.Result, error) {
	runner, ok := e.registry.Get(ndef.Type)
	if !ok {
		return nil, fmt.Errorf("unknown node type: %s", ndef.Type)
	}

	// 渲染 config 中的 {{...}}
	rendered := map[string]any{}
	for k, v := range ndef.Config {
		rendered[k] = RenderAny(v, ec)
	}

	// 写入 node_execution（running）
	start := time.Now()
	inputJSON, _ := json.Marshal(rendered)
	nrow := &NodeExecution{
		ID:          uuid.NewString(),
		ExecutionID: exec.ID,
		NodeID:      ndef.ID,
		NodeType:    ndef.Type,
		Status:      NodeStatusRunning,
		Input:       string(inputJSON),
		StartedAt:   &start,
	}
	if err := e.db.InsertNodeExecution(nrow); err != nil {
		e.log.Warn("insert node execution", zap.Error(err))
	}
	ec.Nodes[ndef.ID] = NodeContext{
		Status:    NodeStatusRunning,
		Input:     rendered,
		StartedAt: &start,
	}

	result, runErr := runner.Run(ctx, rendered)
	end := time.Now()
	nrow.FinishedAt = &end
	nc := ec.Nodes[ndef.ID]
	nc.FinishedAt = &end
	if runErr != nil {
		nrow.Status = NodeStatusFailed
		nrow.Error = runErr.Error()
		nc.Status = NodeStatusFailed
		nc.Error = runErr.Error()
		ec.Nodes[ndef.ID] = nc
		_ = e.db.UpdateNodeExecution(nrow)
		return nil, runErr
	}
	nrow.Status = NodeStatusSuccess
	if result != nil && result.Output != nil {
		ob, _ := json.Marshal(result.Output)
		nrow.Output = string(ob)
		nc.Output = result.Output
	}
	nc.Status = NodeStatusSuccess
	ec.Nodes[ndef.ID] = nc
	_ = e.db.UpdateNodeExecution(nrow)

	// 实时回写 context 到 execution
	exec.Context = mustJSON(ec)
	_ = e.db.UpdateExecution(exec)

	return result, nil
}

// edgeMatches 决定一条出边是否应该激活下游。
//
// 规则：
//   - condition 节点：若 edge.branch 不为空，必须出现在 result.NextBranches；
//     若 edge.branch 为空，则不激活（强制走 branch 出口）。
//   - 其他节点：edge.branch 必须为空；edge.condition 为空 → 激活；
//     edge.condition 渲染后等于 "true" / "" / 非 "false" → 激活。
func edgeMatches(edge EdgeDef, result *nodes.Result, ec *ExecutionContext) bool {
	if result != nil && len(result.NextBranches) > 0 {
		// condition-style
		if edge.Branch == "" {
			return false
		}
		for _, b := range result.NextBranches {
			if b == edge.Branch {
				return true
			}
		}
		return false
	}
	if edge.Branch != "" {
		// 普通节点不应携带 branch
		return false
	}
	if edge.Condition == "" {
		return true
	}
	rendered := Render(edge.Condition, ec)
	return rendered != "" && rendered != "false" && rendered != "0"
}

func (e *Engine) finishSuccess(exec *Execution, ec *ExecutionContext) error {
	now := time.Now()
	exec.Status = ExecutionStatusSuccess
	exec.FinishedAt = &now
	exec.Context = mustJSON(ec)
	return e.db.UpdateExecution(exec)
}

func (e *Engine) finishFailed(exec *Execution, ec *ExecutionContext, msg string) error {
	now := time.Now()
	exec.Status = ExecutionStatusFailed
	exec.FinishedAt = &now
	exec.Error = msg
	exec.Context = mustJSON(ec)
	return e.db.UpdateExecution(exec)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func ptrTime(t time.Time) *time.Time { return &t }

func copyVars(v map[string]any) map[string]any {
	out := make(map[string]any, len(v))
	for k, vv := range v {
		out[k] = vv
	}
	return out
}
