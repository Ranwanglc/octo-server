---
type: Task
title: "Task: auth-verify-handlers"
description: Move /v1/auth/verify and /v1/auth/verify-bot HTTP handlers from modules/user to modules/auth, using the BotLookup interface from PR-A2 and the unified Auth errcodes. Combines plan PR-A3 + PR-A5 into one PR (route registration migrates here in one shot).
tags: ["auth", "modules", "refactor", "http-handler", "route-migration"]
timestamp: 2026-06-22T00:00:00Z
slug: auth-verify-handlers
upstream: "octo-server#428"
source: self
---

# Task: auth-verify-handlers

## Goal

Move the `verify` and `verify-bot` HTTP handlers out of
`modules/user/api.go` (lines 3964-4105) and into a new
`modules/auth/{contract,service,api,1module,registry}.go`
implementation that:
- consumes the `BotLookup` interface registered in PR-A2,
- uses the unified `errcode.ErrAuth*` codes (registered in this PR's
  `pkg/errcode/auth.go`) with `httperr.ResponseErrorL` per the i18n
  envelope rule,
- preserves the legacy wire shape exactly (additive new fields
  `schema_version`, `kind`, `bot_kind` only),
- registers the routes from `modules/auth/1module.go` and removes the
  duplicates from `modules/user/api.go` (collapses plan PR-A3 and
  PR-A5 into a single PR — the artificial delegate-then-migrate
  dance the plan proposed has no net benefit since the result is
  identical and the diff is more reviewable in one shot).

## Background

- Existing legacy handlers in `modules/user/api.go:3964-4105`
  (`authVerifyToken` / `authVerifyBot`) use raw
  `c.AbortWithStatusJSON` for failure paths — a direct violation of
  CLAUDE.md's Error Handling rule (the i18n envelope contract). They
  also do their own SQL inline rather than through any shared
  abstraction. No tests existed for either handler before this PR.
- The PR-A2 `BotLookup` interface is the consumer-defined seam used
  here; PR-A2's compile-time assertions
  (`var _ auth.BotLookup = (*BotAPI)(nil)`) pin the implementation in
  bot_api. Wiring in `modules/bot_api/1module.go` SetupAPI registers
  the BotAPI instance via `auth.SetBotLookup(ba)` so the verify-bot
  handler can resolve tokens at request time.
- The legacy `verifyLimit` rate-limiter
  (`StrictIPRateLimitMiddleware` tag `"verify"`,
  1000 req/min/IP / burst 100, configured at
  `modules/user/api.go:202` pre-PR) is recreated in
  `modules/auth/1module.go` with the **same tag**, so the Redis bucket
  namespace (and operator-side rate-limit dashboards) keep working.
- Plan reference: §5.1 (target directory layout), §5.2 (route /
  handler migration table), §4.1.1 / §4.1.2 (response shapes),
  §4.2 (anti-enumeration: single 401 for all verify failures).

## Load-bearing list

- **auth**: this PR is the first time the new modules/auth handles
  real HTTP traffic. Wrong response shapes break matter/fleet and
  any future SDK consumer; the wire contract here is the source of
  truth that PR-C* matter/fleet integration depends on. (rules:
  space-isolation — auth tag)
- **wire-contract**: legacy fields (uid/name/role/owned_bots,
  bot_uid/bot_name/owner_uid/owner_name/space_id) are preserved at
  the same JSON locations with the same JSON tags. New fields
  (schema_version, kind, bot_kind, scope) added additively.
  (rules: error-handling — wire-contract)
- **space + isolation**: VerifyBot's response carries `bot_kind` /
  `scope` / `space_id` that downstream SDK callers
  (PR-C3 RequireSpaceMember) use to fail-closed cross-space access.
  AppBotIdentity.Scope/SpaceID are taken verbatim from the
  bot_api LookupAppBot output (same fields the legacy authAppBot
  middleware set on Gin context); no widening. (rules:
  space-isolation)
- **bot-api**: BotLookup wiring (bot_api.1module.go SetupAPI calls
  auth.SetBotLookup) is the first cross-module DI hop that depends
  on modules/auth's registry singleton being initialised. Init-order
  is guaranteed by the import graph (modules/auth → no bot_api
  dependency, pinned by imports_test.go); blank-import order in
  internal/modules.go is a nudge for readers, not load-bearing.
  (rules: space-isolation — bot-api)
