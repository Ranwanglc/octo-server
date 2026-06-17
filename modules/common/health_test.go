package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIMReachable_AnyHTTPResponseIsUp confirms the probe treats any HTTP
// response — including non-2xx — as reachable. The probe asserts the WuKongIM
// API server is answering on the network, not that a specific route exists, so
// a 404/500 from a live server must NOT mark the IM boundary down.
func TestIMReachable_AnyHTTPResponseIsUp(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		err := imReachable(srv.URL)
		srv.Close()
		assert.NoError(t, err, "HTTP %d from a live IM API must count as reachable", code)
	}
}

// TestIMReachable_TransportErrorIsDown confirms a transport-level failure
// (server closed -> connection refused) surfaces as an error so the health
// handler can mark im=down.
func TestIMReachable_TransportErrorIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // close immediately so the address refuses connections

	assert.Error(t, imReachable(url))
}

// TestIMReachable_BadURL confirms a malformed URL returns an error rather than
// panicking.
func TestIMReachable_BadURL(t *testing.T) {
	assert.Error(t, imReachable("://not-a-url"))
}
