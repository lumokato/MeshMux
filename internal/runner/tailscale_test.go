package runner

import (
	"context"
	"errors"
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

func TestTailscaleAuthKeyConfigured(t *testing.T) {
	if TailscaleAuthKeyConfigured(nil) {
		t.Fatal("nil config must report no key")
	}
	cfg := &config.Config{}
	if TailscaleAuthKeyConfigured(cfg) {
		t.Fatal("empty config must report no key")
	}
	cfg.Tailscale.AuthKey = "tskey-inline"
	if !TailscaleAuthKeyConfigured(cfg) {
		t.Fatal("inline key must count as configured")
	}
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "authkey")
	if err := os.WriteFile(keyFile, []byte("tskey-from-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Tailscale.AuthKey = ""
	cfg.Tailscale.AuthKeyFile = keyFile
	if !TailscaleAuthKeyConfigured(cfg) {
		t.Fatal("existing key file must count as configured")
	}
	cfg.Tailscale.AuthKeyFile = filepath.Join(dir, "missing")
	if TailscaleAuthKeyConfigured(cfg) {
		t.Fatal("missing key file must not count as configured")
	}
}

func TestTailscaleAutoLoginSkipsWithoutKeyOrDisabled(t *testing.T) {
	ctx := context.Background()
	enabledWithoutKey := &config.Config{}
	enabledWithoutKey.Tailscale.Enabled = true
	if err := tailscaleAutoLogin(ctx, enabledWithoutKey); err != nil {
		t.Fatalf("no key: auto login must skip without touching the daemon, got %v", err)
	}
	disabled := &config.Config{}
	disabled.Tailscale.AuthKey = "tskey-inline"
	if err := tailscaleAutoLogin(ctx, disabled); err != nil {
		t.Fatalf("disabled: auto login must skip, got %v", err)
	}
}

func TestTailscaleAutoLoginRunsUpOnceReady(t *testing.T) {
	oldStatus, oldUp := tailscaleStatusFn, tailscaleUpFn
	t.Cleanup(func() { tailscaleStatusFn, tailscaleUpFn = oldStatus, oldUp })

	statusCalls := 0
	tailscaleStatusFn = func(ctx context.Context, cfg *config.Config) (TailscaleState, error) {
		statusCalls++
		if statusCalls == 1 {
			return TailscaleState{}, errors.New("daemon not ready")
		}
		return TailscaleState{BackendState: "NeedsLogin"}, nil
	}
	upCalls := 0
	tailscaleUpFn = func(ctx context.Context, cfg *config.Config) error {
		upCalls++
		return nil
	}

	cfg := &config.Config{}
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.AuthKey = "tskey-inline"
	if err := tailscaleAutoLogin(context.Background(), cfg); err != nil {
		t.Fatalf("auto login failed: %v", err)
	}
	if statusCalls != 2 || upCalls != 1 {
		t.Fatalf("statusCalls = %d, upCalls = %d; want 2 retries then exactly one up", statusCalls, upCalls)
	}
}

func TestTailscaleAutoLoginSkipsWhenAlreadyRunning(t *testing.T) {
	oldStatus, oldUp := tailscaleStatusFn, tailscaleUpFn
	t.Cleanup(func() { tailscaleStatusFn, tailscaleUpFn = oldStatus, oldUp })

	tailscaleStatusFn = func(ctx context.Context, cfg *config.Config) (TailscaleState, error) {
		return TailscaleState{BackendState: "Running", Online: true}, nil
	}
	upCalls := 0
	tailscaleUpFn = func(ctx context.Context, cfg *config.Config) error {
		upCalls++
		return nil
	}

	cfg := &config.Config{}
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.AuthKey = "tskey-inline"
	if err := tailscaleAutoLogin(context.Background(), cfg); err != nil {
		t.Fatalf("auto login failed: %v", err)
	}
	if upCalls != 0 {
		t.Fatalf("up ran %d times for an already logged-in daemon", upCalls)
	}
}
