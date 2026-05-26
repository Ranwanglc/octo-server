package flow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
)

// Flow 是 flow 模块的 HTTP API 入口，实现 register.APIRouter
type FlowAPI struct {
	log.Log
	ctx     *config.Context
	service *Service
}

// New 构造 Flow API
func New(ctx *config.Context) *FlowAPI {
	logger := log.NewTLog("flow")
	db := NewDB(ctx.DB())
	engine := NewEngine(db, nil, nil)
	svc, err := NewService(db, engine, nil)
	if err != nil {
		// 不直接 panic，让 Route 时返回错误更好；这里 New 失败说明配置异常
		// 保留警告，service 仍构造避免空指针
		logger.Error("init flow service failed: " + err.Error())
	}
	return &FlowAPI{
		Log:     logger,
		ctx:     ctx,
		service: svc,
	}
}

// Service 暴露 Service，便于测试 / 其他模块复用
func (f *FlowAPI) Service() *Service { return f.service }

// Start 启动调度
func (f *FlowAPI) Start() error {
	if f.service == nil {
		return errors.New("flow service not initialized")
	}
	return f.service.Start()
}

// Stop 停止调度
func (f *FlowAPI) Stop() error {
	if f.service == nil {
		return nil
	}
	return f.service.Stop()
}

// Route 注册路由
func (f *FlowAPI) Route(r *wkhttp.WKHttp) {
	// Webhook 入口不要求登录态（外部 push）
	r.POST("/v1/flow/webhook/:path", f.handleWebhook)

	auth := r.Group("/v1", f.ctx.AuthMiddleware(r))
	{
		auth.POST("/flows", f.createFlow)
		auth.GET("/flows", f.listFlows)
		auth.GET("/flows/:id", f.getFlow)
		auth.PUT("/flows/:id", f.updateFlow)
		auth.DELETE("/flows/:id", f.deleteFlow)
		auth.POST("/flows/:id/activate", f.activateFlow)
		auth.POST("/flows/:id/deactivate", f.deactivateFlow)
		auth.POST("/flows/:id/execute", f.executeFlow)
		auth.GET("/flows/:id/executions", f.listExecutions)
		auth.GET("/executions/:id", f.getExecution)
		auth.POST("/executions/:id/cancel", f.cancelExecution)
	}
}

// ---------- Handlers ----------

