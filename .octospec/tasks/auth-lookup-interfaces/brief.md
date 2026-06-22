---
type: Task
title: "Task: auth-lookup-interfaces"
description: Add Lookup interfaces in modules/auth and implement them in bot_api / usersecret so PR-A3's verify handlers can build identity responses without modules/auth importing those packages.
tags: ["auth", "modules", "refactor", "interfaces", "dependency-direction"]
timestamp: 2026-06-22T00:00:00Z
slug: auth-lookup-interfaces
upstream: "octo-server#428"
source: self
---

# Task: auth-lookup-interfaces

## Goal

Define `BotLookup` and `APIKeyLookup` interfaces (plus identity result
types) in `modules/auth`, and provide implementations in `modules/bot_api`
and `modules/usersecret`. Add a self-contained guard test in
`modules/auth/imports_test.go` enforcing the dependency-direction
invariant (`modules/auth` may NOT import
`modules/{user,bot_api,usersecret,oidc}`). This is PR-A2 of the Stage A
refactor (epic #428).

Why now: PR-A3 will implement the `verify` / `verify-bot` handlers inside
`modules/auth`. Those handlers need to look up a User Bot or App Bot from
its bot token, and a future API Key from its key value, without
`modules/auth` importing the implementation packages. The Go-idiomatic
way to do this is "consumer-defined interfaces" — `modules/auth` declares
what it needs; `bot_api` / `usersecret` implement those interfaces; the
wiring happens in `main.go`. This PR establishes that seam.

## Background

- `modules/bot_api/auth.go` already contains `authUserBot` and
  `authAppBot` (lines 46-105). They each: extract a token, query the
  underlying DB row, populate Gin context keys. The DB lookup is
  delegated to `db.queryRobotByBotToken` (User Bot) and
  `db.queryAppBotByToken` (App Bot), plus an in-memory
  `lookupAppBotRegistry` O(1) fast path with DB fallback for App Bot.
- `modules/bot_api/db.go:46` defines `(d *botAPIDB) queryRobotByBotToken`
  returning `*robotModel`; `:214` defines `queryAppBotByToken` returning
  `*appBotModel`. Both are package-private.
- `modules/usersecret/db.go:166` defines `(s *store) queryBotByToken`
  returning `*botIdentity{RobotID, OwnerUID}` — but that is for the
  `resolve` endpoint's bot-side authentication, NOT for API Key
  validation. The plan's `LookupAPIKey(uk_xxx) → identity` does not
  match any existing storage in `modules/usersecret`: the
  `user_secret_alias` table stores user-owned third-party secrets
  (encrypted blobs) consumed by bots via the `resolve` endpoint, not
  daemon API keys. **There is currently no `uk_` API Key store at all
  in octo-server.** Fleet's existing call to `/v1/auth/verify-api-key`
  is a ghost endpoint that has always 404'd.
- Plan reference: §5.2 (Lookup extraction table) and §5.3 (dependency
  direction invariant: `modules/auth` ↛ `modules/{user,bot_api,
  usersecret}` implementation packages).

## Load-bearing list

- **auth**: introduces the boundary interfaces between modules/auth and
  its identity sources (bot_api, usersecret). The interface shapes will
  be the contract every future module that wants to be a verify-able
  identity source plugs into — picking these signatures wrong forces
  churn in every Stage-A PR that follows. (rules: space-isolation —
  auth tag)
- **wire-contract**: `BotLookup` / `APIKeyLookup` are internal Go
  interfaces, not HTTP wire. But the identity *types*
  (`UserBotIdentity`, `AppBotIdentity`, `APIKeyIdentity`) feed directly
  into PR-A3's verify response shape (`schema_version: 1`,
  `bot_kind` / `scope` / `space_id` / `owner_uid` per plan §4.1.2).
  Field naming and types must match the SDK contract so PR-A3 is a
  trivial mapping. (rules: error-handling — wire-contract concerns)
- **bot-api**: bot_api gains exported methods that satisfy modules/auth
  interfaces. The underlying DB queries (`queryRobotByBotToken`,
  `queryAppBotByToken`) are reused; no SQL changes. Bot ownership
  semantics — User Bot is its own owner, App Bot has an `owner_uid` /
  `scope` / `space_id` — must round-trip into the identity struct
  exactly. (rules: space-isolation — bot-api tag)
- **space + isolation**: `AppBotIdentity` exposes `Scope` (platform /
  space) and `SpaceID`. PR-A3 will use these to populate the verify
  response so downstream services can fail-closed on cross-space
  access. Getting them wrong here would silently widen App Bot
  visibility. (rules: space-isolation)

## Out of scope

- Implementing `verify` / `verify-bot` / `verify-api-key` HTTP handlers
  in `modules/auth` — that is PR-A3 and PR-A4.
- Touching route registration in `modules/user` — PR-A5.
- Wiring the Lookup implementations into `main.go` — happens in PR-A3
  when the handlers that consume them land. PR-A2 only exposes the
  interface and the implementations; no caller exists in this PR.
- Designing or building the actual `uk_` API Key storage system. Adding
  a `user_api_key` table, key-generation endpoints, encryption-at-rest,
  rotation, audit — all out of scope. `LookupAPIKey` lands as a stub
  that returns `(nil, nil)` (cache miss / not found) with a clear
  TODO and a typed error sentinel so PR-A4's verify-api-key handler
  can wire it up and return `AUTH_TOKEN_INVALID` until a future PR
  ships real key storage.
