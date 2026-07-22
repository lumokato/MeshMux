package runner

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

func TestLifecycleRecoversPIDAfterControllerRestart(t *testing.T) {
	dir := useTempWorkingDir(t)
	fake := newFakeProcessSystem()
	restoreRunnerHooks(t, fake)

	cfg := testRunnerConfig(t, dir)
	executable, err := expectedMihomoPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	launchPIDs := []int{101, 301}
	mihomoLauncher = func(mihomo, profile string, stdout, stderr io.Writer) (launchedProcess, error) {
		if len(launchPIDs) == 0 {
			return launchedProcess{}, errors.New("unexpected launch")
		}
		pid := launchPIDs[0]
		launchPIDs = launchPIDs[1:]
		wait := fake.addLaunchedProcess(pid, executable, discoveryPorts(cfg))
		return launchedProcess{pid: pid, wait: wait}, nil
	}

	profile := filepath.Join("profiles", "windows.yaml")
	if err := Start(cfg, profile); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if pid, ok := PID(cfg); !ok || pid != 101 {
		t.Fatalf("PID after start = %d, %v", pid, ok)
	}

	fake.replaceProcess(101, 202, discoveryPorts(cfg))
	if pid, ok := PID(cfg); !ok || pid != 202 {
		t.Fatalf("PID after controller restart = %d, %v", pid, ok)
	}
	if stored, err := readPID(); err != nil || stored != 202 {
		t.Fatalf("stored PID = %d, %v", stored, err)
	}
	if !IsRunning(cfg) {
		t.Fatal("IsRunning returned false after PID change")
	}
	status := Status(cfg)
	if !strings.Contains(status, "mihomo PID: 202") || strings.Contains(status, "mihomo PID: 101") {
		t.Fatalf("unexpected status:\n%s", status)
	}

	fake.restartOnKill(202, 203, discoveryPorts(cfg))
	if err := Stop(cfg); err != nil {
		t.Fatalf("Stop after PID change: %v", err)
	}
	if got := fake.killedPIDs(); !reflect.DeepEqual(got, []int{202, 203}) {
		t.Fatalf("killed PIDs = %v", got)
	}
	if _, err := os.Stat(pidFilePath); !os.IsNotExist(err) {
		t.Fatalf("PID file still exists: %v", err)
	}
	if live := fake.livePIDs(); len(live) != 0 {
		t.Fatalf("orphan processes after stop: %v", live)
	}

	if err := Restart(cfg, profile); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if pid, ok := PID(cfg); !ok || pid != 301 {
		t.Fatalf("PID after restart = %d, %v", pid, ok)
	}
	if live := fake.livePIDs(); !reflect.DeepEqual(live, []int{301}) {
		t.Fatalf("live processes after restart = %v", live)
	}
	if err := Stop(cfg); err != nil {
		t.Fatalf("final Stop: %v", err)
	}
}

func TestStartRejectsUnmanagedPortConflict(t *testing.T) {
	useTempWorkingDir(t)
	fake := newFakeProcessSystem()
	restoreRunnerHooks(t, fake)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	cfg := &config.Config{}
	cfg.Ports.Controller = "127.0.0.1:" + strconv.Itoa(port)
	cfg.Components.Mihomo.Path = filepath.Join("bin", "mihomo.exe")
	fake.addProcess(900, filepath.Join(t.TempDir(), "other.exe"), []int{port})

	launched := false
	mihomoLauncher = func(mihomo, profile string, stdout, stderr io.Writer) (launchedProcess, error) {
		launched = true
		return launchedProcess{}, nil
	}
	if err := Start(cfg, "profiles/windows.yaml"); err == nil || !strings.Contains(err.Error(), "端口仍被占用") {
		t.Fatalf("Start conflict error = %v", err)
	}
	if launched {
		t.Fatal("mihomo launched despite unmanaged port conflict")
	}
	if got := fake.killedPIDs(); len(got) != 0 {
		t.Fatalf("unmanaged process was killed: %v", got)
	}
}

func TestStopCatchesDelayedReplacementProcess(t *testing.T) {
	dir := useTempWorkingDir(t)
	fake := newFakeProcessSystem()
	restoreRunnerHooks(t, fake)
	stopProcessTimeout = 75 * time.Millisecond
	stopQuietPeriod = 15 * time.Millisecond

	cfg := testRunnerConfig(t, dir)
	executable, err := expectedMihomoPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePID(401); err != nil {
		t.Fatal(err)
	}
	spawned := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Millisecond)
		fake.addProcess(402, executable, discoveryPorts(cfg))
		close(spawned)
	}()

	if err := Stop(cfg); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-spawned
	if got := fake.killedPIDs(); !reflect.DeepEqual(got, []int{402}) {
		t.Fatalf("delayed replacement was not stopped: %v", got)
	}
	if live := fake.livePIDs(); len(live) != 0 {
		t.Fatalf("orphan processes after delayed replacement: %v", live)
	}
}

