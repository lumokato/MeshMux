package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

const (
	minMihomoSize        = 1024 * 1024
	minGeoIPSize         = 1024 * 1024
	mihomoComponentState = "state/mihomo.component.json"
)

type mihomoComponentRecord struct {
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

type launchedProcess struct {
	pid  int
	wait func() error
}

var (
	mihomoLauncher    = launchMihomoProcess
	bundledMihomoPath = config.BundledMihomoPath
	startupProbeDelay = 1200 * time.Millisecond
)

func Start(cfg *config.Config, profile string) error {
	return runManaged(context.Background(), cfg, profile, false, false, nil)
}

func Supervise(cfg *config.Config, profile string, ready func(int) error) error {
	return SuperviseContext(context.Background(), cfg, profile, ready)
}

func SuperviseContext(ctx context.Context, cfg *config.Config, profile string, ready func(int) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return runManaged(ctx, cfg, profile, true, false, ready)
}

func ServiceContext(ctx context.Context, cfg *config.Config, profile string, ready func(int) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return runManaged(ctx, cfg, profile, true, true, ready)
}

func RunContext(ctx context.Context, cfg *config.Config, profile string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return runManaged(ctx, cfg, profile, true, true, nil)
}

func runManaged(ctx context.Context, cfg *config.Config, profile string, supervise, failOnExit bool, ready func(int) error) error {
	if profile == "" {
		return errors.New("profile path is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.TUN.Enabled && !canStartTUN() {
		return errors.New(tunUnavailableMessage())
	}
	if err := CleanupResidual(cfg); err != nil {
		return err
	}
	mihomo, err := prepareMihomo(cfg, true)
	if err != nil {
		return err
	}
	if err := ensureGeoIP(); err != nil {
		return err
	}
	if err := ensureDashboard(cfg.Paths.Dashboard); err != nil {
		return err
	}
	if err := os.MkdirAll("state", 0755); err != nil {
		return err
	}
	out, err := newSanitizedRotatingLog(filepath.Join("logs", "mihomo.out.log"), mihomoLogPolicy)
	if err != nil {
		return err
	}
	errLog, err := newSanitizedRotatingLog(filepath.Join("logs", "mihomo.err.log"), mihomoLogPolicy)
	if err != nil {
		_ = out.Close()
		return err
	}
	process, err := mihomoLauncher(mihomo, profile, out, errLog)
	if err != nil {
		_ = out.Close()
		_ = errLog.Close()
		return err
	}
	if err := writePID(process.pid); err != nil {
		_ = processOS.kill(process.pid)
		_ = process.wait()
		_ = out.Close()
		_ = errLog.Close()
		return fmt.Errorf("write mihomo PID: %w", err)
	}
	done := make(chan error, 1)
	go func(pid int) {
		err := process.wait()
		_ = out.Close()
		_ = errLog.Close()
		done <- err
	}(process.pid)
	startupTimer := time.NewTimer(startupProbeDelay)
	defer startupTimer.Stop()
	select {
	case err := <-done:
		if current, readErr := readPID(); readErr == nil && current == process.pid {
			_ = clearPID()
		}
		if err == nil {
			err = errors.New("process exited")
		}
		return fmt.Errorf("mihomo exited immediately: %w%s", err, recentCoreLog())
	case <-ctx.Done():
		return stopAfterCancellation(cfg, done, ctx.Err())
	case <-startupTimer.C:
	}
	if err := postStartNetwork(cfg); err != nil {
		appendRunnerLog("网络后处理失败: %v", err)
	}
	if ready != nil {
		pid := process.pid
		if current, ok := PID(cfg); ok {
			pid = current
		}
		if err := ready(pid); err != nil {
			_ = Stop(cfg)
			return fmt.Errorf("report supervised start: %w", err)
		}
	}
	if supervise {
		select {
		case err := <-done:
			if _, ok := PID(cfg); !ok {
				_ = clearPID()
			}
			if failOnExit {
				if err == nil {
					err = errors.New("process exited")
				}
				return fmt.Errorf("mihomo exited: %w%s", err, recentCoreLog())
			}
		case <-ctx.Done():
			return stopAfterCancellation(cfg, done, ctx.Err())
		}
	}
	return nil
}

func stopAfterCancellation(cfg *config.Config, done <-chan error, cause error) error {
	if err := Stop(cfg); err != nil {
		return fmt.Errorf("stop mihomo after cancellation: %w", err)
	}
	select {
	case <-done:
	case <-time.After(stopProcessTimeout):
	}
	if _, ok := PID(cfg); !ok {
		_ = clearPID()
	}
	if errors.Is(cause, context.Canceled) {
		return nil
	}
	return cause
}

func TestConfig(cfg *config.Config, profile string) error {
	if profile == "" {
		return errors.New("profile path is required")
	}
	mihomo, err := prepareMihomo(cfg, false)
	if err != nil {
		return err
	}
	if err := ensureGeoIP(); err != nil {
		return err
	}
	if err := ensureDashboard(cfg.Paths.Dashboard); err != nil {
		return err
	}
	cmd := hiddenCommand(mihomo, "-t", "-d", ".", "-f", profile)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(redactSensitiveText(string(out)))
	if text != "" {
		appendRunnerLog("配置测试: %s", text)
	}
	if err != nil {
		if text != "" {
			return fmt.Errorf("mihomo 配置测试失败: %w: %s", err, text)
		}
		return fmt.Errorf("mihomo 配置测试失败: %w", err)
	}
	return nil
}

func CleanupResidual(cfg *config.Config) error {
	if err := Stop(cfg); err != nil {
		return err
	}
	if err := CleanupLogs(); err != nil {
		appendRunnerLog("日志清理失败: %v", err)
	}
	return nil
}

func Stop(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	ports := mihomoPorts(cfg)
	deadline := time.Now().Add(stopProcessTimeout)
	var lastKillErr error
	var quietSince time.Time
	for {
		pids, err := managedPIDs(cfg)
		if err != nil {
			return fmt.Errorf("发现受管 mihomo 进程失败: %w", err)
		}
		for _, pid := range pids {
			if err := processOS.kill(pid); err != nil {
				lastKillErr = err
			}
		}
		if len(pids) == 0 {
			if err := waitPortsFree(ports, 0); err == nil {
				if quietSince.IsZero() {
					quietSince = time.Now()
				}
				if time.Since(quietSince) >= stopQuietPeriod {
					_ = clearPID()
					return nil
				}
			} else {
				quietSince = time.Time{}
			}
		} else {
			quietSince = time.Time{}
		}
		if time.Now().After(deadline) {
			remaining, err := managedPIDs(cfg)
			if err != nil {
				return fmt.Errorf("确认受管 mihomo 进程停止失败: %w", err)
			}
			if len(remaining) > 0 {
				if lastKillErr != nil {
					return lastKillErr
				}
				var values []string
				for _, pid := range remaining {
					values = append(values, strconv.Itoa(pid))
				}
				return fmt.Errorf("mihomo 进程仍在运行: %s", strings.Join(values, ", "))
			}
			if err := waitPortsFree(ports, 0); err != nil {
				return err
			}
			_ = clearPID()
			return nil
		}
		time.Sleep(stopPollInterval)
	}
}

func Restart(cfg *config.Config, profile string) error {
	return Start(cfg, profile)
}

func IsRunning(cfg *config.Config) bool {
	_, ok := PID(cfg)
	return ok
}

func Status(cfg *config.Config) string {
	var b strings.Builder
	b.WriteString("MeshMux 状态\n")
	if pid, ok := PID(cfg); ok {
		fmt.Fprintf(&b, "mihomo PID: %d\n", pid)
	} else {
		b.WriteString("mihomo PID: 未运行\n")
	}
	fmt.Fprintf(&b, "混合代理: 127.0.0.1:%d\n", cfg.Ports.Mixed)
	fmt.Fprintf(&b, "MetaCubeXD: http://%s/ui\n", cfg.Ports.Controller)
	if err := controllerPing(cfg); err == nil {
		b.WriteString("控制器: 可连接\n")
	} else {
		fmt.Fprintf(&b, "控制器: %v\n", err)
	}
	return b.String()
}

func PID(cfg *config.Config) (int, bool) {
	return findManagedPID(cfg)
}

func CanStartTUN() bool {
	return canStartTUN()
}

func ControllerReady(cfg *config.Config) bool {
	return controllerPing(cfg) == nil
}

func Dashboard(cfg *config.Config) error {
	return OpenURL("http://" + cfg.Ports.Controller + "/ui")
}

func OpenURL(rawURL string) error {
	switch runtime.GOOS {
	case "windows":
		return hiddenCommand("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
	case "darwin":
		return hiddenCommand("open", rawURL).Start()
	default:
		return hiddenCommand("xdg-open", rawURL).Start()
	}
}

func controllerPing(cfg *config.Config) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + cfg.Ports.Controller + "/configs")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New(resp.Status)
	}
	return nil
}

