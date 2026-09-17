//go:build !windows && !darwin

package runner

// caseInsensitivePaths reports whether executable paths must be compared
// without case. Linux and other Unix filesystems are case-sensitive.
func caseInsensitivePaths() bool { return false }
