---
type: Journal
title: "Journal: modules-auth-skeleton (octo-server #428)"
description: Record of the modules/auth skeleton + pkg/auth alias-shim relocation and the rules it honored.
tags: ["auth", "modules", "refactor", "package-move"]
timestamp: 2026-06-22T00:00:00Z
# --- octospec extension fields ---
task: modules-auth-skeleton
source: self
---
# Journal: modules-auth-skeleton (octo-server #428)

## What was done

Established `modules/auth/` as the Resource-Server-facing token contract owner,
moving `TokenInfo` + `Encode/Decode` + `CacheTokenParser` out of `pkg/auth/`
without any HTTP-behavior change. This is PR-A1 of the six-PR Stage A refactor
tracked in epic #428.

- `modules/auth/doc.go`: package-level documentation establishing the
  OAuth2 Authorization-Server / Resource-Server boundary and the
  dependency-direction invariant (`modules/{user,bot_api,usersecret} →
  modules/auth`, never the reverse). A CI depguard rule will be added in
  PR-A2 once the Lookup interfaces are introduced — the package doc states
  the rule now so a future contributor reading the package sees the
  constraint even before CI enforces it.
- `modules/auth/tokeninfo.go`: byte-identical relocation of `pkg/auth/
  tokeninfo.go`. v2 JSON envelope (`v2:`-prefixed) and legacy
  `uid@name[@role]` decode-fallback preserved exactly; sentinel errors
  (`ErrEmptyToken`, `ErrInvalidToken`) preserve identity.
- `modules/auth/parser.go`: byte-identical relocation of `pkg/auth/parser.go`.
  `LanguageResolver` / `RoleResolver` interfaces, `WithLanguageResolver` /
  `WithRoleResolver` option constructors, `NewCacheTokenParser` panic-on-nil-
  cache contract, fail-open resolver semantics, and "empty resolver result
  drops snapshot" invariant all preserved verbatim. The package comment now
  references "modules/auth" instead of "pkg/auth" where it explains its own
  identity, but no executable code changed.
- `modules/auth/{tokeninfo,parser}_test.go`: full test suite migrated. Same
  `package auth`, same unexported-field access, same coverage of: v2 round
  trip, legacy decode, sentinel-error semantics, V2-prefix-required guard,
  cache-error propagation (must not collapse to `ErrTokenNotFound`),
  language resolver upgrade / failure-keeps-snapshot / empty-drops-snapshot
  pinning the documented `UserLanguageResolver` contract, role resolver
  override / empty-demotes / failure-keeps-snapshot pinning the parallel
  `RoleResolver` revocation contract, and `panic-on-nil-cache`.
- `pkg/auth/aliases.go` (new): Deprecated alias shim. Every existing
  exported name (`TokenInfo`, `LanguageResolver`, `RoleResolver`,
  `CacheTokenParser`, `ParserOption`, `ErrEmptyToken`, `ErrInvalidToken`,
  `Encode`, `Decode`, `WithLanguageResolver`, `WithRoleResolver`,
  `NewCacheTokenParser`) re-exported. Types use Go `type =` aliases (so
  pkg/auth.TokenInfo IS modulesauth.TokenInfo at the type level); errors
  re-exported by value so `errors.Is(err, pkgauth.ErrEmptyToken)` keeps
  matching errors produced by the canonical package; **functions
  re-exported as wrapper funcs** that forward to `modulesauth.*` —
  preserves call signatures including variadic options while keeping the
  symbols immutable (a `var X = ...` re-export would let importers
  reassign the symbol package-globally; Jerry-Xin flagged this on PR
  review and the wrapper form is the more defensive choice for a shim
  consumed by other packages).
- `pkg/auth/aliases_test.go` (new): tiny guard test. Round-trips
  `Encode → Decode` via the shim names, then cross-checks that the shim's
  Encode output is byte-identical to the canonical package's Encode output.
  Pins `ErrEmptyToken == modulesauth.ErrEmptyToken` and `ErrInvalidToken ==
  modulesauth.ErrInvalidToken` (value identity, not just `errors.Is`). This
  fails loud if any alias drifts during the six-month deprecation window.
- `pkg/auth/{tokeninfo,parser,_test}.go`: deleted. Canonical copies now
  live in `modules/auth/`.

