//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
)

func trayIcon() []byte {
	const size = 32
	pngData := trayPNG()
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
	_ = binary.Write(&ico, binary.LittleEndian, uint32(len(pngData)))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(22))
	_, _ = ico.Write(pngData)
	return ico.Bytes()
}
