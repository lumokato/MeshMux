#!/bin/sh
# Remove the MeshMux root core LaunchDaemon and return to manual start mode.
#
#   sudo packaging/macos/uninstall-core-daemon.sh
#   sudo packaging/macos/uninstall-core-daemon.sh /Applications/MeshMux.app/Contents/MacOS/meshmux
#
# The protected copies under /Library/Application Support/MeshMux/core are kept
# in a recovery directory. After this the tray starts the core as before (TUN
# then needs the old administrator authorization).
set -eu

CLI=${1:-/Applications/MeshMux.app/Contents/MacOS/meshmux}

if [ "$(id -u)" -ne 0 ]; then
	echo "需要管理员权限：sudo $0" >&2
	exit 1
fi
if [ ! -x "$CLI" ]; then
	echo "找不到可执行的 MeshMux CLI: $CLI" >&2
	exit 1
fi

PLAN=$("$CLI" core-service plan uninstall)
printf '%s\n' "$PLAN" | /bin/sh
