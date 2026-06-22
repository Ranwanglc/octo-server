// imports_test.go is the in-tree enforcement of the dependency-direction
// invariant documented in doc.go: modules/auth must NOT import the
// implementation packages of any identity source it consumes
// (modules/{user,bot_api,usersecret,oidc}). The Go-idiomatic pattern is
// "consumer-defined interfaces": modules/auth declares the BotLookup /
// APIKeyLookup interfaces, the implementer packages import modules/auth
// and satisfy them, and main.go wires the concrete instance at boot.
//
// This test walks every non-_test.go file under modules/auth/, parses
// imports with go/parser, and fails if any import path begins with a
// forbidden prefix. It runs in CI via `go test ./...` so an accidental
// reverse import is caught at the same time as any other test failure
// — no separate lint gate, no .golangci.yml change.
//
// The alternative would have been a golangci-lint depguard rule, but
// (1) the project's .golangci.yml is in a deliberately minimal
// post-recovery profile with only govet enabled, and (2) a Go test
// that travels with the package it guards is harder to silently
// disable than a lint config a contributor can comment out.

package auth

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportPrefixes lists package import paths that modules/auth
// MUST NOT depend on, even transitively at the source level. The
// invariant is documented in doc.go ("Dependency direction (load-bearing
// invariant)"). Each entry is matched as a prefix so `modules/user`
// also catches `modules/user/foo`.
var forbiddenImportPrefixes = []string{
	"github.com/Mininglamp-OSS/octo-server/modules/user",
	"github.com/Mininglamp-OSS/octo-server/modules/bot_api",
	"github.com/Mininglamp-OSS/octo-server/modules/usersecret",
	"github.com/Mininglamp-OSS/octo-server/modules/oidc",
}

// TestNoForbiddenImports asserts that no non-test source file under
// modules/auth/ imports any of the identity-source implementation
// packages it logically depends on. Reverse imports — even a single
// stray "_ " blank import — would break the OAuth2 Resource-Server /
// Authorization-Server split that this module is built around and
// would prevent the future extraction of modules/auth into its own
// service.
func TestNoForbiddenImports(t *testing.T) {
	t.Parallel()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("readdir %s: %v", wd, err)
	}

	fset := token.NewFileSet()
	var checked int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(wd, name)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			// imp.Path.Value is the import literal including quotes; strip.
			ip := strings.Trim(imp.Path.Value, `"`)
			for _, forbidden := range forbiddenImportPrefixes {
				if ip == forbidden || strings.HasPrefix(ip, forbidden+"/") {
					t.Errorf("%s imports forbidden package %q — modules/auth must not depend on identity-source implementations (see doc.go)", name, ip)
				}
			}
		}
		checked++
	}

	if checked == 0 {
		t.Fatal("guard test scanned zero files — this would silently disable enforcement; check working dir / file glob")
	}
}
