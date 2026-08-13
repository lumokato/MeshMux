package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	systemctlPath = "/usr/bin/systemctl"
	sudoPath      = "/usr/bin/sudo"
	meshMuxUnit   = "meshmux.service"
)

var systemdActionAllowed = map[string]bool{
	"start":   true,
	"stop":    true,
	"restart": true,
	"enable":  true,
	"disable": true,
}

type commandRunner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type systemdController struct {
	runner  commandRunner
	timeout time.Duration
	unit    string
}

func newSystemdController(runner commandRunner) systemdController {
	return systemdController{
		runner:  runner,
		timeout: 10 * time.Second,
		unit:    meshMuxUnit,
	}
}

func (c systemdController) IsActive() (bool, error) {
	state, err := c.query("is-active", activeUnitStates)
	return state == "active", err
}

func (c systemdController) IsEnabled() (bool, error) {
	state, err := c.query("is-enabled", enabledUnitStates)
	return state == "enabled" || state == "enabled-runtime", err
}

func (c systemdController) Action(action string) error {
	if !systemdActionAllowed[action] {
		return fmt.Errorf("unsupported systemd action %q", action)
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.commandTimeout())
	defer cancel()
	out, err := c.runner.CombinedOutput(ctx, sudoPath, "-n", systemctlPath, action, c.unitName())
	if err != nil {
		return commandFailure("systemctl "+action, out, err)
	}
	return nil
}

func (c systemdController) query(action string, knownStates map[string]bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.commandTimeout())
	defer cancel()
	out, err := c.runner.CombinedOutput(ctx, systemctlPath, action, c.unitName())
	state := strings.TrimSpace(string(out))
	if knownStates[state] {
		return state, nil
	}
	if err != nil {
		return "", commandFailure("systemctl "+action, out, err)
	}
	return "", fmt.Errorf("systemctl %s returned unexpected state %q", action, state)
}

func (c systemdController) commandTimeout() time.Duration {
	if c.timeout > 0 {
		return c.timeout
	}
	return 10 * time.Second
}

func (c systemdController) unitName() string {
	if strings.TrimSpace(c.unit) != "" {
		return c.unit
	}
	return meshMuxUnit
}

func commandFailure(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, message)
}

var activeUnitStates = map[string]bool{
	"active":       true,
	"reloading":    true,
	"inactive":     true,
	"failed":       true,
	"activating":   true,
	"deactivating": true,
	"maintenance":  true,
	"unknown":      true,
}

var enabledUnitStates = map[string]bool{
	"enabled":         true,
	"enabled-runtime": true,
	"linked":          true,
	"linked-runtime":  true,
	"alias":           true,
	"masked":          true,
	"masked-runtime":  true,
	"static":          true,
	"indirect":        true,
	"disabled":        true,
	"generated":       true,
	"transient":       true,
	"bad":             true,
}
