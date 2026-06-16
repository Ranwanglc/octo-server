package messages_search

import (
	"context"
	"errors"
	"net"
	"net/url"
	"reflect"
	"testing"

	"github.com/olivere/elastic"
)

// TestClassifyOSError_TransportErrors guards P1-2: connection errors must
// route to UPSTREAM_UNAVAILABLE, not the catch-all INTERNAL_ERROR bucket.
func TestClassifyOSError_TransportErrors(t *testing.T) {
	upstreamPC := reflect.ValueOf(respondUpstream).Pointer()

	cases := []struct {
		name string
		err  error
	}{
		{"context deadline", context.DeadlineExceeded},
		{"elastic ErrNoClient", elastic.ErrNoClient},
		{"net.OpError refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
		{"url.Error wrapping OpError", &url.Error{Op: "Get", URL: "http://os", Err: &net.OpError{Op: "dial", Err: errors.New("x")}}},
		{"elastic 502", &elastic.Error{Status: 502}},
		{"elastic 503", &elastic.Error{Status: 503}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOSError(tc.err)
			if got == nil {
				t.Fatalf("expected a responder, got nil")
			}
			if reflect.ValueOf(got).Pointer() != upstreamPC {
				t.Fatalf("expected respondUpstream branch")
			}
		})
	}
}

// TestClassifyOSError_InternalForOther — non-transport errors must NOT reach
// the upstream branch; they go to INTERNAL_ERROR for ops triage.
func TestClassifyOSError_InternalForOther(t *testing.T) {
	got := classifyOSError(errors.New("malformed dsl"))
	if got == nil {
		t.Fatalf("expected a responder")
	}
	if reflect.ValueOf(got).Pointer() == reflect.ValueOf(respondUpstream).Pointer() {
		t.Fatalf("plain non-transport error should map to INTERNAL, not UPSTREAM")
	}
}

// TestClassifyOSError_Nil returns nil so callers can distinguish "ok" from
// "classified as internal".
func TestClassifyOSError_Nil(t *testing.T) {
	if got := classifyOSError(nil); got != nil {
		t.Fatalf("classifyOSError(nil) should be nil, got %T", got)
	}
}
