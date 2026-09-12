//go:build windows

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/meshmux/meshmux/internal/winservice"
)

func Autostart(mode string) error {
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
	if winservice.Installed() {
		return true
	}
	out, err := hiddenCommand("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "MeshMux").CombinedOutput()
	return err == nil && strings.Contains(string(out), "MeshMux")
}
