//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/generator"
	"github.com/meshmux/meshmux/internal/runner"
	"github.com/meshmux/meshmux/internal/webui"
	"github.com/meshmux/meshmux/internal/winservice"
)

type windowsBackend struct {
	cfgPath string
	server  *webui.Server
}

func newPlatformBackend() (trayBackend, error) {
	cfgPath, err := ensureWorkingDir()
	return &windowsBackend{cfgPath: cfgPath}, err
}

func platformStartupFatal(error) bool { return false }

func recordTrayActionError(string, error) {}

func (b *windowsBackend) Capabilities() trayCapabilities {
	return trayCapabilities{SystemProxy: true}
}

func (b *windowsBackend) InitialStart() error {
	if winservice.Installed() {
		return b.withConfig(func(cfg *config.Config) error {
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if runner.ControllerReady(cfg) {
					return runner.Proxy("on", cfg.Ports.Mixed)
				}
				time.Sleep(250 * time.Millisecond)
			}
			return fmt.Errorf("MeshMux 服务尚未就绪")
		})
	}
	return b.withConfig(func(cfg *config.Config) error {
		if err := runner.CleanupResidual(cfg); err != nil {
			return err
		}
		profile, err := generator.GenerateNamed(cfg, "windows")
		if err != nil {
			return err
		}
		if err := runner.Start(cfg, profile); err != nil {
			return err
		}
		return runner.Proxy("on", cfg.Ports.Mixed)
	})
}

func (b *windowsBackend) State() (trayState, error) {
	state := trayState{
		SystemProxyOn:    runner.ProxyEnabled(),
		AutostartEnabled: runner.AutostartEnabled() || winservice.Installed(),
	}
	cfg, _, err := config.Load(b.cfgPath)
	if err != nil {
		return state, err
	}
	if winservice.Installed() {
		state.CoreRunning = winservice.Running() && runner.ControllerReady(cfg)
	} else {
		state.CoreRunning = runner.IsRunning(cfg)
	}
	return state, nil
}

func (b *windowsBackend) OpenConfig() error {
	if b.server == nil {
		server, err := webui.Start(b.cfgPath)
		if err != nil {
			return err
		}
		b.server = server
	}
	return runner.OpenURL(b.server.URL)
}

func (b *windowsBackend) OpenDashboard() error {
	return b.withConfig(runner.Dashboard)
}

func (b *windowsBackend) ToggleCore() error {
	if winservice.Installed() {
		action := "start"
		if winservice.Running() {
			action = "stop"
		}
		return runElevatedServiceAction(action, b.cfgPath)
	}
	return b.withConfig(func(cfg *config.Config) error {
		if runner.IsRunning(cfg) {
			return runner.Stop(cfg)
		}
		return startWindowsCore(cfg)
	})
}

func (b *windowsBackend) RestartCore() error {
	if winservice.Installed() {
		return runElevatedServiceAction("restart", b.cfgPath)
	}
	return b.withConfig(func(cfg *config.Config) error {
		profile, err := generator.GenerateNamed(cfg, "windows")
		if err != nil {
			return err
		}
		if err := runner.Restart(cfg, profile); err != nil {
			return err
		}
		return runner.Proxy("on", cfg.Ports.Mixed)
	})
}

func (b *windowsBackend) ToggleSystemProxy() error {
	return b.withConfig(func(cfg *config.Config) error {
		if runner.ProxyEnabled() {
			return runner.Proxy("off", cfg.Ports.Mixed)
		}
		return runner.Proxy("on", cfg.Ports.Mixed)
	})
}

func (b *windowsBackend) ToggleAutostart() error {
	if winservice.Installed() {
		return fmt.Errorf("服务模式下开机自启由安装器管理")
	}
	if runner.AutostartEnabled() {
		return runner.Autostart("off")
	}
	return runner.Autostart("on")
}

func (b *windowsBackend) OnExit() {
	if !winservice.Installed() {
		cfg, _, err := config.Load(b.cfgPath)
		if err == nil {
			_ = runner.Proxy("off", cfg.Ports.Mixed)
			_ = runner.CleanupResidual(cfg)
		}
	}
	if b.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.server.Shutdown(ctx)
	}
}

func runElevatedServiceAction(action, configPath string) error {
	return winservice.RunElevated(action, configPath)
}

func (b *windowsBackend) withConfig(fn func(*config.Config) error) error {
	cfg, _, err := config.Load(b.cfgPath)
	if err != nil {
		return err
	}
	return fn(cfg)
}

func startWindowsCore(cfg *config.Config) error {
	profile, err := generator.GenerateNamed(cfg, "windows")
	if err != nil {
		return err
	}
	if err := runner.Start(cfg, profile); err != nil {
		return err
	}
	return runner.Proxy("on", cfg.Ports.Mixed)
}

func ensureWorkingDir() (string, error) {
	cwdExample, _ := filepath.Abs(config.ExampleConfigPath)
	exe, err := os.Executable()
	if err != nil {
		return config.LocalConfigPath(), err
	}
	dir := filepath.Dir(exe)
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
