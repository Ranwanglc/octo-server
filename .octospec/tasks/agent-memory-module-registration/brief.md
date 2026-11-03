---
type: Task
title: "Task: agent-memory-module-registration"
description: Register the Agent Memory control-plane module behind a default-off runtime switch.
tags: ["agent-memory", "module", "migration", "feature-gate"]
timestamp: 2026-07-23T11:25:00Z
slug: agent-memory-module-registration
source: spec-kit
---
# Task: agent-memory-module-registration

## Goal

Register Agent Memory migrations and lifecycle through the native module system only when `MEMORY_CONTROL_ENABLED` is enabled.

## Load-bearing behavior

- The switch defaults false and disabled registration exposes no migrations, routes, service, or lifecycle hooks.
- Enabled registration deterministically exposes the existing Agent Memory migration and service boundary.
- App Bot synchronization fails closed until durable deletion and credential epoch facts are supplied.

## Out of scope

- Management or internal API routes.
- App Bot schema or historical migration changes.
- Inventing deletion tombstones or credential epoch persistence.

## Acceptance

- Disabled registration changes no legacy runtime behavior.
- Enabled registration exposes one deterministic migration set and an empty route scaffold.
- Missing App Bot governance facts prevent profile synchronization.
