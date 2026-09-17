//go:build windows

package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

// postStartNetwork clears the TUN adapter's DNS servers when DNS is disabled in
// the configuration. Route installation is left to mihomo's own auto-route
// handling; MeshMux no longer installs routes for another tunnel's address
// space.
func postStartNetwork(parent context.Context, cfg *config.Config) error {
	if cfg == nil || !cfg.TUN.Enabled || !dnsDisabled(cfg) {
		return nil
	}
	script := buildPostStartNetworkScript()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	hideWindow(cmd)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text != "" {
		appendRunnerLog("网络后处理: %s", text)
	}
	if err != nil {
		if text != "" {
			return fmt.Errorf("%w: %s", err, text)
		}
		return err
	}
	return nil
}

func dnsDisabled(cfg *config.Config) bool {
	return cfg.DNS.Enabled != nil && !*cfg.DNS.Enabled
}

func buildPostStartNetworkScript() string {
	return `
$ErrorActionPreference = 'Continue'
$alias = 'Meta'

$adapter = $null
$tunIp = $null
for ($i = 0; $i -lt 60; $i++) {
  $adapter = Get-NetAdapter -Name $alias -ErrorAction SilentlyContinue
  $tunIp = Get-NetIPAddress -InterfaceAlias $alias -AddressFamily IPv4 -ErrorAction SilentlyContinue |
    Where-Object { $_.IPAddress -eq '198.18.0.1' } |
    Select-Object -First 1
  if ($adapter -and $adapter.Status -eq 'Up' -and $tunIp) { break }
  Start-Sleep -Milliseconds 500
}

if (-not $adapter) {
  Write-Warning "TUN adapter '$alias' was not found after mihomo start."
  exit 0
}
if ($adapter.Status -ne 'Up' -or -not $tunIp) {
  Write-Warning "TUN adapter '$alias' is not ready after mihomo start."
  exit 0
}

netsh interface ipv4 set dnsservers name="$alias" source=static address=none register=none validate=no | Out-Null
netsh interface ipv6 set dnsservers name="$alias" source=static address=none register=none validate=no | Out-Null
netsh interface ipv4 delete dnsservers name="$alias" all | Out-Null
netsh interface ipv6 delete dnsservers name="$alias" all | Out-Null
Set-NetIPInterface -InterfaceAlias $alias -AddressFamily IPv4 -InterfaceMetric 5000 -ErrorAction SilentlyContinue
Set-NetIPInterface -InterfaceAlias $alias -AddressFamily IPv6 -InterfaceMetric 5000 -ErrorAction SilentlyContinue
Write-Output "Meta adapter DNS cleared."
`
}
