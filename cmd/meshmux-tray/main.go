package main

import (
	"fmt"
	"os"
	"time"

	"github.com/getlantern/systray"
)

const menuRefreshInterval = 3 * time.Second

type trayCapabilities struct {
	SystemProxy bool
}

type trayState struct {
	CoreRunning      bool
	CoreDegraded     bool
	SystemProxyOn    bool
	AutostartEnabled bool
}

type trayBackend interface {
	Capabilities() trayCapabilities
	InitialStart() error
	State() (trayState, error)
	OpenConfig() error
	OpenDashboard() error
	ToggleCore() error
	RestartCore() error
	ToggleSystemProxy() error
	ToggleAutostart() error
	OnExit()
}

type unavailableBackend struct {
	err error
}

func (b unavailableBackend) Capabilities() trayCapabilities { return trayCapabilities{} }
func (b unavailableBackend) InitialStart() error            { return b.err }
func (b unavailableBackend) State() (trayState, error)      { return trayState{}, b.err }
func (b unavailableBackend) OpenConfig() error              { return b.err }
func (b unavailableBackend) OpenDashboard() error           { return b.err }
func (b unavailableBackend) ToggleCore() error              { return b.err }
func (b unavailableBackend) RestartCore() error             { return b.err }
func (b unavailableBackend) ToggleSystemProxy() error       { return b.err }
func (b unavailableBackend) ToggleAutostart() error         { return b.err }
func (b unavailableBackend) OnExit()                        {}

var (
	backend    trayBackend
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

	var backendErr error
	backend, backendErr = newPlatformBackend()
	if backendErr != nil && platformStartupFatal(backendErr) {
		os.Exit(1)
	}
	if backend == nil {
		if backendErr == nil {
			backendErr = fmt.Errorf("MeshMux tray backend is unavailable")
		}
		backend = unavailableBackend{err: backendErr}
	}
	if backendErr != nil {
		startupErr = backendErr
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
	if backend.Capabilities().SystemProxy {
		systray.AddSeparator()
		mProxy = systray.AddMenuItemCheckbox("系统代理：关", "开启或关闭桌面系统代理", false)
	}
	mAutostart = systray.AddMenuItemCheckbox("开机自启：关", "开启或关闭 MeshMux 开机自启", false)
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "退出 MeshMux")
	if err := refreshMenuState(); err != nil && startupErr == nil {
		notify("状态", err)
	}

	go runMenuLoop(mOpenConfig, mOpenDashboard, mRestart, mQuit)
}

func runMenuLoop(mOpenConfig, mOpenDashboard, mRestart, mQuit *systray.MenuItem) {
	ticker := time.NewTicker(menuRefreshInterval)
	defer ticker.Stop()

	initialDone := make(chan error, 1)
	go func() {
		initialDone <- backend.InitialStart()
	}()

	var proxyClicks <-chan struct{}
	if mProxy != nil {
		proxyClicks = mProxy.ClickedCh
	}

	for {
		select {
		case err := <-initialDone:
			initialDone = nil
			notify("MeshMux", err)
			refreshAfterAction()
		case <-ticker.C:
			if err := refreshMenuState(); err != nil {
				notify("状态", err)
			}
		case <-mOpenConfig.ClickedCh:
			notify("配置页面", backend.OpenConfig())
		case <-mOpenDashboard.ClickedCh:
			notify("MetaCubeXD", backend.OpenDashboard())
		case <-mCore.ClickedCh:
			notify("核心", backend.ToggleCore())
			refreshAfterAction()
		case <-mRestart.ClickedCh:
			notify("重启核心", backend.RestartCore())
			refreshAfterAction()
		case <-proxyClicks:
			notify("系统代理", backend.ToggleSystemProxy())
			refreshAfterAction()
		case <-mAutostart.ClickedCh:
			notify("开机自启", backend.ToggleAutostart())
			refreshAfterAction()
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func onExit() {
	if backend != nil {
		backend.OnExit()
	}
}

func notify(action string, err error) {
	if err != nil {
		recordTrayActionError(action, err)
		systray.SetTooltip(action + ": " + err.Error())
		return
	}
	systray.SetTooltip(action + ": 成功")
}

func refreshAfterAction() {
	if err := refreshMenuState(); err != nil {
		notify("状态", err)
	}
}

func refreshMenuState() error {
	state, err := backend.State()
	if mCore != nil {
		if state.CoreRunning && state.CoreDegraded {
			mCore.Check()
			mCore.SetTitle("核心运行：服务异常")
		} else if state.CoreRunning {
			mCore.Check()
			mCore.SetTitle("核心运行：开")
		} else {
			mCore.Uncheck()
			mCore.SetTitle("核心运行：关")
		}
	}
	if mProxy != nil {
		if state.SystemProxyOn {
			mProxy.Check()
			mProxy.SetTitle("系统代理：开")
		} else {
			mProxy.Uncheck()
			mProxy.SetTitle("系统代理：关")
		}
	}
	if mAutostart != nil {
		if state.AutostartEnabled {
			mAutostart.Check()
			mAutostart.SetTitle("开机自启：开")
		} else {
			mAutostart.Uncheck()
			mAutostart.SetTitle("开机自启：关")
		}
	}
	return err
}
