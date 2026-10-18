package config

import (
	"path/filepath"
	"testing"
)

func TestBundleDirFor(t *testing.T) {
	app := filepath.Join("root with spaces", "MeshMux.app", "Contents")
	for _, tc := range []struct {
		name, dir, goos, want string
	}{
		{"mac-app", filepath.Join(app, "MacOS"), "darwin", filepath.Join(app, "Resources")},
		{"portable-mac", filepath.Join("root", "meshmux-macos"), "darwin", filepath.Join("root", "meshmux-macos")},
		{"non-app", filepath.Join("root", "Contents", "MacOS"), "darwin", filepath.Join("root", "Contents", "MacOS")},
		{"windows-unchanged", filepath.Join(app, "MacOS"), "windows", filepath.Join(app, "MacOS")},
		{"linux-unchanged", filepath.Join(app, "MacOS"), "linux", filepath.Join(app, "MacOS")},
		{"unknown-executable", "", "darwin", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bundleDirFor(tc.dir, tc.goos); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
