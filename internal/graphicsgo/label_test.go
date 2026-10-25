package graphicsgo

import (
	"image"
	"image/color"
	"testing"
)

func TestPillowMultiLayerGlyphPaintOracle(t *testing.T) {
	size := image.Rect(0, 0, 41, 29)
	face, inner, outer, acc := image.NewAlpha(size), image.NewAlpha(size), image.NewAlpha(size), image.NewAlpha(size)
	for y := 0; y < 29; y++ {
		for x := 0; x < 41; x++ {
			f := (x*17 + y*43 + (x*y)%13) % 256
			if (x+y)%11 == 0 {
				f = 0
			}
			if (x*3+y)%17 == 0 {
				f = 255
			}
			i := y*face.Stride + x
			face.Pix[i] = uint8(f)
			vv := f + ((x+y)%3)*30
			if vv > 255 {
				vv = 255
			}
			inner.Pix[i] = uint8(vv)
			vv = f + ((x*2+y)%5)*25
			if vv > 255 {
				vv = 255
			}
			outer.Pix[i] = uint8(vv)
			if x%7 == 0 && y%3 == 0 {
				acc.Pix[i] = 255
			}
		}
	}
	result, err := PaintGlyphLayers(GlyphMasks{face, inner, outer, acc}, GlyphPaint{
		OuterEnabled: true, InnerEnabled: true, GradientEnabled: true, ShadowEnabled: true, AccentsEnabled: true,
		OuterColor: color.NRGBA{214, 12, 93, 255}, InnerColor: color.NRGBA{3, 0, 3, 255},
		GradientTop: color.NRGBA{255, 253, 255, 255}, GradientBottom: color.NRGBA{244, 120, 192, 200},
		ShadowColor: color.NRGBA{10, 2, 10, 180}, AccentColor: color.NRGBA{9, 4, 9, 230}, ShadowOffset: image.Pt(3, -2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(result.Pix); got != "7a93ba1e3d87e3630ad762e347cc5c2e392a704fa8ef87a750931cfdd97016f8" {
		t.Fatalf("Pillow R32C3 layers drift: %s", got)
	}
	if _, err := PaintGlyphLayers(GlyphMasks{face, inner, outer, acc}, GlyphPaint{ShadowBlurRadius: 1}); err == nil {
		t.Fatal("unsupported Pillow blur silently accepted")
	}
	if _, err := PaintGlyphLayers(GlyphMasks{face, inner, image.NewAlpha(image.Rect(0, 0, 2, 2)), acc}, GlyphPaint{}); err == nil {
		t.Fatal("mismatched masks accepted")
	}
}
