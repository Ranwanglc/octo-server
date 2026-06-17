// Command lint-manager-authz is the octo-server#366 Part 1 source-guard. It
// AST-walks the module packages and fails the build if any route registered on
// a `/v1/manager` route group has a handler that does not perform a role/authz
// check.
//
// Why this guard exists:
//
//	Authorization on the manager (admin) surface is enforced per-handler by an
//	inline `c.CheckLoginRoleIsSuperAdmin()` / `c.CheckLoginRole()` call — there
//	is no central policy middleware yet (that is the SecurityEngineer's systemic
//	workstream). With the check scattered across ~100 handlers, a new
//	`/v1/manager/...` route that simply forgets the check silently ships a
//	privilege-escalation hole: `AuthMiddleware` only authenticates the caller,
//	it does NOT gate on admin role. This guard makes that omission a CI failure
//	instead of a production incident — a thin, reversible safety net that holds
//	the line until the centralized authz layer lands.
//
// What counts as "checked":
//
//	A handler is covered if its body (or a same-package helper it calls, up to a
//	small depth) invokes one of the approved role-check primitives, OR its route
//	group is constructed with a role-enforcing middleware (see roleMiddleware),
//	OR the route is explicitly allowlisted in allowlist.txt with a reason.
//
// How to satisfy the guard for a NEW /v1/manager route:
//
//	Call a role check at the top of the handler before doing any work, e.g.
//	    if err := c.CheckLoginRoleIsSuperAdmin(); err != nil { c.ResponseError(err); return }
//	(use CheckLoginRole for the admin∪superAdmin tier). If the route is
//	deliberately NOT an admin route (public, or user-scoped by UID like
//	/v1/manager/secrets), add it to tools/lint-manager-authz/allowlist.txt with
//	a one-line reason.
//
// Usage:
//
//	go run ./tools/lint-manager-authz [root...]
//
// Defaults to scanning ./modules. Test files (*_test.go) are skipped.
//
// Exit codes: 0 (clean), 1 (uncovered route(s)), 2 (walk/allowlist error).
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

// managerPathPrefix is the route-group path that puts a handler in scope. It
// matches the exact prefix and any deeper group (e.g. /v1/manager/workplace,
// /v1/manager/dashboard, /v1/manager/secrets).
const managerPathPrefix = "/v1/manager"

// allowlistRelPath lives next to this command so `go run ./tools/...` finds it
// regardless of the CI step's working directory.
const allowlistRelPath = "tools/lint-manager-authz/allowlist.txt"

// roleCheckNames is the set of *wkhttp.Context methods that count as a role/
// authz gate. Keep this in sync with octo-lib's wkhttp.Context.
var roleCheckNames = map[string]bool{
	"CheckLoginRole":             true, // admin ∪ superAdmin
	"CheckLoginRoleIsSuperAdmin": true, // superAdmin only
}

// roleMiddleware is the set of route-group middleware identifiers that enforce
// a role by themselves, making a per-handler check unnecessary. Empty today —
// the manager surface authenticates at the group (AuthMiddleware) and gates
// role per-handler. Populated here so a future role-enforcing middleware is
// recognized without touching the walk logic.
var roleMiddleware = map[string]bool{}

// httpVerbs is the set of route-registration method names on a route group.
var httpVerbs = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true, "Any": true, "Handle": true,
}

