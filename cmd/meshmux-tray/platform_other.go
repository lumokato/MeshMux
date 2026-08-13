//go:build !windows && !linux

package main

import "fmt"

func newPlatformBackend() (trayBackend, error) {
	err := fmt.Errorf("MeshMux tray is not supported on this platform")
	return unavailableBackend{err: err}, err
}

func platformStartupFatal(error) bool { return false }

func recordTrayActionError(string, error) {}
