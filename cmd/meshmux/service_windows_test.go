//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/winservice"
	"golang.org/x/sys/windows/svc"
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
	if !serviceSpecific || exitCode != 1 {
		t.Fatalf("serviceFailure = (%v, %d), want (true, 1)", serviceSpecific, exitCode)
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

func TestServiceRestartsCoreOnceAfterResumeEvents(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	configPath := filepath.Join(home, config.DefaultConfigPath)
	profilePath := filepath.Join(home, "profiles", "windows.yaml")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("mixed-port: 2080\n"), 0600); err != nil {
		t.Fatal(err)
	}

	originalRun := runServiceCore
	originalDelay := serviceResumeDelay
	t.Cleanup(func() {
		runServiceCore = originalRun
		serviceResumeDelay = originalDelay
	})
	serviceResumeDelay = 20 * time.Millisecond
	started := make(chan struct{}, 3)
	runServiceCore = func(ctx context.Context, _ *config.Config, _ string, ready func(int) error) error {
		started <- struct{}{}
		if err := ready(100); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}

	requests := make(chan svc.ChangeRequest, 4)
	changes := make(chan svc.Status, 4)
	done := make(chan struct {
		specific bool
		code     uint32
	}, 1)
	go func() {
		specific, code := (&serviceHandler{configPath: configPath}).Execute(nil, requests, changes)
		done <- struct {
			specific bool
			code     uint32
		}{specific: specific, code: code}
	}()

	waitServiceStatus(t, changes, svc.StartPending)
	waitServiceStatus(t, changes, svc.Running)
	waitSignal(t, started, "initial core start")
	requests <- svc.ChangeRequest{Cmd: svc.PowerEvent, EventType: powerEventResumeAutomatic}
	requests <- svc.ChangeRequest{Cmd: svc.PowerEvent, EventType: powerEventResumeSuspend}
	waitSignal(t, started, "resumed core restart")
	select {
	case <-started:
		t.Fatal("resume events started more than one replacement core")
	case <-time.After(60 * time.Millisecond):
	}

	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	waitServiceStatus(t, changes, svc.StopPending)
	select {
	case result := <-done:
		if result.specific || result.code != 0 {
			t.Fatalf("service result = (%v, %d)", result.specific, result.code)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not stop")
	}
}

func TestServiceResumeFailureReturnsErrorWithoutPanic(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	configPath := filepath.Join(home, config.DefaultConfigPath)
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "profiles", "windows.yaml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	originalRun, originalDelay := runServiceCore, serviceResumeDelay
	t.Cleanup(func() {
		runServiceCore, serviceResumeDelay = originalRun, originalDelay
	})
	serviceResumeDelay = time.Millisecond
	attempts := 0
	runServiceCore = func(ctx context.Context, _ *config.Config, _ string, ready func(int) error) error {
		attempts++
		if attempts == 2 {
			return errors.New("replacement failed")
		}
		if err := ready(100); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	requests := make(chan svc.ChangeRequest, 1)
	requests <- svc.ChangeRequest{Cmd: svc.PowerEvent, EventType: powerEventResumeAutomatic}
	changes := make(chan svc.Status, 8)
	specific, code := (&serviceHandler{configPath: configPath}).Execute(nil, requests, changes)
	if !specific || code != 1 || attempts != 2 {
		t.Fatalf("result=(%v, %d), attempts=%d", specific, code, attempts)
	}
	data, err := os.ReadFile(filepath.Join(home, "logs", "service.log"))
	if err != nil || !strings.Contains(string(data), "replacement failed") {
		t.Fatalf("missing failure diagnostic: %s, %v", data, err)
	}
}

func waitServiceStatus(t *testing.T, changes <-chan svc.Status, want svc.State) {
	t.Helper()
	select {
	case status := <-changes:
		if status.State != want {
			t.Fatalf("service state = %v, want %v", status.State, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("service did not report state %v", want)
	}
}

func waitSignal(t *testing.T, signals <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
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

func TestWriteWindowsCommandResultRecordsSuccessAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.txt")
	writeWindowsCommandResult([]string{"service", "restart", "-result", path}, nil)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "ok\n" {
		t.Fatalf("success result = %q, %v", data, err)
	}
	writeWindowsCommandResult([]string{"service", "restart", "-result", path}, os.ErrPermission)
	data, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "error: "+os.ErrPermission.Error()) {
		t.Fatalf("error result = %q, %v", data, err)
	}
}

func TestActivateWindowsServiceRestoresUserCoreWhenServiceStartFails(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	originalControl := controlWindowsService
	originalStop := stopUserCore
	originalStart := startUserCore
	originalVerify := verifyWindowsService
	originalPrepare := prepareWindowsSnapshot
	t.Cleanup(func() {
		controlWindowsService = originalControl
		stopUserCore = originalStop
		startUserCore = originalStart
		verifyWindowsService = originalVerify
		prepareWindowsSnapshot = originalPrepare
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
	prepareWindowsSnapshot = func(string) (string, error) {
		actions = append(actions, "prepare")
		return configPath, nil
	}

	err := activateWindowsService(configPath)
	if err == nil || !strings.Contains(err.Error(), "previous user core was restored") {
		t.Fatalf("activate error = %v", err)
	}
	want := []string{"stop", "stop-user", "prepare", "start", "stop", "restore-user"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
}

func TestActivateWindowsServiceRestoresUserCoreWhenReadinessFails(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	originalControl := controlWindowsService
	originalStop := stopUserCore
	originalStart := startUserCore
	originalVerify := verifyWindowsService
	originalPrepare := prepareWindowsSnapshot
	t.Cleanup(func() {
		controlWindowsService = originalControl
		stopUserCore = originalStop
		startUserCore = originalStart
		verifyWindowsService = originalVerify
		prepareWindowsSnapshot = originalPrepare
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
	prepareWindowsSnapshot = func(string) (string, error) {
		actions = append(actions, "prepare")
		return configPath, nil
	}

	err := activateWindowsService(configPath)
	if err == nil || !strings.Contains(err.Error(), "previous user core was restored") {
		t.Fatalf("activate error = %v", err)
	}
	want := []string{"stop", "stop-user", "prepare", "start", "verify", "stop", "restore-user"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
}

func TestRestartWindowsServiceStopsServiceBeforeUserCore(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	t.Setenv("ProgramData", t.TempDir())
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	originalControl := controlWindowsService
	originalRunning := windowsServiceRunning
	originalStop := stopUserCore
	originalVerify := verifyWindowsService
	originalPrepare := prepareWindowsSnapshot
	t.Cleanup(func() {
		controlWindowsService = originalControl
		windowsServiceRunning = originalRunning
		stopUserCore = originalStop
		verifyWindowsService = originalVerify
		prepareWindowsSnapshot = originalPrepare
	})
	var actions []string
	windowsServiceRunning = func() bool { return true }
	controlWindowsService = func(action string, _ time.Duration) error {
		actions = append(actions, action)
		return nil
	}
	stopUserCore = func(*config.Config) error {
		actions = append(actions, "stop-user")
		return nil
	}
	prepareWindowsSnapshot = func(string) (string, error) {
		actions = append(actions, "prepare")
		return configPath, nil
	}
	verifyWindowsService = func(*config.Config, time.Duration) error {
		actions = append(actions, "verify")
		return nil
	}

	if err := restartWindowsService("restart", configPath); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop", "stop-user", "prepare", "start", "verify"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
}

func TestRestartWindowsServiceRestoresSnapshotWhenStartFails(t *testing.T) {
	home := t.TempDir()
	programData := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	t.Setenv("ProgramData", programData)
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	serviceConfig := filepath.Join(programData, "MeshMux", config.DefaultConfigPath)
	serviceProfile := filepath.Join(programData, "MeshMux", "profiles", "windows.yaml")
	if err := os.MkdirAll(filepath.Dir(serviceProfile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serviceConfig, []byte("old-config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serviceProfile, []byte("old-profile"), 0600); err != nil {
		t.Fatal(err)
	}

	originalControl := controlWindowsService
	originalRunning := windowsServiceRunning
	originalStop := stopUserCore
	originalPrepare := prepareWindowsSnapshot
	t.Cleanup(func() {
		controlWindowsService = originalControl
		windowsServiceRunning = originalRunning
		stopUserCore = originalStop
		prepareWindowsSnapshot = originalPrepare
	})
	var starts int
	windowsServiceRunning = func() bool { return true }
	controlWindowsService = func(action string, _ time.Duration) error {
		if action == "start" {
			starts++
			if starts == 1 {
				return errors.New("new service failed")
			}
		}
		return nil
	}
	stopUserCore = func(*config.Config) error { return nil }
	prepareWindowsSnapshot = func(string) (string, error) {
		if err := os.WriteFile(serviceConfig, []byte("new-config"), 0600); err != nil {
			return "", err
		}
		if err := os.WriteFile(serviceProfile, []byte("new-profile"), 0600); err != nil {
			return "", err
		}
		return serviceConfig, nil
	}

	err := restartWindowsService("restart", configPath)
	if err == nil || !strings.Contains(err.Error(), "previous service snapshot was restored") {
		t.Fatalf("restart error = %v", err)
	}
	if starts != 2 {
		t.Fatalf("service starts = %d, want 2", starts)
	}
	if data, _ := os.ReadFile(serviceConfig); string(data) != "old-config" {
		t.Fatalf("restored config = %q", data)
	}
	if data, _ := os.ReadFile(serviceProfile); string(data) != "old-profile" {
		t.Fatalf("restored profile = %q", data)
	}
}

func TestRestartWindowsServiceRestoresSnapshotWhenStopFails(t *testing.T) {
	home := t.TempDir()
	programData := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	t.Setenv("ProgramData", programData)
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	serviceConfig := filepath.Join(programData, "MeshMux", config.DefaultConfigPath)
	serviceProfile := filepath.Join(programData, "MeshMux", "profiles", "windows.yaml")
	if err := os.MkdirAll(filepath.Dir(serviceProfile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serviceConfig, []byte("old-config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serviceProfile, []byte("old-profile"), 0600); err != nil {
		t.Fatal(err)
	}

	originalControl := controlWindowsService
	originalRunning := windowsServiceRunning
	originalStop := stopUserCore
	originalPrepare := prepareWindowsSnapshot
	t.Cleanup(func() {
		controlWindowsService = originalControl
		windowsServiceRunning = originalRunning
		stopUserCore = originalStop
		prepareWindowsSnapshot = originalPrepare
	})
	windowsServiceRunning = func() bool { return true }
	controlWindowsService = func(action string, _ time.Duration) error {
		if action == "stop" {
			return errors.New("stop failed")
		}
		return nil
	}
	stopUserCore = func(*config.Config) error {
		t.Fatal("user core was touched after service stop failed")
		return nil
	}
	prepareWindowsSnapshot = func(string) (string, error) {
		if err := os.WriteFile(serviceConfig, []byte("new-config"), 0600); err != nil {
			return "", err
		}
		if err := os.WriteFile(serviceProfile, []byte("new-profile"), 0600); err != nil {
			return "", err
		}
		return serviceConfig, nil
	}

	if err := restartWindowsService("restart", configPath); err == nil || !strings.Contains(err.Error(), "stop existing service") {
		t.Fatalf("restart error = %v", err)
	}
	if data, _ := os.ReadFile(serviceConfig); string(data) != "old-config" {
		t.Fatalf("restored config = %q", data)
	}
	if data, _ := os.ReadFile(serviceProfile); string(data) != "old-profile" {
		t.Fatalf("restored profile = %q", data)
	}
}

func TestStartWindowsServiceDoesNotStopRunningService(t *testing.T) {
	originalRunning := windowsServiceRunning
	originalStop := stopUserCore
	t.Cleanup(func() {
		windowsServiceRunning = originalRunning
		stopUserCore = originalStop
	})
	windowsServiceRunning = func() bool { return true }
	stopUserCore = func(*config.Config) error {
		t.Fatal("running service was treated as a user core")
		return nil
	}
	if err := restartWindowsService("start", `C:\missing\meshmux.local.json`); err != nil {
		t.Fatal(err)
	}
}

func TestTailnetNeedsForcedLoginOnlyWithoutValidState(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.AuthKey = "test-key"
	home := t.TempDir()
	if !tailnetNeedsForcedLogin(cfg, home) {
		t.Fatal("missing state did not request forced login")
	}
	writeValidTailnetState(t, filepath.Join(home, "state", "tailscale"))
	if tailnetNeedsForcedLogin(cfg, home) {
		t.Fatal("valid state requested forced login")
	}
}

func writeValidTailnetState(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"_machinekey":"machine","_current-profile":"current","_profiles":"profiles"}`)
	if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestActivateWindowsServiceDoesNotStopUserCoreWhenServiceStopFails(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	configPath := filepath.Join(home, "meshmux.local.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true}}`), 0600); err != nil {
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

func TestRejectBootstrapRegressionProtectsServiceSnapshot(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.json")
	current := filepath.Join(dir, "current.json")
	if err := os.WriteFile(source, []byte(`{"name":"default","setup":{"providerUrl":""}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte(`{"name":"current","setup":{"providerUrl":"https://secret.invalid/sub"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rejectBootstrapRegression(source, current); err == nil || !strings.Contains(err.Error(), "拒绝") {
		t.Fatalf("regression error = %v", err)
	}
	if err := os.WriteFile(source, []byte(`{"setup":{"allowDirectOnly":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rejectBootstrapRegression(source, current); err != nil {
		t.Fatalf("explicit direct-only config rejected: %v", err)
	}
}

func TestSnapshotReferencedAssetsCopiesProviderAndWireGuard(t *testing.T) {
	root := t.TempDir()
	restoreWorkingDir(t)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join("providers", "main.yaml")
	wireguard := filepath.Join("wireguard", "office.conf")
	for path, data := range map[string]string{
		provider:  "proxies:\n  - name: node-a\n",
		wireguard: "[Interface]\nPrivateKey = hidden\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Providers: []config.Provider{{Name: "main", Path: provider}},
		WireGuard: config.WireGuard{Configs: []string{wireguard}},
	}
	destination := filepath.Join(root, "snapshot")
	if err := snapshotReferencedAssets(cfg, destination); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{provider, wireguard} {
		if _, err := os.Stat(filepath.Join(destination, path)); err != nil {
			t.Fatalf("snapshot asset %s: %v", path, err)
		}
	}
}

func TestSnapshotReferencedAssetsRejectsEscapingPath(t *testing.T) {
	cfg := &config.Config{WireGuard: config.WireGuard{Configs: []string{filepath.Join("..", "secret.conf")}}}
	if err := snapshotReferencedAssets(cfg, t.TempDir()); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaping path error = %v", err)
	}
}

func TestActivateIfReadyRestoresInstalledServiceAfterAnyValidationFailure(t *testing.T) {
	originalInstalled := windowsServiceInstalled
	originalControl := controlWindowsService
	t.Cleanup(func() {
		windowsServiceInstalled = originalInstalled
		controlWindowsService = originalControl
	})
	windowsServiceInstalled = func() bool { return true }
	started := false
	controlWindowsService = func(action string, _ time.Duration) error {
		started = action == "start"
		return nil
	}
	err := handleSnapshotPreparationError("activate-if-ready", errors.New("invalid configuration"))
	if err == nil || !strings.Contains(err.Error(), "previous service was restored") || !started {
		t.Fatalf("recovery result = %v, started = %v", err, started)
	}
}

func TestActivateIfReadyDoesNotHideUnexpectedFirstInstallFailure(t *testing.T) {
	originalInstalled := windowsServiceInstalled
	t.Cleanup(func() { windowsServiceInstalled = originalInstalled })
	windowsServiceInstalled = func() bool { return false }
	want := errors.New("invalid configuration")
	if got := handleSnapshotPreparationError("activate-if-ready", want); !errors.Is(got, want) {
		t.Fatalf("first-install error = %v", got)
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

func TestSnapshotRollbackIncludesAssetsAndRemovesNewFiles(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir())
	home := winservice.DataDir()
	old := filepath.Join(home, "providers", "main.yaml")
	fresh := filepath.Join(home, "wireguard", "new.conf")
	if err := writeSnapshotFile(old, []byte("old-cache")); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{WireGuard: config.WireGuard{Configs: []string{"wireguard/new.conf"}}}
	backup, err := captureServiceSnapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshotFile(old, []byte("new-cache")); err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshotFile(fresh, []byte("new-key")); err != nil {
		t.Fatal(err)
	}
	if err := restoreServiceSnapshot(backup); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(old)
	if err != nil || string(data) != "old-cache" {
		t.Fatalf("asset rollback: %q %v", data, err)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatal("new asset survived rollback")
	}
}

func TestRestartRollsBackPartiallyWrittenAssetsOnPrepareFailure(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	t.Setenv("MESHMUX_HOME", home)
	t.Setenv("ProgramData", t.TempDir())
	source := filepath.Join(home, config.DefaultConfigPath)
	if err := writeSnapshotFile(source, []byte(`{"setup":{"allowDirectOnly":true}}`)); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(winservice.DataDir(), "providers", "main.yaml")
	if err := writeSnapshotFile(cache, []byte("old-cache")); err != nil {
		t.Fatal(err)
	}
	originalPrepare, originalControl, originalStop := prepareWindowsSnapshot, controlWindowsService, stopUserCore
	t.Cleanup(func() {
		prepareWindowsSnapshot = originalPrepare
		controlWindowsService = originalControl
		stopUserCore = originalStop
	})
	var actions []string
	controlWindowsService = func(action string, _ time.Duration) error { actions = append(actions, action); return nil }
	stopUserCore = func(*config.Config) error { return nil }
	prepareWindowsSnapshot = func(string) (string, error) {
		if err := writeSnapshotFile(cache, []byte("partially-new-cache")); err != nil {
			return "", err
		}
		return "", errors.New("injected snapshot failure")
	}
	if err := restartWindowsService("restart", source); err == nil {
		t.Fatal("expected preparation failure")
	}
	data, err := os.ReadFile(cache)
	if err != nil || string(data) != "old-cache" {
		t.Fatalf("partial cache survived: %q %v", data, err)
	}
	if strings.Join(actions, ",") != "stop,start" {
		t.Fatalf("actions=%v", actions)
	}
}
