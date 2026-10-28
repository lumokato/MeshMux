package main

import (
	"context"
	"fmt"
	"os"

	"github.com/meshmux/meshmux/internal/runner"
)

// coreService exposes the macOS root core daemon to the tray, scripts and
// manual recovery. Only "status" and "plan" are read-only; everything else is
// elevated by runner.CoreDaemonService.
func coreService(args []string) error {
	if len(args) == 1 && args[0] == "status" {
		fmt.Print(runner.CoreDaemonStatus())
		return nil
	}
	if len(args) == 2 && args[0] == "plan" {
		script, err := runner.CoreDaemonPlan(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, script)
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("core-service expects status, plan <action>, install, sync, uninstall, start, stop, or restart")
	}
	output, err := runner.CoreDaemonService(context.Background(), args[0])
	if output != "" {
		fmt.Fprintln(os.Stdout, output)
	}
	return err
}
