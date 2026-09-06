#!/usr/bin/env bash
set -euo pipefail
root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
case "$root" in /tmp/meshmux-verify-*/src) ;; *) echo "Refusing non-isolated test root" >&2; exit 1 ;; esac
export HOME="$root/../home"
export XDG_CONFIG_HOME="$HOME/config"
export XDG_CACHE_HOME="$HOME/cache"
export MESHMUX_HOME="$HOME/meshmux"
export GOCACHE="$HOME/go-build"
export GOPATH="$HOME/go"
export CGO_ENABLED=1
export GOTOOLCHAIN=local
export GOMAXPROCS=4
unset DISPLAY DBUS_SESSION_BUS_ADDRESS HTTP_PROXY HTTPS_PROXY ALL_PROXY
mkdir -p "$HOME" "$MESHMUX_HOME"
cd "$root"
go version
gcc --version | head -1
pkg-config --modversion gtk+-3.0 ayatana-appindicator3-0.1
go mod verify
timeout 900 go test -p 4 -count=1 ./...
timeout 900 go test -p 4 -race -count=1 ./...
go vet ./...
mkdir -p "$root/../artifacts"
go build -trimpath -o "$root/../artifacts/meshmux" ./cmd/meshmux
go build -trimpath -o "$root/../artifacts/meshmux-tray" ./cmd/meshmux-tray
"$root/../artifacts/meshmux" version
printf 'LINUX_VERIFICATION_PASSED\n'
