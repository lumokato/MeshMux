//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxExplicitRuntimeHomePreservesSplitLayout(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	home := t.TempDir()
	t.Setenv("MESHMUX_HOME", home)
	path := filepath.Join(t.TempDir(), "meshmux.local.json")
	if err := enterRuntimeDir(path); err != nil {
		t.Fatal(err)
	}
	current, err := os.Getwd()
	if err != nil || current != home {
		t.Fatalf("runtime=%q error=%v", current, err)
	}
	t.Setenv("MESHMUX_HOME", "")
	if err := enterRuntimeDir(path); err != nil {
		t.Fatal(err)
	}
	current, err = os.Getwd()
	if err != nil || current != filepath.Dir(path) {
		t.Fatalf("runtime=%q error=%v", current, err)
	}
}