func readPID() (int, error) {
	data, err := os.ReadFile(pidFilePath)
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return 0, err
	}
	return pid, nil
}

func recentCoreLog() string {
	paths := []string{
		filepath.Join("logs", "mihomo.err.log"),
		filepath.Join("logs", "mihomo.out.log"),
	}
	var chunks []string
	for _, path := range paths {
		if text := recentLogText(path); text != "" {
			chunks = append(chunks, filepath.Base(path)+": "+text)
		}
	}
	if len(chunks) == 0 {
		return ""
	}
	return ":\n" + strings.Join(chunks, "\n")
}

func recentLogText(path string) string {
	data, err := readFileTail(path, 2048)
	if err != nil || len(data) == 0 {
		return ""
	}
	text := strings.TrimSpace(redactSensitiveText(string(data)))
	if text == "" {
		return ""
	}
	return text
}

func appendRunnerLog(format string, args ...any) {
	runnerLogMu.Lock()
	defer runnerLogMu.Unlock()
	message := strings.TrimSpace(fmt.Sprintf(format, args...))
	if message == "" {
		return
	}
	file, err := newSanitizedRotatingLog(filepath.Join("logs", "meshmux.log"), runnerLogPolicy)
	if err != nil {
		return
	}
	defer file.Close()
	now := time.Now().Format(time.RFC3339)
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_, _ = fmt.Fprintf(file, "%s %s\n", now, line)
	}
}

