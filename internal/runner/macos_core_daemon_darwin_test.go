//go:build darwin

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreDaemonPlistIsRootOwnedAndReadsProtectedCopy(t *testing.T) {
	xml := coreDaemonPlistXML("/Applications/MeshMux.app/Contents/MacOS/meshmux")
	required := []string{
		"<string>com.meshmux.core</string>",
		"<key>RunAtLoad</key><true/>",
		"<key>KeepAlive</key><true/>",
		"<string>/Applications/MeshMux.app/Contents/MacOS/meshmux</string>",
		"<string>_supervise</string>",
		"<string>/Library/Application Support/MeshMux/core/meshmux.local.json</string>",
		"<string>/Library/Application Support/MeshMux/core/profiles/darwin.yaml</string>",
		"<key>WorkingDirectory</key><string>/Library/Application Support/MeshMux/core</string>",
	}
	for _, want := range required {
		if !strings.Contains(xml, want) {
			t.Fatalf("plist missing %q:\n%s", want, xml)
		}
	}
	// The daemon must never be pointed at a member-writable path or a script.
	for _, forbidden := range []string{"/Users/", "profiles/darwin.yaml</string>\n<key>WorkingDirectory", "<key>Program</key>"} {
		if strings.Contains(xml, forbidden) {
			t.Fatalf("plist unexpectedly contains %q:\n%s", forbidden, xml)
		}
	}
	if strings.Contains(xml, "<key>UserName</key>") {
		t.Fatalf("daemon must run as root, not a named user:\n%s", xml)
	}
}

func TestParseCoreDaemonLaunchctl(t *testing.T) {
	state, pid := parseCoreDaemonLaunchctl("system/com.meshmux.core = {\n\tstate = running\n\tpid = 4242\n}\n")
	if state != "running" || pid != 4242 {
		t.Fatalf("got state=%q pid=%d", state, pid)
	}
	state, pid = parseCoreDaemonLaunchctl("system/com.meshmux.core = {\n\tstate = running\n\tendpoints = {\n\t\tstate = active\n\t}\n\tpid = 99\n}\n")
	if state != "running" || pid != 99 {
		t.Fatalf("nested endpoint state overrode the job state: state=%q pid=%d", state, pid)
	}
	state, pid = parseCoreDaemonLaunchctl("system/com.meshmux.core = {\n\tstate = not running\n}\n")
	if state != "not running" || pid != 0 {
		t.Fatalf("got state=%q pid=%d", state, pid)
	}
	if _, pid := parseCoreDaemonLaunchctl("no such service"); pid != 0 {
		t.Fatalf("unexpected pid %d", pid)
	}
}

func writeCoreDaemonTestConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "meshmux.local.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAuditCoreDaemonConfigAcceptsRelativePaths(t *testing.T) {
	path := writeCoreDaemonTestConfig(t, `{"name":"default","paths":{"runtime":"runtime","dashboard":"dashboard"}}`)
	if err := auditCoreDaemonConfig(path); err != nil {
		t.Fatalf("relative config rejected: %v", err)
	}
}

func TestAuditCoreDaemonConfigRejectsMemberWritablePaths(t *testing.T) {
	cases := []string{
		`{"components":{"mihomo":{"path":"/Users/tau/Library/Application Support/MeshMux/bin/mihomo"}}}`,
		`{"paths":{"runtime":"/tmp/runtime"}}`,
		`{"paths":{"dashboard":"../escape/dashboard"}}`,
		`{"wireguard":{"configs":["/Users/tau/wg.conf"]}}`,
		`{"tailscale":{"authKeyFile":"/Users/tau/key"}}`,
	}
	for _, body := range cases {
		path := writeCoreDaemonTestConfig(t, body)
		err := auditCoreDaemonConfig(path)
		if err == nil {
			t.Fatalf("accepted member-writable path config: %s", body)
		}
		if !strings.Contains(err.Error(), "相对路径") && !strings.Contains(err.Error(), "跳出") {
			t.Fatalf("unexpected rejection reason for %s: %v", body, err)
		}
	}
}

