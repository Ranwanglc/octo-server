# Agent Memory Revocation Freshness

## Goal

Implement T015 credential-epoch and policy freshness for internal effective Agent contexts.

## Load-Bearing Behavior

- Every context request reads authoritative published App Bot, profile, active policy, Bank, and grant facts; no positive authorization cache is used.
- App Bot token and profile epoch/version/policy facts are revalidated immediately before context issuance.
- Contexts carry explicit `issued_at` and `expires_at` and are valid for at most five seconds, with expiry exclusive at the exact boundary.
- Profile disable, App Bot rotate/unpublish/delete, grant revoke/deny, and policy-version changes fail closed on the next request.
- Bank and grant policy versions must match the active profile policy; stale registry values cannot override authoritative database state.

## Out of Scope

- Gateway T020+, Hindsight, deployment, and external multi-service environment execution.

## Acceptance

- Fake-clock and sqlmock/fake-store tests prove the five-second boundary and immediate refresh/denial for lifecycle, credential epoch, grant, profile, and policy changes.
- Agent Memory, focused App Bot/Bot API, internal, compile-only, vet, i18n, and diff checks pass.
