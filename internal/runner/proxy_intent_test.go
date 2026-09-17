package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// intentHome isolates LocalDataDir for the duration of a test. On Windows the
// data dir comes from %LOCALAPPDATA% (not the working directory), so chdir
// alone is not enough; MESHMUX_HOME covers both platforms.
func intentHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "meshmux-intent-home")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MESHMUX_HOME", dir)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestProxyIntentDefaultsToOffWhenMissing(t *testing.T) {
	intentHome(t)
	if ProxyWanted() {
		t.Fatal("missing intent file must count as proxy-off")
	}
}

func TestProxyIntentRoundTrip(t *testing.T) {
	intentHome(t)
	if err := SetProxyIntent(true); err != nil {
		t.Fatalf("SetProxyIntent(true): %v", err)
	}
	if !ProxyWanted() {
		t.Fatal("ProxyWanted = false after recording on")
	}
	if err := SetProxyIntent(false); err != nil {
		t.Fatalf("SetProxyIntent(false): %v", err)
	}
	if ProxyWanted() {
		t.Fatal("ProxyWanted = true after recording off")
	}
}

func TestProxyIntentCorruptFileCountsAsOff(t *testing.T) {
	intentHome(t)
	if err := SetProxyIntent(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProxyIntentPath(), []byte("enabled"), 0600); err != nil {
		t.Fatal(err)
	}
	if ProxyWanted() {
		t.Fatal("corrupt intent file must count as proxy-off")
	}
}

func TestProxyIntentPathLivesInDataDir(t *testing.T) {
	dir := intentHome(t)
	want := filepath.Join(dir, "proxy-intent")
	if got := ProxyIntentPath(); got != want {
		t.Fatalf("ProxyIntentPath = %q, want %q", got, want)
	}
	if err := SetProxyIntent(true); err != nil {
		t.Fatalf("SetProxyIntent: %v", err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("intent file not created under MESHMUX_HOME: %v", err)
	}
}

// Regression: the first version of these tests only chdir'd into a temp dir
// and ended up rewriting the real user intent file on Windows (LOCALAPPDATA
// does not follow the working directory). Intent tests must never touch the
// machine's actual %LOCALAPPDATA%\MeshMux\proxy-intent.
func TestProxyIntentTestsNeverTouchRealDataDir(t *testing.T) {
	dir := intentHome(t)
	if err := SetProxyIntent(true); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(os.Getenv("LOCALAPPDATA"), "MeshMux", "proxy-intent")
	if real == filepath.Join(dir, "proxy-intent") {
		t.Skip("no LOCALAPPDATA isolation available")
	}
	if _, err := os.Stat(real); err == nil {
		data, err := os.ReadFile(real)
		if err == nil && string(data) != "on" && string(data) != "off" {
			t.Fatalf("real intent file contains non-canonical value %q; tests are leaking", data)
		}
	}
}
