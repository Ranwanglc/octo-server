package messages_search

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// SearchConfig holds runtime configuration for the OpenSearch-backed
// /v1/messages/_search* endpoints.
//
// TODO: lift this struct to octo-lib/config.SearchConfig once the next
// octo-lib release window opens. For now we read directly from environment
// variables to avoid coupling this feature work to an octo-lib bump.
type SearchConfig struct {
	OSAddrs     []string
	OSUsername  string
	OSPassword  string
	OSReadAlias string
	// OSInsecureHTTP permits sending basic-auth credentials to non-loopback
	// http:// addresses. Off by default: credentials over cleartext HTTP are
	// rejected at client build time unless this is explicitly set.
	OSInsecureHTTP bool
	// OSInsecureSkipVerify disables TLS certificate verification when talking
	// to the OpenSearch read cluster. Required for dev / test environments
	// that use self-signed or internal-CA-signed certificates that the
	// pod's system trust store does not include. MUST stay false in
	// production deployments where the OS cluster has properly trusted
	// certificates. Off by default; opt in via
	// OCTO_SEARCH_OS_INSECURE_SKIP_VERIFY=true.
	OSInsecureSkipVerify bool
	Timeout              time.Duration
	RateLimit            RateLimitCfg
	CursorHMAC           string
	// UserAvatarBaseURL, when non-empty, is prepended to the relative
	// `users/{uid}/avatar` template so the response carries an absolute
	// URL (spec v4.2 §2.1 / R8). When empty we keep the relative path and
	// rely on the frontend joining it with its own API base — see
	// docs/messages-search/FIX-2026-06-12.md for the SRE rollout note.
	UserAvatarBaseURL string
	// RequireSpaceID gates the p2p (DM) Space-scoping filter.
	//
	//   - true  (default): every p2p search MUST carry a non-empty
	//     X-Space-ID / `space_id` (resolved via SpaceMiddleware) and the
	//     OS DSL filters by `spaceId`. Requests without a Space resolve
	//     to NOT_FOUND (resource=channel) — fail-closed.
	//   - false: skip the spaceId term filter entirely. Operational
	//     escape hatch used while the v1.9 indexer / OS mapping is being
	//     rolled out and the corpus has not been backfilled with the
	//     `spaceId` field. Logged at WARN on every p2p request so the
	//     deviation cannot stay enabled silently.
	RequireSpaceID bool
}

// RateLimitCfg drives the per-loginUID 5 QPS / 20 burst limiter.
type RateLimitCfg struct {
	QPS   float64
	Burst int
}

// loadConfig builds a SearchConfig from process environment variables.
func loadConfig() SearchConfig {
	return SearchConfig{
		OSAddrs:              splitCSV(os.Getenv("OCTO_SEARCH_OS_ADDRS"), []string{"http://localhost:9200"}),
		OSUsername:           os.Getenv("OCTO_SEARCH_OS_USERNAME"),
		OSPassword:           os.Getenv("OCTO_SEARCH_OS_PASSWORD"),
		OSReadAlias:          defaultStr(os.Getenv("OCTO_SEARCH_OS_READ_ALIAS"), "wukongim-messages-read"),
		OSInsecureHTTP:       os.Getenv("OCTO_SEARCH_OS_INSECURE_HTTP") == "true",
		OSInsecureSkipVerify: os.Getenv("OCTO_SEARCH_OS_INSECURE_SKIP_VERIFY") == "true",
		Timeout:              parseDuration(os.Getenv("OCTO_SEARCH_TIMEOUT"), 5*time.Second),
		RateLimit: RateLimitCfg{
			QPS:   parseFloat(os.Getenv("OCTO_SEARCH_RPS"), 5.0),
			Burst: parseInt(os.Getenv("OCTO_SEARCH_BURST"), 20),
		},
		CursorHMAC:        os.Getenv("OCTO_SEARCH_CURSOR_HMAC"),
		UserAvatarBaseURL: strings.TrimRight(os.Getenv("OCTO_USER_AVATAR_BASE_URL"), "/"),
		RequireSpaceID:    parseBool(os.Getenv("OCTO_SEARCH_REQUIRE_SPACE_ID"), true),
	}
}

func splitCSV(v string, def []string) []string {
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func parseDuration(v string, def time.Duration) time.Duration {
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return def
}

func parseFloat(v string, def float64) float64 {
	if v == "" {
		return def
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
		return f
	}
	return def
}

func parseInt(v string, def int) int {
	if v == "" {
		return def
	}
	if i, err := strconv.Atoi(v); err == nil && i > 0 {
		return i
	}
	return def
}

// parseBool resolves a boolean env var, returning the default when unset or
// unparseable. We use strconv.ParseBool here so "1"/"0", "true"/"false",
// "TRUE"/"FALSE", etc. all behave the same as the Go-standard set — keeping
// operator-facing toggles boring.
func parseBool(v string, def bool) bool {
	if v == "" {
		return def
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return def
}
