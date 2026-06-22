---
type: Task
title: "Task: auth-verify-api-key"
description: Add POST /v1/auth/verify-api-key endpoint that wires the PR-A2 APIKeyLookup stub through the standard verify Service + handler path. Same verifyLimit bucket as the other two verify endpoints; same i18n envelope; until real uk_ key storage lands, every call returns 401 ErrAuthTokenInvalid.
tags: ["auth", "modules", "verify-api-key", "endpoint"]
timestamp: 2026-06-22T00:00:00Z
slug: auth-verify-api-key
upstream: "octo-server#428"
source: self
---

# Task: auth-verify-api-key

## Goal

Add the missing `/v1/auth/verify-api-key` endpoint to modules/auth so
fleet's daemon `Authorization: Bearer uk_...` flow stops 404ing.
The endpoint wires the PR-A2 `APIKeyLookup` interface (whose
modules/usersecret implementation is still a stub) through the
existing verify Service + handler + i18n envelope + verifyLimit
plumbing. Every call returns 401 `ErrAuthTokenInvalid` until real
`uk_` storage lands in a future PR; the contract surface exists so
fleet sees structured errors instead of generic 404s, and the day
real storage arrives only the stub body in modules/usersecret has
to change.

## Background

- Plan §4.1.3 + §5.2 + §10: the verify-api-key endpoint was the
  "ghost call" fleet has been making against octo-server with no
  server-side implementation. PR-A2 added the `APIKeyLookup`
  interface; PR-A3 added the wider Service / API / 1module
  scaffold for the two existing verify endpoints. PR-A4 closes
  the loop with the missing third endpoint.
- The verifyLimit rate-limiter tag `"verify"` is shared with the
  other two verify endpoints (plan §5.4) — prevents attackers from
  probing API keys at a higher rate than they could probe session
  tokens.

## Load-bearing list

- **wire-contract**: response shape (`VerifyAPIKeyResp` —
  `schema_version`, `kind`, `uid`, `key_id`, optional `space_id`,
  optional `owned_bots_by_space`) is the source of truth for the
  SDK contract in `octo-auth/sdk-go/contract/auth-v1.yaml`.
  (rules: error-handling — wire-contract)
- **error-response / i18n**: existing `errcode.ErrAuth*` codes from
  PR-A3 are reused; no new errcode registered. Anti-enumeration:
  `ErrInvalidAPIKey` joins the existing
  `{ErrInvalidUserToken, ErrInvalidBotToken}` family that collapses
  to the single 401 `ErrAuthTokenInvalid` at the wire. (rules:
  error-handling)
- **rate-limit**: shares the `"verify"` tag with the other two
  endpoints — same bucket namespace, same operator dashboards.
  (rules: rate-limit)

## Out of scope

- Real `uk_` API Key storage (table schema, generation, encryption
  at rest, rotation, audit) — separate future PR. The
  `LookupAPIKey` stub from PR-A2 stays stub.
- Adding richer fields to the `APIKeyIdentity` shape — current
  fields match plan §4.1.3 exactly.

## Acceptance

- New code in already-modified modules/auth/{contract,service,
  api,1module,service_test}.go — pure additive surface.
- `go build ./...` clean
- `go vet ./...` clean
- `golangci-lint run ./...` 0 issues
- `make i18n-extract-check` + `make i18n-lint` green (no new
  errcodes registered)
- `go test ./modules/auth/...` PASS (service_test.go covers all
  four documented VerifyAPIKey paths: empty, no-provider, no-match,
  hit, plus the infra-error → ErrUpstreamFailure path)
- Per-package matrix passes the same 77/80 baseline.

## Verification

1. `go test ./modules/auth/...` — service tests via fakeAPIKeyLookup
2. `go build ./...`, `go vet ./...`, `golangci-lint run ./...`
3. `make i18n-extract-check`, `make i18n-lint`
4. Manual: confirm fleet's `/v1/auth/verify-api-key` calls now hit
   a real handler and get a structured 401 envelope instead of 404.
