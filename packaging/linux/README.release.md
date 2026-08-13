# MeshMux Linux amd64

This archive is the portable Linux amd64 application bundle. It contains:

- `meshmux`: CLI and headless runner.
- `meshmux-tray`: desktop tray application.
- `bin/mihomo`: pinned `v1.19.29-meshmux.1` core.
- `bin/geoip.metadb` and `dashboard/`: bundled runtime assets.
- `meshmux.local.json`: bootstrap configuration.
- `meshmux.service`, `meshmux-web.service`, `meshmux-tray.desktop`, and `meshmux-sudoers`: deployment examples for the existing `codex` account layout. Review and adjust their user and paths before installing them on another machine.

Run `./meshmux config-check -config /path/to/meshmux.local.json` before starting the core. The command validates configuration completeness without printing provider URLs, Tailnet auth keys, or WireGuard private keys.
