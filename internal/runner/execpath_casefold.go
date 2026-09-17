//go:build windows || darwin

package runner

// caseInsensitivePaths reports whether executable paths must be compared
// without case. Windows filesystems are case-insensitive, and the default
// macOS APFS volume preserves case but matches case-insensitively, so the
// same pid can be reported with different casing than the configured path.
func caseInsensitivePaths() bool { return true }
