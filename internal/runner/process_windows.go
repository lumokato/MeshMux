//go:build windows

package runner

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func canStartTUN() bool {
	cmd := exec.Command("net", "session")
	hideWindow(cmd)
	return cmd.Run() == nil
}

func stopMihomoOnPorts(ports []int) error {
	pids := map[string]bool{}
	for _, port := range ports {
		if port <= 0 {
			continue
		}
		cmd := hiddenCommand("cmd", "/c", "netstat -ano -p tcp | findstr LISTENING | findstr :"+strconv.Itoa(port))
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			local := fields[1]
			if !strings.HasSuffix(local, ":"+strconv.Itoa(port)) {
				continue
			}
			pid := fields[len(fields)-1]
			if pid != "" && pid != "0" {
				pids[pid] = true
			}
		}
	}
	for pid := range pids {
		cmd := hiddenCommand("cmd", "/c", "tasklist /FI \"PID eq "+pid+"\" /FO CSV /NH")
		nameOut, err := cmd.Output()
		if err != nil || !strings.Contains(strings.ToLower(string(nameOut)), "mihomo.exe") {
			continue
		}
		kill := hiddenCommand("taskkill", "/PID", pid, "/F", "/T")
		if out, err := kill.CombinedOutput(); err != nil {
			return fmt.Errorf("停止残留 mihomo PID %s 失败: %w: %s", pid, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
