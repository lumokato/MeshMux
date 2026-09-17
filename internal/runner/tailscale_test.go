package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/meshmux/meshmux/internal/config"
)

func TestTailscaleSocketPathIsIsolated(t *testing.T) {
	sock := tailscaleSocketPath()
	if strings.TrimSpace(sock) == "" {
		t.Fatal("socket path is empty")
	}
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(sock, `\\.\pipe\MeshMux`) {
			t.Fatalf("windows socket must use the MeshMux named pipe, got %q", sock)
		}
		return
	}
	if !filepath.IsAbs(sock) {
		t.Fatalf("unix socket path must be absolute, got %q", sock)
	}
	if !strings.HasSuffix(sock, "tailscaled.sock") {
		t.Fatalf("unix socket path should end in tailscaled.sock, got %q", sock)
	}
}

func TestPrepareTailscaleAuthKeyPrefersKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "authkey")
	if err := os.WriteFile(keyFile, []byte("tskey-from-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Tailscale.AuthKey = "tskey-inline"
	cfg.Tailscale.AuthKeyFile = keyFile

	path, cleanup, err := prepareTailscaleAuthKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if path != keyFile {
		t.Fatalf("auth key path = %q, want the configured file %q", path, keyFile)
	}
}

func TestPrepareTailscaleAuthKeyMaterializesInlineKey(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tailscale.AuthKey = "tskey-inline"

	path, cleanup, err := prepareTailscaleAuthKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "tskey-inline" {
		t.Fatalf("materialized key = %q", string(data))
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("materialized key file was not removed")
	}
}

func TestPrepareTailscaleAuthKeyWithoutKeyIsEmpty(t *testing.T) {
	cfg := &config.Config{}
	path, cleanup, err := prepareTailscaleAuthKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if path != "" {
		t.Fatalf("expected empty key path, got %q", path)
	}
}
