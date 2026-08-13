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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

var errMissingProvider = errors.New("missing daily proxy provider")

func IsMissingProviderError(err error) bool {
	return errors.Is(err, errMissingProvider)
}

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
	if target.Output == "" {
		return "", fmt.Errorf("target %q has empty output", target.Name)
	}
	if err := ensureProviderCaches(cfg); err != nil {
		return "", err
	}
	yaml, err := Render(cfg, target)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target.Output), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target.Output, []byte(yaml), 0600); err != nil {
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
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			normalized, normalizeErr := normalizeProviderData(data)
			if normalizeErr == nil {
				if err := os.WriteFile(path, normalized, 0600); err != nil {
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
		if err := os.WriteFile(path, normalized, 0600); err != nil {
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("空响应")
	}
	return data, nil
}

func normalizeProviderData(data []byte) ([]byte, error) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if block, ok := extractTopLevelProxies(text); ok {
		normalized := strings.TrimRight(block, "\n") + "\n"
		if countProviderNodes(normalized) == 0 {
			return nil, fmt.Errorf("返回内容没有可用节点")
		}
		return []byte(normalized), nil
	}
	if list, ok := extractTopLevelProxyList(text); ok {
		normalized := "proxies:\n" + list
		if countProviderNodes(normalized) == 0 {
			return nil, fmt.Errorf("返回内容没有可用节点")
		}
		return []byte(normalized), nil
	}
	return nil, fmt.Errorf("返回内容不是 mihomo 节点列表")
}

func extractTopLevelProxies(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if hasLeadingSpace(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "proxies:") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !hasLeadingSpace(lines[i]) && strings.Contains(trimmed, ":") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n"), true
}

func extractTopLevelProxyList(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	var out []string
	started := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if started {
				out = append(out, "")
			}
			continue
		}
		if !started {
			if !strings.HasPrefix(trimmed, "- ") {
				return "", false
			}
			started = true
		}
		if !hasLeadingSpace(line) && !strings.HasPrefix(trimmed, "- ") {
			return "", false
		}
		out = append(out, "  "+line)
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, "\n") + "\n", true
}

func hasLeadingSpace(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func countProviderNodes(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			count++
		}
	}
	return count
}

func Render(cfg *config.Config, target config.Target) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
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
	renderProxies(&b, cfg, target, wgConfigs, providerProxyLines)
	renderGroups(&b, providerProxyNames, wgNames, cfg.Tailscale.Enabled)
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

var (
	blockNodeNamePattern  = regexp.MustCompile(`^\s*-\s*name\s*:\s*(.+?)\s*$`)
	inlineNodeNamePattern = regexp.MustCompile(`(?i)(?:^|[,{]\s*)["']?name["']?\s*:\s*(?:"([^"]*)"|'([^']*)'|([^,}]+))`)
)

