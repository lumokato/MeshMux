package webui

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/fileutil"
	"github.com/meshmux/meshmux/internal/generator"
	"github.com/meshmux/meshmux/internal/publisher"
	"github.com/meshmux/meshmux/internal/runner"
	"github.com/meshmux/meshmux/internal/updater"
	"github.com/meshmux/meshmux/internal/winservice"
)

type Server struct {
	ConfigPath string
	Token      string
	URL        string
	server     *http.Server
	done       chan error
	operations sync.Mutex
}

type platformUI struct {
	RuntimeTitle       string
	RuntimeTarget      string
	RuntimeTargetType  string
	RuntimeHostname    string
	RuntimeOutput      string
	ProxyModeLabel     string
	SubscriptionScope  string
	InboundScope       string
	InboundPlaceholder string
	SystemProxy        bool
	RuntimeActions     bool
}

func Start(configPath string) (*Server, error) {
	return StartAt(configPath, "127.0.0.1:0")
}

func StartAt(configPath, listenAddress string) (*Server, error) {
	if err := validateLoopbackAddress(listenAddress); err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	s := &Server{ConfigPath: configPath, Token: token, done: make(chan error, 1)}
	mux.HandleFunc("/", s.auth(s.index))
	mux.HandleFunc("/api/config", s.auth(s.configAPI))
	mux.HandleFunc("/api/action", s.auth(s.actionAPI))
	mux.HandleFunc("/api/status", s.auth(s.statusAPI))
	mux.HandleFunc("/api/wireguard/import", s.auth(s.importWireGuardAPI))

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, err
	}
	s.URL = "http://" + listener.Addr().String() + "/?token=" + token
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		err := s.server.Serve(listener)
		if err == http.ErrServerClosed {
			err = nil
		}
		s.done <- err
		close(s.done)
	}()
	return s, nil
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must use a loopback IP, got %q", host)
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return fmt.Errorf("invalid listen port %q", port)
	}
	if parsedPort > 65535 {
		return fmt.Errorf("invalid listen port %q", port)
	}
	return nil
}

func (s *Server) Done() <-chan error {
	return s.done
}

type statusItem struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type statusPayload struct {
	Items []statusItem `json:"items"`
	Logs  []string     `json:"logs"`
}

func (s *Server) statusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, _, err := config.Load(s.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, buildStatus(cfg))
}

func buildStatus(cfg *config.Config) statusPayload {
	return buildStatusFor(runtime.GOOS, cfg)
}