// maxHelperDepth bounds the transitive same-package helper search so a handler
// that delegates its role check to a small wrapper still counts as covered,
// without the cost (or cycles) of whole-program analysis.
const maxHelperDepth = 2

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

	pkgs, err := collectPackages(roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	var violations []string
	routeCount := 0
	for _, pkg := range pkgs {
		v, n := pkg.collectViolations(allow)
		violations = append(violations, v...)
		routeCount += n
	}

	sort.Strings(violations)
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Println(v)
		}
		fmt.Printf("\nFound %d /v1/manager route(s) with no role check.\n", len(violations))
		fmt.Println("Add a role check at the top of the handler, e.g.:")
		fmt.Println("    if err := c.CheckLoginRoleIsSuperAdmin(); err != nil { c.ResponseError(err); return }")
		fmt.Println("If the route is intentionally NOT an admin route (public, or user-scoped by UID),")
		fmt.Println("add it to " + allowlistRelPath + " with a one-line reason.")
		fmt.Println("See https://github.com/Mininglamp-OSS/octo-server/issues/366.")
		os.Exit(1)
	}

	fmt.Printf("OK: all %d /v1/manager route(s) are role-checked or allowlisted (%d allowlist entr(ies)).\n",
		routeCount, len(allow))
}

// route is a single registration on a manager route group.
type route struct {
	file                   string
	line                   int
	method                 string // HTTP verb
	path                   string // full path including the group base
	handler                ast.Expr
	enclosingRecvType      string // receiver type of the func that registered the route
	groupHasRoleMiddleware bool
}

func (r route) sig() string { return r.method + " " + r.path }

func (r route) handlerName() string {
	switch h := r.handler.(type) {
	case *ast.SelectorExpr:
		if h.Sel != nil {
			return h.Sel.Name
		}
	case *ast.Ident:
		return h.Name
	case *ast.FuncLit:
		return "<func literal>"
	}
	return "<unknown>"
}

// pkg holds the parsed files of a single Go package directory plus the indexes
// the analysis needs.
type pkg struct {
	dir   string
	fset  *token.FileSet
	files []*ast.File
	// funcsByName maps a function/method name to all its declarations in the
	// package (handlers may collide across receiver types; resolution prefers a
	// matching receiver type, see handlerHasRoleCheck). This is package-global
	// on purpose: a route registered in api_manager.go may point at a handler
	// method defined in another file of the same package.
	funcsByName map[string][]*ast.FuncDecl
}

type groupInfo struct {
	basePath  string
	hasRoleMW bool
}

// collectPackages walks the roots and groups .go files by directory.
func collectPackages(roots []string) ([]*pkg, error) {
	byDir := map[string][]string{}
	for _, root := range roots {
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if name := info.Name(); name == "vendor" || name == "tools" || name == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			dir := filepath.Dir(path)
			byDir[dir] = append(byDir[dir], path)
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("walk %q: %w", root, walkErr)
		}
	}

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var pkgs []*pkg
	for _, dir := range dirs {
		p := &pkg{
			dir:         dir,
			fset:        token.NewFileSet(),
			funcsByName: map[string][]*ast.FuncDecl{},
		}
		for _, path := range byDir[dir] {
			f, perr := parser.ParseFile(p.fset, path, nil, 0)
			if perr != nil {
				// go build / go vet is the canonical syntax gate; skip
				// unparseable files rather than failing the lint on them.
				continue
			}
			p.files = append(p.files, f)
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					p.funcsByName[fn.Name.Name] = append(p.funcsByName[fn.Name.Name], fn)
				}
			}
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// localGroupVars resolves, within a single function body, the variables bound to
// a `/v1/manager` route group. Route-group variables are function-local (every
// Route() method reuses names like `auth`/`v`), so resolution MUST be scoped to
// the function — a package-global index would conflate the admin group in
// api_manager.go with an unrelated `/v1` or `/v1/space` group of the same name
// in api.go. Subgroups derived from a manager group within the same body are
// followed to a fixed point.
func localGroupVars(body *ast.BlockStmt) map[string]groupInfo {
	type pending struct {
		name     string
		recvExpr ast.Expr // the X in X.Group(...)
		basePath string
		hasMW    bool
	}
	var all []pending
	ast.Inspect(body, func(n ast.Node) bool {
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
		if !ok || sel.Sel == nil || sel.Sel.Name != "Group" || len(call.Args) == 0 {
			return true
		}
		lit := stringLit(call.Args[0])
		if lit == "" {
			return true
		}
		all = append(all, pending{
			name:     lhs.Name,
			recvExpr: sel.X,
			basePath: lit,
			hasMW:    hasRoleMiddleware(call.Args[1:]),
		})
		return true
	})

	groups := map[string]groupInfo{}
	for changed := true; changed; {
		changed = false
		for _, pd := range all {
			if _, done := groups[pd.name]; done {
				continue
			}
			var base string
			var inheritedMW bool
			if strings.HasPrefix(pd.basePath, managerPathPrefix) {
				base = pd.basePath
			} else if recv, ok := pd.recvExpr.(*ast.Ident); ok {
				parent, ok := groups[recv.Name]
				if !ok {
					continue // not a manager group (or parent not yet known)
				}
				base = parent.basePath + pd.basePath
				inheritedMW = parent.hasRoleMW
			} else {
				continue
			}
			groups[pd.name] = groupInfo{basePath: base, hasRoleMW: pd.hasMW || inheritedMW}
			changed = true
		}
	}
	return groups
}

