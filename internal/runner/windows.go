package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/meshmux/meshmux/internal/winservice"
)

func Proxy(mode string, port int) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("system proxy is only implemented on Windows")
	}
	return platformProxy(mode, port)
}

func ProxyEnabled() bool { return platformProxyEnabled() }

func Autostart(mode string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("autostart is only implemented on Windows")
	}
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	if winservice.Installed() {
		switch mode {
		case "on", "show":
			return nil
		case "off":
			return fmt.Errorf("服务模式下开机自启由安装器管理")
		}
	}
	switch mode {
	case "on":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		value := fmt.Sprintf(`"%s" start`, filepath.Clean(exe))
		return hiddenCommand("reg", "add", key, "/v", "MeshMux", "/t", "REG_SZ", "/d", value, "/f").Run()
	case "off":
		if !AutostartEnabled() {
			return nil
		}
		return hiddenCommand("reg", "delete", key, "/v", "MeshMux", "/f").Run()
	case "show":
		cmd := hiddenCommand("reg", "query", key, "/v", "MeshMux")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	default:
		return fmt.Errorf("autostart expects on, off, or show")
	}
}

func AutostartEnabled() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if winservice.Installed() {
		return true
	}
	out, err := hiddenCommand("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "MeshMux").CombinedOutput()
	return err == nil && strings.Contains(string(out), "MeshMux")
}
