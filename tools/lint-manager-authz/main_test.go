package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, src string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// routesByKey indexes collected routes by "METHOD path" for assertions.
func routesByKey(rs []route) map[string]route {
	m := map[string]route{}
	for _, r := range rs {
		m[r.method+" "+r.path] = r
	}
	return m
}

// TestCollectRoutes_Gating exercises every gate-recognition path the guard
// supports: direct CheckLoginRole*, require* wrappers, one-hop delegation, a
// declarative authz middleware on the group, sub-group prefixing, an ungated
// handler, and prefix scoping (a non-manager group is ignored).
func TestCollectRoutes_Gating(t *testing.T) {
	dir := t.TempDir()

	// Direct in-handler gates + a require* wrapper + delegation, all in one
	// package (so cross-method resolution within a package is covered).
	writeFile(t, dir, "modules/direct/api.go", `package direct
type Manager struct{}
func (m *Manager) Route(r *R) {
	auth := r.Group("/v1/manager", m.authMW())
	auth.GET("/a", m.gatedAdmin)
	auth.POST("/b", m.gatedSuper)
	auth.GET("/c", m.viaWrapper)
	auth.GET("/d", m.viaDelegate)
	auth.DELETE("/e", m.ungated)
}
func (m *Manager) gatedAdmin(c *C) { _ = c.CheckLoginRole() }
func (m *Manager) gatedSuper(c *C) { _ = c.CheckLoginRoleIsSuperAdmin() }
func (m *Manager) viaWrapper(c *C) { if !m.requireAdmin(c) { return } }
func (m *Manager) viaDelegate(c *C) { m.impl(c) }
func (m *Manager) impl(c *C) { if !m.requireSuperAdmin(c) { return }; _ = c }
func (m *Manager) ungated(c *C) { _ = c.Query("x") }
`)

	// Sub-group prefix (/v1/manager/dashboard) + authz middleware (branch b),
	// whose handler body has NO in-handler gate yet is still gated.
	writeFile(t, dir, "modules/sub/api.go", `package sub
type Mgr struct{}
func (m *Mgr) Route(r *R) {
	g := r.Group("/v1/manager/dashboard", m.authMW(), m.RequireSuperAdmin())
	g.GET("/overview", m.overview)
}
func (m *Mgr) overview(c *C) { _ = c.Query("x") } // no in-body gate; middleware gates it
`)

	// A non-manager group must be ignored entirely.
	writeFile(t, dir, "modules/other/api.go", `package other
type API struct{}
func (a *API) Route(r *R) {
	g := r.Group("/v1/foo", a.authMW())
	g.GET("/bar", a.bar)
}
func (a *API) bar(c *C) { _ = c.Query("x") }
`)

	routes, err := collectManagerRoutes([]string{filepath.Join(dir, "modules")})
	if err != nil {
		t.Fatalf("collectManagerRoutes: %v", err)
	}
	by := routesByKey(routes)

	want := map[string]bool{ // key -> expected gated
		"GET /v1/manager/a":                  true,  // CheckLoginRole
		"POST /v1/manager/b":                 true,  // CheckLoginRoleIsSuperAdmin
		"GET /v1/manager/c":                  true,  // requireAdmin wrapper
		"GET /v1/manager/d":                  true,  // delegated to impl -> requireSuperAdmin
		"DELETE /v1/manager/e":               false, // genuinely ungated
		"GET /v1/manager/dashboard/overview": true,  // authz middleware (branch b)
	}
	for key, wantGated := range want {
		rt, ok := by[key]
		if !ok {
			t.Fatalf("route %q not collected; got %v", key, keys(by))
		}
		if rt.gated != wantGated {
			t.Errorf("route %q gated=%v, want %v (reason=%q handler=%q)", key, rt.gated, wantGated, rt.reason, rt.handler)
		}
	}
	if _, ok := by["GET /v1/foo/bar"]; ok {
		t.Errorf("non-manager route /v1/foo/bar must not be collected")
	}
	if len(routes) != len(want) {
		t.Errorf("collected %d routes, want %d: %v", len(routes), len(want), keys(by))
	}
}

