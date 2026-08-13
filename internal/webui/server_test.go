package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

func TestIndexHTMLIsChineseFormUI(t *testing.T) {
	html := renderIndexHTML("windows")
	for _, want := range []string{"快速设置", "Sub-Store 地址", "后端名", "生成并上传手机配置", "导入 WireGuard 配置", "multiple", "状态概览", "Tailnet 入站转发", "tsInboundForwards"} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered HTML missing %q", want)
		}
	}
	for _, private := range []string{"private-backend-name", "/api/files", "TUN 模式通常"} {
		if strings.Contains(html, private) {
			t.Fatalf("rendered HTML should not contain %q", private)
		}
	}
}

func TestPlatformUIRendersLinuxRuntimeWithoutSystemProxy(t *testing.T) {
	html := renderIndexHTML("linux")
	for _, want := range []string{
		"Linux 服务器运行",
		"代理端口（固定）",
		"const runtimeTarget = 'linux'",
		"type:'linux-mihomo'",
		"output:'profiles/linux.yaml'",
		"linux-ssh,tcp,22,127.0.0.1:22",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("Linux HTML missing %q", want)
		}
	}
	if strings.Contains(html, "saveThen('start','windows')") {
		t.Fatal("Linux HTML contains hard-coded Windows start target")
	}
	if !strings.Contains(html, `<button id="modeTun" onclick="setMode('tun')">TUN</button>`) {
		t.Fatal("Linux HTML does not expose the TUN mode control")
	}
	for _, want := range []string{
		`class="primary" hidden onclick="saveThen('start',runtimeTarget)"`,
		`<button hidden onclick="runAction('stop')"`,
		`<button hidden onclick="runAction('dashboard')"`,
		`class="hidden" onclick="runAction('proxy-on')"`,
		`class="hidden" onclick="runAction('proxy-off')"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("Linux HTML does not hide action control %q", want)
		}
	}
}

func TestPlatformUIKeepsWindowsRuntime(t *testing.T) {
	html := renderIndexHTML("windows")
	for _, want := range []string{
		"Windows 运行方式",
		"const runtimeTarget = 'windows'",
		"type:'windows-mihomo'",
		"output:'profiles/windows.yaml'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("Windows HTML missing %q", want)
		}
	}
	if strings.Contains(html, `class="hidden"`) {
		t.Fatal("Windows HTML hides system proxy controls")
	}
	if strings.Contains(html, ` hidden onclick=`) {
		t.Fatal("Windows HTML hides runtime action controls")
	}
}

func TestLinuxConfigAPIPreservesTUNChoice(t *testing.T) {
	for _, requestKind := range []string{"config", "content"} {
		t.Run(requestKind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "meshmux.local.json")
			chosen := config.Config{Name: "linux-tun-save"}
			chosen.TUN = config.TUN{
				Enabled:             true,
				Stack:               "mixed",
				AutoRoute:           true,
				AutoDetectInterface: true,
				StrictRoute:         true,
				DNSHijack:           []string{"any:53", "tcp://any:53"},
			}
			var body []byte
			var err error
			if requestKind == "config" {
				body, err = json.Marshal(map[string]any{"config": chosen})
			} else {
				content, marshalErr := json.Marshal(chosen)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				body, err = json.Marshal(map[string]string{"content": string(content)})
			}
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(body))
			response := httptest.NewRecorder()
			(&Server{ConfigPath: path}).configAPIFor("linux", response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got config.Config
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !got.TUN.Enabled || !got.TUN.AutoRoute || !got.TUN.AutoDetectInterface || !got.TUN.StrictRoute {
				t.Fatalf("Linux save changed the selected TUN mode: %+v", got.TUN)
			}
			if len(got.TUN.DNSHijack) != 2 || got.TUN.DNSHijack[0] != "any:53" || got.TUN.DNSHijack[1] != "tcp://any:53" {
				t.Fatalf("Linux save changed DNS hijack = %#v", got.TUN.DNSHijack)
			}
		})
	}
}

func TestTUNStatusUsesPlatformRuntimeEvidence(t *testing.T) {
	permissionChecks := 0
	canStartTUN := func() bool {
		permissionChecks++
		return false
	}

	linux := tunStatusFor("linux", true, true, canStartTUN)
	if linux.Value != "已启用" || linux.State != "ok" || linux.Detail != "核心实时配置已验证" {
		t.Fatalf("Linux TUN status = %+v", linux)
	}
	if permissionChecks != 0 {
		t.Fatalf("Linux TUN status checked Web process permissions %d times", permissionChecks)
	}

	linuxStopped := tunStatusFor("linux", true, false, canStartTUN)
	if linuxStopped.Value != "已配置，等待重启" || linuxStopped.State != "warn" {
		t.Fatalf("stopped Linux TUN status = %+v", linuxStopped)
	}
	if permissionChecks != 0 {
		t.Fatalf("stopped Linux TUN status checked Web process permissions %d times", permissionChecks)
	}

	windows := tunStatusFor("windows", true, true, canStartTUN)
	if windows.Value != "已启用" || windows.State != "ok" || permissionChecks != 0 {
		t.Fatalf("Windows TUN status = %+v, permission checks = %d", windows, permissionChecks)
	}

	windowsStopped := tunStatusFor("windows", true, false, canStartTUN)
	if windowsStopped.Value != "权限不足" || windowsStopped.State != "err" || permissionChecks != 1 {
		t.Fatalf("stopped Windows TUN status = %+v, permission checks = %d", windowsStopped, permissionChecks)
	}
}

func TestTailnetStatusSaysRuntimeNeedsVerification(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.AuthKey = "not-a-real-key"
	status := buildStatus(cfg)
	for _, item := range status.Items {
		if item.Label != "Tailnet" {
			continue
		}
		if item.Value != "已配置，运行态需验证" || item.State != "warn" {
			t.Fatalf("Tailnet status = %+v", item)
		}
		return
	}
	t.Fatal("Tailnet status item missing")
}

func TestPlatformActionPolicy(t *testing.T) {
	blocked := []string{"start", "stop", "proxy-on", "proxy-off", "dashboard"}
	for _, action := range blocked {
		if platformActionAllowed("linux", action) {
			t.Errorf("Linux action %q is allowed", action)
		}
		if !platformActionAllowed("windows", action) {
			t.Errorf("Windows action %q is blocked", action)
		}
	}
	for _, action := range []string{"generate", "download-mihomo", "download-dashboard", "probe-substore", "publish-mobile"} {
		if !platformActionAllowed("linux", action) {
			t.Errorf("Linux action %q is blocked", action)
		}
	}
}

func TestLinuxActionAPIRejectsRuntimeAndDesktopActions(t *testing.T) {
	s := &Server{ConfigPath: filepath.Join(t.TempDir(), "missing.json")}
	for _, action := range []string{"start", "stop", "proxy-on", "proxy-off", "dashboard"} {
		t.Run(action, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"action": action})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
			response := httptest.NewRecorder()
			s.actionAPIFor("linux", response, req)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
			if text := response.Body.String(); !strings.Contains(text, action) || !strings.Contains(text, "systemd") {
				t.Fatalf("response = %q", text)
			}
		})
	}
}

func TestStartAtAcceptsLoopbackAndRejectsOtherAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meshmux.local.json")
	if err := os.WriteFile(path, []byte(`{"name":"test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := StartAt(path, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(server.URL, "http://127.0.0.1:") || !strings.Contains(server.URL, "/?token=") {
		t.Fatalf("server URL = %q", server.URL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-server.Done(); err != nil {
		t.Fatalf("serve result = %v", err)
	}

	for _, address := range []string{"0.0.0.0:9088", "[::]:9088", "localhost:9088", "127.0.0.1"} {
		t.Run(address, func(t *testing.T) {
			if _, err := StartAt(path, address); err == nil {
				t.Fatalf("StartAt(%q) unexpectedly succeeded", address)
			}
		})
	}
}

func TestPlatformUIForCurrentRuntime(t *testing.T) {
	want := "windows"
	if runtime.GOOS == "linux" {
		want = "linux"
	}
	if got := platformUIFor(runtime.GOOS).RuntimeTarget; got != want {
		t.Fatalf("runtime target = %q, want %q", got, want)
	}
}

func TestConfigAPIRejectsInvalidInboundForward(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meshmux.local.json")
	data, err := json.Marshal(config.Config{Name: "before"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	server, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	invalid := config.Config{Name: "invalid", Tailscale: config.Tailscale{
		Enabled:         true,
		InboundForwards: []config.InboundForward{{Name: "ssh", Network: "tcp", ListenPort: 22, Target: "invalid"}},
	}}
	body, err := json.Marshal(map[string]any{"config": invalid})
	if err != nil {
		t.Fatal(err)
	}
	configURL := strings.Replace(server.URL, "/?token=", "/api/config?token=", 1)
	resp, err := http.Post(configURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST invalid config status = %d", resp.StatusCode)
	}
}

func TestImportWireGuardAPIAcceptsMultipleConfigs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meshmux.local.json")
	initial := config.Config{Name: "wg"}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	server, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	importURL := strings.Replace(server.URL, "/?token=", "/api/wireguard/import?token=", 1)
	for _, name := range []string{"home.conf", "office.conf"} {
		body, err := json.Marshal(map[string]string{"name": name, "content": testWireGuardConf})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(importURL, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST import %s status = %d", name, resp.StatusCode)
		}
		if _, err := os.Stat(filepath.Join(dir, "wireguard", name)); err != nil {
			t.Fatalf("stored %s: %v", name, err)
		}
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got config.Config
	if err := json.Unmarshal(written, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.WireGuard.Configs) != 2 {
		t.Fatalf("wireguard configs = %#v", got.WireGuard.Configs)
	}
}

func TestConfigAPIAcceptsStructuredConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meshmux.local.json")
	initial := config.Config{Name: "before"}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	server, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	configURL := strings.Replace(server.URL, "/?token=", "/api/config?token=", 1)
	resp, err := http.Get(configURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config status = %d", resp.StatusCode)
	}

	updated := config.Config{Name: "after"}
	updated.Ports.Mixed = 2081
	body, err := json.Marshal(map[string]any{"config": updated})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(configURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST config status = %d", resp.StatusCode)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got config.Config
	if err := json.Unmarshal(written, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "after" || got.Ports.Mixed != 2081 {
		t.Fatalf("written config = %+v", got)
	}
}

const testWireGuardConf = `[Interface]
PrivateKey = private
Address = 10.10.0.2/32
DNS = 10.10.0.1

[Peer]
PublicKey = public
AllowedIPs = 10.10.0.0/24
Endpoint = 127.0.0.1:51820
PersistentKeepalive = 25
`

func TestStatusAPIContainsCommonItems(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meshmux.local.json")
	cfg := config.Config{Name: "status"}
	cfg.Setup.ProviderURL = "https://sub.example.test/download"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	server, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	statusURL := strings.Replace(server.URL, "/?token=", "/api/status?token=", 1)
	resp, err := http.Get(statusURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status status = %d", resp.StatusCode)
	}
	var payload statusPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, item := range payload.Items {
		labels[item.Label] = true
	}
	for _, want := range []string{"核心进程", "TUN", "系统代理", "开机自启", "混合端口", "控制接口", "订阅", "Tailnet", "WireGuard"} {
		if !labels[want] {
			t.Fatalf("status missing %q in %+v", want, payload.Items)
		}
	}
}

func TestRecentStatusLogsReadTailAndRedactSecrets(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.MkdirAll("logs", 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("logs", "mihomo.err.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(256 * 1024 * 1024)
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	marker := []byte("level=error subscription=https://secret.example/path PrivateKey=tail-secret\n")
	if _, err := file.WriteAt(marker, size-int64(len(marker))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	text := strings.Join(recentStatusLogs(), "\n")
	if !strings.Contains(text, "level=error") {
		t.Fatalf("error line missing: %q", text)
	}
	for _, secret := range []string{"secret.example", "tail-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("status log contains %q: %q", secret, text)
		}
	}
}
