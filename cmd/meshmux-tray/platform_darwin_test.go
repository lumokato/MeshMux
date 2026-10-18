//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/fileutil"
)

func TestDarwinTrayLockUsesRuntimeRoot(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", home)
	// A real config must survive startup, including subscriptions and identity.
	path := filepath.Join(home, config.DefaultConfigPath)
	data := []byte(`{"name":"keep-existing-config"}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := newPlatformBackend()
	if err != nil {
		t.Fatal(err)
	}
	unlock := first.(*darwinBackend).unlock
	defer unlock()
	if _, err := newPlatformBackend(); !platformStartupFatal(err) {
		t.Fatalf("second tray must refuse to own the same runtime, got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("existing config changed: %q, %v", got, err)
	}
	unlock()
	second, err := newPlatformBackend()
	if err != nil {
		t.Fatalf("tray lock not released: %v", err)
	}
	second.(*darwinBackend).unlock()
}

func TestDarwinOnlyBusyStartupIsFatal(t *testing.T) {
	if !platformStartupFatal(fileutil.ErrBusy) {
		t.Fatal("duplicate tray must exit before creating the UI")
	}
	if platformStartupFatal(os.ErrNotExist) {
		t.Fatal("configuration failure must leave the management UI available")
	}
}
