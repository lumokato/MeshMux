//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/fileutil"
	"github.com/meshmux/meshmux/internal/generator"
	"github.com/meshmux/meshmux/internal/runner"
	"github.com/meshmux/meshmux/internal/winservice"
	"golang.org/x/sys/windows/svc"
)

func runWindowsService(args []string) error {
	configPath := configPathArg(args)
	if strings.TrimSpace(configPath) == "" {
		return errors.New("service config path is required")
	}
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	home := filepath.Dir(configPath)
	if err := os.Setenv("MESHMUX_HOME", home); err != nil {
		return err
	}
	if err := os.Chdir(home); err != nil {
		return err
	}
	return svc.Run(winservice.Name, &serviceHandler{configPath: configPath})
}

func serviceCorePath() string {
	return filepath.Join(winservice.DataDir(), config.DefaultMihomoPath())
}

type serviceHandler struct {
	configPath string
}

const (
	powerEventResumeSuspend   = 7
	powerEventResumeAutomatic = 18
)

var serviceResumeDelay = 5 * time.Second
var serviceCoreRetryDelay = 5 * time.Second
var serviceRetryMaxDelay = 5 * time.Minute

// serviceRetryBackoff returns the delay before the nth consecutive retry of one
// component. A fixed interval turned a single missing binary into a permanent
// five-second restart storm, so the delay doubles up to serviceRetryMaxDelay.
func serviceRetryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return serviceCoreRetryDelay
	}
	delay := serviceCoreRetryDelay
	for index := 1; index < attempt; index++ {
		if delay >= serviceRetryMaxDelay {
			break
		}
		delay *= 2
	}
	if delay > serviceRetryMaxDelay {
		delay = serviceRetryMaxDelay
	}
	return delay
}

// serviceRetryMinUptime is how long a component must stay up before its retry
// schedule counts as healthy again. Starting a component only spawns a
// goroutine, so "start returned no error" says nothing about the process
// surviving; without this, a daemon that exits immediately would reset the
// backoff on every attempt and retry forever on the base delay.
const serviceRetryMinUptime = 30 * time.Second

// serviceRetry tracks one component's retry schedule. Components back off
// independently so a failure in one never restarts the other.
type serviceRetry struct {
	attempt int
	timer   *time.Timer
	ready   <-chan time.Time
}

func newServiceRetry() *serviceRetry {
	return &serviceRetry{}
}

func (r *serviceRetry) schedule() {
	r.stop()
	r.attempt++
	r.timer = time.NewTimer(serviceRetryBackoff(r.attempt))
	r.ready = r.timer.C
}

// observe clears the accumulated backoff only after a component has actually
// stayed up. A short-lived run keeps the current attempt so the delay keeps
// growing.
func (r *serviceRetry) observe(uptime time.Duration) {
	if uptime >= serviceRetryMinUptime {
		r.reset()
	}
}

func (r *serviceRetry) reset() {
	r.stop()
	r.attempt = 0
}

