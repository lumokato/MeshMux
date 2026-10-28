#!/bin/sh
# Collect read-only evidence for the MeshMux root core daemon.
#
#   sh packaging/macos/verify-core-daemon.sh
#
# Safe to run before login and after boot: it changes nothing. The output is
# the evidence used in the LS-190 acceptance record.
set -u

LABEL=com.meshmux.core
PLIST=/Library/LaunchDaemons/$LABEL.plist
ROOT='/Library/Application Support/MeshMux/core'
USER_DATA="$HOME/Library/Application Support/MeshMux"

echo "== time =="
date
echo "== boot =="
sysctl -n kern.boottime
uptime
echo "== login =="
who
echo "== plist =="
ls -l "$PLIST" 2>&1
/usr/libexec/PlistBuddy -c 'Print :ProgramArguments' "$PLIST" 2>&1
echo "== launchd =="
/bin/launchctl print "system/$LABEL" 2>&1 | /usr/bin/grep -E 'state =|pid =|runs =|last exit|program =|path =' 
echo "== processes =="
/bin/ps -axo pid,uid,lstart,command | /usr/bin/grep -E 'meshmux _supervise|mihomo|meshmux-tray' | /usr/bin/grep -v grep
echo "== root copies =="
ls -ld "$ROOT" "$ROOT/profiles" 2>&1
ls -l "$ROOT/meshmux.local.json" "$ROOT/profiles/darwin.yaml" "$ROOT/bin/mihomo" 2>&1
echo "== user copies (must not be the runtime input) =="
ls -l "$USER_DATA/meshmux.local.json" 2>&1
echo "== utun =="
ifconfig | /usr/bin/awk '/^utun[0-9]+:/{iface=$1} /198\.18\.0\.1/{print iface, $0}'
echo "== routes =="
netstat -rn | /usr/bin/grep -E '^0/1|^128\.0/1|^default.*utun'
echo "== probe =="
curl -s -o /dev/null -w 'generate_204=%{http_code}\n' --max-time 8 https://www.gstatic.com/generate_204
echo "== controller =="
curl -s --max-time 4 http://127.0.0.1:9090/version 2>&1
echo
