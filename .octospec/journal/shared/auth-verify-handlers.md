---
type: Journal
title: "Journal: auth-verify-handlers (octo-server #428)"
description: Move /v1/auth/verify and /v1/auth/verify-bot handlers to modules/auth using the PR-A2 BotLookup interface; register the unified auth errcodes; migrate the verify routes off modules/user.
tags: ["auth", "modules", "refactor", "http-handler", "route-migration"]
timestamp: 2026-06-22T00:00:00Z
task: auth-verify-handlers
source: self
---
# Journal: auth-verify-handlers (octo-server #428)

## What was done

Migrated the `verify` and `verify-bot` HTTP handlers from `modules/user/api.go`
to a new `modules/auth/` implementation that uses the PR-A2 BotLookup interface,
the unified `errcode.ErrAuth*` codes via the i18n envelope, and registers
its own routes (collapsing plan PR-A3 + PR-A5 into one). This is PR-A3 of
the Stage A epic (#428).

- `pkg/errcode/auth.go` registers three codes —
  `err.server.auth.token_invalid` (401, anti-enumeration single code for
  every verify failure), `err.server.auth.bot_unavailable` (503, App Bot
  status != 1 — the one exception to the single-401 rule), and
  `err.server.auth.upstream_failed` (500, Internal=true, DB/cache
  failure with the cause hidden from the wire and surfaced via zap.Error
  in the handler).
- `pkg/i18n/locales/active.zh-CN.toml` adds zh-CN translations for the
  three new codes; `make i18n-extract` regenerated the server markers.
- `modules/auth/contract.go` declares `VerifyUserReq` / `VerifyUserResp` /
  `VerifyBotReq` / `VerifyBotResp` with field tags identical to the
  legacy `authVerifyTokenResp` / `authVerifyBotResp` in the pre-PR
  modules/user/api.go. New fields (`schema_version`, `kind`, `bot_kind`,
  `scope`) are additive — old SDKs and existing matter/fleet callers
  ignore unknown fields and continue to work.
- `modules/auth/registry.go` provides `SetBotLookup` / `GetBotLookup` /
  `SetAPIKeyLookup` / `GetAPIKeyLookup` singletons that mirror the
  existing `AppBotRegistry` pattern in `modules/bot_api/registry.go`.
  `atomic.Value` holders avoid the "interface type mismatch" panic
  pattern that `atomic.Value` enforces.
- `modules/auth/service.go` implements `Service.VerifyUser` and
  `Service.VerifyBot`. VerifyUser preserves the legacy
  Cache→Decode→owned_bots-join behaviour verbatim. VerifyBot routes by
  token prefix (`app_` → LookupAppBot, else → LookupUserBot), maps
  `ErrAppBotUnpublished` → `ErrBotUnavailable` (the 503), and hydrates
  owner_name + (for User Bots) current space_id via the same SQL the
  legacy handler ran. `lookupUserName` and the space-id query are
  `if s.ctx != nil`-guarded so unit tests can construct a Service with
  no `config.Context` and exercise the prefix-routing paths without
  standing up MySQL.
- `modules/auth/api.go` is the thin Gin handler layer. The
  sentinel-error → errcode mapping is concentrated in
  `handleServiceError` so the wire-contract collapse (every "token bad"
  reason → single 401) lives in one place. All failure paths use
  `httperr.ResponseErrorL` — no raw `c.JSON` / `AbortWithStatusJSON`
  remains.
- `modules/auth/1module.go` registers the module via the standard
  `register.AddModule` pattern. The `Route` hook recreates the
  `verifyLimit` rate-limiter with the IDENTICAL tag string `"verify"`
  the legacy modules/user route used so the Redis bucket namespace
  (`ratelimit:strict:verify:ip:*`) is byte-for-byte preserved.
- `modules/auth/service_test.go` covers the BotLookup-driven paths
  with an in-memory fake: prefix routing, sentinel-error mapping,
  empty-token short-circuit, and no-provider → `ErrUpstreamFailure`
  fallback. The registry-singleton tests use `t.Cleanup` to restore
  the prior value so they don't pollute siblings (the registry is
  process-global by design).
