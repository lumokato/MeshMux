package config

import (
	"encoding/json"
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
