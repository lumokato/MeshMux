//go:build linux

package main

import (
	"reflect"
	"testing"
)

func TestGSettingsProxyEnabled(t *testing.T) {
	runner := &fakeCommandRunner{output: []byte("'manual'\n")}
	controller := newGSettingsController(runner)
	enabled, err := controller.Enabled()
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("manual desktop proxy was reported disabled")
	}
}

func TestGSettingsEnablesConfiguredPortBeforeManualMode(t *testing.T) {
	runner := &fakeCommandRunner{}
	controller := newGSettingsController(runner)
	if err := controller.SetEnabled(true, 32080); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 8 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	wantPort := recordedCommand{name: gsettingsPath, args: []string{"set", "org.gnome.system.proxy.http", "port", "32080"}}
	if !reflect.DeepEqual(runner.commands[1], wantPort) {
		t.Fatalf("HTTP port command = %#v, want %#v", runner.commands[1], wantPort)
	}
	wantLast := recordedCommand{name: gsettingsPath, args: []string{"set", "org.gnome.system.proxy", "mode", "manual"}}
	if !reflect.DeepEqual(runner.commands[len(runner.commands)-1], wantLast) {
		t.Fatalf("last command = %#v, want %#v", runner.commands[len(runner.commands)-1], wantLast)
	}
}

func TestGSettingsDisablesOnlyProxyMode(t *testing.T) {
	runner := &fakeCommandRunner{}
	controller := newGSettingsController(runner)
	if err := controller.SetEnabled(false, 0); err != nil {
		t.Fatal(err)
	}
	want := []recordedCommand{{name: gsettingsPath, args: []string{"set", "org.gnome.system.proxy", "mode", "none"}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}
