package nodes

import (
	"context"
	"testing"
)

func TestScriptNode_ReturnsObject(t *testing.T) {
	s := NewScriptNode()
	cfg := map[string]any{
		"runtime": "javascript",
		"code":    `return { sum: input.a + input.b };`,
		"input":   map[string]any{"a": 2, "b": 3},
	}
	res, err := s.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	v, ok := res.Output["sum"]
	if !ok {
		t.Fatalf("output missing sum: %#v", res.Output)
	}
	// goja exports number as int64 or float64; allow both
	switch x := v.(type) {
	case int64:
		if x != 5 {
			t.Fatalf("got %v", x)
		}
	case float64:
		if x != 5 {
			t.Fatalf("got %v", x)
		}
	default:
		t.Fatalf("unexpected type %T", v)
	}
}

func TestScriptNode_ReturnsScalar(t *testing.T) {
	s := NewScriptNode()
	cfg := map[string]any{
		"code": `return "hello";`,
	}
	res, err := s.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Output["result"] != "hello" {
		t.Fatalf("got %v", res.Output["result"])
	}
}

func TestScriptNode_ConsoleLog(t *testing.T) {
	s := NewScriptNode()
	cfg := map[string]any{
		"code": `console.log("hi"); return { ok: true };`,
	}
	res, err := s.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	logs, ok := res.Output["_logs"].([]string)
	if !ok || len(logs) != 1 || logs[0] != "hi" {
		t.Fatalf("logs: %#v", res.Output["_logs"])
	}
}

func TestScriptNode_RejectsUnknownRuntime(t *testing.T) {
	s := NewScriptNode()
	if _, err := s.Run(context.Background(), map[string]any{
		"runtime": "python", "code": "x",
	}); err == nil {
		t.Fatalf("expected err")
	}
}

func TestScriptNode_RequiresCode(t *testing.T) {
	s := NewScriptNode()
	if _, err := s.Run(context.Background(), map[string]any{}); err == nil {
		t.Fatalf("expected err")
	}
}
