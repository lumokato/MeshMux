# MeshMux macOS (Intel x86_64)

This archive is the portable macOS amd64 application bundle. It targets Intel
Macs natively; Apple Silicon runs it under Rosetta 2. It contains:

- `meshmux`: CLI and headless runner (pure Go).
- `meshmux-tray`: menu bar tray application (Cocoa, CGO build).
- `bin/mihomo`: the core selected and checksum-verified by the release workflow.
- `bin/tailscaled`, `bin/tailscale`: the tailscale rebuild packaged by the
  release workflow (`tailscale-darwin-amd64-*.zip`).
- `bin/geoip.metadb` and `dashboard/`: bundled runtime assets.
- `meshmux.example.json`: template configuration.

## Install

1. Extract wherever you keep applications and clear the quarantine attribute
   macOS puts on files from a downloaded archive (the binaries are unsigned):

```sh
tar -xzf MeshMux-macos-amd64-v*.tar.gz -C ~/Applications
xattr -cr ~/Applications/meshmux-macos
```

2. Validate the configuration before the first start:

```sh
cd ~/Applications/meshmux-macos
./meshmux config-check
```

The command validates configuration completeness without printing provider
URLs, Tailnet auth keys, or WireGuard private keys.

3. Start the tray:

```sh
./meshmux-tray
```

The tray works in `~/Library/Application Support/MeshMux` (override with
`MESHMUX_HOME`): on first start it creates the local configuration from the
bundled example and seeds `bin/`, `dashboard/`, and the GeoIP database from
this bundle on demand. Quitting the tray stops the core and turns the system
proxy off.

## System proxy

The tray (and `./meshmux proxy on|off`) switches the HTTP/HTTPS proxy of every
enabled network service via `networksetup`. The original per-service values are
backed up in the data directory (`state/darwin-proxy-backup.json`) and restored
when the proxy is turned off; MeshMux only ever resets services that still
point at its own loopback port.

## Autostart

The tray toggle (or `./meshmux autostart on`) installs a per-user LaunchAgent
(`~/Library/LaunchAgents/com.meshmux.agent.plist`) that starts the tray at
login.

## TUN mode

macOS creates utun interfaces only for privileged processes. TUN profiles need
the core to run as root, which the tray cannot do — run the CLI with `sudo`
(`sudo ./meshmux run`) for TUN, or keep TUN off and use the system proxy.
