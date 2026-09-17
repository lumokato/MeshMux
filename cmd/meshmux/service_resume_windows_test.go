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
	"golang.org/x/sys/windows/svc"
)

// Resume must restart BOTH supervised components. The mihomo core is relaunched
// through a full stop+start, and the tailscale daemon - which survives suspend
// with dead WireGuard sessions while looking perfectly healthy to its supervisor -
// must get the same treatment. Skipping stopTailscale left the stale daemon
// holding the IPC pipe while every replacement failed with access denied.
func TestServiceResumeRestartsCoreAndTailscale(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	configPath := filepath.Join(home, config.DefaultConfigPath)
	profilePath := filepath.Join(home, "profiles", "windows.yaml")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true},"tailscale":{"enabled":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("mixed-port: 2080\n"), 0600); err != nil {
		t.Fatal(err)
	}

	originalRun := runServiceCore
	originalRunTS := runTailscaleSupervision
	originalDelay := serviceResumeDelay
	t.Cleanup(func() {
		runServiceCore = originalRun
		runTailscaleSupervision = originalRunTS
		serviceResumeDelay = originalDelay
	})
	serviceResumeDelay = 20 * time.Millisecond

	coreStops := make(chan struct{}, 4)
	coreStarts := make(chan struct{}, 4)
	tsStops := make(chan struct{}, 4)
	tsStarts := make(chan struct{}, 4)

	runServiceCore = func(ctx context.Context, _ *config.Config, _ string, ready func(int) error) error {
		coreStarts <- struct{}{}
		if err := ready(100); err != nil {
			return err
		}
		<-ctx.Done()
		coreStops <- struct{}{}
		return nil
	}
	runTailscaleSupervision = func(ctx context.Context, _ *config.Config) error {
		tsStarts <- struct{}{}
		<-ctx.Done()
		tsStops <- struct{}{}
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
	waitSignal(t, coreStarts, "initial core start")
	waitSignal(t, tsStarts, "initial tailscale start")

	requests <- svc.ChangeRequest{Cmd: svc.PowerEvent, EventType: powerEventResumeAutomatic}
	waitSignal(t, coreStops, "core stopped on resume")
	waitSignal(t, coreStarts, "core restarted on resume")
	waitSignal(t, tsStops, "tailscale stopped on resume")
	waitSignal(t, tsStarts, "tailscale restarted on resume")

	// Exactly one replacement pair; no restart storm.
	select {
	case <-coreStarts:
		t.Fatal("resume started more than one replacement core")
	case <-tsStarts:
		t.Fatal("resume started more than one replacement tailscale daemon")
	case <-time.After(80 * time.Millisecond):
	}

	data, err := os.ReadFile(filepath.Join(home, "logs", "service.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"power resume: restarting core", "power resume: restarting tailscale"} {
		if !strings.Contains(text, want) {
			t.Fatalf("service log missing %q:\n%s", want, text)
		}
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

// A tailscale stop failure on resume must not take the service down: the error
// is logged and the tailscale component is scheduled for a backoff retry.
func TestServiceResumeTailscaleStopFailureKeepsServiceAlive(t *testing.T) {
	home := t.TempDir()
	restoreWorkingDir(t)
	configPath := filepath.Join(home, config.DefaultConfigPath)
	profilePath := filepath.Join(home, "profiles", "windows.yaml")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"name":"test","setup":{"allowDirectOnly":true},"tailscale":{"enabled":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("mixed-port: 2080\n"), 0600); err != nil {
		t.Fatal(err)
	}

	originalRun := runServiceCore
	originalRunTS := runTailscaleSupervision
	originalResumeDelay := serviceResumeDelay
	originalRetryDelay := serviceCoreRetryDelay
	t.Cleanup(func() {
		runServiceCore = originalRun
		runTailscaleSupervision = originalRunTS
		serviceResumeDelay = originalResumeDelay
		serviceCoreRetryDelay = originalRetryDelay
	})
	serviceResumeDelay = time.Millisecond
	serviceCoreRetryDelay = time.Millisecond

	coreStarts := make(chan struct{}, 4)
	var (
		tsStopErr   error
		tsAttempts  int
		tsRestarted = make(chan struct{}, 2)
	)
	runServiceCore = func(ctx context.Context, _ *config.Config, _ string, ready func(int) error) error {
		coreStarts <- struct{}{}
		if err := ready(100); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	// The first tailscale run blocks until cancelled, then fails to stop cleanly.
	tsStopErr = errors.New("stale tailscaled refused to die")
	runTailscaleSupervision = func(ctx context.Context, _ *config.Config) error {
		tsAttempts++
		switch tsAttempts {
		case 1:
			<-ctx.Done()
			return tsStopErr
		default:
			tsRestarted <- struct{}{}
			<-ctx.Done()
			return nil
		}
	}

	requests := make(chan svc.ChangeRequest, 2)
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
	waitSignal(t, coreStarts, "initial core start")

	requests <- svc.ChangeRequest{Cmd: svc.PowerEvent, EventType: powerEventResumeAutomatic}
	waitSignal(t, tsRestarted, "tailscale retry after failed stop")

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

	data, err := os.ReadFile(filepath.Join(home, "logs", "service.log"))
	if err != nil || !strings.Contains(string(data), "restart tailscale after resume") {
		t.Fatalf("missing tailscale stop diagnostic: %s, %v", data, err)
	}
}
