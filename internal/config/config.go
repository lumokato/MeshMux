package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	AppName                         = "MeshMux"
	DefaultConfigPath               = "meshmux.local.json"
	ExampleConfigPath               = "templates/meshmux.example.json"
	DefaultMihomoRepo               = "lumokato/MeshMux"
	DefaultMihomoReleaseTag         = "mihomo-v1.19.29-meshmux.1"
	DefaultMihomoAssetPattern       = `mihomo-windows-amd64-compatible-v1\.19\.29-meshmux\.1\.zip$`
	LinuxMihomoAssetPattern         = `mihomo-linux-amd64-compatible-v1\.19\.29-meshmux\.1\.gz$`
	OfficialMihomoRepo              = "MetaCubeX/mihomo"
	OfficialLinuxMihomoAssetPattern = `mihomo-linux-amd64-compatible.*\.gz$`
)

type Config struct {
	Name       string          `json:"name"`
	Setup      Setup           `json:"setup"`
	Ports      Ports           `json:"ports"`
	Paths      Paths           `json:"paths"`
	Providers  []Provider      `json:"providers,omitempty"`
	WireGuard  WireGuard       `json:"wireguard"`
	Tailscale  Tailscale       `json:"tailscale"`
	TUN        TUN             `json:"tun"`
	DNS        DNS             `json:"dns"`
	Rules      Rules           `json:"rules"`
	Targets    []Target        `json:"targets,omitempty"`
	Publish    []PublishTarget `json:"publish,omitempty"`
	Components Components      `json:"components"`
}

type Setup struct {
	ProviderURL      string `json:"providerUrl,omitempty"`
	AllowDirectOnly  bool   `json:"allowDirectOnly,omitempty"`
	SubStoreURL      string `json:"subStoreUrl"`
	SubStoreBackend  string `json:"subStoreBackend"`
	SubStoreFileName string `json:"subStoreFileName"`
}

type Ports struct {
	Mixed      int    `json:"mixed"`
	Controller string `json:"controller"`
}

type Paths struct {
	Runtime   string `json:"runtime"`
	Dashboard string `json:"dashboard"`
}

type Provider struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Interval int    `json:"interval"`
	Path     string `json:"path"`
}

type WireGuard struct {
	Configs          []string `json:"configs"`
	RemoteDNSResolve bool     `json:"remoteDnsResolve"`
	Domains          []string `json:"domains"`
	Routes           []string `json:"routes"`
}

type Tailscale struct {
	Enabled                bool             `json:"enabled"`
	ControlURL             string           `json:"controlUrl"`
	AuthKey                string           `json:"authKey"`
	AuthKeyFile            string           `json:"authKeyFile"`
	AcceptRoutes           bool             `json:"acceptRoutes"`
	Ephemeral              bool             `json:"ephemeral"`
	ExitNode               string           `json:"exitNode"`
	ExitNodeAllowLANAccess bool             `json:"exitNodeAllowLanAccess"`
	MagicDNSSuffix         string           `json:"magicDnsSuffix"`
	Routes                 []string         `json:"routes"`
	IPv6Routes             []string         `json:"ipv6Routes"`
	Domains                []string         `json:"domains"`
	InboundForwards        []InboundForward `json:"inboundForwards,omitempty"`
}

type InboundForward struct {
	Name       string `json:"name"`
	Network    string `json:"network"`
	ListenPort int    `json:"listenPort"`
	Target     string `json:"target"`
}

type TUN struct {
	Enabled             bool     `json:"enabled"`
	Stack               string   `json:"stack"`
	AutoRoute           bool     `json:"autoRoute"`
	AutoDetectInterface bool     `json:"autoDetectInterface"`
	StrictRoute         bool     `json:"strictRoute"`
	DNSHijack           []string `json:"dnsHijack"`
}

