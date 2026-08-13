//go:build linux

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const gsettingsPath = "/usr/bin/gsettings"

type gsettingsController struct {
	runner  commandRunner
	timeout time.Duration
}

func newGSettingsController(runner commandRunner) gsettingsController {
	return gsettingsController{runner: runner, timeout: 5 * time.Second}
}

func (c gsettingsController) Enabled() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.commandTimeout())
	defer cancel()
	out, err := c.runner.CombinedOutput(ctx, gsettingsPath, "get", "org.gnome.system.proxy", "mode")
	if err != nil {
		return false, commandFailure("gsettings get proxy mode", out, err)
	}
	mode := strings.Trim(strings.TrimSpace(string(out)), "'\"")
	switch mode {
	case "manual":
		return true, nil
	case "none", "auto":
		return false, nil
	default:
		return false, fmt.Errorf("gsettings returned unexpected proxy mode %q", mode)
	}
}

func (c gsettingsController) SetEnabled(enabled bool, port int) error {
	if !enabled {
		return c.set("org.gnome.system.proxy", "mode", "none")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid proxy port %d", port)
	}
	portText := strconv.Itoa(port)
	settings := [][3]string{
		{"org.gnome.system.proxy.http", "host", "127.0.0.1"},
		{"org.gnome.system.proxy.http", "port", portText},
		{"org.gnome.system.proxy.https", "host", "127.0.0.1"},
		{"org.gnome.system.proxy.https", "port", portText},
		{"org.gnome.system.proxy.socks", "host", "127.0.0.1"},
		{"org.gnome.system.proxy.socks", "port", portText},
		{"org.gnome.system.proxy", "ignore-hosts", "['localhost', '127.0.0.0/8', '::1']"},
		{"org.gnome.system.proxy", "mode", "manual"},
	}
	for _, setting := range settings {
		if err := c.set(setting[0], setting[1], setting[2]); err != nil {
			return err
		}
	}
	return nil
}

func (c gsettingsController) set(schema, key, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.commandTimeout())
	defer cancel()
	out, err := c.runner.CombinedOutput(ctx, gsettingsPath, "set", schema, key, value)
	if err != nil {
		return commandFailure("gsettings set "+schema+" "+key, out, err)
	}
	return nil
}

func (c gsettingsController) commandTimeout() time.Duration {
	if c.timeout > 0 {
		return c.timeout
	}
	return 5 * time.Second
}
