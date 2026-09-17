//go:build darwin

package runner

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/meshmux/meshmux/internal/fileutil"
)

// macOS autostart uses a per-user LaunchAgent. Writing the plist is the
// authoritative state; launchctl is only asked to load or unload it.
const darwinLaunchAgentLabel = "com.meshmux.agent"

func darwinLaunchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录失败: %w", err)
	}
	if home == "" {
		return "", errors.New("用户主目录为空，无法写入 LaunchAgent")
	}
	return filepath.Join(home, "Library", "LaunchAgents", darwinLaunchAgentLabel+".plist"), nil
}

func darwinLaunchAgentContent(executable string) (string, error) {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(executable)); err != nil {
		return "", fmt.Errorf("转义可执行文件路径失败: %w", err)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>start</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
</dict>
</plist>
`, darwinLaunchAgentLabel, escaped.String()), nil
}

func Autostart(mode string) error {
	path, err := darwinLaunchAgentPath()
	if err != nil {
		return err
	}
	switch mode {
	case "on":
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		content, err := darwinLaunchAgentContent(filepath.Clean(executable))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := fileutil.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
		// Registration is best effort: the plist is what makes the next login
		// start MeshMux, and launchctl only applies it to this session.
		if err := hiddenCommand("launchctl", "load", "-w", path).Run(); err != nil {
			appendRunnerLog("注册 LaunchAgent 失败（下次登录仍会生效）: %v", err)
		}
		return nil
	case "off":
		if err := hiddenCommand("launchctl", "unload", "-w", path).Run(); err != nil {
			appendRunnerLog("注销 LaunchAgent 失败: %v", err)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case "show":
		fmt.Printf("LaunchAgent: %s\n", path)
		fmt.Printf("enabled: %t\n", AutostartEnabled())
		return nil
	default:
		return fmt.Errorf("autostart expects on, off, or show")
	}
}

func AutostartEnabled() bool {
	path, err := darwinLaunchAgentPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