type DNS struct {
	Enabled                *bool               `json:"enabled"`
	DefaultNameservers     []string            `json:"defaultNameservers"`
	DirectNameservers      []string            `json:"directNameservers"`
	ProxyServerNameservers []string            `json:"proxyServerNameservers"`
	Nameservers            []string            `json:"nameservers"`
	Fallbacks              []string            `json:"fallbacks"`
	NameserverPolicy       map[string][]string `json:"nameserverPolicy"`
}

type Rules struct {
	DirectCIDRs   []string `json:"directCidrs"`
	DirectDomains []string `json:"directDomains"`
	ProxyDomains  []string `json:"proxyDomains"`
}

type Target struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Hostname string `json:"hostname"`
	Output   string `json:"output"`
}

type PublishTarget struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Input     string `json:"input"`
	RemoteURL string `json:"remoteUrl"`
	APIURL    string `json:"apiUrl"`
	BaseURL   string `json:"baseUrl"`
	Backend   string `json:"backend"`
	APIPath   string `json:"apiPath"`
	TokenEnv  string `json:"tokenEnv"`
	FileName  string `json:"fileName"`
}

type Components struct {
	Mihomo    Component `json:"mihomo"`
	Dashboard Component `json:"dashboard"`
}

type Component struct {
	Path         string `json:"path"`
	Repo         string `json:"repo"`
	ReleaseTag   string `json:"releaseTag,omitempty"`
	AssetPattern string `json:"assetPattern"`
}

func ResolveConfigPath(path string) string {
	if path != "" {
		return path
	}
	if runtime.GOOS == "windows" {
		if local := LocalConfigPath(); local != DefaultConfigPath {
			if _, err := os.Stat(local); err == nil {
				return local
			}
		}
	}
	if _, err := os.Stat(DefaultConfigPath); err == nil {
		return DefaultConfigPath
	}
	if local := LocalConfigPath(); local != DefaultConfigPath {
		if _, err := os.Stat(local); err == nil {
			return local
		}
	}
	return ExampleConfigPath
}

func LocalDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("MESHMUX_HOME")); dir != "" {
		return dir
	}
	if runtime.GOOS == "windows" {
		if dir := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); dir != "" {
			return filepath.Join(dir, AppName)
		}
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, AppName)
	}
	return "."
}

func LocalConfigPath() string {
	return filepath.Join(LocalDataDir(), DefaultConfigPath)
}

func EnsureLocalConfig(examplePath string) (string, error) {
	legacyDirs := []string{}
	if runtime.GOOS == "windows" {
		if roaming := strings.TrimSpace(os.Getenv("APPDATA")); roaming != "" {
			legacyDirs = append(legacyDirs, filepath.Join(roaming, AppName))
		}
		if programData := strings.TrimSpace(os.Getenv("ProgramData")); programData != "" {
			legacyDirs = append(legacyDirs, filepath.Join(programData, AppName))
		}
	}
	return ensureLocalConfigAt(LocalDataDir(), legacyDirs, examplePath)
}

func EnsureCanonicalConfig(path, examplePath string, fallbackPaths ...string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return EnsureLocalConfig(examplePath)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	localPath, err := filepath.Abs(LocalConfigPath())
	if err != nil || !strings.EqualFold(filepath.Clean(absPath), filepath.Clean(localPath)) {
		return absPath, nil
	}
	canonical, err := EnsureLocalConfig(examplePath)
	if err != nil {
		return "", err
	}
	currentData, err := os.ReadFile(canonical)
	if err != nil || !IsBootstrapConfig(currentData) {
		return canonical, err
	}
	for _, fallbackPath := range fallbackPaths {
		fallbackData, readErr := os.ReadFile(fallbackPath)
		if readErr != nil || IsBootstrapConfig(fallbackData) {
			continue
		}
		if _, _, loadErr := loadData(fallbackData, fallbackPath); loadErr != nil {
			continue
		}
		if err := writeConfigFile(canonical, fallbackData); err != nil {
			return "", err
		}
		return canonical, nil
	}
	return canonical, nil
}

