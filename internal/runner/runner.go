package runner

import (
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
	minMihomoSize = 1024 * 1024
	minGeoIPSize  = 1024 * 1024
)

func Start(cfg *config.Config, profile string) error {
	if profile == "" {
		return errors.New("profile path is required")
	}
	if cfg.TUN.Enabled && !canStartTUN() {
		return errors.New("TUN 模式需要管理员权限；请用管理员身份运行 MeshMux，或关闭 TUN 改用系统代理模式")
	}
	if err := CleanupResidual(cfg); err != nil {
		return err
	}
	mihomo := cfg.Components.Mihomo.Path
	if mihomo == "" {
		mihomo = filepath.Join("bin", "mihomo.exe")
	}
	if !fileLooksUsable(mihomo, minMihomoSize) {
		if copyErr := copyBundledMihomo(mihomo); copyErr != nil {
			return fmt.Errorf("mihomo not found at %s and bundled copy is unavailable: %w", mihomo, copyErr)
		}
	}
	if err := ensureGeoIP(); err != nil {
		return err
	}
	if err := ensureDashboard(cfg.Paths.Dashboard); err != nil {
		return err
	}
	if err := os.MkdirAll("logs", 0755); err != nil {
		return err
	}
	if err := os.MkdirAll("state", 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join("logs", "mihomo.out.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	errLog, err := os.OpenFile(filepath.Join("logs", "mihomo.err.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		_ = out.Close()
		return err
	}
	cmd := hiddenCommand(mihomo, "-d", ".", "-f", profile)
	cmd.Stdout = out
	cmd.Stderr = errLog
	if err := cmd.Start(); err != nil {
		_ = out.Close()
		_ = errLog.Close()
		return err
	}
	if err := os.WriteFile(filepath.Join("state", "mihomo.pid"), []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0644); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func(pid int) {
		err := cmd.Wait()
		_ = out.Close()
		_ = errLog.Close()
		if current, readErr := readPID(); readErr == nil && current == pid {
			_ = os.Remove(filepath.Join("state", "mihomo.pid"))
		}
		done <- err
	}(cmd.Process.Pid)
	select {
	case err := <-done:
		if err == nil {
			err = errors.New("process exited")
		}
		return fmt.Errorf("mihomo exited immediately: %w%s", err, recentCoreLog())
	case <-time.After(1200 * time.Millisecond):
	}
	if err := postStartNetwork(cfg); err != nil {
		appendRunnerLog("网络后处理失败: %v", err)
	}
	return nil
}

func TestConfig(cfg *config.Config, profile string) error {
	if profile == "" {
		return errors.New("profile path is required")
	}
	mihomo := cfg.Components.Mihomo.Path
	if mihomo == "" {
		mihomo = filepath.Join("bin", "mihomo.exe")
	}
	if !fileLooksUsable(mihomo, minMihomoSize) {
		if copyErr := copyBundledMihomo(mihomo); copyErr != nil {
			return fmt.Errorf("mihomo not found at %s and bundled copy is unavailable: %w", mihomo, copyErr)
		}
	}
	if err := ensureGeoIP(); err != nil {
		return err
	}
	if err := ensureDashboard(cfg.Paths.Dashboard); err != nil {
		return err
	}
	cmd := hiddenCommand(mihomo, "-t", "-d", ".", "-f", profile)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
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
	_ = Stop()
	ports := mihomoPorts(cfg)
	if err := stopMihomoOnPorts(ports); err != nil {
		return err
	}
	return waitPortsFree(ports, 5*time.Second)
}

func Stop() error {
	pid, err := readPID()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Kill(); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join("state", "mihomo.pid"))
	return nil
}

func IsRunning() bool {
	pid, err := readPID()
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		cmd := hiddenCommand("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
		out, err := cmd.Output()
		return err == nil && strings.Contains(string(out), fmt.Sprintf(`"%d"`, pid))
	}
	_, err = os.FindProcess(pid)
	return err == nil
}

func Status(cfg *config.Config) string {
	var b strings.Builder
	b.WriteString("MeshMux 状态\n")
	if pid, err := readPID(); err == nil {
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

func PID() (int, bool) {
	pid, err := readPID()
	return pid, err == nil && pid > 0
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
		return fmt.Errorf(resp.Status)
	}
	return nil
}

func readPID() (int, error) {
	data, err := os.ReadFile(filepath.Join("state", "mihomo.pid"))
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
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > 2048 {
		data = data[len(data)-2048:]
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	return text
}

func appendRunnerLog(format string, args ...any) {
	message := strings.TrimSpace(fmt.Sprintf(format, args...))
	if message == "" {
		return
	}
	_ = os.MkdirAll("logs", 0755)
	file, err := os.OpenFile(filepath.Join("logs", "meshmux.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
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
	return copyBundledFile(config.BundledMihomoPath(), target)
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
	if _, err := os.Stat(bundled); err != nil {
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
	out, err := os.Create(tmp)
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

func mihomoPorts(cfg *config.Config) []int {
	ports := []int{cfg.Ports.Mixed}
	if _, portText, ok := strings.Cut(cfg.Ports.Controller, ":"); ok {
		if port, err := strconv.Atoi(portText); err == nil && port > 0 {
			ports = append(ports, port)
		}
	}
	return ports
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