func copyBundledMihomo(target string) error {
	return copyBundledFile(bundledMihomoPath(), target)
}

func prepareMihomo(cfg *config.Config, syncBundled bool) (string, error) {
	if cfg == nil {
		return "", errors.New("config is required")
	}
	target := strings.TrimSpace(cfg.Components.Mihomo.Path)
	if target == "" {
		target = config.DefaultMihomoPath()
	}
	bundled := bundledMihomoPath()
	if !syncBundled && shouldManageBundledMihomo(cfg, target) && fileLooksUsable(bundled, minMihomoSize) {
		return bundled, nil
	}
	if syncBundled && shouldManageBundledMihomo(cfg, target) && fileLooksUsable(bundled, minMihomoSize) {
		if err := syncBundledMihomo(bundled, target); err != nil {
			return "", err
		}
	}
	if !fileLooksUsable(target, minMihomoSize) {
		if copyErr := copyBundledMihomo(target); copyErr != nil {
			return "", fmt.Errorf("mihomo not found at %s and bundled copy is unavailable: %w", target, copyErr)
		}
	}
	return target, nil
}

func shouldManageBundledMihomo(cfg *config.Config, target string) bool {
	if !isDefaultMihomoPath(target) {
		return false
	}
	component := cfg.Components.Mihomo
	return (component.Repo == "" || component.Repo == config.DefaultMihomoRepo) &&
		(component.AssetPattern == "" || component.AssetPattern == config.DefaultMihomoAssetPatternFor(runtime.GOOS))
}

func syncBundledMihomo(bundled, target string) error {
	bundledHash, err := fileSHA256(bundled)
	if err != nil {
		return fmt.Errorf("hash bundled mihomo: %w", err)
	}
	targetHash, targetErr := fileSHA256(target)
	if targetErr == nil && bundledHash == targetHash {
		return writeMihomoComponentRecord("bundle", targetHash)
	}
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return fmt.Errorf("hash current mihomo: %w", targetErr)
	}
	record, recordErr := readMihomoComponentRecord()
	if recordErr == nil && targetErr == nil && strings.EqualFold(record.SHA256, hex.EncodeToString(targetHash[:])) {
		if record.Source != "bundle" {
			return nil
		}
	} else if recordErr == nil && targetErr == nil {
		if err := writeMihomoComponentRecord("external", targetHash); err != nil {
			return err
		}
		return nil
	} else if recordErr != nil && !os.IsNotExist(recordErr) {
		return fmt.Errorf("read mihomo component state: %w", recordErr)
	}
	if err := copyBundledFile(bundled, target); err != nil {
		return fmt.Errorf("update bundled mihomo: %w", err)
	}
	if err := writeMihomoComponentRecord("bundle", bundledHash); err != nil {
		return err
	}
	appendRunnerLog("已同步安装包内置 mihomo 到 %s", target)
	return nil
}

