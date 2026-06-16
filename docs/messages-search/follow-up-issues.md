# messages_search — Follow-up issue backlog

> Created: 2026-06-13 during the P0 visibility + authz fix that landed
> in PR #361's update. These items are intentionally out of scope for
> the P0 fix (per the spec's "minimal-fix" guard rail), but each is a
> real residual gap worth tracking.

## 1. Extract a shared `pkg/messagevisibility`

**What**: lift `modules/messages_search/visibility.go::filterVisible` (and the
visibilityProbe interface around it) into `pkg/messagevisibility/` so the
read-side `modules/message/api_channel_files.go::filterMessages` can move
onto the same primitive. Today the two implementations are deliberately
parallel (search vs. read), and any divergence is a parity bug magnet.

**Why not now**: the spec for the P0 fix explicitly capped the change at
"option (a) minimal — keep new code inside modules/messages_search". An
extraction touches modules/message and rewires its callers; out of scope
for a security fix.

**Owner**: TBD. Tag: `tech-debt`, `messages_search`, `parity`.

## 2. Extract a shared `pkg/channelaccess`

**What**: same idea but for the channel-membership gate. The new
`checkP2PAccess` / `checkGroupAccess` / `checkThreadAccess` in
`modules/messages_search/authz.go` largely duplicates the patterns in
`modules/message/api_channel_files.go:160-211` (read path) and the
thread/group disband fail-closed templates. A shared helper would
prevent the next reviewer from having to spot the same disband-vs-member
ordering trap a fourth time.

**Why not now**: same scoping rule.

**Owner**: TBD. Tag: `tech-debt`, `authz`, `parity`.

## 3. OS mapping v1.10 — `revoked` partial-update + `isDeleted` field

**What**: see `docs/messages-search/v1.8-opensearch-mapping.md` § 5.
indexer needs to (a) issue a partial update setting `revoked=true` on
existing OS docs when the corresponding `message_extra.revoke` flips,
and (b) add an `isDeleted` boolean to the mapping that mirrors
`message_extra.is_deleted` so OS DSL can pre-filter the global delete
case.

**Why not now**: indexer change, not search-server change. Tracking
here so the search team's post-filter can shrink to a residual
defence-in-depth check once the indexer side lands.

**Owner**: indexer team (Niko). Tag: `indexer`, `mapping`, `messages_search`.

## 4. Sync the `/v1/messages/_search*` and `/messages/channel-files`
read paths onto a single visibility/authz pipeline

**What**: today the search handlers and the read handlers share signal
sources (message.IService, group.IService, user.IService) but each
inlines the orchestration. Items 1 and 2 above feed into this larger
goal: one pipeline, one set of fail-closed invariants, one place to add
the next channel type / status check.

**Why not now**: needs items 1 + 2 first, plus a design pass for the
combined surface (pagination semantics differ between the two; e.g.
read paths don't oversample).

**Owner**: TBD. Tag: `architecture`, `cleanup`.
