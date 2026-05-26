package nodes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPNode_GET_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"hello":"world"}`)
	}))
	defer srv.Close()

	n := NewHTTPNode(nil)
	res, err := n.Run(context.Background(), map[string]any{
		"method": "GET",
		"url":    srv.URL,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Output["status"].(int) != 200 {
		t.Fatalf("status: %v", res.Output["status"])
	}
	jv, ok := res.Output["json"].(map[string]any)
	if !ok || jv["hello"] != "world" {
		t.Fatalf("json: %#v", res.Output["json"])
	}
}

func TestHTTPNode_POST_JSONBody(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(201)
	}))
	defer srv.Close()

	n := NewHTTPNode(nil)
	_, err := n.Run(context.Background(), map[string]any{
		"method":  "POST",
		"url":     srv.URL,
		"body":    map[string]any{"a": 1.0, "b": "x"},
		"headers": map[string]any{"X-Test": "yes"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["a"].(float64) != 1 || got["b"] != "x" {
		t.Fatalf("body received: %#v", got)
	}
}

func TestHTTPNode_RequiresURL(t *testing.T) {
	n := NewHTTPNode(nil)
	if _, err := n.Run(context.Background(), map[string]any{}); err == nil {
		t.Fatalf("expected err")
	}
}
