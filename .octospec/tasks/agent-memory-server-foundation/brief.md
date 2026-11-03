---
type: Task
title: "Task: agent-memory-server-foundation"
description: Add test fixtures and a reversible, isolated control-plane schema for Agent Memory.
tags: ["agent-memory", "migration", "space-isolation", "testing"]
timestamp: 2026-07-23T10:49:00Z
slug: agent-memory-server-foundation
source: spec-kit
---
# Task: agent-memory-server-foundation

## Goal

Provide T005 test-only Space/Agent/Bank/grant fixtures and the T006 data-model migration without registering runtime routes or changing historical App Bot migrations.

## Load-bearing behavior

- Cross-Space negative fixtures keep Space A/Agent A distinct from Space B/Agent B.
- The migration creates only Agent Memory control-plane tables and references.
- The rollback drops those tables in foreign-key-safe reverse order.
- Existing `app_bot` schema and data are not altered.

## Out of scope

- Runtime module registration, routes, services, or policy enforcement.
- Changes to existing `modules/app_bot/sql/` migrations.
- Applying migrations to shared or deployment databases.

## Acceptance

- `modules/agent_memory` tests load the fixtures and validate the cross-Space denial case.
- The migration parser finds ten forward and ten rollback statements.
- Static guards confirm all specified entities have create/drop statements and `app_bot` is not altered or dropped.