func (r *serviceRetry) stop() {
	if r.timer != nil {
		if !r.timer.Stop() {
			select {
			case <-r.timer.C:
			default:
			}
		}
	}
	r.ready = nil
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	var core *serviceCore
	var coreDone <-chan struct{}
	startCore := func() error {
		cfg, _, err := load(configArgs(h.configPath))
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		// A system service owns one protected core copy. It is updated explicitly by
		// the component updater and is independent from the install directory.
		cfg.Components.Mihomo.Path = serviceCorePath()
		profile := filepath.Join(filepath.Dir(h.configPath), "profiles", "windows.yaml")
		if _, err := os.Stat(profile); err != nil {
			return fmt.Errorf("find generated profile: %w", err)
		}
		replacement := startServiceCore(cfg, profile)
		core = replacement
		coreDone = replacement.done
		return nil
	}
	stopCore := func() error {
		if core == nil {
			return nil
		}
		err := stopServiceCore(core, 8*time.Second)
		if err != nil {
			return err
		}
		core = nil
		coreDone = nil
		return err
	}
	var tsCore *serviceCore
	var tsDone <-chan struct{}
	startTailscale := func() error {
		cfg, _, err := load(configArgs(h.configPath))
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if !cfg.Tailscale.Enabled {
			return nil
		}
		replacement := startTailscaleService(cfg)
		tsCore = replacement
		tsDone = replacement.done
		return nil
	}
	stopTailscale := func() error {
		if tsCore == nil {
			return nil
		}
		// The supervisor kills the daemon and waits for the process to disappear
		// before it reports a stop, so this budget has to cover that wait. A
		// shorter one reported "timed out" while the stop was still in flight and
		// turned a working stop into a service failure.
		err := stopServiceCore(tsCore, runner.TailscaledStopBudget()+5*time.Second)
		if err != nil {
			return err
		}
		tsCore = nil
		tsDone = nil
		return err
	}
	defer func() {
		if core != nil {
			core.cancel()
		}
		if tsCore != nil {
			tsCore.cancel()
		}
	}()

	var resumeTimer *time.Timer
	var resume <-chan time.Time
	stopResumeTimer := func() {
		if resumeTimer != nil {
			if !resumeTimer.Stop() {
				select {
				case <-resumeTimer.C:
				default:
				}
			}
		}
		resume = nil
	}
	defer stopResumeTimer()

	// The core and the tailscale daemon back off independently. Sharing one
	// schedule let a missing tailscaled restart the mihomo core every few
	// seconds, which is what left several cores fighting over the same ports.
	coreRetry := newServiceRetry()
	tailscaleRetry := newServiceRetry()
	defer coreRetry.stop()
	defer tailscaleRetry.stop()

	// A successful return only means the component was spawned; the retry
	// schedule is cleared later, once the process has actually stayed up.
	startCoreTracked := func() {
		if err := startCore(); err != nil {
			appendServiceLog(h.configPath, err.Error())
			coreRetry.schedule()
		}
	}
	startTailscaleTracked := func() {
		if err := startTailscale(); err != nil {
			appendServiceLog(h.configPath, "tailscale: "+err.Error())
			tailscaleRetry.schedule()
		}
	}
	startCoreTracked()
	startTailscaleTracked()

	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.PowerEvent:
				if !isResumePowerEvent(request.EventType) {
					continue
				}
				stopResumeTimer()
				resumeTimer = time.NewTimer(serviceResumeDelay)
				resume = resumeTimer.C
			case svc.Stop, svc.Shutdown:
				stopResumeTimer()
				coreRetry.stop()
				tailscaleRetry.stop()
				changes <- svc.Status{State: svc.StopPending}
				stopErr := stopCore()
				tsStopErr := stopTailscale()
				if stopErr != nil {
					return serviceFailure(h.configPath, "stop core", stopErr)
				}
				if tsStopErr != nil {
					return serviceFailure(h.configPath, "stop tailscale", tsStopErr)
				}
				return false, 0
			}
		case <-resume:
			resume = nil
			coreRetry.stop()
			tailscaleRetry.stop()
			appendServiceLog(h.configPath, "power resume: restarting core")
			if err := stopCore(); err != nil {
				appendServiceLog(h.configPath, "restart core after resume: "+err.Error())
				continue
			}
			if err := startCore(); err != nil {
				appendServiceLog(h.configPath, "restart core after resume: "+err.Error())
				coreRetry.schedule()
				continue
			}
			// Sleep also kills the tailscaled daemon's WireGuard sessions and the
			// proxy mapping it dials through, but a supervised daemon that survives
			// the suspend never reports a failure. Restart it unconditionally: a
			// daemon that was already gone simply makes stopTailscale a no-op, while
			// keeping the stale one left the data plane dead until a manual restart.
			appendServiceLog(h.configPath, "power resume: restarting tailscale")
			if err := stopTailscale(); err != nil {
				appendServiceLog(h.configPath, "restart tailscale after resume: "+err.Error())
				continue
			}
			if err := startTailscale(); err != nil {
				appendServiceLog(h.configPath, "power resume: tailscale: "+err.Error())
				tailscaleRetry.schedule()
				continue
			}
			appendServiceLog(h.configPath, "power resume: core start scheduled")
		case <-coreDone:
			err := core.err
			uptime := core.uptime()
			core.cancel()
			core = nil
			coreDone = nil
			if err != nil {
				appendServiceLog(h.configPath, "run core: "+err.Error())
			} else {
				appendServiceLog(h.configPath, "run core: process exited")
			}
			coreRetry.observe(uptime)
			coreRetry.schedule()
		case <-tsDone:
			err := tsCore.err
			uptime := tsCore.uptime()
			tsCore.cancel()
			tsCore = nil
			tsDone = nil
			if err != nil {
				appendServiceLog(h.configPath, "tailscale: "+err.Error())
			} else {
				appendServiceLog(h.configPath, "tailscale: daemon exited")
			}
			// Only the tailscale component is rescheduled; the mihomo core keeps
			// running untouched.
			tailscaleRetry.observe(uptime)
			tailscaleRetry.schedule()
		case <-coreRetry.ready:
			coreRetry.ready = nil
			if err := startCore(); err != nil {
				appendServiceLog(h.configPath, err.Error())
				coreRetry.schedule()
			}
		case <-tailscaleRetry.ready:
			tailscaleRetry.ready = nil
			if err := startTailscale(); err != nil {
				appendServiceLog(h.configPath, "tailscale: "+err.Error())
				tailscaleRetry.schedule()
			}
		}
	}
}

