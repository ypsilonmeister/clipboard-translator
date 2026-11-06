// Command mkicon builds assets/icon.ico (multi-size) and assets/icon.png from
// assets/icon-source.jpg. The source is a rounded square on a white backdrop;
// the backdrop is cut away so the corners are transparent.
//
// Run from the repository root:
//
//	go run ./tools/mkicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"log"
	"os"
)

func main() {
	f, err := os.Open("assets/icon-source.jpg")
	if err != nil {
		log.Fatal(err)
	}
	src, err := jpeg.Decode(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	img := cutBackground(src)

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var pngs [][]byte
	for _, n := range sizes {
		pngs = append(pngs, encodePNG(resize(img, n)))
	}
	if err := os.WriteFile("assets/icon.ico", buildICO(sizes, pngs), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("assets/icon.png", encodePNG(resize(img, 512)), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote assets/icon.ico (%v) and assets/icon.png", sizes)
}

// cutBackground makes the white area outside the rounded square transparent.
// The backdrop is found by flood-filling from the four corners; pixels near its
// border keep the icon colour with an alpha derived from how white they are, so
// the edge stays anti-aliased.
func cutBackground(src image.Image) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out.SetNRGBA(x, y, color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 255})
		}
	}
	// Solid icon colour, sampled from the middle of the left edge.
	base := out.NRGBAAt(w/30, h/2)

	isWhite := func(c color.NRGBA) bool { return c.R > 200 && c.G > 200 && c.B > 200 }
	bg := make([]bool, w*h)
	var stack [][2]int
	push := func(x, y int) {
		if x < 0 || y < 0 || x >= w || y >= h || bg[y*w+x] || !isWhite(out.NRGBAAt(x, y)) {
			return
		}
		bg[y*w+x] = true
		stack = append(stack, [2]int{x, y})
	}
	push(0, 0)
	push(w-1, 0)
	push(0, h-1)
	push(w-1, h-1)
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		push(p[0]+1, p[1])
		push(p[0]-1, p[1])
		push(p[0], p[1]+1)
		push(p[0], p[1]-1)
	}

	const edge = 3
	nearBG := func(x, y int) bool {
		for dy := -edge; dy <= edge; dy++ {
			for dx := -edge; dx <= edge; dx++ {
				xx, yy := x+dx, y+dy
				if xx >= 0 && yy >= 0 && xx < w && yy < h && bg[yy*w+xx] {
					return true
				}
			}
		}
		return false
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case bg[y*w+x]:
				out.SetNRGBA(x, y, color.NRGBA{})
			case nearBG(x, y):
				c := out.NRGBAAt(x, y)
				t := float64(255-int(c.R)) / float64(255-int(base.R))
				if t > 1 {
					t = 1
				}
				if t < 0 {
					t = 0
				}
				out.SetNRGBA(x, y, color.NRGBA{base.R, base.G, base.B, uint8(t*255 + 0.5)})
			}
		}
	}
	return out
}

// resize shrinks img to n×n with an area average on premultiplied alpha.
func resize(img *image.NRGBA, n int) *image.NRGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		y0, y1 := y*h/n, (y+1)*h/n
		for x := 0; x < n; x++ {
			x0, x1 := x*w/n, (x+1)*w/n
			var r, g, b, a, cnt float64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					c := img.NRGBAAt(xx, yy)
					al := float64(c.A)
					r += float64(c.R) * al
					g += float64(c.G) * al
					b += float64(c.B) * al
					a += al
					cnt++
				}
			}
			if a == 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{
				uint8(r/a + 0.5), uint8(g/a + 0.5), uint8(b/a + 0.5), uint8(a/cnt + 0.5),
			})
		}
	}
	return out
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

// buildICO packs PNG-compressed images into an .ico container.
func buildICO(sizes []int, pngs [][]byte) []byte {
	var buf bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&buf, le, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, n := range sizes {
		dim := byte(n)
		if n >= 256 {
			dim = 0 // 0 means 256
		}
		buf.Write([]byte{dim, dim, 0, 0})
		binary.Write(&buf, le, uint16(1))  // planes
		binary.Write(&buf, le, uint16(32)) // bits per pixel
		binary.Write(&buf, le, uint32(len(pngs[i])))
		binary.Write(&buf, le, uint32(offset))
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes()
}
