//go:build !windows

package main

func setDPIAwareness() {}

func relaunchElevatedIfNeeded() (bool, error) {
	return false, nil
}
