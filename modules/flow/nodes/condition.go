package nodes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ConditionNode 实现条件分支。
//
// 支持两种配置格式：
//
// 1) 值匹配模式（legacy / 简单 switch）：
//
//	expression: string             # 已由引擎渲染完得到的实际值
//	branches:
//	  - value: "trivial"           # 与 expression 比较
//	    label: "..."               # 可选
//	  - default: true              # 默认分支
//	    label: "..."
//
// 2) 布尔表达式模式（用于 PR Review Flow 等需要判断 "200 == 200" 这类条件的场景）：
//
//	conditions:
//	  - branch: "approved"
//	    expression: "200 == 200"   # 已由引擎渲染过；evalBoolExpr 求值
//	  - branch: "rejected"
//	    default: true              # 显式默认分支
//	# 没有 expression 且没有 default 的 condition 退化为隐式默认分支。
//
// 输出：
//
//	matched_branch: <value 或 branch 名 或 "__default__">
//
// 引擎应根据 NextBranches 与 EdgeDef.Branch 选择出边。
type ConditionNode struct{}

// NewConditionNode 构造
func NewConditionNode() *ConditionNode { return &ConditionNode{} }

// Type 返回 "condition"
func (c *ConditionNode) Type() string { return "condition" }

// Run 选择匹配的分支
func (c *ConditionNode) Run(ctx context.Context, cfg map[string]any) (*Result, error) {
	// --- 模式 2：conditions 数组（布尔表达式） ---
	if condsRaw, ok := cfg["conditions"].([]any); ok && len(condsRaw) > 0 {
		return runConditionsMode(condsRaw)
	}

	// --- 模式 1：expression + branches（值匹配） ---
	expr := cfg["expression"]
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

// runConditionsMode 处理布尔表达式模式。
// 顺序遍历 conditions，第一个 expression 求值为 truthy 的胜出。
// 显式 default:true 或 没有 expression 的 condition 记为默认分支。
func runConditionsMode(condsRaw []any) (*Result, error) {
	defaultBranch := ""
	for _, raw := range condsRaw {
		cm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		branch, _ := cm["branch"].(string)
		if branch == "" {
			continue
		}
		// 显式默认分支
		if d, ok := cm["default"].(bool); ok && d {
			if defaultBranch == "" {
				defaultBranch = branch
			}
			continue
		}
		exprAny, hasExpr := cm["expression"]
		if !hasExpr {
			// 没有 expression → 退化为隐式默认分支
			if defaultBranch == "" {
				defaultBranch = branch
			}
			continue
		}
		expr := strings.TrimSpace(fmt.Sprint(exprAny))
		if expr == "" {
			continue
		}
		if evalBoolExpr(expr) {
			return &Result{
				Output:       map[string]any{"matched_branch": branch},
				NextBranches: []string{branch},
			}, nil
		}
	}
	if defaultBranch != "" {
		return &Result{
			Output:       map[string]any{"matched_branch": defaultBranch},
			NextBranches: []string{defaultBranch},
		}, nil
	}
	return nil, errors.New("condition node: no condition matched and no default")
}

func isDefault(m map[string]any) bool {
	if v, ok := m["default"].(bool); ok && v {
		return true
	}
	return false
}

// evalBoolExpr 对一个已渲染的简单表达式做布尔求值。
//
// 设计原则：保持「窄而可预期」，不引入完整表达式引擎。
// 支持：
//
//	- 字面量：true / TRUE → true；false / FALSE / 0 / "" → false
//	- 比较：a == b、a != b（先尝试数值比较，否则按字符串）
//	  两侧的成对单/双引号会被剥掉。
//	- 其它任何非空、非 "false"、非 "0" 字符串 → true（truthy 兜底）
//
// 注意：仅识别第一个 == 或 != 操作符；不支持 && / || / 嵌套。
// 复杂逻辑请在 script 节点里写。
func evalBoolExpr(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false", "0":
		return false
	}
	// 同时含 == 和 !=：取最先出现的那个
	idxEq := strings.Index(s, "==")
	idxNe := strings.Index(s, "!=")
	op := ""
	cut := -1
	switch {
	case idxEq >= 0 && idxNe >= 0:
		if idxEq < idxNe {
			op, cut = "==", idxEq
		} else {
			op, cut = "!=", idxNe
		}
	case idxEq >= 0:
		op, cut = "==", idxEq
	case idxNe >= 0:
		op, cut = "!=", idxNe
	}
	if cut >= 0 {
		left := stripQuotes(strings.TrimSpace(s[:cut]))
		right := stripQuotes(strings.TrimSpace(s[cut+2:]))
		eq := compareEq(left, right)
		if op == "==" {
			return eq
		}
		return !eq
	}
	// truthy 兜底
	return true
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		first := s[0]
		last := s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func compareEq(a, b string) bool {
	if af, errA := strconv.ParseFloat(a, 64); errA == nil {
		if bf, errB := strconv.ParseFloat(b, 64); errB == nil {
			return af == bf
		}
	}
	return a == b
}
