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
