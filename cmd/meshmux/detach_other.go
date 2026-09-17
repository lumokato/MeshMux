//go:build !windows && !darwin

package main

import "os/exec"

func configureDetachedCommand(cmd *exec.Cmd) {}
