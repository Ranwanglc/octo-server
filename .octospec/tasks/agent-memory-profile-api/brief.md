# Agent Memory Profile API

## Goal

Implement T011 authorized list/get/update Agent memory profile APIs behind the enabled memory-control runtime.

## Load-Bearing Behavior

- Authentication is server-derived through `AuthMiddleware`.
- Platform role and active `space_member` role determine visibility and `can_manage`.
- Cross-Space and otherwise hidden profile IDs return not found.
- Profile updates synchronize authoritative App Bot owner, scope, lifecycle, trust tier, and credential epoch.
- The feature remains absent when `MEMORY_CONTROL_ENABLED` is disabled.

## Out of Scope

- Grant replacement, audit persistence, internal Agent authentication, and Bank APIs.

## Acceptance

- Platform admin and Space owner/admin/member behavior is covered by focused tests.
- Cross-Space and hidden resources anti-enumerate.
- Final `go test`, `go vet`, internal tests, i18n checks, and `git diff --check` pass.