- Token model V2 (signed JWT / PASETO / scope claims).
- depguard `.golangci.yml` config change. The project's golangci-lint
  is in post-recovery profile with only `govet` enabled (see the
  comment block at the top of `.golangci.yml`); adding a new linter
  for one package is out-of-band. An in-tree Go test
  (`modules/auth/imports_test.go`) using `go/parser` walks the
  package's own AST and fails on a forbidden import — equally
  effective, in-CI via `go test ./...`, no lint-config churn.

## Acceptance

- New file `modules/auth/lookup.go` declares:
  - `type UserBotIdentity struct { BotUID, BotName string }` —
    minimal fields that map onto plan §4.1.2 `verify-bot` response's
    User Bot variant (`bot_kind: "user"`).
  - `type AppBotIdentity struct { BotUID, BotName, OwnerUID,
    OwnerName, Scope, SpaceID, Language string }` — covers plan §4.1.2
    App Bot variant including `scope ∈ {platform, space}` and the
    space binding.
  - `type APIKeyIdentity struct { UID, KeyID, SpaceID string;
    OwnedBotsBySpace map[string][]string }` — covers plan §4.1.3
    response shape; `SpaceID` and `OwnedBotsBySpace` may be empty when
    no context binding exists.
  - `type BotLookup interface { LookupUserBot(token string)
    (*UserBotIdentity, error); LookupAppBot(token string)
    (*AppBotIdentity, error) }` — single interface owns both bot
    flavors; an implementer may return `(nil, nil)` to signal "no
    match", reserving non-nil error for infrastructure failures.
  - `type APIKeyLookup interface { LookupAPIKey(apiKey string)
    (*APIKeyIdentity, error) }`.
  - Documentation in `lookup.go` reiterates the consumer-defined
    interface pattern and the dependency-direction invariant so the
    Go-idiom reasoning lives next to the code.
- New file `modules/bot_api/lookup.go` declares:
  - `func (ba *BotAPI) LookupUserBot(token string)
    (*auth.UserBotIdentity, error)` — wraps `ba.db.queryRobotByBotToken`,
    mapping `*robotModel` → `*UserBotIdentity{BotUID: RobotID,
    BotName: <RobotName field>}`. Returns `(nil, nil)` on miss; wraps
    DB errors with package context.
  - `func (ba *BotAPI) LookupAppBot(token string)
    (*auth.AppBotIdentity, error)` — preserves the existing two-tier
    lookup: in-memory registry first via `lookupAppBotRegistry`, DB
    fallback via `ba.db.queryAppBotByToken`. The `status != 1` check
    on the DB path becomes a typed sentinel
    (`ErrAppBotUnpublished` — see below) so PR-A3 can map it to the
    `AUTH_BOT_UNAVAILABLE` HTTP error.
  - Compile-time assertion: `var _ auth.BotLookup = (*BotAPI)(nil)`
    pins the implementation.
- New file `modules/auth/sentinels.go` extends the sentinel set with
  `ErrAppBotUnpublished` (Bot exists but `status != 1`). The existing
  `ErrEmptyToken` / `ErrInvalidToken` stay unchanged. This sentinel
  is exposed at the modules/auth boundary so neither bot_api nor the
  future verify handler need to invent their own error code for the
  same semantic event.
- New file `modules/usersecret/lookup.go` declares:
  - `func (a *API) LookupAPIKey(apiKey string)
    (*auth.APIKeyIdentity, error)` — **stub**: returns `(nil, nil)`
    for any input (interpreted as "not found"), with a clear TODO
    comment documenting that real `uk_` API Key storage / lookup is
    deferred to a future PR. The signature is final and matches
    `auth.APIKeyLookup` so PR-A4's verify-api-key handler can wire
    it through without breaking when real storage lands.
  - Compile-time assertion: `var _ auth.APIKeyLookup = (*API)(nil)`.
- New file `modules/auth/imports_test.go` is a guard test that parses
  every non-`_test.go` Go file under `modules/auth/` with `go/parser`
  and fails if any import path begins with the forbidden prefixes:
  `github.com/Mininglamp-OSS/octo-server/modules/user`,
  `.../modules/bot_api`, `.../modules/usersecret`, `.../modules/oidc`.
  This is the in-tree replacement for the `depguard` rule the plan
  proposed.
- `internal/modules.go` is NOT modified — the new methods are
  package-exported on existing types; no new module registration is
  added (PR-A5 handles that for `modules/auth`).
- `go build ./...` clean.
- `go test ./modules/auth/... ./modules/bot_api/... ./modules/usersecret/...`
  green.
- `go test ./...` per-package matrix has the same pass/fail
  fingerprint as PR-A1: the three known broken packages
  (`modules/{botfather,channel,robot}`) fail identically; nothing
  else regresses.
- `make i18n-extract-check` + `make i18n-lint` green.
- `golangci-lint run ./...` 0 issues.

## Verification

1. `make env-test` (or equivalent docker stack on default ports).
2. Loop: drop+create `test` DB, `redis-cli FLUSHALL`, `go test -race
   -shuffle=on -count=1 -timeout 5m PACKAGE` per package from
   `go list ./...`. 77/80 expected pass (same baseline as PR-A1).
3. `go vet ./...`, `golangci-lint run ./...`, `make i18n-extract-check`,
   `make i18n-lint` — all green.
4. Branch-flip sanity: switch to `refactor/modules-auth-skeleton`
   (PR-A1 head) and re-run the three packages that this PR touches
   (`modules/{auth,bot_api,usersecret}`) — confirm they pass there
   too (proving PR-A2 introduces no regression in the three
   touched packages either).
