//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/fileutil"
	"github.com/meshmux/meshmux/internal/generator"
	"github.com/meshmux/meshmux/internal/runner"
	"github.com/meshmux/meshmux/internal/webui"
)

// darwinBackend follows the user-mode Windows path: the tray runs in the
// logged-in GUI session, starts mihomo through the runner layer, drives the
// per-network-service system proxy (networksetup) and the per-user LaunchAgent
// autostart. There is no service-manager mode on macOS, so the tray always
// owns the core lifecycle, like a non-service Windows install.
type darwinBackend struct {
	cfgPath string
	server  *webui.Server
	unlock  func()
}

func newPlatformBackend() (trayBackend, error) {
	cfgPath, err := ensureWorkingDir()
	if err != nil {
		return &darwinBackend{cfgPath: cfgPath}, err
	}
	unlock, err := fileutil.TryLock(filepath.Join("state", "tray.lock"))
	if err != nil {
		return nil, err
	}
	return &darwinBackend{cfgPath: cfgPath, unlock: unlock}, nil
}

func platformStartupFatal(err error) bool { return errors.Is(err, fileutil.ErrBusy) }

func recordTrayActionError(action string, err error) {
	if err == nil {
		return
	}
	_ = runner.AppendDiagnosticLog(filepath.Join(config.LocalDataDir(), "logs", "tray.log"), fmt.Sprintf("MeshMux tray action %q failed: %v", action, err))
}

func (b *darwinBackend) Capabilities() trayCapabilities {
	return trayCapabilities{SystemProxy: true}
}

func (b *darwinBackend) InitialStart() error {
	// The tray starts or observes the core only. Upstream reachability and
	// proxy selection are independent user-visible runtime states.
	return b.withConfig(func(cfg *config.Config) error {
		if runner.CoreDaemonInstalled() {
			// The root LaunchDaemon owns start, stop and self-healing. The tray
			// only re-applies the user's system-proxy intent; it never starts a
			// second core or asks for a password at login.
			b.restoreProxyIntent()
			return nil
		}
		if runner.IsRunning(cfg) {
			b.restoreProxyIntent()
			return nil
		}
		profile, err := generator.GenerateNamed(cfg, "darwin")
		if err != nil {
			return err
		}
		if err := runner.Start(cfg, profile); err != nil {
			return err
		}
		b.restoreProxyIntent()
		return nil
	})
}

// restoreProxyIntent re-applies the system proxy when the user's last explicit
// choice was "on". networksetup refuses to switch while the local mixed port
// is not answering yet, so retry briefly instead of failing the start. Errors
// are non-fatal — a stale intent must never block the core from running.
func (b *darwinBackend) restoreProxyIntent() {
	if !runner.ProxyWanted() {
		return
	}
	cfg, _, err := config.Load(b.cfgPath)
	if err != nil {
		return
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := runner.Proxy("on", cfg.Ports.Mixed); err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = runner.AppendDiagnosticLog(filepath.Join(config.LocalDataDir(), "logs", "tray.log"), "restoreProxyIntent: system proxy stayed unavailable for 15s")
}

func (b *darwinBackend) State() (trayState, error) {
	state := trayState{
		SystemProxyOn:    runner.ProxyEnabled(),
		AutostartEnabled: runner.AutostartEnabled(),
		CoreDaemon:       runner.CoreDaemonInstalled(),
	}
	cfg, _, err := config.Load(b.cfgPath)
	if err != nil {
		return state, err
	}
	if state.CoreDaemon {
		_, running := runner.CoreDaemonPID()
		state.CoreRunning = running
		return state, nil
	}
	state.CoreRunning = runner.IsRunning(cfg)
	return state, nil
}

func (b *darwinBackend) OpenConfig() error {
	if b.server == nil {
		server, err := webui.Start(b.cfgPath)
		if err != nil {
			return err
		}
		server.Version = version
		b.server = server
	}
	return runner.OpenURL(b.server.URL)
}

func (b *darwinBackend) OpenDashboard() error {
	return b.withConfig(runner.Dashboard)
}

func (b *darwinBackend) ToggleCore() error {
	if runner.CoreDaemonInstalled() {
		// Stopping or starting the protected service is a system change; the
		// daemon keeps ownership and the tray only requests the action.
		action := "start"
		if _, running := runner.CoreDaemonPID(); running {
			action = "stop"
		}
		_, err := runner.CoreDaemonService(context.Background(), action)
		return err
	}
	return b.withConfig(func(cfg *config.Config) error {
		if runner.IsRunning(cfg) {
			if err := runner.DisableOwnedProxy(cfg.Ports.Mixed); err != nil {
				return err
			}
			return runner.Stop(cfg)
		}
		profile, err := generator.GenerateNamed(cfg, "darwin")
		if err != nil {
			return err
		}
		return runner.Start(cfg, profile)
	})
}

func (b *darwinBackend) RestartCore() error {
	if runner.CoreDaemonInstalled() {
		_, err := runner.CoreDaemonService(context.Background(), "restart")
		return err
	}
	return b.withConfig(func(cfg *config.Config) error {
		profile, err := generator.GenerateNamed(cfg, "darwin")
		if err != nil {
			return err
		}
		return runner.Restart(cfg, profile)
	})
}

func (b *darwinBackend) ToggleSystemProxy() error {
	return b.withConfig(func(cfg *config.Config) error {
		if runner.ProxyEnabled() {
			if err := runner.Proxy("off", cfg.Ports.Mixed); err != nil {
				return err
			}
			return runner.SetProxyIntent(false)
		}
		if err := runner.Proxy("on", cfg.Ports.Mixed); err != nil {
			return err
		}
		return runner.SetProxyIntent(true)
	})
}

func (b *darwinBackend) ToggleAutostart() error {
	if runner.AutostartEnabled() {
		return runner.Autostart("off")
	}
	return runner.Autostart("on")
}

func (b *darwinBackend) OnExit() {
	if b.unlock != nil {
		defer b.unlock()
	}
	if runner.CoreDaemonInstalled() {
		// The root daemon is an independent network service. Quitting the menu
		// bar UI must not stop the core or rewrite the system proxy.
		if b.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = b.server.Shutdown(ctx)
		}
		return
	}
	cfg, _, err := config.Load(b.cfgPath)
	if err == nil {
		_ = runner.DisableOwnedProxy(cfg.Ports.Mixed)
		_ = runner.CleanupResidual(cfg)
	}
	if b.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.server.Shutdown(ctx)
	}
}

func (b *darwinBackend) withConfig(fn func(*config.Config) error) error {
	cfg, _, err := config.Load(b.cfgPath)
	if err != nil {
		return err
	}
	return fn(cfg)
}

// ensureWorkingDir keeps writable data outside the application. Resources in
// Contents/Resources (or beside portable executables) seed fresh installations;
// all runtime paths resolve under Application Support/MeshMux or MESHMUX_HOME.
func ensureWorkingDir() (string, error) {
	cwdExample, _ := filepath.Abs(config.ExampleConfigPath)
	dir := config.BundleDir()
	exeExample := filepath.Join(dir, "meshmux.example.json")
	example := exeExample
	if _, err := os.Stat(example); err != nil {
		example = cwdExample
	}
	dataDir := config.LocalDataDir()
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return filepath.Join(dataDir, config.DefaultConfigPath), err
	}
	if err := os.Chdir(dataDir); err != nil {
		return filepath.Join(dataDir, config.DefaultConfigPath), err
	}
	path, err := config.EnsureLocalConfig(example)
	if err != nil {
		return filepath.Join(dataDir, config.DefaultConfigPath), err
	}
	return path, nil
}
