package nodes

import (
	"context"
	"testing"
)

func TestConditionNode_MatchesValue(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"expression": "trivial",
		"branches": []any{
			map[string]any{"value": "trivial"},
			map[string]any{"value": "complex"},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Output["matched_branch"] != "trivial" {
		t.Fatalf("got %v", r.Output)
	}
	if len(r.NextBranches) != 1 || r.NextBranches[0] != "trivial" {
		t.Fatalf("next: %v", r.NextBranches)
	}
}

func TestConditionNode_DefaultBranch(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"expression": "weird",
		"branches": []any{
			map[string]any{"value": "trivial"},
			map[string]any{"default": true, "value": "fallback"},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "fallback" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

func TestConditionNode_NoMatchNoDefault(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"expression": "x",
		"branches":   []any{map[string]any{"value": "y"}},
	}
	if _, err := c.Run(context.Background(), cfg); err == nil {
		t.Fatalf("expected err")
	}
}

// --- 模式 2：conditions 数组（布尔表达式）---

func TestConditionNode_ConditionsMode_NumericEqual(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "approved", "expression": "200 == 200"},
			map[string]any{"branch": "rejected", "expression": "200 != 200"},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Output["matched_branch"] != "approved" {
		t.Fatalf("got %v", r.Output)
	}
	if len(r.NextBranches) != 1 || r.NextBranches[0] != "approved" {
		t.Fatalf("next: %v", r.NextBranches)
	}
}

func TestConditionNode_ConditionsMode_NumericNotEqual(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "approved", "expression": "200 == 404"},
			map[string]any{"branch": "rejected", "expression": "200 != 404"},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "rejected" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

func TestConditionNode_ConditionsMode_BooleanLiteral(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "no", "expression": "false"},
			map[string]any{"branch": "yes", "expression": "true"},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "yes" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

func TestConditionNode_ConditionsMode_StringEqualWithQuotes(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "approved", "expression": `"APPROVED" == "APPROVED"`},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "approved" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

func TestConditionNode_ConditionsMode_TruthyFallback(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "yes", "expression": "some-non-empty-string"},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "yes" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

func TestConditionNode_ConditionsMode_FalsyValues(t *testing.T) {
	c := NewConditionNode()
	for _, expr := range []string{"", "0", "false", "FALSE"} {
		cfg := map[string]any{
			"conditions": []any{
				map[string]any{"branch": "primary", "expression": expr},
				map[string]any{"branch": "fallback", "default": true},
			},
		}
		r, err := c.Run(context.Background(), cfg)
		if err != nil {
			t.Fatalf("expr=%q err: %v", expr, err)
		}
		if r.NextBranches[0] != "fallback" {
			t.Fatalf("expr=%q expected fallback, got %v", expr, r.NextBranches)
		}
	}
}

func TestConditionNode_ConditionsMode_ExplicitDefault(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "approved", "expression": "200 == 404"},
			map[string]any{"branch": "fallback", "default": true},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "fallback" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

func TestConditionNode_ConditionsMode_ImplicitDefault(t *testing.T) {
	// 没有 expression 字段 → 退化为隐式默认分支
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "approved", "expression": "1 == 2"},
			map[string]any{"branch": "rejected"}, // 隐式默认
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "rejected" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

func TestConditionNode_ConditionsMode_NoMatchNoDefault(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "approved", "expression": "false"},
		},
	}
	if _, err := c.Run(context.Background(), cfg); err == nil {
		t.Fatalf("expected err")
	}
}

func TestConditionNode_ConditionsMode_FirstMatchWins(t *testing.T) {
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "first", "expression": "true"},
			map[string]any{"branch": "second", "expression": "true"},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "first" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

// 向后兼容：legacy expression+branches 模式优先级低于 conditions，
// 但当只有 expression+branches 时仍然正常工作 —— TestConditionNode_MatchesValue
// 与 TestConditionNode_DefaultBranch 已覆盖。

func TestConditionNode_LegacyAndConditionsCoexist(t *testing.T) {
	// 同时存在 conditions 和 expression+branches 时：conditions 优先（更具表达力）
	c := NewConditionNode()
	cfg := map[string]any{
		"conditions": []any{
			map[string]any{"branch": "via_conditions", "expression": "true"},
		},
		"expression": "trivial",
		"branches": []any{
			map[string]any{"value": "trivial"},
		},
	}
	r, err := c.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.NextBranches[0] != "via_conditions" {
		t.Fatalf("got %v", r.NextBranches)
	}
}

func TestEvalBoolExpr_Cases(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"true", true},
		{"TRUE", true},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"1", true},
		{"200 == 200", true},
		{"200 == 201", false},
		{"200 != 200", false},
		{"200 != 201", true},
		{`"abc" == "abc"`, true},
		{`'abc' != 'def'`, true},
		{"hello", true}, // truthy fallback
	}
	for _, tc := range cases {
		got := evalBoolExpr(tc.expr)
		if got != tc.want {
			t.Errorf("evalBoolExpr(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}
