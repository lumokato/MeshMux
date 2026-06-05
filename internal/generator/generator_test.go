package generator

import (
	"strings"
	"testing"

	"github.com/meshmux/meshmux/internal/config"
)

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
