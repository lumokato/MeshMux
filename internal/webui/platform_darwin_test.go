package webui

import "testing"

// platformUIFor is pure, so the macOS branch is verifiable from any host.
func TestPlatformUIForDarwin(t *testing.T) {
	ui := platformUIFor("darwin")
	if ui.RuntimeTarget != "darwin" || ui.RuntimeTargetType != "darwin-mihomo" {
		t.Fatalf("unexpected darwin ui: %+v", ui)
	}
	if ui.RuntimeOutput != "profiles/darwin.yaml" || ui.RuntimeHostname != "mac-meshmux" {
		t.Fatalf("unexpected darwin paths: %+v", ui)
	}
	if !ui.SystemProxy || !ui.RuntimeActions {
		t.Fatalf("darwin must allow runtime actions and system proxy: %+v", ui)
	}
}

func TestPlatformActionAllowedPerPlatform(t *testing.T) {
	actions := []string{"start", "stop", "proxy-on", "proxy-off", "dashboard"}
	for _, action := range actions {
		if !platformActionAllowed("darwin", action) {
			t.Fatalf("darwin should allow %s", action)
		}
		if !platformActionAllowed("windows", action) {
			t.Fatalf("windows should allow %s", action)
		}
		if platformActionAllowed("linux", action) {
			t.Fatalf("linux should not allow %s", action)
		}
	}
}

func TestPlatformUIForOtherPlatformsUnchanged(t *testing.T) {
	if ui := platformUIFor("windows"); ui.RuntimeTarget != "windows" || ui.RuntimeTargetType != "windows-mihomo" {
		t.Fatalf("windows ui regressed: %+v", ui)
	}
	if ui := platformUIFor("linux"); ui.RuntimeTarget != "linux" || ui.SystemProxy || ui.RuntimeActions {
		t.Fatalf("linux ui regressed: %+v", ui)
	}
}