type createFlowReq struct {
	SpaceID     string      `json:"space_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Definition  *Definition `json:"definition"`
}

func (f *FlowAPI) createFlow(c *wkhttp.Context) {
	var req createFlowReq
	if err := bindJSON(c, &req); err != nil {
		c.ResponseError(err)
		return
	}
	if req.Name == "" {
		c.ResponseError(errors.New("name is required"))
		return
	}
	createdBy := c.GetLoginUID()
	flow, err := f.service.CreateFlow(req.SpaceID, req.Name, req.Description, req.Definition, createdBy)
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.Response(flowToResp(flow))
}

func (f *FlowAPI) listFlows(c *wkhttp.Context) {
	spaceID := c.Query("space_id")
	status := c.Query("status")
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	list, err := f.service.ListFlows(spaceID, status, limit, offset)
	if err != nil {
		c.ResponseError(err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, fl := range list {
		out = append(out, flowToResp(fl))
	}
	c.Response(map[string]any{"items": out, "count": len(out)})
}

func (f *FlowAPI) getFlow(c *wkhttp.Context) {
	id := c.Param("id")
	flow, err := f.service.GetFlow(id)
	if err != nil {
		c.ResponseError(err)
		return
	}
	if flow == nil {
		c.ResponseError(ErrNotFound)
		return
	}
	c.Response(flowToResp(flow))
}

type updateFlowReq struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Definition  *Definition `json:"definition"`
	Changelog   string      `json:"changelog"`
}

func (f *FlowAPI) updateFlow(c *wkhttp.Context) {
	id := c.Param("id")
	var req updateFlowReq
	if err := bindJSON(c, &req); err != nil {
		c.ResponseError(err)
		return
	}
	flow, err := f.service.UpdateFlow(id, req.Name, req.Description, req.Definition, req.Changelog, c.GetLoginUID())
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.Response(flowToResp(flow))
}

func (f *FlowAPI) deleteFlow(c *wkhttp.Context) {
	id := c.Param("id")
	if err := f.service.DeleteFlow(id); err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

func (f *FlowAPI) activateFlow(c *wkhttp.Context) {
	id := c.Param("id")
	if err := f.service.Activate(id); err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

func (f *FlowAPI) deactivateFlow(c *wkhttp.Context) {
	id := c.Param("id")
	if err := f.service.Deactivate(id); err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

type executeReq struct {
	Input map[string]any `json:"input"`
}

func (f *FlowAPI) executeFlow(c *wkhttp.Context) {
	id := c.Param("id")
	var req executeReq
	_ = bindJSON(c, &req) // input 可选
	exec, err := f.service.Execute(c.Request.Context(), id, req.Input)
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.Response(executionToResp(exec, nil))
}

func (f *FlowAPI) listExecutions(c *wkhttp.Context) {
	id := c.Param("id")
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	list, err := f.service.ListExecutions(id, limit, offset)
	if err != nil {
		c.ResponseError(err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, executionToResp(e, nil))
	}
	c.Response(map[string]any{"items": out, "count": len(out)})
}

func (f *FlowAPI) getExecution(c *wkhttp.Context) {
	id := c.Param("id")
	exec, nodes, err := f.service.GetExecution(id)
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.Response(executionToResp(exec, nodes))
}

func (f *FlowAPI) cancelExecution(c *wkhttp.Context) {
	id := c.Param("id")
	if err := f.service.CancelExecution(id); err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

func (f *FlowAPI) handleWebhook(c *wkhttp.Context) {
	path := c.Param("path")
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.ResponseError(fmt.Errorf("read body: %w", err))
		return
	}
	headers := map[string]string{}
	for k, v := range c.Request.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	exec, err := f.service.HandleWebhook(c.Request.Context(), path, body, headers)
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.Response(map[string]any{"execution_id": exec.ID, "status": exec.Status})
}

// ---------- helpers ----------

func bindJSON(c *wkhttp.Context, dst any) error {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, dst)
}

func flowToResp(f *Flow) map[string]any {
	if f == nil {
		return nil
	}
	var def any
	if f.Definition != "" {
		_ = json.Unmarshal([]byte(f.Definition), &def)
	}
	return map[string]any{
		"id":          f.ID,
		"space_id":    f.SpaceID,
		"name":        f.Name,
		"description": f.Description,
		"definition":  def,
		"version":     f.Version,
		"status":      f.Status,
		"created_by":  f.CreatedBy,
		"created_at":  f.CreatedAt,
		"updated_at":  f.UpdatedAt,
	}
}

func executionToResp(e *Execution, nodes []*NodeExecution) map[string]any {
	if e == nil {
		return nil
	}
	var ctx any
	if e.Context != "" {
		_ = json.Unmarshal([]byte(e.Context), &ctx)
	}
	out := map[string]any{
		"id":          e.ID,
		"flow_id":     e.FlowID,
		"trigger_id":  e.TriggerID,
		"status":      e.Status,
		"context":     ctx,
		"scope_key":   e.ScopeKey,
		"started_at":  e.StartedAt,
		"finished_at": e.FinishedAt,
		"error":       e.Error,
		"created_at":  e.CreatedAt,
	}
	if nodes != nil {
		nodeList := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			var in any
			var outp any
			if n.Input != "" {
				_ = json.Unmarshal([]byte(n.Input), &in)
			}
			if n.Output != "" {
				_ = json.Unmarshal([]byte(n.Output), &outp)
			}
			nodeList = append(nodeList, map[string]any{
				"id":           n.ID,
				"node_id":      n.NodeID,
				"node_type":    n.NodeType,
				"status":       n.Status,
				"input":        in,
				"output":       outp,
				"error":        n.Error,
				"started_at":   n.StartedAt,
				"finished_at":  n.FinishedAt,
			})
		}
		out["nodes"] = nodeList
	}
	return out
}
