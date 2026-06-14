// Command lint-manager-authz is the #366 Part-1 authorization safety net. It
// asserts that every HTTP handler mounted under a privileged route prefix
// (`/v1/manager`) performs a role check, so a forgotten gate on a new
// management endpoint fails CI instead of shipping as a silent privilege
// escalation (the #364 class of bug).
//
// # What counts as "gated"
//
// A mounted manager route is considered gated if EITHER:
//
//	(a) its handler body contains a call to one of the in-handler role gates
//	    — c.CheckLoginRole(), c.CheckLoginRoleIsSuperAdmin(), or a recognized
//	    per-module wrapper (requireAdmin / requireSuperAdmin); OR
//	(b) the route (or its group) carries a recognized declarative authz
//	    middleware (RequireRole / RequireAdmin / RequireSuperAdmin).
//
// Branch (b) is wired from day one even though no such middleware exists yet:
// it is the forward-compatibility hook for #366 Part 2 (the centralized authz
// layer). When Part 2 lands and a module moves its check from the handler body
// into a route middleware, this guard keeps passing with no change — the
// allowlist only shrinks. NOTE: AuthMiddleware is authentication (identity),
// NOT authorization (role); it is deliberately NOT in the authz-middleware set,
// because "you are logged in" is exactly the guarantee #366 says is not enough.
//
// # Allowlist (intentional exceptions)
//
// Some routes under /v1/manager are legitimately not role-gated — e.g. the
// user-scoped secrets CRUD (owner = the current login user, not an admin) and
// the manager login endpoint itself (it runs before auth and verifies the role
// from the DB record). These are enumerated in allowlist.txt with a reason.
// The allowlist is stricter than a tolerate-count baseline: an entry that no
// longer matches any ungated route is reported as STALE and fails the lint, so
// the exception list cannot rot as routes gain gates or get deleted.
//
// # Resolution is fail-closed
//
// If the lint cannot resolve a route's handler to its function declaration (so
// it cannot prove a gate is present), the route is treated as ungated. A new
// handler shape the resolver does not understand therefore fails loudly rather
// than slipping through unverified.
//
// Usage:
//
//	go run ./tools/lint-manager-authz [root...]
//
// Defaults to scanning ./modules. Test files (*_test.go) are skipped.
//
// Exit codes: 0 (clean), 1 (violations: ungated/unresolved route or stale
// allowlist entry), 2 (walk/parse/baseline error).
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// allowlistRelPath lives next to this command so `go run ./tools/...` finds it
// regardless of the working directory the CI step uses.
const allowlistRelPath = "tools/lint-manager-authz/allowlist.txt"

// managerPrefixes are the privileged route prefixes the guard enforces. A route
// group whose path begins with one of these is in scope. Kept as a small list
// so coverage can grow (e.g. another privileged surface) without code changes.
var managerPrefixes = []string{"/v1/manager"}

// gateSelectorNames are the method/function names that, when called inside a
// handler body, count as an in-handler role check (branch a). The require*
// wrappers (modules/space, modules/opanalytics) themselves delegate to
// CheckLoginRole* — recognizing them by name avoids a full call-graph walk,
// matching the name-based heuristic the sibling lints already use.
var gateSelectorNames = map[string]bool{
	"CheckLoginRole":             true,
	"CheckLoginRoleIsSuperAdmin": true,
	"requireAdmin":               true,
	"requireSuperAdmin":          true,
}

// authzMiddlewareNames are the declarative role-enforcing middlewares that
// satisfy the gate when present on a route/group (branch b). This is the #366
// Part-2 hook: the names are reserved now so the central layer drops in without
// touching this guard. AuthMiddleware is intentionally absent (it is authn).
var authzMiddlewareNames = map[string]bool{
	"RequireRole":       true,
	"RequireAdmin":      true,
	"RequireSuperAdmin": true,
}

