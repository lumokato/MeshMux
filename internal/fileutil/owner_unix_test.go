//go:build !windows

package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

type chownCall struct {
	path string
	uid  int
	gid  int
}

func withSudoEnv(t *testing.T, home, uid, gid string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_UID", uid)
	t.Setenv("SUDO_GID", gid)
	prevChown, prevUID := chownPath, effectiveUID
	t.Cleanup(func() { chownPath, effectiveUID = prevChown, prevUID })
}

func TestRestoreOwnerChownsFileAndCreatedDirsUnderHome(t *testing.T) {
	home := t.TempDir()
	withSudoEnv(t, home, "501", "20")
	var calls []chownCall
	chownPath = func(path string, uid, gid int) error {
		calls = append(calls, chownCall{path, uid, gid})
		return nil
	}
	effectiveUID = func() int { return 0 }

	target := filepath.Join(home, "Library", "Application Support", "MeshMux", "meshmux.local.json")
	if err := WriteFile(target, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if len(calls) == 0 {
		t.Fatal("expected chown calls when running under sudo")
	}
	seenFile := false
	for _, c := range calls {
		if c.uid != 501 || c.gid != 20 {
			t.Fatalf("unexpected ids: %+v", c)
		}
		if c.path == target {
			seenFile = true
		}
	}
	if !seenFile {
		t.Fatalf("target file was not chowned: %+v", calls)
	}
}

func TestRestoreOwnerSkipsPathsOutsideHome(t *testing.T) {
	withSudoEnv(t, t.TempDir(), "501", "20")
	var calls []chownCall
	chownPath = func(path string, uid, gid int) error {
		calls = append(calls, chownCall{path, uid, gid})
		return nil
	}
	effectiveUID = func() int { return 0 }

	outside := filepath.Join(t.TempDir(), "elsewhere", "file.json")
	if err := WriteFile(outside, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("must not chown outside HOME: %+v", calls)
	}
}

func TestRestoreOwnerSkipsWhenNotRoot(t *testing.T) {
	home := t.TempDir()
	withSudoEnv(t, home, "501", "20")
	var calls int
	chownPath = func(string, int, int) error { calls++; return nil }
	effectiveUID = func() int { return 501 }

	if err := WriteFile(filepath.Join(home, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("must not chown when not root, calls=%d", calls)
	}
}

func TestRestoreOwnerSkipsWithoutSudoUID(t *testing.T) {
	home := t.TempDir()
	withSudoEnv(t, home, "", "")
	var calls int
	chownPath = func(string, int, int) error { calls++; return nil }
	effectiveUID = func() int { return 0 }

	if err := WriteFile(filepath.Join(home, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("must not chown without SUDO_UID, calls=%d", calls)
	}
}

var _ = os.Geteuid
