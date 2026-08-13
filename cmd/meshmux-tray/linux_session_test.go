//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLinuxTraySessionRequiresGraphicalSessionAndBus(t *testing.T) {
	runtimeDir := t.TempDir()
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "display", env: map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus", "XDG_RUNTIME_DIR": runtimeDir}, want: "DISPLAY or WAYLAND_DISPLAY"},
		{name: "session bus", env: map[string]string{"DISPLAY": ":10", "XDG_RUNTIME_DIR": runtimeDir}, want: "DBUS_SESSION_BUS_ADDRESS"},
		{name: "runtime directory", env: map[string]string{"DISPLAY": ":10", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}, want: "XDG_RUNTIME_DIR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateLinuxTraySession(func(key string) string { return tt.env[key] }, func(string) error {
				t.Fatal("session bus probe was called for invalid environment")
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateLinuxTraySession error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateLinuxTraySessionProbesBus(t *testing.T) {
	env := map[string]string{
		"DISPLAY":                  ":10",
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
		"XDG_RUNTIME_DIR":          t.TempDir(),
	}
	var probed string
	session, err := validateLinuxTraySession(func(key string) string { return env[key] }, func(address string) error {
		probed = address
		return nil
	})
	if err != nil {
		t.Fatalf("validateLinuxTraySession returned error: %v", err)
	}
	if probed != env["DBUS_SESSION_BUS_ADDRESS"] {
		t.Fatalf("probed bus = %q, want %q", probed, env["DBUS_SESSION_BUS_ADDRESS"])
	}
	if session.key == "" {
		t.Fatal("session key is empty")
	}
}

func TestLinuxTraySessionKeySeparatesXRDPDisplays(t *testing.T) {
	bus := "unix:path=/run/user/1000/bus"
	key10 := linuxTraySessionKey(":10", "", bus)
	key11 := linuxTraySessionKey(":11", "", bus)
	if key10 == key11 {
		t.Fatalf("XRDP display keys unexpectedly match: %q", key10)
	}
	if key10 != linuxTraySessionKey(":10", "", bus) {
		t.Fatal("session key is not stable")
	}
}

func TestLinuxSessionBusEndpoint(t *testing.T) {
	tests := map[string]string{
		"unix:path=/run/user/1000/bus":               "/run/user/1000/bus",
		"tcp:host=localhost;unix:abstract=dbus-test": "@dbus-test",
		"unix:path=/tmp/dbus%2Dbus":                  "/tmp/dbus-bus",
	}
	for raw, want := range tests {
		got, err := linuxSessionBusEndpoint(raw)
		if err != nil {
			t.Fatalf("linuxSessionBusEndpoint(%q) returned error: %v", raw, err)
		}
		if got != want {
			t.Fatalf("linuxSessionBusEndpoint(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestAcquireLinuxSessionLockIsPerGraphicalSession(t *testing.T) {
	runtimeDir := t.TempDir()
	first, err := acquireLinuxSessionLock(linuxTraySession{runtimeDir: runtimeDir, key: "display-10"})
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer first.Close()
	if _, err := acquireLinuxSessionLock(linuxTraySession{runtimeDir: runtimeDir, key: "display-10"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock error = %v, want already running", err)
	}
	secondSession, err := acquireLinuxSessionLock(linuxTraySession{runtimeDir: runtimeDir, key: "display-11"})
	if err != nil {
		t.Fatalf("acquire independent display lock: %v", err)
	}
	defer secondSession.Close()
}

func TestValidateLinuxTraySessionReportsBusProbeFailure(t *testing.T) {
	env := map[string]string{
		"DISPLAY":                  ":10",
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=/missing/bus",
		"XDG_RUNTIME_DIR":          t.TempDir(),
	}
	_, err := validateLinuxTraySession(func(key string) string { return env[key] }, func(string) error {
		return errors.New("connection refused")
	})
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("bus probe error = %v, want connection refused", err)
	}
}

func TestLinuxTrayLogPathUsesMeshMuxHome(t *testing.T) {
	home := t.TempDir()
	dataDir, err := resolveLinuxDataDir(func(key string) string {
		if key == "MESHMUX_HOME" {
			return home
		}
		return ""
	}, os.UserHomeDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "logs", "tray.log")
	if got := filepath.Join(dataDir, "logs", "tray.log"); got != want {
		t.Fatalf("tray log path = %q, want %q", got, want)
	}
}