func ensureLocalConfigAt(dir string, legacyDirs []string, examplePath string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, DefaultConfigPath)
	if examplePath == "" {
		examplePath = ExampleConfigPath
	}
	exampleData, err := os.ReadFile(examplePath)
	if err != nil {
		exampleData, err = os.ReadFile(ExampleConfigPath)
		if err != nil {
			return "", err
		}
	}

	currentData, currentErr := os.ReadFile(path)
	if currentErr == nil && !IsBootstrapConfig(currentData) {
		return path, nil
	}
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return "", currentErr
	}

	for _, legacyDir := range legacyDirs {
		legacyPath := filepath.Join(legacyDir, DefaultConfigPath)
		legacyData, readErr := os.ReadFile(legacyPath)
		if readErr != nil || IsBootstrapConfig(legacyData) {
			continue
		}
		if _, _, loadErr := loadData(legacyData, legacyPath); loadErr != nil {
			continue
		}
		if err := writeConfigFile(path, legacyData); err != nil {
			return "", err
		}
		for _, name := range []string{"providers", "wireguard"} {
			if err := copyMissingTree(filepath.Join(legacyDir, name), filepath.Join(dir, name)); err != nil {
				return "", err
			}
		}
		return path, nil
	}

	if currentErr == nil {
		return path, nil
	}
	return path, writeConfigFile(path, exampleData)
}

func Load(path string) (*Config, string, error) {
	resolved := ResolveConfigPath(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, resolved, err
	}
	return loadData(data, resolved)
}

func loadData(data []byte, resolved string) (*Config, string, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, resolved, err
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, resolved, err
	}
	return &cfg, resolved, nil
}

func IsBootstrapConfig(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var cfg Config
	if json.Unmarshal(data, &cfg) != nil {
		return false
	}
	if cfg.Setup.AllowDirectOnly || strings.TrimSpace(cfg.Setup.ProviderURL) != "" {
		return false
	}
	for _, provider := range cfg.Providers {
		if strings.TrimSpace(provider.URL) != "" {
			return false
		}
	}
	if len(cfg.WireGuard.Configs) > 0 || len(cfg.WireGuard.Domains) > 0 || len(cfg.WireGuard.Routes) > 0 {
		return false
	}
	if cfg.Tailscale.Enabled || strings.TrimSpace(cfg.Tailscale.AuthKey) != "" ||
		strings.TrimSpace(cfg.Tailscale.AuthKeyFile) != "" || len(cfg.Tailscale.InboundForwards) > 0 ||
		strings.TrimSpace(cfg.Tailscale.ExitNode) != "" || strings.TrimSpace(cfg.Tailscale.MagicDNSSuffix) != "" {
		return false
	}
	return true
}

func writeConfigFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tempPath, path)
}

func copyMissingTree(source, destination string) error {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.Walk(source, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
}

func InitLocal(overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(DefaultConfigPath); err == nil {
			return errors.New(DefaultConfigPath + " already exists")
		}
	}
	data, err := os.ReadFile(ExampleConfigPath)
	if err != nil {
		return err
	}
	return os.WriteFile(DefaultConfigPath, data, 0600)
}

func (c *Config) ApplyDefaults() {
	c.applyDefaults(runtime.GOOS)
}

