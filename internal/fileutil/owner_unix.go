//go:build !windows

package fileutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Indirection so tests can exercise the sudo path without running as root.
var (
	effectiveUID = os.Geteuid
	chownPath    = os.Chown
)

// restoreOwner hands files and directories created under the invoking user's
// home back to that user when the process runs through sudo. Without this,
// `sudo meshmux start` leaves root-owned profile/config files behind and the
// tray can no longer rewrite them.
func restoreOwner(path string, createdDirs []string) {
	if effectiveUID() != 0 {
		return
	}
	uidText, gidText := os.Getenv("SUDO_UID"), os.Getenv("SUDO_GID")
	if uidText == "" || gidText == "" {
		return
	}
	uid, err := strconv.Atoi(uidText)
	if err != nil || uid <= 0 {
		return
	}
	gid, err := strconv.Atoi(gidText)
	if err != nil || gid < 0 {
		return
	}
	home := os.Getenv("HOME")
	if home == "" {
		return
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return
	}
	absPath, err := filepath.Abs(path)
	if err != nil || !withinHome(absPath, absHome) {
		return
	}
	for _, dir := range createdDirs {
		if abs, err := filepath.Abs(dir); err == nil && withinHome(abs, absHome) {
			_ = chownPath(abs, uid, gid)
		}
	}
	_ = chownPath(absPath, uid, gid)
}

func withinHome(path, home string) bool {
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