// routeVerbs are the *wkhttp.WKHttp / RouterGroup registration methods whose
// last argument is the handler.
var routeVerbs = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true, "Any": true,
}

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"modules"}
	}

	allow, err := loadAllowlist(allowlistRelPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load allowlist %q: %v\n", allowlistRelPath, err)
		os.Exit(2)
	}

	routes, err := collectManagerRoutes(roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	ungated, staleIdx := analyze(routes, allow)

	var violations []string
	for _, rt := range ungated {
		violations = append(violations, rt.violation())
	}
	var stale []string
	for _, i := range staleIdx {
		stale = append(stale, fmt.Sprintf("%s (%s:%d) — matches no ungated manager route; remove it",
			allow[i].raw, allowlistRelPath, allow[i].lineNo))
	}

	sort.Strings(violations)
	sort.Strings(stale)

	if len(violations) == 0 && len(stale) == 0 {
		fmt.Printf("OK: %d manager route(s) scanned, all gated or allowlisted (%d allowlist entr(y/ies)).\n",
			len(routes), len(allow))
		return
	}

	for _, v := range violations {
		fmt.Println("UNGATED " + v)
	}
	for _, s := range stale {
		fmt.Println("STALE   " + s)
	}
	fmt.Println()
	if len(violations) > 0 {
		fmt.Printf("Found %d manager route(s) with no role check (#366).\n", len(violations))
		fmt.Println("Add an in-handler gate (c.CheckLoginRole / c.CheckLoginRoleIsSuperAdmin, or a")
		fmt.Println("require* wrapper), or a declarative authz middleware. If the route is")
		fmt.Println("intentionally not role-gated (user-scoped / pre-auth), allowlist it with a")
		fmt.Println("reason in " + allowlistRelPath + ".")
	}
	if len(stale) > 0 {
		fmt.Printf("Found %d stale allowlist entr(y/ies) in %s.\n", len(stale), allowlistRelPath)
	}
	os.Exit(1)
}

// analyze applies the policy: every ungated route must be covered by an
// allowlist entry, and every allowlist entry must cover at least one ungated
// route. It returns the ungated-and-unallowlisted routes (violations) and the
// indexes of allowlist entries that matched nothing (stale).
func analyze(routes []route, allow []allowEntry) (violations []route, staleIdx []int) {
	used := make([]bool, len(allow))
	for _, rt := range routes {
		if rt.gated {
			continue
		}
		if idx := matchAllowlist(allow, rt); idx >= 0 {
			used[idx] = true
			continue
		}
		violations = append(violations, rt)
	}
	for i := range allow {
		if !used[i] {
			staleIdx = append(staleIdx, i)
		}
	}
	return violations, staleIdx
}

// route is one mounted handler under a manager prefix.
type route struct {
	method  string // HTTP verb, or "ANY"
	path    string // full path, e.g. /v1/manager/user/admin
	gated   bool
	reason  string // why ungated, when !gated (diagnostic only)
	file    string
	line    int
	handler string // package.Type.method, for grep-ability
}

func (rt route) violation() string {
	return fmt.Sprintf("%s %s  [handler %s] (%s:%d)%s",
		rt.method, rt.path, rt.handler, rt.file, rt.line, reasonSuffix(rt.reason))
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " — " + reason
}

// collectManagerRoutes parses every package under the roots and returns all
// routes mounted on a manager-prefixed group.
func collectManagerRoutes(roots []string) ([]route, error) {
	// Group .go files by directory (one Go package per dir) so a handler can be
	// resolved even when it is declared in a different file than its route
	// registration (e.g. modules/integration registers in api.go).
	pkgFiles := map[string][]string{}
	for _, root := range roots {
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if name := info.Name(); name == "vendor" || name == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			dir := filepath.Dir(path)
			pkgFiles[dir] = append(pkgFiles[dir], path)
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("walk %q: %w", root, walkErr)
		}
	}

	var routes []route
	for _, files := range pkgFiles {
		pkg, err := parsePackage(files)
		if err != nil {
			return nil, err
		}
		routes = append(routes, pkg.routes()...)
	}
	return routes, nil
}

// pkgScope holds the parsed files of one package plus the indexes needed to
// resolve a handler reference to its declaration.
type pkgScope struct {
	fset    *token.FileSet
	files   []*ast.File
	paths   []string                            // parallel to files
	methods map[string]map[string]*ast.FuncDecl // recvType -> name -> decl
	funcs   map[string]*ast.FuncDecl            // top-level func name -> decl
}

