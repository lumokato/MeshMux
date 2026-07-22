//go:build !windows

package runner

import (
	"fmt"
	"os"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd) {}

func canStartTUN() bool {
	return true
}

type nativeProcessSystem struct{}

func (nativeProcessSystem) executablePath(pid int) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
}

func (nativeProcessSystem) listeningProcesses(ports []int) ([]portOwner, error) {
	return nil, nil
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