- `modules/bot_api/1module.go` SetupAPI now calls
  `auth.SetBotLookup(ba)` so the BotAPI instance is exposed to
  modules/auth's verify-bot handler.
- `modules/usersecret/1module.go` SetupAPI now calls
  `auth.SetAPIKeyLookup(a)`; the stub from PR-A2 is now wired into the
  registry so PR-A4's verify-api-key handler will pick it up the moment
  it lands (and the wire-up doesn't break the day real `uk_` storage
  arrives).
- `modules/user/api.go` lost the verify route registrations
  (lines 305-310 pre-PR), the `verifyLimit` declaration (now unused in
  this file), and the entire `authVerifyToken` / `authVerifyBot`
  blocks plus their req/resp struct types (lines 3964-4105 pre-PR). A
  block comment at the same location points at the new home.
- `internal/modules.go` adds `_ "github.com/Mininglamp-OSS/octo-server/modules/auth"`.
  The import position (before bot_api / usersecret in alphabetical
  block) is a visible nudge for readers; the actual init-order
  guarantee is the import graph (modules/auth has zero dependency
  on bot_api / usersecret, enforced by imports_test.go), so it
  sorts earlier topologically regardless.

## octospec rules injected (see context.yaml)

- **space-isolation** (load-bearing): bot_kind / scope / space_id
  preserve the existing authAppBot semantics — no widening, no
  fail-open added.
- **error-handling** (load-bearing): unified errcode + httperr
  envelope; anti-enumeration single 401; wire-contract
  backward-compatible (legacy fields verbatim, new fields additive);
  i18n gates green.
- **rate-limit** (load-bearing): verifyLimit tag `"verify"`
  preserved identically — operator dashboards keep working.
- **testing**: service_test.go with fakeBotLookup; nil-ctx-tolerant
  Service so unit tests don't need MySQL; integration paths still
  covered by the env-test matrix.

## Verification

- `go build ./...` — clean
- `go vet ./...` — clean
- `golangci-lint run ./...` — 0 issues across whole repo
- `go test ./modules/auth/...` — PASS (service_test.go all green)
- `make i18n-extract-check` — clean after `make i18n-extract`
  regenerated server markers
- `make i18n-lint` — OK on both subchecks
- Full per-package `go test -race -shuffle=on -count=1 -timeout 5m`
  with DROP+CREATE DB + FLUSHALL between packages: **77 of 80 pass**
  — same fingerprint as PR-A1 and PR-A2 (same three pre-existing
  failures in `modules/{botfather,channel,robot}`, tracked under
  #17).

## Lessons

- **Collapsing the plan's PR-A3 + PR-A5 was the right call**. The
  artificial delegate-then-migrate dance the plan proposed would
  have shipped identical end-state code through two PRs, with the
  intermediate "user delegates into auth via DI" form being uglier
  than either bookend and adding zero review value. One PR with the
  full move is more reviewable.
- **Nil-ctx tolerance lets service tests skip the env-test stack**.
  `if s.ctx != nil` in `lookupUserName` and the user-bot space-id
  query makes `&Service{}` constructable in unit tests. The
  Lookup-driven paths (prefix routing, sentinel mapping) get fast
  unit coverage; the DB-hydrated paths still get coverage in the
  full env-test matrix run.
- **Registry-singleton tests must `t.Cleanup` the prior value**.
  `atomic.Value` is process-global; a test that `SetBotLookup(fake)`
  without restoring would silently corrupt sibling tests that
  expect the production lookup. The cleanup pattern is also why
  `SetBotLookup(nil)` is a documented no-op rather than a clear —
  the only safe clear is "store back what was there before".
- **`make i18n-extract` is required after `pkg/errcode` changes**.
  CI runs `make i18n-extract-check` which fails on stale markers;
  the developer must run `make i18n-extract` locally first. Worth a
  reminder in CONTRIBUTING.md (out of scope here, but flagged for
  `.octospec/learnings/pending/`).
