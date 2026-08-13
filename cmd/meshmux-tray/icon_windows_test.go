//go:build windows

package main

import (
	"bytes"
	"testing"
)

func TestWindowsTrayIconUsesICO(t *testing.T) {
	data := trayIcon()
	if !bytes.HasPrefix(data, []byte{0, 0, 1, 0, 1, 0}) {
		t.Fatalf("trayIcon returned %d bytes without an ICO header", len(data))
	}
}
