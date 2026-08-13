package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeCommandRunner struct {
	commands []recordedCommand
	output   []byte
	err      error
}

func (f *fakeCommandRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	f.commands = append(f.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	return f.output, f.err
}

func TestSystemdQueriesUseDirectSystemctl(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		output string
		runErr error
		want   bool
	}{
		{name: "active", query: "is-active", output: "active\n", want: true},
		{name: "inactive exit status", query: "is-active", output: "inactive\n", runErr: errors.New("exit status 3")},
		{name: "enabled", query: "is-enabled", output: "enabled\n", want: true},
		{name: "disabled exit status", query: "is-enabled", output: "disabled\n", runErr: errors.New("exit status 1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeCommandRunner{output: []byte(tt.output), err: tt.runErr}
			controller := newSystemdController(runner)
			var got bool
			var err error
			if tt.query == "is-active" {
				got, err = controller.IsActive()
			} else {
				got, err = controller.IsEnabled()
			}
			if err != nil {
				t.Fatalf("query returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("query result = %v, want %v", got, tt.want)
			}
			wantCommand := recordedCommand{name: systemctlPath, args: []string{tt.query, meshMuxUnit}}
			if !reflect.DeepEqual(runner.commands, []recordedCommand{wantCommand}) {
				t.Fatalf("commands = %#v, want %#v", runner.commands, []recordedCommand{wantCommand})
			}
		})
	}
}

func TestSystemdActionsUseFixedNonInteractiveSudo(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			runner := &fakeCommandRunner{}
			controller := newSystemdController(runner)
			if err := controller.Action(action); err != nil {
				t.Fatalf("Action(%q) returned error: %v", action, err)
			}
			want := []recordedCommand{{
				name: sudoPath,
				args: []string{"-n", systemctlPath, action, meshMuxUnit},
			}}
			if !reflect.DeepEqual(runner.commands, want) {
				t.Fatalf("commands = %#v, want %#v", runner.commands, want)
			}
		})
	}
}

func TestSystemdActionRejectsUnknownAction(t *testing.T) {
	runner := &fakeCommandRunner{}
	controller := newSystemdController(runner)
	if err := controller.Action("daemon-reload"); err == nil {
		t.Fatal("Action accepted an unsupported command")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsupported action executed commands: %#v", runner.commands)
	}
}

func TestSystemdQueryRejectsUnexpectedOutput(t *testing.T) {
	runner := &fakeCommandRunner{output: []byte("permission denied\n"), err: errors.New("exit status 1")}
	controller := newSystemdController(runner)
	if _, err := controller.IsActive(); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("IsActive error = %v, want command output", err)
	}
}

func TestSystemdCommandTimeoutDefault(t *testing.T) {
	controller := systemdController{}
	if controller.commandTimeout() != 10*time.Second {
		t.Fatalf("default timeout = %v", controller.commandTimeout())
	}
}

func TestResolveLinuxDataDir(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		home string
		want string
	}{
		{name: "explicit MeshMux home", env: map[string]string{"MESHMUX_HOME": "/srv/meshmux"}, home: "/home/codex", want: filepath.Clean("/srv/meshmux")},
		{name: "XDG data home", env: map[string]string{"XDG_DATA_HOME": "/home/codex/data"}, home: "/home/codex", want: filepath.Join("/home/codex/data", "meshmux")},
		{name: "standard user data", home: "/home/codex", want: filepath.Join("/home/codex", ".local", "share", "meshmux")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveLinuxDataDir(func(key string) string { return tt.env[key] }, func() (string, error) { return tt.home, nil })
			if err != nil {
				t.Fatalf("resolveLinuxDataDir returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("data dir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveLinuxConfigPath(t *testing.T) {
	got, err := resolveLinuxConfigPath(func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return "/home/codex/config"
		}
		return ""
	}, func() (string, error) { return "/home/codex", nil })
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/home/codex/config", "meshmux", "meshmux.local.json")
	if got != want {
		t.Fatalf("config path = %q, want %q", got, want)
	}
}

func TestOpenLocalURLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-url")
	want := "http://127.0.0.1:9088/?token=test-token"
	if err := os.WriteFile(path, []byte(want+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var opened string
	if err := openLocalURLFile(path, func(rawURL string) error {
		opened = rawURL
		return nil
	}); err != nil {
		t.Fatalf("openLocalURLFile returned error: %v", err)
	}
	if opened != want {
		t.Fatalf("opened URL = %q, want %q", opened, want)
	}
}

func TestOpenLocalURLFileRejectsRemoteHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-url")
	if err := os.WriteFile(path, []byte("https://example.com/?token=secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := openLocalURLFile(path, func(string) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("openLocalURLFile accepted a remote URL")
	}
	if called {
		t.Fatal("URL opener was called for a remote URL")
	}
}
