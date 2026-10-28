//go:build darwin

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/meshmux/meshmux/internal/config"
)

// macOS runs the mihomo core as a root LaunchDaemon so TUN exists before any
// user logs in. The daemon reads only root-owned files under coreDaemonRoot;
// the member's Application Support directory is read once by the explicit,
// administrator-authorized install/update step and never by the running job.
const (
	coreDaemonLabel      = "com.meshmux.core"
	coreDaemonRoot       = "/Library/Application Support/MeshMux/core"
	coreDaemonPlistPath  = "/Library/LaunchDaemons/com.meshmux.core.plist"
	coreDaemonConfigPath = coreDaemonRoot + "/meshmux.local.json"
	coreDaemonProfile    = coreDaemonRoot + "/profiles/darwin.yaml"
	coreDaemonErrorDir   = "/Library/Logs/MeshMux"
	coreDaemonErrorPath  = coreDaemonErrorDir + "/core.err.log"
	coreDaemonTargetName = "darwin"
)

// coreDaemonPaths pins the exact inputs of one install/update run. The values
// become shell literals in the generated script; callers never inject user
// input into the script.
type coreDaemonPaths struct {
	CLI           string
	UserData      string
	UserConfig    string
	UserProviders string
}

// CoreDaemonInstalled reports whether a root-owned core LaunchDaemon plist is
// registered. Reading its content is not enough: a member-writable file is
// treated as absent.
func CoreDaemonInstalled() bool {
	info, err := os.Lstat(coreDaemonPlistPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if info.Mode().Perm()&022 != 0 {
		return false
	}
	return fileOwnerUID(info) == 0
}

func fileOwnerUID(info os.FileInfo) uint32 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid
	}
	return ^uint32(0)
}

var coreDaemonPIDPattern = regexp.MustCompile(`(?m)^\s*pid = (\d+)\s*$`)

// parseCoreDaemonLaunchctl extracts the job state and pid from
// `launchctl print system/com.meshmux.core`. It is separated for tests.
func parseCoreDaemonLaunchctl(output string) (state string, pid int) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "state = ") {
			// The first state is the job; nested endpoints also report
			// "state = active" later in the same output.
			state = strings.TrimSpace(strings.TrimPrefix(trimmed, "state = "))
			break
		}
	}
	if match := coreDaemonPIDPattern.FindStringSubmatch(output); match != nil {
		if value, err := strconv.Atoi(match[1]); err == nil {
			pid = value
		}
	}
	return state, pid
}

