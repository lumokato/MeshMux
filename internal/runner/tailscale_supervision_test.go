package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const tailscaleHelperEnv = "MESHMUX_TAILSCALE_SUPERVISION_HELPER"

// tailscaleHelperCommand starts this test binary as a process that blocks until
// it is killed, so supervision can be exercised without a real tailscaled.
func tailscaleHelperCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestTailscaleSupervisionHelperProcess$")
	cmd.Env = append(os.Environ(), tailscaleHelperEnv+"=1")
	hideWindow(cmd)
	return cmd
}

// TestTailscaleSupervisionHelperProcess is not a test: it is the body of the
// long-running process the supervision tests start and then expect to be killed.
func TestTailscaleSupervisionHelperProcess(t *testing.T) {
	if os.Getenv(tailscaleHelperEnv) != "1" {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}

// A cancelled supervisor must not return while the daemon is still running: the
// daemon's IPC endpoint is one name for the whole machine, and returning early
// is what reserved it and turned a restart into an outage.
func TestSuperviseDaemonStopsDaemonOnCancel(t *testing.T) {
	cmd := tailscaleHelperCommand(t)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	exited := make(chan struct{}, 1)
	oldWait := waitDaemonExit
	waitDaemonExit = func(c *exec.Cmd) error {
		err := c.Wait()
		exited <- struct{}{}
		return err
	}
	t.Cleanup(func() { waitDaemonExit = oldWait })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- superviseDaemon(ctx, cmd) }()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("superviseDaemon returned %v, want context.Canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("superviseDaemon did not return after cancellation")
	}
	select {
	case <-exited:
	default:
		t.Fatal("supervisor reported a stop while the daemon was still running")
	}
}

// An exit status is identical whatever went wrong, so the supervisor has to
// quote the daemon's own last word about the failure.
func TestSuperviseDaemonReportsDaemonLogOnExit(t *testing.T) {
	oldWait := waitDaemonExit
	waitDaemonExit = func(*exec.Cmd) error { return errors.New("exit status 1") }
	t.Cleanup(func() { waitDaemonExit = oldWait })

	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("logs", 0755); err != nil {
		t.Fatal(err)
	}
	daemonLog := "Program starting: v1.0\n" +
		"safesocket.Listen: namedpipe.Listen: open `\\\\.\\pipe\\MeshMux-Tailscale`: Access is denied.\n" +
		"logger closing down\n"
	if err := os.WriteFile("logs/tailscaled.out.log", []byte(daemonLog), 0644); err != nil {
		t.Fatal(err)
	}

	err = superviseDaemon(context.Background(), &exec.Cmd{})
	if err == nil {
		t.Fatal("expected an error for a failed daemon")
	}
	message := err.Error()
	if !strings.Contains(message, "exit status 1") {
		t.Fatalf("message %q lost the exit status", message)
	}
	if !strings.Contains(message, "Access is denied") {
		t.Fatalf("message %q does not quote the daemon log", message)
	}
	if !strings.Contains(message, "tailscaled.out.log") {
		t.Fatalf("message %q does not name the log it quoted", message)
	}
}

func TestTailscaledReasonLine(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "the last failure line wins over later noise",
			text: "Program starting\nAccess is denied.\nlogger closing down",
			want: "Access is denied.",
		},
		{
			name: "the last of several failures wins",
			text: "level=error first\nlevel=error second",
			want: "level=error second",
		},
		{
			name: "a log without a failure line falls back to its last line",
			text: "line one\n\nsecond line\n",
			want: "second line",
		},
		{name: "an empty log has no reason", text: " \n\n", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := tailscaledReasonLine(test.text); got != test.want {
				t.Fatalf("tailscaledReasonLine(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}

func TestTruncateLogLineKeepsRunesIntact(t *testing.T) {
	if got := truncateLogLine("short"); got != "short" {
		t.Fatalf("short line = %q", got)
	}
	long := strings.Repeat("a", 300)
	got := truncateLogLine(long)
	body := strings.TrimSuffix(got, "…")
	if got == body {
		t.Fatalf("long line was not truncated: %q", got)
	}
	if len(body) != 240 {
		t.Fatalf("truncated line keeps %d bytes, want 240", len(body))
	}
	multibyte := strings.Repeat("字", 200)
	got = truncateLogLine(multibyte)
	body = strings.TrimSuffix(got, "…")
	if got == body {
		t.Fatalf("multibyte line was not truncated: %q", got)
	}
	if len(body)%3 != 0 || strings.ContainsRune(body, '\uFFFD') {
		t.Fatalf("truncation split a rune: %q", body)
	}
}
