# MeshMux

MeshMux is a Windows tray tool for mihomo-based proxy, WireGuard, Tailscale, and mobile profile publishing.

## Features

- Tray control for starting, stopping, and restarting mihomo.
- Windows system proxy and TUN mode support.
- Browser setup page for subscription, Sub-Store, WireGuard, and Tailscale.
- Windows and mobile profile generation.
- Mobile profile publishing through Sub-Store Files.
- Installer bundle with `mihomo.exe`, `geoip.metadb`, and MetaCubeXD.

## Usage

1. Install and start MeshMux.
2. Open the setup page from the tray menu.
3. Fill in the proxy subscription, Sub-Store URL, backend name, and file name.
4. Import WireGuard configs or enable Tailscale when needed.
5. Save the config and generate Windows/mobile profiles.
6. Import the mobile profile link in an Android mihomo client.

## Paths

Application:

```text
%LocalAppData%\Programs\MeshMux
```

User data:

```text
%LocalAppData%\MeshMux
```

The user data directory stores local config, generated profiles, logs, and mihomo state.

## License

MeshMux is licensed under MIT. Bundled components are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
