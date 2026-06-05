package webui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/generator"
	"github.com/meshmux/meshmux/internal/publisher"
	"github.com/meshmux/meshmux/internal/runner"
	"github.com/meshmux/meshmux/internal/updater"
)

type Server struct {
	ConfigPath string
	Token      string
	URL        string
	server     *http.Server
}

func Start(configPath string) (*Server, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	s := &Server{ConfigPath: configPath, Token: token}
	mux.HandleFunc("/", s.auth(s.index))
	mux.HandleFunc("/api/config", s.auth(s.configAPI))
	mux.HandleFunc("/api/action", s.auth(s.actionAPI))
	mux.HandleFunc("/api/status", s.auth(s.statusAPI))
	mux.HandleFunc("/api/wireguard/import", s.auth(s.importWireGuardAPI))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.URL = "http://" + listener.Addr().String() + "/?token=" + token
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		_ = s.server.Serve(listener)
	}()
	return s, nil
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
	var items []statusItem
	add := func(label, value, state, detail string) {
		items = append(items, statusItem{Label: label, Value: value, State: state, Detail: detail})
	}
	if pid, ok := runner.PID(); ok && runner.IsRunning() {
		add("核心进程", "运行中", "ok", "PID "+strconv.Itoa(pid))
	} else {
		add("核心进程", "未运行", "warn", "")
	}
	if cfg.TUN.Enabled {
		if runner.CanStartTUN() {
			add("TUN", "已启用", "ok", "管理员权限可用")
		} else {
			add("TUN", "权限不足", "err", "需要管理员权限")
		}
	} else {
		add("TUN", "关闭", "muted", "")
	}
	if runner.ProxyEnabled() {
		add("系统代理", "开启", "ok", fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Mixed))
	} else {
		add("系统代理", "关闭", "muted", "")
	}
	if runner.AutostartEnabled() {
		add("开机自启", "开启", "ok", "")
	} else {
		add("开机自启", "关闭", "muted", "")
	}
	if tcpReady(fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Mixed)) {
		add("混合端口", "监听中", "ok", fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Mixed))
	} else {
		add("混合端口", "未监听", "warn", fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Mixed))
	}
	if runner.ControllerReady(cfg) {
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
		if cfg.Tailscale.AuthKey != "" || cfg.Tailscale.AuthKeyFile != "" {
			add("Tailnet", "已启用", "ok", fmt.Sprintf("%d 条路由", len(cfg.Tailscale.Routes)+len(cfg.Tailscale.IPv6Routes)))
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

var urlPattern = regexp.MustCompile(`https?://[^\s'"]+`)

func recentStatusLogs() []string {
	var lines []string
	for _, path := range []string{filepath.Join("logs", "meshmux.log"), filepath.Join("logs", "mihomo.err.log"), filepath.Join("logs", "mihomo.out.log")} {
		data, err := os.ReadFile(path)
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
			lines = append(lines, urlPattern.ReplaceAllString(line, "[url-hidden]"))
		}
	}
	if len(lines) == 0 {
		return []string{"暂无错误或警告"}
	}
	return lines
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
		next(w, r)
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, indexHTML)
}

