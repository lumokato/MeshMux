//go:build !windows

package main

func trayIcon() []byte {
	return trayPNG()
}
