package runner

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	cfg.Components.Mihomo.Path = config.DefaultMihomoPath()
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

func TestServiceReportsReadyBeforeNetworkPostStart(t *testing.T) {
	dir := useTempWorkingDir(t)
	fake := newFakeProcessSystem()
	restoreRunnerHooks(t, fake)
	cfg := testRunnerConfig(t, dir)
	executable, err := expectedMihomoPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mihomoLauncher = func(mihomo, profile string, stdout, stderr io.Writer) (launchedProcess, error) {
		wait := fake.addLaunchedProcess(551, executable, discoveryPorts(cfg))
		return launchedProcess{pid: 551, wait: wait}, nil
	}
	postStarted := make(chan struct{})
	postRelease := make(chan struct{})
	postStartNetworkRun = func(*config.Config) error {
		close(postStarted)
		<-postRelease
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan int, 1)
	done := make(chan error, 1)
	go func() {
		done <- ServiceContext(ctx, cfg, "profiles/windows.yaml", func(pid int) error {
			ready <- pid
			return nil
		})
	}()
	select {
	case pid := <-ready:
		if pid != 551 {
			t.Fatalf("ready PID = %d", pid)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not report ready")
	}
	select {
	case <-postStarted:
	case <-time.After(time.Second):
		t.Fatal("network post-start did not begin")
	}
	close(postRelease)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServiceContext: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not stop after cancellation")
	}
}
func TestSuperviseContextStopsManagedProcessOnCancellation(t *testing.T) {
	dir := useTempWorkingDir(t)
	fake := newFakeProcessSystem()
	restoreRunnerHooks(t, fake)
	cfg := testRunnerConfig(t, dir)
	executable, err := expectedMihomoPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mihomoLauncher = func(mihomo, profile string, stdout, stderr io.Writer) (launchedProcess, error) {
		wait := fake.addLaunchedProcess(601, executable, discoveryPorts(cfg))
		return launchedProcess{pid: 601, wait: wait}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- SuperviseContext(ctx, cfg, "profiles/linux.yaml", func(pid int) error {
			if pid != 601 {
				t.Errorf("ready PID = %d", pid)
			}
			ready <- struct{}{}
			return nil
		})
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not report ready")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SuperviseContext: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop after cancellation")
	}
	if got := fake.killedPIDs(); !reflect.DeepEqual(got, []int{601}) {
		t.Fatalf("killed PIDs = %v", got)
	}
	if _, err := os.Stat(pidFilePath); !os.IsNotExist(err) {
		t.Fatalf("PID file still exists: %v", err)
	}
}

func TestRunContextReportsUnexpectedProcessExit(t *testing.T) {
	dir := useTempWorkingDir(t)
	fake := newFakeProcessSystem()
	restoreRunnerHooks(t, fake)
	cfg := testRunnerConfig(t, dir)
	executable, err := expectedMihomoPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mihomoLauncher = func(mihomo, profile string, stdout, stderr io.Writer) (launchedProcess, error) {
		wait := fake.addLaunchedProcess(701, executable, discoveryPorts(cfg))
		return launchedProcess{pid: 701, wait: wait}, nil
	}
	done := make(chan error, 1)
	go func() { done <- RunContext(context.Background(), cfg, "profiles/linux.yaml") }()
	deadline := time.Now().Add(time.Second)
	for !fake.hasProcess(701) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := fake.kill(701); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "mihomo exited") {
			t.Fatalf("RunContext error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunContext did not report process exit")
	}
}

