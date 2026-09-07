# Windows installation incident: 0.3.1

## Release status

Do not install 0.3.1. The GitHub release has been marked prerelease with an installation warning after a reported Windows service activation and rollback failure. The user's affected machine was restored to 0.2.0. Do not request another upgrade or Tailnet login reset to reproduce this incident.

## Evidence and limits

The reported service-command error says both new activation and previous-snapshot restart failed to reach Running. It does not identify the first failing subsystem. The protected service.log and mihomo logs, plus SCM exit codes, are needed to distinguish permission, executable, configuration, listener and network failures.

The prior cold-boot acceptance replaced only manager binaries and kept the existing core and identity. The published installer overwrote the complete app/core pair. Successful CI compilation, checksum verification and that manager-only deployment were not adequate acceptance for another machine's installer upgrade. The previous formal release decision was incorrect.

## Confirmed defects

- TUN authorization used net session, which depends on LanmanServer as well as privilege. A privileged process could be rejected when that unrelated service is stopped. Changed to the Windows process token; this is a plausible failure path, not a confirmed diagnosis of the affected machine.
- Installer PrepareToInstall stops the installed service, then Files overwrites the executable/core. Runtime rollback restores configuration files only, not the previous executable/core. A repeated failed start is therefore not proof that the previous working version was restored.
- Failed service starts could leave a pending/recovering writer active while the snapshot was restored. Recovery now requires a successful stop first.
- SCM waits discarded the observed stopped state and exit codes. Failures now return these immediately, with bounded redacted log tails labeled as potentially including previous attempts.
- Tray core failures and manual stops left the user's enabled system proxy pointing to an unavailable listener. Local proxy enabling now requires a listener; repeated failed tray checks disable only the exact MeshMux loopback proxy. This handles that proxy setting, not every possible TUN/DNS route failure, and is not a full proxy preference restoration transaction.
- Windows service execution overrides the core path to the bundled executable, while component downloads target the user directory. A successful component download does not update the service runtime.
- Default core selection pinned an old fork indefinitely. Upstream v1.19.30 includes Tailnet network-recovery and TLS fixes. Artifact hashes must identify a release, not prevent supported stable updates. Upstream still lacks the custom inbound-forwarding implementation, so changing the URL alone would silently remove a retained feature.

## Required before another formal release

1. Transactional installer upgrade: stage and validate the complete candidate, retain the previous executable/core/service definition and protected snapshot, stop writers, activate, and restore the complete known-good pair on failure. Do not publish a config-only rollback as full rollback. A failed installation must leave a usable configuration/recovery surface.
2. User-context network recovery: cover installer abort, absent tray, core crash, failed restart and uninstall. Preserve other proxies/PAC, do not alter another user's HKCU under over-the-shoulder elevation, and test TUN route/DNS cleanup separately. No forced relogin or identity replacement.
3. One effective update path: follow supported current stable upstream by default, port required inbound functionality, validate generated configuration, promote only a verified core into the protected service-owned location, restart and revert binary/state selection on failed health checks. Preserve explicit user version pins.
4. Disposable Windows acceptance: fresh install, upgrades from 0.2.0 and 0.3.x, failed executable/configuration, occupied ports, disabled LanmanServer, rejected elevation, installer cancellation, failed rollback and reboot. Test exact release artifacts, not only source or manager-only copies. Do not use a user's working networking stack as the test fixture.

## Current patch boundary

Local Go tests and vet pass for the initial privilege, diagnostics and tray-proxy recovery patches. They have not been installed on either user workstation. Full installer transactions and the protected update path are not implemented or accepted yet. No replacement release is authorized by these local test results.
