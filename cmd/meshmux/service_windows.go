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

type serviceHandler struct {
	configPath string
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	cfg, _, err := load(configArgs(h.configPath))
	if err != nil {
		return serviceFailure(h.configPath, "load config", err)
	}
	// A system service must never execute a user-replaceable custom core.
	cfg.Components.Mihomo.Path = config.BundledMihomoPath()
	profile := filepath.Join(filepath.Dir(h.configPath), "profiles", "windows.yaml")
	if _, err := os.Stat(profile); err != nil {
		return serviceFailure(h.configPath, "find generated profile", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- runServiceCore(ctx, cfg, profile, func(int) error {
			ready <- nil
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			return serviceFailure(h.configPath, "start core", err)
		}
		return false, 0
	case <-ready:
		changes <- svc.Status{State: svc.Running, Accepts: accepts}
	}

	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case err := <-done:
					if err != nil {
						return serviceFailure(h.configPath, "stop core", err)
					}
				case <-time.After(15 * time.Second):
					return serviceFailure(h.configPath, "stop core", errors.New("timed out after 15 seconds"))
				}
				return false, 0
			}
		case err := <-done:
			if err != nil {
				return serviceFailure(h.configPath, "run core", err)
			}
			return false, 0
		}
	}
}

var runServiceCore = func(ctx context.Context, cfg *config.Config, profile string, ready func(int) error) error {
	return runner.ServiceContext(ctx, cfg, profile, ready)
}

