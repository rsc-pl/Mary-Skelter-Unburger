package graphicsgo

// Unhinted R32C3 Latin text geometry, RESEARCH ONLY. This is an independently
// testable Go-only candidate, NOT a FreeType textbbox replacement. The
// accepted Python renderer uses hinted Pillow/FreeType ink and layout metrics.
// Pair adjustments use the existing GPOS/kern subset; GSUB and complex script
// shaping are intentionally rejected instead of inventing substitute glyphs.

import (
	"image"
	"math"
	"unicode/utf8"
)

// MeasureLatinTextBBoxUnhinted estimates Pillow's default (left, ascender)
// textbbox for an unhinted printable ASCII run. px is a positive integer at
// the HIGH resolution requested by the R32C3 fit code. Curves are flattened
// by the existing limited SFNT outline implementation. It is safe for future
// tests to reject a font/case rather than silently provide a fake exact bbox.
func (f *SFNT) MeasureLatinTextBBoxUnhinted(text string, px int) (image.Rectangle, error) {
	if f == nil || !utf8.ValidString(text) || text == "" || utf8.RuneCountInString(text) > 64 || px < 1 || px > 1024 || f.UnitsPerEm == 0 {
		return image.Rectangle{}, ErrInvalidRaster
	}
	glyphs := make([]uint16, 0, len(text))
	for _, r := range text {
		if r < 32 || r > 126 {
			return image.Rectangle{}, fontErr("R32C3 unhinted Latin bbox supports printable ASCII only")
		}
		gid, err := f.GlyphIndex(r)
		if err != nil {
			return image.Rectangle{}, err
		}
		glyphs = append(glyphs, gid)
	}
	scale := float64(px) / float64(f.UnitsPerEm)
	pen, loX, hiX, loY, hiY := 0.0, math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for i, gid := range glyphs {
		advance, _, err := f.HMetric(gid)
		if err != nil {
			return image.Rectangle{}, err
		}
		placement, kern := 0, 0
		if i+1 < len(glyphs) {
			p, err := f.GPOSPairLatin(gid, glyphs[i+1])
			if err != nil {
				return image.Rectangle{}, err
			}
			if p.FirstYAdvance != 0 || p.FirstYPlacement != 0 || p.SecondYAdvance != 0 || p.SecondYPlacement != 0 || p.SecondXAdvance != 0 || p.SecondXPlacement != 0 {
				return image.Rectangle{}, fontErr("unsupported GPOS second-glyph or vertical positioning")
			}
			placement, kern = p.FirstXPlacement, p.FirstXAdvance
		}
		var outline GlyphOutline
		if f.Outline == CFF1Outline {
			outline, err = f.GlyphOutlineCFF1(gid)
		} else {
			outline, err = f.GlyphOutlineTTF(gid)
		}
		if err != nil {
			return image.Rectangle{}, err
		}
		for _, c := range outline {
			for _, p := range c {
				x, y := pen+(p.X+float64(placement))*scale, p.Y*scale
				if !finiteWarp(x) || !finiteWarp(y) {
					return image.Rectangle{}, ErrInvalidRaster
				}
				loX = math.Min(loX, x)
				hiX = math.Max(hiX, x)
				loY = math.Min(loY, y)
				hiY = math.Max(hiY, y)
			}
		}
		pen += float64(int(advance)+kern) * scale
	}
	if math.IsInf(loX, 0) || math.IsInf(loY, 0) {
		return image.Rectangle{}, fontErr("empty glyph outlines")
	}
	asc := math.Ceil(float64(f.Ascent) * scale)
	out := image.Rect(int(math.Min(0, math.Floor(loX))), int(math.Floor(asc-hiY)),
		int(math.Max(math.Round(pen), math.Ceil(hiX))), int(math.Ceil(asc-loY)))
	if out.Empty() || out.Dx() > 16384 || out.Dy() > 4096 {
		return image.Rectangle{}, ErrInvalidRaster
	}
	return out, nil
}
