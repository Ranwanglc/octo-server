---
type: Rule
title: Trust boundaries for adapters & external content
description: Inbound adapters must escape/validate at the boundary the caller cannot cross, and keep parity across adapters; never block one adapter while leaving a sibling open.
tags: ["security", "trust-boundary", "adapter", "webhook", "markdown-injection"]
timestamp: 2026-06-23T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: trust-boundary
tier: repo
priority: 90
load_bearing: true
inject_when:
  paths: ["modules/incomingwebhook/**/*.go", "modules/webhook/**/*.go", "modules/*adapter*/**/*.go", "pkg/richtext/**/*.go", "adapters/**"]
  touches: ["adapter", "webhook", "external-content", "trust-boundary", "escape", "markdown", "url-destination", "wire-contract"]
source: self
supersedes: []
---

# Trust boundaries for adapters & external content

Inbound adapters (incomingwebhook GitHub/WeCom, webhook push, voice/other
adapters) carry attacker-controlled content into native message payloads. The
rendering/escaping decision must be made at the boundary the **caller cannot
cross**, and must be **consistent across sibling adapters**.

## Rules

- **Escape at the right boundary.** Apply escaping/encoding only where a
  downstream caller cannot undo or bypass it. Do not push the responsibility
  onto a caller that has no way to enforce it. If the native render layer is the
  last place that controls the output contract, escape there — not in one adapter
  and hope the others did the same.
- **Adapter parity.** A defense added for one adapter (e.g. GitHub) must hold for
  every sibling adapter (WeCom, push, future ones). Never block adapter A while
  leaving adapter B open to the same input — divergent escaping across adapters
  is a vulnerability, not a feature.
- **URL destination breakout.** When emitting a markdown/link destination, an
  unescaped `)` (or other delimiter) can close the destination early and inject
  attacker markup. Percent-encode the destination; do not rely on naive string
  interpolation.
- **Markdown / richtext injection.** External text rendered through richtext
  blocks must not be able to forge native formatting or links. Validate block
  structure (count/shape) AND escape leaf text content.
- **Bound the payload.** Enforce explicit structural limits (e.g. block count)
  in addition to byte caps, so a well-formed-but-pathological payload cannot
  exhaust validation.

## Why load-bearing

Adapters are the primary externally-reachable attack surface. A
markdown-injection or URL-destination breakout reaches end users' clients; an
escaping gap that exists in one adapter but not its siblings is a real
cross-channel vulnerability.
