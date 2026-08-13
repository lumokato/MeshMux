//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/runner"
)

type linuxBackend struct {
	service     systemdController
	proxy       gsettingsController
	cfgPath     string
	webURLPath  string
	openURL     func(string) error
	sessionLock *linuxSessionLock
}

func newPlatformBackend() (trayBackend, error) {
	dataDir, err := resolveLinuxDataDir(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, err
	}
	cfgPath, err := resolveLinuxConfigPath(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, err
	}
	if err := redirectLinuxTrayOutput(dataDir); err != nil {
		return nil, err
	}
	session, err := validateLinuxTraySession(os.Getenv, probeLinuxSessionBus)
	if err != nil {
		return nil, err
	}
	sessionLock, err := acquireLinuxSessionLock(session)
	if err != nil {
		return nil, err
	}
	logLinuxTrayStartup(session)
	return &linuxBackend{
		service:     newSystemdController(execCommandRunner{}),
		proxy:       newGSettingsController(execCommandRunner{}),
		cfgPath:     cfgPath,
		webURLPath:  filepath.Join(dataDir, "runtime", "web-url"),
		openURL:     runner.OpenURL,
		sessionLock: sessionLock,
	}, nil
}

func (b *linuxBackend) Capabilities() trayCapabilities {
	return trayCapabilities{SystemProxy: true}
}

func (b *linuxBackend) InitialStart() error {
	return nil
}

func (b *linuxBackend) State() (trayState, error) {
	active, activeErr := b.service.IsActive()
	enabled, enabledErr := b.service.IsEnabled()
	proxyOn, proxyErr := b.proxy.Enabled()
	return trayState{
		CoreRunning:      active,
		SystemProxyOn:    proxyOn,
		AutostartEnabled: enabled,
	}, errors.Join(activeErr, enabledErr, proxyErr)
}

func (b *linuxBackend) OpenConfig() error {
	return openLocalURLFile(b.webURLPath, b.openURL)
}

func (b *linuxBackend) OpenDashboard() error {
	return b.openURL("http://127.0.0.1:9090/ui")
}

func (b *linuxBackend) ToggleCore() error {
	active, err := b.service.IsActive()
	if err != nil {
		return err
	}
	if active {
		return b.service.Action("stop")
	}
	return b.service.Action("start")
}

func (b *linuxBackend) RestartCore() error {
	return b.service.Action("restart")
}

func (b *linuxBackend) ToggleSystemProxy() error {
	enabled, err := b.proxy.Enabled()
	if err != nil {
		return err
	}
	cfg, _, err := config.Load(b.cfgPath)
	if err != nil {
		return err
	}
	return b.proxy.SetEnabled(!enabled, cfg.Ports.Mixed)
}

func (b *linuxBackend) ToggleAutostart() error {
	enabled, err := b.service.IsEnabled()
	if err != nil {
		return err
	}
	if enabled {
		return b.service.Action("disable")
	}
	return b.service.Action("enable")
}

func (b *linuxBackend) OnExit() {
	if b.sessionLock != nil {
		if err := b.sessionLock.Close(); err != nil {
			recordTrayActionError("release session lock", err)
		}
	}
}

func platformStartupFatal(err error) bool {
	recordTrayActionError("startup", err)
	return true
}