func parsePackage(files []string) (*pkgScope, error) {
	p := &pkgScope{
		fset:    token.NewFileSet(),
		methods: map[string]map[string]*ast.FuncDecl{},
		funcs:   map[string]*ast.FuncDecl{},
	}
	for _, path := range files {
		f, err := parser.ParseFile(p.fset, path, nil, 0)
		if err != nil {
			// go build / go vet is the canonical syntax gate; skip unparseable
			// files rather than failing the lint on them.
			continue
		}
		p.files = append(p.files, f)
		p.paths = append(p.paths, path)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if recv := receiverType(fn); recv != "" {
				if p.methods[recv] == nil {
					p.methods[recv] = map[string]*ast.FuncDecl{}
				}
				p.methods[recv][fn.Name.Name] = fn
			} else {
				p.funcs[fn.Name.Name] = fn
			}
		}
	}
	return p, nil
}

// routes scans the package for manager-group route registrations.
func (p *pkgScope) routes() []route {
	var out []route
	for i, f := range p.files {
		path := filepath.ToSlash(p.paths[i])
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			recvVar, recvType := receiverVarType(fn)
			groups := managerGroupsIn(fn)
			if len(groups) == 0 {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				recv, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				g, ok := groups[recv.Name]
				if !ok || !routeVerbs[sel.Sel.Name] {
					return true
				}
				rt := p.buildRoute(g, sel.Sel.Name, call, path, recvVar, recvType)
				if rt != nil {
					out = append(out, *rt)
				}
				return true
			})
		}
	}
	return out
}

// groupInfo describes a local variable bound to a router group.
type groupInfo struct {
	prefix      string   // full path prefix, e.g. /v1/manager/dashboard
	mws         []string // middleware selector names on the group (Group(...) + Use(...))
	managerRoot bool
}

// managerGroupsIn finds local variables in fn bound to a manager-prefixed group
// (directly or via a sub-group of one), returning var name -> groupInfo.
func managerGroupsIn(fn *ast.FuncDecl) map[string]groupInfo {
	groups := map[string]groupInfo{}
	// Two passes resolve sub-groups declared before their parent in source
	// order (rare, but cheap to be order-independent).
	for pass := 0; pass < 2; pass++ {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			lhs, ok := as.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			call, ok := as.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Group" || len(call.Args) == 0 {
				return true
			}
			sub := stringLit(call.Args[0])
			if sub == "" && !isStringLit(call.Args[0]) {
				return true
			}
			mws := middlewareNames(call.Args[1:])
			// Parent: either the router (any ident) or an already-known group.
			if base, ok := sel.X.(*ast.Ident); ok {
				if parent, known := groups[base.Name]; known {
					gi := groupInfo{
						prefix:      joinPath(parent.prefix, sub),
						mws:         append(append([]string{}, parent.mws...), mws...),
						managerRoot: parent.managerRoot,
					}
					groups[lhs.Name] = gi
					return true
				}
			}
			if hasManagerPrefix(sub) {
				groups[lhs.Name] = groupInfo{prefix: sub, mws: mws, managerRoot: true}
			}
			return true
		})
		// Fold in .Use(mw...) calls that add middleware to a known group.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Use" {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if gi, known := groups[id.Name]; known {
				gi.mws = append(gi.mws, middlewareNames(call.Args)...)
				groups[id.Name] = gi
			}
			return true
		})
	}
	// Keep only manager-rooted groups.
	for name, gi := range groups {
		if !gi.managerRoot {
			delete(groups, name)
		}
	}
	return groups
}