func coreDaemonLaunchctlPrint() (string, error) {
	out, err := hiddenCommand("launchctl", "print", "system/"+coreDaemonLabel).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// CoreDaemonPID returns the launchd-reported supervisor pid. The pid is only
// meaningful while the job is running; callers must not treat a stale launchd
// record as a live core.
func CoreDaemonPID() (int, bool) {
	out, err := coreDaemonLaunchctlPrint()
	if err != nil {
		return 0, false
	}
	state, pid := parseCoreDaemonLaunchctl(out)
	if state != "running" || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// CoreDaemonStatus is a read-only, non-elevating report. It deliberately shows
// both the daemon job and the protected copy so a stale file cannot pass as an
// installed service.
func CoreDaemonStatus() string {
	var b strings.Builder
	b.WriteString("MeshMux 系统核心守护\n")
	fmt.Fprintf(&b, "label: %s\n", coreDaemonLabel)
	fmt.Fprintf(&b, "plist: %s\n", coreDaemonPlistPath)
	fmt.Fprintf(&b, "installed: %t\n", CoreDaemonInstalled())
	if info, err := os.Lstat(coreDaemonPlistPath); err == nil {
		fmt.Fprintf(&b, "plist-owner: %d mode: %04o\n", fileOwnerUID(info), info.Mode().Perm())
	}
	if installed := CoreDaemonInstalled(); installed {
		if out, err := coreDaemonLaunchctlPrint(); err == nil {
			state, pid := parseCoreDaemonLaunchctl(out)
			if state == "" {
				state = "unknown"
			}
			fmt.Fprintf(&b, "launchd-state: %s\n", state)
			if pid > 0 {
				fmt.Fprintf(&b, "supervisor-pid: %d\n", pid)
			}
			if pid, ok := CoreDaemonPID(); ok {
				fmt.Fprintf(&b, "running: true (supervisor %d)\n", pid)
			} else {
				b.WriteString("running: false\n")
			}
		} else {
			b.WriteString("launchd-state: not loaded\n")
			b.WriteString("running: false\n")
		}
		for _, path := range []string{coreDaemonConfigPath, coreDaemonProfile} {
			info, err := os.Lstat(path)
			switch {
			case err == nil && info.Mode().IsRegular():
				fmt.Fprintf(&b, "file: %s owner=%d mode=%04o\n", path, fileOwnerUID(info), info.Mode().Perm())
			case os.IsPermission(err):
				fmt.Fprintf(&b, "file: %s protected (目录 0700，仅 root 可读)\n", path)
			default:
				fmt.Fprintf(&b, "file: %s missing\n", path)
			}
		}
	}
	b.WriteString("core-binary: " + coreDaemonRoot + "/bin/mihomo\n")
	return b.String()
}

// CoreDaemonPlan returns the exact administrator script for an action without
// performing elevation. Used by the CLI, tray, tests and manual recovery.
func CoreDaemonPlan(action string) (string, error) {
	switch action {
	case "install":
		paths, err := coreDaemonInstallPaths()
		if err != nil {
			return "", err
		}
		return coreDaemonInstallScript(paths), nil
	case "sync":
		paths, err := coreDaemonInstallPaths()
		if err != nil {
			return "", err
		}
		return coreDaemonSyncScript(paths), nil
	case "uninstall":
		return coreDaemonUninstallScript(), nil
	case "start", "stop", "restart":
		return coreDaemonControlScript(action), nil
	default:
		return "", fmt.Errorf("unsupported core daemon action %q", action)
	}
}

// CoreDaemonService performs one explicit user-requested LaunchDaemon action.
// Every action that changes system state asks for administrator approval; only
// status reads never elevate.
func CoreDaemonService(ctx context.Context, action string) (string, error) {
	switch action {
	case "status":
		return CoreDaemonStatus(), nil
	case "install", "sync", "uninstall", "start", "stop", "restart":
		if action != "install" && !CoreDaemonInstalled() {
			return "", errors.New("尚未安装 MeshMux 系统核心守护；请先安装")
		}
		script, err := CoreDaemonPlan(action)
		if err != nil {
			return "", err
		}
		return macElevate(ctx, script)
	default:
		return "", fmt.Errorf("不支持的 MeshMux 核心守护操作 %q", action)
	}
}

// coreDaemonInstallUser resolves the member whose Application Support
// configuration is the source of the protected copy. Running as root via sudo
// must not silently read /var/root.
func coreDaemonInstallUser() (*user.User, error) {
	if os.Geteuid() == 0 {
		name := strings.TrimSpace(os.Getenv("SUDO_USER"))
		if name == "" || name == "root" {
			return nil, errors.New("以 root 直接运行无法确定成员配置目录；请从托盘安装，或用 sudo -u <user> 运行")
		}
		return user.Lookup(name)
	}
	return user.Current()
}

func coreDaemonInstallPaths() (coreDaemonPaths, error) {
	cli, err := os.Executable()
	if err != nil {
		return coreDaemonPaths{}, fmt.Errorf("定位 MeshMux 可执行文件失败: %w", err)
	}
	cli = filepath.Clean(cli)
	if !strings.HasPrefix(cli, "/Applications/") || !strings.Contains(cli, ".app/Contents/MacOS/") {
		return coreDaemonPaths{}, errors.New("只允许从 /Applications 下的 MeshMux.app 安装系统守护，避免 root 运行开发目录里的二进制")
	}
	current, err := coreDaemonInstallUser()
	if err != nil {
		return coreDaemonPaths{}, err
	}
	if strings.TrimSpace(current.HomeDir) == "" {
		return coreDaemonPaths{}, errors.New("无法确定成员主目录")
	}
	userData := filepath.Join(current.HomeDir, "Library", "Application Support", "MeshMux")
	userConfig := filepath.Join(userData, config.DefaultConfigPath)
	if info, err := os.Lstat(userConfig); err != nil || !info.Mode().IsRegular() {
		return coreDaemonPaths{}, fmt.Errorf("成员配置不存在或不是普通文件: %s", userConfig)
	}
	if err := auditCoreDaemonConfig(userConfig); err != nil {
		return coreDaemonPaths{}, err
	}
	return coreDaemonPaths{
		CLI:           cli,
		UserData:      userData,
		UserConfig:    userConfig,
		UserProviders: filepath.Join(userData, "providers"),
	}, nil
}

// auditCoreDaemonConfig refuses absolute or escaping paths. A root daemon that
// follows a member-writable path would be a privilege boundary, not a feature.
func auditCoreDaemonConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取成员配置失败: %w", err)
	}
	var raw config.Config
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("成员配置不是有效 JSON: %w", err)
	}
	// 校验存储值，再校验默认值：两者都会参与 root 运行时解析。
	if err := auditCoreDaemonPathFields(&raw); err != nil {
		return err
	}
	cfg := raw
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("成员配置校验失败: %w", err)
	}
	targetFound := false
	for _, target := range cfg.Targets {
		if target.Name == coreDaemonTargetName {
			targetFound = true
		}
	}
	if !targetFound {
		return fmt.Errorf("成员配置缺少 %q 目标，无法生成守护进程 profile", coreDaemonTargetName)
	}
	return auditCoreDaemonPathFields(&cfg)
}

