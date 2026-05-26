package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
)

// ScriptNode 用 goja 执行 JavaScript。
//
// 约定：
//   - script 在一个临时 runtime 内执行
//   - 全局变量 `input` 暴露 config.input（已渲染）
//   - 全局函数 `console.log` 把消息塞入 logs 数组（不直接 stdout）
//   - 脚本最终值（最后一行表达式 / 显式 return 通过 IIFE）变成 output
//
// 节点 config:
//   runtime: "javascript"            # 当前只支持 javascript
//   code:    string                  # JS 代码
//   input:   map[string]any          # 注入到 input 全局变量
//   timeout: string                  # 默认 "5s"
type ScriptNode struct{}

// NewScriptNode 构造一个 script runner
func NewScriptNode() *ScriptNode { return &ScriptNode{} }

// Type 返回 "script"
func (s *ScriptNode) Type() string { return "script" }

// Run 在 goja 中执行 JS
func (s *ScriptNode) Run(ctx context.Context, cfg map[string]any) (*Result, error) {
	runtime, _ := cfg["runtime"].(string)
	if runtime == "" {
		runtime = "javascript"
	}
	if runtime != "javascript" && runtime != "js" {
		return nil, fmt.Errorf("script node: runtime %q not supported (only javascript)", runtime)
	}
	code, _ := cfg["code"].(string)
	if code == "" {
		return nil, errors.New("script node: code is required")
	}
	input, _ := cfg["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}

	timeout := 5 * time.Second
	if ts, ok := cfg["timeout"].(string); ok && ts != "" {
		if d, err := time.ParseDuration(ts); err == nil {
			timeout = d
		}
	}

	vm := goja.New()
	if err := vm.Set("input", input); err != nil {
		return nil, fmt.Errorf("script node: set input: %w", err)
	}
	var logs []string
	console := vm.NewObject()
	_ = console.Set("log", func(call goja.FunctionCall) goja.Value {
		var parts []string
		for _, a := range call.Arguments {
			parts = append(parts, a.String())
		}
		logs = append(logs, joinSpace(parts))
		return goja.Undefined()
	})
	_ = vm.Set("console", console)

	// 用 IIFE 包一层，允许脚本 return 任意类型
	wrapped := "(function(){\n" + code + "\n})()"

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan struct {
		v   goja.Value
		err error
	}, 1)
	go func() {
		v, err := vm.RunString(wrapped)
		done <- struct {
			v   goja.Value
			err error
		}{v, err}
	}()

	var val goja.Value
	var err error
	select {
	case r := <-done:
		val, err = r.v, r.err
	case <-runCtx.Done():
		vm.Interrupt("script timeout")
		<-done
		return nil, fmt.Errorf("script node: timeout after %s", timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("script node: %w", err)
	}

	output := map[string]any{}
	if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
		exported := val.Export()
		switch v := exported.(type) {
		case map[string]any:
			output = v
		default:
			// 非 map 返回值统一放到 "result"
			// 用 JSON round-trip 把 goja 类型转换为 Go 原生
			b, _ := json.Marshal(exported)
			var rv any
			_ = json.Unmarshal(b, &rv)
			output["result"] = rv
		}
	}
	if len(logs) > 0 {
		output["_logs"] = logs
	}
	return &Result{Output: output}, nil
}

func joinSpace(p []string) string {
	if len(p) == 0 {
		return ""
	}
	out := p[0]
	for _, x := range p[1:] {
		out += " " + x
	}
	return out
}
