# Change log

Change history for this repo's `.octospec/`, following the
[OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
change-log convention (§7). Newest first.

## 2026-06-22

- **Creation** — Added task `auth-lookup-interfaces` (`.octospec/tasks/auth-lookup-interfaces/`):
  PR-A2 of Stage A epic (octo-server #428). Adds `BotLookup` / `APIKeyLookup`
  consumer-defined interfaces in `modules/auth`, implements them in
  `modules/bot_api` and `modules/usersecret`, and locks the dependency
  direction with an in-tree `imports_test.go` guard. Journal:
  `.octospec/journal/shared/auth-lookup-interfaces.md`.
- **Creation** — Added task `modules-auth-skeleton` (`.octospec/tasks/modules-auth-skeleton/`):
  brief + context for PR-A1 (octo-server #428). Relocated `pkg/auth/{tokeninfo,parser}` and
  their tests to the new `modules/auth/` package; `pkg/auth/` becomes a Deprecated alias
  shim with a `aliases_test.go` guard. First step of the Stage A epic that culminates
  in `verify` / `verify-bot` / `verify-api-key` HTTP handlers moving out of
  `modules/user/api.go`. Journal: `.octospec/journal/shared/modules-auth-skeleton.md`.

## 2026-06-19

- **Update** — Adopted OKF v0.1 compatible frontmatter across all repo rules
  (`commit-style`, `error-handling`, `rate-limit`, `space-isolation`,
  `testing`): added `type`, `title`, `description`, `tags`, `timestamp`. The
  octospec orchestration fields are retained as OKF extension fields.
- **Update** — Bumped global inheritance pin to `octo-spec@1.1.0`.
- **Creation** — Added `.octospec/index.md` (human-readable rule catalog) and
  this `.octospec/log.md` change log.

## 2026-06-18

- **Creation** — octospec pilot scaffolding: rules `error-handling`,
  `rate-limit`, `space-isolation`, `testing`, `commit-style`; manifest, task
  templates, slash commands (PR #418).
- **Creation** — Dogfood task `member-list-name-fallback` (#344 → PR #420).

## 2026-06-19 (tooling)

- **Update** — Synced OKF-aware slash commands, workflow skill, and task brief
  template from octo-spec 1.1.0 so generated briefs/journals stay conformant.
