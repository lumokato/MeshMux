//go:build windows

package winservice

import (
	"golang.org/x/sys/windows/svc"
	"strings"
	"testing"
	"time"
)

func TestWaitServiceStateReportsStoppedExitCodesImmediately(t *testing.T) {
	calls := 0
	err := waitServiceState(func() (svc.Status, error) {
		calls++
		return svc.Status{State: svc.Stopped, Win32ExitCode: 1066, ServiceSpecificExitCode: 1}, nil
	}, svc.Running, time.Second, time.Millisecond)
	if calls != 1 || err == nil || !strings.Contains(err.Error(), "win32-exit=1066 service-exit=1") {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestWaitServiceStateAcceptsPendingThenRunning(t *testing.T) {
	calls := 0
	err := waitServiceState(func() (svc.Status, error) {
		calls++
		if calls == 1 {
			return svc.Status{State: svc.StartPending}, nil
		}
		return svc.Status{State: svc.Running}, nil
	}, svc.Running, time.Second, time.Millisecond)
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