type serviceCore struct {
	cancel    context.CancelFunc
	done      chan struct{}
	err       error
	startedAt time.Time
}

// uptime reports how long the component process has been supervised. It is
// read when the process exits to decide whether the run was healthy.
func (c *serviceCore) uptime() time.Duration {
	if c == nil || c.startedAt.IsZero() {
		return 0
	}
	return time.Since(c.startedAt)
}

func startServiceCore(cfg *config.Config, profile string) *serviceCore {
	ctx, cancel := context.WithCancel(context.Background())
	core := &serviceCore{cancel: cancel, done: make(chan struct{}), startedAt: time.Now()}
	go func() {
		defer close(core.done)
		core.err = runServiceCore(ctx, cfg, profile, func(int) error { return nil })
	}()
	return core
}

func stopServiceCore(core *serviceCore, timeout time.Duration) error {
	core.cancel()
	select {
	case <-core.done:
		return core.err
	case <-time.After(timeout):
		return fmt.Errorf("timed out after %s", timeout)
	}
}

func isResumePowerEvent(eventType uint32) bool {
	return eventType == powerEventResumeSuspend || eventType == powerEventResumeAutomatic
}

var runServiceCore = func(ctx context.Context, cfg *config.Config, profile string, ready func(int) error) error {
	return runner.ServiceContext(ctx, cfg, profile, ready)
}

var runTailscaleSupervision = func(ctx context.Context, cfg *config.Config) error {
	return runner.TailscaleSupervision(ctx, cfg)
}

func startTailscaleService(cfg *config.Config) *serviceCore {
	ctx, cancel := context.WithCancel(context.Background())
	core := &serviceCore{cancel: cancel, done: make(chan struct{}), startedAt: time.Now()}
	go func() {
		defer close(core.done)
		core.err = runTailscaleSupervision(ctx, cfg)
	}()
	return core
}

var (
	installWindowsService   = winservice.Install
	controlWindowsService   = controlServiceWithDiagnostics
	windowsServiceRunning   = winservice.Running
	windowsServiceInstalled = winservice.Installed
	stopUserCore            = runner.Stop
	prepareWindowsSnapshot  = prepareServiceSnapshot
)

