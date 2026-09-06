package fileutil

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLockAcrossProcesses(t *testing.T) {
	path := os.Getenv("MESHMUX_LOCK_TEST")
	if path != "" {
		unlock, err := TryLock(path)
		if !errors.Is(err, ErrBusy) {
			if unlock != nil {
				unlock()
			}
			os.Exit(2)
		}
		return
	}
	path = filepath.Join(t.TempDir(), "operation.lock")
	unlock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	command := exec.Command(os.Args[0], "-test.run=^TestLockAcrossProcesses$")
	command.Env = append(os.Environ(), "MESHMUX_LOCK_TEST="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, output)
	}
	unlock()
	released, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	released()
}
