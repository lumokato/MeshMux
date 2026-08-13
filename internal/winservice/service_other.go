//go:build !windows

package winservice

import (
	"fmt"
	"time"
)

const Name = "MeshMux"

func Install(string, string) error { return fmt.Errorf("Windows services are unavailable") }
func Remove() error                { return fmt.Errorf("Windows services are unavailable") }
func Control(string, time.Duration) error {
	return fmt.Errorf("Windows services are unavailable")
}
func Status() (string, error) { return "unavailable", fmt.Errorf("Windows services are unavailable") }
func Installed() bool         { return false }
func Running() bool           { return false }
func AutostartEnabled() bool  { return false }
func DataDir() string         { return "" }
func SecureDataDir(string) error {
	return fmt.Errorf("Windows services are unavailable")
}
func RunElevated(string, string) error {
	return fmt.Errorf("Windows services are unavailable")
}
