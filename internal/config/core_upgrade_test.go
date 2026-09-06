package config

import "testing"

func TestReleasedCoreDefaultUpgrade(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		for _, pinned := range []bool{false, true} {
			component := Component{Repo: DefaultMihomoRepo, ReleaseTag: "mihomo-v1.19.29-meshmux.1", AssetPattern: `mihomo-windows-amd64-compatible-v1\.19\.29-meshmux\.1\.zip$`}
			if pinned {
				component.SHA256 = "explicit-user-pin"
			}
			cfg := Config{Components: Components{Mihomo: component}}
			cfg.applyDefaults(goos)
			if pinned {
				if cfg.Components.Mihomo.ReleaseTag != component.ReleaseTag || cfg.Components.Mihomo.SHA256 != component.SHA256 {
					t.Fatal("explicit core pin changed")
				}
			} else if cfg.Components.Mihomo.ReleaseTag != DefaultMihomoReleaseTag || cfg.Components.Mihomo.AssetPattern != DefaultMihomoAssetPatternFor(goos) {
				t.Fatalf("%s released core default did not upgrade", goos)
			}
		}
	}
}
