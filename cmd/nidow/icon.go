package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// trayIcon draws Nido's logo, a nest of curly braces holding three eggs on a rounded square, at the
// sizes Windows uses in the tray (100% to 300% scaling) inside one ICO. Windows accepts ICO files
// that wrap PNG images, which keeps this free of image assets.
func trayIcon(top, bottom color.RGBA) []byte {
	var images [][]byte
	sizes := []int{16, 20, 24, 32, 48}
	for _, size := range sizes {
		var buf bytes.Buffer
		png.Encode(&buf, drawLogo(size, top, bottom))
		images = append(images, buf.Bytes())
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, struct{ Reserved, Type, Count uint16 }{0, 1, uint16(len(images))})
	offset := 6 + 16*len(images)
	for i, data := range images {
		binary.Write(&ico, binary.LittleEndian, struct {
			Width, Height, Colors, Reserved uint8
			Planes, BitCount                uint16
			Size, Offset                    uint32
		}{uint8(sizes[i]), uint8(sizes[i]), 0, 0, 1, 32, uint32(len(data)), uint32(offset)})
		offset += len(data)
	}
	for _, data := range images {
		ico.Write(data)
	}
	return ico.Bytes()
}

type point struct{ x, y float64 }

// cubic samples a Bézier curve (without its first point).
func cubic(p0, p1, p2, p3 point, n int) []point {
	out := make([]point, 0, n)
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		out = append(out, point{
			u*u*u*p0.x + 3*u*u*t*p1.x + 3*u*t*t*p2.x + t*t*t*p3.x,
			u*u*u*p0.y + 3*u*u*t*p1.y + 3*u*t*t*p2.y + t*t*t*p3.y,
		})
	}
	return out
}

// leftBrace is the logo's "{" in its 64×64 units, the same path as the SVG of the panel.
func leftBrace() []point {
	p := []point{{23, 14}}
	p = append(p, cubic(point{23, 14}, point{17, 14}, point{17, 18}, point{17, 24}, 8)...)
	p = append(p, point{17, 28})
	p = append(p, cubic(point{17, 28}, point{17, 31}, point{15, 32}, point{12, 32}, 6)...)
	p = append(p, cubic(point{12, 32}, point{15, 32}, point{17, 33}, point{17, 36}, 6)...)
	p = append(p, point{17, 40})
	return append(p, cubic(point{17, 40}, point{17, 46}, point{17, 50}, point{23, 50}, 8)...)
}

func distance(q point, line []point) float64 {
	best := math.Inf(1)
	for i := 1; i < len(line); i++ {
		a, b := line[i-1], line[i]
		dx, dy := b.x-a.x, b.y-a.y
		t := 0.0
		if l := dx*dx + dy*dy; l > 0 {
			t = math.Max(0, math.Min(1, ((q.x-a.x)*dx+(q.y-a.y)*dy)/l))
		}
		best = math.Min(best, math.Hypot(q.x-a.x-t*dx, q.y-a.y-t*dy))
	}
	return best
}

// drawLogo renders the logo at size×size pixels, with 4×4 samples per pixel for smooth edges.
func drawLogo(size int, top, bottom color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	scale := float64(size) / 64
	left := leftBrace()
	right := make([]point, len(left))
	for i, p := range left {
		right[i] = point{64 - p.x, p.y}
	}
	half := math.Max(4.5, 1.3/scale) / 2 // strokes stay over a pixel wide at 16 px
	eggs := [][4]float64{{25.6, 38, 3.7, 4.6}, {32, 34.5, 3.7, 4.6}, {38.4, 38, 3.7, 4.6}}
	cream := [3]float64{255, 247, 237}
	const samples = 4
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					q := point{(float64(x) + (float64(sx)+0.5)/samples) / scale, (float64(y) + (float64(sy)+0.5)/samples) / scale}
					// Rounded square: 60 units wide with corners of radius 16.
					if math.Hypot(math.Max(math.Abs(q.x-32)-14, 0), math.Max(math.Abs(q.y-32)-14, 0)) > 16 {
						continue
					}
					t := (q.x + q.y) / 128 // diagonal gradient
					c := [3]float64{
						float64(top.R) + (float64(bottom.R)-float64(top.R))*t,
						float64(top.G) + (float64(bottom.G)-float64(top.G))*t,
						float64(top.B) + (float64(bottom.B)-float64(top.B))*t,
					}
					mark := distance(q, left) <= half || distance(q, right) <= half
					for _, e := range eggs {
						dx, dy := (q.x-e[0])/e[2], (q.y-e[1])/e[3]
						mark = mark || dx*dx+dy*dy <= 1
					}
					if mark {
						c = cream
					}
					r, g, b, a = r+c[0], g+c[1], b+c[2], a+255
				}
			}
			n := float64(samples * samples)
			img.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / n)})
		}
	}
	return img
}

var (
	iconRunning = trayIcon(color.RGBA{245, 158, 11, 255}, color.RGBA{180, 83, 9, 255})
	iconStopped = trayIcon(color.RGBA{168, 162, 158, 255}, color.RGBA{87, 83, 78, 255})
)
