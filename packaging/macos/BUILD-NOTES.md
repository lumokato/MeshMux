# MeshMux macOS bundle — structure and build notes

This directory documents the macOS bundle assembled by the `macos` job in
`.github/workflows/release.yml`. `README.release.md` is shipped inside the
archive as `README-macOS.md`.

## Bundle layout

```
meshmux-macos/
  meshmux               CLI + headless runner; pure Go (CGO_ENABLED=0)
  meshmux-tray          menu bar tray; Cocoa via cgo (CGO_ENABLED=1)
  bin/mihomo            upstream mihomo darwin-amd64-compatible release
  bin/tailscaled        tailscale rebuild (tailscale-darwin-amd64-*.zip)
  bin/tailscale
  bin/geoip.metadb      verified against the upstream-published digest
  dashboard/            MetaCubeXD, downloaded and checksum-verified via the CLI
  meshmux.example.json  template configuration
  README-macOS.md, README.md, README.en.md, LICENSE, SECURITY.md,
  THIRD_PARTY_NOTICES.md
```

At runtime the tray changes its working directory to
`~/Library/Application Support/MeshMux` (`config.LocalDataDir`, overridable
with `MESHMUX_HOME`) and seeds `bin/`, `dashboard/`, and geoip from the bundle
directory (`config.Bundled*Path` points next to the executable). This is the
same model the Windows installer implements by copying into
`%LOCALAPPDATA%\MeshMux`; on macOS the seeding happens on demand at first
start, so the bundle can live anywhere.

## Why the tray is cross-compiled on an arm64 runner

- The tray links Cocoa through cgo (getlantern/systray), so it cannot build
  with `CGO_ENABLED=0` and needs a macOS toolchain — it cannot be produced on
  the Linux/Windows jobs.
- `macos-13`, the last Intel runner image, is retired (checked against
  actions/runner-images in October 2026); the remaining images are arm64
  (plus deprecated macos-14 x64, which shares the same retirement path).
- The arm64 runner's Xcode toolchain targets x86_64 with the universal macOS
  SDK, so the job cross-compiles the tray with
  `CGO_ENABLED=1 GOARCH=amd64 CC="clang -arch x86_64"`.
- The CLI needs no cgo and cross-compiles pure Go
  (`CGO_ENABLED=0 GOARCH=amd64`).
- The job verifies every shipped binary is `Mach-O 64-bit executable x86_64`
  with `file` instead of executing it, so it never depends on Rosetta 2 being
  installed. Dashboard provisioning uses a throwaway arm64 build of the CLI
  for the same reason.
- The macOS job builds MeshMux with a current Go (1.26.x), not the 1.22 the
  other jobs pin: Go 1.22's linker emits no LC_UUID load command, and the
  dyld of macOS 15.4+/26 aborts binaries without it ("dyld: missing LC_UUID
  load command" — first seen failing `go test` on a macOS 26 runner). The
  tailscale rebuild is unaffected because tailscale's own go.mod forces a
  newer toolchain via GOTOOLCHAIN; a Go 1.26 darwin build was verified to
  carry LC_UUID before pinning this.

If GitHub reintroduces an Intel image (or the target moves to arm64), prefer a
native runner over cross-compiling and update this note together with the
workflow.

## Upstream assets

- mihomo: `mihomo-darwin-amd64-compatible-$MIHOMO_UPSTREAM_VERSION.gz`,
  pinned by `MIHOMO_UPSTREAM_DARWIN_SHA256` in the workflow env (re-hash when
  bumping `MIHOMO_UPSTREAM_VERSION`).
- tailscale: `tailscale-darwin-amd64-*.zip` from the `tailscale-components`
  job (same rebuild as the other platforms, no wintun).
- `geoip.metadb`: rolling asset, verified against the digest the upstream
  release itself publishes.
- dashboard: pinned by `DASHBOARD_VERSION` / `DASHBOARD_SHA256` in the
  workflow env.

## Relation to real-machine acceptance

This pipeline produces and checksums the bundle, but nothing here executes the
tray or the core on macOS. Real-machine install/first-run/acceptance is a
separate step on the target machine.
