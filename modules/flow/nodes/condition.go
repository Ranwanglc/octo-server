package nodes

import (
	"context"
	"errors"
	"fmt"
)

// ConditionNode 实现条件分支。
//
// config:
//   expression: string             # 已由引擎渲染完得到的实际值
//   branches:
//     - value: "trivial"           # 与 expression 比较
//       label: "..."               # 可选
//     - default: true              # 默认分支
//       label: "..."
//
// 输出：
//   matched_branch: <value 或 "__default__">
//
// 引擎应根据 NextBranches 与 EdgeDef.Branch 选择出边。
type ConditionNode struct{}

// NewConditionNode 构造
func NewConditionNode() *ConditionNode { return &ConditionNode{} }

// Type 返回 "condition"
func (c *ConditionNode) Type() string { return "condition" }

// Run 选择匹配的分支
func (c *ConditionNode) Run(ctx context.Context, cfg map[string]any) (*Result, error) {
	expr, _ := cfg["expression"]
	branchesRaw, ok := cfg["branches"].([]any)
	if !ok {
		// 也允许 nil branches → 走默认（所有出边）
		branchesRaw = nil
	}
	if len(branchesRaw) == 0 {
		return &Result{Output: map[string]any{"matched_branch": ""}}, nil
	}

	exprStr := fmt.Sprint(expr)
	defaultBranch := ""
	for _, b := range branchesRaw {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if isDefault(bm) {
			if v, ok := bm["value"].(string); ok && v != "" {
				defaultBranch = v
			} else {
				defaultBranch = "__default__"
			}
			continue
		}
		v, ok := bm["value"]
		if !ok {
			continue
		}
		if fmt.Sprint(v) == exprStr {
			return &Result{
				Output:       map[string]any{"matched_branch": fmt.Sprint(v)},
				NextBranches: []string{fmt.Sprint(v)},
			}, nil
		}
	}
	if defaultBranch != "" {
		return &Result{
			Output:       map[string]any{"matched_branch": defaultBranch},
			NextBranches: []string{defaultBranch},
		}, nil
	}
	return nil, errors.New("condition node: no branch matched and no default")
}

func isDefault(m map[string]any) bool {
	if v, ok := m["default"].(bool); ok && v {
		return true
	}
	return false
}
