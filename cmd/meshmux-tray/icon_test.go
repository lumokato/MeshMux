package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestTrayPNG(t *testing.T) {
	data := trayPNG()
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("trayPNG returned %d bytes without a PNG signature", len(data))
	}
	image, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode tray PNG: %v", err)
	}
	if image.Bounds().Dx() != 32 || image.Bounds().Dy() != 32 {
		t.Fatalf("tray PNG dimensions = %v, want 32x32", image.Bounds())
	}
}
