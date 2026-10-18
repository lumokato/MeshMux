//go:build darwin

package runner

import (
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

const macTailscaleRoot = "/Library/Application Support/MeshMux/Tailscale"
const macTailscaleSocket = macTailscaleRoot + "/ipc/tailscaled.sock"
const macTailscalePlist = "/Library/LaunchDaemons/com.meshmux.tailscaled.plist"

// ValidateMacTailscaleBundle checks the exact shipped bytes, not just their size.
// The manifest is generated after signing by the release builder. Ad-hoc signing
// establishes integrity, NOT publisher identity; installation always needs consent.
func ValidateMacTailscaleBundle(root string) error {
	var manifest map[string]string
	b, err := os.ReadFile(filepath.Join(root, "tailscale-sha256.json"))
	if err != nil {
		return fmt.Errorf("缺少组件清单；请从 https://github.com/lumokato/MeshMux/releases 下载完整 MeshMux.app: %w", err)
	}
	if err = json.Unmarshal(b, &manifest); err != nil {
		return fmt.Errorf("组件清单无效: %w", err)
	}
	for _, name := range []string{"tailscale", "tailscaled"} {
		p := filepath.Join(root, "bin", name)
		st, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("缺少组件 %s；请重新安装完整 MeshMux.app", name)
		}
		if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("组件 %s 不可执行或不是普通文件", name)
		}
		f, err := macho.Open(p)
		if err != nil {
			return fmt.Errorf("组件 %s 不是 macOS 可执行文件", name)
		}
		cpu := f.Cpu
		f.Close()
		if cpu != macho.CpuAmd64 {
			return fmt.Errorf("组件 %s 架构不匹配，需要 Intel x86_64", name)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if manifest[name] != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("组件 %s SHA-256 不匹配；拒绝安装", name)
		}
	}
	return nil
}

func platformTailscaleSocket() string { return macTailscaleSocket }
func platformTailscaleCLI() string    { return macTailscaleCLI() }

func macTailscaleCLI() string { return filepath.Join(macTailscaleRoot, "bin", "tailscale") }
func MacTailscaleInstalled() bool {
	st, err := os.Stat(macTailscaleCLI())
	return err == nil && st.Mode().IsRegular()
}

// AppleScript receives a single quoted shell program. No caller can supply an
// executable, launchd label, daemon flag, socket, or root state directory.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`).Replace(s) + `"`
}

var macElevate = func(ctx context.Context, script string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", "do shell script "+appleQuote(script)+" with administrator privileges").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("管理员授权未完成或操作失败（未自动重试）: %w；%s", err, redactSensitiveText(string(out)))
	}
	return string(out), nil
}

// MacTailscaleService performs explicit user-requested launchd operations. The
// daemon is a persistent, independent network service; quitting the UI does not
// log out or destroy its identity. Stop disables next-boot startup; Start enables
// it. Ordinary status reads never elevate, install or change login preferences.
func MacTailscaleService(ctx context.Context, action string) (string, error) {
	switch action {
	case "install":
		script, err := macTailscaleInstallScript(config.BundleDir(), config.LocalDataDir())
		if err != nil {
			return "", err
		}
		return macElevate(ctx, script)
	case "start", "stop", "restart", "login", "down":
		if !MacTailscaleInstalled() {
			return "", errors.New("尚未安装 MeshMux Tailnet 服务；请先点击「安装/修复 Tailnet 服务」")
		}
		script, err := macTailscaleControlScript(action)
		if err != nil {
			return "", err
		}
		return macElevate(ctx, script)
	default:
		return "", fmt.Errorf("不支持的 Tailnet 服务操作 %q", action)
	}
}

