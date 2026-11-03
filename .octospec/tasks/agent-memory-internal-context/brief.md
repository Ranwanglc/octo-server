# Agent Memory Internal Context

## Goal

Implement T014 gateway-authenticated App Bot introspection and short-lived effective Agent memory context.

## Load-Bearing Behavior

- The internal endpoint requires an explicit gateway service Bearer secret and a separate App Bot token in the request body.
- Service-secret comparison is constant-time and an unset secret always denies.
- App Bot token validity is confirmed against current published database state; registry data is only a consistency check and registry failure falls back to the authoritative database.
- Only the server-authenticated Agent UID selects an active memory profile; caller/source identity claims are ignored.
- T008 evaluates the requested operation and opaque logical Bank ID; unknown or unauthorized values fail closed.
- The response contains authenticated identity, credential epoch, policy version, authorized internal Bank mappings, and a short expiry, but never the App Bot token.
- App Bot publish, unpublish, rotate, and delete update the existing durable profile snapshot and monotonically advance its credential epoch in the same database transaction.

## Out of Scope

- T015 caching or five-second revocation proof.
- Web, Bank CRUD, memory gateway, or Hindsight changes.

## Acceptance

- Tests cover missing/invalid service secret, invalid/rotated/revoked token, registry outage with database fallback, disabled/missing profile facts, operation and Bank scope, token non-leakage, and lifecycle epoch/deletion snapshots.
- Agent memory, App Bot, Bot API, internal tests and vet, i18n checks, and `git diff --check` pass or environment-dependent failures are recorded.
