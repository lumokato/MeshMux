//go:build darwin

package main

import (
	"os/exec"
	"syscall"
)

// configureDetachedCommand places the core in its own session so it survives
// the terminal that launched it and is not signalled when the user interrupts
// the launching shell.
func configureDetachedCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