Crucially, the **six existing pkg/auth importers** (`main.go`,
`modules/{group,message,user,qrcode}/api.go`, `modules/user/api_manager.go`)
are NOT touched in this PR. Their `import "github.com/Mininglamp-OSS/
octo-server/pkg/auth"` lines continue to work through the alias shim.
Caller migration is deliberately deferred to keep this PR's blast radius
minimal and the diff trivially reviewable.

## octospec rules injected (see context.yaml)

- **space-isolation** (load-bearing): `CacheTokenParser` is the gateway
  hydrating `wkhttp.UserInfo` (uid / role / language) consumed by every
  authenticated handler and by Space middleware. Verified the relocation is
  pure — no new fail-open path, no widened access; the unexported
  `resolver` / `roleResolver` field shape is preserved so the resolver
  fail-open semantics (5xx-on-cache-outage → snapshot fallback) cannot be
  accidentally bypassed. The Space-isolation boundary downstream of
  AuthMiddleware is unaffected.
- **error-handling** (load-bearing): no new `httperr` / `errcode` /
  `ResponseError*` surface introduced (verify handlers and their
  per-failure error mapping land in PR-A3 onwards). Wire contract for the
  six existing call sites preserved by the alias shim. `make i18n-extract-
  check` + `make i18n-lint` both green.
- **testing**: all existing pkg/auth unit tests moved with the
  implementation, preserving t.Parallel and exact assertions. New
  `aliases_test.go` is a minimal guard, not a duplication of the codec
  tests (those live with the canonical implementation).
- Not injected: **rate-limit** (no route added/moved in PR-A1; verify-api-
  key rate limiting lands in PR-A4 reusing the existing `verifyLimit`),
  **commit-style** (always-on, applied at commit time).

## Verification

- `go test ./modules/auth/... ./pkg/auth/...` → PASS (both packages)
- `go build ./...` → clean
- `go vet ./...` → clean
- `golangci-lint run ./...` → 0 issues across the whole repo
- `make i18n-extract-check` → exit 0 (no marker diff — PR introduces no
  new errcodes)
- `make i18n-lint` → OK on both subchecks
  (`lint-direct-error-response`, `lint-unregistered-code`)
- Importer surface: `grep -rE '"github.com/Mininglamp-OSS/octo-server/
  pkg/auth"' --include='*.go' .` still returns the same 6 caller files;
  modules/auth is only imported by pkg/auth's alias shim.
- Full local integration suite (mirroring CI's per-package DROP/CREATE +
  FLUSHALL loop against a fresh docker stack on default ports): **77 of 80
  packages pass**. The three failing packages — `modules/{botfather,
  channel,robot}` — fail identically on `main` (verified by branch-flip
  comparison) and are tracked as the pre-existing migration issues
  alluded to in `.github/workflows/ci.yml` (search "OCTO migration TODO";
  issue #17 is the cleanup tracker). `modules/oidc` failed once under
  shuffle but passed two subsequent runs (flaky in pre-existing test
  infra; not auth-related).

## Lessons

- **Aliasing functions via wrapper funcs** (not `var X = Y`) is the
  more defensive form for a shim package: it preserves the exported
  call signature including variadic options, and keeps the symbols
  immutable so an importer cannot reassign `auth.Encode = customFn`
  package-globally. Worth remembering for any future package
  relocation where a shim must outlive a deprecation window. Jerry-Xin
  flagged this on PR-A1 review and the codebase now follows it.
- **Sentinel error re-export must be by value** (`var ErrX = pkg.ErrX`),
  not `var ErrX = errors.New(...)`. The latter would create a distinct
  error value and silently break `errors.Is` for callers — a footgun
  the new `aliases_test.go` guard test now pins explicitly.
- **Pure relocations still want the OAuth2-style boundary doc up front**.
  `modules/auth/doc.go` states the Resource-Server / Authorization-
  Server split and the dependency-direction invariant on day one, even
  though the CI depguard rule that enforces the invariant only lands in
  PR-A2. The reasoning lives in the destination package; the
  enforcement comes later. This avoids a future contributor adding a
  user-table import in modules/auth between PR-A1 and PR-A2 and being
  surprised when PR-A2's CI guard catches it.
- **Local integration testing requires per-package DB reset** to dodge
  the cross-package `gorp_migrations` ledger leak. The CI workflow
  comment already documents this; I replicated the same DROP/CREATE +
  FLUSHALL loop locally to verify PR-A1. Promoting that pattern to a
  Makefile target (e.g. `make local-ci`) would save the next person an
  hour. Not in scope here; flagged for `.octospec/learnings/pending/`.