func TestAuditCoreDaemonConfigRejectsInvalidJSON(t *testing.T) {
	path := writeCoreDaemonTestConfig(t, `{`)
	if err := auditCoreDaemonConfig(path); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func coreDaemonTestPaths(t *testing.T) coreDaemonPaths {
	t.Helper()
	return coreDaemonPaths{
		CLI:           "/Applications/MeshMux.app/Contents/MacOS/meshmux",
		UserData:      "/Users/tau/Library/Application Support/MeshMux",
		UserConfig:    "/Users/tau/Library/Application Support/MeshMux/meshmux.local.json",
		UserProviders: "/Users/tau/Library/Application Support/MeshMux/providers",
	}
}

func TestCoreDaemonInstallScriptStagesProtectedCopies(t *testing.T) {
	script := coreDaemonInstallScript(coreDaemonTestPaths(t))
	required := []string{
		"check_dir",
		"root='/Library/Application Support/MeshMux/core'",
		"/bin/cat \"$user_config\" > \"$stage_config\"",
		"/usr/sbin/chown root:wheel \"$stage_config\"",
		"/bin/chmod 600 \"$stage_config\"",
		"generate -config \"$root/meshmux.local.json\" darwin",
		"plutil -lint",
		"/bin/launchctl bootstrap system",
		"trap rollback EXIT",
		"recovery-",
		`pgrep -f`,
	}
	for _, want := range required {
		if !strings.Contains(script, want) {
			t.Fatalf("install script missing %q", want)
		}
	}
	// Generated scripts must use absolute paths that exist on macOS.
	if strings.Contains(script, "/bin/chown") {
		t.Fatal("install script uses /bin/chown; macOS ships chown in /usr/sbin")
	}
	// A user-owned profile is never trusted; only the protected copy is generated.
	if strings.Contains(script, "cp -p \"$user_data/profiles") {
		t.Fatal("install script copies a member-writable profile")
	}
}

func TestCoreDaemonSyncScriptRegeneratesFromProtectedCopy(t *testing.T) {
	script := coreDaemonSyncScript(coreDaemonTestPaths(t))
	if !strings.Contains(script, "generate -config \"$root/meshmux.local.json\" darwin") {
		t.Fatal("sync script does not regenerate the protected profile")
	}
	if !strings.Contains(script, "kickstart -k system/com.meshmux.core") {
		t.Fatal("sync script does not restart the daemon")
	}
	if !strings.Contains(script, "install.lock") {
		t.Fatal("sync script is not serialized")
	}
}

func TestCoreDaemonUninstallScriptRestoresManualMode(t *testing.T) {
	script := coreDaemonUninstallScript()
	for _, want := range []string{
		"launchctl disable system/com.meshmux.core",
		"launchctl bootout system/com.meshmux.core",
		`/bin/rm -f "$plist"`,
		"recovery-uninstall-",
		"手动模式",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("uninstall script missing %q", want)
		}
	}
}

func TestCoreDaemonControlScripts(t *testing.T) {
	stop := coreDaemonControlScript("stop")
	if !strings.Contains(stop, "launchctl disable system/com.meshmux.core") ||
		!strings.Contains(stop, "launchctl bootout system/com.meshmux.core") {
		t.Fatalf("stop script does not disable and unload the daemon:\n%s", stop)
	}
	start := coreDaemonControlScript("start")
	if !strings.Contains(start, "launchctl enable system/com.meshmux.core") ||
		!strings.Contains(start, "state = running") {
		t.Fatalf("start script does not enable and wait for readiness:\n%s", start)
	}
	restart := coreDaemonControlScript("restart")
	if !strings.Contains(restart, "kickstart -k system/com.meshmux.core") {
		t.Fatalf("restart script does not kickstart the job:\n%s", restart)
	}
}

func TestCoreDaemonPlanRejectsUnknownAction(t *testing.T) {
	if _, err := CoreDaemonPlan("destroy"); err == nil {
		t.Fatal("unknown action accepted")
	}
}
