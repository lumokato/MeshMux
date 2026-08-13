package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func resolveLinuxDataDir(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if dir := strings.TrimSpace(getenv("MESHMUX_HOME")); dir != "" {
		return filepath.Clean(dir), nil
	}
	if dir := strings.TrimSpace(getenv("XDG_DATA_HOME")); dir != "" {
		return filepath.Join(dir, "meshmux"), nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve Linux home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve Linux home directory: empty path")
	}
	return filepath.Join(home, ".local", "share", "meshmux"), nil
}

func resolveLinuxConfigPath(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if dir := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "meshmux", "meshmux.local.json"), nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve Linux home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve Linux home directory: empty path")
	}
	return filepath.Join(home, ".config", "meshmux", "meshmux.local.json"), nil
}

func openLocalURLFile(path string, openURL func(string) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read MeshMux web URL %s: %w", path, err)
	}
	rawURL := strings.TrimSpace(string(data))
	if err := validateLocalWebURL(rawURL); err != nil {
		return err
	}
	return openURL(rawURL)
}

func validateLocalWebURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid MeshMux web URL: %w", err)
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.Hostname() == "" {
		return fmt.Errorf("invalid MeshMux web URL %q", rawURL)
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("MeshMux web URL must use a loopback host")
	}
	return nil
}
