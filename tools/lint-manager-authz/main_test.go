package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// newTestPkg builds a *pkg from one or more in-memory source files (all in the
// same package/dir), mirroring what collectPackages does on disk. Each source
// is given a synthetic filename so violation messages are stable.
func newTestPkg(t *testing.T, srcs ...string) *pkg {
	t.Helper()
	p := &pkg{
		dir:         "testpkg",
		fset:        token.NewFileSet(),
		funcsByName: map[string][]*ast.FuncDecl{},
	}
	for i, src := range srcs {
		name := "src" + string(rune('0'+i)) + ".go"
		f, err := parser.ParseFile(p.fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		p.files = append(p.files, f)
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				p.funcsByName[fn.Name.Name] = append(p.funcsByName[fn.Name.Name], fn)
			}
		}
	}
	return p
}

func violationsFor(t *testing.T, allow map[string]bool, srcs ...string) []string {
	t.Helper()
	if allow == nil {
		allow = map[string]bool{}
	}
	p := newTestPkg(t, srcs...)
	v, _ := p.collectViolations(allow)
	sort.Strings(v)
	return v
}

// A manager route whose handler has NO role check must be reported. This is the
// acceptance-criterion fixture: a deliberately-introduced unchecked manager
// route makes the guard (and therefore CI) fail.
func TestFlagsUncheckedManagerRoute(t *testing.T) {
	src := `package x
type Manager struct{}
func (m *Manager) Route(r R) {
	auth := r.Group("/v1/manager", AuthMiddleware())
	auth.GET("/danger", m.danger)
}
func (m *Manager) danger(c C) {
	c.Response("leaked admin data")
}
`
	got := violationsFor(t, nil, src)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "GET /v1/manager/danger") || !strings.Contains(got[0], "danger") {
		t.Fatalf("violation message not as expected: %q", got[0])
	}
}

// A manager route whose handler calls CheckLoginRoleIsSuperAdmin must pass.
func TestPassesSuperAdminCheckedRoute(t *testing.T) {
	src := `package x
type Manager struct{}
func (m *Manager) Route(r R) {
	auth := r.Group("/v1/manager", AuthMiddleware())
	auth.POST("/backup/trigger", m.trigger)
}
func (m *Manager) trigger(c C) {
	if err := c.CheckLoginRoleIsSuperAdmin(); err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}
`
	if got := violationsFor(t, nil, src); len(got) != 0 {
		t.Fatalf("expected 0 violations on a checked route, got: %v", got)
	}
}

// CheckLoginRole (admin∪superAdmin tier) also counts as a check.
func TestPassesAdminTierCheckedRoute(t *testing.T) {
	src := `package x
type Manager struct{}
func (m *Manager) Route(r R) {
	auth := r.Group("/v1/manager", AuthMiddleware())
	auth.GET("/me", m.me)
}
func (m *Manager) me(c C) {
	if err := c.CheckLoginRole(); err != nil {
		return
	}
	c.Response("ok")
}
`
	if got := violationsFor(t, nil, src); len(got) != 0 {
		t.Fatalf("expected 0 violations, got: %v", got)
	}
}

// REGRESSION: route-group variables are function-local. The user-scoped `/v1`
// group in one method reuses the name `auth` from the admin `/v1/manager` group
// in another method. The guard must NOT conflate them: the `/v1/users/:uid`
// route is out of scope (no flag), while the unchecked `/v1/manager/x` admin
// route IS flagged. (An earlier package-global index produced a flood of false
// positives here.)
func TestFunctionLocalGroupScoping(t *testing.T) {
	adminSrc := `package x
type Manager struct{}
func (m *Manager) Route(r R) {
	auth := r.Group("/v1/manager", AuthMiddleware())
	auth.GET("/x", m.adminX)
}
func (m *Manager) adminX(c C) { c.Response("admin") }
`
	userSrc := `package x
type API struct{}
func (a *API) Route(r R) {
	auth := a.Group("/v1", AuthMiddleware())
	auth.GET("/users/:uid", a.getUser)
}
func (a *API) getUser(c C) { c.Response("self") }
`
	got := violationsFor(t, nil, adminSrc, userSrc)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 violation (admin /v1/manager/x only), got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "/v1/manager/x") {
		t.Fatalf("expected the admin route to be flagged, got: %q", got[0])
	}
	for _, v := range got {
		if strings.Contains(v, "/v1/users") {
			t.Fatalf("user-scoped /v1 route must NOT be flagged: %q", v)
		}
	}
}

