package generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/fileutil"
	"gopkg.in/yaml.v3"
)

var errMissingProvider = errors.New("missing daily proxy provider")

// Warn reports configuration that a mihomo profile cannot express. It defaults
// to a no-op so library callers are not forced to wire logging; the CLI points
// it at stderr and the diagnostic log. Without it, enabled features silently
// disappear from the generated profile.
var Warn = func(string) {}

func GenerateAll(cfg *config.Config) ([]string, error) {
	var written []string
	for _, target := range cfg.Targets {
		path, err := GenerateTarget(cfg, target)
		if err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}

func GenerateNamed(cfg *config.Config, name string) (string, error) {
	target, ok := cfg.Target(name)
	if !ok {
		return "", fmt.Errorf("unknown target %q", name)
	}
	return GenerateTarget(cfg, target)
}

func GenerateTarget(cfg *config.Config, target config.Target) (string, error) {
	unlock, err := fileutil.TryLock(filepath.Join("state", "generation.lock"))
	if err != nil {
		return "", err
	}
	defer unlock()
	if target.Output == "" {
		return "", fmt.Errorf("target %q has empty output", target.Name)
	}
	yaml, err := Render(cfg, target)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target.Output), 0755); err != nil {
		return "", err
	}
	if err := fileutil.WriteFile(target.Output, []byte(yaml), 0600); err != nil {
		return "", err
	}
	return target.Output, nil
}

