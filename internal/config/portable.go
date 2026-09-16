package config

import (
	"os"
	"path/filepath"
)

func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

func BundledMihomoPath() string {
	if dir := ExeDir(); dir != "" {
		return filepath.Join(dir, DefaultMihomoPath())
	}
	return ""
}

func BundledGeoIPPath() string {
	if dir := ExeDir(); dir != "" {
		return filepath.Join(dir, "bin", "geoip.metadb")
	}
	return ""
}

func BundledDashboardPath() string {
	if dir := ExeDir(); dir != "" {
		return filepath.Join(dir, "dashboard")
	}
	return ""
}

// BundledTailscaledPath is the daemon the installer ships next to the
// executable. Runtime directories are never assumed to already hold it: the
// service reads its own data directory, not the install directory.
func BundledTailscaledPath() string {
	if dir := ExeDir(); dir != "" {
		return filepath.Join(dir, DefaultTailscaledPath())
	}
	return ""
}

// BundledTailscaleCLIPath is the client that talks to the bundled daemon.
func BundledTailscaleCLIPath() string {
	if dir := ExeDir(); dir != "" {
		return filepath.Join(dir, DefaultTailscaleCLIPath())
	}
	return ""
}

// BundledWintunPath is the TUN driver shared by mihomo and tailscaled. Both
// load it from the directory that holds their own executable, so it has to be
// installed next to every core copy rather than only in the install directory.
func BundledWintunPath() string {
	if dir := ExeDir(); dir != "" {
		return filepath.Join(dir, "bin", "wintun.dll")
	}
	return ""
}