func manageWindowsService(args []string) error {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "configuration path")
	_ = fs.String("result", "", "command result path")
	action := commandArg(args, "status")
	filtered := removeCommandArg(args, action)
	if err := fs.Parse(filtered); err != nil {
		return err
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "status" {
		unlock, err := fileutil.TryLock(filepath.Join(winservice.DataDir(), "service-operation.lock"))
		if err != nil {
			return err
		}
		defer unlock()
	}

	switch action {
	case "register":
		path, err := filepath.Abs(strings.TrimSpace(*configPath))
		if err != nil || strings.TrimSpace(*configPath) == "" {
			return errors.New("service register requires an absolute config path")
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		path, err = config.EnsureCanonicalConfig(
			path,
			filepath.Join(filepath.Dir(executable), "meshmux.example.json"),
			filepath.Join(winservice.DataDir(), config.DefaultConfigPath),
		)
		if err != nil {
			return err
		}
		if err := winservice.SecureDataDir(winservice.DataDir()); err != nil {
			return err
		}
		// Registration prepares only local files. It never contacts providers, Tailnet,
		// controllers, or other upstream services.
		snapshotPath, err := prepareServiceSnapshotFiles(path)
		if err != nil {
			return err
		}
		return installWindowsService(executable, snapshotPath)
	case "install", "activate":
		path, err := filepath.Abs(strings.TrimSpace(*configPath))
		if err != nil || strings.TrimSpace(*configPath) == "" {
			return fmt.Errorf("service %s requires an absolute config path", action)
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		path, err = config.EnsureCanonicalConfig(
			path,
			filepath.Join(filepath.Dir(executable), "meshmux.example.json"),
			filepath.Join(winservice.DataDir(), config.DefaultConfigPath),
		)
		if err != nil {
			return err
		}
		if windowsServiceInstalled() {
			snapshotPath := filepath.Join(winservice.DataDir(), config.DefaultConfigPath)
			if err := installWindowsService(executable, snapshotPath); err != nil {
				return err
			}
			if action == "install" {
				return nil
			}
			return restartWindowsService("restart", path)
		}
		snapshotPath, err := prepareServiceSnapshotFiles(path)
		if err != nil {
			return err
		}
		if err := installWindowsService(executable, snapshotPath); err != nil {
			return err
		}
		if action == "install" {
			return nil
		}
		return activateWindowsService(path)
	case "remove":
		return winservice.Remove()
	case "update-core":
		path, err := filepath.Abs(strings.TrimSpace(*configPath))
		if err != nil || strings.TrimSpace(*configPath) == "" {
			return errors.New("service update-core requires an absolute config path")
		}
		return updateInstalledServiceCore(path)
	case "start", "stop", "restart":
		if action == "stop" {
			return controlWindowsService("stop", 30*time.Second)
		}
		if strings.TrimSpace(*configPath) == "" {
			return fmt.Errorf("service %s requires an absolute config path", action)
		}
		if _, err := os.Stat(filepath.Join(winservice.DataDir(), config.DefaultConfigPath)); os.IsNotExist(err) {
			return activateWindowsService(*configPath)
		}
		return restartWindowsService(action, *configPath)
	case "status":
		status, err := winservice.Status()
		if err != nil {
			return err
		}
		fmt.Println(status)
		return nil
	default:
		return fmt.Errorf("service expects register, install, activate, update-core, remove, start, stop, restart, or status")
	}
}

func activateWindowsService(sourcePath string) error {
	cfg, _, err := load(configArgs(sourcePath))
	if err != nil {
		return err
	}
	if _, err := prepareWindowsSnapshot(sourcePath); err != nil {
		return fmt.Errorf("prepare snapshot: %w", err)
	}
	if windowsServiceRunning() {
		if err := controlWindowsService("stop", 15*time.Second); err != nil {
			return fmt.Errorf("stop existing service: %w", err)
		}
	}
	if err := stopUserCore(cfg); err != nil {
		return fmt.Errorf("stop existing user core: %w", err)
	}
	if err := controlWindowsService("start", 15*time.Second); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

func restartWindowsService(action, sourcePath string) error {
	if action == "start" && windowsServiceRunning() {
		return nil
	}
	cfg, _, err := load(configArgs(sourcePath))
	if err != nil {
		return err
	}
	if _, err := prepareWindowsSnapshot(sourcePath); err != nil {
		return fmt.Errorf("prepare service snapshot: %w", err)
	}
	if action == "restart" && windowsServiceRunning() {
		if err := controlWindowsService("stop", 15*time.Second); err != nil {
			return fmt.Errorf("stop existing service: %w", err)
		}
	}
	if err := stopUserCore(cfg); err != nil {
		return fmt.Errorf("stop existing user core: %w", err)
	}
	if err := controlWindowsService("start", 15*time.Second); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

type snapshotFileBackup struct {
	path   string
	data   []byte
	exists bool
}

func captureServiceSnapshot(incoming ...*config.Config) ([]snapshotFileBackup, error) {
	home := winservice.DataDir()
	paths := []string{
		filepath.Join(home, config.DefaultConfigPath),
		filepath.Join(home, "profiles", "windows.yaml"),
		filepath.Join(home, config.DefaultMihomoPath()),
	}
	for _, directory := range []string{"providers", "wireguard"} {
		root := filepath.Join(home, directory)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("snapshot links are not supported: %s", path)
			}
			if !entry.IsDir() {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for _, cfg := range incoming {
		references := append([]string{}, cfg.WireGuard.Configs...)
		for _, provider := range cfg.Providers {
			path := provider.Path
			if path == "" && provider.Name != "" {
				path = filepath.Join("providers", provider.Name+".yaml")
			}
			if path != "" {
				references = append(references, path)
			}
		}
		for _, path := range references {
			if !filepath.IsLocal(path) {
				return nil, fmt.Errorf("snapshot asset must stay inside data directory: %s", path)
			}
			paths = append(paths, filepath.Join(home, path))
		}
	}
	seen := map[string]bool{}
	backup := make([]snapshotFileBackup, 0, len(paths))
	for _, path := range paths {
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			continue
		}
		seen[key] = true
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			backup = append(backup, snapshotFileBackup{path: path})
			continue
		}
		if err != nil {
			return nil, err
		}
		backup = append(backup, snapshotFileBackup{path: path, data: data, exists: true})
	}
	return backup, nil
}

func controlServiceWithDiagnostics(action string, timeout time.Duration) error {
	if err := winservice.Control(action, timeout); err != nil {
		return fmt.Errorf("%w; %s", err, runner.ServiceDiagnostics(winservice.DataDir()))
	}
	return nil
}

func restoreServiceSnapshot(backup []snapshotFileBackup) error {
	for _, file := range backup {
		if file.exists {
			if err := writeSnapshotFile(file.path, file.data); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func serviceFailure(configPath, stage string, err error) (bool, uint32) {
	message := fmt.Sprintf("%s: %v", strings.TrimSpace(stage), err)
	appendServiceLog(configPath, message)
	return true, 1
}

func appendServiceLog(configPath, message string) {
	_ = runner.AppendDiagnosticLog(filepath.Join(filepath.Dir(configPath), "logs", "service.log"), message)
}

func writeWindowsCommandError(args []string, err error) {
	if err == nil || len(args) == 0 || args[0] != "service" {
		return
	}
	dataDir := winservice.DataDir()
	if configPath := strings.TrimSpace(configPathArg(args)); configPath != "" {
		dataDir = filepath.Dir(configPath)
	}
	_ = runner.AppendDiagnosticLog(filepath.Join(dataDir, "logs", "service-command.log"), fmt.Sprintf("%s: %v", strings.Join(args, " "), err))
}

func writeWindowsCommandResult(args []string, commandErr error) {
	path := strings.TrimSpace(optionValue(args, "result"))
	if path == "" {
		return
	}
	value := "ok\n"
	if commandErr != nil {
		value = "error: " + commandErr.Error() + "\n"
	}
	_ = writeSnapshotFile(path, []byte(value))
}

func prepareServiceSnapshot(sourcePath string) (string, error) {
	return prepareServiceSnapshotFiles(sourcePath)
}

func prepareServiceSnapshotFiles(sourcePath string) (result string, resultErr error) {
	sourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", err
	}
	cfg, _, err := load(configArgs(sourcePath))
	if err != nil {
		return "", err
	}
	dataDir := winservice.DataDir()
	if err := rejectBootstrapRegression(sourcePath, filepath.Join(dataDir, config.DefaultConfigPath)); err != nil {
		return "", err
	}
	profile, err := generator.GenerateNamed(cfg, "windows")
	if err != nil {
		return "", err
	}
	profileData, err := os.ReadFile(profile)
	if err != nil {
		return "", err
	}
	if err := winservice.SecureDataDir(dataDir); err != nil {
		return "", err
	}
	backup, err := captureServiceSnapshot(cfg)
	if err != nil {
		return "", err
	}
	defer func() {
		if resultErr != nil {
			if restoreErr := restoreServiceSnapshot(backup); restoreErr != nil {
				resultErr = fmt.Errorf("%v; snapshot rollback failed: %w", resultErr, restoreErr)
			}
		}
	}()
	profilePath := filepath.Join(dataDir, "profiles", "windows.yaml")
	if err := writeSnapshotFile(profilePath, profileData); err != nil {
		return "", err
	}
	if err := snapshotReferencedAssets(cfg, dataDir); err != nil {
		return "", err
	}
	if err := installServiceCore(dataDir); err != nil {
		return "", err
	}
	if err := installServiceComponents(dataDir); err != nil {
		return "", err
	}

	stored := cfg.StorageCopy()
	stored.Components.Mihomo.Path = config.DefaultMihomoPath()
	stored.Components.Tailscale.Path = config.DefaultTailscaledPath()
	stored.Paths.Dashboard = "dashboard"
	configData, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return "", err
	}
	snapshotPath := filepath.Join(dataDir, config.DefaultConfigPath)
	if err := writeSnapshotFile(snapshotPath, append(configData, '\n')); err != nil {
		return "", err
	}
	if err := winservice.SecureDataDir(dataDir); err != nil {
		return "", err
	}
	return snapshotPath, nil
}

func installServiceCore(dataDir string) error {
	target := filepath.Join(dataDir, config.DefaultMihomoPath())
	if info, err := os.Stat(target); err == nil && !info.IsDir() && info.Size() >= 1024*1024 {
		// The service-owned core is an explicit runtime state. An installer upgrade
		// must not silently downgrade it; use the core update action instead.
		return nil
	}
	source := config.BundledMihomoPath()
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("bundled mihomo is unavailable: %w", err)
	}
	return copyFileForService(source, target)
}

func copyFileForService(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	return fileutil.Write(target, 0700, func(out io.Writer) error {
		_, err := io.Copy(out, in)
		return err
	})
}

// installServiceComponents seeds the bundled tailscale trio and the shared TUN
// driver into the directory the service actually reads. The installer places
// them next to its own executable, but the service resolves bin/ relative to
// its data directory, so without this copy every tailscaled start fails and a
// copied mihomo core silently loses TUN support.
func installServiceComponents(dataDir string) error {
	candidates := []struct {
		source string
		target string
	}{
		{config.BundledTailscaledPath(), filepath.Join(dataDir, config.DefaultTailscaledPath())},
		{config.BundledTailscaleCLIPath(), filepath.Join(dataDir, config.DefaultTailscaleCLIPath())},
		{config.BundledWintunPath(), filepath.Join(dataDir, "bin", "wintun.dll")},
	}
	missing := 0
	for _, candidate := range candidates {
		if serviceComponentMissing(candidate.source, candidate.target) {
			missing++
		}
	}
	if missing == 0 {
		return nil
	}
	for _, candidate := range candidates {
		if !serviceComponentMissing(candidate.source, candidate.target) {
			continue
		}
		if err := copyFileForService(candidate.source, candidate.target); err != nil {
			return err
		}
	}
	return nil
}

// serviceComponentMissing reports whether the bundle should be copied. A missing
// bundle is not an error: tailscale support is optional, and an existing file is
// explicit runtime state that an installer upgrade must not overwrite.
func serviceComponentMissing(source, target string) bool {
	if source == "" || source == target {
		return false
	}
	if _, err := os.Stat(source); err != nil {
		return false
	}
	info, err := os.Stat(target)
	return err != nil || info.IsDir() || info.Size() == 0
}

func updateInstalledServiceCore(sourcePath string) error {
	if !winservice.Installed() {
		return nil
	}
	if err := winservice.SecureDataDir(winservice.DataDir()); err != nil {
		return err
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return err
	}
	running := winservice.Running()
	if running {
		if err := controlWindowsService("stop", 30*time.Second); err != nil {
			return fmt.Errorf("stop service for core update: %w", err)
		}
	}
	if err := installServiceCoreFromConfig(sourcePath); err != nil {
		if running {
			_ = controlWindowsService("start", 30*time.Second)
		}
		return err
	}
	if running {
		if err := controlWindowsService("start", 30*time.Second); err != nil {
			return fmt.Errorf("restart service after core update: %w", err)
		}
	}
	return nil
}

func updateServiceCoreIfInstalled(sourcePath string) error {
	if !winservice.Installed() {
		return nil
	}
	return winservice.RunElevated("update-core", sourcePath)
}

func installServiceCoreFromConfig(sourcePath string) error {
	cfg, _, err := config.Load(sourcePath)
	if err != nil {
		return err
	}
	source := strings.TrimSpace(cfg.Components.Mihomo.Path)
	if source == "" {
		source = config.DefaultMihomoPath()
	}
	if !filepath.IsAbs(source) {
		abs, err := filepath.Abs(filepath.Join(filepath.Dir(sourcePath), source))
		if err != nil {
			return err
		}
		source = abs
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("downloaded mihomo is unavailable: %w", err)
	}
	return copyFileForService(source, serviceCorePath())
}

func snapshotReferencedAssets(cfg *config.Config, destinationRoot string) error {
	paths := make([]string, 0, len(cfg.Providers)+len(cfg.WireGuard.Configs))
	for _, provider := range cfg.Providers {
		path := strings.TrimSpace(provider.Path)
		if path == "" && strings.TrimSpace(provider.Name) != "" {
			path = filepath.Join("providers", provider.Name+".yaml")
		}
		if path != "" {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			}
			paths = append(paths, path)
		}
	}
	paths = append(paths, cfg.WireGuard.Configs...)
	for _, path := range paths {
		cleaned := filepath.Clean(strings.TrimSpace(path))
		if cleaned == "." || filepath.IsAbs(cleaned) {
			return fmt.Errorf("service snapshot asset must use a relative path: %s", path)
		}
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("service snapshot asset escapes the data directory: %s", path)
		}
		data, err := os.ReadFile(cleaned)
		if err != nil {
			return fmt.Errorf("read service snapshot asset %s: %w", cleaned, err)
		}
		if err := writeSnapshotFile(filepath.Join(destinationRoot, cleaned), data); err != nil {
			return fmt.Errorf("write service snapshot asset %s: %w", cleaned, err)
		}
	}
	return nil
}

func rejectBootstrapRegression(sourcePath, currentPath string) error {
	sourceData, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	if !config.IsBootstrapConfig(sourceData) {
		return nil
	}
	currentData, err := os.ReadFile(currentPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if config.IsBootstrapConfig(currentData) {
		return nil
	}
	return errors.New("拒绝用安装器空模板覆盖现有 MeshMux 服务配置；请恢复 LocalAppData 中的真实配置后重试")
}

func writeSnapshotFile(path string, data []byte) error {
	return fileutil.WriteFile(path, data, 0600)
}

func removeCommandArg(args []string, command string) []string {
	result := make([]string, 0, len(args))
	removed := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !removed && arg == command {
			removed = true
			continue
		}
		result = append(result, arg)
	}
	return result
}