func TestSuperviseStaysAliveUntilManagedProcessStops(t *testing.T) {
	dir := useTempWorkingDir(t)
	fake := newFakeProcessSystem()
	restoreRunnerHooks(t, fake)
	cfg := testRunnerConfig(t, dir)
	executable, err := expectedMihomoPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mihomoLauncher = func(mihomo, profile string, stdout, stderr io.Writer) (launchedProcess, error) {
		wait := fake.addLaunchedProcess(501, executable, discoveryPorts(cfg))
		return launchedProcess{pid: 501, wait: wait}, nil
	}
	ready := make(chan int, 1)
	done := make(chan error, 1)
	go func() {
		done <- Supervise(cfg, "profiles/windows.yaml", func(pid int) error {
			ready <- pid
			return nil
		})
	}()
	select {
	case pid := <-ready:
		if pid != 501 {
			t.Fatalf("ready PID = %d", pid)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not report ready")
	}
	select {
	case err := <-done:
		t.Fatalf("supervisor exited early: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	if err := fake.kill(501); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("supervisor exit: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not exit after managed process stopped")
	}
}

func restoreRunnerHooks(t *testing.T, fake *fakeProcessSystem) {
	t.Helper()
	oldProcessOS := processOS
	oldLauncher := mihomoLauncher
	oldStartupDelay := startupProbeDelay
	oldStopTimeout := stopProcessTimeout
	oldStopPoll := stopPollInterval
	oldStopQuiet := stopQuietPeriod
	processOS = fake
	startupProbeDelay = time.Millisecond
	stopProcessTimeout = 25 * time.Millisecond
	stopPollInterval = time.Millisecond
	stopQuietPeriod = 3 * time.Millisecond
	t.Cleanup(func() {
		processOS = oldProcessOS
		mihomoLauncher = oldLauncher
		startupProbeDelay = oldStartupDelay
		stopProcessTimeout = oldStopTimeout
		stopPollInterval = oldStopPoll
		stopQuietPeriod = oldStopQuiet
	})
}

func useTempWorkingDir(t *testing.T) string {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

func testRunnerConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	mihomo := filepath.Join(dir, "bin", "mihomo.exe")
	if err := os.WriteFile(mihomo, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(mihomo, minMihomoSize); err != nil {
		t.Fatal(err)
	}
	geoIP := filepath.Join(dir, "geoip.metadb")
	if err := os.WriteFile(geoIP, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(geoIP, minGeoIPSize); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Components.Mihomo.Path = filepath.Join("bin", "mihomo.exe")
	cfg.Ports.Controller = "127.0.0.1:" + strconv.Itoa(freeTCPPort(t))
	cfg.Ports.Mixed = freeTCPPort(t)
	return cfg
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

type fakeProcessSystem struct {
	mu           sync.Mutex
	paths        map[int]string
	ports        map[int]map[int]bool
	waits        map[int]chan error
	restarts     map[int]int
	restartPorts map[int][]int
	killed       []int
}

func newFakeProcessSystem() *fakeProcessSystem {
	return &fakeProcessSystem{
		paths:        map[int]string{},
		ports:        map[int]map[int]bool{},
		waits:        map[int]chan error{},
		restarts:     map[int]int{},
		restartPorts: map[int][]int{},
	}
}

func (f *fakeProcessSystem) executablePath(pid int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path, ok := f.paths[pid]
	if !ok {
		return "", os.ErrProcessDone
	}
	return path, nil
}

func (f *fakeProcessSystem) listeningProcesses(ports []int) ([]portOwner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wanted := map[int]bool{}
	for _, port := range ports {
		wanted[port] = true
	}
	var owners []portOwner
	for port, pids := range f.ports {
		if !wanted[port] {
			continue
		}
		for pid := range pids {
			owners = append(owners, portOwner{Port: port, PID: pid})
		}
	}
	return owners, nil
}

func (f *fakeProcessSystem) kill(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	path, ok := f.paths[pid]
	if !ok {
		return os.ErrProcessDone
	}
	f.killed = append(f.killed, pid)
	f.removeProcessLocked(pid)
	if next := f.restarts[pid]; next > 0 {
		delete(f.restarts, pid)
		ports := f.restartPorts[pid]
		delete(f.restartPorts, pid)
		f.addProcessLocked(next, path, ports)
	}
	return nil
}

func (f *fakeProcessSystem) addProcess(pid int, path string, ports []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addProcessLocked(pid, path, ports)
}

func (f *fakeProcessSystem) addLaunchedProcess(pid int, path string, ports []int) func() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addProcessLocked(pid, path, ports)
	wait := make(chan error, 1)
	f.waits[pid] = wait
	return func() error { return <-wait }
}

func (f *fakeProcessSystem) replaceProcess(oldPID, newPID int, ports []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := f.paths[oldPID]
	f.removeProcessLocked(oldPID)
	f.addProcessLocked(newPID, path, ports)
}

func (f *fakeProcessSystem) restartOnKill(pid, nextPID int, ports []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts[pid] = nextPID
	f.restartPorts[pid] = append([]int(nil), ports...)
}

func (f *fakeProcessSystem) addProcessLocked(pid int, path string, ports []int) {
	f.paths[pid] = path
	for _, port := range ports {
		if f.ports[port] == nil {
			f.ports[port] = map[int]bool{}
		}
		f.ports[port][pid] = true
	}
}

func (f *fakeProcessSystem) removeProcessLocked(pid int) {
	delete(f.paths, pid)
	for port, pids := range f.ports {
		delete(pids, pid)
		if len(pids) == 0 {
			delete(f.ports, port)
		}
	}
	if wait := f.waits[pid]; wait != nil {
		wait <- nil
		close(wait)
		delete(f.waits, pid)
	}
}

func (f *fakeProcessSystem) killedPIDs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.killed...)
}

func (f *fakeProcessSystem) livePIDs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var pids []int
	for pid := range f.paths {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}