func (s *Server) configAPI(w http.ResponseWriter, r *http.Request) {
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
		content := req.Content
		if req.Config != nil {
			path := s.ConfigPath
			if path == "" {
				path = config.DefaultConfigPath
			}
			if err := saveConfig(path, req.Config); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"ok": true, "path": path})
			return
		} else {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(req.Content), &parsed); err != nil {
				http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		path := s.ConfigPath
		if path == "" {
			path = config.DefaultConfigPath
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "path": path})
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
		target := req.Target
		if target == "" {
			target = "windows"
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
		if err := runner.Stop(); err != nil {
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
	if strings.TrimSpace(cfg.Setup.ProviderURL) != "" {
		if err := generator.RefreshProviders(cfg); err != nil {
			return err
		}
	}
	stored := cfg.StorageCopy()
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
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

const indexHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>MeshMux</title>
  <style>
    :root { color-scheme: light dark; font-family: "Microsoft YaHei UI", "Segoe UI", sans-serif; --bg:#f6f7f9; --panel:#fff; --text:#18202a; --muted:#667085; --line:#d9dee7; --accent:#2563eb; --ok:#15803d; --warn:#b45309; --danger:#b91c1c; --soft:#eef2f7; }
    @media (prefers-color-scheme: dark) { :root { --bg:#101316; --panel:#171b20; --text:#eef2f6; --muted:#9aa4b2; --line:#2a3038; --accent:#60a5fa; --ok:#4ade80; --warn:#f59e0b; --danger:#f87171; } }
    * { box-sizing: border-box; }
    body { margin:0; min-height:100vh; background:var(--bg); color:var(--text); font-size:14px; -webkit-font-smoothing:antialiased; text-rendering:geometricPrecision; }
    .shell { min-height:100vh; display:grid; grid-template-columns:230px minmax(0,1fr); }
    aside { border-right:1px solid var(--line); background:var(--panel); padding:18px 14px; display:flex; flex-direction:column; gap:16px; }
    .brand { display:flex; align-items:center; gap:10px; padding:4px 6px 12px; border-bottom:1px solid var(--line); }
    .mark { width:30px; height:30px; border-radius:7px; background:var(--accent); color:white; display:grid; place-items:center; font-weight:800; }
    .brand strong { font-size:17px; }
    nav { display:grid; gap:6px; }
    nav button { justify-content:flex-start; border:0; background:transparent; color:var(--text); }
    nav button.active { background:color-mix(in srgb, var(--accent) 13%, transparent); color:var(--accent); }
    main { padding:22px; display:grid; gap:16px; align-content:start; max-width:1120px; width:100%; }
    header { display:flex; justify-content:space-between; gap:16px; align-items:flex-start; }
    h1 { margin:0; font-size:24px; line-height:1.2; }
    .sub { color:var(--muted); margin-top:6px; }
    .path { color:var(--muted); font-size:12px; text-align:right; overflow-wrap:anywhere; max-width:430px; }
    section { display:none; gap:16px; }
    section.active { display:grid; }
    .panel { background:var(--panel); border:1px solid var(--line); border-radius:8px; padding:16px; display:grid; gap:14px; }
    .panel h2 { margin:0; font-size:16px; }
    .grid { display:grid; grid-template-columns:repeat(2,minmax(240px,1fr)); gap:12px; }
    label { display:grid; gap:6px; font-weight:600; }
    label span, .hint { color:var(--muted); font-size:12px; font-weight:400; }
    input, textarea, select { width:100%; min-height:36px; border:1px solid var(--line); border-radius:7px; background:Canvas; color:CanvasText; padding:8px 10px; font:inherit; }
    textarea { min-height:220px; font-family:Consolas, ui-monospace, monospace; line-height:1.45; }
    .switch { display:flex; align-items:center; gap:10px; font-weight:600; }
    .switch input { width:18px; min-height:18px; }
    .seg { display:flex; border:1px solid var(--line); border-radius:8px; overflow:hidden; width:max-content; }
    .seg button { border:0; border-radius:0; background:transparent; min-width:108px; }
    .seg button.active { background:var(--accent); color:white; }
    .actions { display:flex; flex-wrap:wrap; gap:8px; }
    button { min-height:34px; border:1px solid var(--line); border-radius:7px; background:var(--panel); color:var(--text); padding:8px 12px; cursor:pointer; font:inherit; display:inline-flex; align-items:center; justify-content:center; gap:7px; }
    button.primary { background:var(--accent); color:white; border-color:var(--accent); }
    button:hover { border-color:var(--accent); }
    .notice { border:1px solid color-mix(in srgb, var(--warn) 45%, var(--line)); background:color-mix(in srgb, var(--warn) 10%, var(--panel)); border-radius:8px; padding:10px 12px; color:var(--text); display:none; }
    .notice.show { display:block; }
    .status { white-space:pre-wrap; min-height:46px; border:1px solid var(--line); border-radius:8px; background:var(--panel); padding:12px; color:var(--muted); }
    .status.ok { color:var(--ok); }
    .status.err { color:var(--danger); }
    .cards { display:grid; grid-template-columns:repeat(3,minmax(170px,1fr)); gap:10px; }
    .metric { border:1px solid var(--line); border-radius:8px; padding:12px; background:var(--panel); display:grid; gap:5px; min-height:92px; }
    .metric .label { color:var(--muted); font-size:12px; }
    .metric .value { font-size:18px; font-weight:700; }
    .metric .detail { color:var(--muted); font-size:12px; overflow-wrap:anywhere; }
    .metric.ok { border-color:color-mix(in srgb, var(--ok) 35%, var(--line)); }
    .metric.warn { border-color:color-mix(in srgb, var(--warn) 45%, var(--line)); }
    .metric.err { border-color:color-mix(in srgb, var(--danger) 45%, var(--line)); }
    .loglist { display:grid; gap:8px; }
    .logline { border:1px solid var(--line); border-radius:8px; padding:9px 10px; color:var(--muted); overflow-wrap:anywhere; font-family:Consolas, ui-monospace, monospace; font-size:12px; line-height:1.45; }
    .compact { display:grid; gap:10px; }
    @media (max-width:900px) { .cards { grid-template-columns:repeat(2,minmax(150px,1fr)); } }
    @media (max-width:800px) { .shell { grid-template-columns:1fr; } aside { border-right:0; border-bottom:1px solid var(--line); } nav { grid-template-columns:repeat(3,1fr); } main { padding:14px; } header { display:grid; } .path { text-align:left; } .grid { grid-template-columns:1fr; } .cards { grid-template-columns:1fr; } }
  </style>
</head>
<body>
  <div class="shell">
    <aside>
      <div class="brand"><div class="mark">M</div><div><strong>MeshMux</strong><div class="hint">mihomo runner</div></div></div>
      <nav>
        <button class="active" data-tab="quick" onclick="showTab('quick')">快速设置</button>
        <button data-tab="advanced" onclick="showTab('advanced')">高级</button>
        <button data-tab="status" onclick="showTab('status')">状态</button>
      </nav>
    </aside>
    <main>
      <header>
        <div><h1>快速设置</h1><div class="sub">只填写首次运行必须的信息，其他配置由 MeshMux 生成。</div></div>
        <div id="path" class="path"></div>
      </header>

      <section id="tab-quick" class="active">
        <div class="panel">
          <h2>订阅与手机同步</h2>
          <div class="grid">
            <label>日常代理订阅链接
              <input id="providerUrl" placeholder="https://sub-store/download/collection/...">
              <span>用于生成 Windows 和手机共用的 mihomo 配置。</span>
            </label>
            <label>Sub-Store 地址
              <input id="subStoreUrl" placeholder="https://substore.example/">
              <span>站点根地址。</span>
            </label>
            <label>后端名
              <input id="subStoreBackend" placeholder="backend">
            </label>
            <label>Tailnet Auth Key
              <input id="tsAuthKey" type="password" placeholder="可选，启用 Tailnet 时填写">
              <span>启用 Tailnet 时填写；不填则只生成普通代理配置。</span>
            </label>
          </div>
          <label class="switch"><input id="tsEnabled" type="checkbox">启用 Tailnet 出站</label>
          <div class="actions">
            <button class="primary" onclick="saveQuick()">保存</button>
            <button onclick="saveThen('probe-substore')">验证 Sub-Store</button>
            <button onclick="saveThen('publish-mobile')">生成并上传手机配置</button>
          </div>
        </div>

        <div class="panel">
          <h2>WireGuard</h2>
          <div class="grid">
            <label>WireGuard .conf
              <input id="wgFile" type="file" accept=".conf,text/plain" multiple>
              <span id="wgSummary">未导入配置</span>
            </label>
          </div>
          <div class="actions">
            <button onclick="importWireGuard()">导入 WireGuard 配置</button>
          </div>
        </div>

        <div class="panel">
          <h2>Windows 运行方式</h2>
          <div class="seg">
            <button id="modeProxy" onclick="setMode('proxy')">系统代理</button>
            <button id="modeTun" onclick="setMode('tun')">TUN</button>
          </div>
          <div class="actions">
            <button onclick="runAction('download-mihomo')">下载/更新 mihomo</button>
            <button onclick="runAction('download-dashboard')">下载/更新 MetaCubeXD</button>
            <button class="primary" onclick="saveThen('start','windows')">启动核心</button>
            <button onclick="runAction('stop')">停止核心</button>
            <button onclick="runAction('proxy-on')">系统代理开</button>
            <button onclick="runAction('proxy-off')">系统代理关</button>
          </div>
        </div>
      </section>

      <section id="tab-advanced">
        <div class="panel">
          <h2>高级选项</h2>
          <div class="grid">
            <label>混合代理端口<input id="mixedPort" type="number" min="1" max="65535"></label>
            <label>控制器地址<input id="controller"></label>
            <label>MagicDNS 后缀<input id="tsMagic" placeholder="tailnet.ts.net"></label>
            <label>Tailnet 域名规则<input id="tsDomains" placeholder="*.ts.net, *.example.ts.net"></label>
          </div>
        </div>
        <div class="panel">
          <h2>配置 JSON</h2>
          <textarea id="advancedJson" spellcheck="false"></textarea>
          <div class="actions">
            <button onclick="applyJson()">从 JSON 应用</button>
            <button onclick="saveJson()">保存 JSON</button>
          </div>
        </div>
      </section>

      <section id="tab-status">
        <div class="panel">
          <h2>状态概览</h2>
          <div id="statusCards" class="cards"></div>
          <div class="actions">
            <button onclick="refreshStatus()">刷新状态</button>
            <button onclick="runAction('dashboard')">打开 MetaCubeXD</button>
          </div>
        </div>
        <div class="panel">
          <h2>最近警告</h2>
          <div id="statusLogs" class="loglist"></div>
        </div>
      </section>

      <div id="status" class="status">正在加载配置...</div>
    </main>
  </div>
  <script>
    const token = new URLSearchParams(location.search).get('token') || '';
    const headers = {'Content-Type':'application/json','X-MeshMux-Token':token};
    let cfg = {};
    let mode = 'proxy';
    function el(id){ return document.getElementById(id); }
    function val(id){ return el(id).value.trim(); }
    function setVal(id,v){ el(id).value = v == null ? '' : String(v); }
    function ok(text){ const s=el('status'); s.className='status ok'; s.textContent=text; }
    function err(text){ const s=el('status'); s.className='status err'; s.textContent=text; }
    function info(text){ const s=el('status'); s.className='status'; s.textContent=text; }
    function ensure(c){
      c.name ||= 'default'; c.setup ||= {}; c.ports ||= {}; c.paths ||= {}; c.providers ||= [];
      c.tun ||= {}; c.dns ||= {}; c.rules ||= {}; c.tailscale ||= {}; c.wireguard ||= {};
      c.wireguard.configs ||= [];
      c.components ||= {}; c.components.mihomo ||= {}; c.components.dashboard ||= {};
      c.targets ||= []; c.publish ||= []; return c;
    }
    function deriveSetup(c){
      ensure(c);
      const pub = c.publish.find(p => p.type === 'substore-files') || {};
      if (!c.setup.subStoreUrl && pub.baseUrl && !pub.baseUrl.includes('example')) c.setup.subStoreUrl = pub.baseUrl;
      if (!c.setup.subStoreBackend && pub.backend) c.setup.subStoreBackend = pub.backend;
      if (!c.setup.subStoreFileName && pub.fileName) c.setup.subStoreFileName = pub.fileName;
    }
    function storageConfig(c){
      const out = JSON.parse(JSON.stringify(ensure(c)));
      delete out.providers;
      delete out.targets;
      delete out.publish;
      return out;
    }
    async function load(){
      const r = await fetch('/api/config?token=' + encodeURIComponent(token));
      const text = await r.text();
      if (!r.ok) throw new Error(text);
      const j = JSON.parse(text);
      cfg = ensure(j.config || JSON.parse(j.content));
      deriveSetup(cfg);
      el('path').textContent = j.path;
      fill();
      ok('配置已加载');
    }
    function fill(){
      ensure(cfg); deriveSetup(cfg);
      setVal('providerUrl', cfg.setup.providerUrl || '');
      setVal('subStoreUrl', cfg.setup.subStoreUrl || '');
      setVal('subStoreBackend', cfg.setup.subStoreBackend || '');
      el('tsEnabled').checked = !!cfg.tailscale.enabled;
      setVal('tsAuthKey', cfg.tailscale.authKey || '');
      mode = cfg.tun.enabled ? 'tun' : 'proxy';
      setMode(mode);
      setVal('mixedPort', cfg.ports.mixed || 2080);
      setVal('controller', cfg.ports.controller || '127.0.0.1:9090');
      setVal('tsMagic', cfg.tailscale.magicDnsSuffix || '');
      setVal('tsDomains', (cfg.tailscale.domains || ['*.ts.net']).join(', '));
      refreshWireGuardSummary();
      el('advancedJson').value = JSON.stringify(storageConfig(cfg), null, 2);
    }
    function collect(){
      ensure(cfg);
      cfg.setup.providerUrl = val('providerUrl');
      cfg.setup.subStoreUrl = val('subStoreUrl').replace(/\/+$/, '') + (val('subStoreUrl') ? '/' : '');
      cfg.setup.subStoreBackend = val('subStoreBackend').replace(/^\/+|\/+$/g, '');
      cfg.setup.subStoreFileName ||= (cfg.publish.find(p => p.type === 'substore-files') || {}).fileName || '';
      cfg.ports.mixed = Number(val('mixedPort')) || 2080;
      cfg.ports.controller = val('controller') || '127.0.0.1:9090';
      cfg.providers = [{name:'main', type:'substore', url:cfg.setup.providerUrl, path:'providers/main.yaml', interval:3600}];
      cfg.targets = [
        {name:'windows', type:'windows-mihomo', hostname:'windows-meshmux', output:'profiles/windows.yaml'},
        {name:'mobile', type:'mobile-mihomo', hostname:'mobile-meshmux', output:'profiles/mobile.yaml'}
      ];
      cfg.publish = [{name:'mobile-substore', type:'substore-files', input:'profiles/mobile.yaml', baseUrl:cfg.setup.subStoreUrl, backend:cfg.setup.subStoreBackend, fileName:cfg.setup.subStoreFileName || '', tokenEnv:'MESHMUX_SUBSTORE_TOKEN'}];
      cfg.tailscale.enabled = el('tsEnabled').checked;
      cfg.tailscale.controlUrl ||= 'https://controlplane.tailscale.com';
      cfg.tailscale.authKey = val('tsAuthKey');
      cfg.tailscale.acceptRoutes = true;
      cfg.tailscale.routes = cfg.tailscale.routes?.length ? cfg.tailscale.routes : ['100.64.0.0/10'];
      cfg.tailscale.ipv6Routes = cfg.tailscale.ipv6Routes?.length ? cfg.tailscale.ipv6Routes : ['fd7a:115c:a1e0::/48'];
      cfg.tailscale.magicDnsSuffix = val('tsMagic');
      cfg.tailscale.domains = val('tsDomains').split(',').map(s => s.trim()).filter(Boolean);
      cfg.tun.enabled = mode === 'tun';
      cfg.tun.stack = 'mixed';
      cfg.tun.autoRoute = mode === 'tun';
      cfg.tun.autoDetectInterface = true;
      cfg.tun.strictRoute = false;
      cfg.dns.enabled = true;
      cfg.rules.directCidrs ||= ['127.0.0.0/8','10.0.0.0/8','172.16.0.0/12','192.168.0.0/16','169.254.0.0/16'];
      cfg.rules.directDomains ||= ['localhost','*.local'];
      el('advancedJson').value = JSON.stringify(storageConfig(cfg), null, 2);
      return cfg;
    }
    function setMode(next){
      mode = next;
      el('modeProxy').classList.toggle('active', mode === 'proxy');
      el('modeTun').classList.toggle('active', mode === 'tun');
    }
    function showTab(name){
      document.querySelectorAll('section').forEach(s => s.classList.remove('active'));
      document.querySelectorAll('nav button').forEach(b => b.classList.toggle('active', b.dataset.tab === name));
      el('tab-' + name).classList.add('active');
      if (name === 'advanced') el('advancedJson').value = JSON.stringify(storageConfig(collect()), null, 2);
      if (name === 'status') refreshStatus();
    }
    async function saveQuick(){
      try {
        collect();
        const r = await fetch('/api/config?token=' + encodeURIComponent(token), {method:'POST', headers, body:JSON.stringify({config:cfg})});
        const text = await responseText(r);
        if (!r.ok) return err(text);
        el('advancedJson').value = JSON.stringify(storageConfig(cfg), null, 2);
        ok(text || '配置已保存');
        return true;
      } catch(e) { err(e.message); return false; }
    }
    async function saveThen(action,target){
      if (!await saveQuick()) return;
      await runAction(action,target);
    }
    async function importWireGuard(){
      const files = Array.from(el('wgFile').files || []);
      if (!files.length) return err('请选择 WireGuard .conf 文件');
      if (!await saveQuick()) return;
      try {
        info('正在导入 WireGuard...');
        let imported = 0;
        for (const file of files) {
          const content = await file.text();
          const r = await fetch('/api/wireguard/import?token=' + encodeURIComponent(token), {
            method:'POST',
            headers,
            body:JSON.stringify({name:file.name, content})
          });
          const text = await r.text();
          if (!r.ok) return err(text);
          const j = JSON.parse(text);
          cfg = ensure(j.config || cfg);
          imported++;
        }
        fill();
        ok('WireGuard 已导入: ' + imported + ' 个配置');
        refreshStatus();
      } catch(e) { err(e.message); }
    }
    async function saveJson(){
      try {
        cfg = ensure(JSON.parse(el('advancedJson').value));
        const r = await fetch('/api/config?token=' + encodeURIComponent(token), {method:'POST', headers, body:JSON.stringify({config:cfg})});
        const text = await responseText(r);
        if (!r.ok) return err(text);
        fill(); ok(text || 'JSON 已保存');
      } catch(e) { err('JSON 无效: ' + e.message); }
    }
    function applyJson(){
      try { cfg = ensure(JSON.parse(el('advancedJson').value)); fill(); ok('JSON 已应用'); }
      catch(e) { err('JSON 无效: ' + e.message); }
    }
    function refreshWireGuardSummary(){
      const n = (cfg.wireguard?.configs || []).length;
      el('wgSummary').textContent = n ? (n + ' 个配置已导入') : '未导入配置';
    }
    async function runAction(action,target){
      info('正在执行...');
      const r = await fetch('/api/action?token=' + encodeURIComponent(token), {method:'POST', headers, body:JSON.stringify({action, target:target || ''})});
      const text = await responseText(r);
      if (!r.ok) err(text); else ok(text);
      if (action === 'start' || action === 'stop' || action === 'proxy-on' || action === 'proxy-off') refreshStatus();
    }
    async function refreshStatus(){
      const r = await fetch('/api/status?token=' + encodeURIComponent(token));
      const text = await r.text();
      if (!r.ok) return err(text);
      const data = JSON.parse(text);
      el('statusCards').innerHTML = (data.items || []).map(item =>
        '<div class="metric ' + escapeHtml(item.state || '') + '">' +
        '<div class="label">' + escapeHtml(item.label) + '</div>' +
        '<div class="value">' + escapeHtml(item.value) + '</div>' +
        '<div class="detail">' + escapeHtml(item.detail || '') + '</div>' +
        '</div>'
      ).join('');
      el('statusLogs').innerHTML = (data.logs || []).map(line =>
        '<div class="logline">' + escapeHtml(line) + '</div>'
      ).join('');
    }
    async function responseText(r){
      const text = await r.text();
      try { const j = JSON.parse(text); return j.message || JSON.stringify(j, null, 2); }
      catch { return text; }
    }
    function escapeHtml(s){
      return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
    }
    load().catch(e => err(e.message));
  </script>
</body>
</html>`
