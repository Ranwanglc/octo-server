package flow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// templateRefRe 匹配 {{path.to.value}}
var templateRefRe = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Render 把字符串里的 {{ref}} 替换为 ctx 中的实际值（toString）
//
// 支持的 ref:
//   {{trigger.payload.foo.bar}}
//   {{trigger.input.x}}
//   {{nodes.node_id.output.field}}  或简写: {{node_id.output.field}}
//   {{vars.var_name}}
func Render(s string, ctx *ExecutionContext) string {
	if ctx == nil {
		return s
	}
	return templateRefRe.ReplaceAllStringFunc(s, func(match string) string {
		m := templateRefRe.FindStringSubmatch(match)
		if len(m) < 2 {
			return match
		}
		v, ok := Resolve(m[1], ctx)
		if !ok {
			return ""
		}
		return toString(v)
	})
}

// RenderAny 渲染任意值：
//   - string → Render
//   - map / slice → 递归
//   - 如果整段就是 {{single_ref}}，会保留原始类型（不强转 string）
func RenderAny(v any, ctx *ExecutionContext) any {
	if ctx == nil {
		return v
	}
	switch val := v.(type) {
	case string:
		// 整段 single-ref 直接返回原始类型
		trimmed := strings.TrimSpace(val)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") &&
			strings.Count(trimmed, "{{") == 1 {
			inner := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			if rv, ok := Resolve(inner, ctx); ok {
				return rv
			}
			return nil
		}
		return Render(val, ctx)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = RenderAny(vv, ctx)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = RenderAny(vv, ctx)
		}
		return out
	default:
		return v
	}
}

// Resolve 按 dotted path 在 ctx 中查找值
func Resolve(path string, ctx *ExecutionContext) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" || ctx == nil {
		return nil, false
	}
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil, false
	}
	var cur any
	switch parts[0] {
	case "trigger":
		cur = map[string]any{
			"type":    ctx.Trigger.Type,
			"payload": ctx.Trigger.Payload,
			"input":   ctx.Trigger.Input,
		}
	case "nodes":
		// {{nodes.id.output.field}}
		m := map[string]any{}
		for k, v := range ctx.Nodes {
			m[k] = nodeContextToMap(v)
		}
		cur = m
	case "vars":
		cur = ctx.Vars
	default:
		// 简写：{{node_id.output.field}} — 第一段是 node_id
		if nc, ok := ctx.Nodes[parts[0]]; ok {
			cur = nodeContextToMap(nc)
			parts = parts[1:]
			return descend(cur, parts)
		}
		// 也可能是 vars 中的顶层名（无 vars. 前缀，但更安全是必须前缀）
		return nil, false
	}
	return descend(cur, parts[1:])
}

func descend(cur any, parts []string) (any, bool) {
	for _, p := range parts {
		if cur == nil {
			return nil, false
		}
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[p]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil, false
			}
			cur = v[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

func nodeContextToMap(n NodeContext) map[string]any {
	return map[string]any{
		"status": n.Status,
		"input":  n.Input,
		"output": n.Output,
		"error":  n.Error,
	}
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int, int32, int64, float32, float64:
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
