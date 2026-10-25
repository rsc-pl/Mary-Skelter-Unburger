// Package graphicsgo contains native Go primitives for the Mary Skelter
// typography pipeline. It also includes research-only TrueType/CFF1 outline parsing, GPOS Latin pair
// positioning and select Pillow-compatible mask operations. None of these
// constitute an approved production Pillow replacement.
package graphicsgo

import (
	"errors"
	"image"
	"image/color"
	"math"
)

var ErrInvalidRaster = errors.New("graphicsgo: invalid raster geometry or backing bytes")

// validateNRGBA validates image geometry and backing storage before any writes.
func validateNRGBA(im *image.NRGBA) error {
	if im == nil || im.Rect.Empty() || im.Stride < im.Rect.Dx()*4 || len(im.Pix) < (im.Rect.Dy()-1)*im.Stride+im.Rect.Dx()*4 {
		return ErrInvalidRaster
	}
	return nil
}
func validateAlpha(im *image.Alpha) error {
	if im == nil || im.Rect.Empty() || im.Stride < im.Rect.Dx() || len(im.Pix) < (im.Rect.Dy()-1)*im.Stride+im.Rect.Dx() {
		return ErrInvalidRaster
	}
	return nil
}

// shiftForDiv255 matches Pillow's integer alpha_composite rounding.
func shiftForDiv255(x uint32) uint32 { return ((x >> 8) + x) >> 8 }

// OverPixels reproduces PIL.Image.alpha_composite's straight-alpha RGBA math.
// In particular, preserving dst when src alpha == 0 is significant even for
// otherwise invisible RGB values. This is not Go's premultiplied draw.Over.
func OverPixels(dst, src color.NRGBA) color.NRGBA {
	sa, da := uint32(src.A), uint32(dst.A)
	if sa == 0 {
		return dst
	}
	blend := da * (255 - sa)
	outA255 := sa*255 + blend
	coef1 := sa * 255 * 255 * 128 / outA255
	coef2 := 255*128 - coef1
	channel := func(s, d uint8) uint8 {
		v := uint32(s)*coef1 + uint32(d)*coef2 + (0x80 << 7)
		return uint8(shiftForDiv255(v) >> 7)
	}
	return color.NRGBA{R: channel(src.R, dst.R), G: channel(src.G, dst.G), B: channel(src.B, dst.B), A: uint8(shiftForDiv255(outA255 + 0x80))}
}

// CompositeOver composites src onto dst at origin, clipping out-of-bounds
// regions like Pillow's alpha_composite. Destination is updated in-place;
// source is never mutated. Input image rectangles are allowed nonzero Min.
func CompositeOver(dst, src *image.NRGBA, origin image.Point) error {
	if err := validateNRGBA(dst); err != nil {
		return err
	}
	if err := validateNRGBA(src); err != nil {
		return err
	}
	overlap := image.Rect(origin.X, origin.Y, origin.X+src.Rect.Dx(), origin.Y+src.Rect.Dy()).Intersect(dst.Rect)
	for y := overlap.Min.Y; y < overlap.Max.Y; y++ {
		for x := overlap.Min.X; x < overlap.Max.X; x++ {
			si := (y-origin.Y)*src.Stride + (x-origin.X)*4
			di := (y-dst.Rect.Min.Y)*dst.Stride + (x-dst.Rect.Min.X)*4
			d := color.NRGBA{dst.Pix[di], dst.Pix[di+1], dst.Pix[di+2], dst.Pix[di+3]}
			s := color.NRGBA{src.Pix[si], src.Pix[si+1], src.Pix[si+2], src.Pix[si+3]}
			o := OverPixels(d, s)
			dst.Pix[di], dst.Pix[di+1], dst.Pix[di+2], dst.Pix[di+3] = o.R, o.G, o.B, o.A
		}
	}
	return nil
}

// TintMask converts a straight-alpha raster glyph mask to a colored RGBA
// layer. Alpha multiplication uses Pillow's (v*alpha+127)//255 semantics.
func TintMask(mask *image.Alpha, ink color.NRGBA) (*image.NRGBA, error) {
	if err := validateAlpha(mask); err != nil {
		return nil, err
	}
	out := image.NewNRGBA(image.Rect(0, 0, mask.Rect.Dx(), mask.Rect.Dy()))
	for y := 0; y < mask.Rect.Dy(); y++ {
		for x := 0; x < mask.Rect.Dx(); x++ {
			m := mask.Pix[y*mask.Stride+x]
			at := y*out.Stride + x*4
			out.Pix[at], out.Pix[at+1], out.Pix[at+2] = ink.R, ink.G, ink.B
			out.Pix[at+3] = uint8((uint32(m)*uint32(ink.A) + 127) / 255)
		}
	}
	return out, nil
}

// GradientMask reproduces the R32C3 scanline RGBA gradient: Python's round()
// is ties-to-even, and ImageChops.multiply truncates mask*alpha/255.
// Both ends of the interpolation are included when the height exceeds one.
func GradientMask(mask *image.Alpha, top, bottom color.NRGBA) (*image.NRGBA, error) {
	if err := validateAlpha(mask); err != nil {
		return nil, err
	}
	w, h := mask.Rect.Dx(), mask.Rect.Dy()
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		den := h - 1
		if den == 0 {
			den = 1
		}
		k := float64(y) / float64(den)
		lerp := func(a, b uint8) uint8 {
			return uint8(math.RoundToEven(float64(a)*(1-k) + float64(b)*k))
		}
		c := color.NRGBA{lerp(top.R, bottom.R), lerp(top.G, bottom.G), lerp(top.B, bottom.B), lerp(top.A, bottom.A)}
		for x := 0; x < w; x++ {
			at := y*out.Stride + x*4
			out.Pix[at], out.Pix[at+1], out.Pix[at+2] = c.R, c.G, c.B
			out.Pix[at+3] = uint8((uint32(mask.Pix[y*mask.Stride+x]) * uint32(c.A)) / 255)
		}
	}
	return out, nil
}