func MarkMihomoDownloaded(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	target := strings.TrimSpace(cfg.Components.Mihomo.Path)
	if target == "" {
		target = config.DefaultMihomoPath()
	}
	if !isDefaultMihomoPath(target) {
		return nil
	}
	hash, err := fileSHA256(target)
	if err != nil {
		return err
	}
	return writeMihomoComponentRecord("download", hash)
}

func readMihomoComponentRecord() (mihomoComponentRecord, error) {
	data, err := os.ReadFile(mihomoComponentState)
	if err != nil {
		return mihomoComponentRecord{}, err
	}
	var record mihomoComponentRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return mihomoComponentRecord{}, err
	}
	return record, nil
}

func writeMihomoComponentRecord(source string, hash [sha256.Size]byte) error {
	if err := os.MkdirAll(filepath.Dir(mihomoComponentState), 0755); err != nil {
		return err
	}
	record := mihomoComponentRecord{Source: source, SHA256: hex.EncodeToString(hash[:])}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp := mihomoComponentState + ".part"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Remove(mihomoComponentState); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, mihomoComponentState)
}

func isDefaultMihomoPath(path string) bool {
	defaultPath := filepath.Clean(config.DefaultMihomoPath())
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(cleaned, defaultPath)
	}
	return cleaned == defaultPath
}

func sameFileContents(left, right string) (bool, error) {
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false, err
	}
	if leftInfo.IsDir() || rightInfo.IsDir() || leftInfo.Size() != rightInfo.Size() {
		return false, nil
	}
	leftHash, err := fileSHA256(left)
	if err != nil {
		return false, err
	}
	rightHash, err := fileSHA256(right)
	if err != nil {
		return false, err
	}
	return leftHash == rightHash, nil
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return zero, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return zero, err
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func ensureGeoIP() error {
	target := "geoip.metadb"
	if fileLooksUsable(target, minGeoIPSize) {
		return nil
	}
	if err := copyBundledFile(config.BundledGeoIPPath(), target); err != nil {
		return fmt.Errorf("prepare GeoIP database: %w", err)
	}
	return nil
}

func ensureDashboard(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(path, "index.html")); err == nil {
		return nil
	}
	bundled := config.BundledDashboardPath()
	if bundled == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(bundled, "index.html")); err != nil {
		return nil
	}
	return copyBundledDir(bundled, path)
}

func fileLooksUsable(path string, minSize int64) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() >= minSize
}

func copyBundledFile(bundled, target string) error {
	if bundled == "" {
		return errors.New("bundled file path is empty")
	}
	targetAbs, _ := filepath.Abs(target)
	bundledAbs, _ := filepath.Abs(bundled)
	if filepath.Clean(targetAbs) == filepath.Clean(bundledAbs) {
		if _, err := os.Stat(bundled); err != nil {
			return err
		}
		return nil
	}
	info, err := os.Stat(bundled)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	in, err := os.Open(bundled)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := target + ".part"
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0644
	}
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, target)
}

func copyBundledDir(bundled, target string) error {
	bundledAbs, _ := filepath.Abs(bundled)
	targetAbs, _ := filepath.Abs(target)
	if filepath.Clean(bundledAbs) == filepath.Clean(targetAbs) {
		return nil
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	return filepath.WalkDir(bundled, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bundled, path)
		if err != nil || rel == "." {
			return err
		}
		dst := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		return copyBundledFile(path, dst)
	})
}

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	hideWindow(cmd)
	return cmd
}

func launchMihomoProcess(mihomo, profile string, stdout, stderr io.Writer) (launchedProcess, error) {
	cmd := hiddenCommand(mihomo, "-d", ".", "-f", profile)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return launchedProcess{}, err
	}
	return launchedProcess{pid: cmd.Process.Pid, wait: cmd.Wait}, nil
}

func mihomoPorts(cfg *config.Config) []int {
	return discoveryPorts(cfg)
}

func waitPortsFree(ports []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var busy []string
		for _, port := range ports {
			if port <= 0 {
				continue
			}
			if tcpPortReady(port) {
				busy = append(busy, strconv.Itoa(port))
			}
		}
		if len(busy) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("mihomo 端口仍被占用: %s", strings.Join(busy, ", "))
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func tcpPortReady(port int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
