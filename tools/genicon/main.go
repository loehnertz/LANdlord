// Command genicon draws the LANdlord icon (a house with Wi-Fi arcs) and writes it as an ICO file
// for the tray and as PNG files for the Windows resources.
//
//	go run ./tools/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

var (
	background = color.RGBA{0x2a, 0x78, 0xd6, 0xff}
	white      = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

// draw renders the icon at size px with 4x4 supersampling for smooth edges.
func draw(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	for y := range size {
		for x := range size {
			var r, g, b, a float64
			for sy := range ss {
				for sx := range ss {
					u := (float64(x) + (float64(sx)+0.5)/ss) / float64(size)
					v := (float64(y) + (float64(sy)+0.5)/ss) / float64(size)
					c, alpha := shade(u, v)
					r += float64(c.R) * alpha
					g += float64(c.G) * alpha
					b += float64(c.B) * alpha
					a += alpha
				}
			}
			n := float64(ss * ss)
			if a > 0 {
				img.SetRGBA(x, y, color.RGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(255 * a / n)})
			}
		}
	}
	return img
}

// shade returns the colour and coverage at normalised coordinates (0..1).
func shade(u, v float64) (color.RGBA, float64) {
	// Rounded square background.
	const radius = 0.2
	dx := math.Max(math.Max(radius-u, u-(1-radius)), 0)
	dy := math.Max(math.Max(radius-v, v-(1-radius)), 0)
	if dx*dx+dy*dy > radius*radius {
		return color.RGBA{}, 0
	}
	// House body and roof.
	inBody := u >= 0.27 && u <= 0.73 && v >= 0.48 && v <= 0.82
	roofTop, roofBase := 0.2, 0.52
	halfWidth := (v - roofTop) / (roofBase - roofTop) * 0.34
	inRoof := v >= roofTop && v <= roofBase && math.Abs(u-0.5) <= halfWidth
	if inBody || inRoof {
		// Wi-Fi arcs and dot cut out of the house.
		cx, cy := 0.5, 0.74
		d := math.Hypot(u-cx, v-cy)
		angle := math.Atan2(cy-v, u-cx)
		inCone := angle > math.Pi/4 && angle < 3*math.Pi/4
		if d < 0.035 || inCone && ((d > 0.08 && d < 0.12) || (d > 0.16 && d < 0.2)) {
			return background, 1
		}
		return white, 1
	}
	return background, 1
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

// writeICO writes an ICO container with PNG-compressed images (supported since Windows Vista).
func writeICO(path string, sizes []int) {
	var images [][]byte
	for _, s := range sizes {
		images = append(images, encodePNG(draw(s)))
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s)
		if s >= 256 {
			dim = 0
		}
		_ = binary.Write(&buf, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BitCount       uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, data := range images {
		buf.Write(data)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

func main() {
	writeICO(filepath.Join("internal", "ui", "icon.ico"), []int{16, 20, 24, 32, 48, 256})
	if err := os.MkdirAll("winres", 0o755); err != nil {
		log.Fatal(err)
	}
	for _, s := range []int{16, 32, 48, 256} {
		name := "icon.png"
		if s != 256 {
			name = "icon" + itoa(s) + ".png"
		}
		if err := os.WriteFile(filepath.Join("winres", name), encodePNG(draw(s)), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
