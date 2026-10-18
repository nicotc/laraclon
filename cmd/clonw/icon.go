package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// trayIcon draws the tray icon: a rounded square with a white "C" ring. Windows accepts ICO files
// that wrap a PNG image, which keeps this free of image assets.
func trayIcon(fill color.RGBA) []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	c := float64(size-1) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-c, float64(y)-c
			// Rounded square background.
			ax, ay := math.Max(math.Abs(dx)-9, 0), math.Max(math.Abs(dy)-9, 0)
			if math.Hypot(ax, ay) > 6.5 {
				continue
			}
			px := fill
			// "C": a ring with an opening on the right side.
			r := math.Hypot(dx, dy)
			angle := math.Atan2(dy, dx) * 180 / math.Pi
			if r >= 6 && r <= 10.5 && math.Abs(angle) > 45 {
				px = color.RGBA{255, 255, 255, 255}
			}
			img.SetRGBA(x, y, px)
		}
	}
	var pngData bytes.Buffer
	png.Encode(&pngData, img)

	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, struct{ Reserved, Type, Count uint16 }{0, 1, 1})
	binary.Write(&ico, binary.LittleEndian, struct {
		Width, Height, Colors, Reserved uint8
		Planes, BitCount                uint16
		Size, Offset                    uint32
	}{size, size, 0, 0, 1, 32, uint32(pngData.Len()), 6 + 16})
	ico.Write(pngData.Bytes())
	return ico.Bytes()
}

var (
	iconRunning = trayIcon(color.RGBA{15, 118, 110, 255})
	iconStopped = trayIcon(color.RGBA{110, 118, 129, 255})
)
