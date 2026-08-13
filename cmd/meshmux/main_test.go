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
