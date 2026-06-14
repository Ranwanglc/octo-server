# RFC: Centralized Authorization Layer for Management Routes

**Issue**: [#366](https://github.com/Mininglamp-OSS/octo-server/issues/366) Part 2 · follow-up to [#363](https://github.com/Mininglamp-OSS/octo-server/issues/363) audit / [#364](https://github.com/Mininglamp-OSS/octo-server/pull/364) fix
**Status**: **Draft RFC — design proposal for maintainer review. Not yet implemented.**
**Date**: 2026-06-14
**Scope**: design only. Part 1 (the CI guard, `tools/lint-manager-authz`) has already landed; this document proposes Part 2 and how it dovetails with Part 1. No business code is changed by this RFC.
**Decision target**: agree on the *shape* of the declarative authz layer (middleware-first vs. policy-table), the migration strategy, and the eventual runtime guard — before any module is migrated.

---

## TL;DR

1. Today every `/v1/manager` handler hand-rolls its own role check in the handler body (`c.CheckLoginRole()` / `c.CheckLoginRoleIsSuperAdmin()`, or a per-module `requireAdmin` / `requireSuperAdmin` wrapper). Part 1 added a CI guard that *catches a missing check*; it does not *remove the boilerplate* or let a reviewer see the whole privilege surface in one place. That is Part 2.
2. **Proposal: a declarative role middleware** in `pkg/auth` — `RequireAdmin()` / `RequireSuperAdmin()` (thin wrappers over `RequireRole(min)`) — mounted **at route registration**, next to the path, *after* `AuthMiddleware`. The requirement moves from inside the handler body to the route declaration: `auth.POST("/x", authz.RequireSuperAdmin(), m.handler)`.
3. It is **non-breaking**: same role semantics, same single generic `ErrSharedForbidden` 403 (anti-enumeration preserved), and it reads the **per-request resolved role** wired in #364, so it stays revocation-aware for free.
4. **It already fits Part 1.** The guard's branch (b) recognizes `RequireRole` / `RequireAdmin` / `RequireSuperAdmin` by name from day one — a migrated route stays "gated" with **zero guard churn**, and the allowlist (`/v1/manager/secrets/*`, `/v1/manager/login`) is unaffected.
5. **Migration is incremental, module-by-module.** Start with uniform-tier modules (`backup`, `opanalytics` — all `superAdmin`), then mixed-tier ones. Each migration deletes the per-module `require*` + `respond*Forbidden` boilerplate and keeps the existing "plain `admin` is rejected" regression tests green.
6. **End state: a runtime guard supersedes the AST heuristic.** Once gates are middleware, a test can enumerate the registered router and assert every `/v1/manager` route's middleware chain contains an authz middleware — strictly stronger than the AST guard (no handler-resolution heuristics). The Part-1 AST guard stays as belt-and-braces until migration completes.

---

## 1. Background & current state

The #363 audit found the systemic root cause: *"鉴权裸数值散落各 handler，新增端点靠开发者自觉，漏了即权限放大，无机制兜底"*. #364 fixed the specific gaps by hand (raised four destructive cross-space ops + `appversion` to `superAdmin`) and, importantly, added **per-request role resolution** so a demotion converges within a cache TTL instead of token lifetime:

- `pkg/auth/parser.go` — `RoleResolver` interface + `WithRoleResolver`; `Parse` overrides the token's baked-in role with the authoritative `user_role:{uid}` value (Redis → DB → `""`), fail-open to the snapshot on resolver error.
- `modules/user/role_service.go` — the concrete `RoleService`.
- `main.go` — wires `auth.WithRoleResolver(userRoleSvc)` into the token parser.

Part 1 (this issue) added `tools/lint-manager-authz`: an AST guard asserting every handler under `/v1/manager` performs a role check, with an allowlist for the two intentional exceptions.

**Inventory the guard reports today: 108 manager routes across 13 modules, 2 allowlisted.** The four gate idioms in use:

| Idiom | Tier | Example |
|---|---|---|
| `c.CheckLoginRole()` | admin ∪ superAdmin | `modules/message/api_manager.go` |
| `c.CheckLoginRoleIsSuperAdmin()` | superAdmin only | `modules/backup/api_manager.go` |
| `m.requireAdmin(c)` (wrapper) | admin ∪ superAdmin | `modules/space/api_manager.go:161` |
| `m.requireSuperAdmin(c)` (wrapper) | superAdmin only | `modules/space/api_manager.go:174`, `modules/opanalytics/api.go:267` |

All four ultimately call the same two `wkhttp.Context` methods and respond with the same `errcode.ErrSharedForbidden`. The wrappers are **duplicated per module** (`space`, `opanalytics`, … each define their own), and every handler repeats the same 3–4 line preamble:

```go
func (m *Manager) handler(c *wkhttp.Context) {
    if err := c.CheckLoginRoleIsSuperAdmin(); err != nil { // ← boilerplate
        respondManagerForbidden(c)                          // ← boilerplate
        return                                              // ← boilerplate
    }
    // ... actual work ...
}
```

### Problems this leaves

- **Boilerplate**: ~108 copies of the same preamble; 5+ duplicated `require*` / `respond*Forbidden` helpers.
- **No single audit surface**: to answer "which manager routes are superAdmin-only?" you must open every handler body. The requirement is not visible at the route table.
- **Wrong-tier is invisible**: Part 1 catches a *missing* gate but cannot catch `CheckLoginRole` used where `superAdmin` was intended — that judgment is buried in the body.

---

## 2. Goals / non-goals

**Goals**
- Move the role requirement to the **route registration site**, declaratively.
- One shared implementation; delete the per-module wrappers.
- Preserve exact behavior: same tiers, same single generic 403, revocation-aware via the #364 resolver.
- Make the privilege surface **auditable in one place** (the route tables, and eventually a runtime enumeration test).
- Stay inside octo-server (no octo-lib release): the middleware builds on `wkhttp.Context` methods that already exist.

**Non-goals (explicitly out of scope here)**
- Changing any route path or role tier (that was #364's job; this is a pure refactor of *where the check lives*).
- A general RBAC/permission system, scopes, or per-resource ACLs.
- Migrating non-manager privileged surfaces (e.g. the one `/v1/common` write #363 mentioned) — can adopt the same middleware later; the guard's prefix list is already extensible.
- Retiring the Part-1 AST guard (kept until the runtime guard fully covers migration).

---

## 3. Proposed design

### 3.1 The middleware (primary mechanism)

A new file `pkg/auth/authz.go` exposing role-enforcing middleware that returns `wkhttp.HandlerFunc`:

```go
package auth

import (
    "github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
    "github.com/Mininglamp-OSS/octo-server/pkg/errcode"
    "github.com/Mininglamp-OSS/octo-server/pkg/httperr"
)

// RequireAdmin gates a route to the admin ∪ superAdmin tier. Mount AFTER
// AuthMiddleware (it reads the per-request resolved role off the context).
// On failure it writes the same generic ErrSharedForbidden 403 the in-handler
// checks use — no "needs higher role" leak (anti-enumeration) — and aborts the
// chain so the handler never runs.
func RequireAdmin() wkhttp.HandlerFunc {
    return func(c *wkhttp.Context) {
        if err := c.CheckLoginRole(); err != nil {
            httperr.ResponseErrorL(c, errcode.ErrSharedForbidden, nil, nil)
            c.Abort()
            return
        }
        c.Next()
    }
}

// RequireSuperAdmin gates a route to superAdmin only (cross-space destructive /
// supply-chain-sensitive writes). Same response + abort contract as above.
func RequireSuperAdmin() wkhttp.HandlerFunc {
    return func(c *wkhttp.Context) {
        if err := c.CheckLoginRoleIsSuperAdmin(); err != nil {
            httperr.ResponseErrorL(c, errcode.ErrSharedForbidden, nil, nil)
            c.Abort()
            return
        }
        c.Next()
    }
}
```

> A single `RequireRole(min Role)` core with the two as thin wrappers is equivalent; the two-function form is proposed because the codebase only has two tiers and explicit names read better at the call site. The Part-1 guard recognizes all three names.

Because the check delegates to `c.CheckLoginRole*`, which read the role that `CacheTokenParser.Parse` already resolved per request (#364), the middleware is **revocation-aware with no extra wiring**.

### 3.2 Mounting at registration

**Per-route** (the default, because most manager groups mix tiers — reads at `admin`, writes at `superAdmin`):

```go
auth := r.Group("/v1/manager", m.ctx.AuthMiddleware(r))
{
    auth.GET("/spaces", authz.RequireAdmin(), m.list)                  // read → admin
    auth.DELETE("/spaces/:space_id", authz.RequireSuperAdmin(), m.forceDisband) // destructive → superAdmin
}
```

**Group-level** where every route shares one floor (e.g. `backup` and `opanalytics` are uniformly `superAdmin`):

```go
auth := r.Group("/v1/manager",
    m.ctx.AuthMiddleware(r),
    authz.RequireSuperAdmin(), // every route below requires superAdmin
)
```

The handler bodies lose their preamble entirely:

```go
func (m *Manager) forceDisband(c *wkhttp.Context) {
    // role already enforced by RequireSuperAdmin() middleware
    spaceID := c.Param("space_id")
    // ... work ...
}
```

### 3.3 Ordering constraint (must document + assert)

The middleware reads the **resolved role off the request context**, which only exists after `AuthMiddleware` has run. It **must** be mounted *after* `AuthMiddleware` — exactly the same constraint `SharedUIDRateLimiter` already documents (CLAUDE.md › Rate Limiting). Mounted before, the role is empty and the middleware fails *closed* (always 403) — safe, but a broken route. Mitigations:

- Convention + doc comment on the middleware (as above).
- Optional: a tiny boot-time/registration assertion (or extend the Part-1 guard) that an authz middleware never appears earlier in a group's chain than `AuthMiddleware`.

### 3.4 Why middleware-first (and what about a policy table?)

A **policy table** (a central `map[routeKey]Role` validated at startup) was floated in the issue. Trade-offs:

| | Middleware at registration | Central policy table |
|---|---|---|
| Requirement lives next to the route | ✅ yes | ❌ in a separate file, can drift from the route |
| Greppable / diff-reviewable per route | ✅ | partial |
| Single-screen audit of all tiers | partial (read route tables) | ✅ one file |
| Integrates with existing gin/wkhttp chain | ✅ native | needs a lookup shim middleware anyway |
| Recognized by Part-1 guard today | ✅ (branch b) | needs new guard logic |

**Recommendation: middleware-first.** It is the smallest, most idiomatic change, keeps the requirement co-located with the route, and is already wired into the Part-1 guard. A policy table can be added *later* as a pure read-model: a single `authz_routes.md` / generated table for the one-screen audit, derived from the middleware annotations — without becoming the enforcement mechanism. Enforcement should stay in the request chain.

---

## 4. The runtime guard (Part-2 end state)

Once gates are middleware, the privilege surface becomes **introspectable at registration time** — we no longer need the AST heuristic to peek inside handler bodies. Proposed test (lives in `main` / an integration package that builds the real router):

```go
// Build the production router, enumerate registered routes, and assert every
// /v1/manager route's middleware chain contains a recognized authz middleware
// (or is on the documented allowlist). Strictly stronger than the AST guard:
// it sees the actual mounted chain, not a static approximation.
func TestEveryManagerRouteIsRoleGated(t *testing.T) {
    routes := buildRouter().Routes() // gin exposes RoutesInfo with handler names
    for _, rt := range routes {
        if !strings.HasPrefix(rt.Path, "/v1/manager") || allowlisted(rt) {
            continue
        }
        require.True(t, chainHasAuthz(rt), "ungated manager route: %s %s", rt.Method, rt.Path)
    }
}
```

> Feasibility note: gin's `RoutesInfo` reports the *final* handler name, not the full middleware chain, so `chainHasAuthz` needs either (a) a thin registration wrapper that records each route's middleware names as routes are added, or (b) tagging the authz middleware so its presence is detectable. Either is a small, well-contained addition. This is the one genuinely new piece of infrastructure Part 2 introduces and should be prototyped early.

**Coexistence with Part 1.** Keep the AST guard running throughout migration (it covers the not-yet-migrated in-handler checks). When 100% of manager routes use the middleware and the runtime guard is green, the AST guard can either be retired or kept as cheap belt-and-braces (it costs nothing on CI).

---

## 5. Migration plan (incremental, non-breaking)

Per module, in one PR each (small, reviewable, behavior-preserving):

1. Replace each handler's in-body `CheckLoginRole*` / `require*` preamble with the matching route/group middleware at registration.
2. Delete the now-unused per-module `requireAdmin` / `requireSuperAdmin` wrapper and `respond*Forbidden` helper.
3. Keep the **existing regression tests** (e.g. `TestManager_DestructiveOpsRequireSuperAdmin` from #364, which asserts a plain `admin` gets 403) — they should pass unchanged, since the tier and the 403 are identical. This is the proof the refactor is non-breaking.
4. No change to the Part-1 allowlist; the two genuine exceptions stay out of the middleware.

**Suggested order** (lowest risk first):

1. **Uniform-tier modules** → group-level middleware, biggest boilerplate win, simplest diff: `backup` (all superAdmin), `opanalytics` (all superAdmin, also drops its local `requireSuperAdmin`).
2. **Mostly-uniform**: `robot` (one admin read + rest superAdmin), `report` (single admin route).
3. **Mixed-tier** → per-route middleware: `space` (the biggest, with the #364 destructive ops), `message`, `common`, `workplace`, `group`, `user`, `integration`.

Worked example — `backup` before/after:

```go
// before: 8 handlers each open with
//     if err := c.CheckLoginRoleIsSuperAdmin(); err != nil { ...; return }
auth := r.Group("/v1/manager", m.ctx.AuthMiddleware(r))
auth.PUT("/backup/config", m.updateConfig) // + 7 more

// after: one group-level gate, 8 handlers lose their preamble
auth := r.Group("/v1/manager", m.ctx.AuthMiddleware(r), authz.RequireSuperAdmin())
auth.PUT("/backup/config", m.updateConfig) // body no longer re-checks
```

---

## 6. Risks & edge cases

- **Forgotten `c.Abort()`** in the middleware → handler runs despite the 403 write. The reference impl aborts; a unit test must assert the handler is not reached on denial.
- **Mis-ordering vs. `AuthMiddleware`** → fail-closed 403 (safe but broken). Covered by doc + the optional ordering assertion (§3.3).
- **The two allowlisted exceptions must NOT get the middleware.** `/v1/manager/secrets/*` is user-scoped (owner = caller, not admin) and `/v1/manager/login` is pre-auth. They stay middleware-free and remain on the Part-1 allowlist.
- **Wrong-tier review**: the migration makes the tier explicit at the route (`RequireSuperAdmin()` next to the path), which is the audit win — but the migration PRs must preserve each route's *current* tier exactly (cross-check against #364), not "tidy" them. Any tier change is a separate, signed-off decision.
- **octo-lib boundary**: the middleware lives in octo-server `pkg/auth`; it only calls existing `wkhttp.Context` methods, so **no octo-lib release is required**.
- **Anti-enumeration**: keep the single generic `ErrSharedForbidden`. Do not introduce a "needs superAdmin" variant — that leaks the tier of a route to an `admin` caller.

---

## 7. Acceptance / rollout checklist

- [ ] `pkg/auth/authz.go` with `RequireAdmin` / `RequireSuperAdmin` (+ optional `RequireRole`) and unit tests (allow / deny / abort / fail-open-on-resolver-degradation parity with #364).
- [ ] Ordering assertion (or guard extension) for authz-after-auth.
- [ ] Runtime guard `TestEveryManagerRouteIsRoleGated` + the registration-tagging shim it needs.
- [ ] Module migrations (one PR each), each keeping its `admin`-rejected regression green and deleting local `require*` / `respond*Forbidden`.
- [ ] Part-1 AST guard stays green throughout (branch b already recognizes the names); allowlist unchanged.
- [ ] (Optional) generated one-screen tier audit (`docs/authz_routes.md`).

---

## 8. Open questions for maintainer sign-off

1. **Middleware-first vs. policy-table-first** — this RFC recommends middleware-first with an optional derived audit table. Agree?
2. **`RequireRole(min)` core vs. two named functions** — two named functions proposed for readability; OK, or prefer the single parameterized form?
3. **Runtime-guard mechanism** — registration-tagging shim (records middleware names per route) vs. tagging the authz middleware. Preference?
4. **Retire vs. keep the Part-1 AST guard** after migration completes.
5. Should the same middleware be adopted for the non-manager privileged write(s) (#363's `/v1/common` note) in the same effort, or tracked separately?
