package updater

import (
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
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