- **error-response / i18n**: three new errcodes registered
  (`err.server.auth.{token_invalid,bot_unavailable,upstream_failed}`),
  with zh-CN translations. All failure paths use
  `httperr.ResponseErrorL`; no raw `c.JSON` / `AbortWithStatusJSON`.
  Anti-enumeration: every "token bad" reason collapses to the single
  `ErrAuthTokenInvalid` 401 at the wire (specific reason in zap log
  only); `ErrAuthBotUnpublished` 503 is the one exception. (rules:
  error-handling)

## Out of scope

- `/v1/auth/verify-api-key` endpoint — PR-A4.
- Real `uk_` API Key storage (the
  `usersecret.LookupAPIKey` stub from PR-A2 stays stub).
- Migrating any of the **six existing pkg/auth importers** to import
  modules/auth directly — they keep using the alias shim. The
  modules/user importer of pkg/auth is now a smaller surface
  (Encode + Decode only — verify handler was the heaviest user) but
  is not deleted here.
- `context_included=true` query param + `spaces[]` /
  `owned_bots_by_space{}` context support — added in a future PR
  when Stage C SDK consumers need it. PR-A3 only ships
  schema_version=1 and the additive kind / bot_kind fields.
- Owner-language hydration on the verify-bot response (plan §4.1.2
  shows `language` field) — would require a per-bot user_language
  lookup; deferred until a downstream consumer needs it.
- Adding a `depguard` rule to `.golangci.yml` — the in-tree
  `modules/auth/imports_test.go` guard from PR-A2 is the
  enforcement.

## Acceptance

- New files:
  - `pkg/errcode/auth.go` (three `err.server.auth.*` codes)
  - `pkg/i18n/locales/active.zh-CN.toml` (three new entries)
  - `modules/auth/{contract,service,api,1module,registry,service_test}.go`
- Modified files:
  - `modules/bot_api/1module.go` — SetupAPI calls
    `auth.SetBotLookup(ba)`
  - `modules/usersecret/1module.go` — SetupAPI calls
    `auth.SetAPIKeyLookup(a)`
  - `modules/user/api.go` — verify route registrations removed (lines
    305-310) and handler bodies + types removed (lines 3964-4105
    pre-PR). `verifyLimit` declaration removed (no longer used in
    this file).
  - `internal/modules.go` — blank import for
    `modules/auth` added.
- `go build ./...` clean
- `go vet ./...` clean
- `golangci-lint run ./...` 0 issues
- `make i18n-extract-check` clean (markers regenerated)
- `make i18n-lint` green
- `go test ./modules/auth/...` PASS (service_test.go covers prefix
  routing / sentinel mapping / no-provider fallback / empty token
  short-circuit)
- Full per-package `go test -race -shuffle=on -count=1 -timeout 5m`
  with DROP+CREATE DB + FLUSHALL between packages: **77 of 80
  pass** — same baseline as PR-A1 / PR-A2 (same three pre-existing
  failures in `modules/{botfather,channel,robot}`).
- Wire-shape parity smoke-checked: the response JSON tags for
  uid/name/role/owned_bots and
  bot_uid/bot_name/owner_uid/owner_name/space_id are identical to
  the legacy handlers' types; verified by code-reading + the new
  contract.go's struct tags.

## Verification

1. `make env-test` (or equivalent docker stack on default ports).
2. Reset-DB loop (per CI workflow) running full `go test` matrix.
3. `golangci-lint run ./...`, `make i18n-extract-check`,
   `make i18n-lint`, `go vet ./...`.
4. Service-level unit tests via the fakeBotLookup
   (service_test.go) cover the prefix routing, sentinel-error
   mapping (`ErrAppBotUnpublished` → `ErrBotUnavailable`),
   empty-token short circuit, and no-provider fallback.
5. Manual contract spot-check: open the new contract.go beside the
   legacy `authVerifyTokenResp` / `authVerifyBotResp` from the
   pre-PR git revision and diff the JSON tags by eye to confirm
   wire compatibility.
