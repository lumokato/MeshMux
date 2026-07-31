package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

func TestIndexHTMLIsChineseFormUI(t *testing.T) {
	for _, want := range []string{"快速设置", "Sub-Store 地址", "后端名", "生成并上传手机配置", "导入 WireGuard 配置", "multiple", "状态概览", "Tailnet 入站转发", "tsInboundForwards"} {
		if !strings.Contains(indexHTML, want) {
			t.Fatalf("indexHTML missing %q", want)
		}
	}
	for _, private := range []string{"private-backend-name", "/api/files", "TUN 模式通常"} {
		if strings.Contains(indexHTML, private) {
			t.Fatalf("indexHTML should not contain %q", private)
		}
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
