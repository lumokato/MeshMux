//go:build windows

package runner

import (
	"fmt"
	"strings"

	"github.com/meshmux/meshmux/internal/config"
)

func postStartNetwork(cfg *config.Config) error {
	if cfg == nil || !cfg.TUN.Enabled {
		return nil
	}

	routes := make([]string, 0, len(cfg.Tailscale.Routes))
	for _, route := range cfg.Tailscale.Routes {
		route = strings.TrimSpace(route)
		if route == "" || strings.Contains(route, ":") {
			continue
		}
		routes = append(routes, route)
	}

	script := buildPostStartNetworkScript(routes, dnsDisabled(cfg), !cfg.TUN.AutoRoute)
	cmd := hiddenCommand("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
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

func buildPostStartNetworkScript(routes []string, clearDNS, addRoutes bool) string {
	var routeList strings.Builder
	for _, route := range routes {
		routeList.WriteString("  ")
		routeList.WriteString(psQuote(route))
		routeList.WriteByte('\n')
	}

	return fmt.Sprintf(`
$ErrorActionPreference = 'Continue'
$alias = 'Meta'
$gateway = '198.18.0.2'
$clearDNS = %s
$addRoutes = %s
$routes = @(
%s)

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

if ($clearDNS) {
  netsh interface ipv4 set dnsservers name="$alias" source=static address=none register=none validate=no | Out-Null
  netsh interface ipv6 set dnsservers name="$alias" source=static address=none register=none validate=no | Out-Null
  netsh interface ipv4 delete dnsservers name="$alias" all | Out-Null
  netsh interface ipv6 delete dnsservers name="$alias" all | Out-Null
  Set-NetIPInterface -InterfaceAlias $alias -AddressFamily IPv4 -InterfaceMetric 5000 -ErrorAction SilentlyContinue
  Set-NetIPInterface -InterfaceAlias $alias -AddressFamily IPv6 -InterfaceMetric 5000 -ErrorAction SilentlyContinue
  Write-Output "Meta adapter DNS cleared."
}

if ($addRoutes) {
  foreach ($cidr in $routes) {
    if (-not $cidr) { continue }
    Get-NetRoute -AddressFamily IPv4 -ErrorAction SilentlyContinue |
      Where-Object { $_.DestinationPrefix -eq [string]$cidr -and $_.InterfaceAlias -eq $alias } |
      Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
    try {
      New-NetRoute -DestinationPrefix ([string]$cidr) -InterfaceAlias $alias -NextHop $gateway -RouteMetric 0 -ErrorAction Stop | Out-Null
      Write-Output "Added TUN route: $cidr -> $alias via $gateway"
    } catch {
      Write-Warning "Failed to add TUN route $cidr -> $alias via ${gateway}: $($_.Exception.Message)"
    }
  }
}
`, psBool(clearDNS), psBool(addRoutes), routeList.String())
}

func psBool(value bool) string {
	if value {
		return "$true"
	}
	return "$false"
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