func auditCoreDaemonPathFields(cfg *config.Config) error {
	type pathCheck struct {
		name  string
		value string
	}
	checks := []pathCheck{
		{"paths.runtime", cfg.Paths.Runtime},
		{"paths.dashboard", cfg.Paths.Dashboard},
		{"components.mihomo.path", cfg.Components.Mihomo.Path},
		{"components.dashboard.path", cfg.Components.Dashboard.Path},
		{"components.tailscale.path", cfg.Components.Tailscale.Path},
		{"tailscale.authKeyFile", cfg.Tailscale.AuthKeyFile},
	}
	for index, provider := range cfg.Providers {
		checks = append(checks, pathCheck{fmt.Sprintf("providers[%d].path", index), provider.Path})
	}
	for index, wg := range cfg.WireGuard.Configs {
		checks = append(checks, pathCheck{fmt.Sprintf("wireguard.configs[%d]", index), wg})
	}
	for index, target := range cfg.Targets {
		checks = append(checks, pathCheck{fmt.Sprintf("targets[%d].output", index), target.Output})
	}
	for index, publish := range cfg.Publish {
		checks = append(checks, pathCheck{fmt.Sprintf("publish[%d].input", index), publish.Input})
	}
	for _, check := range checks {
		value := strings.TrimSpace(check.value)
		if value == "" {
			continue
		}
		if filepath.IsAbs(value) {
			return fmt.Errorf("%s 必须是相对路径：root 守护进程不读取成员目录中的绝对路径 (%s)", check.name, value)
		}
		cleaned := filepath.Clean(value)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s 不能跳出守护进程运行目录 (%s)", check.name, value)
		}
	}
	return nil
}

