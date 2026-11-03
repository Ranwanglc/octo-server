# Agent Memory Bank Service

## Goal

Implement T018 by wiring the T016 logical Bank creation boundary to a transactional service using T017 V1 templates and durable gateway provisioning intents.

## Load-Bearing Behavior

- Logical creation stores a `provisioning` Bank with no fabricated physical Hindsight ID.
- Creation and its idempotent provisioning intent commit or roll back together.
- The active policy version and immutable V1 template defaults are selected deterministically.
- Disable changes logical state and enqueues a versioned disable intent without deleting grants, source data, or physical Banks.
- Existing policy and internal context paths deny non-active Banks immediately.
- Re-enable without a physical ID remains non-authorizing while a new provisioning intent is pending.

## Out Of Scope

- Gateway workers, retries, or synchronous Hindsight calls.
- Physical Bank deletion.
- T019 management API matrix expansion.

## Acceptance

- Focused transaction tests cover atomicity, rollback, canonical idempotency/conflict, nullable physical IDs, disable authorization denial, non-deletion, lifecycle intent uniqueness, re-enable safety, and optimistic concurrency.
- The isolated migration applies and reverses cleanly.
- Module/internal tests, vet, i18n checks, and `git diff --check` pass.
