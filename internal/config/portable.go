package config

import (
	"os"
	"path/filepath"
	"runtime"
)

func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// BundleDir is the read-only resource root, not the writable runtime directory.
// macOS apps put entry points in Contents/MacOS and payloads in Resources.
// Portable archives on every platform retain the executable-relative layout.
func BundleDir() string {
	return bundleDirFor(ExeDir(), runtime.GOOS)
}

func bundleDirFor(exeDir, goos string) string {
	if goos == "darwin" && filepath.Base(exeDir) == "MacOS" &&
		filepath.Base(filepath.Dir(exeDir)) == "Contents" &&
		filepath.Ext(filepath.Dir(filepath.Dir(exeDir))) == ".app" {
		return filepath.Join(filepath.Dir(exeDir), "Resources")
	}
	return exeDir
}

func BundledMihomoPath() string {
	if dir := BundleDir(); dir != "" {
		return filepath.Join(dir, DefaultMihomoPath())
	}
	return ""
}

func BundledGeoIPPath() string {
	if dir := BundleDir(); dir != "" {
		return filepath.Join(dir, "bin", "geoip.metadb")
	}
	return ""
}

func BundledDashboardPath() string {
	if dir := BundleDir(); dir != "" {
		return filepath.Join(dir, "dashboard")
	}
	return ""
}

// BundledTailscaledPath is the daemon shipped with the application. Runtime
// directories are never assumed to already hold it.
func BundledTailscaledPath() string {
	if dir := BundleDir(); dir != "" {
		return filepath.Join(dir, DefaultTailscaledPath())
	}
	return ""
}

func BundledTailscaleCLIPath() string {
	if dir := BundleDir(); dir != "" {
		return filepath.Join(dir, DefaultTailscaleCLIPath())
	}
	return ""
}

// BundledWintunPath is the TUN driver shared by mihomo and tailscaled. Both
// load it from the directory holding their executable, so each runtime copy
// needs its own driver. Non-Windows packages need not contain it.
func BundledWintunPath() string {
	if dir := BundleDir(); dir != "" {
		return filepath.Join(dir, "bin", "wintun.dll")
	}
	return ""
}
