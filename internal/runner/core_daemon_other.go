//go:build !darwin

package runner

import (
	"context"
	"errors"
)

// The root LaunchDaemon core is a macOS-only ownership model. Other platforms
// keep their service-manager or user-mode behavior.

func CoreDaemonInstalled() bool { return false }

func CoreDaemonPID() (int, bool) { return 0, false }

func CoreDaemonStatus() string { return "MeshMux 系统核心守护：仅 macOS 支持\n" }

func CoreDaemonPlan(string) (string, error) {
	return "", errors.New("系统核心守护仅 macOS 支持")
}

func CoreDaemonService(context.Context, string) (string, error) {
	return "", errors.New("系统核心守护仅 macOS 支持")
}
