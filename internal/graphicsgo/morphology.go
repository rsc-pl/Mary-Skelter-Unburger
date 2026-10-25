package graphicsgo

// Native implementations of Pillow's grayscale MaxFilter and its RGBA outline
// recipe used by the accepted R32C3 graphics builder. Only this specific path
// is claimed: this is not a general font stroker or an antialias parity gate.
import (
	"fmt"
	"image"
	"image/color"
)

// MaxFilterAlpha matches Pillow ImageFilter.MaxFilter on an L image. Kernel
// sizes must be odd >=3. Pixel neighborhoods are clipped at the image border.
// The source is never modified; non-zero Rect.Min/Stride are supported.
func MaxFilterAlpha(src *image.Alpha, kernel int) (*image.Alpha, error) {
	if err := validateAlpha(src); err != nil {
		return nil, err
	}
	if kernel < 3 || kernel%2 == 0 || kernel > 255 {
		return nil, fmt.Errorf("%w: unsupported MaxFilter kernel %d", ErrInvalidRaster, kernel)
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	// Refuse unbounded attacker dimensions, not an assumption about project art.
	if w > 32768 || h > 32768 {
		return nil, ErrInvalidRaster
	}
	if uint64(w)*uint64(h)*uint64(kernel) > 400000000 {
		return nil, fmt.Errorf("%w: filter work budget", ErrInvalidRaster)
	}
	// Square max filters are separable. Two bounded 1-D passes preserve
	// Pillow's results exactly while reducing O(WH*K*K) to O(WH*K).
	interim := image.NewAlpha(image.Rect(0, 0, w, h))
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	r := kernel / 2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var m uint8
			for dx := -r; dx <= r; dx++ {
				xx := x + dx
				if xx < 0 || xx >= w {
					continue
				}
				v := src.Pix[y*src.Stride+xx]
				if v > m {
					m = v
				}
			}
			interim.Pix[y*interim.Stride+x] = m
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var m uint8
			for dy := -r; dy <= r; dy++ {
				yy := y + dy
				if yy < 0 || yy >= h {
					continue
				}
				v := interim.Pix[yy*interim.Stride+x]
				if v > m {
					m = v
				}
			}
			out.Pix[y*out.Stride+x] = m
		}
	}
	return out, nil
}

// AddRGBAOutline reproduces the explicit _add_rgba_outline() logic in the
// accepted Python reference: pad, MaxFilter(A), RGBA Image.composite between
// solid ink and transparent (which multiplies ALL four channels), Pillow
// alpha_composite of that layer, followed by the original image.
// Pillow's composite for 8-bit channels rounds (a*m+127)/255.
func AddRGBAOutline(src *image.NRGBA, ink color.NRGBA, radius int) (*image.NRGBA, error) {
	if err := validateNRGBA(src); err != nil {
		return nil, err
	}
	if radius < 1 || radius > 255 {
		return nil, fmt.Errorf("%w: outline radius", ErrInvalidRaster)
	}
	kernel := radius
	if kernel%2 == 0 {
		kernel++
	}
	if kernel < 3 {
		return nil, fmt.Errorf("%w: Pillow MaxFilter kernel %d", ErrInvalidRaster, kernel)
	}
	a := image.NewAlpha(image.Rect(0, 0, src.Rect.Dx(), src.Rect.Dy()))
	for y := 0; y < a.Rect.Dy(); y++ {
		for x := 0; x < a.Rect.Dx(); x++ {
			a.Pix[y*a.Stride+x] = src.Pix[y*src.Stride+x*4+3]
		}
	}
	dilated, e := MaxFilterAlpha(a, kernel)
	if e != nil {
		return nil, e
	}
	pad := kernel/2 + 2
	if pad < 2 {
		pad = 2
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if w+2*pad > 32768 || h+2*pad > 32768 {
		return nil, ErrInvalidRaster
	}
	out := image.NewNRGBA(image.Rect(0, 0, w+2*pad, h+2*pad))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m := uint32(dilated.Pix[y*dilated.Stride+x])
			at := (y+pad)*out.Stride + (x+pad)*4
			c := color.NRGBA{uint8((uint32(ink.R)*m + 127) / 255), uint8((uint32(ink.G)*m + 127) / 255), uint8((uint32(ink.B)*m + 127) / 255), uint8((uint32(ink.A)*m + 127) / 255)}
			o := OverPixels(color.NRGBA{}, c)
			out.Pix[at], out.Pix[at+1], out.Pix[at+2], out.Pix[at+3] = o.R, o.G, o.B, o.A
		}
	}
	if e = CompositeOver(out, src, image.Pt(pad, pad)); e != nil {
		return nil, e
	}
	return out, nil
}
