//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

func TestRemoveCommandArgPreservesFlags(t *testing.T) {
	got := removeCommandArg([]string{"install", "-config", `C:\Users\test\MeshMux\meshmux.local.json`}, "install")
	if len(got) != 2 || got[0] != "-config" || got[1] != `C:\Users\test\MeshMux\meshmux.local.json` {
		t.Fatalf("filtered args = %#v", got)
	}
}

func TestServiceFailureWritesProtectedDiagnostic(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "meshmux.local.json")
	serviceSpecific, exitCode := serviceFailure(configPath, "start core", os.ErrPermission)
	if serviceSpecific || exitCode != 1 {
		t.Fatalf("serviceFailure = (%v, %d), want (false, 1)", serviceSpecific, exitCode)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(configPath), "logs", "service.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "start core") || !strings.Contains(text, os.ErrPermission.Error()) {
		t.Fatalf("service log = %q", text)
	}
}

func TestWriteWindowsCommandErrorSkipsNonServiceCommand(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir())
	writeWindowsCommandError([]string{"status"}, os.ErrPermission)
	if _, err := os.Stat(filepath.Join(winserviceDataDirForTest(), "logs", "service-command.log")); !os.IsNotExist(err) {
		t.Fatalf("non-service command log exists: %v", err)
	}
}

func TestWriteWindowsCommandErrorUsesConfigDirectory(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "meshmux.local.json")
	writeWindowsCommandError([]string{"service", "install", "-config", configPath}, os.ErrPermission)
	data, err := os.ReadFile(filepath.Join(home, "logs", "service-command.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), os.ErrPermission.Error()) {
		t.Fatalf("service command log = %q", data)
	}
}

func TestActivateWindowsServiceRestoresUserCoreWhenServiceStartFails(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test"}`), 0600); err != nil {
		t.Fatal(err)
	}

	originalControl := controlWindowsService
	originalStop := stopUserCore
	originalStart := startUserCore
	originalVerify := verifyWindowsService
	t.Cleanup(func() {
		controlWindowsService = originalControl
		stopUserCore = originalStop
		startUserCore = originalStart
		verifyWindowsService = originalVerify
	})
	var actions []string
	controlWindowsService = func(action string, _ time.Duration) error {
		actions = append(actions, action)
		if action == "start" {
			return errors.New("service failed")
		}
		return nil
	}
	stopUserCore = func(*config.Config) error {
		actions = append(actions, "stop-user")
		return nil
	}
	startUserCore = func(_ *config.Config, gotConfig, gotProfile string) error {
		actions = append(actions, "restore-user")
		if gotConfig != configPath || !strings.HasSuffix(gotProfile, filepath.Join("profiles", "windows.yaml")) {
			t.Fatalf("restore paths = %q, %q", gotConfig, gotProfile)
		}
		return nil
	}
	verifyWindowsService = func(*config.Config, time.Duration) error {
		t.Fatal("service verification ran after start failed")
		return nil
	}

	err := activateWindowsService(configPath)
	if err == nil || !strings.Contains(err.Error(), "previous user core was restored") {
		t.Fatalf("activate error = %v", err)
	}
	want := []string{"stop", "stop-user", "start", "stop", "restore-user"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
}

func TestActivateWindowsServiceRestoresUserCoreWhenReadinessFails(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test"}`), 0600); err != nil {
		t.Fatal(err)
	}

	originalControl := controlWindowsService
	originalStop := stopUserCore
	originalStart := startUserCore
	originalVerify := verifyWindowsService
	t.Cleanup(func() {
		controlWindowsService = originalControl
		stopUserCore = originalStop
		startUserCore = originalStart
		verifyWindowsService = originalVerify
	})
	var actions []string
	controlWindowsService = func(action string, _ time.Duration) error {
		actions = append(actions, action)
		return nil
	}
	stopUserCore = func(*config.Config) error {
		actions = append(actions, "stop-user")
		return nil
	}
	verifyWindowsService = func(*config.Config, time.Duration) error {
		actions = append(actions, "verify")
		return errors.New("not ready")
	}
	startUserCore = func(*config.Config, string, string) error {
		actions = append(actions, "restore-user")
		return nil
	}

	err := activateWindowsService(configPath)
	if err == nil || !strings.Contains(err.Error(), "previous user core was restored") {
		t.Fatalf("activate error = %v", err)
	}
	want := []string{"stop", "stop-user", "start", "verify", "stop", "restore-user"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
}

func TestActivateWindowsServiceDoesNotStopUserCoreWhenServiceStopFails(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test"}`), 0600); err != nil {
		t.Fatal(err)
	}

	originalControl := controlWindowsService
	originalStop := stopUserCore
	t.Cleanup(func() {
		controlWindowsService = originalControl
		stopUserCore = originalStop
	})
	stoppedUser := false
	controlWindowsService = func(string, time.Duration) error { return errors.New("stop failed") }
	stopUserCore = func(*config.Config) error {
		stoppedUser = true
		return nil
	}

	if err := activateWindowsService(configPath); err == nil || !strings.Contains(err.Error(), "stop existing service") {
		t.Fatalf("activate error = %v", err)
	}
	if stoppedUser {
		t.Fatal("user core was stopped after service stop failed")
	}
}

func winserviceDataDirForTest() string {
	return filepath.Join(os.Getenv("ProgramData"), "MeshMux")
}

func restoreWorkingDir(t *testing.T) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