// buildRoute assembles a route from a `group.VERB(path, mws..., handler)` call.
func (p *pkgScope) buildRoute(g groupInfo, verb string, call *ast.CallExpr, file, recvVar, recvType string) *route {
	if len(call.Args) == 0 {
		return nil
	}
	sub := stringLit(call.Args[0])
	if !isStringLit(call.Args[0]) {
		return nil // dynamic path — cannot reason about it
	}
	full := joinPath(g.prefix, sub)
	method := verb
	if verb == "Any" {
		method = "ANY"
	}
	rt := &route{
		method: method,
		path:   full,
		file:   file,
		line:   p.fset.Position(call.Pos()).Line,
	}

	// Per-route middlewares are the args between the path and the handler.
	var routeMWs []string
	if len(call.Args) > 2 {
		routeMWs = middlewareNames(call.Args[1 : len(call.Args)-1])
	}
	if hasAuthzMiddleware(g.mws) || hasAuthzMiddleware(routeMWs) {
		rt.gated = true
		rt.handler = "(authz middleware)"
		return rt
	}

	// Otherwise inspect the handler body for an in-handler gate.
	handler := call.Args[len(call.Args)-1]
	decl, name, ok := p.resolveHandler(handler, recvVar, recvType)
	rt.handler = name
	if !ok {
		rt.gated = false
		rt.reason = "handler could not be resolved (fail-closed); add a gate or allowlist"
		return rt
	}
	if p.handlerGated(decl) {
		rt.gated = true
		return rt
	}
	rt.gated = false
	rt.reason = "no role check in handler (incl. delegated helpers)"
	return rt
}

// resolveHandler maps a handler argument to its FuncDecl. Handles method values
// (recv.Method), bare function names, and inline function literals. Returns the
// decl, a human-readable name, and whether resolution succeeded. A FuncLit is
// reported via a synthetic decl wrapper so the caller can scan it.
func (p *pkgScope) resolveHandler(expr ast.Expr, recvVar, recvType string) (*ast.FuncDecl, string, bool) {
	switch e := expr.(type) {
	case *ast.FuncLit:
		return &ast.FuncDecl{Body: e.Body}, "(inline func)", true
	case *ast.Ident:
		if fn, ok := p.funcs[e.Name]; ok {
			return fn, e.Name, true
		}
		return nil, e.Name, false
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return nil, exprName(e), false
		}
		name := e.Sel.Name
		// Same-receiver method value (the common case): recv type is known.
		if x.Name == recvVar && recvType != "" {
			if m, ok := p.methods[recvType]; ok {
				if fn, ok := m[name]; ok {
					return fn, recvType + "." + name, true
				}
			}
		}
		// Fallback: unique method with this name anywhere in the package.
		var found *ast.FuncDecl
		var foundType string
		dup := false
		for t, m := range p.methods {
			if fn, ok := m[name]; ok {
				if found != nil {
					dup = true
				}
				found, foundType = fn, t
			}
		}
		if found != nil && !dup {
			return found, foundType + "." + name, true
		}
		return nil, x.Name + "." + name, false
	default:
		return nil, exprName(expr), false
	}
}

// maxGateDepth bounds how far the body scan follows same-package delegation
// when looking for a role gate. Handlers that fan a check out through one or
// two private helpers (e.g. modules/space list -> listByStatuses ->
// requireAdmin) are common; a cycle- and depth-guarded walk keeps the guard
// honest without a full type-checked call graph.
const maxGateDepth = 6

// handlerGated reports whether the handler enforces a role check, either
// directly or through a same-receiver / same-package helper it delegates to.
func (p *pkgScope) handlerGated(fn *ast.FuncDecl) bool {
	return p.bodyGated(fn, 0, map[string]bool{})
}

func (p *pkgScope) bodyGated(fn *ast.FuncDecl, depth int, visited map[string]bool) bool {
	if fn == nil || fn.Body == nil || depth > maxGateDepth {
		return false
	}
	recvVar, recvType := receiverVarType(fn)
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			if gateSelectorNames[f.Sel.Name] {
				found = true
				return false
			}
			// Same-receiver method delegation: recurse into the callee body.
			if x, ok := f.X.(*ast.Ident); ok && x.Name == recvVar && recvType != "" {
				if p.recurseGate(recvType+"."+f.Sel.Name, p.methodDecl(recvType, f.Sel.Name), depth, visited) {
					found = true
					return false
				}
			}
		case *ast.Ident:
			if gateSelectorNames[f.Name] {
				found = true
				return false
			}
			// Same-package free-function delegation.
			if p.recurseGate("."+f.Name, p.funcs[f.Name], depth, visited) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// recurseGate descends into a resolved callee once (cycle-guarded by key).
func (p *pkgScope) recurseGate(key string, callee *ast.FuncDecl, depth int, visited map[string]bool) bool {
	if callee == nil || visited[key] {
		return false
	}
	visited[key] = true
	return p.bodyGated(callee, depth+1, visited)
}

func (p *pkgScope) methodDecl(recvType, name string) *ast.FuncDecl {
	if m, ok := p.methods[recvType]; ok {
		return m[name]
	}
	return nil
}

// middlewareNames returns the trailing selector/ident name of each arg that is
// a call expression (the typical `mw()` middleware-factory form).
func middlewareNames(args []ast.Expr) []string {
	var names []string
	for _, a := range args {
		if call, ok := a.(*ast.CallExpr); ok {
			switch f := call.Fun.(type) {
			case *ast.SelectorExpr:
				names = append(names, f.Sel.Name)
			case *ast.Ident:
				names = append(names, f.Name)
			}
		}
	}
	return names
}

func hasAuthzMiddleware(names []string) bool {
	for _, n := range names {
		if authzMiddlewareNames[n] {
			return true
		}
	}
	return false
}

// --- small AST helpers -----------------------------------------------------

func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return typeName(fn.Recv.List[0].Type)
}