func coreDaemonPlistXML(cli string) string {
	escape := func(value string) string {
		replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
		return replacer.Replace(value)
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + coreDaemonLabel + `</string>
<key>ProgramArguments</key><array>
<string>` + escape(cli) + `</string>
<string>_supervise</string>
<string>-config</string>
<string>` + escape(coreDaemonConfigPath) + `</string>
<string>-profile</string>
<string>` + escape(coreDaemonProfile) + `</string>
</array>
<key>WorkingDirectory</key><string>` + escape(coreDaemonRoot) + `</string>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>10</integer>
<key>ExitTimeOut</key><integer>20</integer>
<key>Umask</key><integer>63</integer>
<key>EnvironmentVariables</key><dict>
<key>PATH</key><string>/usr/bin:/bin:/usr/sbin:/sbin</string>
</dict>
<key>StandardOutPath</key><string>/dev/null</string>
<key>StandardErrorPath</key><string>` + escape(coreDaemonErrorPath) + `</string>
</dict></plist>
`
}

// coreDaemonSyncSteps stages the member configuration under root ownership and
// regenerates the profile from that copy. Both install and update use it so
// there is exactly one writer of the protected runtime inputs.
func coreDaemonSyncSteps(paths coreDaemonPaths) string {
	return `# 成员目录只在这个已授权的安装/更新步骤里读取一次。
user_data=` + shellQuote(paths.UserData) + `
user_config=` + shellQuote(paths.UserConfig) + `
user_providers=` + shellQuote(paths.UserProviders) + `
test -d "$user_data" && test ! -L "$user_data" || exit 71
test -f "$user_config" && test ! -L "$user_config" || exit 71

stage_config=$(/usr/bin/mktemp "$root/.meshmux.local.XXXXXX")
/bin/cat "$user_config" > "$stage_config"
/usr/sbin/chown root:wheel "$stage_config"
/bin/chmod 600 "$stage_config"
/bin/mv -f "$stage_config" "$root/meshmux.local.json"

/bin/rm -rf "$root/providers.next"
/bin/mkdir -m 700 "$root/providers.next"
if test -d "$user_providers" && test ! -L "$user_providers"; then
 for f in "$user_providers"/*; do
  test -e "$f" || continue
  test -f "$f" && test ! -L "$f" || exit 74
  name=$(/usr/bin/basename "$f")
  case "$name" in .*|*/*) exit 74;; esac
  /bin/cp -p "$f" "$root/providers.next/$name"
 done
fi
/usr/sbin/chown -R root:wheel "$root/providers.next"
/bin/chmod -R go-rwx "$root/providers.next"
/bin/rm -rf "$root/providers.prev"
if test -d "$root/providers"; then /bin/mv "$root/providers" "$root/providers.prev"; fi
/bin/mv "$root/providers.next" "$root/providers"
/bin/rm -rf "$root/providers.prev"

# profile 必须由受保护副本生成，不复制成员目录里的 profile。
cd "$root"
` + shellQuote(paths.CLI) + ` generate -config "$root/meshmux.local.json" ` + coreDaemonTargetName + ` >/dev/null
test -f "$root/profiles/darwin.yaml" && test ! -L "$root/profiles/darwin.yaml" || exit 78
/usr/sbin/chown root:wheel "$root/profiles/darwin.yaml"
/bin/chmod 600 "$root/profiles/darwin.yaml"
`
}

func coreDaemonProtectedRootSteps() string {
	return `root=` + shellQuote(coreDaemonRoot) + `
plist=` + shellQuote(coreDaemonPlistPath) + `
if test ! -e "$root"; then /bin/mkdir "$root"; fi
test -d "$root" && test ! -L "$root" || exit 71
/usr/sbin/chown root:wheel "$root"
/bin/chmod 755 "$root"
for sub in profiles state logs bin providers; do
 case "$sub" in bin) mode=755;; *) mode=700;; esac
 if test ! -e "$root/$sub"; then /bin/mkdir -m "$mode" "$root/$sub"; /usr/sbin/chown root:wheel "$root/$sub"; fi
 test -d "$root/$sub" && test ! -L "$root/$sub" || exit 71
 test "$(/usr/bin/stat -f '%u' "$root/$sub")" = 0 || exit 72
 test -z "$(/usr/bin/find "$root/$sub" -prune -perm +022 -print)" || exit 73
done
`
}

func coreDaemonInstallScript(paths coreDaemonPaths) string {
	return "set -eu\numask 077\n" + macDirectoryCheck + `
for p in /Library '/Library/Application Support' '/Library/Application Support/MeshMux' /Library/LaunchDaemons; do check_dir "$p"; done
` + coreDaemonProtectedRootSteps() + `
cli=` + shellQuote(paths.CLI) + `
test -f "$cli" && test ! -L "$cli" || exit 74
test "$(/usr/bin/stat -f '%u' "$cli")" = 0 || exit 75
test -z "$(/usr/bin/find "$cli" -prune -perm +022 -print)" || exit 76

if test ! -e ` + shellQuote(coreDaemonErrorDir) + `; then /bin/mkdir ` + shellQuote(coreDaemonErrorDir) + `; /usr/sbin/chown root:wheel ` + shellQuote(coreDaemonErrorDir) + `; fi
check_dir ` + shellQuote(coreDaemonErrorDir) + `

/bin/mkdir -m 700 "$root/install.lock" || { echo '另一个安装正在进行；未修改系统'; exit 81; }
backup="$root/recovery-$(/bin/date +%Y%m%d-%H%M%S)"
/bin/mkdir -m 700 "$backup"
was_running=0
if /bin/launchctl print system/` + coreDaemonLabel + ` >/dev/null 2>&1; then was_running=1; fi
if test -e "$plist" || test -L "$plist"; then
 test ! -L "$plist" && test "$(/usr/bin/stat -f '%u' "$plist")" = 0 || exit 74
 program=$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$plist" 2>/dev/null || true)
 case "$program" in */MeshMux.app/Contents/MacOS/meshmux) ;; *) exit 77;; esac
 /bin/cp -p "$plist" "$backup/service.plist"
fi
if test -f "$root/meshmux.local.json"; then /bin/cp -p "$root/meshmux.local.json" "$backup/meshmux.local.json"; fi
if test -d "$root/providers"; then /bin/cp -R "$root/providers" "$backup/providers"; fi

rollback() {
 code=$?
 /bin/rmdir "$root/install.lock" 2>/dev/null || true
 if test "$code" != 0; then
  /bin/launchctl bootout system/` + coreDaemonLabel + ` >/dev/null 2>&1 || true
  /bin/launchctl disable system/` + coreDaemonLabel + ` >/dev/null 2>&1 || true
  if test -f "$backup/meshmux.local.json"; then /bin/cp -p "$backup/meshmux.local.json" "$root/meshmux.local.json"; else /bin/rm -f "$root/meshmux.local.json"; fi
  if test -d "$backup/providers"; then /bin/rm -rf "$root/providers"; /bin/cp -R "$backup/providers" "$root/providers"; fi
  if test -f "$backup/service.plist"; then
   /bin/cp -p "$backup/service.plist" "$plist"
   /bin/launchctl enable system/` + coreDaemonLabel + ` >/dev/null 2>&1 || true
   if test "$was_running" = 1; then /bin/launchctl bootstrap system "$plist" >/dev/null 2>&1 || true; fi
  else
   /bin/rm -f "$plist"
  fi
  /bin/rm -rf "$root/providers.next"
 fi
}
trap rollback EXIT

` + coreDaemonSyncSteps(paths) + `
plist_stage=$(/usr/bin/mktemp /Library/LaunchDaemons/.meshmux.XXXXXX)
printf '%s' ` + shellQuote(coreDaemonPlistXML(paths.CLI)) + ` > "$plist_stage"
/usr/sbin/chown root:wheel "$plist_stage"
/bin/chmod 644 "$plist_stage"
/bin/mv -f "$plist_stage" "$plist"
/usr/bin/plutil -lint "$plist" >/dev/null

# 从手动授权模式切换：结束成员目录里仍由旧 supervisor 托管的核心，避免端口冲突。
legacy_pattern=` + shellQuote(regexp.QuoteMeta(filepath.Join(paths.UserData, "profiles", "darwin.yaml"))) + `
for pid in $(/usr/bin/pgrep -f "$legacy_pattern" || true); do
 case "$(/bin/ps -p "$pid" -o comm= 2>/dev/null || true)" in */mihomo|mihomo) /bin/kill -TERM "$pid" >/dev/null 2>&1 || true;; esac
done
for attempt in 1 2 3 4 5 6 7 8 9 10; do
 if ! /usr/bin/pgrep -f "$legacy_pattern" >/dev/null 2>&1; then break; fi
 /bin/sleep 1
done
for attempt in 1 2 3 4 5 6 7 8 9 10; do
 if ! /bin/launchctl print system/` + coreDaemonLabel + ` >/dev/null 2>&1; then break; fi
 /bin/launchctl bootout system/` + coreDaemonLabel + ` >/dev/null 2>&1 || true
 /bin/sleep 1
done
/bin/launchctl enable system/` + coreDaemonLabel + `
/bin/launchctl bootstrap system "$plist"
ready=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
 if /bin/launchctl print system/` + coreDaemonLabel + ` 2>/dev/null | /usr/bin/grep -q 'state = running'; then ready=1; break; fi
 /bin/sleep 1
done
test "$ready" = 1 || exit 80
/bin/rmdir "$root/install.lock"
trap - EXIT
printf 'MeshMux 系统核心守护已安装；恢复点: %s' "$backup"
`
}

func coreDaemonSyncScript(paths coreDaemonPaths) string {
	return "set -eu\numask 077\n" + macDirectoryCheck + `
for p in /Library '/Library/Application Support' '/Library/Application Support/MeshMux' /Library/LaunchDaemons; do check_dir "$p"; done
plist=` + shellQuote(coreDaemonPlistPath) + `
test -f "$plist" && test ! -L "$plist" || exit 74
test "$(/usr/bin/stat -f '%u' "$plist")" = 0 || exit 75
` + coreDaemonProtectedRootSteps() + `
/bin/mkdir -m 700 "$root/install.lock" || { echo '另一个安装正在进行；未修改系统'; exit 81; }
trap '/bin/rmdir "$root/install.lock" 2>/dev/null || true' EXIT
` + coreDaemonSyncSteps(paths) + `
/bin/launchctl kickstart -k system/` + coreDaemonLabel + `
ready=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
 if /bin/launchctl print system/` + coreDaemonLabel + ` 2>/dev/null | /usr/bin/grep -q 'state = running'; then ready=1; break; fi
 /bin/sleep 1
done
test "$ready" = 1 || exit 80
/bin/rmdir "$root/install.lock"
trap - EXIT
printf 'MeshMux 受保护配置已更新并重启核心'
`
}

func coreDaemonUninstallScript() string {
	return "set -eu\numask 077\n" + macDirectoryCheck + `
root=` + shellQuote(coreDaemonRoot) + `
plist=` + shellQuote(coreDaemonPlistPath) + `
test -d "$root" && test ! -L "$root" || exit 71
backup="$root/recovery-uninstall-$(/bin/date +%Y%m%d-%H%M%S)"
/bin/mkdir -m 700 "$backup"
if test -f "$plist"; then /bin/cp -p "$plist" "$backup/service.plist"; fi
if test -f "$root/meshmux.local.json"; then /bin/cp -p "$root/meshmux.local.json" "$backup/meshmux.local.json"; fi
/bin/launchctl disable system/` + coreDaemonLabel + ` >/dev/null 2>&1 || true
if /bin/launchctl print system/` + coreDaemonLabel + ` >/dev/null 2>&1; then
 /bin/launchctl bootout system/` + coreDaemonLabel + ` >/dev/null 2>&1 || true
 for attempt in 1 2 3 4 5 6 7 8 9 10; do
  if ! /bin/launchctl print system/` + coreDaemonLabel + ` >/dev/null 2>&1; then break; fi
  /bin/sleep 1
 done
fi
/bin/rm -f "$plist"
printf 'MeshMux 系统核心守护已卸载；恢复点: %s。手动模式：从托盘或 sudo meshmux start 启动核心' "$backup"
`
}

func coreDaemonControlScript(action string) string {
	header := "set -eu\n" + macDirectoryCheck + `
plist=` + shellQuote(coreDaemonPlistPath) + `
test -f "$plist" && test ! -L "$plist" || exit 74
test "$(/usr/bin/stat -f '%u' "$plist")" = 0 || exit 75
`
	waitRunning := `ready=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
 if /bin/launchctl print system/` + coreDaemonLabel + ` 2>/dev/null | /usr/bin/grep -q 'state = running'; then ready=1; break; fi
 /bin/sleep 1
done
test "$ready" = 1 || exit 80
`
	switch action {
	case "start":
		return header + `/bin/launchctl enable system/` + coreDaemonLabel + `
if ! /bin/launchctl print system/` + coreDaemonLabel + ` >/dev/null 2>&1; then /bin/launchctl bootstrap system "$plist"; fi
/bin/launchctl kickstart system/` + coreDaemonLabel + `
` + waitRunning + `printf 'MeshMux 核心守护已启动'
`
	case "stop":
		return header + `/bin/launchctl disable system/` + coreDaemonLabel + `
if /bin/launchctl print system/` + coreDaemonLabel + ` >/dev/null 2>&1; then /bin/launchctl bootout system/` + coreDaemonLabel + `; fi
for attempt in 1 2 3 4 5 6 7 8 9 10; do
 if ! /bin/launchctl print system/` + coreDaemonLabel + ` >/dev/null 2>&1; then exit 0; fi
 /bin/sleep 1
done
exit 82
`
	case "restart":
		return header + `/bin/launchctl enable system/` + coreDaemonLabel + `
if ! /bin/launchctl print system/` + coreDaemonLabel + ` >/dev/null 2>&1; then /bin/launchctl bootstrap system "$plist"; fi
/bin/launchctl kickstart -k system/` + coreDaemonLabel + `
` + waitRunning + `printf 'MeshMux 核心守护已重启'
`
	}
	return ""
}
