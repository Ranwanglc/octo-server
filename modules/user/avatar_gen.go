package user

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// hslToRGBA converts HSL color values to RGBA.
// h: [0, 360), s: [0, 1], l: [0, 1]
func hslToRGBA(h, s, l float64) color.RGBA {
	c := (1.0 - absF64(2*l-1)) * s
	sector := int(h / 60.0)
	if sector >= 6 {
		sector = 5
	}
	frac := h/60.0 - float64(sector)
	xFrac := frac
	if sector%2 != 0 {
		xFrac = 1 - frac
	}
	x := c * xFrac
	m := l - c/2

	var r, g, b float64
	switch sector {
	case 0:
		r, g, b = c, x, 0
	case 1:
		r, g, b = x, c, 0
	case 2:
		r, g, b = 0, c, x
	case 3:
		r, g, b = 0, x, c
	case 4:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}

	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 0xff,
	}
}

func absF64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// generateDefaultAvatar creates a 200×200 PNG avatar: a colored circle with the
// first character of uid centered in white. All rendering uses the standard library
// plus golang.org/x/image (already an indirect dependency).
func generateDefaultAvatar(uid string) ([]byte, error) {
	const size = 200

	// Pick background color deterministically from uid bytes using HSL.
	var hash uint32
	for _, b := range []byte(uid) {
		hash = hash*31 + uint32(b)
	}
	hue := float64(hash % 360)
	bg := hslToRGBA(hue, 0.65, 0.45)

	// Create RGBA canvas, white background.
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	// Draw filled circle using squared-distance comparison (avoids sqrt per pixel).
	cx, cy := float64(size)/2, float64(size)/2
	radius := float64(size)/2 - 1
	radiusSq := radius * radius
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - cx + 0.5
			dy := float64(y) - cy + 0.5
			if dx*dx+dy*dy <= radiusSq {
				img.SetRGBA(x, y, bg)
			}
		}
	}

	// Draw the first Unicode character of uid, white, centered.
	// basicfont.Face7x13 is 7 px wide × 13 px tall.
	// We scale it up 10× by rendering on a small canvas first, then blitting.
	const scale = 10
	const glyphW, glyphH = 7, 13

	ch := 'U'
	if len(uid) > 0 {
		r := []rune(uid)[0]
		if r >= 'a' && r <= 'z' {
			r -= 32
		}
		ch = r
	}

	// Small canvas for a single glyph.
	small := image.NewRGBA(image.Rect(0, 0, glyphW, glyphH))
	draw.Draw(small, small.Bounds(), &image.Uniform{color.Transparent}, image.Point{}, draw.Src)
	fd := &font.Drawer{
		Dst:  small,
		Src:  image.NewUniform(color.White),
		Face: basicfont.Face7x13,
		Dot:  fixed.Point26_6{X: fixed.I(0), Y: fixed.I(glyphH - 2)},
	}
	fd.DrawString(string(ch))

	// Blit scaled glyph onto the circle, centered.
	scaledW := glyphW * scale
	scaledH := glyphH * scale
	offX := (size - scaledW) / 2
	offY := (size - scaledH) / 2

	for sy := 0; sy < glyphH; sy++ {
		for sx := 0; sx < glyphW; sx++ {
			src := small.RGBAAt(sx, sy)
			if src.A == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					px := offX + sx*scale + dx
					py := offY + sy*scale + dy
					if px >= 0 && px < size && py >= 0 && py < size {
						img.SetRGBA(px, py, src)
					}
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
