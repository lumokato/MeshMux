package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/fileutil"
)

// The bundled tailscaled is a stock upstream build (see THIRD_PARTY_NOTICES.md).
// MeshMux supervises the daemon and drives the tailscale CLI against a private
// IPC socket so a system-wide Tailscale installation can never be touched.
const (
	tailscaleStateFileName = "tailscaled.state"
	tailscaleAuthKeyFile   = "authkey"
)

func tailscaleStateDir() string {
	return filepath.Join("state", "tailscale")
}

func tailscaleStatePath() string {
	return filepath.Join(tailscaleStateDir(), tailscaleStateFileName)
}

func tailscaleAuthKeyPath() string {
	return filepath.Join(tailscaleStateDir(), tailscaleAuthKeyFile)
}

// tailscaleSocketPath returns the IPC endpoint handed to both tailscaled and
// the tailscale CLI. Windows uses a private named pipe; other platforms use a
// socket inside the runtime state directory.
func tailscaleSocketPath() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\MeshMux-Tailscale`
	}
	abs, err := filepath.Abs(filepath.Join(tailscaleStateDir(), "tailscaled.sock"))
	if err != nil {
		return filepath.Join(tailscaleStateDir(), "tailscaled.sock")
	}
	return abs
}

func tailscaledExecutable(cfg *config.Config) (string, error) {
	exe := strings.TrimSpace(cfg.Components.Tailscale.Path)
	if exe == "" {
		exe = config.DefaultTailscaledPath()
	}
	if !fileLooksUsable(exe, 1<<20) {
		return "", fmt.Errorf("tailscaled 未找到或不可用: %s；请先通过下载组件安装 tailscale", exe)
	}
	return exe, nil
}

func isDefaultTailscaledExecPath(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return false
	}
	return sameExecutablePath(cleaned, filepath.Clean(config.DefaultTailscaledPath()))
}

// PrepareTailscaleComponents mirrors prepareMihomo for the tailscale trio. The
// installer ships tailscaled, the CLI and the wintun driver next to its own
// executable, but the service reads its private data directory, so nothing is
// reachable until the bundle is copied into the directory the running process
// actually resolves. Existing files are left untouched: an explicitly updated
// component is runtime state, exactly like the service-owned mihomo core.
func PrepareTailscaleComponents(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	if !cfg.Tailscale.Enabled {
		return nil
	}
	target := strings.TrimSpace(cfg.Components.Tailscale.Path)
	if target == "" {
		target = config.DefaultTailscaledPath()
	}
	if !isDefaultTailscaledExecPath(target) {
		// A custom path is an explicit operator choice; never seed it from the bundle.
		return nil
	}
	dir := filepath.Dir(target)
	cliTarget := filepath.Join(dir, filepath.Base(config.DefaultTailscaleCLIPath()))
	candidates := []struct {
		source  string
		target  string
		minSize int64
	}{
		{config.BundledTailscaledPath(), target, 1 << 20},
		{config.BundledTailscaleCLIPath(), cliTarget, 1 << 20},
		{config.BundledWintunPath(), filepath.Join(dir, "wintun.dll"), 0},
	}
	var missing []string
	for _, candidate := range candidates {
		if candidate.source == "" || candidate.source == candidate.target {
			continue
		}
		if !fileLooksUsable(candidate.source, candidate.minSize) {
			continue
		}
		if fileLooksUsable(candidate.target, candidate.minSize) {
			continue
		}
		missing = append(missing, filepath.Base(candidate.target))
	}
	if len(missing) == 0 {
		return nil
	}
	for _, candidate := range candidates {
		if candidate.source == "" || candidate.source == candidate.target {
			continue
		}
		if !fileLooksUsable(candidate.source, candidate.minSize) || fileLooksUsable(candidate.target, candidate.minSize) {
			continue
		}
		if err := copyBundledFile(candidate.source, candidate.target); err != nil {
			return fmt.Errorf("同步内置 tailscale 组件失败 (%s): %w", filepath.Base(candidate.target), err)
		}
	}
	appendRunnerLog("已同步安装包内置 tailscale 组件到 %s (%s)", dir, strings.Join(missing, ", "))
	return nil
}

// EnsureWintunBeside seeds the TUN driver next to a core when the installer did
// not leave one there. mihomo and tailscaled both load wintun from the
// directory of their own executable, so a copied core without the DLL silently
// loses TUN support.
func EnsureWintunBeside(execPath string) {
	if runtime.GOOS != "windows" || strings.TrimSpace(execPath) == "" {
		return
	}
	source := config.BundledWintunPath()
	target := filepath.Join(filepath.Dir(execPath), "wintun.dll")
	if source == "" || source == target {
		return
	}
	if !fileLooksUsable(source, 0) || fileLooksUsable(target, 0) {
		return
	}
	if err := copyBundledFile(source, target); err != nil {
		appendRunnerLog("同步 wintun.dll 到 %s 失败: %v", filepath.Dir(execPath), err)
		return
	}
	appendRunnerLog("已同步安装包内置 wintun.dll 到 %s", filepath.Dir(execPath))
}

func tailscaledCommand(cfg *config.Config) (*exec.Cmd, error) {
	exe, err := tailscaledExecutable(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(tailscaleStateDir(), 0700); err != nil {
		return nil, err
	}
	cmd := exec.Command(exe,
		"--state", tailscaleStatePath(),
		"--statedir", tailscaleStateDir(),
		"--socket", tailscaleSocketPath(),
	)
	hideWindow(cmd)
	return cmd, nil
}

// TailscaleSupervision runs the tailscaled daemon until ctx is cancelled or
// the process exits. The caller (service loop or headless run) decides how to
// react to a non-nil error.
func TailscaleSupervision(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	if err := PrepareTailscaleComponents(cfg); err != nil {
		return err
	}
	cmd, err := tailscaledCommand(cfg)
	if err != nil {
		return err
	}
	logOut, err := newSanitizedRotatingLog(filepath.Join("logs", "tailscaled.out.log"), mihomoLogPolicy)
	if err != nil {
		return err
	}
	defer logOut.Close()
	cmd.Stdout = logOut
	cmd.Stderr = logOut
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 tailscaled 失败: %w", err)
	}
	appendRunnerLog("tailscaled 已启动 (PID %d)", cmd.Process.Pid)
	go func() {
		if err := tailscaleAutoLogin(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
			appendRunnerLog("tailscale 自动登录失败: %v", err)
		}
	}()
	return cmd.Wait()
}

func tailscaleCLIPath(cfg *config.Config) string {
	dir := filepath.Dir(strings.TrimSpace(cfg.Components.Tailscale.Path))
	if dir == "" || dir == "." {
		dir = filepath.Dir(config.DefaultTailscaledPath())
	}
	return filepath.Join(dir, filepath.Base(config.DefaultTailscaleCLIPathFor(runtime.GOOS)))
}

func runTailscaleCLI(ctx context.Context, cfg *config.Config, args ...string) (string, error) {
	cli := tailscaleCLIPath(cfg)
	if !fileLooksUsable(cli, 1<<20) {
		return "", fmt.Errorf("tailscale CLI 未找到或不可用: %s；请先通过下载组件安装 tailscale", cli)
	}
	full := append([]string{"--socket", tailscaleSocketPath()}, args...)
	cmd := exec.CommandContext(ctx, cli, full...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// prepareTailscaleAuthKey materializes the configured key as a file for
// --auth-key=file: so the secret never appears in a command line. The returned
// cleanup removes the materialized copy; a user-configured AuthKeyFile is
// referenced in place.
func prepareTailscaleAuthKey(cfg *config.Config) (string, func(), error) {
	if keyFile := strings.TrimSpace(cfg.Tailscale.AuthKeyFile); keyFile != "" && fileHasContentRunner(keyFile) {
		return keyFile, func() {}, nil
	}
	key := strings.TrimSpace(cfg.Tailscale.AuthKey)
	if key == "" {
		return "", func() {}, nil
	}
	if err := os.MkdirAll(tailscaleStateDir(), 0700); err != nil {
		return "", nil, err
	}
	path := tailscaleAuthKeyPath()
	if err := fileutil.WriteFile(path, []byte(key+"\n"), 0600); err != nil {
		return "", nil, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func fileHasContentRunner(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	return err == nil && !info.IsDir() && info.Size() > 0
}

// TailscaleAuthKeyConfigured reports whether a headless login can complete:
// without an auth key the "tailscale up" transaction needs an interactive
// browser session, which the service never has.
func TailscaleAuthKeyConfigured(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	if keyFile := strings.TrimSpace(cfg.Tailscale.AuthKeyFile); keyFile != "" && fileHasContentRunner(keyFile) {
		return true
	}
	return strings.TrimSpace(cfg.Tailscale.AuthKey) != ""
}

// Test seams for the auto-login flow; production code uses the real functions.
var (
	tailscaleStatusFn = TailscaleStatus
	tailscaleUpFn     = TailscaleUp
)

// tailscaleAutoLogin performs the deferred login once the daemon is reachable.
// Restarting only respawns tailscaled, which restores NeedsLogin from its
// state file until something executes the actual "tailscale up" transaction.
// With an auth key configured that transaction runs unattended right after
// the daemon comes up; without one the guard skips and the status card keeps
// asking the user to act.
func tailscaleAutoLogin(ctx context.Context, cfg *config.Config) error {
	if cfg == nil || !cfg.Tailscale.Enabled || !TailscaleAuthKeyConfigured(cfg) {
		return nil
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		status, err := tailscaleStatusFn(statusCtx, cfg)
		cancel()
		if err == nil {
			if status.BackendState == "Running" {
				return nil
			}
			break
		}
		if time.Now().After(deadline) {
			return errors.New("tailscaled 未就绪，放弃自动登录")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if err := tailscaleUpFn(ctx, cfg); err != nil {
		return err
	}
	appendRunnerLog("tailscale 自动登录完成")
	return nil
}

// TailscaleUp applies the configured desired state through the tailscale CLI.
// It is idempotent: when the node is already logged in and the flags match,
// tailscale up does nothing.
func TailscaleUp(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	args := []string{"up"}
	if hostname := strings.TrimSpace(cfg.Tailscale.Hostname); hostname != "" {
		args = append(args, "--hostname="+hostname)
	}
	args = append(args, "--accept-dns="+strconv.FormatBool(cfg.Tailscale.AcceptDNS))
	if cfg.Tailscale.AcceptRoutes != nil {
		args = append(args, "--accept-routes="+strconv.FormatBool(*cfg.Tailscale.AcceptRoutes))
	}
	if runtime.GOOS == "windows" {
		// Keep the daemon connected after the interactive user logs out; the
		// service runs it without any user session.
		args = append(args, "--unattended")
	}
	keyPath, cleanup, err := prepareTailscaleAuthKey(cfg)
	if err != nil {
		return err
	}
	defer cleanup()
	if keyPath != "" {
		args = append(args, "--auth-key=file:"+keyPath)
	}
	out, err := runTailscaleCLI(ctx, cfg, args...)
	if err != nil {
		return fmt.Errorf("tailscale up 失败: %w: %s", err, redactSensitiveText(strings.TrimSpace(out)))
	}
	return nil
}

func TailscaleDown(ctx context.Context, cfg *config.Config) error {
	out, err := runTailscaleCLI(ctx, cfg, "down")
	if err != nil {
		return fmt.Errorf("tailscale down 失败: %w: %s", err, redactSensitiveText(strings.TrimSpace(out)))
	}
	return nil
}

type TailscaleState struct {
	BackendState string
	Online       bool
	TailscaleIPs []string
	Health       []string
}

// TailscaleStatus queries the daemon through the private socket. An absent or
// stopped daemon surfaces as an error; the caller decides how to display it.
func TailscaleStatus(ctx context.Context, cfg *config.Config) (TailscaleState, error) {
	out, err := runTailscaleCLI(ctx, cfg, "status", "--json")
	if err != nil {
		return TailscaleState{}, fmt.Errorf("tailscale status 失败: %w", err)
	}
	var payload struct {
		BackendState string   `json:"BackendState"`
		Health       []string `json:"Health"`
		Self         *struct {
			Online       bool     `json:"Online"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return TailscaleState{}, fmt.Errorf("tailscale status 输出无效: %w", err)
	}
	status := TailscaleState{BackendState: payload.BackendState, Health: payload.Health}
	if payload.Self != nil {
		status.Online = payload.Self.Online
		status.TailscaleIPs = payload.Self.TailscaleIPs
	}
	return status, nil
}

// withTailscaleSupervision runs fn alongside a tailscaled daemon when the
// configuration enables it. The daemon stops when fn returns or ctx is
// cancelled; a daemon failure alone does not abort fn.
// The windows service path supervises tailscaled separately and must not use
// this wrapper, or two daemons would race for the same socket.
func withTailscaleSupervision(ctx context.Context, cfg *config.Config, fn func() error) error {
	if cfg == nil || !cfg.Tailscale.Enabled {
		return fn()
	}
	daemonCtx, stopDaemon := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		err := TailscaleSupervision(daemonCtx, cfg)
		select {
		case done <- err:
		case <-daemonCtx.Done():
		}
	}()
	fnErr := fn()
	stopDaemon()
	tsErr := <-done
	if fnErr != nil {
		return fnErr
	}
	if errors.Is(tsErr, context.Canceled) {
		return nil
	}
	return tsErr
}