// A subgroup path is NOT under /v1/manager: routes registered on it are out of
// scope even when defined in the same function as a manager group.
func TestIgnoresNonManagerSiblingGroup(t *testing.T) {
	src := `package x
type API struct{}
func (a *API) Route(r R) {
	base := r.Group("/v1/integrations", AuthMiddleware())
	base.GET("/spaces", a.listSpaces)
	manager := r.Group("/v1/manager", AuthMiddleware())
	manager.PUT("/integrations/oidc/client", a.upsert)
}
func (a *API) listSpaces(c C) { c.Response("ok") }
func (a *API) upsert(c C) {
	if err := c.CheckLoginRoleIsSuperAdmin(); err != nil { return }
	c.ResponseOK()
}
`
	if got := violationsFor(t, nil, src); len(got) != 0 {
		t.Fatalf("expected 0 violations (base group out of scope, manager route checked), got: %v", got)
	}
}

// A nested subgroup whose parent is a /v1/manager group is in scope and its
// full path is composed from the parent base + subpath.
func TestManagerSubgroupInScope(t *testing.T) {
	src := `package x
type Manager struct{}
func (m *Manager) Route(r R) {
	mgr := r.Group("/v1/manager", AuthMiddleware())
	sub := mgr.Group("/reports")
	sub.GET("/list", m.list)
}
func (m *Manager) list(c C) { c.Response("no check") }
`
	got := violationsFor(t, nil, src)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation on the subgroup route, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "/v1/manager/reports/list") {
		t.Fatalf("expected composed subgroup path, got: %q", got[0])
	}
}

// Delegated check: the handler itself does no check but calls a same-package
// helper that does. This must count as covered (no false positive).
func TestPassesDelegatedCheck(t *testing.T) {
	src := `package x
type Manager struct{}
func (m *Manager) Route(r R) {
	auth := r.Group("/v1/manager", AuthMiddleware())
	auth.GET("/thing", m.thing)
}
func (m *Manager) thing(c C) {
	if !m.requireAdmin(c) { return }
	c.Response("ok")
}
func (m *Manager) requireAdmin(c C) bool {
	if err := c.CheckLoginRoleIsSuperAdmin(); err != nil { return false }
	return true
}
`
	if got := violationsFor(t, nil, src); len(got) != 0 {
		t.Fatalf("expected 0 violations on a delegated check, got: %v", got)
	}
}

// The allowlist suppresses an otherwise-flagged route (public/login or
// user-scoped routes that live under /v1/manager by path but are not admin).
func TestAllowlistSuppresses(t *testing.T) {
	src := `package x
type Manager struct{}
func (m *Manager) Route(r R) {
	user := r.Group("/v1/manager")
	user.POST("/login", m.login)
}
func (m *Manager) login(c C) { c.Response("token") }
`
	if got := violationsFor(t, nil, src); len(got) != 1 {
		t.Fatalf("expected 1 violation without allowlist, got: %v", got)
	}
	allow := map[string]bool{"POST /v1/manager/login": true}
	if got := violationsFor(t, allow, src); len(got) != 0 {
		t.Fatalf("expected allowlist to suppress the login route, got: %v", got)
	}
}

// A role-enforcing group middleware covers all routes on the group without a
// per-handler check. roleMiddleware is empty in production today; the test
// injects one to lock in the behavior for when one is introduced.
func TestRoleMiddlewareCoversGroup(t *testing.T) {
	roleMiddleware["RequireSuperAdmin"] = true
	defer delete(roleMiddleware, "RequireSuperAdmin")

	src := `package x
type Manager struct{}
func (m *Manager) Route(r R) {
	auth := r.Group("/v1/manager", AuthMiddleware(), RequireSuperAdmin(r))
	auth.GET("/x", m.x)
}
func (m *Manager) x(c C) { c.Response("ok") }
`
	if got := violationsFor(t, nil, src); len(got) != 0 {
		t.Fatalf("expected role middleware to cover the group, got: %v", got)
	}
}