func (c *Config) applyDefaults(goos string) {
	if c.Name == "" {
		c.Name = "default"
	}
	if c.Ports.Mixed == 0 {
		c.Ports.Mixed = 2080
	}
	if c.Ports.Controller == "" {
		c.Ports.Controller = "127.0.0.1:9090"
	}
	if c.Paths.Runtime == "" {
		c.Paths.Runtime = "runtime"
	}
	if c.Paths.Dashboard == "" {
		c.Paths.Dashboard = "dashboard"
	}
	if c.Rules.DirectCIDRs == nil {
		c.Rules.DirectCIDRs = []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
	}
	if c.Rules.DirectDomains == nil {
		c.Rules.DirectDomains = []string{"localhost", "*.local"}
	}
	if c.Tailscale.ControlURL == "" {
		c.Tailscale.ControlURL = "https://controlplane.tailscale.com"
	}
	if c.Tailscale.Routes == nil {
		c.Tailscale.Routes = []string{"100.64.0.0/10"}
	}
	if c.Tailscale.IPv6Routes == nil {
		c.Tailscale.IPv6Routes = []string{"fd7a:115c:a1e0::/48"}
	}
	if c.Components.Mihomo.Repo == "" || legacyMihomoComponent(goos, c.Components.Mihomo) {
		c.Components.Mihomo.Repo = DefaultMihomoRepo
		c.Components.Mihomo.ReleaseTag = DefaultMihomoReleaseTag
		c.Components.Mihomo.AssetPattern = DefaultMihomoAssetPatternFor(goos)
	} else if defaultMihomoComponent(c.Components.Mihomo) {
		c.Components.Mihomo.AssetPattern = DefaultMihomoAssetPatternFor(goos)
	} else if c.Components.Mihomo.AssetPattern == "" {
		c.Components.Mihomo.AssetPattern = mihomoAssetPatternFor(goos, c.Components.Mihomo.Repo)
	}
	if c.Components.Mihomo.Repo == DefaultMihomoRepo && c.Components.Mihomo.ReleaseTag == "" {
		c.Components.Mihomo.ReleaseTag = DefaultMihomoReleaseTag
	}
	if c.Components.Mihomo.Path == "" || (defaultMihomoComponent(c.Components.Mihomo) && isKnownDefaultMihomoPath(c.Components.Mihomo.Path)) {
		c.Components.Mihomo.Path = DefaultMihomoPathFor(goos)
	}
	if c.Components.Dashboard.Path == "" {
		c.Components.Dashboard.Path = "dashboard"
	}
	if c.Components.Dashboard.Repo == "" {
		c.Components.Dashboard.Repo = "MetaCubeX/metacubexd"
	}
	if c.Components.Dashboard.AssetPattern == "" {
		c.Components.Dashboard.AssetPattern = `compressed-dist\.tgz$`
	}
	c.deriveSetup()
	c.deriveTailnetDomains()
	c.applySetup(goos)
}

func defaultMihomoComponent(component Component) bool {
	if !strings.EqualFold(strings.TrimSpace(component.Repo), DefaultMihomoRepo) {
		return false
	}
	switch component.AssetPattern {
	case "", DefaultMihomoAssetPattern, LinuxMihomoAssetPattern:
		return true
	default:
		return false
	}
}

func legacyMihomoComponent(goos string, component Component) bool {
	switch component.Repo {
	case OfficialMihomoRepo:
		switch component.AssetPattern {
		case `mihomo-windows-amd64-compatible.*\.zip$`, `mihomo-windows-amd64.*\.zip$`:
			return true
		case "":
			return goos == "windows"
		}
	}
	return false
}

func DefaultMihomoPath() string {
	return DefaultMihomoPathFor(runtime.GOOS)
}

func DefaultMihomoPathFor(goos string) string {
	name := "mihomo"
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join("bin", name)
}

func isKnownDefaultMihomoPath(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	return cleaned == filepath.Clean(DefaultMihomoPathFor("windows")) || cleaned == filepath.Clean(DefaultMihomoPathFor("linux"))
}

func DefaultMihomoAssetPatternFor(goos string) string {
	if goos != "linux" {
		return DefaultMihomoAssetPattern
	}
	return LinuxMihomoAssetPattern
}

func mihomoAssetPatternFor(goos, repo string) string {
	if strings.EqualFold(strings.TrimSpace(repo), OfficialMihomoRepo) {
		if goos == "linux" {
			return OfficialLinuxMihomoAssetPattern
		}
	}
	return DefaultMihomoAssetPatternFor(goos)
}

