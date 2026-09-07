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
	if cfg, _, err := load(configArgs(configPath)); err == nil && tailnetNeedsForcedLogin(cfg, home) {
		if err := os.Setenv("TSNET_FORCE_LOGIN", "1"); err != nil {
			return err
		}
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

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	var core *serviceCore
	var coreDone <-chan error
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
		replacement, err := startServiceCore(cfg, profile)
		if err != nil {
			return fmt.Errorf("start core: %w", err)
		}
		core = replacement
		coreDone = replacement.done
		return nil
	}
	stopCore := func() error {
		if core == nil {
			return nil
		}
		err := stopServiceCore(core, 8*time.Second)
		core = nil
		coreDone = nil
		return err
	}
	defer func() {
		if core != nil {
			core.cancel()
		}
	}()

	var resumeTimer *time.Timer
	var resume <-chan time.Time
	var retryTimer *time.Timer
	var retry <-chan time.Time
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
	stopRetryTimer := func() {
		if retryTimer != nil {
			if !retryTimer.Stop() {
				select {
				case <-retryTimer.C:
				default:
				}
			}
		}
		retry = nil
	}
	defer stopRetryTimer()
	scheduleRetry := func() {
		stopRetryTimer()
		retryTimer = time.NewTimer(serviceCoreRetryDelay)
		retry = retryTimer.C
	}
	if err := startCore(); err != nil {
		appendServiceLog(h.configPath, err.Error())
		scheduleRetry()
	}

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
				stopRetryTimer()
				changes <- svc.Status{State: svc.StopPending}
				if err := stopCore(); err != nil {
					return serviceFailure(h.configPath, "stop core", err)
				}
				return false, 0
			}
		case <-resume:
			resume = nil
			stopRetryTimer()
			appendServiceLog(h.configPath, "power resume: restarting core")
			if err := stopCore(); err != nil {
				appendServiceLog(h.configPath, "restart core after resume: "+err.Error())
			}
			if err := startCore(); err != nil {
				appendServiceLog(h.configPath, "restart core after resume: "+err.Error())
				scheduleRetry()
				continue
			}
			appendServiceLog(h.configPath, "power resume: core restarted")
		case err := <-coreDone:
			core = nil
			coreDone = nil
			if err != nil {
				appendServiceLog(h.configPath, "run core: "+err.Error())
			} else {
				appendServiceLog(h.configPath, "run core: process exited")
			}
			scheduleRetry()
		case <-retry:
			retry = nil
			if err := startCore(); err != nil {
				appendServiceLog(h.configPath, err.Error())
				scheduleRetry()
			}
		}
	}
}

type serviceCore struct {
	cancel context.CancelFunc
	done   chan error
}

func startServiceCore(cfg *config.Config, profile string) (*serviceCore, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runServiceCore(ctx, cfg, profile, func(int) error {
			ready <- struct{}{}
			return nil
		})
	}()
	core := &serviceCore{cancel: cancel, done: done}
	select {
	case err := <-done:
		cancel()
		if err == nil {
			err = errors.New("core exited before process creation")
		}
		return nil, err
	case <-ready:
		return core, nil
	}
}

func stopServiceCore(core *serviceCore, timeout time.Duration) error {
	core.cancel()
	select {
	case err := <-core.done:
		return err
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

	stored := cfg.StorageCopy()
	stored.Components.Mihomo.Path = config.DefaultMihomoPath()
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

func tailnetNeedsForcedLogin(cfg *config.Config, home string) bool {
	if cfg == nil || !cfg.Tailscale.Enabled {
		return false
	}
	if strings.TrimSpace(cfg.Tailscale.AuthKey) == "" && strings.TrimSpace(cfg.Tailscale.AuthKeyFile) == "" {
		return false
	}
	return !validTailnetState(filepath.Join(home, "state", "tailscale"))
}

func validTailnetState(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "tailscaled.state"))
	if err != nil || len(data) == 0 {
		return false
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(data, &state) != nil {
		return false
	}
	for _, key := range []string{"_machinekey", "_current-profile", "_profiles"} {
		value, ok := state[key]
		if !ok || len(value) == 0 || string(value) == `""` || string(value) == "null" {
			return false
		}
	}
	return true
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
