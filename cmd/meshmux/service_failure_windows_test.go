//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRollbackDoesNotWriteSnapshotWhileFailedServiceStillRuns(t *testing.T) {
	old := controlWindowsService
	t.Cleanup(func() { controlWindowsService = old })
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("new-active"), 0600); err != nil {
		t.Fatal(err)
	}
	controlWindowsService = func(action string, _ time.Duration) error {
		if action != "stop" {
			t.Fatal(action)
		}
		return errors.New("still running")
	}
	err := restoreStoppedService([]snapshotFileBackup{{path: path, data: []byte("old"), exists: true}}, errors.New("start failed"))
	if err == nil || !strings.Contains(err.Error(), "cannot stop failed service") {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new-active" {
		t.Fatal("snapshot changed under live writer")
	}
}
