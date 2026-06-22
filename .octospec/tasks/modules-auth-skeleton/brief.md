---
type: Task
title: "Task: modules-auth-skeleton"
description: Create the modules/auth submodule and migrate pkg/auth (tokeninfo + parser) so future Resource-Server-facing handlers have a stable home.
tags: ["auth", "modules", "refactor", "package-move"]
timestamp: 2026-06-22T00:00:00Z
slug: modules-auth-skeleton
upstream: "octo-server#428"
source: self
---

# Task: modules-auth-skeleton

> One task = one `.octospec/tasks/<slug>/` directory. This brief is the spec for
> the work. AI may draft it from existing code; a human confirms it.

## Goal

Establish `modules/auth/` as the new home for the Resource-Server-facing token
contract (Encode/Decode + CacheTokenParser), without changing any HTTP behavior
or wire contract in this PR. `pkg/auth/` keeps working for all existing
importers via type aliases marked `// Deprecated:`. This is the first step of
the four-stage Stage A refactor that culminates in `verify` / `verify-bot` /
`verify-api-key` HTTP handlers moving out of `modules/user/api.go` (PR-A2..A5).

Why now: see plan `/Users/merlin/.claude/plans/streamed-sparking-eagle.md` §5
(`modules/auth/` design). Splitting the verify handlers out of `modules/user`
requires a destination package that owns `TokenInfo` / `CacheTokenParser` and
can be imported by `modules/user`, `modules/bot_api`, `modules/usersecret`
without cyclic dependencies. PR-A1 only establishes that destination; the
handler migration and the new `verify-api-key` endpoint land in later PRs so
each step stays independently reviewable and revertible.

## Background

- Current location: `pkg/auth/{tokeninfo,parser}.go` (+ their `_test.go`).
- Importers of `pkg/auth` today (6 files):
  - `main.go`
  - `modules/group/api.go`
  - `modules/message/api.go`
  - `modules/user/api.go`
  - `modules/user/api_manager.go`
  - `modules/qrcode/api.go`
- All six import `"github.com/Mininglamp-OSS/octo-server/pkg/auth"` and use the
  `auth.TokenInfo`, `auth.Encode`, `auth.NewCacheTokenParser`,
  `auth.WithLanguageResolver`, `auth.WithRoleResolver`, `auth.ErrEmptyToken`,
  `auth.ErrInvalidToken` surfaces. **None** of them touch unexported internals.
- `CacheTokenParser` depends only on octo-lib types
  (`cache.Cache`, `wkhttp.UserInfo`, `wkhttp.TokenParser`) — no module-local
  imports — so it is safe to relocate without touching the dependency tree
  outside the two packages.
- Plan reference: §5.1 (target directory layout), §5.3 (dependency direction
  invariant: `modules/{user,bot_api,usersecret} → modules/auth`, never the
  reverse), §11 (file checklist).
- Future work this PR enables (out of scope, listed only for review context):
  - PR-A2: extract `LookupUserBot/LookupAppBot` from `modules/bot_api`,
    `LookupAPIKey` from `modules/usersecret`.
  - PR-A3: implement new `verify` / `verify-bot` handlers in `modules/auth`,
    delegate-mode (routes still registered by `modules/user`).
  - PR-A4: add `verify-api-key` handler + route + e2e tests.
  - PR-A5: move route registration to `modules/auth/1module.go`,
    delete from `modules/user`.

## Load-bearing list

- **auth**: `TokenInfo` and `CacheTokenParser` are the single source of truth
  for what is stored under `TokenCachePrefix+token` and how every authenticated
  request's UserInfo is hydrated. Wire encoding (`v2:` prefix, legacy
  `uid@name[@role]` fallback) must remain byte-identical so tokens written by
  pre-PR binaries keep decoding. (rules: space-isolation — auth tag;
  error-handling — wire-contract concerns indirectly)
- **wire-contract**: 6 existing importers consume the exported surface
  `TokenInfo / Encode / Decode / NewCacheTokenParser / WithLanguageResolver /
  WithRoleResolver / ErrEmptyToken / ErrInvalidToken`. After this PR they must
  compile and run **without code change** — the aliases preserve every name,
  type, function signature, sentinel error identity, and panic-on-nil-cache
  behavior of `NewCacheTokenParser`. (rules: error-handling — wire-contract)