// collectViolations returns the uncovered-route messages for this package and
// the total number of manager routes inspected. A route is covered if it is
// allowlisted, its group carries a role-enforcing middleware, or its handler
// performs a role check.
func (p *pkg) collectViolations(allow map[string]bool) (violations []string, routeCount int) {
	for _, rt := range p.managerRoutes() {
		routeCount++
		if allow[rt.sig()] || rt.groupHasRoleMiddleware {
			continue
		}
		if p.handlerHasRoleCheck(rt.handler, rt.enclosingRecvType) {
			continue
		}
		violations = append(violations,
			fmt.Sprintf("%s:%d: %s %s -> handler %s has no role check (CheckLoginRole / CheckLoginRoleIsSuperAdmin)",
				rt.file, rt.line, rt.method, rt.path, rt.handlerName()))
	}
	return violations, routeCount
}

// managerRoutes returns every route registered on a `/v1/manager` group,
// analyzing each function independently so function-local group variables do
// not leak across Route() methods.
func (p *pkg) managerRoutes() []route {
	var routes []route
	for _, f := range p.files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			groups := localGroupVars(fn.Body)
			if len(groups) == 0 {
				continue
			}
			recvType := recvTypeName(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil || !httpVerbs[sel.Sel.Name] {
					return true
				}
				recv, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				gi, ok := groups[recv.Name]
				if !ok {
					return true
				}
				if len(call.Args) < 2 {
					return true
				}
				sub := stringLit(call.Args[0])
				handler := call.Args[len(call.Args)-1]
				pos := p.fset.Position(call.Pos())
				routes = append(routes, route{
					file:                   filepath.ToSlash(pos.Filename),
					line:                   pos.Line,
					method:                 sel.Sel.Name,
					path:                   joinRoutePath(gi.basePath, sub),
					handler:                handler,
					enclosingRecvType:      recvType,
					groupHasRoleMiddleware: gi.hasRoleMW,
				})
				return true
			})
		}
	}
	return routes
}

// handlerHasRoleCheck reports whether the handler expression (a func literal,
// package func ident, or recv.method selector) performs a role check, directly
// or via a same-package helper within maxHelperDepth.
func (p *pkg) handlerHasRoleCheck(handler ast.Expr, recvType string) bool {
	switch h := handler.(type) {
	case *ast.FuncLit:
		return p.bodyHasRoleCheck(h.Body, recvType, maxHelperDepth, map[string]bool{})
	case *ast.Ident:
		return p.namedFuncHasRoleCheck(h.Name, recvType, maxHelperDepth, map[string]bool{})
	case *ast.SelectorExpr:
		if h.Sel == nil {
			return false
		}
		return p.namedFuncHasRoleCheck(h.Sel.Name, recvType, maxHelperDepth, map[string]bool{})
	}
	return false
}