func (c *Config) Validate() error {
	if len(c.Tailscale.InboundForwards) > 0 && !c.Tailscale.Enabled {
		return errors.New("Tailnet 入站转发要求先启用 Tailscale")
	}
	names := make(map[string]struct{}, len(c.Tailscale.InboundForwards))
	listeners := make(map[string]struct{}, len(c.Tailscale.InboundForwards))
	for index := range c.Tailscale.InboundForwards {
		forward := &c.Tailscale.InboundForwards[index]
		forward.Name = strings.TrimSpace(forward.Name)
		forward.Network = strings.ToLower(strings.TrimSpace(forward.Network))
		forward.Target = strings.TrimSpace(forward.Target)
		if forward.Name == "" {
			return fmt.Errorf("Tailnet 入站转发第 %d 项缺少名称", index+1)
		}
		if _, exists := names[forward.Name]; exists {
			return fmt.Errorf("Tailnet 入站转发名称重复: %s", forward.Name)
		}
		names[forward.Name] = struct{}{}
		if forward.Network != "tcp" && forward.Network != "udp" {
			return fmt.Errorf("Tailnet 入站转发 %s 的协议必须是 tcp 或 udp", forward.Name)
		}
		if forward.ListenPort < 1 || forward.ListenPort > 65535 {
			return fmt.Errorf("Tailnet 入站转发 %s 的监听端口必须在 1-65535", forward.Name)
		}
		listenerKey := forward.Network + "/" + strconv.Itoa(forward.ListenPort)
		if _, exists := listeners[listenerKey]; exists {
			return fmt.Errorf("Tailnet 入站端口重复: %s", listenerKey)
		}
		listeners[listenerKey] = struct{}{}
		host, port, err := net.SplitHostPort(forward.Target)
		if err != nil || strings.TrimSpace(host) == "" {
			return fmt.Errorf("Tailnet 入站转发 %s 的目标必须是 host:port", forward.Name)
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("Tailnet 入站转发 %s 的目标端口必须在 1-65535", forward.Name)
		}
	}
	return nil
}

func (c Config) StorageCopy() Config {
	c.ApplyDefaults()
	c.Providers = nil
	c.Targets = nil
	c.Publish = nil
	return c
}

func (c *Config) ApplySetup() {
	c.applySetup(runtime.GOOS)
}

func (c *Config) applySetup(goos string) {
	c.Setup.ProviderURL = strings.TrimSpace(c.Setup.ProviderURL)
	c.Setup.SubStoreURL = strings.TrimSpace(c.Setup.SubStoreURL)
	c.Setup.SubStoreBackend = strings.Trim(strings.TrimSpace(c.Setup.SubStoreBackend), "/")
	c.Setup.SubStoreFileName = strings.TrimSpace(c.Setup.SubStoreFileName)

	c.Providers = []Provider{{
		Name:     "main",
		Type:     "substore",
		URL:      c.Setup.ProviderURL,
		Path:     filepath.Join("providers", "main.yaml"),
		Interval: 3600,
	}}
	desktopTarget := DefaultMihomoTargetFor(goos)
	c.Targets = []Target{
		desktopTarget,
		{Name: "mobile", Type: "mobile-mihomo", Hostname: "mobile-meshmux", Output: filepath.Join("profiles", "mobile.yaml")},
	}
	c.Publish = []PublishTarget{{
		Name:     "mobile-substore",
		Type:     "substore-files",
		Input:    filepath.Join("profiles", "mobile.yaml"),
		BaseURL:  c.Setup.SubStoreURL,
		Backend:  c.Setup.SubStoreBackend,
		FileName: c.Setup.SubStoreFileName,
		TokenEnv: "MESHMUX_SUBSTORE_TOKEN",
	}}
}

func DefaultMihomoTarget() Target {
	return DefaultMihomoTargetFor(runtime.GOOS)
}

func DefaultMihomoTargetFor(goos string) Target {
	if DefaultTargetNameFor(goos) == "linux" {
		return Target{Name: "linux", Type: "linux-mihomo", Hostname: "linux-meshmux", Output: filepath.Join("profiles", "linux.yaml")}
	}
	return Target{Name: "windows", Type: "windows-mihomo", Hostname: "windows-meshmux", Output: filepath.Join("profiles", "windows.yaml")}
}

func DefaultTargetNameFor(goos string) string {
	if goos == "linux" {
		return "linux"
	}
	return "windows"
}

