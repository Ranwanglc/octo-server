package flow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Mininglamp-OSS/octo-server/modules/flow/nodes"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

func newMockDB(t *testing.T) (*DB, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	conn := &dbr.Connection{DB: rawDB, EventReceiver: &dbr.NullEventReceiver{}, Dialect: dialect.MySQL}
	sess := conn.NewSession(nil)
	return NewDB(sess), mock
}

func TestEngine_RunSimpleScriptFlow(t *testing.T) {
	db, mock := newMockDB(t)
	// 简单：所有 query/exec 都放行；不验证具体语句（QueryMatcherRegexp + ExpectAny 风格）
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 1))
	for i := 0; i < 20; i++ {
		mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 1))
	}

	eng := NewEngine(db, nodes.DefaultRegistry(), nil)

	def := &Definition{
		Nodes: []NodeDef{
			{ID: "a", Type: "script", Config: map[string]any{
				"code":  `return { greeting: "hello " + input.who };`,
				"input": map[string]any{"who": "{{trigger.input.who}}"},
			}},
			{ID: "b", Type: "script", Config: map[string]any{
				"code":  `return { upper: input.s.toUpperCase() };`,
				"input": map[string]any{"s": "{{a.output.greeting}}"},
			}},
		},
		Edges: []EdgeDef{{From: "a", To: "b"}},
	}
	defJSON, _ := json.Marshal(def)
	flow := &Flow{ID: "f1", Definition: string(defJSON), Status: FlowStatusActive}

	exec, err := eng.StartExecution(context.Background(), flow, "", TriggerData{
		Type:  TriggerTypeManual,
		Input: map[string]any{"who": "world"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 等待异步完成
	deadline := time.Now().Add(3 * time.Second)
	var final *ExecutionContext
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		// 读 in-memory exec.Context（engine 会更新）
		ec, _ := exec.DecodeContext()
		if ec != nil && len(ec.Nodes) == 2 && ec.Nodes["b"].Status == NodeStatusSuccess {
			final = ec
			break
		}
	}
	if final == nil {
		t.Fatalf("script flow did not finish: %+v", exec)
	}
	got := final.Nodes["b"].Output["upper"]
	if got != "HELLO WORLD" {
		t.Fatalf("got upper=%v", got)
	}
}

func TestEngine_ConditionBranches(t *testing.T) {
	db, mock := newMockDB(t)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 30; i++ {
		mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 1))
	}

	eng := NewEngine(db, nodes.DefaultRegistry(), nil)
	def := &Definition{
		Nodes: []NodeDef{
			{ID: "classify", Type: "condition", Config: map[string]any{
				"expression": "{{trigger.input.kind}}",
				"branches": []any{
					map[string]any{"value": "small"},
					map[string]any{"value": "big"},
				},
			}},
			{ID: "small_path", Type: "script", Config: map[string]any{"code": `return { hit: "small" };`}},
			{ID: "big_path", Type: "script", Config: map[string]any{"code": `return { hit: "big" };`}},
		},
		Edges: []EdgeDef{
			{From: "classify", To: "small_path", Branch: "small"},
			{From: "classify", To: "big_path", Branch: "big"},
		},
	}
	defJSON, _ := json.Marshal(def)
	flow := &Flow{ID: "fcond", Definition: string(defJSON), Status: FlowStatusActive}

	exec, err := eng.StartExecution(context.Background(), flow, "", TriggerData{
		Type:  TriggerTypeManual,
		Input: map[string]any{"kind": "big"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		ec, _ := exec.DecodeContext()
		if ec == nil {
			continue
		}
		if ec.Nodes["big_path"].Status == NodeStatusSuccess {
			// small_path 应未执行
			if _, ran := ec.Nodes["small_path"]; ran {
				t.Fatalf("small_path should not run, got: %+v", ec.Nodes["small_path"])
			}
			return
		}
	}
	ec, _ := exec.DecodeContext()
	t.Fatalf("did not finish: %+v", ec)
}

func TestEngine_HTTPNodeIntegrates(t *testing.T) {
	db, mock := newMockDB(t)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 30; i++ {
		mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 1))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"echo":"x"}`))
	}))
	defer srv.Close()

	eng := NewEngine(db, nodes.DefaultRegistry(), nil)
	def := &Definition{
		Nodes: []NodeDef{
			{ID: "fetch", Type: "http", Config: map[string]any{
				"method": "GET",
				"url":    srv.URL,
			}},
		},
	}
	defJSON, _ := json.Marshal(def)
	flow := &Flow{ID: "fhttp", Definition: string(defJSON), Status: FlowStatusActive}
	exec, err := eng.StartExecution(context.Background(), flow, "", TriggerData{Type: TriggerTypeManual})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		ec, _ := exec.DecodeContext()
		if ec != nil && ec.Nodes["fetch"].Status == NodeStatusSuccess {
			jv, _ := ec.Nodes["fetch"].Output["json"].(map[string]any)
			if jv["ok"] != true {
				t.Fatalf("json=%#v", jv)
			}
			return
		}
	}
	t.Fatalf("http flow did not finish")
}
