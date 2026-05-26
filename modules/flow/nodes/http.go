package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPNode 执行 HTTP 请求。
//
// config:
//   method:  string                  # GET / POST / PUT / DELETE / PATCH
//   url:     string                  # 必填
//   headers: map[string]string       # 模板替换前已由引擎完成
//   body:    string | map[string]any # string 直发；map 序列化为 JSON
//   query:   map[string]string
//   timeout: string                  # 默认 "30s"
//
// output:
//   status: int
//   headers: map[string][]string
//   body: string
//   json: any   # 当响应是 JSON 时
type HTTPNode struct {
	client *http.Client
}

// NewHTTPNode 构造，client 为 nil 时使用默认（30s）
func NewHTTPNode(client *http.Client) *HTTPNode {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPNode{client: client}
}

// Type 返回 "http"
func (h *HTTPNode) Type() string { return "http" }

// Run 发请求并解析响应
func (h *HTTPNode) Run(ctx context.Context, cfg map[string]any) (*Result, error) {
	method, _ := cfg["method"].(string)
	if method == "" {
		method = http.MethodGet
	}
	method = strings.ToUpper(method)

	urlStr, _ := cfg["url"].(string)
	if urlStr == "" {
		return nil, errors.New("http node: url is required")
	}

	timeout := h.client.Timeout
	if ts, ok := cfg["timeout"].(string); ok && ts != "" {
		if d, err := time.ParseDuration(ts); err == nil {
			timeout = d
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var bodyReader io.Reader
	contentType := ""
	if b, ok := cfg["body"]; ok && b != nil {
		switch v := b.(type) {
		case string:
			if v != "" {
				bodyReader = strings.NewReader(v)
			}
		case []byte:
			bodyReader = bytes.NewReader(v)
		default:
			data, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("http node: marshal body: %w", err)
			}
			bodyReader = bytes.NewReader(data)
			contentType = "application/json"
		}
	}

	req, err := http.NewRequestWithContext(reqCtx, method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("http node: build request: %w", err)
	}
	if hdrs, ok := cfg["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			req.Header.Set(k, fmt.Sprint(v))
		}
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	if q, ok := cfg["query"].(map[string]any); ok && len(q) > 0 {
		qs := req.URL.Query()
		for k, v := range q {
			qs.Set(k, fmt.Sprint(v))
		}
		req.URL.RawQuery = qs.Encode()
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http node: do request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http node: read body: %w", err)
	}
	headers := map[string][]string{}
	for k, v := range resp.Header {
		headers[k] = v
	}
	out := map[string]any{
		"status":  resp.StatusCode,
		"headers": headers,
		"body":    string(bodyBytes),
	}
	// 尝试解析 JSON
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") || looksLikeJSON(bodyBytes) {
		var jv any
		if err := json.Unmarshal(bodyBytes, &jv); err == nil {
			out["json"] = jv
		}
	}
	return &Result{Output: out}, nil
}

func looksLikeJSON(b []byte) bool {
	s := bytes.TrimSpace(b)
	if len(s) == 0 {
		return false
	}
	return s[0] == '{' || s[0] == '['
}