func (c *Config) deriveSetup() {
	if c.Setup.ProviderURL == "" {
		for _, provider := range c.Providers {
			if strings.TrimSpace(provider.URL) != "" {
				c.Setup.ProviderURL = strings.TrimSpace(provider.URL)
				break
			}
		}
	}
	if c.Setup.SubStoreURL != "" && c.Setup.SubStoreBackend != "" {
		return
	}
	for _, target := range c.Publish {
		if c.Setup.SubStoreURL == "" && target.BaseURL != "" && !strings.Contains(target.BaseURL, "example") {
			c.Setup.SubStoreURL = target.BaseURL
		}
		if c.Setup.SubStoreBackend == "" && target.Backend != "" {
			c.Setup.SubStoreBackend = target.Backend
		}
		if c.Setup.SubStoreFileName == "" && target.FileName != "" {
			c.Setup.SubStoreFileName = strings.TrimSpace(target.FileName)
		}
		if c.Setup.SubStoreURL == "" || c.Setup.SubStoreBackend == "" {
			c.deriveSubStoreFromAPIURL(target.APIURL)
			if !strings.Contains(target.BaseURL, "example") {
				c.deriveSubStoreFromAPIURL(joinLegacyAPI(target.BaseURL, target.APIPath))
			}
		}
	}
	if c.Setup.SubStoreURL == "" || c.Setup.SubStoreBackend == "" {
		c.deriveSubStoreFromProviderURL(c.Setup.ProviderURL)
	}
}

func (c *Config) deriveTailnetDomains() {
	root := rootDomain(c.Setup.SubStoreURL)
	if root == "" {
		root = rootDomain(c.Setup.ProviderURL)
	}
	if root == "" {
		return
	}
	domain := "*.i." + root
	c.Tailscale.Domains = replaceOrAppend(c.Tailscale.Domains, "*.i.example.com", domain)
	if c.DNS.NameserverPolicy != nil {
		if values, ok := c.DNS.NameserverPolicy["+.i.example.com"]; ok {
			delete(c.DNS.NameserverPolicy, "+.i.example.com")
			c.DNS.NameserverPolicy["+.i."+root] = values
		}
	}
}

func rootDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	parts := strings.Split(u.Hostname(), ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func replaceOrAppend(values []string, placeholder, value string) []string {
	if value == "" {
		return values
	}
	if len(values) == 0 {
		return []string{value}
	}
	for i, item := range values {
		if item == value {
			return values
		}
		if item == placeholder {
			values[i] = value
			return values
		}
	}
	return append(values, value)
}

func (c *Config) deriveSubStoreFromAPIURL(raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i+1] == "api" && parts[i+2] == "files" {
			if c.Setup.SubStoreBackend == "" {
				c.Setup.SubStoreBackend = parts[i]
			}
			if c.Setup.SubStoreURL == "" {
				u.Path = strings.Join(parts[:i], "/")
				u.RawQuery = ""
				u.Fragment = ""
				c.Setup.SubStoreURL = strings.TrimRight(u.String(), "/") + "/"
			}
			return
		}
	}
}

func (c *Config) deriveSubStoreFromProviderURL(raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return
	}
	if c.Setup.SubStoreBackend == "" {
		c.Setup.SubStoreBackend = parts[0]
	}
	if c.Setup.SubStoreURL == "" {
		u.Path = ""
		u.RawQuery = ""
		u.Fragment = ""
		c.Setup.SubStoreURL = strings.TrimRight(u.String(), "/") + "/"
	}
}

func joinLegacyAPI(base, path string) string {
	if base == "" || path == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return u.String()
}

func (c *Config) Target(name string) (Target, bool) {
	for _, target := range c.Targets {
		if target.Name == name {
			return target, true
		}
	}
	return Target{}, false
}

func (c *Config) PublishTarget(name string) (PublishTarget, bool) {
	for _, target := range c.Publish {
		if target.Name == name {
			return target, true
		}
	}
	return PublishTarget{}, false
}
