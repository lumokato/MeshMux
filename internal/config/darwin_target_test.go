package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// The darwin target is pure configuration derivation, so it can be verified on
// any host. Keeping this here means a change to the shared target helpers
// cannot silently drop macOS support.
func TestDefaultTargetsForDarwin(t *testing.T) {
	if got := DefaultTargetNameFor("darwin"); got != "darwin" {
		t.Fatalf("DefaultTargetNameFor(darwin) = %q", got)
	}
	target := DefaultMihomoTargetFor("darwin")
	if target.Type != "darwin-mihomo" || target.Hostname != "mac-meshmux" || target.Output != filepath.Join("profiles", "darwin.yaml") {
		t.Fatalf("unexpected darwin target: %+v", target)
	}
	if got := DefaultMihomoAssetPatternFor("darwin"); got != DarwinMihomoAssetPattern {
		t.Fatalf("darwin asset pattern = %q", got)
	}
	if got := DefaultMihomoPathFor("darwin"); strings.Contains(got, ".exe") {
		t.Fatalf("darwin mihomo path must not carry a .exe suffix: %q", got)
	}
}

func TestApplyDefaultsForDarwin(t *testing.T) {
	cfg := &Config{}
	cfg.applyDefaults("darwin")
	if cfg.Targets[0].Type != "darwin-mihomo" {
		t.Fatalf("applyDefaults(darwin) produced %+v", cfg.Targets[0])
	}
	if cfg.Components.Mihomo.AssetPattern != DarwinMihomoAssetPattern {
		t.Fatalf("applyDefaults(darwin) asset pattern = %q", cfg.Components.Mihomo.AssetPattern)
	}
	if cfg.Components.Mihomo.Path != DefaultMihomoPathFor("darwin") {
		t.Fatalf("applyDefaults(darwin) mihomo path = %q", cfg.Components.Mihomo.Path)
	}
}

func TestOtherPlatformTargetsUnchanged(t *testing.T) {
	if DefaultTargetNameFor("linux") != "linux" || DefaultTargetNameFor("windows") != "windows" {
		t.Fatal("linux/windows target names regressed")
	}
	if DefaultMihomoAssetPatternFor("windows") != DefaultMihomoAssetPattern {
		t.Fatal("windows asset pattern regressed")
	}
	if DefaultMihomoAssetPatternFor("linux") != LinuxMihomoAssetPattern {
		t.Fatal("linux asset pattern regressed")
	}
}
