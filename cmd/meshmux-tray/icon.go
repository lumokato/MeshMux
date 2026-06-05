package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

func trayIcon() []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{R: 15, G: 118, B: 110, A: 255}
	shadow := color.RGBA{R: 7, G: 89, B: 82, A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if inRoundedRect(x, y, 3, 3, 26, 26, 7) {
				img.SetRGBA(x, y, bg)
			}
			if inRoundedRect(x, y, 5, 24, 22, 4, 2) {
				img.SetRGBA(x, y, shadow)
			}
		}
	}
	drawLine(img, 8, 22, 8, 10, white, 2)
	drawLine(img, 8, 10, 16, 18, white, 2)
	drawLine(img, 16, 18, 24, 10, white, 2)
	drawLine(img, 24, 10, 24, 22, white, 2)

	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)

	var ico bytes.Buffer
	_ = binary.Write(&ico, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	ico.WriteByte(size)
	ico.WriteByte(size)
	ico.WriteByte(0)
	ico.WriteByte(0)
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(32))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(pngBuf.Len()))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(22))
	_, _ = ico.Write(pngBuf.Bytes())
	return ico.Bytes()
}

func inRoundedRect(x, y, left, top, width, height, radius int) bool {
	right := left + width - 1
	bottom := top + height - 1
	if x < left || x > right || y < top || y > bottom {
		return false
	}
	cx := x
	if x < left+radius {
		cx = left + radius
	} else if x > right-radius {
		cx = right - radius
	}
	cy := y
	if y < top+radius {
		cy = top + radius
	} else if y > bottom-radius {
		cy = bottom - radius
	}
	dx := x - cx
	dy := y - cy
	return dx*dx+dy*dy <= radius*radius
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA, thickness int) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx := -1
	if x0 < x1 {
		sx = 1
	}
	sy := -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		for oy := -thickness; oy <= thickness; oy++ {
			for ox := -thickness; ox <= thickness; ox++ {
				x := x0 + ox
				y := y0 + oy
				if image.Pt(x, y).In(img.Bounds()) {
					img.SetRGBA(x, y, c)
				}
			}
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
