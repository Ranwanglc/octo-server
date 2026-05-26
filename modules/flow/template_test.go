package flow

import "testing"

func ctxFixture() *ExecutionContext {
	return &ExecutionContext{
		Trigger: TriggerData{
			Type:    "webhook",
			Payload: map[string]any{"number": float64(42), "user": map[string]any{"login": "alice"}},
			Input:   map[string]any{"x": "hello"},
		},
		Nodes: map[string]NodeContext{
			"n1": {Status: "success", Output: map[string]any{"text": "ok", "n": float64(7)}},
		},
		Vars: map[string]any{"env": "prod"},
	}
}

func TestRender_TriggerPayload(t *testing.T) {
	ec := ctxFixture()
	got := Render("PR #{{trigger.payload.number}} by {{trigger.payload.user.login}}", ec)
	if got != "PR #42 by alice" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_NodeOutput_Shorthand(t *testing.T) {
	ec := ctxFixture()
	got := Render("text={{n1.output.text}}", ec)
	if got != "text=ok" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_Vars(t *testing.T) {
	ec := ctxFixture()
	got := Render("env is {{vars.env}}", ec)
	if got != "env is prod" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderAny_PreservesTypes(t *testing.T) {
	ec := ctxFixture()
	v := RenderAny("{{trigger.payload.number}}", ec)
	if f, ok := v.(float64); !ok || f != 42 {
		t.Fatalf("expected float64 42, got %T %v", v, v)
	}
}

func TestRenderAny_RecursesMaps(t *testing.T) {
	ec := ctxFixture()
	in := map[string]any{
		"a": "{{vars.env}}",
		"b": map[string]any{"c": "{{n1.output.text}}"},
	}
	out := RenderAny(in, ec).(map[string]any)
	if out["a"] != "prod" {
		t.Fatalf("a: %v", out["a"])
	}
	if out["b"].(map[string]any)["c"] != "ok" {
		t.Fatalf("b.c: %v", out["b"])
	}
}

func TestRender_Missing(t *testing.T) {
	ec := ctxFixture()
	got := Render("hello {{trigger.payload.missing}}!", ec)
	if got != "hello !" {
		t.Fatalf("got %q", got)
	}
}
