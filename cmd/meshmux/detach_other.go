//go:build !windows

package main

import "os/exec"

func configureDetachedCommand(cmd *exec.Cmd) {}
