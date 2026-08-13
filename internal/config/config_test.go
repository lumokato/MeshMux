package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyDefaultsDerivesSimpleSubStoreSetup(t *testing.T) {
	cfg := Config{
		Providers: []Provider{{
			Name: "main",
			URL:  "https://sub.example.test/backend/download/collection/mix?target=ClashMeta",
		}},
		Targets: []Target{
			{Name: "android-flclash", Type: "android-flclash", Output: "profiles/android-flclash.yaml"},
			{Name: "android-yumebox", Type: "android-yumebox", Output: "profiles/android-yumebox.yaml"},
		},
		Publish: []PublishTarget{{
			Name:     "old-substore",
			Type:     "substore-files",
			BaseURL:  "https://substore.example",
			APIPath:  "/backend/api/files",
			FileName: "android-flclash",
		}},
	}

	cfg.ApplyDefaults()

	if cfg.Setup.SubStoreURL != "https://sub.example.test/" {
		t.Fatalf("SubStoreURL = %q", cfg.Setup.SubStoreURL)
	}
	if cfg.Setup.SubStoreBackend != "backend" {
		t.Fatalf("SubStoreBackend = %q", cfg.Setup.SubStoreBackend)
	}
	if cfg.Setup.SubStoreFileName != "android-flclash" {
		t.Fatalf("SubStoreFileName = %q", cfg.Setup.SubStoreFileName)
	}
	if len(cfg.Targets) != 2 || cfg.Targets[1].Name != "mobile" {
		t.Fatalf("targets = %+v", cfg.Targets)
	}
	if len(cfg.Publish) != 1 || cfg.Publish[0].Backend != "backend" || cfg.Publish[0].FileName != "android-flclash" {
		t.Fatalf("publish = %+v", cfg.Publish)
	}
}

