# MeshMux

MeshMux manages mihomo-based proxy, WireGuard, Tailscale, and mobile profile publishing on Windows desktops and Linux desktop/headless environments.

The Windows installer registers the core as an automatic system service that runs before sign-in. The service only reads a protected runtime snapshot under `ProgramData\MeshMux`; the editable user configuration remains in the existing local data directory. The tray starts after user sign-in and manages the current user's proxy, configuration page, and core controls. UAC is only required for installation, removal, or explicit service control, not during a normal boot.

## Features

- Tray control for starting, stopping, and restarting mihomo.
- Windows system proxy and TUN mode support.
- Browser setup page for subscription, Sub-Store, WireGuard, and Tailscale.
- Tailnet-only TCP/UDP port forwarding to local Windows services.
- Windows and mobile profile generation.
- Mobile profile publishing through Sub-Store Files.
- Installer bundle with `mihomo.exe`, `geoip.metadb`, and MetaCubeXD.
- Linux systemd core, loopback configuration service, and XFCE tray controls.

## Usage

1. Install and start MeshMux.
2. Open the setup page from the tray menu.
3. Fill in the proxy subscription, Sub-Store URL, backend name, and file name.
4. Import WireGuard configs or enable Tailscale when needed.
5. Add inbound mappings in the advanced page when Tailnet devices need to reach local services.
6. Save the config and generate Windows/mobile profiles.
7. Import the mobile profile link in an Android mihomo client.

## Tailnet inbound forwarding

MeshMux bundles a patched Mihomo core that listens only on the embedded tsnet node's Tailnet addresses and forwards configured TCP or UDP ports to local targets. Tailscale ACLs and Grants remain the access-control layer. The mappings are emitted only in the Windows profile; mobile profiles do not inherit them. With no mappings configured, existing outbound-only behavior is unchanged.

See the Chinese README for the full JSON example. The patched core is based on upstream `v1.19.29`; its binary and corresponding source are published as a [fixed MeshMux core asset](https://github.com/lumokato/MeshMux/releases/tag/mihomo-v1.19.29-meshmux.1). MeshMux CI downloads that asset, verifies its SHA-256, and does not rebuild Mihomo for every application release.

## Paths

Application:

```text
C:\Program Files\MeshMux
```

User data:

```text
%LocalAppData%\MeshMux
```

The user data directory stores local config, generated profiles, logs, and mihomo state.

Logs rotate automatically by size. `mihomo.out.log` and `mihomo.err.log` are limited to 8 MiB each with up to 3 backups; `meshmux.log` is limited to 2 MiB with up to 2 backups. Oversized legacy logs are cleaned before the core starts, and URLs, keys, tokens, and similar sensitive fields are redacted before log writes.

## Linux

Linux can run the persistent core with `meshmux run linux` and expose the configuration page on loopback with `meshmux serve`. The `packaging/linux` directory contains systemd units, an XFCE login entry, restricted sudoers rules, and the target-host installer. The tray is independent from the core: exiting it does not stop the proxy, and no tray process runs without a graphical session.

## License

MeshMux is licensed under MIT. Bundled components are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
