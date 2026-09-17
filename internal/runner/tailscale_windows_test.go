//go:build windows

package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

// stubProcessTable drives the reaper from a fixed process table instead of the
// real machine, so the test does not have to terminate anything.
func stubProcessTable(t *testing.T, fake *fakeProcessSystem, pids []uint32) {
	t.Helper()
	oldProcessOS, oldEnumerate := processOS, enumerateProcessIDs
	processOS = fake
	enumerateProcessIDs = func() []uint32 { return pids }
	t.Cleanup(func() {
		processOS = oldProcessOS
		enumerateProcessIDs = oldEnumerate
	})
}

// writeTailscaledBinary creates a component big enough to pass the same size
// check the real launcher applies before it starts anything.
func writeTailscaledBinary(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, 2<<20), 0644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// A leftover daemon holds the one IPC endpoint for the whole machine, so it has
// to go before the launch. Only processes running the same MeshMux-owned binary
// may be terminated: a system-wide Tailscale installation runs an executable
// with the same file name and must survive.
func TestReapStaleTailscaledKillsOnlyItsOwnBinary(t *testing.T) {
	dir := useTempWorkingDir(t)
	owned := config.DefaultTailscaledPath()
	ownedAbs := writeTailscaledBinary(t, owned)

	const (
		ownedPID  = 4101
		systemPID = 4102
		otherPID  = 4103
	)
	fake := newFakeProcessSystem()
	fake.addProcess(ownedPID, ownedAbs, nil)
	fake.addProcess(systemPID, filepath.Join(dir, "system", "tailscaled.exe"), nil)
	fake.addProcess(otherPID, filepath.Join(dir, "bin", "mihomo.exe"), nil)
	stubProcessTable(t, fake, []uint32{ownedPID, systemPID, otherPID, uint32(os.Getpid()), 4})

	cfg := &config.Config{}
	cfg.Components.Tailscale.Path = owned
	reapStaleTailscaled(cfg)

	killed := fake.killedPIDs()
	if len(killed) != 1 || killed[0] != ownedPID {
		t.Fatalf("killed %v, want only the leftover instance of the owned binary (PID %d)", killed, ownedPID)
	}
	if !fake.hasProcess(systemPID) {
		t.Fatal("a process running from another path was terminated")
	}
}

// An operator-supplied component path may be a system-wide installation that
// MeshMux never touches, so cleanup is limited to the MeshMux-owned default path.
func TestReapStaleTailscaledLeavesACustomComponentPathAlone(t *testing.T) {
	dir := useTempWorkingDir(t)
	custom := filepath.Join(dir, "system", "tailscaled.exe")
	customAbs := writeTailscaledBinary(t, custom)

	const customPID = 4201
	fake := newFakeProcessSystem()
	fake.addProcess(customPID, customAbs, nil)
	stubProcessTable(t, fake, []uint32{customPID})

	cfg := &config.Config{}
	cfg.Components.Tailscale.Path = custom
	reapStaleTailscaled(cfg)

	if killed := fake.killedPIDs(); len(killed) != 0 {
		t.Fatalf("killed %v, want a custom component path to be left alone", killed)
	}
}

// Windows does not terminate a child when its parent dies. Closing the job is
// what makes the daemon follow the supervisor into a crash or a hard kill.
func TestKillOnCloseJobStopsTheDaemonWithTheSupervisor(t *testing.T) {
	cmd := tailscaleHelperCommand(t)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	release := assignToKillOnCloseJob(cmd.Process.Pid)
	select {
	case <-exited:
		t.Fatal("the daemon exited while its job was still open")
	case <-time.After(500 * time.Millisecond):
	}

	release()
	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		t.Fatal("closing the job did not terminate the daemon")
	}
}
