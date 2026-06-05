package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/getlantern/systray"
	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/generator"
	"github.com/meshmux/meshmux/internal/runner"
	"github.com/meshmux/meshmux/internal/webui"
)

var (
	cfgPath    string
	server     *webui.Server
	startupErr error
	mCore      *systray.MenuItem
	mProxy     *systray.MenuItem
	mAutostart *systray.MenuItem
)

func main() {
	setDPIAwareness()
	var elevateErr error
	if relaunched, err := relaunchElevatedIfNeeded(); relaunched {
		return
	} else if err != nil {
		elevateErr = err
	}
	var workingDirErr error
	cfgPath, workingDirErr = ensureWorkingDir()
	if workingDirErr != nil {
		startupErr = workingDirErr
	} else {
		startupErr = elevateErr
	}
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle("MeshMux")
	systray.SetTooltip("MeshMux")
	if startupErr != nil {
		systray.SetTooltip("启动: " + startupErr.Error())
	}

	mOpenConfig := systray.AddMenuItem("打开配置页面", "在浏览器打开 MeshMux 配置")
	mOpenDashboard := systray.AddMenuItem("打开 MetaCubeXD", "打开 mihomo 面板")
	systray.AddSeparator()
	mCore = systray.AddMenuItemCheckbox("核心运行：关", "启动或停止 mihomo", false)
	mRestart := systray.AddMenuItem("重启核心", "重新启动 mihomo")
	systray.AddSeparator()
	mProxy = systray.AddMenuItemCheckbox("系统代理：关", "开启或关闭 Windows 系统代理", false)
	mAutostart = systray.AddMenuItemCheckbox("开机自启：关", "开启或关闭 MeshMux 开机自启", false)
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "退出 MeshMux")
	refreshMenuState()
	go func() {
		withConfig(func(cfg *config.Config) error {
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
		refreshMenuState()
	}()

	go func() {
		for {
			select {
			case <-mOpenConfig.ClickedCh:
				openConfig()
			case <-mOpenDashboard.ClickedCh:
				withConfig(func(cfg *config.Config) error { return runner.Dashboard(cfg) })
			case <-mCore.ClickedCh:
				if runner.IsRunning() {
					notify("核心", runner.Stop())
				} else {
					start()
				}
				refreshMenuState()
			case <-mRestart.ClickedCh:
				_ = runner.Stop()
				time.Sleep(500 * time.Millisecond)
				start()
				refreshMenuState()
			case <-mProxy.ClickedCh:
				if runner.ProxyEnabled() {
					withConfig(func(cfg *config.Config) error { return runner.Proxy("off", cfg.Ports.Mixed) })
				} else {
					withConfig(func(cfg *config.Config) error { return runner.Proxy("on", cfg.Ports.Mixed) })
				}
				refreshMenuState()
			case <-mAutostart.ClickedCh:
				if runner.AutostartEnabled() {
					notify("开机自启", runner.Autostart("off"))
				} else {
					notify("开机自启", runner.Autostart("on"))
				}
				refreshMenuState()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {
	cfg, _, err := config.Load(cfgPath)
	if err == nil {
		_ = runner.Proxy("off", cfg.Ports.Mixed)
		_ = runner.CleanupResidual(cfg)
	}
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
}

func openConfig() {
	if server == nil {
		var err error
		server, err = webui.Start(cfgPath)
		if err != nil {
			notify("Config", err)
			return
		}
	}
	notify("配置页面", runner.OpenURL(server.URL))
}

func start() {
	withConfig(func(cfg *config.Config) error {
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

func withConfig(fn func(*config.Config) error) {
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		notify("配置", err)
		return
	}
	notify("MeshMux", fn(cfg))
}

func notify(action string, err error) {
	if err != nil {
		systray.SetTooltip(action + ": " + err.Error())
		return
	}
	systray.SetTooltip(action + ": 成功")
}

func refreshMenuState() {
	if mCore != nil {
		if runner.IsRunning() {
			mCore.Check()
			mCore.SetTitle("核心运行：开")
		} else {
			mCore.Uncheck()
			mCore.SetTitle("核心运行：关")
		}
	}
	if mProxy != nil {
		if runner.ProxyEnabled() {
			mProxy.Check()
			mProxy.SetTitle("系统代理：开")
		} else {
			mProxy.Uncheck()
			mProxy.SetTitle("系统代理：关")
		}
	}
	if mAutostart != nil {
		if runner.AutostartEnabled() {
			mAutostart.Check()
			mAutostart.SetTitle("开机自启：开")
		} else {
			mAutostart.Uncheck()
			mAutostart.SetTitle("开机自启：关")
		}
	}
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
