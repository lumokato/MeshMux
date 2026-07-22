//go:build windows

package runner

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func canStartTUN() bool {
	cmd := exec.Command("net", "session")
	hideWindow(cmd)
	return cmd.Run() == nil
}

type nativeProcessSystem struct{}

func (nativeProcessSystem) executablePath(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func (nativeProcessSystem) listeningProcesses(ports []int) ([]portOwner, error) {
	wanted := map[int]bool{}
	for _, port := range ports {
		if port > 0 {
			wanted[port] = true
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	cmd := hiddenCommand("netstat", "-ano", "-p", "tcp")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("查询监听端口失败: %w: %s", err, strings.TrimSpace(string(out)))
	}
	seen := map[portOwner]bool{}
	var owners []portOwner
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		port := netstatPort(fields[1])
		if !wanted[port] {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || pid <= 0 {
			continue
		}
		owner := portOwner{Port: port, PID: pid}
		if !seen[owner] {
			seen[owner] = true
			owners = append(owners, owner)
		}
	}
	return owners, nil
}

func (nativeProcessSystem) kill(pid int) error {
	cmd := hiddenCommand("taskkill", "/PID", strconv.Itoa(pid), "/F", "/T")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("停止 mihomo PID %d 失败: %w: %s", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func netstatPort(address string) int {
	index := strings.LastIndex(address, ":")
	if index < 0 || index+1 >= len(address) {
		return 0
	}
	port, _ := strconv.Atoi(address[index+1:])
	return port
}

func replaceFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
