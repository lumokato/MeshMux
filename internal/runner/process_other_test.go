//go:build linux

package runner

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNativeProcessSystemFindsListeningProcess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	owners, err := (nativeProcessSystem{}).listeningProcesses([]int{port})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range owners {
		if owner.Port == port && owner.PID == os.Getpid() {
			return
		}
	}
	t.Fatalf("listener %d owned by PID %d not found in %+v", port, os.Getpid(), owners)
}

func TestControllerPortIPv6(t *testing.T) {
	if got := controllerPort("[::1]:9088"); got != 9088 {
		t.Fatalf("controllerPort = %d, want %s", got, strconv.Itoa(9088))
	}
}

func TestNativeProcessSystemExecutablePath(t *testing.T) {
	got, err := (nativeProcessSystem{}).executablePath(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !sameExecutablePath(filepath.Clean(got), filepath.Clean(want)) {
		t.Fatalf("executable path = %q, want %q", got, want)
	}
}

func TestStatusHasEffectiveCapability(t *testing.T) {
	for _, testCase := range []struct {
		status string
		bit    uint
		want   bool
	}{
		{status: "Name:\ttest\nCapEff:\t0000000000001000\n", bit: 12, want: true},
		{status: "Name:\ttest\nCapEff:\t0000000000000000\n", bit: 12, want: false},
		{status: "Name:\ttest\nCapEff:\tnot-hex\n", bit: 12, want: false},
	} {
		if got := statusHasEffectiveCapability(testCase.status, testCase.bit); got != testCase.want {
			t.Fatalf("statusHasEffectiveCapability(%q, %d) = %v, want %v", testCase.status, testCase.bit, got, testCase.want)
		}
	}
}
