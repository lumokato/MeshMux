//go:build windows

package main

import "testing"

func TestProxyWatchdogRequiresRepeatedFailureAndDoesNotEnableProxy(t *testing.T) {
	oldReady, oldDisable := localProxyReady, disableOwnedProxy
	t.Cleanup(func() { localProxyReady, disableOwnedProxy = oldReady, oldDisable })
	healthy := false
	calls := 0
	localProxyReady = func(int) bool { return healthy }
	disableOwnedProxy = func(port int) error {
		if port != 2080 {
			t.Fatal(port)
		}
		calls++
		return nil
	}
	b := &windowsBackend{}
	if err := b.reconcileProxy(2080); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("single transient failure cleared proxy")
	}
	healthy = true
	_ = b.reconcileProxy(2080)
	healthy = false
	_ = b.reconcileProxy(2080)
	if calls != 0 {
		t.Fatal("recovered failure was not reset")
	}
	_ = b.reconcileProxy(2080)
	if calls != 1 {
		t.Fatal("dead proxy was not released")
	}
}