// namedFuncHasRoleCheck resolves a function/method by name (preferring a
// receiver-type match) and checks its body.
func (p *pkg) namedFuncHasRoleCheck(name, recvType string, depth int, visited map[string]bool) bool {
	if visited[name] {
		return false
	}
	visited[name] = true

	cands := p.funcsByName[name]
	if len(cands) == 0 {
		// Handler defined outside this package (rare for manager routes). We
		// cannot see its body, so we cannot confirm a check — treat as not
		// covered so the gap surfaces (allowlist if intentional).
		return false
	}
	// Prefer a candidate whose receiver type matches the registering func.
	var chosen *ast.FuncDecl
	for _, fn := range cands {
		if recvTypeName(fn) == recvType {
			chosen = fn
			break
		}
	}
	if chosen == nil {
		chosen = cands[0]
	}
	if chosen.Body == nil {
		return false
	}
	return p.bodyHasRoleCheck(chosen.Body, recvTypeName(chosen), depth, visited)
}

// bodyHasRoleCheck scans a function body for a role-check call. If none is found
// directly and depth remains, it recurses into same-package functions/methods
// the body calls (handles a handler that delegates the check to a wrapper).
func (p *pkg) bodyHasRoleCheck(body *ast.BlockStmt, recvType string, depth int, visited map[string]bool) bool {
	found := false
	var helperCalls []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if fun.Sel == nil {
				return true
			}
			if roleCheckNames[fun.Sel.Name] {
				found = true
				return false
			}
			// recv.helper(...) — candidate same-package method to recurse into.
			helperCalls = append(helperCalls, fun.Sel.Name)
		case *ast.Ident:
			// helper(...) — candidate same-package function.
			helperCalls = append(helperCalls, fun.Name)
		}
		return true
	})
	if found {
		return true
	}
	if depth <= 0 {
		return false
	}
	for _, name := range helperCalls {
		if roleCheckNames[name] {
			return true
		}
		if p.namedFuncHasRoleCheck(name, recvType, depth-1, visited) {
			return true
		}
	}
	return false
}

// hasRoleMiddleware reports whether any of the group's middleware arguments is
// a recognized role-enforcing middleware.
func hasRoleMiddleware(args []ast.Expr) bool {
	for _, a := range args {
		if name := calleeName(a); name != "" && roleMiddleware[name] {
			return true
		}
	}
	return false
}

// calleeName returns the trailing identifier of a middleware expression, e.g.
// `it.requireSuperAdmin()` -> "requireSuperAdmin", `RequireRole(r)` -> "RequireRole".
func calleeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.CallExpr:
		return calleeName(t.Fun)
	case *ast.SelectorExpr:
		if t.Sel != nil {
			return t.Sel.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// recvTypeName returns the receiver type name of a method decl ("" for a plain
// func), unwrapping a pointer receiver.
func recvTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// stringLit returns the unquoted value of a basic string literal, or "".
func stringLit(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	return strings.Trim(lit.Value, "`\"")
}

// joinRoutePath joins a group base path and a route sub-path with exactly one
// slash, tolerating sub-paths written with or without a leading slash.
func joinRoutePath(base, sub string) string {
	if sub == "" {
		return base
	}
	if strings.HasSuffix(base, "/") {
		base = strings.TrimSuffix(base, "/")
	}
	if !strings.HasPrefix(sub, "/") {
		sub = "/" + sub
	}
	return base + sub
}

// loadAllowlist parses allowlist.txt. Each non-blank, non-comment line is a
// route signature "<VERB> <path>"; an inline "# reason" is required by
// convention but not enforced syntactically. Returns a set keyed by signature.
func loadAllowlist(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	defer f.Close()

	allow := map[string]bool{}
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("malformed allowlist line %d: %q (want '<VERB> <path>')", lineNo, line)
		}
		allow[fields[0]+" "+fields[1]] = true
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return allow, nil
}
