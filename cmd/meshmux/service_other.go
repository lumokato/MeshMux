//go:build !windows

package main

import "fmt"

func runWindowsService([]string) error {
	return fmt.Errorf("Windows service mode is only available on Windows")
}

func manageWindowsService([]string) error {
	return fmt.Errorf("Windows service management is only available on Windows")
}

func writeWindowsCommandError([]string, error) {}
