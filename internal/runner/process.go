package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

const pidFilePath = "state/mihomo.pid"

type portOwner struct {
	Port int
	PID  int
}

type processSystem interface {
	executablePath(pid int) (string, error)
	listeningProcesses(ports []int) ([]portOwner, error)
	kill(pid int) error
}

var (
	processOS          processSystem = nativeProcessSystem{}
	stopProcessTimeout               = 5 * time.Second
	stopPollInterval                 = 100 * time.Millisecond
	stopQuietPeriod                  = 500 * time.Millisecond
)

func findManagedPID(cfg *config.Config) (int, bool) {
	expected, err := expectedMihomoPath(cfg)
	if err != nil {
		return 0, false
	}
	ports := discoveryPorts(cfg)
	owners, _ := processOS.listeningProcesses(ports)
	for _, port := range ports {
		for _, owner := range owners {
			if owner.Port != port || !managedPIDMatches(owner.PID, expected) {
				continue
			}
			if current, err := readPID(); err != nil || current != owner.PID {
				if err := writePID(owner.PID); err != nil {
					appendRunnerLog("PID 更新失败: %v", err)
				}
			}
			return owner.PID, true
		}
	}

	pid, err := readPID()
	if err == nil && managedPIDMatches(pid, expected) {
		return pid, true
	}
	if err == nil || (err != nil && !os.IsNotExist(err)) {
		_ = clearPID()
	}
	return 0, false
}

func managedPIDs(cfg *config.Config) ([]int, error) {
	expected, err := expectedMihomoPath(cfg)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	if pid, err := readPID(); err == nil && managedPIDMatches(pid, expected) {
		seen[pid] = true
	}
	owners, discoverErr := processOS.listeningProcesses(discoveryPorts(cfg))
	for _, owner := range owners {
		if managedPIDMatches(owner.PID, expected) {
			seen[owner.PID] = true
		}
	}
	if discoverErr != nil && len(seen) == 0 {
		return nil, discoverErr
	}
	pids := make([]int, 0, len(seen))
	for pid := range seen {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}

func managedPIDMatches(pid int, expected string) bool {
	if pid <= 0 {
		return false
	}
	actual, err := processOS.executablePath(pid)
	if err != nil || strings.TrimSpace(actual) == "" {
		return false
	}
	return sameExecutablePath(actual, expected)
}

func expectedMihomoPath(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is required")
	}
	path := strings.TrimSpace(cfg.Components.Mihomo.Path)
	if path == "" {
		path = filepath.Join("bin", "mihomo.exe")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func sameExecutablePath(a, b string) bool {
	clean := func(path string) string {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		return filepath.Clean(path)
	}
	a = clean(a)
	b = clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func discoveryPorts(cfg *config.Config) []int {
	var ports []int
	if cfg != nil {
		if port := controllerPort(cfg.Ports.Controller); port > 0 {
			ports = append(ports, port)
		}
		if cfg.Ports.Mixed > 0 && !containsPort(ports, cfg.Ports.Mixed) {
			ports = append(ports, cfg.Ports.Mixed)
		}
	}
	return ports
}

func controllerPort(address string) int {
	address = strings.TrimSpace(address)
	if address == "" {
		return 0
	}
	if index := strings.LastIndex(address, ":"); index >= 0 && index+1 < len(address) {
		port, _ := strconv.Atoi(strings.Trim(address[index+1:], "[]"))
		return port
	}
	return 0
}

func containsPort(ports []int, wanted int) bool {
	for _, port := range ports {
		if port == wanted {
			return true
		}
	}
	return false
}

func writePID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID %d", pid)
	}
	if err := os.MkdirAll(filepath.Dir(pidFilePath), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(pidFilePath), "mihomo.pid.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := fmt.Fprintf(tmp, "%d\n", pid); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpPath, pidFilePath)
}

func clearPID() error {
	err := os.Remove(pidFilePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