- **space + isolation**: indirectly, `CacheTokenParser` is the gateway through
  which every authenticated handler receives `wkhttp.UserInfo` (uid/role/lang)
  used by Space middleware and per-route ACLs. A regression in Decode/Parse
  would silently bypass downstream isolation. (rules: space-isolation)
- **testing**: existing tests `pkg/auth/{tokeninfo,parser}_test.go` cover v2
  decode, legacy decode, resolver wiring, sentinel-error semantics, and the
  empty-language → drop-snapshot invariant. They must keep passing against the
  new canonical location. (rules: testing)

## Out of scope

- Any new HTTP handler (verify / verify-bot / verify-api-key) — PR-A3 / A4.
- Any change to existing HTTP routes or response shapes in `modules/user`,
  `modules/group`, `modules/message`, `modules/qrcode`, `main.go`.
- Touching `modules/bot_api/auth.go` or `modules/usersecret` — PR-A2.
- Token model V2 (signed JWT / PASETO / scope claims) — out of project scope.
- i18n marker churn beyond what mechanical package-move requires (no new error
  codes; no `ResponseError*` calls introduced).
- Changing `pkg/auth`'s dependency surface — it stays a thin shim, no new
  imports.

## Acceptance

- New package exists: `modules/auth/{tokeninfo,parser,doc}.go` plus
  `{tokeninfo,parser}_test.go`. Package name `auth`. Exports identical to
  current `pkg/auth/`.
- `pkg/auth/` becomes alias shim:
  - `pkg/auth/aliases.go`: `package auth` with
    `type TokenInfo = modulesauth.TokenInfo`, sentinel `var ErrEmptyToken =
    modulesauth.ErrEmptyToken` (must preserve identity for `errors.Is`),
    function variables `var Encode = modulesauth.Encode` (same signature) and a
    `NewCacheTokenParser` wrapper preserving the existing variadic options
    surface. Whole file commented with `// Deprecated: use
    github.com/Mininglamp-OSS/octo-server/modules/auth instead. To be removed
    6 months after PR-A1.`
  - `pkg/auth/aliases_test.go`: a tiny guard test that round-trips
    `Encode(TokenInfo{...})` → `Decode` via the shimmed names, plus an
    `errors.Is` check on the sentinel, to fail-loud if aliases drift.
  - Old `pkg/auth/{tokeninfo,parser,_test}.go` files removed (their
    canonical copies live in `modules/auth/`).
- Importer surface unchanged: no source edits to `main.go`,
  `modules/{group,message,user,qrcode}/api.go`,
  `modules/user/api_manager.go`. `go build ./...` succeeds with zero
  changes outside `pkg/auth/` and the new `modules/auth/`.
- `go test ./pkg/auth/... ./modules/auth/...` passes locally.
- `go test ./...` passes locally against the standard env-test compose stack
  (MySQL + Redis + WuKongIM via `make env-test`). Existing tests that
  imported `pkg/auth` keep working unchanged.
- `make i18n-extract-check` and `make i18n-lint` pass (this PR registers no
  new error codes; markers should be a no-op).
- `golangci-lint run ./...` passes.
- `internal/modules.go` **not** modified (no blank import yet — the new
  package has no `init()` registration in PR-A1).
- The `modules/auth/doc.go` package doc states the dependency-direction
  invariant from plan §5.3 verbatim ("禁止 modules/auth 反向 import" rule),
  so a future contributor reading the package doc sees the constraint.

## Verification

1. `make env-test && go test ./...` (full suite with live MySQL/Redis/WuKongIM).
2. `make i18n-extract-check` (no diff expected).
3. `make i18n-lint` (no D23 regressions).
4. `golangci-lint run ./...` (no new findings).
5. Manual sanity: `go vet ./pkg/auth/... ./modules/auth/...`.
6. `grep -rE '"github.com/Mininglamp-OSS/octo-server/pkg/auth"' --include='*.go' .`
   still returns the same 6 files (no caller migration in this PR — that is
   intentionally deferred to keep blast radius minimal).
