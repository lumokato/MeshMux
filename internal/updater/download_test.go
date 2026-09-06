package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"github.com/meshmux/meshmux/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsMihomoExecutableName(t *testing.T) {
	testCases := []struct {
		name string
		want bool
	}{
		{name: "mihomo.exe", want: true},
		{name: "mihomo-windows-amd64-compatible.exe", want: true},
		{name: "mihomo", want: true},
		{name: "mihomo-linux-amd64-compatible-v1.19.29", want: true},
		{name: "mihomo.yaml", want: false},
		{name: "mihomo-source-v1.19.29.zip", want: false},
		{name: "README", want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isMihomoExecutableName(testCase.name); got != testCase.want {
				t.Fatalf("isMihomoExecutableName(%q) = %t, want %t", testCase.name, got, testCase.want)
			}
		})
	}
}

func TestInstallMihomoRecognizesWindowsExecutable(t *testing.T) {
	dir := t.TempDir()
	useWorkingDir(t, dir)
	archive := filepath.Join(dir, "mihomo.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "nested/mihomo-windows-amd64-compatible.exe", Method: zip.Deflate}
	header.SetMode(0644)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("windows-core")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "bin", "mihomo.exe")
	if _, err := installMihomo(archive, target); err != nil {
		t.Fatal(err)
	}
	assertInstalledMihomo(t, target, "windows-core")
}

func TestInstallMihomoRecognizesLinuxGzipExecutable(t *testing.T) {
	dir := t.TempDir()
	useWorkingDir(t, dir)
	archive := filepath.Join(dir, "mihomo-linux-amd64-compatible-v1.19.29.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(file)
	writer.Name = "mihomo-linux-amd64-compatible-v1.19.29"
	if _, err := writer.Write([]byte("linux-core")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "bin", "mihomo")
	if _, err := installMihomo(archive, target); err != nil {
		t.Fatal(err)
	}
	assertInstalledMihomo(t, target, "linux-core")
}

func useWorkingDir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func assertInstalledMihomo(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("installed contents = %q, want %q", data, want)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0755 {
			t.Fatalf("installed mode = %o, want 755", got)
		}
	}
}

func TestInvalidDashboardPreservesExistingInstallation(t *testing.T) {
	dir := t.TempDir()
	useWorkingDir(t, dir)
	target := filepath.Join(dir, "dashboard")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "index.html"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := writeTestZip(t, dir, "README.txt", "not a dashboard")
	if _, err := installDashboard(archive, target); err == nil {
		t.Fatal("invalid dashboard accepted")
	}
	data, err := os.ReadFile(filepath.Join(target, "index.html"))
	if err != nil || string(data) != "old" {
		t.Fatalf("old dashboard lost: %q %v", data, err)
	}
}
func TestDashboardReplacesTreeAndRejectsWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	useWorkingDir(t, dir)
	target := filepath.Join(dir, "dashboard")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "obsolete.js"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := writeTestZip(t, dir, "dist/index.html", "new")
	if _, err := installDashboard(archive, dir); err == nil {
		t.Fatal("working directory accepted")
	}
	if _, err := installDashboard(archive, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "index.html"))
	if err != nil || string(data) != "new" {
		t.Fatalf("replacement failed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(target, "obsolete.js")); !os.IsNotExist(err) {
		t.Fatalf("obsolete asset survived: %v", err)
	}
}
func TestZipRejectsTraversalAndWindowsStreams(t *testing.T) {
	for _, name := range []string{"../escape", "stream:payload", "/absolute"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			archive := writeTestZip(t, dir, name, "bad")
			if err := extractZip(archive, filepath.Join(dir, "out")); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
}
func writeTestZip(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, "test.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFailedDownloadPreservesPreviousArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.zip")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("partial"))
	}))
	defer server.Close()
	if err := downloadFile(server.URL, path); err == nil {
		t.Fatal("truncated download accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Fatalf("previous archive lost: %q %v", data, err)
	}
}
func TestTarRejectsLinksAndTraversal(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../outside"},
		{Name: "link", Typeflag: tar.TypeLink, Linkname: "../outside"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.tgz")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		compressed := gzip.NewWriter(file)
		archive := tar.NewWriter(compressed)
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := extractTarGz(path, filepath.Join(dir, "out")); err == nil {
			t.Fatal("unsafe tar entry accepted")
		}
	}
}

func TestComponentChecksumRequiredAndVerified(t *testing.T) {
	if _, err := assetChecksum(config.Component{}, asset{}); err == nil {
		t.Fatal("missing digest accepted")
	}
	expected := strings.Repeat("a", 64)
	got, err := assetChecksum(config.Component{SHA256: expected}, asset{Digest: "sha256:" + strings.Repeat("b", 64)})
	if err != nil || got != expected {
		t.Fatalf("explicit pin lost: %s %v", got, err)
	}
	got, err = assetChecksum(config.Component{Repo: config.DefaultMihomoRepo, ReleaseTag: config.DefaultMihomoReleaseTag}, asset{Name: "mihomo-windows-amd64-compatible-v1.19.29-meshmux.2.zip"})
	if err != nil || got != pinnedWindowsCoreSHA256 {
		t.Fatal("fixed core pin lost")
	}
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(path, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(path, expected); err == nil {
		t.Fatal("corrupt component accepted")
	}
}
func TestCopyLimited(t *testing.T) {
	for _, input := range []string{"abc", "abcd"} {
		var out bytes.Buffer
		_, err := copyLimited(&out, strings.NewReader(input), 3)
		if (err != nil) != (len(input) > 3) {
			t.Fatalf("limit result: %v", err)
		}
	}
}
func TestTarRejectsOversizedHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.tgz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	archive := tar.NewWriter(gz)
	if err := archive.WriteHeader(&tar.Header{Name: "large", Mode: 0600, Size: maxExpandedBytes + 1}); err != nil {
		t.Fatal(err)
	}
	_ = gz.Close()
	_ = file.Close()
	if err := extractTarGz(path, filepath.Join(dir, "out")); err == nil {
		t.Fatal("oversized archive accepted")
	}
}
