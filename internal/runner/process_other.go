//go:build !windows

package runner

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}

func canStartTUN() bool {
	return true
}

func stopMihomoOnPorts(ports []int) error {
	return nil
}