func macTailscaleControlScript(action string) (string, error) {
	prefix := macProtectedChecks() + "\n"
	target := "system/com.meshmux.tailscaled"
	switch action {
	case "start":
		return prefix + "/bin/launchctl enable " + target + "\n/bin/launchctl print " + target + " >/dev/null 2>&1 || /bin/launchctl bootstrap system " + shellQuote(macTailscalePlist) + "\n/bin/launchctl kickstart " + target, nil
	case "stop":
		return prefix + "/bin/launchctl disable " + target + "\nif /bin/launchctl print " + target + " >/dev/null 2>&1; then /bin/launchctl bootout " + target + "; fi\nfor attempt in 1 2 3 4 5; do if ! /bin/launchctl print " + target + " >/dev/null 2>&1; then exit 0; fi; /bin/sleep 1; done\nexit 82", nil
	case "restart":
		start, _ := macTailscaleControlScript("start")
		return start + "\n/bin/launchctl kickstart -k " + target, nil
	case "login":
		// The bounded command can time out while waiting for the browser. Return
		// its login URL only to the authenticated local UI, not diagnostic logs.
		// Never pass an AuthKey or force reauthentication/DNS/routes.
		return prefix + "set +e\noutput=$(" + shellQuote(macTailscaleCLI()) + " --socket " + shellQuote(macTailscaleSocket) + " up --timeout=20s 2>&1)\ncode=$?\nprintf 'Tailnet login command exit %s (follow the URL if shown):\\n%s\\n' \"$code\" \"$output\"", nil
	case "down":
		return prefix + shellQuote(macTailscaleCLI()) + " --socket " + shellQuote(macTailscaleSocket) + " down", nil
	}
	return "", errors.New("unsupported service action")
}

const macDirectoryCheck = `
check_dir() {
 test -d "$1" && test ! -L "$1" || exit 71
 test "$(/usr/bin/stat -f '%u' "$1")" = 0 || exit 72
 test -z "$(/usr/bin/find "$1" -prune -perm +022 -print)" || exit 73
 if /bin/ls -lde "$1" | /usr/bin/tail -n +2 | /usr/bin/grep -Eq 'allow.*(write|add_|delete|append|chown)'; then exit 73; fi
}
`

func macProtectedChecks() string {
	// Each ancestor and installed executable must be root-owned, not linked or
	// group/world writable. Never run a root process from Application Support
	// under a user's home, including the legacy installation.
	return "set -eu\n" + macDirectoryCheck + `
for p in /Library '/Library/Application Support' '/Library/Application Support/MeshMux' '/Library/Application Support/MeshMux/Tailscale' '/Library/Application Support/MeshMux/Tailscale/bin' /Library/LaunchDaemons; do check_dir "$p"; done
for p in '/Library/Application Support/MeshMux/Tailscale/bin/tailscale' '/Library/Application Support/MeshMux/Tailscale/bin/tailscaled' /Library/LaunchDaemons/com.meshmux.tailscaled.plist; do
 test -f "$p" && test ! -L "$p" || exit 74
 test "$(/usr/bin/stat -f '%u' "$p")" = 0 || exit 75
 test -z "$(/usr/bin/find "$p" -prune -perm +022 -print)" || exit 76
 if /bin/ls -le "$p" | /usr/bin/tail -n +2 | /usr/bin/grep -Eq 'allow.*(write|add_|delete|append|chown)'; then exit 76; fi
done`
}

// MacTailscaleInstallPlan returns the exact bounded installation script for
// diagnostics and administrator-driven installs. It does not perform elevation.
func MacTailscaleInstallPlan() (string, error) {
	return macTailscaleInstallScript(config.BundleDir(), config.LocalDataDir())
}

