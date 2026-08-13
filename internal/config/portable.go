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
