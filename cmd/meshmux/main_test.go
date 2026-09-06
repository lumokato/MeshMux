package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

func TestStopLoadsConfigAndEntersRuntimeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", home)
	cfg := config.Config{}
	cfg.Ports.Controller = "127.0.0.1:" + freePort(t)
	cfg.Ports.Mixed = freePortNumber(t)
	cfg.Components.Mihomo.Path = filepath.Join("bin", "mihomo.exe")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, config.DefaultConfigPath)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	if err := run([]string{"stop", "-config", path}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(got, home) {
		t.Fatalf("working directory = %s, want %s", got, home)
	}
}

func TestConfigCheckReportsCompletenessWithoutSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "providers"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "wireguard"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "providers", "main.yaml"), []byte("proxies:\n  - name: node\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "wireguard", "home.conf"), []byte("private material"), 0600); err != nil {
		t.Fatal(err)
	}

	const providerSecret = "https://secret.example/subscription"
	const authSecret = "tskey-auth-secret"
	cfg := config.Config{
		Setup:     config.Setup{ProviderURL: providerSecret},
		WireGuard: config.WireGuard{Configs: []string{filepath.Join("wireguard", "home.conf")}},
		Tailscale: config.Tailscale{
			Enabled: true,
			AuthKey: authSecret,
			InboundForwards: []config.InboundForward{{
				Name: "rdp", Network: "tcp", ListenPort: 3389, Target: "127.0.0.1:3389",
			}},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, config.DefaultConfigPath)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	var output bytes.Buffer
	if err := checkConfig([]string{"-config", path}, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"daily-proxy-source: configured",
		"daily-proxy-cache: configured",
		"tailnet: enabled",
		"tailnet-auth: configured",
		"wireguard-configs: 1/1 available",
		"tailnet-inbound-forwards: 1",
		"direct-only: disabled",
		"result: ready",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config-check output missing %q: %s", want, text)
		}
	}
	for _, secret := range []string{providerSecret, authSecret, "private material"} {
		if strings.Contains(text, secret) {
			t.Fatalf("config-check leaked secret %q: %s", secret, text)
		}
	}
}

func TestConfigCheckResolvesAssetsRelativeToExplicitConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, "providers"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "wireguard"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "providers", "main.yaml"), []byte("proxies:\n  - name: node\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "wireguard", "home.conf"), []byte("private material"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Providers: []config.Provider{{Name: "main", Path: filepath.Join("providers", "main.yaml")}},
		WireGuard: config.WireGuard{Configs: []string{filepath.Join("wireguard", "home.conf")}},
	}
	cfg.Setup.AllowDirectOnly = true
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, config.DefaultConfigPath)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	var output bytes.Buffer
	if err := checkConfig([]string{"-config", path}, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"daily-proxy-cache: configured", "wireguard-configs: 1/1 available", "result: ready"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config-check output missing %q: %s", want, text)
		}
	}
}
func TestConfigCheckRejectsMissingDailyProxyAndTailnetAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", home)
	path := filepath.Join(home, config.DefaultConfigPath)
	if err := os.WriteFile(path, []byte(`{"tailscale":{"enabled":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	var output bytes.Buffer
	err = checkConfig([]string{"-config", path}, &output)
	if err == nil || !strings.Contains(err.Error(), "daily proxy") || !strings.Contains(err.Error(), "auth key") {
		t.Fatalf("config-check error = %v", err)
	}
	if strings.Contains(output.String(), "result: ready") {
		t.Fatalf("incomplete config reported ready: %s", output.String())
	}
}

func TestServeHeadlessContextPrintsTokenURLAndShutsDown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", home)
	path := filepath.Join(home, config.DefaultConfigPath)
	data, err := json.Marshal(config.Config{Name: "headless"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	ctx, cancel := context.WithCancel(context.Background())
	output := make(channelWriter, 1)
	done := make(chan error, 1)
	go func() {
		done <- serveHeadlessContext(ctx, []string{"-listen", "127.0.0.1:0", "-config", path}, output)
	}()
	var url string
	select {
	case text := <-output:
		url = strings.TrimSpace(text)
	case <-time.After(time.Second):
		t.Fatal("serve did not print a URL")
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.Contains(url, "/?token=") {
		t.Fatalf("serve output = %q", url)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not stop after cancellation")
	}
}

func TestServeHeadlessContextWritesPrivateURLFileAndRemovesIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", home)
	path := filepath.Join(home, config.DefaultConfigPath)
	if err := os.WriteFile(path, []byte(`{"name":"headless"}`), 0600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(home, "runtime")
	if err := os.Mkdir(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	urlPath := filepath.Join(runtimeDir, "web-url")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serveHeadlessContext(ctx, []string{
			"-listen", "127.0.0.1:0",
			"-config", path,
			"-url-file", urlPath,
		}, &output)
	}()

	var url string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(urlPath)
		if readErr == nil {
			url = strings.TrimSpace(string(data))
			break
		}
		if !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.Contains(url, "/?token=") {
		t.Fatalf("URL file = %q", url)
	}
	if output.Len() != 0 {
		t.Fatalf("serve output leaked URL: %q", output.String())
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(urlPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("URL file mode = %o, want 600", got)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not stop after cancellation")
	}
	if _, err := os.Stat(urlPath); !os.IsNotExist(err) {
		t.Fatalf("URL file still exists after shutdown: %v", err)
	}
}

type channelWriter chan string

func (w channelWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func TestServeHeadlessRejectsNonLoopback(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", home)
	path := filepath.Join(home, config.DefaultConfigPath)
	if err := os.WriteFile(path, []byte(`{"name":"headless"}`), 0600); err != nil {
		t.Fatal(err)
	}
	err = serveHeadlessContext(context.Background(), []string{"-listen", "0.0.0.0:9088", "-config", path}, &bytes.Buffer{})
	if chdirErr := os.Chdir(old); chdirErr != nil {
		t.Fatal(chdirErr)
	}
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("serve error = %v", err)
	}
}

func TestDefaultRuntimeTargetMatchesPlatform(t *testing.T) {
	want := "windows"
	if runtime.GOOS == "linux" {
		want = "linux"
	}
	if got := defaultRuntimeTarget(); got != want {
		t.Fatalf("defaultRuntimeTarget = %q, want %q", got, want)
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()[len("127.0.0.1:"):]
}

func freePortNumber(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