func TestPrepareMihomoSyncsBundledDefaultPath(t *testing.T) {
	dir := useTempWorkingDir(t)
	coreName := filepath.Base(config.DefaultMihomoPath())
	bundled := filepath.Join(dir, "bundled", coreName)
	if err := os.MkdirAll(filepath.Dir(bundled), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundled, []byte("new-core"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(bundled, minMihomoSize); err != nil {
		t.Fatal(err)
	}
	target := config.DefaultMihomoPath()
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old-core"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(target, minMihomoSize); err != nil {
		t.Fatal(err)
	}
	oldBundledMihomoPath := bundledMihomoPath
	bundledMihomoPath = func() string { return bundled }
	t.Cleanup(func() { bundledMihomoPath = oldBundledMihomoPath })

	path, err := prepareMihomo(&config.Config{Components: config.Components{Mihomo: config.Component{Path: target}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if path != target {
		t.Fatalf("path = %q", path)
	}
	equal, err := sameFileContents(bundled, target)
	if err != nil || !equal {
		t.Fatalf("bundled core was not synchronized: equal=%v err=%v", equal, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		t.Fatalf("bundled core mode = %v, want executable", info.Mode().Perm())
	}
}

func TestPrepareMihomoPreservesCustomPath(t *testing.T) {
	dir := useTempWorkingDir(t)
	coreName := filepath.Base(config.DefaultMihomoPath())
	bundled := filepath.Join(dir, "bundled", coreName)
	custom := filepath.Join(dir, "custom", coreName)
	for path, content := range map[string]string{bundled: "bundled-core", custom: "custom-core"} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, minMihomoSize); err != nil {
			t.Fatal(err)
		}
	}
	oldBundledMihomoPath := bundledMihomoPath
	bundledMihomoPath = func() string { return bundled }
	t.Cleanup(func() { bundledMihomoPath = oldBundledMihomoPath })

	before, err := fileSHA256(custom)
	if err != nil {
		t.Fatal(err)
	}
	path, err := prepareMihomo(&config.Config{Components: config.Components{Mihomo: config.Component{Path: custom}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	after, err := fileSHA256(custom)
	if err != nil {
		t.Fatal(err)
	}
	if path != custom || before != after {
		t.Fatalf("custom core changed: path=%q before=%x after=%x", path, before, after)
	}
}

func TestPrepareMihomoUpgradesManagedBundle(t *testing.T) {
	dir := useTempWorkingDir(t)
	bundled := filepath.Join(dir, "bundled", filepath.Base(config.DefaultMihomoPath()))
	if err := os.MkdirAll(filepath.Dir(bundled), 0755); err != nil {
		t.Fatal(err)
	}
	writeCore := func(path, marker string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(marker), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, minMihomoSize); err != nil {
			t.Fatal(err)
		}
	}
	writeCore(bundled, "bundle-one")
	oldBundledMihomoPath := bundledMihomoPath
	bundledMihomoPath = func() string { return bundled }
	t.Cleanup(func() { bundledMihomoPath = oldBundledMihomoPath })
	cfg := &config.Config{Components: config.Components{Mihomo: config.Component{Path: config.DefaultMihomoPath()}}}
	cfg.ApplyDefaults()
	if _, err := prepareMihomo(cfg, true); err != nil {
		t.Fatal(err)
	}
	writeCore(bundled, "bundle-two")
	if _, err := prepareMihomo(cfg, true); err != nil {
		t.Fatal(err)
	}
	equal, err := sameFileContents(bundled, config.DefaultMihomoPath())
	if err != nil || !equal {
		t.Fatalf("managed bundle was not upgraded: equal=%v err=%v", equal, err)
	}
}

func TestPrepareMihomoPreservesDownloadedDefaultCore(t *testing.T) {
	dir := useTempWorkingDir(t)
	bundled := filepath.Join(dir, "bundled", filepath.Base(config.DefaultMihomoPath()))
	target := config.DefaultMihomoPath()
	for path, marker := range map[string]string{bundled: "bundle-core", target: "downloaded-core"} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(marker), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, minMihomoSize); err != nil {
			t.Fatal(err)
		}
	}
	oldBundledMihomoPath := bundledMihomoPath
	bundledMihomoPath = func() string { return bundled }
	t.Cleanup(func() { bundledMihomoPath = oldBundledMihomoPath })
	cfg := &config.Config{Components: config.Components{Mihomo: config.Component{Path: target}}}
	cfg.ApplyDefaults()
	if err := MarkMihomoDownloaded(cfg); err != nil {
		t.Fatal(err)
	}
	before, err := fileSHA256(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareMihomo(cfg, true); err != nil {
		t.Fatal(err)
	}
	after, err := fileSHA256(target)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("downloaded default core was overwritten: before=%x after=%x", before, after)
	}
}

func restoreRunnerHooks(t *testing.T, fake *fakeProcessSystem) {
	t.Helper()
	oldProcessOS := processOS
	oldLauncher := mihomoLauncher
	oldBundledMihomoPath := bundledMihomoPath
	oldStartupDelay := startupProbeDelay
	oldPostStartNetwork := postStartNetworkRun
	oldStopTimeout := stopProcessTimeout
	oldStopPoll := stopPollInterval
	oldStopQuiet := stopQuietPeriod
	processOS = fake
	bundledMihomoPath = func() string { return "" }
	startupProbeDelay = time.Millisecond
	stopProcessTimeout = 100 * time.Millisecond
	stopPollInterval = time.Millisecond
	stopQuietPeriod = 5 * time.Millisecond
	t.Cleanup(func() {
		processOS = oldProcessOS
		mihomoLauncher = oldLauncher
		bundledMihomoPath = oldBundledMihomoPath
		startupProbeDelay = oldStartupDelay
		postStartNetworkRun = oldPostStartNetwork
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
	mihomo := filepath.Join(dir, config.DefaultMihomoPath())
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
	cfg.Components.Mihomo.Path = config.DefaultMihomoPath()
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

func (f *fakeProcessSystem) hasProcess(pid int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.paths[pid]
	return ok
}

func TestMissingCustomCoreDoesNotFallBackToBundle(t *testing.T) {
	dir := useTempWorkingDir(t)
	bundled := filepath.Join(dir, "bundled-core")
	if err := os.WriteFile(bundled, make([]byte, minMihomoSize), 0700); err != nil {
		t.Fatal(err)
	}
	previous := bundledMihomoPath
	bundledMihomoPath = func() string { return bundled }
	t.Cleanup(func() { bundledMihomoPath = previous })
	custom := filepath.Join(dir, "custom", "mihomo")
	cfg := &config.Config{Components: config.Components{Mihomo: config.Component{Path: custom}}}
	if _, err := prepareMihomo(cfg, true); err == nil {
		t.Fatal("missing custom core silently replaced")
	}
	if _, err := os.Stat(custom); !os.IsNotExist(err) {
		t.Fatalf("custom core was written: %v", err)
	}
}

func TestOldSupervisorCancellationPreservesReplacementCore(t *testing.T) {
	dir := useTempWorkingDir(t)
	fake := newFakeProcessSystem()
	restoreRunnerHooks(t, fake)
	cfg := testRunnerConfig(t, dir)
	executable, err := expectedMihomoPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = fake.addLaunchedProcess(992, executable, discoveryPorts(cfg))
	if err := writePID(992); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	done <- nil
	if err := stopAfterCancellation(cfg, 991, done, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if !fake.hasProcess(992) {
		t.Fatal("old supervisor killed replacement")
	}
	if len(fake.killedPIDs()) != 0 {
		t.Fatal("unexpected process termination")
	}
}
