# Development and Version Authority

## Source and build inputs

Use the current application checkout, not extracted installers or old build directories. cmd/, internal/, templates/ and assets/ are application source; .github/workflows/release.yml and installer/MeshMux.iss define packaging. packaging/linux/ owns Linux deployment examples.

MeshMux 0.3.1 pairs with Mihomo v1.19.29-meshmux.2. The separate Mihomo checkout is maintained source, not obsolete output. Its corresponding source archive and both platform binaries are published in the fixed core release. Workflow hashes and updater pins identify exact assets; version strings alone do not establish provenance.

Do not execute MeshMux.exe for a version query: it launches the tray. Use the exact meshmux-cli.exe path and its version command. Installed paths, process ownership and hashes are runtime evidence; a README or old installer is not.

## Verification

Run from the application root:

~~~text
go test -count=1 ./...
go vet ./...
go mod verify
~~~

The manager supports Go 1.22. Windows ordinary CLI/tray builds do not require a C compiler. Race testing requires CGO and a supported C compiler. The Linux GTK tray also requires GCC, GTK3 and Ayatana AppIndicator development packages. Build the separate Mihomo core with Go 1.26.4 and with_gvisor, CGO_ENABLED=0, GOAMD64=v1; do not apply the manager's Go baseline to the core dependencies.

scripts/test-linux.sh runs full/race tests, vet and CLI/tray builds as an unprivileged user in /tmp/meshmux-verify-*/src. Use isolated HOME, caches and MESHMUX_HOME, without a display or session bus. The script never starts a tray, core or installed service. Linux race covers shared/Linux code, not Windows-specific code.

Keep verification binaries in .verification/. Application relative paths resolve from the config directory, except an explicit absolute Linux MESHMUX_HOME selects the existing runtime root. CI stages its example config beside downloaded components.

## Historical artifacts

build/ and release/ are disposable outputs, never implicit current-version evidence. Previous outputs were archived and individually SHA-256 verified before removal. .local-history/ holds the local archive and per-file manifests, is ignored by Git, and must never be packaged or executed automatically. Git history preserves old tracked code.

~~~powershell
pwsh -NoProfile -File scripts/archive-local-builds.ps1
pwsh -NoProfile -File scripts/archive-local-builds.ps1 -Apply
~~~

The default is preview. Apply accepts only the exact build/ and release/ children, rejects running binaries and reparse points, verifies archive/source hashes, then removes those exact directories. No scheduled cleanup exists. Deliberate historical investigation must extract into a separate inspection directory, never over source, application or user-data roots.

## Release boundary

A source edit is not a deployment. Choose an explicit version, pass both platform workflows, inspect asset hashes and publish only that source/core pair. Installer compilation requires MESHMUX_VERSION; CI refuses stale output directories and checks native command exits. Release versions must be numeric major.minor.patch values.

The workflow pins core binaries, corresponding source, dashboard version/hash and the GeoIP content hash. Mutable upstream data may later make a rebuild fail its checksum; refresh pins deliberately, never silently accept different bytes. Application downloads require an explicit sha256, a fixed core pin, or a GitHub asset SHA-256 digest. Older custom assets may need an explicit pin. Limits: 512 MiB download, 1 GiB expanded, 20,000 entries.

## Acceptance boundaries

Windows and Linux full tests, vet, manager builds and Linux race passed during the review. The deployed manager passed Windows cold boot and Linux LXC reboot, with existing Tailnet identities preserved and real SSH/proxy traffic verified. These are manager deployment results, not proof of every fresh installation or every network destination.

Windows race, sleep/resume and interactive Linux desktop acceptance remain separate checks. Local deployment reports contain machine/backup details and are ignored; public release notes describe product behavior only. Keep protected rollback backups until the applicable runtime acceptance is complete.