var (
	installWindowsService   = winservice.Install
	controlWindowsService   = winservice.Control
	windowsServiceRunning   = winservice.Running
	windowsServiceInstalled = winservice.Installed
	stopUserCore            = runner.Stop
	startUserCore           = startDetached
	verifyWindowsService    = waitForWindowsService
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

	switch action {
	case "install", "activate", "activate-if-ready":
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
		snapshotPath, err := prepareServiceSnapshotFiles(path)
		if err != nil {
			return handleSnapshotPreparationError(action, err)
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
	case "start", "stop", "restart":
		if action == "stop" {
			return controlWindowsService("stop", 30*time.Second)
		}
		if strings.TrimSpace(*configPath) == "" {
			return fmt.Errorf("service %s requires an absolute config path", action)
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
		return fmt.Errorf("service expects install, activate, activate-if-ready, remove, start, stop, restart, or status")
	}
}

func handleSnapshotPreparationError(action string, prepareErr error) error {
	if action != "activate-if-ready" {
		return prepareErr
	}
	if windowsServiceInstalled() {
		if startErr := controlWindowsService("start", 30*time.Second); startErr != nil {
			return fmt.Errorf("new config validation failed: %v; previous service could not be restored: %w", prepareErr, startErr)
		}
		return fmt.Errorf("new config validation failed: %w; previous service was restored", prepareErr)
	}
	if generator.IsMissingProviderError(prepareErr) {
		return nil
	}
	return prepareErr
}

func activateWindowsService(sourcePath string) error {
	cfg, configPath, err := load(configArgs(sourcePath))
	if err != nil {
		return err
	}
	profile, err := generator.GenerateNamed(cfg, "windows")
	if err != nil {
		return err
	}
	if err := controlWindowsService("stop", 30*time.Second); err != nil {
		return fmt.Errorf("stop existing service: %w", err)
	}
	if err := stopUserCore(cfg); err != nil {
		return fmt.Errorf("stop existing user core: %w", err)
	}
	if _, err := prepareWindowsSnapshot(sourcePath); err != nil {
		return fmt.Errorf("prepare service snapshot: %w", err)
	}
	startErr := controlWindowsService("start", 30*time.Second)
	if startErr == nil {
		startErr = verifyWindowsService(cfg, 5*time.Second)
	}
	if startErr == nil {
		return nil
	}
	_ = controlWindowsService("stop", 15*time.Second)
	restoreErr := startUserCore(cfg, configPath, profile)
	if restoreErr != nil {
		return fmt.Errorf("start service: %v; restore user core: %w", startErr, restoreErr)
	}
	return fmt.Errorf("start service: %w; previous user core was restored", startErr)
}

func restartWindowsService(action, sourcePath string) error {
	if action == "start" && windowsServiceRunning() {
		return nil
	}
	cfg, _, err := load(configArgs(sourcePath))
	if err != nil {
		return err
	}
	backup, err := captureServiceSnapshot()
	if err != nil {
		return fmt.Errorf("backup service snapshot: %w", err)
	}
	if _, err := prepareWindowsSnapshot(sourcePath); err != nil {
		return fmt.Errorf("prepare service snapshot: %w", err)
	}
	if action == "restart" {
		if err := controlWindowsService("stop", 30*time.Second); err != nil {
			cause := fmt.Errorf("stop existing service: %w", err)
			if restoreErr := restoreServiceSnapshot(backup); restoreErr != nil {
				return fmt.Errorf("%v; restore previous service snapshot: %w", cause, restoreErr)
			}
			return cause
		}
	}
	if err := stopUserCore(cfg); err != nil {
		return restoreStoppedService(backup, fmt.Errorf("stop existing user core: %w", err))
	}
	if err := controlWindowsService("start", 30*time.Second); err != nil {
		return restoreStoppedService(backup, fmt.Errorf("start service: %w", err))
	}
	if err := verifyWindowsService(cfg, 5*time.Second); err != nil {
		_ = controlWindowsService("stop", 15*time.Second)
		return restoreStoppedService(backup, fmt.Errorf("verify service: %w", err))
	}
	return nil
}

type snapshotFileBackup struct {
	path   string
	data   []byte
	exists bool
}

func captureServiceSnapshot() ([]snapshotFileBackup, error) {
	home := winservice.DataDir()
	paths := []string{
		filepath.Join(home, config.DefaultConfigPath),
		filepath.Join(home, "profiles", "windows.yaml"),
	}
	backup := make([]snapshotFileBackup, 0, len(paths))
	for _, path := range paths {
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

func restoreStoppedService(backup []snapshotFileBackup, cause error) error {
	if err := restoreServiceSnapshot(backup); err != nil {
		return fmt.Errorf("%v; restore previous service snapshot: %w", cause, err)
	}
	if err := controlWindowsService("start", 30*time.Second); err != nil {
		return fmt.Errorf("%v; restart previous service snapshot: %w", cause, err)
	}
	return fmt.Errorf("%w; previous service snapshot was restored", cause)
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

func waitForWindowsService(cfg *config.Config, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var readySince time.Time
	for time.Now().Before(deadline) {
		if winservice.Running() && runner.ControllerReady(cfg) {
			if readySince.IsZero() {
				readySince = time.Now()
			}
			if time.Since(readySince) >= time.Second {
				return nil
			}
		} else {
			readySince = time.Time{}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("service did not remain running with a ready controller")
}

func serviceFailure(configPath, stage string, err error) (bool, uint32) {
	message := fmt.Sprintf("%s: %v", strings.TrimSpace(stage), err)
	logDir := filepath.Join(filepath.Dir(configPath), "logs")
	if mkdirErr := os.MkdirAll(logDir, 0700); mkdirErr == nil {
		path := filepath.Join(logDir, "service.log")
		if file, openErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); openErr == nil {
			_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().Format(time.RFC3339), message)
			_ = file.Close()
		}
	}
	return true, 1
}

func writeWindowsCommandError(args []string, err error) {
	if err == nil || len(args) == 0 || args[0] != "service" {
		return
	}
	dataDir := winservice.DataDir()
	if configPath := strings.TrimSpace(configPathArg(args)); configPath != "" {
		dataDir = filepath.Dir(configPath)
	}
	if mkdirErr := os.MkdirAll(filepath.Join(dataDir, "logs"), 0700); mkdirErr != nil {
		return
	}
	path := filepath.Join(dataDir, "logs", "service-command.log")
	file, openErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if openErr != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s %s: %v\n", time.Now().Format(time.RFC3339), strings.Join(args, " "), err)
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

func prepareServiceSnapshotFiles(sourcePath string) (string, error) {
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
	profilePath := filepath.Join(dataDir, "profiles", "windows.yaml")
	if err := writeSnapshotFile(profilePath, profileData); err != nil {
		return "", err
	}
	if err := snapshotReferencedAssets(cfg, dataDir); err != nil {
		return "", err
	}

	stored := cfg.StorageCopy()
	stored.Components.Mihomo.Path = config.BundledMihomoPath()
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

func snapshotReferencedAssets(cfg *config.Config, destinationRoot string) error {
	paths := make([]string, 0, len(cfg.Providers)+len(cfg.WireGuard.Configs))
	for _, provider := range cfg.Providers {
		path := strings.TrimSpace(provider.Path)
		if path == "" && strings.TrimSpace(provider.Name) != "" {
			path = filepath.Join("providers", provider.Name+".yaml")
		}
		if path != "" {
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
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tempPath, path)
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
