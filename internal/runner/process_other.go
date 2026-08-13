//go:build linux

package runner

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func hideWindow(cmd *exec.Cmd) {}

func canStartTUN() bool {
	if !hasEffectiveCapability(12) {
		return false
	}
	file, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	return file.Close() == nil
}

func hasEffectiveCapability(bit uint) bool {
	data, err := os.ReadFile("/proc/self/status")
	return err == nil && statusHasEffectiveCapability(string(data), bit)
}

func statusHasEffectiveCapability(status string, bit uint) bool {
	for _, line := range strings.Split(status, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != "CapEff" {
			continue
		}
		caps, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		return err == nil && caps&(uint64(1)<<bit) != 0
	}
	return false
}

func tunUnavailableMessage() string {
	return "TUN 模式需要可读写的 /dev/net/tun；请检查设备、权限和容器 capabilities，或关闭 TUN"
}

type nativeProcessSystem struct{}

func (nativeProcessSystem) executablePath(pid int) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
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
	inodes, err := listeningSocketInodes(wanted)
	if err != nil {
		return nil, err
	}
	if len(inodes) == 0 {
		return nil, nil
	}
	owners, err := processesForSocketInodes(inodes)
	if err != nil {
		return nil, err
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].Port == owners[j].Port {
			return owners[i].PID < owners[j].PID
		}
		return owners[i].Port < owners[j].Port
	})
	return owners, nil
}

func listeningSocketInodes(wanted map[int]bool) (map[string]int, error) {
	inodes := make(map[string]int)
	var firstErr error
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		file, err := os.Open(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			_, rawPort, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			port64, err := strconv.ParseUint(rawPort, 16, 16)
			if err == nil && wanted[int(port64)] {
				inodes[fields[9]] = int(port64)
			}
		}
		if err := scanner.Err(); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = file.Close()
	}
	if len(inodes) == 0 && firstErr != nil {
		return nil, fmt.Errorf("read Linux listening sockets: %w", firstErr)
	}
	return inodes, nil
}

func processesForSocketInodes(inodes map[string]int) ([]portOwner, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read Linux process table: %w", err)
	}
	seen := make(map[portOwner]bool)
	var owners []portOwner
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			port, ok := inodes[inode]
			if !ok {
				continue
			}
			owner := portOwner{Port: port, PID: pid}
			if !seen[owner] {
				seen[owner] = true
				owners = append(owners, owner)
			}
		}
	}
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
