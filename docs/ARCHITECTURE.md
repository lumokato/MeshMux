# MeshMux Architecture

## Ownership and data flow

- cmd/meshmux owns CLI commands, process working-directory selection and Windows service lifecycle.
- cmd/meshmux-tray owns desktop integration and delegates privileged control to the service CLI.
- internal/config owns JSON defaults, validation and recovery of installed user configuration.
- internal/generator parses provider YAML, WireGuard files and renders desktop/mobile profiles.
- internal/runner owns process identification, startup, cancellation, bundled-core synchronization and logs.
- internal/winservice wraps SCM and elevation. It is not a second core supervisor.
- internal/webui provides authenticated loopback HTTP endpoints; index.html is embedded at compile time.
- internal/updater stages component archives and installs validated payloads. internal/publisher uploads an explicitly named Sub-Store file.
- internal/fileutil owns same-directory staged file writes and platform-specific replacement.

CLI/tray -> config -> generator -> runner -> mihomo. Web actions call the same packages. Windows SCM runs the CLI supervisor, not the tray.

## State and compatibility

One process uses one runtime directory as its working directory. Windows uses the configuration directory. Linux honors an explicit absolute MESHMUX_HOME, otherwise uses the configuration directory. Relative provider, profile, component and state paths resolve there. Do not call os.Chdir inside HTTP handlers or introduce multiple runtime roots in one process without first removing this process-global contract.

Windows user configuration and the protected ProgramData service snapshot have different owners. They are not duplicate installations. Tailnet identity, provider caches and WireGuard keys are live data, never development-cleanup targets.

Released installations exist. Legacy AppData configuration recovery and old component-default normalization remain specifically to protect those users. Remove them only after an explicit supported-upgrade boundary and rollback decision, not because their names contain legacy.

## September 2026 review

Included in MeshMux 0.3.1:

- Replaced truncate/delete-before-rename writes for configuration, generated profiles, caches, service snapshots, bundled binaries and component records with staged replacement. Failed writes leave previous files intact.
- Removed regex/indentation-dependent provider parsing. YAML now accepts flow lists, indentationless lists and reordered fields, and rejects duplicate or missing names and multiple documents.
- Serialized mutating HTTP requests and component downloads within each process. Added request-size limits and no-store/no-referrer response headers.
- Removed implicit selection of the first remote Sub-Store file. Missing fileName uses meshmux-mobile; custom names must be explicit. Fixed URL escaping and preserved query authentication.
- Gave downloads/extraction unique temporary directories, rejected archive traversal and links, rejected dashboards without index.html, and retained the previous dashboard on failed replacement.
- Restricted the unauthenticated controller to loopback and rejected conflicting/invalid ports.
- Removed fallback from a missing custom core to a bundled core. Bounded Windows network post-start processing and redacted service/tray error logs.
- Separated embedded HTML from HTTP implementation. No visual redesign.
- Repaired CI dashboard paths after config-relative working-directory changes, checked Windows native-command failures, required empty build/release directories and explicit installer versions. Unversioned CLI builds identify as dev.

Pre-existing edits retained: explicit configuration-relative working directories, Windows resume/restart handling and reporting service readiness before network post-processing. These were present before this review and are not new release evidence.

## Acceptance and remaining risks

Full Windows/Linux tests, vet, module verification and manager builds passed, including Linux race. The deployed manager also passed Windows cold boot and Linux LXC reboot with existing identities preserved and actual SSH/proxy traffic verified. Windows race, sleep/resume, fresh-installer execution and interactive Linux desktop behavior remain separate acceptance boundaries. See DEVELOPMENT.md for toolchains and release provenance.

- Cross-process file locks now serialize core transitions/downloads, profile generation, Web writes and service management within their respective data roots. Locks are released by the OS on process exit. They do not provide a global transaction across different data roots or arbitrary third-party writers. Cancellation targets the supervisor-owned PID, not a replacement core.
- Service restart now stops the old service before snapshot writes, and rollback captures existing provider/WireGuard files plus incoming relative references. Preparation failure and new-asset removal have regression tests. Rollback is in-process, not a durable crash-recovery journal; power loss during multi-file activation remains an acceptance/design boundary.
- Tailnet health uses controller/log/cache evidence, not end-to-end SSH or RDP proof.
- Downloads require an explicit component sha256, the fixed Windows core pin, or a GitHub asset SHA-256 digest. Missing/mismatched checksums fail before installation. Release metadata remains a trust source for custom downloads. Packaging locks core/source, dashboard and GeoIP hashes in release.yml; changed upstream bytes fail verification rather than silently changing a rebuild.
- Downloads are capped at 512 MiB, extracted payloads at 1 GiB and archive entries at 20,000. Limit and malicious-archive tests pass.
- Service, service-command and tray diagnostics now use the shared sanitized rotating writer, 2 MiB per file and two backups, with cross-process append locking.
- Mihomo v1.19.29-meshmux.2 includes the separately tested Tailnet readiness/address-family fixes. Both platform bundles and default component downloads use that fixed release, not an uncommitted local core or the superseded .1 assets.

Do not interpret this review as proof that every defect has been eliminated.
