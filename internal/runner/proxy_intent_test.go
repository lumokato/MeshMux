package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProxyIntentDefaultsToOffWhenMissing(t *testing.T) {
	dir := useTempWorkingDir(t)
	_ = dir
	if ProxyWanted() {
		t.Fatal("missing intent file must count as proxy-off")
	}
}

func TestProxyIntentRoundTrip(t *testing.T) {
	useTempWorkingDir(t)
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
	useTempWorkingDir(t)
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
	useTempWorkingDir(t)
	dir, err := os.MkdirTemp("", "meshmux-home")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MESHMUX_HOME", dir)
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