func providerNodeNames(lines []string) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range lines {
		var raw string
		item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if strings.HasPrefix(item, "{") {
			var node map[string]any
			if err := json.Unmarshal([]byte(item), &node); err == nil {
				if name, ok := node["name"]; ok {
					raw = fmt.Sprint(name)
				}
			}
		}
		if raw == "" {
			raw = inlineName(line)
		}
		if raw == "" {
			match := blockNodeNamePattern.FindStringSubmatch(line)
			if len(match) == 2 {
				raw = match[1]
			}
		}
		name := strings.Trim(strings.TrimSpace(raw), `"'`)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func inlineName(line string) string {
	match := inlineNodeNamePattern.FindStringSubmatch(line)
	if len(match) == 0 {
		return ""
	}
	for _, group := range match[1:] {
		if strings.TrimSpace(group) != "" {
			return group
		}
	}
	return ""
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
	if len(cfg.TUN.DNSHijack) > 0 {
		linef(b, "  dns-hijack:")
		for _, item := range cfg.TUN.DNSHijack {
			linef(b, "    - %s", item)
		}
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
	fallbacks := defaultList(cfg.DNS.Fallbacks, []string{"https://dns.google/dns-query"})

	linef(b, "dns:")
	linef(b, "  enable: true")
	if !isMobileTarget(target) {
		listenAddress := "0.0.0.0:1053"
		if target.Type == "linux-mihomo" {
			listenAddress = "127.0.0.1:1053"
		}
		linef(b, "  listen: %s", listenAddress)
	}
	linef(b, "  ipv6: true")
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

func renderProxies(b *strings.Builder, cfg *config.Config, target config.Target, wgConfigs []wgConfig, providerProxyLines []string) {
	linef(b, "proxies:")
	if len(providerProxyLines) == 0 && len(wgConfigs) == 0 && !cfg.Tailscale.Enabled {
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
	if cfg.Tailscale.Enabled {
		renderTSProxy(b, cfg, target)
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

func renderTSProxy(b *strings.Builder, cfg *config.Config, target config.Target) {
	name := "Tailnet"
	linef(b, "  - name: %s", quote(name))
	linef(b, "    type: tailscale")
	if target.Hostname != "" {
		linef(b, "    hostname: %s", quote(target.Hostname))
	}
	if cfg.Tailscale.AuthKey != "" {
		linef(b, "    auth-key: %s", quote(cfg.Tailscale.AuthKey))
	}
	if cfg.Tailscale.AuthKeyFile != "" {
		if data, err := os.ReadFile(cfg.Tailscale.AuthKeyFile); err == nil {
			key := strings.TrimSpace(string(data))
			if key != "" {
				linef(b, "    auth-key: %s", quote(key))
			}
		}
	}
	if cfg.Tailscale.ControlURL != "" {
		linef(b, "    control-url: %s", quote(cfg.Tailscale.ControlURL))
	}
	linef(b, "    state-dir: %s", quote(tailscaleStateDir(target)))
	linef(b, "    ephemeral: %t", cfg.Tailscale.Ephemeral)
	linef(b, "    udp: true")
	linef(b, "    accept-routes: %t", cfg.Tailscale.AcceptRoutes)
	if cfg.Tailscale.ExitNode != "" {
		linef(b, "    exit-node: %s", quote(cfg.Tailscale.ExitNode))
	}
	linef(b, "    exit-node-allow-lan-access: %t", cfg.Tailscale.ExitNodeAllowLANAccess)
	if !isMobileTarget(target) && len(cfg.Tailscale.InboundForwards) > 0 {
		linef(b, "    inbound-forwards:")
		for _, forward := range cfg.Tailscale.InboundForwards {
			linef(b, "      - name: %s", quote(forward.Name))
			linef(b, "        network: %s", forward.Network)
			linef(b, "        listen-port: %d", forward.ListenPort)
			linef(b, "        target: %s", quote(forward.Target))
		}
	}
}

func renderGroups(b *strings.Builder, providers, wgNames []string, tailscale bool) {
	linef(b, "proxy-groups:")
	linef(b, "  - name: PROXY")
	linef(b, "    type: select")
	if len(providers) == 0 {
		linef(b, "    proxies: ['DIRECT']")
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
	linef(b, "  - name: TS")
	linef(b, "    type: select")
	if tailscale {
		linef(b, "    proxies: ['Tailnet', 'DIRECT']")
	} else {
		linef(b, "    proxies: ['DIRECT']")
	}
	linef(b, "  - name: GLOBAL")
	linef(b, "    type: select")
	linef(b, "    proxies: ['PROXY', 'DIRECT', 'WG', 'TS']")
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
	if cfg.Tailscale.Enabled {
		if cfg.Tailscale.MagicDNSSuffix != "" {
			linef(b, "  - DOMAIN-SUFFIX,%s,TS", cfg.Tailscale.MagicDNSSuffix)
		}
		for _, domain := range cfg.Tailscale.Domains {
			domainRule(b, domain, "TS")
		}
		for _, cidr := range cfg.Tailscale.Routes {
			linef(b, "  - IP-CIDR,%s,TS,no-resolve", cidr)
		}
		for _, cidr := range cfg.Tailscale.IPv6Routes {
			linef(b, "  - IP-CIDR6,%s,TS,no-resolve", cidr)
		}
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

func tailscaleStateDir(target config.Target) string {
	switch target.Type {
	case "android-flclash", "android-yumebox", "mobile-mihomo":
		return "tailscale"
	default:
		return "./state/tailscale"
	}
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
