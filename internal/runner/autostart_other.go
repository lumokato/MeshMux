//go:build !windows && !darwin

package runner

import "fmt"

func Autostart(string) error {
	return fmt.Errorf("autostart is only implemented on Windows and macOS")
}

func AutostartEnabled() bool { return false }