func buildStatusFor(goos string, cfg *config.Config) statusPayload {
	var items []statusItem
	add := func(label, value, state, detail string) {
		items = append(items, statusItem{Label: label, Value: value, State: state, Detail: detail})
	}
	controllerReady := runner.ControllerReady(cfg)
	pid, managedCoreRunning := runner.PID(cfg)
	if managedCoreRunning {
		add("核心进程", "运行中", "ok", "PID "+strconv.Itoa(pid))
	} else if goos == "windows" && winservice.Running() {
		if controllerReady {
			add("核心进程", "运行中", "ok", "Windows 服务运行中")
		} else {
			add("核心进程", "服务异常", "err", "Windows 服务运行，但控制接口不可用")
		}
	} else if goos == "linux" && controllerReady {
		add("核心进程", "运行中", "ok", "控制接口实时可用")
	} else {
		add("核心进程", "未运行", "warn", "")
	}
	tun := tunStatusFor(goos, cfg.TUN.Enabled, runtimeTUNEnabled(cfg), runner.CanStartTUN)
	add(tun.Label, tun.Value, tun.State, tun.Detail)
	if runner.ProxyEnabled() {
		add("系统代理", "开启", "ok", fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Mixed))
	} else {
		add("系统代理", "关闭", "muted", "")
	}
	if (goos == "windows" && winservice.AutostartEnabled()) || runner.AutostartEnabled() {
		add("开机自启", "开启", "ok", "")
	} else {
		add("开机自启", "关闭", "muted", "")
	}
	if tcpReady(fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Mixed)) {
		add("混合端口", "监听中", "ok", fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Mixed))
	} else {
		add("混合端口", "未监听", "warn", fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Mixed))
	}
	if controllerReady {
		add("控制接口", "可连接", "ok", "http://"+cfg.Ports.Controller)
	} else {
		add("控制接口", "不可连接", "warn", "http://"+cfg.Ports.Controller)
	}
	nodes := countProviderNodes(filepath.Join("providers", "main.yaml"))
	if nodes > 0 {
		add("订阅", fmt.Sprintf("%d 个节点", nodes), "ok", "")
	} else {
		add("订阅", "未配置", "warn", "")
	}
	if cfg.Tailscale.Enabled {
		tailnet := runtimeTailnetStatus(goos, cfg)
		if tailnet.connected {
			add("Tailnet", "已连接", "ok", fmt.Sprintf("%d 条路由，%d 个入站转发", len(cfg.Tailscale.Routes)+len(cfg.Tailscale.IPv6Routes), len(cfg.Tailscale.InboundForwards)))
		} else if tailnet.detail != "" {
			if strings.HasPrefix(tailnet.detail, "近期") {
				add("Tailnet", "路径异常", "warn", tailnet.detail)
			} else {
				add("Tailnet", "连接异常", "err", tailnet.detail)
			}
		} else if cfg.Tailscale.AuthKey != "" || cfg.Tailscale.AuthKeyFile != "" {
			add("Tailnet", "已配置，运行态需验证", "warn", fmt.Sprintf("%d 条路由，%d 个入站转发", len(cfg.Tailscale.Routes)+len(cfg.Tailscale.IPv6Routes), len(cfg.Tailscale.InboundForwards)))
		} else {
			add("Tailnet", "缺少 Auth Key", "warn", "")
		}
	} else {
		add("Tailnet", "关闭", "muted", "")
	}
	wg := generator.SummarizeWireGuard(cfg.WireGuard.Configs)
	if wg.ConfigCount == 0 {
		add("WireGuard", "未配置", "muted", "")
	} else if wg.ReadableCount == 0 {
		add("WireGuard", "读取失败", "err", fmt.Sprintf("%d 个配置文件", wg.ConfigCount))
	} else {
		add("WireGuard", fmt.Sprintf("%d 个配置", wg.ReadableCount), "ok", fmt.Sprintf("%d 个 peer", wg.PeerCount))
	}
	return statusPayload{Items: items, Logs: recentStatusLogs()}
}

func tunStatusFor(goos string, enabled, runtimeEnabled bool, canStartTUN func() bool) statusItem {
	if !enabled {
		return statusItem{Label: "TUN", Value: "关闭", State: "muted"}
	}
	if goos == "linux" {
		if runtimeEnabled {
			return statusItem{Label: "TUN", Value: "已启用", State: "ok", Detail: "核心实时配置已验证"}
		}
		return statusItem{Label: "TUN", Value: "已配置，等待重启", State: "warn", Detail: "重启核心后验证"}
	}
	if runtimeEnabled {
		return statusItem{Label: "TUN", Value: "已启用", State: "ok", Detail: "核心实时配置已验证"}
	}
	if canStartTUN() {
		return statusItem{Label: "TUN", Value: "已配置，等待重启", State: "warn", Detail: "重启核心后验证"}
	}
	return statusItem{Label: "TUN", Value: "权限不足", State: "err", Detail: "需要管理员权限"}
}

func runtimeTUNEnabled(cfg *config.Config) bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get("http://" + cfg.Ports.Controller + "/configs")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var current struct {
		TUN struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&current); err != nil {
		return false
	}
	return current.TUN.Enable
}

type tailnetRuntimeStatus struct {
	connected bool
	detail    string
}

