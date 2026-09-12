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
