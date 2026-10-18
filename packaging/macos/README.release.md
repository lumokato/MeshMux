# MeshMux macOS (Intel x86_64)

The archive contains a standard **MeshMux.app** menu bar application. Intel Macs
run it natively; Apple Silicon requires Rosetta 2. It is ad-hoc signed, not
Developer ID signed or notarized, so Gatekeeper may require user approval.

## Install and open (no Terminal needed for normal use)

1. Expand `MeshMux-macos-amd64-v*.tar.gz` in Finder.
2. Drag **MeshMux.app** into **Applications** (or your user's `Applications`).
3. Double-click MeshMux, or find it in Spotlight. On first launch, use macOS
   Privacy & Security → Open Anyway if Gatekeeper blocks this unsigned-by-Developer-ID build.
4. MeshMux appears in the **menu bar**, not the Dock. Choose **打开配置页面**
   to open its browser-based settings. Existing subscriptions and data remain in
   `~/Library/Application Support/MeshMux`.

Do not launch the copy from inside an unexpanded archive. When upgrading an
existing running copy, quit MeshMux using its menu first, then replace only the
app. Never delete Application Support/MeshMux to upgrade. Quitting MeshMux stops
its user-mode mihomo core and restores the system proxy it owns. The explicitly
installed Tailnet LaunchDaemon is an independent network service: quitting the UI
keeps Tailnet connected. Use **停止服务（开机禁用）** to stop it and disable next-boot
startup; **启动服务（开机启用）** reverses that choice.

## Application layout

- `Contents/Info.plist`: application identity, menu-bar mode and icon metadata.
- `Contents/MacOS/meshmux-tray`: desktop entry point.
- `Contents/MacOS/meshmux`: CLI for advanced/diagnostic use.
- `Contents/Resources/MeshMux.icns`: application icon.
- `Contents/Resources/bin/`: mihomo, tailscale, tailscaled, GeoIP database.
- `Contents/Resources/dashboard/`: bundled MetaCubeXD.
- `Contents/Resources/meshmux.example.json`: fresh-user template.

The app is read-only at runtime. Configuration, generated profiles, component
updates, logs and Tailnet identity stay in Application Support (override with
an explicit `MESHMUX_HOME`). Duplicate new-version tray processes for the same
runtime root exit without stopping the existing one. Quit an older portable
tray before first launching this app: older versions do not hold the tray lock.

## System proxy and login startup

The menu toggles system HTTP/HTTPS proxies via `networksetup` with per-service
backup and restore. Login startup is optional and **off unless enabled by the
user**. The tray's autostart toggle registers a per-user LaunchAgent pointing to
the tray inside the app. Keep the app in its installed location; after moving
it, toggle autostart off and on again. A one-time launch is not login startup.

## TUN and Tailscale boundaries

A normal user-mode tray cannot create privileged utun interfaces. Keep mihomo
TUN off unless you have separately configured a privileged runner. Tailnet has
its own narrowly scoped service, not a root copy of the UI or user configuration.

In the authenticated settings page choose **安装/修复 Tailnet 服务**. macOS requests
administrator consent; cancellation fails visibly without automatic retries.
The app verifies the Intel CLI/daemon against its signed bundle manifest, stages
root-owned copies, and checks hashes again before running them. No silent download
or automatic authorization occurs. Missing/corrupt resources require a complete
[MeshMux release](https://github.com/lumokato/MeshMux/releases). These binaries are
MeshMux rebuilds of the official upstream tag, **not** official Tailscale-signed
macOS installer binaries. For a separately managed official Tailscale installation
use https://tailscale.com/download/mac; MeshMux does not modify that installation
or invent a raw macOS CLI zip URL.

The MeshMux service uses only `/Library/Application Support/MeshMux/Tailscale`:
root-owned executable files, a 0700 state directory and a dedicated socket below
a root-owned 0700 `ipc` directory. Only the installing local user gets a search
ACL on `ipc`; other users cannot reach the socket even though tailscaled creates
its socket with broad mode bits. Tailscale itself still enforces privileged write
operations: login/down/start/stop/restart request administrator consent. No AuthKey
is passed on a process command line. The login action returns a browser URL when
registration is needed, and does not force reauthentication of an existing identity.

The fixed launchd label is `com.meshmux.tailscaled`. Installation preserves an
existing protected identity; a recognized legacy MeshMux root identity is copied
only when no protected identity exists. Empty templates never replace identities.
Each explicit install/update keeps a protected `recovery-<timestamp>` containing
the previous service definition, binaries and identity snapshot. Installation
checks refuse symlinks, unsafe ownership, writable ancestors and unrelated jobs.
Ordinary launches do not install, upgrade, reset identity or override a disabled
service. Component upgrades require another explicit install/repair action.

For rollback stop this MeshMux job, restore its previous plist/binaries from the
printed protected recovery directory, and bootstrap that plist. The original
legacy identity is left in place. Do not restore state over a running daemon.
Keep recovery directories until real network and identity acceptance is complete;
MeshMux does not automatically delete them. No global Tailscale job is stopped.
A Running state and assigned IP are not proof of peer connectivity; test actual
Tailnet traffic separately. NeedsMachineAuth requires administrator approval.

## Build

Build the tray and CLI with a current Go toolchain, stage the pinned/checksummed
components and template into a portable payload, then run:

```sh
python3 packaging/macos/test_build_app.py
python3 packaging/macos/build-app.py --payload /path/to/staged-payload \
  --output /path/to/new/MeshMux.app --version 0.3.9
# The builder signs executables, records their digests, then signs the outer app.
# Do not re-sign nested binaries after creating the manifest.
codesign --verify --deep --strict /path/to/new/MeshMux.app
```

The builder refuses existing destinations, missing/non-executable/wrong-architecture
components and linked resources. It copies an explicit allowlist, not runtime
configuration, state, logs or arbitrary files from the payload. GitHub Actions
uses the same builder; local candidates are not released CI artifacts.