func keys(m map[string]route) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestAnalyze covers the violation / stale-allowlist decision, including the
// two acceptance criteria: a new ungated route fails, and an allowlist entry
// that covers nothing is reported stale.
func TestAnalyze(t *testing.T) {
	routes := []route{
		{method: "GET", path: "/v1/manager/a", gated: true},
		{method: "DELETE", path: "/v1/manager/e", gated: false},      // ungated, not allowlisted
		{method: "POST", path: "/v1/manager/login", gated: false},    // ungated, exact-allowlisted
		{method: "GET", path: "/v1/manager/secrets", gated: false},   // ungated, wildcard-allowlisted
		{method: "PUT", path: "/v1/manager/secrets/x", gated: false}, // ungated, wildcard-allowlisted
	}
	allow := []allowEntry{
		{method: "POST", path: "/v1/manager/login"},
		{method: "*", path: "/v1/manager/secrets/*"},
		{method: "GET", path: "/v1/manager/gone"}, // matches nothing -> stale
	}

	violations, staleIdx := analyze(routes, allow)

	if len(violations) != 1 || violations[0].path != "/v1/manager/e" {
		t.Fatalf("violations=%v, want exactly the ungated /v1/manager/e", violations)
	}
	if len(staleIdx) != 1 || allow[staleIdx[0]].path != "/v1/manager/gone" {
		t.Fatalf("staleIdx=%v, want exactly the unused /v1/manager/gone entry", staleIdx)
	}
}

func TestMatchAllowlist(t *testing.T) {
	allow := []allowEntry{
		{method: "POST", path: "/v1/manager/login"},
		{method: "*", path: "/v1/manager/secrets/*"},
	}
	cases := []struct {
		method, path string
		wantIdx      int
	}{
		{"POST", "/v1/manager/login", 0},
		{"GET", "/v1/manager/login", -1},         // method mismatch
		{"GET", "/v1/manager/secrets", 1},        // wildcard base (no trailing segment)
		{"DELETE", "/v1/manager/secrets/abc", 1}, // wildcard subtree, any method
		{"GET", "/v1/manager/secretsX", -1},      // must not match sibling prefix
		{"GET", "/v1/manager/other", -1},
	}
	for _, tc := range cases {
		got := matchAllowlist(allow, route{method: tc.method, path: tc.path})
		if got != tc.wantIdx {
			t.Errorf("matchAllowlist(%s %s)=%d, want %d", tc.method, tc.path, got, tc.wantIdx)
		}
	}
}

func TestLoadAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allowlist.txt")
	writeFile(t, dir, "allowlist.txt", `# header comment

POST /v1/manager/login  # pre-auth entry
* /v1/manager/secrets/*  # user-scoped
`)
	entries, err := loadAllowlist(path)
	if err != nil {
		t.Fatalf("loadAllowlist: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2 (comment + blank ignored)", len(entries))
	}
	if entries[0].method != "POST" || entries[0].path != "/v1/manager/login" {
		t.Errorf("entry0=%+v", entries[0])
	}
	if entries[1].method != "*" || entries[1].path != "/v1/manager/secrets/*" {
		t.Errorf("entry1=%+v", entries[1])
	}
}

func TestLoadAllowlist_Errors(t *testing.T) {
	cases := map[string]string{
		"missing reason":  "POST /v1/manager/login\n",
		"empty reason":    "POST /v1/manager/login  #   \n",
		"too few fields":  "/v1/manager/login  # reason\n",
		"too many fields": "POST /v1/manager/login extra  # reason\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "allowlist.txt")
			writeFile(t, dir, "allowlist.txt", body)
			if _, err := loadAllowlist(path); err == nil {
				t.Fatalf("expected error for %q, got nil", body)
			}
		})
	}
}

// TestResolutionFailClosed: a handler the resolver cannot map to a declaration
// (here, a method on an unknown/imported value) must be reported ungated so it
// cannot slip through unverified.
func TestResolutionFailClosed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "modules/x/api.go", `package x
type API struct{ ext otherpkg.Handlers }
func (a *API) Route(r *R) {
	g := r.Group("/v1/manager", a.authMW())
	g.GET("/y", a.ext.Foreign) // a.ext is not a known same-package receiver
}
`)
	routes, err := collectManagerRoutes([]string{filepath.Join(dir, "modules")})
	if err != nil {
		t.Fatalf("collectManagerRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes=%d, want 1", len(routes))
	}
	if routes[0].gated {
		t.Errorf("unresolved handler must be treated as ungated (fail-closed), got gated=true")
	}
}
