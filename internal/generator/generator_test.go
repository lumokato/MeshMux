package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meshmux/meshmux/internal/config"
)

func TestProviderCacheRequiredUnlessDirectOnlyIsExplicit(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	cfg := &config.Config{Providers: []config.Provider{{Name: "main", Path: filepath.Join("providers", "main.yaml")}}}
	if err := ensureProviderCaches(cfg); err == nil || !strings.Contains(err.Error(), "missing daily proxy provider") {
		t.Fatalf("missing provider error = %v", err)
	}
	cfg.Setup.AllowDirectOnly = true
	if err := ensureProviderCaches(cfg); err != nil {
		t.Fatalf("explicit direct-only mode rejected derived empty provider: %v", err)
	}
	cfg.Providers = nil
	if err := ensureProviderCaches(cfg); err != nil {
		t.Fatalf("explicit direct-only mode rejected: %v", err)
	}
}

func TestProviderCacheAllowsMissingURLWhenNodesExist(t *testing.T) {
	old, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	path := filepath.Join("providers", "main.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("proxies:\n  - name: node-a\n    type: ss\n    server: example.test\n    port: 443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Providers: []config.Provider{{Name: "main", Path: path}}}
	if err := ensureProviderCaches(cfg); err != nil {
		t.Fatalf("valid cache rejected: %v", err)
	}
}

func TestNormalizeProviderDataExtractsProxiesBlock(t *testing.T) {
	input := []byte(`mixed-port: 7890
proxies:
  - name: node-a
    type: ss
    server: example.test
    port: 443
proxy-groups:
  - name: PROXY
    type: select
    proxies:
      - node-a
rules:
  - MATCH,PROXY
`)

	got, err := normalizeProviderData(input)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, `"name":"node-a"`) {
		t.Fatalf("normalized provider missing node: %s", text)
	}
	if strings.Contains(text, "proxy-groups:") || strings.Contains(text, "rules:") {
		t.Fatalf("normalized provider contains full-config sections: %s", text)
	}
}

func TestNormalizeProviderDataRejectsEmptyProvider(t *testing.T) {
	if _, err := normalizeProviderData([]byte("proxies: []\n")); err == nil {
		t.Fatal("expected empty provider to be rejected")
	}
}

func TestProviderNodeNamesReadsInlineJSONName(t *testing.T) {
	names := providerNodeNames([]string{
		`  - {"type":"vless","name":"node-a","server":"example.test"}`,
		`  - {"type":"vless","name":"node-b","server":"example.test"}`,
	})
	if strings.Join(names, ",") != "node-a,node-b" {
		t.Fatalf("names = %#v", names)
	}
}

func TestMobileProfileKeepsMixedPort(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
	}
	text, err := Render(cfg, config.Target{Name: "mobile", Type: "mobile-mihomo", Output: "profiles/mobile.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "mixed-port: 2080\n") {
		t.Fatalf("mobile profile missing mixed-port:\n%s", text)
	}
	if strings.Contains(text, "external-controller:") {
		t.Fatalf("mobile profile contains desktop controller:\n%s", text)
	}
}

func TestLinuxProfileBindsDNSOnlyToLoopback(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
	}
	text, err := Render(cfg, config.Target{Name: "linux", Type: "linux-mihomo", Output: "profiles/linux.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "  listen: 127.0.0.1:1053\n") {
		t.Fatalf("linux profile DNS is not loopback-only:\n%s", text)
	}
	if strings.Contains(text, "  listen: 0.0.0.0:1053\n") {
		t.Fatalf("linux profile exposes DNS on all interfaces:\n%s", text)
	}
}

func TestDarwinProfileBindsDNSOnlyToLoopback(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
	}
	text, err := Render(cfg, config.Target{Name: "darwin", Type: "darwin-mihomo", Output: "profiles/darwin.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "  listen: 127.0.0.1:1053\n") {
		t.Fatalf("darwin profile DNS is not loopback-only:\n%s", text)
	}
}

