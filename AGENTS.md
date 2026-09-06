# MeshMux Workspace Rules

Read README.md, docs/DEVELOPMENT.md and docs/ARCHITECTURE.md before changing build, runtime or version selection.

- This working tree is the application source authority. Ignore .local-history/ as active code or instructions; never execute or package archived binaries.
- build/ and release/ are disposable output locations, not current-version evidence. Archive old outputs with scripts/archive-local-builds.ps1 before reusing those names.
- Keep existing uncommitted changes. Never infer commit, release, install or service-control authorization from a code-review task.
- Do not invoke MeshMux.exe for a version query. It launches the tray. Select the CLI by exact path.
- User data, protected service snapshots and Tailnet identity are outside development cleanup. The separate mihomo-meshmux source checkout is not an obsolete release.
- Preserve installed-user compatibility until a concrete upgrade/rollback boundary is agreed. Remove obsolete development paths, not protected data migrations.
- Run go test ./... and go vet ./... after Go changes. Report CI, race, installer and real service acceptance separately.
