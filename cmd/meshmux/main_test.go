package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

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
