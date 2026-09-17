//go:build darwin

package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

func hideWindow(cmd *exec.Cmd) {}

// macOS creates TUN interfaces (utun) only for privileged processes, so the
// effective uid is the whole check. Nothing is opened or created here.
func canStartTUN() bool {
	return os.Geteuid() == 0
}

func tunUnavailableMessage() string {
	return "TUN 模式需要以 root 运行：macOS 的 utun 设备仅对特权进程开放，请用 sudo 或关闭 TUN"
}

type nativeProcessSystem struct{}

func (nativeProcessSystem) executablePath(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid pid %d", pid)
	}
	// lsof reports the text (executable) mapping of a pid; -F n emits it as a
	// single "n/path/to/binary" line.
	if out, err := hiddenCommand("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) > 1 && line[0] == 'n' {
				if path := strings.TrimSpace(line[1:]); path != "" {
					return path, nil
				}
			}
		}
	}
	// Fall back to ps. With an absolute launch path this reports the resolved
	// executable, which is what callers compare against.
	out, err := hiddenCommand("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", fmt.Errorf("resolve executable for pid %d: %w", pid, err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("resolve executable for pid %d: no result", pid)
	}
	return path, nil
}

func (nativeProcessSystem) listeningProcesses(ports []int) ([]portOwner, error) {
	wanted := make(map[int]bool, len(ports))
	for _, port := range ports {
		if port > 0 {
			wanted[port] = true
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	// -F pcn emits one field per line: "p<pid>", "c<command>", "n<address>".
	// Address records belong to the most recent pid record. -n skips DNS
	// resolution and -P keeps numeric ports so parsing stays deterministic.
	out, err := hiddenCommand("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn").Output()
	if err != nil {
		var exitErr *exec.ExitError
		// lsof exits 1 when nothing matches; that is an empty result, not a failure.
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("list listening processes: %w", err)
	}
	var owners []portOwner
	seen := make(map[portOwner]bool)
	currentPID := 0
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, convErr := strconv.Atoi(strings.TrimSpace(line[1:]))
			if convErr != nil {
				currentPID = 0
				continue
			}
			currentPID = pid
		case 'n':
			if currentPID <= 0 {
				continue
			}
			port := portFromAddress(line[1:])
			if port <= 0 || !wanted[port] {
				continue
			}
			owner := portOwner{Port: port, PID: currentPID}
			if !seen[owner] {
				seen[owner] = true
				owners = append(owners, owner)
			}
		}
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].Port == owners[j].Port {
			return owners[i].PID < owners[j].PID
		}
		return owners[i].Port < owners[j].Port
	})
	return owners, nil
}

func (nativeProcessSystem) kill(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