func ensureProviderCaches(cfg *config.Config) error {
	configured := 0
	available := 0
	for _, provider := range cfg.Providers {
		if strings.TrimSpace(provider.Name) == "" {
			continue
		}
		configured++
		path := providerCachePath(provider)
		if data, err := os.ReadFile(path); strings.TrimSpace(provider.URL) == "" && err == nil && len(data) > 0 {
			normalized, normalizeErr := normalizeProviderData(data)
			if normalizeErr == nil {
				if err := fileutil.WriteFile(path, normalized, 0600); err != nil {
					return err
				}
				available++
				continue
			}
		}
		if strings.TrimSpace(provider.URL) == "" {
			if cfg.Setup.AllowDirectOnly {
				continue
			}
			return fmt.Errorf("%w: 日常代理订阅 %q 缺少链接，且缓存 %s 不存在或无有效节点", errMissingProvider, provider.Name, path)
		}
		data, err := fetchProvider(provider.URL)
		if err != nil {
			return fmt.Errorf("下载订阅 %q 失败: %w", provider.Name, err)
		}
		normalized, err := normalizeProviderData(data)
		if err != nil {
			return fmt.Errorf("下载订阅 %q 失败: %w", provider.Name, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := fileutil.WriteFile(path, normalized, 0600); err != nil {
			return err
		}
		available++
	}
	if (configured == 0 || available == 0) && !cfg.Setup.AllowDirectOnly {
		return fmt.Errorf("%w: 未配置可用的日常代理订阅；如确实只需直连，请明确启用仅直连模式", errMissingProvider)
	}
	return nil
}

func RefreshProviders(cfg *config.Config) error {
	unlock, err := fileutil.TryLock(filepath.Join("state", "generation.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return ensureProviderCaches(cfg)
}

func providerCachePath(provider config.Provider) string {
	if strings.TrimSpace(provider.Path) != "" {
		return provider.Path
	}
	return filepath.Join("providers", provider.Name+".yaml")
}

func fetchProvider(rawURL string) ([]byte, error) {
	client := &http.Client{
		Timeout: 25 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16*1024*1024 {
		return nil, errors.New("provider response exceeds 16 MiB")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("空响应")
	}
	return data, nil
}

func normalizeProviderData(data []byte) ([]byte, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff")))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("invalid provider YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("provider must contain one YAML document")
	}
	if len(document.Content) != 1 {
		return nil, errors.New("empty provider")
	}
	proxies := document.Content[0]
	if proxies.Kind == yaml.MappingNode {
		var selected *yaml.Node
		for index := 0; index+1 < len(proxies.Content); index += 2 {
			if proxies.Content[index].Value == "proxies" {
				if selected != nil {
					return nil, errors.New("duplicate proxies key")
				}
				selected = proxies.Content[index+1]
			}
		}
		proxies = selected
	}
	if proxies == nil || proxies.Kind != yaml.SequenceNode || len(proxies.Content) == 0 {
		return nil, errors.New("provider has no proxy nodes")
	}
	seen := map[string]bool{}
	nodes := make([]map[string]any, 0, len(proxies.Content))
	for _, node := range proxies.Content {
		var proxy map[string]any
		if err := node.Decode(&proxy); err != nil {
			return nil, fmt.Errorf("invalid proxy: %w", err)
		}
		name, ok := proxy["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, errors.New("proxy name is required")
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate proxy name %q", name)
		}
		seen[name] = true
		nodes = append(nodes, proxy)
	}
	var out strings.Builder
	out.WriteString("proxies:\n")
	for _, proxy := range nodes {
		encoded, err := json.Marshal(proxy)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&out, "  - %s\n", encoded)
	}
	return []byte(out.String()), nil
}

func Render(cfg *config.Config, target config.Target) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	// Tailnet membership, routes and DNS belong to the supervised tailscaled
	// daemon; mihomo profiles have no representation for them. Say so instead of
	// dropping the section without a trace.
	if cfg.Tailscale.Enabled {
		Warn("tailscale: 已启用，但 tailnet 成员/路由由 tailscaled 守护进程承担，不会写入 mihomo profile；该守护进程需要运行时 bin 目录中存在内置或已下载的 tailscaled/tailscale 组件")
	}
	var b strings.Builder
	wgConfigs, err := loadWGConfigs(cfg.WireGuard.Configs)
	if err != nil {
		return "", err
	}
	providerProxyLines, providerProxyNames, err := loadProviderProxies(cfg.Providers)
	if err != nil {
		return "", err
	}

	wgNames := make([]string, 0, len(wgConfigs))
	for _, wg := range wgConfigs {
		wgNames = append(wgNames, wg.Name)
	}

	mobile := isMobileTarget(target)
	linef(&b, "mixed-port: %d", cfg.Ports.Mixed)
	linef(&b, "allow-lan: false")
	if !mobile {
		linef(&b, "bind-address: 127.0.0.1")
	}
	linef(&b, "mode: rule")
	linef(&b, "log-level: info")
	linef(&b, "ipv6: true")
	if !mobile {
		linef(&b, "external-controller: %s", cfg.Ports.Controller)
		if cfg.Paths.Dashboard != "" {
			linef(&b, "external-ui: %s", mihomoPath(cfg.Paths.Dashboard))
		}
		linef(&b, `secret: ""`)
	}
	linef(&b, "")
	linef(&b, "profile:")
	linef(&b, "  store-selected: true")
	linef(&b, "  store-fake-ip: false")
	linef(&b, "")

	renderTUN(&b, cfg, target)
	renderDNS(&b, cfg, target)
	renderProxies(&b, cfg, wgConfigs, providerProxyLines)
	renderGroups(&b, providerProxyNames, wgNames, cfg.Setup.AllowDirectOnly)
	renderRules(&b, cfg, wgConfigs)

	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func loadWGConfigs(paths []string) ([]wgConfig, error) {
	var configs []wgConfig
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		cfg, err := readWireGuard(path)
		if err != nil {
			return nil, fmt.Errorf("read wireguard %s: %w", path, err)
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

func loadProviderProxies(providers []config.Provider) ([]string, []string, error) {
	var lines []string
	var names []string
	for _, provider := range providers {
		if strings.TrimSpace(provider.Name) == "" {
			continue
		}
		data, err := os.ReadFile(providerCachePath(provider))
		if err != nil {
			continue
		}
		normalized, err := normalizeProviderData(data)
		if err != nil {
			return nil, nil, fmt.Errorf("读取订阅缓存 %q 失败: %w", provider.Name, err)
		}
		blockLines := strings.Split(strings.TrimRight(string(normalized), "\n"), "\n")
		if len(blockLines) <= 1 {
			continue
		}
		lines = append(lines, blockLines[1:]...)
		names = append(names, providerNodeNames(blockLines[1:])...)
	}
	return lines, names, nil
}

func providerNodeNames(lines []string) []string {
	var proxies []struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &proxies); err != nil {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, proxy := range proxies {
		if proxy.Name != "" && !seen[proxy.Name] {
			names = append(names, proxy.Name)
			seen[proxy.Name] = true
		}
	}
	return names
}

// tunDNSHijack returns the DNS hijack entries for a desktop TUN profile.
// Without hijacking, TUN clients keep resolving through their own resolver
// and receive AAAA records; a proxied IPv6 destination then depends on the
// remote node dialing IPv6, which commodity nodes routinely cannot do. Every
// dual-stack client — Go dialers included — intermittently fails on that v6
// leg while the v4 leg works, which shows up as flaky TLS EOFs. Hijacking
// DNS and answering A-only keeps every client on the working IPv4 path.
// An explicitly configured dnsHijack always wins; a disabled DNS section
// disables the default too, since a hijack without a resolver is a black hole.
func tunDNSHijack(cfg *config.Config, target config.Target) []string {
	if cfg == nil || !cfg.TUN.Enabled || isMobileTarget(target) {
		return nil
	}
	if len(cfg.TUN.DNSHijack) > 0 {
		return cfg.TUN.DNSHijack
	}
	if cfg.DNS.Enabled != nil && !*cfg.DNS.Enabled {
		return nil
	}
	return []string{"any:53"}
}

func renderTUN(b *strings.Builder, cfg *config.Config, target config.Target) {
	if !cfg.TUN.Enabled || isMobileTarget(target) {
		return
	}
	stack := cfg.TUN.Stack
	if stack == "" {
		stack = "mixed"
	}
	linef(b, "tun:")
	linef(b, "  enable: true")
	linef(b, "  stack: %s", stack)
	linef(b, "  auto-route: %t", cfg.TUN.AutoRoute)
	linef(b, "  auto-detect-interface: %t", cfg.TUN.AutoDetectInterface)
	linef(b, "  strict-route: %t", cfg.TUN.StrictRoute)
	if hijack := tunDNSHijack(cfg, target); len(hijack) > 0 {
		linef(b, "  dns-hijack:")
		for _, item := range hijack {
			linef(b, "    - %s", item)
		}
	}
	// Tailnet traffic must bypass the TUN capture. Without the exclusion the
	// wireguard destinations fall into the catch-all proxy rule and die on the
	// remote node; only the Tailscale adapter's own routes can reach them.
	if cfg.Tailscale.Enabled {
		linef(b, "  route-exclude-address:")
		linef(b, "    - 100.64.0.0/10")
		linef(b, "    - fd7a:115c:a1e0::/48")
	}
	linef(b, "")
}

func renderDNS(b *strings.Builder, cfg *config.Config, target config.Target) {
	if cfg.DNS.Enabled != nil && !*cfg.DNS.Enabled {
		return
	}
	defaultNS := defaultList(cfg.DNS.DefaultNameservers, []string{"223.5.5.5", "114.114.114.114"})
	directNS := defaultList(cfg.DNS.DirectNameservers, []string{"223.5.5.5", "114.114.114.114"})
	proxyNS := defaultList(cfg.DNS.ProxyServerNameservers, []string{"223.5.5.5", "114.114.114.114"})
	nameservers := defaultList(cfg.DNS.Nameservers, []string{"https://dns.alidns.com/dns-query", "https://doh.pub/dns-query"})
	// The fallback resolvers answer for blocked domains; dns.google itself is
	// blocked from direct dialing, so every fallback query timed out and took
	// the whole resolution down with it (multi-second stalls, intermittent
	// failures). Routing the fallback through the PROXY group keeps it alive.
	fallbacks := defaultList(cfg.DNS.Fallbacks, []string{"https://dns.google/dns-query#PROXY"})

	linef(b, "dns:")
	linef(b, "  enable: true")
	if !isMobileTarget(target) {
		// Linux and macOS resolve through their own local setup, so the DNS
		// listener stays on the loopback interface rather than every adapter.
		listenAddress := "0.0.0.0:1053"
		if target.Type == "linux-mihomo" || target.Type == "darwin-mihomo" {
			listenAddress = "127.0.0.1:1053"
		}
		linef(b, "  listen: %s", listenAddress)
	}
	// When the profile hijacks client DNS through the TUN resolver, AAAA
	// answers are suppressed: proxied IPv6 destinations fail at the remote
	// node, so dual-stack clients must stay on IPv4 (see tunDNSHijack).
	linef(b, "  ipv6: %t", len(tunDNSHijack(cfg, target)) == 0)
	linef(b, "  respect-rules: false")
	yamlList(b, "  default-nameserver:", "    ", defaultNS)
	yamlList(b, "  direct-nameserver:", "    ", directNS)
	yamlList(b, "  proxy-server-nameserver:", "    ", proxyNS)
	linef(b, "  enhanced-mode: redir-host")
	if len(cfg.DNS.NameserverPolicy) > 0 {
		linef(b, "  nameserver-policy:")
		keys := make([]string, 0, len(cfg.DNS.NameserverPolicy))
		for key := range cfg.DNS.NameserverPolicy {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			linef(b, "    %s:", quote(key))
			for _, ns := range cfg.DNS.NameserverPolicy[key] {
				linef(b, "      - %s", ns)
			}
		}
	}
	yamlList(b, "  nameserver:", "    ", nameservers)
	yamlList(b, "  fallback:", "    ", fallbacks)
	linef(b, "")
}

func renderProxies(b *strings.Builder, cfg *config.Config, wgConfigs []wgConfig, providerProxyLines []string) {
	linef(b, "proxies:")
	if len(providerProxyLines) == 0 && len(wgConfigs) == 0 {
		linef(b, "  []")
		linef(b, "")
		return
	}
	for _, line := range providerProxyLines {
		linef(b, "%s", line)
	}
	for _, wg := range wgConfigs {
		renderWGProxy(b, cfg, wg)
	}
	linef(b, "")
}

func renderWGProxy(b *strings.Builder, cfg *config.Config, wg wgConfig) {
	linef(b, "  - name: %s", quote(wg.Name))
	linef(b, "    type: wireguard")
	if ip := firstIPv4(wg.Address); ip != "" {
		linef(b, "    ip: %s", quote(stripCIDR(ip)))
	}
	if ip := firstIPv6(wg.Address); ip != "" {
		linef(b, "    ipv6: %s", quote(stripCIDR(ip)))
	}
	linef(b, "    private-key: %s", quote(wg.PrivateKey))
	linef(b, "    peers:")
	for _, peer := range wg.Peers {
		host, port := splitEndpoint(peer.Endpoint)
		linef(b, "      - server: %s", quote(host))
		linef(b, "        port: %s", port)
		linef(b, "        public-key: %s", quote(peer.PublicKey))
		if len(peer.AllowedIPs) > 0 {
			linef(b, "        allowed-ips: %s", inlineList(peer.AllowedIPs))
		}
		if peer.PresharedKey != "" {
			linef(b, "        pre-shared-key: %s", quote(peer.PresharedKey))
		}
		if peer.Keepalive != "" {
			linef(b, "        persistent-keepalive: %s", peer.Keepalive)
		}
	}
	linef(b, "    udp: true")
	if wg.MTU != "" {
		linef(b, "    mtu: %s", wg.MTU)
	}
	if cfg.WireGuard.RemoteDNSResolve && len(wg.DNS) > 0 {
		linef(b, "    remote-dns-resolve: true")
		linef(b, "    dns: %s", inlineList(wg.DNS))
	}
	linef(b, "")
}

func renderGroups(b *strings.Builder, providers, wgNames []string, directOnly bool) {
	linef(b, "proxy-groups:")
	linef(b, "  - name: PROXY")
	linef(b, "    type: select")
	if len(providers) == 0 {
		if directOnly {
			linef(b, "    proxies: ['DIRECT']")
		} else {
			linef(b, "    proxies: ['REJECT']")
		}
	} else {
		linef(b, "    proxies: %s", inlineList(append(providers, "DIRECT")))
	}
	linef(b, "  - name: WG")
	linef(b, "    type: select")
	if len(wgNames) == 0 {
		linef(b, "    proxies: ['DIRECT']")
	} else {
		linef(b, "    proxies: %s", inlineList(append(wgNames, "DIRECT")))
	}
	linef(b, "  - name: GLOBAL")
	linef(b, "    type: select")
	linef(b, "    proxies: ['PROXY', 'DIRECT', 'WG']")
	linef(b, "")
}

func renderRules(b *strings.Builder, cfg *config.Config, wgConfigs []wgConfig) {
	linef(b, "rules:")
	for _, host := range providerHosts(cfg.Providers) {
		linef(b, "  - DOMAIN,%s,DIRECT", host)
	}
	for _, domain := range cfg.Rules.DirectDomains {
		domainRule(b, domain, "DIRECT")
	}
	for _, cidr := range append(wgAllowedRoutes(wgConfigs), cfg.WireGuard.Routes...) {
		linef(b, "  - IP-CIDR,%s,WG,no-resolve", cidr)
	}
	for _, domain := range cfg.WireGuard.Domains {
		domainRule(b, domain, "WG")
	}
	for _, cidr := range cfg.Rules.DirectCIDRs {
		linef(b, "  - IP-CIDR,%s,DIRECT,no-resolve", cidr)
	}
	for _, domain := range cfg.Rules.ProxyDomains {
		domainRule(b, domain, "PROXY")
	}
	linef(b, "  - GEOIP,CN,DIRECT")
	linef(b, "  - MATCH,PROXY")
}

func providerHosts(providers []config.Provider) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, provider := range providers {
		u, err := url.Parse(provider.URL)
		if err != nil || u.Hostname() == "" || seen[u.Hostname()] {
			continue
		}
		seen[u.Hostname()] = true
		hosts = append(hosts, u.Hostname())
	}
	sort.Strings(hosts)
	return hosts
}

func domainRule(b *strings.Builder, domain, target string) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return
	}
	if strings.HasPrefix(domain, "*.") {
		linef(b, "  - DOMAIN-SUFFIX,%s,%s", strings.TrimPrefix(domain, "*."), target)
		return
	}
	linef(b, "  - DOMAIN,%s,%s", domain, target)
}

func isMobileTarget(target config.Target) bool {
	switch target.Type {
	case "android-flclash", "android-yumebox", "mobile-mihomo":
		return true
	default:
		return false
	}
}

func mihomoPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "/") || strings.Contains(path, ":/") {
		return path
	}
	return "./" + path
}

func defaultList(values, fallback []string) []string {
	if len(values) > 0 {
		return values
	}
	return fallback
}

func yamlList(b *strings.Builder, header, indent string, values []string) {
	linef(b, header)
	for _, value := range values {
		linef(b, "%s- %s", indent, value)
	}
}

func inlineList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quote(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func linef(b *strings.Builder, format string, args ...any) {
	if len(args) == 0 {
		b.WriteString(format)
	} else {
		fmt.Fprintf(b, format, args...)
	}
	b.WriteByte('\n')
}

func firstIPv4(values []string) string {
	for _, value := range values {
		if strings.Contains(value, ".") {
			return value
		}
	}
	return ""
}

func firstIPv6(values []string) string {
	for _, value := range values {
		if strings.Contains(value, ":") {
			return value
		}
	}
	return ""
}

func stripCIDR(value string) string {
	if before, _, ok := strings.Cut(value, "/"); ok {
		return before
	}
	return value
}
