#!/usr/bin/env bash
set -euo pipefail

archive=${1:?usage: install.sh /path/to/meshmux-linux.tar.gz}
install_root=/home/codex/.local/lib/meshmux
data_root=/home/codex/.local/share/meshmux
config_root=/home/codex/.config/meshmux
autostart_root=/home/codex/.config/autostart
xsessionrc=/home/codex/.xsessionrc
backup_root=/home/codex/.local/share/meshmux-backups/$(date -u +%Y%m%dT%H%M%SZ)
stage=$(mktemp -d /tmp/meshmux-install.XXXXXX)

cleanup() {
  status=$?
  rm -rf -- "$stage"
  rm -f -- "$archive"
  exit "$status"
}
trap cleanup EXIT

test "$(hostname)" = mercury-linesky-sunzhitao
id codex >/dev/null
test -c /dev/net/tun
command -v jq >/dev/null
tar -xzf "$archive" -C "$stage"
bundle="$stage/meshmux-linux"
chmod 0755 "$bundle/meshmux" "$bundle/meshmux-tray" "$bundle/bin/mihomo"
test -x "$bundle/meshmux"
test -x "$bundle/meshmux-tray"
test -x "$bundle/bin/mihomo"
test -s "$bundle/bin/geoip.metadb"
test -s "$bundle/meshmux.local.json"
test -s "$bundle/meshmux-tray.desktop"
test -s "$bundle/meshmux-sudoers"

systemctl stop meshmux-web.service meshmux.service 2>/dev/null || true
occupied=$(ss -H -lntup | grep -E ':(2080|9090|1053|9088)([[:space:]]|$)' || true)
if [[ -n "$occupied" ]]; then
  printf 'MeshMux ports are already occupied:\n%s\n' "$occupied" >&2
  exit 1
fi

install -d -m 0700 -o codex -g codex "$backup_root"
if [[ -d "$install_root" ]]; then
  cp -a -- "$install_root" "$backup_root/install"
fi
if [[ -f "$config_root/meshmux.local.json" ]]; then
  install -m 0600 -o codex -g codex "$config_root/meshmux.local.json" "$backup_root/meshmux.local.json"
fi
for unit in meshmux.service meshmux-web.service; do
  if [[ -f "/etc/systemd/system/$unit" ]]; then
    cp -a -- "/etc/systemd/system/$unit" "$backup_root/$unit"
  fi
done
if [[ -f "$autostart_root/meshmux-tray.desktop" ]]; then
  cp -a -- "$autostart_root/meshmux-tray.desktop" "$backup_root/meshmux-tray.desktop"
fi
if [[ -f "$xsessionrc" ]]; then
  cp -a -- "$xsessionrc" "$backup_root/xsessionrc"
fi
if [[ -f /etc/sudoers.d/meshmux ]]; then
  cp -a -- /etc/sudoers.d/meshmux "$backup_root/meshmux-sudoers"
fi

new_root=/home/codex/.local/lib/meshmux.new
rm -rf -- "$new_root"
install -d -m 0755 -o codex -g codex "$new_root/bin"
install -m 0755 -o codex -g codex "$bundle/meshmux" "$new_root/meshmux"
install -m 0755 -o codex -g codex "$bundle/meshmux-tray" "$new_root/meshmux-tray"
install -m 0755 -o codex -g codex "$bundle/bin/mihomo" "$new_root/bin/mihomo"
install -m 0644 -o codex -g codex "$bundle/bin/geoip.metadb" "$new_root/bin/geoip.metadb"
rm -rf -- "$install_root"
mv -- "$new_root" "$install_root"
chown -R codex:codex "$install_root"

install -d -m 0750 -o codex -g codex "$data_root" "$data_root/bin" "$data_root/logs" "$data_root/profiles" "$data_root/providers" "$data_root/state" "$data_root/runtime"
touch "$data_root/logs/tray.log"
chown codex:codex "$data_root/logs/tray.log"
chmod 0600 "$data_root/logs/tray.log"
install -d -m 0700 -o codex -g codex "$config_root"
install -d -m 0700 -o codex -g codex "$autostart_root"
if [[ ! -f "$config_root/meshmux.local.json" ]]; then
  install -m 0600 -o codex -g codex "$bundle/meshmux.local.json" "$config_root/meshmux.local.json"
fi
install -m 0644 -o codex -g codex "$bundle/providers/main.yaml" "$data_root/providers/main.yaml"
install -m 0644 -o codex -g codex "$bundle/bin/geoip.metadb" "$data_root/geoip.metadb"
rm -rf -- "$data_root/dashboard"
cp -a -- "$bundle/dashboard" "$data_root/dashboard"
chown -R codex:codex "$data_root/dashboard"

chmod 0644 "$bundle/meshmux.service" "$bundle/meshmux-web.service"
systemd-analyze verify "$bundle/meshmux.service" "$bundle/meshmux-web.service"
chmod 0440 "$bundle/meshmux-sudoers"
visudo -cf "$bundle/meshmux-sudoers"

(
  cd "$data_root"
  sudo -u codex env MESHMUX_HOME="$data_root" "$install_root/meshmux" test linux -config "$config_root/meshmux.local.json"
)

install -m 0644 "$bundle/meshmux.service" /etc/systemd/system/meshmux.service
install -m 0644 "$bundle/meshmux-web.service" /etc/systemd/system/meshmux-web.service
install -m 0440 "$bundle/meshmux-sudoers" /etc/sudoers.d/meshmux
install -m 0644 -o codex -g codex "$bundle/meshmux-tray.desktop" "$autostart_root/meshmux-tray.desktop"

mixed_port=$(jq -er '.ports.mixed | select(type == "number" and . >= 1 and . <= 65535) | floor' "$config_root/meshmux.local.json")
session_proxy_url=http://127.0.0.1:${mixed_port}
session_proxy_block=$(cat <<EOF
# BEGIN MESHMUX SESSION PROXY
export http_proxy=$session_proxy_url
export https_proxy=$session_proxy_url
export all_proxy=$session_proxy_url
export HTTP_PROXY="\$http_proxy"
export HTTPS_PROXY="\$https_proxy"
export ALL_PROXY="\$all_proxy"
export no_proxy=localhost,127.0.0.1,::1
export NO_PROXY="\$no_proxy"
# END MESHMUX SESSION PROXY
EOF
)
session_tmp=$(mktemp /tmp/meshmux-xsessionrc.XXXXXX)
if [[ -f "$xsessionrc" ]]; then
  awk '
    $0 == "# BEGIN MESHMUX SESSION PROXY" { managed=1; next }
    $0 == "# END MESHMUX SESSION PROXY" { managed=0; next }
    !managed { print }
  ' "$xsessionrc" >"$session_tmp"
fi
if [[ -s "$session_tmp" ]]; then
  printf '\n' >>"$session_tmp"
fi
printf '%s\n' "$session_proxy_block" >>"$session_tmp"
install -m 0600 -o codex -g codex "$session_tmp" "$xsessionrc"
rm -f -- "$session_tmp"

systemctl daemon-reload
systemctl enable --now meshmux.service meshmux-web.service
sleep 3
systemctl is-active --quiet meshmux.service
systemctl is-active --quiet meshmux-web.service
