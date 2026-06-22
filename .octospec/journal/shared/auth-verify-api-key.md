---
type: Journal
title: "Journal: auth-verify-api-key (octo-server #428)"
description: Close the verify-api-key endpoint contract gap — wire the PR-A2 APIKeyLookup stub through the standard verify Service + handler + verifyLimit path so fleet's daemon flow gets a structured 401 envelope instead of a 404 ghost.
tags: ["auth", "modules", "verify-api-key"]
timestamp: 2026-06-22T00:00:00Z
task: auth-verify-api-key
source: self
---
# Journal: auth-verify-api-key (octo-server #428)

## What was done

PR-A4 of Stage A epic (#428). Adds the missing
`/v1/auth/verify-api-key` endpoint by extending the modules/auth
Service + API + 1module surface from PR-A3 with:

- `VerifyAPIKeyReq` / `VerifyAPIKeyResp` DTOs in
  `modules/auth/contract.go` matching plan §4.1.3 (schema_version,
  kind, uid, key_id, optional space_id, optional
  owned_bots_by_space).
- `Service.VerifyAPIKey` in `modules/auth/service.go` — same shape
  as VerifyBot: trim → check empty → lookup → map sentinel.
  Returns `ErrInvalidAPIKey` for empty/whitespace input,
  `ErrUpstreamFailure` for no-provider-registered or infra error,
  and a populated `VerifyAPIKeyResp` for happy path.
- `verifyAPIKeyHTTP` handler in `modules/auth/api.go`;
  `handleServiceError` extended to map `ErrInvalidAPIKey` into the
  existing anti-enumeration 401 `ErrAuthTokenInvalid`.
- Route registered in `modules/auth/1module.go` under the SAME
  `verifyLimit` middleware as the other two verify endpoints
  (shared `"verify"` tag → same Redis bucket namespace →
  attackers can't probe API keys faster than they can probe
  session tokens; plan §10 mitigation).
- 5-path `TestServiceVerifyAPIKey` in
  `modules/auth/service_test.go`: empty/whitespace,
  no-provider-registered, lookup-returns-nil-nil (the stub path
  fleet hits today), lookup-returns-identity (forward-compatible
  future-real-storage path), lookup-returns-error → upstream.

Until real `uk_` API Key storage exists in octo-server, the
`modules/usersecret.LookupAPIKey` stub keeps returning `(nil,
nil)`, so every fleet call to `/v1/auth/verify-api-key` resolves
to "no match" → 401 `ErrAuthTokenInvalid`. That's the same status
code matter and fleet already handle (treat as "session expired",
fall back to re-auth), so fleet's daemon flow doesn't regress —
it just stops hitting a 404 ghost endpoint and starts seeing a
structured envelope.

## octospec rules injected (see context.yaml)

- **error-handling** (load-bearing): anti-enumeration extension —
  ErrInvalidAPIKey joins the single-401 family; wire contract
  matches plan §4.1.3.
- **rate-limit** (load-bearing): shared `"verify"` tag.
- **testing**: 5-path service_test.go covers every documented
  branch.

## Verification

- `go build ./...` — clean
- `go vet ./...` — clean
- `golangci-lint run ./...` — 0 issues
- `go test ./modules/auth/...` — PASS (all 5 VerifyAPIKey paths)
- `make i18n-extract-check` — clean (no new errcodes)
- `make i18n-lint` — green

## Lessons

- **Wiring a stub through end-to-end is cheap insurance**. The
  endpoint exists; the contract is in the wire; only the stub
  body in modules/usersecret needs to change when real storage
  lands. Without this PR, the day real storage arrives someone
  would have to also remember to add the handler, the route, the
  errcode mapping, the rate-limiter, and the SDK contract entry
  — five separate places where the wiring could go subtly wrong.
- **Anti-enumeration sentinel families pay off when extended**.
  Adding ErrInvalidAPIKey to the existing
  {ErrInvalidUserToken, ErrInvalidBotToken} `errors.Is` switch
  was one line; without the family pattern it would have meant
  re-deciding whether API-key failures get their own 401 code or
  share one with bot/user failures (the right answer is share,
  but the family makes it the default).
