//go:build darwin

package main

import (
	"context"
	"fmt"
	"github.com/meshmux/meshmux/internal/runner"
	"os"
)

func macTailnetService(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("tailnet-service expects plan, install, start, stop, restart, login, down")
	}
	if args[0] == "plan" {
		s, err := runner.MacTailscaleInstallPlan()
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, s)
		return nil
	}
	s, err := runner.MacTailscaleService(context.Background(), args[0])
	if s != "" {
		fmt.Fprintln(os.Stdout, s)
	}
	return err
}
