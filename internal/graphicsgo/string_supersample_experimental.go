package graphicsgo

// Research-only whole-run supersampling. It deliberately keeps layout and
// FreeType parity gates open: this implements the Latin cmap/GPOS-kern subset,
// NOT GSUB, hinting, or the production R32C3 family renderer.
//
// The crucial distinction from single-glyph AA experiments is that glyphs are
// arranged on ONE high-resolution canvas, and the entire canvas is resized
// ONCE using the existing Pillow-matched Lanczos kernel.

import (
	"image"
	"math"
	"unicode/utf8"
)

// RasterLatinRunSupersampled draws a bounded Latin run at a fixed baseline
// (in final-size pixels). The canvas and placement remain fixed across
// supersample factors so an external Pillow oracle can compare output directly.
// No input or glyph masks are written to disk.
func (f *SFNT) RasterLatinRunSupersampled(text string, pixelSize, factor, samples int,
	finalW, finalH, baselineX, baselineY int) (*image.Alpha, error) {
	if f == nil || !utf8.ValidString(text) || len(text) == 0 || len([]rune(text)) > 32 ||
		pixelSize < 8 || pixelSize > 100 || (factor != 1 && factor != 2 && factor != 4 && factor != 8) ||
		(samples != 2 && samples != 4 && samples != 8) ||
		finalW < 32 || finalH < 32 || finalW > 1024 || finalH > 256 ||
		int64(finalW)*int64(finalH)*int64(factor*factor) > 16_000_000 ||
		baselineX < 0 || baselineY < 0 || baselineX >= finalW || baselineY >= finalH {
		return nil, ErrInvalidRaster
	}
	w, h := finalW*factor, finalH*factor
	high := image.NewAlpha(image.Rect(0, 0, w, h))
	units := float64(f.UnitsPerEm)
	if units <= 0 {
		return nil, ErrInvalidRaster
	}
	px := float64(pixelSize * factor)
	glyphs := make([]uint16, 0, len(text))
	for _, r := range text {
		// No silent replacement glyph or undetected shaping.
		if r < 32 || r > 126 {
			return nil, fontErr("unsupported Latin run character")
		}
		gid, err := f.GlyphIndex(r)
		if err != nil {
			return nil, err
		}
		glyphs = append(glyphs, gid)
	}
	pen := float64(baselineX * factor)
	baseline := baselineY * factor
	for i, gid := range glyphs {
		advance, _, err := f.HMetric(gid)
		if err != nil {
			return nil, err
		}
		placement := 0
		kern := 0
		if i+1 < len(glyphs) {
			p, err := f.GPOSPairLatin(gid, glyphs[i+1])
			if err != nil {
				return nil, err
			}
			// Disallow unimplemented vertical and second-glyph positioning
			// instead of falsely claiming complete shaping equivalence.
			if p.FirstYAdvance != 0 || p.FirstYPlacement != 0 || p.SecondYAdvance != 0 || p.SecondYPlacement != 0 || p.SecondXAdvance != 0 || p.SecondXPlacement != 0 {
				return nil, fontErr("unsupported pair positioning in whole-run experiment")
			}
			placement = p.FirstXPlacement
			kern = p.FirstXAdvance
		}
		mask, origin, err := f.RasterGlyph(gid, px, samples)
		if err != nil {
			return nil, err
		}
		x0 := int(math.Round(pen+float64(placement)*px/units)) + origin.X
		y0 := baseline + origin.Y
		for y := 0; y < mask.Rect.Dy(); y++ {
			ty := y + y0
			if ty < 0 || ty >= h {
				continue
			}
			for x := 0; x < mask.Rect.Dx(); x++ {
				tx := x + x0
				if tx < 0 || tx >= w {
					continue
				}
				v := mask.Pix[y*mask.Stride+x]
				idx := ty*high.Stride + tx
				if v > high.Pix[idx] {
					high.Pix[idx] = v
				}
			}
		}
		pen += float64(int(advance)+kern) * px / units
		if pen > float64(w)+px*2 {
			return nil, fontErr("run exceeds bounded canvas")
		}
	}
	if factor == 1 {
		return high, nil
	}
	return ResizeLanczosAlpha(high, finalW, finalH)
}