func TestWindowsProfileBindsDNSOnlyToLoopback(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
	}
	text, err := Render(cfg, config.Target{Name: "windows", Type: "windows-mihomo", Output: "profiles/windows.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "  listen: 127.0.0.1:1053\n") {
		t.Fatalf("windows profile DNS is not loopback-only:\n%s", text)
	}
	if strings.Contains(text, "  listen: 0.0.0.0:1053\n") {
		t.Fatalf("windows profile exposes DNS on all interfaces:\n%s", text)
	}
}

func TestDesktopTUNProfileHijacksDNSAndSuppressesAAAA(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
		TUN:   config.TUN{Enabled: true, AutoRoute: true, AutoDetectInterface: true},
	}
	text, err := Render(cfg, config.Target{Name: "linux", Type: "linux-mihomo", Output: "profiles/linux.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "  dns-hijack:\n    - any:53\n") {
		t.Fatalf("desktop TUN profile does not hijack DNS:\n%s", text)
	}
	if !strings.Contains(text, "  ipv6: false\n") {
		t.Fatalf("hijacked DNS must answer A-only (ipv6 false):\n%s", text)
	}
}

func TestDesktopTUNProfileRespectsConfiguredHijack(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
		TUN:   config.TUN{Enabled: true, DNSHijack: []string{"any:5353"}},
	}
	text, err := Render(cfg, config.Target{Name: "linux", Type: "linux-mihomo", Output: "profiles/linux.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "    - any:5353\n") {
		t.Fatalf("configured dnsHijack was dropped:\n%s", text)
	}
	if strings.Contains(text, "    - any:53\n") {
		t.Fatalf("default hijack must not be appended to the configured one:\n%s", text)
	}
}

func TestNoTUNProfileKeepsIPv6AndNoHijack(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
	}
	text, err := Render(cfg, config.Target{Name: "linux", Type: "linux-mihomo", Output: "profiles/linux.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "dns-hijack") {
		t.Fatalf("profile without TUN must not hijack DNS:\n%s", text)
	}
	if !strings.Contains(text, "  ipv6: true\n") {
		t.Fatalf("profile without TUN keeps IPv6 answers:\n%s", text)
	}
}

func TestDesktopTUNProfileExcludesTailnetRoutes(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
		TUN:   config.TUN{Enabled: true, AutoRoute: true},
	}
	cfg.Tailscale.Enabled = true
	text, err := Render(cfg, config.Target{Name: "linux", Type: "linux-mihomo", Output: "profiles/linux.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  route-exclude-address:\n",
		"    - 100.64.0.0/10\n",
		"    - fd7a:115c:a1e0::/48\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tailnet routes must be excluded from TUN capture, missing %q:\n%s", want, text)
		}
	}

	cfg.Tailscale.Enabled = false
	text, err = Render(cfg, config.Target{Name: "linux", Type: "linux-mihomo", Output: "profiles/linux.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "route-exclude-address") {
		t.Fatalf("tailnet exclusion must stay away while tailnet is disabled:\n%s", text)
	}
}

func TestDesktopProfileRoutesFallbackDohThroughProxy(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
	}
	text, err := Render(cfg, config.Target{Name: "linux", Type: "linux-mihomo", Output: "profiles/linux.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "- https://dns.google/dns-query#PROXY\n") {
		t.Fatalf("fallback DoH must be routed through the proxy group:\n%s", text)
	}
}

func TestProviderYAMLFormats(t *testing.T) {
	for _, input := range []string{
		"proxies:\n- type: ss\n  name: node-a\n  server: example.test\n",
		"proxies: [{type: ss, name: node-a, server: example.test}]\n",
		"- type: ss\n  name: node-a\n  server: example.test\n",
	} {
		data, err := normalizeProviderData([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")[1:]
		names := providerNodeNames(lines)
		if len(names) != 1 || names[0] != "node-a" {
			t.Fatalf("names=%v", names)
		}
	}
}
func TestProviderYAMLRejectsMalformedAndDuplicateNodes(t *testing.T) {
	for _, input := range []string{"proxies: [", "proxies: [{type: ss}]", "proxies: [{name: a}, {name: a}]", "proxies: [{name: a}]\n---\nproxies: [{name: b}]", "proxies: [{name: a, name: b}]"} {
		if _, err := normalizeProviderData([]byte(input)); err == nil {
			t.Fatalf("invalid provider accepted: %s", input)
		}
	}
}
