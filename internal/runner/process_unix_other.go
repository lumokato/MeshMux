//go:build !windows && !linux

package runner

import (
	"fmt"
	"os"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd) {}

func canStartTUN() bool {
	return false
}

func tunUnavailableMessage() string {
	return "TUN mode is not supported by MeshMux on this platform"
}

type nativeProcessSystem struct{}

func (nativeProcessSystem) executablePath(pid int) (string, error) {
	return "", fmt.Errorf("process executable discovery is not supported on this platform")
}

func (nativeProcessSystem) listeningProcesses(ports []int) ([]portOwner, error) {
	return nil, fmt.Errorf("listening process discovery is not supported on this platform")
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
