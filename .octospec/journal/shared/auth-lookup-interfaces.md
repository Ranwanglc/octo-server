---
type: Journal
title: "Journal: auth-lookup-interfaces (octo-server #428)"
description: Add BotLookup / APIKeyLookup consumer-defined interfaces in modules/auth, implement them in bot_api / usersecret, and lock the dependency direction with an in-tree guard test.
tags: ["auth", "modules", "refactor", "interfaces"]
timestamp: 2026-06-22T00:00:00Z
task: auth-lookup-interfaces
source: self
---
# Journal: auth-lookup-interfaces (octo-server #428)

## What was done

Established the in-tree seam between `modules/auth` (Resource-Server
verify handlers, landing in PR-A3) and its identity sources
(`modules/bot_api`, `modules/usersecret`) using the Go-idiomatic
**consumer-defined interfaces** pattern. Dependency direction is
locked one-way (bot_api / usersecret → auth) and enforced by an in-tree
guard test. This is PR-A2 of the Stage A epic (#428).

- `modules/auth/lookup.go` — declares `UserBotIdentity`,
  `AppBotIdentity`, `APIKeyIdentity` (field names align 1:1 with
  plan §4.1.2 / §4.1.3 verify response shapes), plus the
  `BotLookup` and `APIKeyLookup` interfaces modules/auth's HTTP
  handlers will call. Both interfaces document the same contract:
  `(nil, nil)` is the "no match" signal, non-nil error is reserved
  for infrastructure failures (must map to
  AUTH_UPSTREAM_UNAVAILABLE).
- `modules/auth/sentinels.go` — adds `ErrAppBotUnpublished` so the
  "bot exists but status != 1" event flows out of the Lookup layer
  as a typed sentinel rather than being collapsed into "no match".
  PR-A3 will map this to AUTH_BOT_UNAVAILABLE (503) per plan §4.2.
- `modules/auth/imports_test.go` — guard test using `go/parser` to
  walk every non-test `.go` file under `modules/auth/` and fail on
  any import path beginning with the forbidden prefixes
  (`modules/{user,bot_api,usersecret,oidc}`). Replaces the plan's
  proposed `depguard` lint rule because the repo's `.golangci.yml`
  is in a deliberately minimal "post-recovery" profile (only
  `govet` enabled, see the top-of-file comment); a Go test that
  travels with the package it guards is harder to silently disable
  than a lint config a contributor can comment out. Runs in CI via
  `go test ./...` with no extra wiring.
- `modules/bot_api/lookup.go` — `LookupUserBot` wraps
  `db.queryRobotByBotToken` (zero SQL change vs the existing
  authUserBot path); `LookupAppBot` preserves the two-tier
  lookup (in-memory Registry first via `lookupAppBotRegistry`, DB
  fallback via `db.queryAppBotByToken`), including the `status != 1`
  → `ErrAppBotUnpublished` mapping that matches the existing
  authAppBot:93 semantics. Compile-time assertion
  `var _ auth.BotLookup = (*BotAPI)(nil)` pins the interface.
- `modules/usersecret/lookup.go` — `LookupAPIKey` STUB that returns
  `(nil, nil)` for any input. The brief documents (and the stub
  comment reiterates) that real `uk_` API Key storage does not yet
  exist in octo-server — the existing `user_secret_alias` table is
  for user-owned third-party secrets accessed via the resolve
  endpoint, a different concept from "daemon API keys". Fleet's
  existing call to `/v1/auth/verify-api-key` is a ghost endpoint
  that has always 404'd; PR-A4 will make it a real endpoint that
  returns AUTH_TOKEN_INVALID (401) until a future PR ships real
  storage. Compile-time assertion
  `var _ auth.APIKeyLookup = (*API)(nil)` ensures the stub
  signature stays interface-compatible.

No HTTP route, errcode, or handler is added in this PR; the new
interfaces have zero callers until PR-A3 wires them up in
`main.go`. The existing authUserBot / authAppBot middleware in
`modules/bot_api/auth.go` is untouched — it stays the path for
bot-authenticated Bot API endpoints, and the new Lookup methods are
a parallel surface for modules/auth's verify handlers. This
intentional parallelism (rather than refactoring authUserBot /
authAppBot to use the new Lookup methods) keeps PR-A2's blast
radius minimal; consolidation is a future cleanup outside the
Stage A scope.

## octospec rules injected (see context.yaml)

- **space-isolation** (load-bearing): AppBotIdentity.Scope / SpaceID
  drive PR-A3's response, which in turn drives SDK fail-closed
  Space checks at the caller. The DB→identity mapping preserves the
  existing authAppBot semantics byte-for-byte (registry-hit path
  exposes Scope/SpaceID minimally; DB-fallback path includes
  full owner / name; SpaceID only populated when Scope=="space").
- **error-handling** (load-bearing): identity struct field names
  match plan §4.1.2 / §4.1.3 1-to-1 so PR-A3 is trivial mapping.
  ErrAppBotUnpublished is the typed sentinel for the
  AUTH_BOT_UNAVAILABLE (503) error code PR-A3 will register. No
  httperr / errcode / ResponseError* surface touched; i18n gates
  stay no-op.
- **testing**: imports_test.go is a self-contained AST walker (no
  DB / Redis / WuKongIM dependency); compile-time
  `var _ auth.X = (*Y)(nil)` assertions catch signature drift at
  build time rather than relying on a downstream test to fire.

## Verification

- `go build ./...` — clean
- `go vet ./modules/auth/... ./modules/bot_api/... ./modules/usersecret/...`
  — clean
- `go test ./modules/auth/... ./modules/bot_api/... ./modules/usersecret/...`
  — PASS (with fresh DB for usersecret integration tests)
- `golangci-lint run ./...` — 0 issues across whole repo
- `make i18n-extract-check` + `make i18n-lint` — green
- Full per-package `go test -race -shuffle=on -count=1 -timeout 5m`
  loop with DROP+CREATE DB + FLUSHALL between packages: **77 of 80
  pass** — exactly the same fingerprint as PR-A1 (same three
  pre-existing failures in `modules/{botfather,channel,robot}`
  tracked under #17, unrelated to auth changes).
- `imports_test.go` actually fires: artificially adding a forbidden
  import (`_ "github.com/Mininglamp-OSS/octo-server/modules/user"`)
  to `modules/auth/doc.go` triggers a test failure with a clear
  diagnostic; reverting restores green. (Sanity-checked locally;
  not committed.)

## Lessons

- **Consumer-defined interfaces over depguard for one-package
  invariants**. Adding a depguard rule to enforce "package X must
  not import Y" works for big codebases with active lint config,
  but for a single architectural seam an in-tree guard test
  (`go/parser` + walk imports) is sturdier: it lives next to the
  package it guards, fails in the same CI step as other tests,
  and can't be silently disabled by a config tweak. Recommended
  pattern for future Resource-Server-style splits.
- **Compile-time interface assertions catch drift faster than
  runtime DI failures**. `var _ auth.BotLookup = (*BotAPI)(nil)`
  fails at `go build` if either the interface or the
  implementation drifts. Cheaper than waiting for main.go's
  wiring to error at boot, and the failure points directly at
  the implementer file rather than at the wiring site.
- **Stubs need typed return contracts**, not just empty bodies.
  Returning `(nil, nil)` from the stub `LookupAPIKey` works only
  because the interface documents that as the "no match" signal
  — without that documented contract, the stub would silently
  morph the verify-api-key handler's error-mapping logic the day
  real storage lands. Explicit nil-nil documentation +
  compile-time interface assertion together give the stub a
  forward-compatible shape.
