//go:build darwin

package runner

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func macBundleFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	// Synthetic, non-runnable Intel Mach-O header, independent of the runner
	// CPU (the native macOS CI tests also run on arm64).
	data := make([]byte, 32)
	binary.LittleEndian.PutUint32(data[0:4], 0xfeedfacf)
	binary.LittleEndian.PutUint32(data[4:8], 0x01000007)
	binary.LittleEndian.PutUint32(data[8:12], 3)
	binary.LittleEndian.PutUint32(data[12:16], 2)
	sum := sha256.Sum256(data)
	manifest := map[string]string{}
	for _, n := range []string{"tailscale", "tailscaled"} {
		if err := os.WriteFile(filepath.Join(root, "bin", n), data, 0755); err != nil {
			t.Fatal(err)
		}
		manifest[n] = hex.EncodeToString(sum[:])
	}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "tailscale-sha256.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestMacTailscaleComponentValidation(t *testing.T) {
	root := macBundleFixture(t)
	if err := ValidateMacTailscaleBundle(root); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"tailscale", "tailscaled"} {
		p := filepath.Join(root, "bin", n)
		b, _ := os.ReadFile(p)
		os.Remove(p)
		if err := ValidateMacTailscaleBundle(root); err == nil || !strings.Contains(err.Error(), "缺少组件") {
			t.Fatalf("missing %s: %v", n, err)
		}
		os.WriteFile(p, b, 0644)
		if err := ValidateMacTailscaleBundle(root); err == nil || !strings.Contains(err.Error(), "不可执行") {
			t.Fatalf("permissions: %v", err)
		}
		os.Chmod(p, 0755)
		changed := append(append([]byte{}, b...), byte(0))
		os.WriteFile(p, changed, 0755)
		if err := ValidateMacTailscaleBundle(root); err == nil || !strings.Contains(err.Error(), "SHA-256") {
			t.Fatalf("hash mismatch: %v", err)
		}
		wrongCPU := append([]byte{}, b...)
		binary.LittleEndian.PutUint32(wrongCPU[4:8], 0x0100000c)
		os.WriteFile(p, wrongCPU, 0755)
		if err := ValidateMacTailscaleBundle(root); err == nil || !strings.Contains(err.Error(), "架构不匹配") {
			t.Fatalf("wrong CPU: %v", err)
		}
		os.WriteFile(p, []byte("ELF not Mach-O"), 0755)
		if err := ValidateMacTailscaleBundle(root); err == nil || !strings.Contains(err.Error(), "macOS") {
			t.Fatalf("architecture: %v", err)
		}
		os.Remove(p)
		os.Symlink("/bin/sh", p)
		if err := ValidateMacTailscaleBundle(root); err == nil {
			t.Fatal("symlink accepted")
		}
		os.Remove(p)
		os.WriteFile(p, b, 0755)
	}
}
func TestMacTailscaleRootInstallConstraints(t *testing.T) {
	root := macBundleFixture(t)
	home, _ := os.UserHomeDir()
	script, err := macTailscaleInstallScript(root, filepath.Join(home, "Library", "Application Support", "MeshMux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"check_dir", "stage_tailscaled", "shasum -a 256", "trap rollback EXIT", "test -f \"$legacy\"", "test ! -L", "chmod 600", "chmod +a", "mktemp /Library/LaunchDaemons", "bootout system/com.meshmux.tailscaled", "O_NOFOLLOW", "O_EXCL", "_machinekey", "launchctl disable system/com.meshmux.tailscaled"} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(macTailscalePlistXML(), home) {
		t.Fatal("daemon references user writable home")
	}
	if _, err := macTailscaleInstallScript(root, t.TempDir()); err == nil {
		t.Fatal("isolated runtime can install global service")
	}
	for _, action := range []string{"start", "stop", "restart", "login", "down"} {
		s, err := macTailscaleControlScript(action)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s, "check_dir") || !strings.Contains(s, macTailscaleRoot) {
			t.Fatal("unprotected service control")
		}
	}
	if _, err := macTailscaleControlScript("start;touch /tmp/pwn"); err == nil {
		t.Fatal("arbitrary action accepted")
	}
}
func TestMacTailscaleNoImplicitElevation(t *testing.T) {
	called := false
	old := macElevate
	defer func() { macElevate = old }()
	macElevate = func(context.Context, string) (string, error) { called = true; return "", nil }
	_, _ = MacTailscaleService(context.Background(), "unknown")
	if called {
		t.Fatal("invalid operation elevated")
	}
}
func TestMacElevationCancellationDoesNotRetry(t *testing.T) {
	if !MacTailscaleInstalled() {
		t.Skip("installed-service command seam requires service files")
	}
	calls := 0
	old := macElevate
	defer func() { macElevate = old }()
	macElevate = func(context.Context, string) (string, error) { calls++; return "", errors.New("user cancelled") }
	if _, err := MacTailscaleService(context.Background(), "restart"); err == nil {
		t.Fatal("cancellation hidden")
	}
	if calls != 1 {
		t.Fatalf("retried cancellation: %d", calls)
	}
}

func TestMacCommandQuoting(t *testing.T) {
	if got := shellQuote("a'b"); got != "'a'\\''b'" {
		t.Fatal(got)
	}
	if got := appleQuote("a\"\\b"); got != `"a\"\\b"` {
		t.Fatal(got)
	}
}