func receiverVarType(fn *ast.FuncDecl) (string, string) {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "", ""
	}
	field := fn.Recv.List[0]
	t := typeName(field.Type)
	if len(field.Names) == 0 {
		return "", t
	}
	return field.Names[0].Name, t
}

// typeName returns the bare type name for `T` or `*T`.
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

func isStringLit(e ast.Expr) bool {
	bl, ok := e.(*ast.BasicLit)
	return ok && bl.Kind == token.STRING
}

func stringLit(e ast.Expr) string {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return ""
	}
	return strings.Trim(bl.Value, "`\"")
}

func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprName(t.X) + "." + t.Sel.Name
	}
	return "<expr>"
}

func hasManagerPrefix(p string) bool {
	for _, pre := range managerPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// joinPath joins a group prefix with a route sub-path, normalizing slashes the
// way the router does (a missing leading slash on the sub-path is inserted).
func joinPath(prefix, sub string) string {
	prefix = strings.TrimRight(prefix, "/")
	if sub == "" {
		return prefix
	}
	if !strings.HasPrefix(sub, "/") {
		sub = "/" + sub
	}
	return prefix + sub
}

// --- allowlist -------------------------------------------------------------

type allowEntry struct {
	method string // "*" or an HTTP verb / "ANY"
	path   string // exact, or a "/*"-suffixed prefix
	raw    string
	lineNo int
}

// matchAllowlist returns the index of the first entry matching rt, or -1.
func matchAllowlist(allow []allowEntry, rt route) int {
	for i, e := range allow {
		if e.method != "*" && !strings.EqualFold(e.method, rt.method) {
			continue
		}
		if strings.HasSuffix(e.path, "/*") {
			base := strings.TrimSuffix(e.path, "/*")
			if rt.path == base || strings.HasPrefix(rt.path, base+"/") {
				return i
			}
			continue
		}
		if e.path == rt.path {
			return i
		}
	}
	return -1
}

// loadAllowlist parses allowlist.txt. Format per line: "<METHOD> <path>"
// followed by a required "# reason". Blank lines and full-line comments are
// ignored. METHOD may be "*" (any verb); path may end in "/*" (prefix match).
func loadAllowlist(path string) ([]allowEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []allowEntry
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		body := raw
		if i := strings.Index(body, "#"); i >= 0 {
			// A reason comment is mandatory: an exception with no documented
			// justification is exactly what this guard exists to prevent.
			if strings.TrimSpace(body[i+1:]) == "" {
				return nil, fmt.Errorf("allowlist line %d: empty reason after '#': %q", lineNo, raw)
			}
			body = strings.TrimSpace(body[:i])
		} else {
			return nil, fmt.Errorf("allowlist line %d: missing '# reason': %q", lineNo, raw)
		}
		fields := strings.Fields(body)
		if len(fields) != 2 {
			return nil, fmt.Errorf("allowlist line %d: want '<METHOD> <path>  # reason', got %q", lineNo, raw)
		}
		entries = append(entries, allowEntry{
			method: fields[0],
			path:   fields[1],
			raw:    raw,
			lineNo: lineNo,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