func macTailscaleInstallScript(bundle, dataDir string) (string, error) {
	if err := ValidateMacTailscaleBundle(bundle); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}
	userName := currentUser.Username
	if userName == "" || strings.ContainsAny(userName, ":\n\r") {
		return "", errors.New("无法确定授权的本机用户")
	}
	if filepath.Clean(dataDir) != filepath.Join(home, "Library", "Application Support", "MeshMux") {
		return "", errors.New("隔离 MESHMUX_HOME 不允许安装系统服务；请从正常安装的应用显式授权")
	}
	var lines []string
	lines = append(lines, "set -eu\numask 077\n"+macDirectoryCheck+`
for p in /Library '/Library/Application Support' /Library/LaunchDaemons; do check_dir "$p"; done
for p in '/Library/Application Support/MeshMux' '/Library/Application Support/MeshMux/Tailscale' '/Library/Application Support/MeshMux/Tailscale/bin' '/Library/Application Support/MeshMux/Tailscale/state' '/Library/Application Support/MeshMux/Tailscale/ipc'; do
 if test ! -e "$p" && test ! -L "$p"; then /bin/mkdir "$p"; /usr/sbin/chown root:wheel "$p"; /bin/chmod 755 "$p"; fi
 check_dir "$p"
done
/bin/chmod 700 '/Library/Application Support/MeshMux/Tailscale/state'
/bin/chmod -N '/Library/Application Support/MeshMux/Tailscale/ipc'
/bin/chmod 700 '/Library/Application Support/MeshMux/Tailscale/ipc'
root='/Library/Application Support/MeshMux/Tailscale'
test ! -L "$root/state/tailscaled.state" || exit 74
was_disabled=0
if /bin/launchctl print-disabled system | /usr/bin/grep -q '"com.meshmux.tailscaled" => disabled'; then was_disabled=1; fi
/bin/mkdir -m 700 "$root/install.lock" || { echo 'Another installation is active; no changes applied'; exit 81; }
trap '/bin/rmdir "$root/install.lock"' EXIT
backup="$root/recovery-$(/bin/date +%Y%m%d-%H%M%S)"
/bin/mkdir -m 700 "$backup"
# Refuse an unrelated job that happens to share our label.
plist='/Library/LaunchDaemons/com.meshmux.tailscaled.plist'
if test -e "$plist" || test -L "$plist"; then
 test ! -L "$plist" && test "$(/usr/bin/stat -f '%u' "$plist")" = 0 || exit 74
 test -z "$(/usr/bin/find "$plist" -prune -perm +022 -print)" || exit 73
 program=$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$plist")
 case "$program" in '/usr/local/libexec/meshmux/tailscaled'|'/Library/Application Support/MeshMux/Tailscale/bin/tailscaled') ;; *) exit 77;; esac
 /bin/cp -p "$plist" "$backup/service.plist"
fi
for name in tailscale tailscaled; do
 if test -e "$root/bin/$name"; then test ! -L "$root/bin/$name" || exit 74; /bin/cp -p "$root/bin/$name" "$backup/$name"; fi
done
# Stage and verify protected copies before stopping a healthy service.`)
	lines = append(lines, "/bin/chmod +a "+shellQuote("user:"+userName+":allow:search,readattr,readsecurity")+" \"$root/ipc\"")
	for _, name := range []string{"tailscale", "tailscaled"} {
		p := filepath.Join(bundle, "bin", name)
		manifestBytes, err := os.ReadFile(filepath.Join(bundle, "tailscale-sha256.json"))
		if err != nil {
			return "", err
		}
		var manifest map[string]string
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			return "", err
		}
		digest := manifest[name]
		if len(digest) != 64 {
			return "", errors.New("invalid component digest")
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", err
		}
		// mktemp allocates root-owned files in the checked root directory. A user
		// cannot swap the source after this copy to influence the executed bytes.
		lines = append(lines, "stage_"+name+"=$(/usr/bin/mktemp \"$root/bin/."+name+".XXXXXX\")",
			"/bin/cat "+shellQuote(p)+" > \"$stage_"+name+"\"",
			"test \"$(/usr/bin/shasum -a 256 \"$stage_"+name+"\" | /usr/bin/awk '{print $1}')\" = "+shellQuote(digest)+" || exit 78",
			"/usr/bin/file -b \"$stage_"+name+"\" | /usr/bin/grep -q 'Mach-O 64-bit executable x86_64'",
			"/usr/sbin/chown root:wheel \"$stage_"+name+"\"; /bin/chmod 755 \"$stage_"+name+"\"")
	}
	legacy := filepath.Join(dataDir, "state", "tailscale", "tailscaled.state")
	lines = append(lines, `was_running=0
if /bin/launchctl print system/com.meshmux.tailscaled >/dev/null 2>&1; then
 was_running=1
 /bin/launchctl bootout system/com.meshmux.tailscaled
 # bootout returns before launchd has necessarily finished reaping the job.
 for attempt in 1 2 3 4 5; do
  if ! /bin/launchctl print system/com.meshmux.tailscaled >/dev/null 2>&1; then break; fi
  /bin/sleep 1
 done
fi
rollback() {
 code=$?
 /bin/rmdir "$root/install.lock" || true
 if test "$code" != 0; then
  /bin/launchctl bootout system/com.meshmux.tailscaled >/dev/null 2>&1 || true
  for name in tailscale tailscaled; do if test -f "$backup/$name"; then /bin/cp -p "$backup/$name" "$root/bin/$name"; fi; done
  if test -f "$backup/service.plist"; then /bin/cp -p "$backup/service.plist" "$plist"; if test "$was_running" = 1; then /bin/sleep 1; /bin/launchctl bootstrap system "$plist" || true; fi; fi
 fi
}
trap rollback EXIT
if test -e "$root/state/tailscaled.state"; then
 test ! -L "$root/state/tailscaled.state" || exit 74
 /bin/cp -p "$root/state/tailscaled.state" "$backup/tailscaled.state"
fi`,
		"legacy="+shellQuote(legacy),
		`# A legacy root-owned identity is copied only into a protected state file.
# Never replace an existing service identity with an empty user template.
if test ! -e "$root/state/tailscaled.state" && test -e "$legacy"; then
 test -f "$legacy" && test ! -L "$legacy" || exit 79
 test "$(/usr/bin/stat -f '%u' "$legacy")" = 0 || exit 79
 test "$(/usr/bin/stat -f '%z' "$legacy")" -gt 0 || exit 79
 /bin/cat "$legacy" > "$root/state/tailscaled.state"
 /usr/sbin/chown root:wheel "$root/state/tailscaled.state"
 /bin/chmod 600 "$root/state/tailscaled.state"
 /bin/cp -p "$root/state/tailscaled.state" "$backup/tailscaled.state"
fi
/bin/mv -f "$stage_tailscale" "$root/bin/tailscale"
/bin/mv -f "$stage_tailscaled" "$root/bin/tailscaled"`,
		"plist_stage=$(/usr/bin/mktemp /Library/LaunchDaemons/.meshmux.XXXXXX)\nprintf '%s' "+shellQuote(macTailscalePlistXML())+" > \"$plist_stage\"",
		`/usr/sbin/chown root:wheel "$plist_stage"
/bin/chmod 644 "$plist_stage"
/bin/mv -f "$plist_stage" "$plist"
/usr/bin/plutil -lint "$plist"
if test "$was_disabled" = 0; then
/bin/launchctl enable system/com.meshmux.tailscaled
# Allow launchd's prior job teardown to finish before loading its replacement.
/bin/sleep 1
/bin/launchctl bootstrap system "$plist"
# Wait for readiness with a hard budget, not launchd registration alone.
ready=0
for attempt in 1 2 3 4 5; do
 if "$root/bin/tailscale" --socket "$root/ipc/tailscaled.sock" status --json >/dev/null 2>&1; then ready=1; break; fi
 /bin/sleep 1
done
test "$ready" = 1 || exit 80
fi
/bin/rmdir "$root/install.lock"
trap - EXIT
printf 'MeshMux Tailnet service installed; protected recovery: %s' "$backup"`)
	return strings.Join(lines, "\n"), nil
}

func macTailscalePlistXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.meshmux.tailscaled</string>
<key>ProgramArguments</key><array>
<string>/Library/Application Support/MeshMux/Tailscale/bin/tailscaled</string>
<string>--state=/Library/Application Support/MeshMux/Tailscale/state/tailscaled.state</string>
<string>--statedir=/Library/Application Support/MeshMux/Tailscale/state</string>
<string>--socket=/Library/Application Support/MeshMux/Tailscale/ipc/tailscaled.sock</string>
</array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>30</integer>
<key>Umask</key><integer>63</integer>
<key>StandardOutPath</key><string>/dev/null</string>
<key>StandardErrorPath</key><string>/dev/null</string>
</dict></plist>
`
}
