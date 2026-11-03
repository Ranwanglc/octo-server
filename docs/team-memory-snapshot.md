# Team memory control-plane snapshot

This branch preserves the existing local Octo Agent Memory implementation as of
2026-11-03. Its base is `47d759c9b1ec66c2f1f7fb2103745255826143a8`; it has not
been rebased onto the current upstream main branch.

The snapshot includes `modules/agent_memory`, Robot authority and credential
epoch migrations, module registration, error codes, translations, and the
existing implementation task notes. The deployment copy based on `4442db4`
contains the same `modules/agent_memory` files.

Companion branches:

- Gateway and specifications: `Muti-Agent-Mem/octo-graph-memory`,
  `feat/team-memory-control-plane-20261103`.
- Web: `Ranwanglc/octo-web`, `feat/team-memory-control-plane-20261103`.

Validation performed for this snapshot:

```sh
go test ./modules/agent_memory -count=1
go test ./modules/robot ./modules/botfather -run '^$' -count=1
go vet ./modules/agent_memory
make i18n-extract-check i18n-lint
```

The Robot/BotFather command is a compilation check. The memory tests use
synthetic fixtures and SQL mocks; no live database migration was executed.
The feature retains its existing default-disabled configuration. Publishing
this branch does not imply deployment or complete production acceptance.