func runtimeTailnetStatus(goos string, cfg *config.Config) tailnetRuntimeStatus {
	stateDir := filepath.Join("state", "tailscale")
	logPath := filepath.Join("logs", "mihomo.out.log")
	if goos == "windows" && winservice.Installed() {
		serviceHome := winservice.DataDir()
		stateDir = filepath.Join(serviceHome, "state", "tailscale")
		logPath = filepath.Join(serviceHome, "logs", "mihomo.out.log")
	}
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get("http://" + cfg.Ports.Controller + "/proxies/Tailnet")
	if err != nil {
		return tailnetRuntimeStatus{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tailnetRuntimeStatus{}
	}
	var proxy struct {
		Alive bool `json:"alive"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&proxy); err != nil {
		return tailnetRuntimeStatus{detail: "Tailnet 运行态响应无效"}
	}
	if !proxy.Alive {
		return tailnetRuntimeStatus{detail: "Tailnet 代理未就绪"}
	}
	evidence := recentTailnetEvidence(logPath, time.Now())
	if evidence.connected {
		return tailnetRuntimeStatus{connected: true}
	}
	if evidence.detail != "" {
		return evidence
	}
	if tailnetStateValid(stateDir) {
		return tailnetRuntimeStatus{connected: true}
	}
	return tailnetRuntimeStatus{}
}

func recentTailnetEvidence(path string, now time.Time) tailnetRuntimeStatus {
	data, err := os.ReadFile(path)
	if err != nil {
		return tailnetRuntimeStatus{}
	}
	const maxTail = 256 << 10
	if len(data) > maxTail {
		data = data[len(data)-maxTail:]
	}
	lines := strings.Split(string(data), "\n")
	cutoff := now.Add(-2 * time.Minute)
	consecutive := 0
	for index := len(lines) - 1; index >= 0; index-- {
		line := lines[index]
		if timestamp, ok := logTimestamp(line); ok && timestamp.Before(cutoff) {
			break
		}
		if strings.Contains(line, "using TS[Tailnet]") {
			return tailnetRuntimeStatus{connected: true}
		}
		if strings.Contains(line, "Authkey is set; but state is NoState") {
			return tailnetRuntimeStatus{detail: "Tailnet 身份状态未加载"}
		}
		if strings.Contains(line, "Start inbound forwards for proxy [Tailnet] failed") {
			return tailnetRuntimeStatus{detail: "Tailnet 入站启动失败"}
		}
		if strings.Contains(line, "dial TS") && (strings.Contains(line, "context deadline exceeded") || strings.Contains(line, "invalid Listen addr")) {
			consecutive++
			if consecutive >= 3 {
				return tailnetRuntimeStatus{detail: "近期 Tailnet 请求持续失败"}
			}
		}
	}
	return tailnetRuntimeStatus{}
}

func recentTailnetFailure(path string, now time.Time) string {
	evidence := recentTailnetEvidence(path, now)
	if evidence.connected {
		return ""
	}
	return evidence.detail
}

func tailnetStateValid(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "tailscaled.state"))
	if err != nil || len(data) == 0 {
		return false
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(data, &state) != nil {
		return false
	}
	for _, key := range []string{"_machinekey", "_current-profile", "_profiles"} {
		value, ok := state[key]
		if !ok || len(value) == 0 || string(value) == `""` || string(value) == "null" {
			return false
		}
	}
	return true
}

func logTimestamp(line string) (time.Time, bool) {
	const marker = `time="`
	start := strings.Index(line, marker)
	if start < 0 {
		return time.Time{}, false
	}
	start += len(marker)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, line[start:start+end])
	return parsed, err == nil
}

func tcpReady(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 700*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func countProviderNodes(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "  - ") {
			count++
		}
	}
	return count
}

