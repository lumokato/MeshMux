//go:build darwin

package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/meshmux/meshmux/internal/fileutil"
)

// macOS keeps proxy settings per network service (Wi-Fi, Ethernet, ...) rather
// than in one global location, so every enabled service has to be handled.
// The original values are captured before the first change and restored when
// MeshMux turns the proxy back off, so a pre-existing corporate proxy or PAC
// setup is not silently destroyed.
const darwinProxyBackupPath = "state/darwin-proxy-backup.json"

type darwinProxySetting struct {
	Service string `json:"service"`
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Port    int    `json:"port"`
}

type darwinProxyBackup struct {
	Services []darwinProxySetting `json:"services"`
}

func platformProxy(mode string, port int) error {
	switch mode {
	case "show":
		services, err := darwinNetworkServices()
		if err != nil {
			return err
		}
		for _, service := range services {
			setting, readErr := darwinReadProxy(service)
			if readErr != nil {
				return readErr
			}
			fmt.Printf("%s\tenabled=%t\tserver=%s\tport=%d\n", service, setting.Enabled, setting.Server, setting.Port)
		}
		return nil
	case "on":
		if !LocalProxyReady(port) {
			return fmt.Errorf("local proxy on port %d is unavailable; system proxy unchanged", port)
		}
		services, err := darwinNetworkServices()
		if err != nil {
			return err
		}
		if len(services) == 0 {
			return errors.New("macOS 未找到已启用的网络服务，无法设置系统代理")
		}
		if err := darwinCaptureBackup(services); err != nil {
			return err
		}
		var applied []string
		for _, service := range services {
			if err := darwinSetProxy(service, true, "127.0.0.1", port); err != nil {
				// Undo the services already switched over so a partial failure
				// does not leave the machine half-pointed at MeshMux.
				for _, done := range applied {
					_ = darwinRestoreService(done)
				}
				return err
			}
			applied = append(applied, service)
		}
		return nil
	case "off":
		return darwinDisableOwned(port)
	default:
		return errors.New("proxy expects on, off, or show")
	}
}

func platformProxyEnabled() bool {
	services, err := darwinNetworkServices()
	if err != nil {
		return false
	}
	for _, service := range services {
		setting, readErr := darwinReadProxy(service)
		if readErr != nil {
			continue
		}
		if setting.Enabled && setting.Port > 0 && darwinLoopbackServer(setting.Server) {
			return true
		}
	}
	return false
}

// DisableOwnedProxy turns the proxy off only for services that still point at
// this MeshMux port. It is used when the core is gone and the tray has to
// clean up after itself.
func DisableOwnedProxy(port int) error {
	return darwinDisableOwned(port)
}

func darwinDisableOwned(port int) error {
	services, err := darwinNetworkServices()
	if err != nil {
		return err
	}
	changed := false
	for _, service := range services {
		setting, readErr := darwinReadProxy(service)
		if readErr != nil {
			return readErr
		}
		if !darwinOwnsProxy(setting, port) {
			continue
		}
		if err := darwinRestoreService(service); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		return darwinClearBackup()
	}
	return nil
}

func darwinOwnsProxy(setting darwinProxySetting, port int) bool {
	return setting.Enabled && setting.Port == port && darwinLoopbackServer(setting.Server)
}

func darwinLoopbackServer(server string) bool {
	switch strings.ToLower(strings.TrimSpace(server)) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func darwinNetworkServices() ([]string, error) {
	out, err := hiddenCommand("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("枚举 macOS 网络服务失败: %w", err)
	}
	var services []string
	for index, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// The first line is an explanatory banner, not a service name.
		if index == 0 || line == "" {
			continue
		}
		// Disabled services are prefixed with an asterisk.
		if strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	return services, nil
}

func darwinReadProxy(service string) (darwinProxySetting, error) {
	setting := darwinProxySetting{Service: service}
	out, err := hiddenCommand("networksetup", "-getwebproxy", service).Output()
	if err != nil {
		return setting, fmt.Errorf("读取 %s 的代理设置失败: %w", service, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Enabled":
			setting.Enabled = strings.EqualFold(value, "yes")
		case "Server":
			setting.Server = value
		case "Port":
			setting.Port, _ = strconv.Atoi(value)
		}
	}
	return setting, nil
}

func darwinSetProxy(service string, enable bool, server string, port int) error {
	if !enable {
		if err := hiddenCommand("networksetup", "-setwebproxystate", service, "off").Run(); err != nil {
			return fmt.Errorf("关闭 %s 的 HTTP 代理失败: %w", service, err)
		}
		if err := hiddenCommand("networksetup", "-setsecurewebproxystate", service, "off").Run(); err != nil {
			return fmt.Errorf("关闭 %s 的 HTTPS 代理失败: %w", service, err)
		}
		return nil
	}
	portText := strconv.Itoa(port)
	if err := hiddenCommand("networksetup", "-setwebproxy", service, server, portText).Run(); err != nil {
		return fmt.Errorf("设置 %s 的 HTTP 代理失败: %w", service, err)
	}
	if err := hiddenCommand("networksetup", "-setsecurewebproxy", service, server, portText).Run(); err != nil {
		return fmt.Errorf("设置 %s 的 HTTPS 代理失败: %w", service, err)
	}
	return nil
}

func darwinCaptureBackup(services []string) error {
	existing, err := darwinLoadBackup()
	if err != nil {
		return err
	}
	if existing != nil && len(existing.Services) > 0 {
		// MeshMux already owns the proxy; keep the original snapshot instead of
		// overwriting it with MeshMux's own values.
		return nil
	}
	snapshot := darwinProxyBackup{Services: make([]darwinProxySetting, 0, len(services))}
	for _, service := range services {
		setting, readErr := darwinReadProxy(service)
		if readErr != nil {
			return readErr
		}
		snapshot.Services = append(snapshot.Services, setting)
	}
	return darwinSaveBackup(snapshot)
}

func darwinRestoreService(service string) error {
	backup, err := darwinLoadBackup()
	if err != nil {
		return err
	}
	if backup != nil {
		for _, saved := range backup.Services {
			if saved.Service != service {
				continue
			}
			if saved.Enabled && saved.Server != "" && saved.Port > 0 {
				return darwinSetProxy(service, true, saved.Server, saved.Port)
			}
			return darwinSetProxy(service, false, "", 0)
		}
	}
	// No recorded original value: the safest end state is a disabled proxy.
	return darwinSetProxy(service, false, "", 0)
}

func darwinLoadBackup() (*darwinProxyBackup, error) {
	data, err := os.ReadFile(darwinProxyBackupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取系统代理备份失败: %w", err)
	}
	var backup darwinProxyBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return nil, fmt.Errorf("解析系统代理备份失败: %w", err)
	}
	return &backup, nil
}

func darwinSaveBackup(backup darwinProxyBackup) error {
	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFile(darwinProxyBackupPath, append(data, '\n'), 0600)
}

func darwinClearBackup() error {
	err := os.Remove(darwinProxyBackupPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清除系统代理备份失败: %w", err)
	}
	return nil
}
