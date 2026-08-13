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
	if err := ensureProviderCaches(cfg); err == nil || !IsMissingProviderError(err) {
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
	if !strings.Contains(text, "proxies:\n  - name: node-a") {
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

func TestTailscaleInboundForwardsOnlyRenderForWindows(t *testing.T) {
	enabledDNS := true
	cfg := &config.Config{
		Ports: config.Ports{Mixed: 2080, Controller: "127.0.0.1:9090"},
		DNS:   config.DNS{Enabled: &enabledDNS},
		Tailscale: config.Tailscale{
			Enabled: true,
			InboundForwards: []config.InboundForward{{
				Name: "windows-ssh", Network: "tcp", ListenPort: 22, Target: "127.0.0.1:22",
			}},
		},
	}
	windows, err := Render(cfg, config.Target{Name: "windows", Type: "windows-mihomo", Hostname: "windows-meshmux"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"inbound-forwards:", "name: 'windows-ssh'", "listen-port: 22", "target: '127.0.0.1:22'"} {
		if !strings.Contains(windows, want) {
			t.Fatalf("windows profile missing %q:\n%s", want, windows)
		}
	}
	mobile, err := Render(cfg, config.Target{Name: "mobile", Type: "mobile-mihomo", Hostname: "mobile-meshmux"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mobile, "inbound-forwards:") {
		t.Fatalf("mobile profile contains Windows inbound forwards:\n%s", mobile)
	}
}
