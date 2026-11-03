# Agent Memory Grant Replacement

## Goal

Implement T012 authorized replacement of an Agent memory profile's grants.

## Load-Bearing Behavior

- T011 platform/Space management authorization and hidden-resource anti-enumeration remain authoritative.
- Requests require an expected profile version and non-empty audit reason.
- Only known capabilities, effects, principal types, logical Bank IDs, and valid time windows are accepted.
- Every Bank and principal must be eligible for the managed profile scope; client ownership claims and raw Hindsight/name/prefix identifiers are never trusted.
- Replacement is atomic and advances the profile version through optimistic concurrency.
- T008 explicit deny continues to override allow.

## Out of Scope

- Audit event persistence, internal Agent authentication, and Bank CRUD.

## Acceptance

- Focused tests cover platform/Space administrators, member denial, cross-scope denial, malformed grants, deny precedence, rollback, and version conflicts.
- Module/internal tests, vet, i18n checks, and git diff checks pass.