func TestStorageCopyOmitsDerivedRuntimeFields(t *testing.T) {
	cfg := Config{}
	cfg.Setup.ProviderURL = "https://sub.example.test/backend/download/self?target=ClashMeta"
	cfg.Setup.SubStoreURL = "https://sub.example.test/"
	cfg.Setup.SubStoreBackend = "backend"
	cfg.Setup.SubStoreFileName = "mobile"

	stored := cfg.StorageCopy()
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{`"providers"`, `"targets"`, `"publish"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("stored config contains %s: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, `"providerUrl"`) || !strings.Contains(text, `"subStoreFileName"`) {
		t.Fatalf("stored config lost setup fields: %s", text)
	}
}

func TestValidateTailscaleInboundForwards(t *testing.T) {
	cfg := Config{Tailscale: Tailscale{
		Enabled: true,
		InboundForwards: []InboundForward{
			{Name: " ssh ", Network: "TCP", ListenPort: 22, Target: "127.0.0.1:22"},
			{Name: "udp", Network: "udp", ListenPort: 12345, Target: "[::1]:12345"},
		},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Tailscale.InboundForwards[0].Name != "ssh" || cfg.Tailscale.InboundForwards[0].Network != "tcp" {
		t.Fatalf("forwards not normalized: %+v", cfg.Tailscale.InboundForwards)
	}
}

func TestValidateTailscaleInboundForwardsRejectsInvalidConfig(t *testing.T) {
	testCases := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "disabled", cfg: Config{Tailscale: Tailscale{InboundForwards: []InboundForward{{Name: "ssh", Network: "tcp", ListenPort: 22, Target: "127.0.0.1:22"}}}}, want: "要求先启用"},
		{name: "duplicate", cfg: Config{Tailscale: Tailscale{Enabled: true, InboundForwards: []InboundForward{{Name: "a", Network: "tcp", ListenPort: 22, Target: "127.0.0.1:22"}, {Name: "b", Network: "tcp", ListenPort: 22, Target: "127.0.0.1:23"}}}}, want: "端口重复"},
		{name: "target", cfg: Config{Tailscale: Tailscale{Enabled: true, InboundForwards: []InboundForward{{Name: "a", Network: "tcp", ListenPort: 22, Target: "invalid"}}}}, want: "host:port"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.cfg.Validate(); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Validate error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestApplyDefaultsMigratesLegacyMihomoComponent(t *testing.T) {
	for _, component := range []Component{
		{Repo: "MetaCubeX/mihomo", AssetPattern: `mihomo-windows-amd64-compatible.*\.zip$`},
		{Repo: "MetaCubeX/mihomo", AssetPattern: `mihomo-windows-amd64.*\.zip$`},
	} {
		t.Run(component.Repo+"/"+component.AssetPattern, func(t *testing.T) {
			cfg := Config{Components: Components{Mihomo: component}}
			cfg.applyDefaults("windows")
			if cfg.Components.Mihomo.Repo != DefaultMihomoRepo || cfg.Components.Mihomo.ReleaseTag != DefaultMihomoReleaseTag || cfg.Components.Mihomo.AssetPattern != DefaultMihomoAssetPattern {
				t.Fatalf("mihomo component = %+v", cfg.Components.Mihomo)
			}
		})
	}
}

func TestApplyDefaultsPreservesCustomMihomoComponent(t *testing.T) {
	cfg := Config{Components: Components{Mihomo: Component{
		Repo:         "example/custom-mihomo",
		ReleaseTag:   "custom-v1",
		AssetPattern: `custom\.zip$`,
	}}}
	cfg.ApplyDefaults()
	if cfg.Components.Mihomo.Repo != "example/custom-mihomo" || cfg.Components.Mihomo.ReleaseTag != "custom-v1" || cfg.Components.Mihomo.AssetPattern != `custom\.zip$` {
		t.Fatalf("custom mihomo component changed: %+v", cfg.Components.Mihomo)
	}
}

func TestApplyDefaultsUsesPlatformMihomoDefaults(t *testing.T) {
	testCases := []struct {
		goos    string
		path    string
		pattern string
	}{
		{goos: "windows", path: filepath.Join("bin", "mihomo.exe"), pattern: DefaultMihomoAssetPattern},
		{goos: "linux", path: filepath.Join("bin", "mihomo"), pattern: LinuxMihomoAssetPattern},
	}
	for _, testCase := range testCases {
		t.Run(testCase.goos, func(t *testing.T) {
			cfg := Config{}
			cfg.applyDefaults(testCase.goos)
			if cfg.Components.Mihomo.Path != testCase.path {
				t.Fatalf("mihomo path = %q, want %q", cfg.Components.Mihomo.Path, testCase.path)
			}
			if cfg.Components.Mihomo.Repo != DefaultMihomoRepo || cfg.Components.Mihomo.ReleaseTag != DefaultMihomoReleaseTag {
				t.Fatalf("mihomo source = %+v", cfg.Components.Mihomo)
			}
			if cfg.Components.Mihomo.AssetPattern != testCase.pattern {
				t.Fatalf("mihomo asset pattern = %q, want %q", cfg.Components.Mihomo.AssetPattern, testCase.pattern)
			}
		})
	}
}

func TestApplyDefaultsUsesOfficialLinuxMihomoAsset(t *testing.T) {
	cfg := Config{Components: Components{Mihomo: Component{Repo: OfficialMihomoRepo}}}
	cfg.applyDefaults("linux")
	if cfg.Components.Mihomo.Repo != OfficialMihomoRepo {
		t.Fatalf("mihomo repo = %q", cfg.Components.Mihomo.Repo)
	}
	if cfg.Components.Mihomo.ReleaseTag != "" {
		t.Fatalf("mihomo release tag = %q, want latest", cfg.Components.Mihomo.ReleaseTag)
	}
	if cfg.Components.Mihomo.AssetPattern != OfficialLinuxMihomoAssetPattern {
		t.Fatalf("mihomo asset pattern = %q, want %q", cfg.Components.Mihomo.AssetPattern, OfficialLinuxMihomoAssetPattern)
	}
}

func TestApplyDefaultsMigratesFixedMihomoAssetAcrossPlatforms(t *testing.T) {
	testCases := []struct {
		goos       string
		oldPath    string
		oldPattern string
		wantPath   string
		want       string
	}{
		{goos: "linux", oldPath: filepath.Join("bin", "mihomo.exe"), oldPattern: DefaultMihomoAssetPattern, wantPath: filepath.Join("bin", "mihomo"), want: LinuxMihomoAssetPattern},
		{goos: "windows", oldPath: filepath.Join("bin", "mihomo"), oldPattern: LinuxMihomoAssetPattern, wantPath: filepath.Join("bin", "mihomo.exe"), want: DefaultMihomoAssetPattern},
	}
	for _, testCase := range testCases {
		t.Run(testCase.goos, func(t *testing.T) {
			cfg := Config{Components: Components{Mihomo: Component{
				Path:         testCase.oldPath,
				Repo:         DefaultMihomoRepo,
				ReleaseTag:   DefaultMihomoReleaseTag,
				AssetPattern: testCase.oldPattern,
			}}}
			cfg.applyDefaults(testCase.goos)
			if cfg.Components.Mihomo.AssetPattern != testCase.want {
				t.Fatalf("mihomo asset pattern = %q, want %q", cfg.Components.Mihomo.AssetPattern, testCase.want)
			}
			if cfg.Components.Mihomo.Path != testCase.wantPath {
				t.Fatalf("mihomo path = %q, want %q", cfg.Components.Mihomo.Path, testCase.wantPath)
			}
		})
	}
}

func TestMihomoPlatformHelpers(t *testing.T) {
	if got := DefaultMihomoPathFor("windows"); got != filepath.Join("bin", "mihomo.exe") {
		t.Fatalf("Windows path = %q", got)
	}
	if got := DefaultMihomoPathFor("linux"); got != filepath.Join("bin", "mihomo") {
		t.Fatalf("Linux path = %q", got)
	}
	if got := mihomoAssetPatternFor("linux", OfficialMihomoRepo); got != OfficialLinuxMihomoAssetPattern {
		t.Fatalf("official Linux pattern = %q", got)
	}
	if got := DefaultMihomoAssetPatternFor("linux"); got != LinuxMihomoAssetPattern {
		t.Fatalf("fixed Linux pattern = %q", got)
	}
	if got := DefaultMihomoAssetPatternFor("windows"); got != DefaultMihomoAssetPattern {
		t.Fatalf("Windows pattern = %q", got)
	}
	if got := DefaultTargetNameFor("windows"); got != "windows" {
		t.Fatalf("Windows target name = %q", got)
	}
	if got := DefaultTargetNameFor("linux"); got != "linux" {
		t.Fatalf("Linux target name = %q", got)
	}
	windowsTarget := DefaultMihomoTargetFor("windows")
	if windowsTarget.Name != "windows" || windowsTarget.Type != "windows-mihomo" || windowsTarget.Output != filepath.Join("profiles", "windows.yaml") {
		t.Fatalf("Windows target = %+v", windowsTarget)
	}
	linuxTarget := DefaultMihomoTargetFor("linux")
	if linuxTarget.Name != "linux" || linuxTarget.Type != "linux-mihomo" || linuxTarget.Hostname != "linux-meshmux" || linuxTarget.Output != filepath.Join("profiles", "linux.yaml") {
		t.Fatalf("Linux target = %+v", linuxTarget)
	}
}