func recentStatusLogs() []string {
	var lines []string
	for _, path := range []string{filepath.Join("logs", "meshmux.log"), filepath.Join("logs", "mihomo.err.log"), filepath.Join("logs", "mihomo.out.log")} {
		data, err := readStatusLogTail(path, 64*1024)
		if err != nil || len(data) == 0 {
			continue
		}
		parts := strings.Split(strings.TrimSpace(string(data)), "\n")
		for i := len(parts) - 1; i >= 0 && len(lines) < 8; i-- {
			line := strings.TrimSpace(parts[i])
			if line == "" {
				continue
			}
			if !strings.Contains(line, "level=warning") && !strings.Contains(line, "level=error") && !strings.Contains(line, "level=fatal") {
				continue
			}
			if isIgnorableStatusLog(line) {
				continue
			}
			lines = append(lines, runner.RedactLogText(line))
		}
	}
	if len(lines) == 0 {
		return []string{"暂无错误或警告"}
	}
	return lines
}

func readStatusLogTail(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 || maxBytes <= 0 {
		return nil, nil
	}
	length := info.Size()
	if length > maxBytes {
		length = maxBytes
	}
	data := make([]byte, int(length))
	n, err := file.ReadAt(data, info.Size()-length)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return data[:n], nil
}

func isIgnorableStatusLog(line string) bool {
	return strings.Contains(line, "dial TS") &&
		strings.Contains(line, "100.") &&
		strings.Contains(line, "connection refused")
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != s.Token && r.Header.Get("X-MeshMux-Token") != s.Token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet {
			if !s.operations.TryLock() {
				http.Error(w, "another operation is in progress", http.StatusConflict)
				return
			}
			defer s.operations.Unlock()
			unlock, err := fileutil.TryLock(filepath.Join(filepath.Dir(s.ConfigPath), "state", "config-operation.lock"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			defer unlock()
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		}
		next(w, r)
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, renderIndexHTML(runtime.GOOS))
}

func renderIndexHTML(goos string) string {
	ui := platformUIFor(goos)
	replacer := strings.NewReplacer(
		"{{RUNTIME_TITLE}}", ui.RuntimeTitle,
		"{{RUNTIME_TARGET}}", ui.RuntimeTarget,
		"{{RUNTIME_TARGET_TYPE}}", ui.RuntimeTargetType,
		"{{RUNTIME_HOSTNAME}}", ui.RuntimeHostname,
		"{{RUNTIME_OUTPUT}}", ui.RuntimeOutput,
		"{{PROXY_MODE_LABEL}}", ui.ProxyModeLabel,
		"{{SUBSCRIPTION_SCOPE}}", ui.SubscriptionScope,
		"{{INBOUND_SCOPE}}", ui.InboundScope,
		"{{INBOUND_PLACEHOLDER}}", ui.InboundPlaceholder,
		"{{SYSTEM_PROXY_CLASS}}", boolClass(ui.SystemProxy),
		"{{RUNTIME_ACTION_HIDDEN}}", hiddenAttribute(ui.RuntimeActions),
	)
	return replacer.Replace(indexHTML)
}

func platformUIFor(goos string) platformUI {
	if goos == "linux" {
		return platformUI{
			RuntimeTitle:       "Linux 服务器运行",
			RuntimeTarget:      "linux",
			RuntimeTargetType:  "linux-mihomo",
			RuntimeHostname:    "linux-meshmux",
			RuntimeOutput:      "profiles/linux.yaml",
			ProxyModeLabel:     "代理端口（固定）",
			SubscriptionScope:  "Linux 服务器和手机共用",
			InboundScope:       "Linux 主机的局域网或公网网卡",
			InboundPlaceholder: "linux-ssh,tcp,22,127.0.0.1:22",
			SystemProxy:        false,
			RuntimeActions:     false,
		}
	}
	return platformUI{
		RuntimeTitle:       "Windows 运行方式",
		RuntimeTarget:      "windows",
		RuntimeTargetType:  "windows-mihomo",
		RuntimeHostname:    "windows-meshmux",
		RuntimeOutput:      "profiles/windows.yaml",
		ProxyModeLabel:     "系统代理",
		SubscriptionScope:  "Windows 和手机共用",
		InboundScope:       "Windows 局域网或公网网卡",
		InboundPlaceholder: "windows-ssh,tcp,22,127.0.0.1:22",
		SystemProxy:        true,
		RuntimeActions:     true,
	}
}

func boolClass(show bool) string {
	if show {
		return ""
	}
	return "hidden"
}

func hiddenAttribute(show bool) string {
	if show {
		return ""
	}
	return "hidden"
}

func platformActionAllowed(goos, action string) bool {
	switch action {
	case "start", "stop", "proxy-on", "proxy-off", "dashboard":
		return platformUIFor(goos).RuntimeActions
	default:
		return true
	}
}

func (s *Server) configAPI(w http.ResponseWriter, r *http.Request) {
	s.configAPIFor(runtime.GOOS, w, r)
}

func (s *Server) configAPIFor(goos string, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, path, err := config.Load(s.ConfigPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		stored := cfg.StorageCopy()
		data, err := json.MarshalIndent(stored, "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"path": path, "content": string(data) + "\n", "config": stored})
	case http.MethodPost:
		var req struct {
			Content string         `json:"content"`
			Config  *config.Config `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Config != nil {
			path := s.ConfigPath
			if path == "" {
				path = config.DefaultConfigPath
			}
			req.Config.ApplyDefaults()
			if err := req.Config.Validate(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := saveConfig(path, req.Config); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{"ok": true, "path": path})
			return
		} else {
			var parsed config.Config
			if err := json.Unmarshal([]byte(req.Content), &parsed); err != nil {
				http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
				return
			}
			parsed.ApplyDefaults()
			if err := parsed.Validate(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			path := s.ConfigPath
			if path == "" {
				path = config.DefaultConfigPath
			}
			if err := saveConfig(path, &parsed); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{"ok": true, "path": path})
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type importWireGuardRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (s *Server) importWireGuardAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
	var req importWireGuardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	content := normalizeWireGuardContent(req.Content)
	if err := validateWireGuardContent(content); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, resolved, err := config.Load(s.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writePath, storedPath, err := wireGuardStoragePath(resolved, req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.MkdirAll(filepath.Dir(writePath), 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(writePath, []byte(content), 0600); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !containsPath(cfg.WireGuard.Configs, storedPath) {
		cfg.WireGuard.Configs = append(cfg.WireGuard.Configs, storedPath)
	}
	if err := saveConfig(resolved, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("WireGuard 已导入: %s", filepath.Base(writePath)),
		"config":  cfg.StorageCopy(),
		"summary": generator.SummarizeWireGuard(cfg.WireGuard.Configs),
	})
}

func (s *Server) actionAPI(w http.ResponseWriter, r *http.Request) {
	s.actionAPIFor(runtime.GOOS, w, r)
}

func (s *Server) actionAPIFor(goos string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action string `json:"action"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !platformActionAllowed(goos, req.Action) {
		http.Error(w, fmt.Sprintf("Linux Web 管理页不允许执行 %s；请通过 systemd 或桌面会话管理", req.Action), http.StatusForbidden)
		return
	}
	cfg, resolved, err := config.Load(s.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var message string
	switch req.Action {
	case "generate":
		if req.Target == "" || req.Target == "all" {
			written, err := generator.GenerateAll(cfg)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			message = fmt.Sprintf("已生成: %v", written)
		} else {
			path, err := generator.GenerateNamed(cfg, req.Target)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			message = "已生成: " + path
		}
	case "start":
		if goos == "windows" {
			action := "activate"
			if winservice.Installed() {
				action = "restart"
			}
			if err := winservice.RunElevated(action, s.ConfigPath); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			message = "核心服务已启动"
			break
		}
		target := req.Target
		if target == "" {
			target = platformUIFor(runtime.GOOS).RuntimeTarget
		}
		path, err := generator.GenerateNamed(cfg, target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := runner.Start(cfg, path); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message = "核心已启动"
	case "stop":
		if goos == "windows" && winservice.Installed() {
			if err := winservice.RunElevated("stop", ""); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			message = "已请求停止核心服务"
			break
		}
		if err := runner.Stop(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message = "核心已停止"
	case "proxy-on":
		if err := runner.Proxy("on", cfg.Ports.Mixed); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message = "系统代理已开启"
	case "proxy-off":
		if err := runner.Proxy("off", cfg.Ports.Mixed); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message = "系统代理已关闭"
	case "dashboard":
		if err := runner.Dashboard(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message = "已打开 MetaCubeXD"
	case "download-mihomo":
		path, err := updater.Download(cfg.Components.Mihomo, "mihomo")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := runner.MarkMihomoDownloaded(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message = "mihomo 已安装: " + path
	case "download-dashboard":
		path, err := updater.Download(cfg.Components.Dashboard, "dashboard")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message = "MetaCubeXD 已安装: " + path
	case "probe-substore":
		target, ok := cfg.PublishTarget("mobile-substore")
		if !ok {
			http.Error(w, "Sub-Store 发布配置不存在", http.StatusBadRequest)
			return
		}
		result, err := publisher.Probe(target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message = fmt.Sprintf("Sub-Store 验证通过: %s HTTP %d", result.URL, result.StatusCode)
	case "publish-mobile":
		profile, err := generator.GenerateNamed(cfg, "mobile")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		target, ok := cfg.PublishTarget("mobile-substore")
		if !ok {
			http.Error(w, "Sub-Store 发布配置不存在", http.StatusBadRequest)
			return
		}
		target.Input = profile
		result, err := publisher.Publish(target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if cfg.Setup.SubStoreFileName == "" && result.FileName != "" {
			cfg.Setup.SubStoreFileName = result.FileName
			if err := saveConfig(resolved, cfg); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		message = fmt.Sprintf("手机配置已上传: %s sha256=%s", result.URL, result.SHA256)
	case "status":
		message = runner.Status(cfg)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": message})
}

func saveConfig(path string, cfg *config.Config) error {
	if path == "" {
		path = config.DefaultConfigPath
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := generator.RefreshProviders(cfg); err != nil {
		return err
	}
	stored := cfg.StorageCopy()
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFile(path, append(data, '\n'), 0600)
}

func normalizeWireGuardContent(content string) string {
	content = strings.TrimPrefix(content, "\ufeff")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content) + "\n"
}

func validateWireGuardContent(content string) error {
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "[interface]") || !strings.Contains(lower, "privatekey") {
		return fmt.Errorf("不是有效的 WireGuard 配置")
	}
	if !strings.Contains(lower, "[peer]") || !strings.Contains(lower, "publickey") {
		return fmt.Errorf("WireGuard 配置缺少 Peer")
	}
	return nil
}

func wireGuardStoragePath(configPath, originalName string) (string, string, error) {
	configDir := filepath.Dir(configPath)
	if configDir == "." || configDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		configDir = cwd
	}
	writePath := filepath.Join(configDir, "wireguard", safeWireGuardFilename(originalName))
	storedPath := writePath
	if cwd, err := os.Getwd(); err == nil {
		if rel, relErr := filepath.Rel(cwd, writePath); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			storedPath = filepath.Clean(rel)
		}
	}
	return writePath, storedPath, nil
}

func safeWireGuardFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "wireguard.conf"
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	var b strings.Builder
	lastDash := false
	for _, r := range stem {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	stem = strings.Trim(b.String(), "-.")
	if stem == "" {
		stem = "wireguard"
	}
	return stem + ".conf"
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(want) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func randomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

//go:embed index.html
var indexHTML string
