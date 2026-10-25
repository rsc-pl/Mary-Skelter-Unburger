package graphicsgo

// Native, bounded Pillow L/RGBA getbbox() and crop() compatibility.
// Pillow RGBA.getbbox() inspects alpha by default in Pillow 12.3.0;
// older Pillow versions could inspect all channels. We explicitly model the
// accepted Pillow 12.3.0 default and distinguish image.getchannel('A').
import "image"

// BBoxAlpha returns the tight nonzero rectangle in the original Go coordinate
// system. An empty mask has ok=false, matching Pillow's None.
func BBoxAlpha(src *image.Alpha) (rect image.Rectangle, ok bool, err error) {
	if err = validateAlpha(src); err != nil {
		return
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	left, top, right, bottom := w, h, 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if src.Pix[y*src.Stride+x] != 0 {
				if x < left {
					left = x
				}
				if y < top {
					top = y
				}
				if x+1 > right {
					right = x + 1
				}
				if y+1 > bottom {
					bottom = y + 1
				}
			}
		}
	}
	if right <= left || bottom <= top {
		return image.Rectangle{}, false, nil
	}
	return image.Rect(src.Rect.Min.X+left, src.Rect.Min.Y+top, src.Rect.Min.X+right, src.Rect.Min.Y+bottom), true, nil
}

// BBoxNRGBA emulates Pillow 12.3 RGBA.getbbox() (alpha-only), not a scan
// across all RGB channels. Pixel RGB beneath zero alpha does not expand it.
func BBoxNRGBA(src *image.NRGBA) (rect image.Rectangle, ok bool, err error) {
	if err = validateNRGBA(src); err != nil {
		return
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	left, top, right, bottom := w, h, 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if src.Pix[y*src.Stride+4*x+3] != 0 {
				if x < left {
					left = x
				}
				if y < top {
					top = y
				}
				if x+1 > right {
					right = x + 1
				}
				if y+1 > bottom {
					bottom = y + 1
				}
			}
		}
	}
	if right <= left || bottom <= top {
		return image.Rectangle{}, false, nil
	}
	return image.Rect(src.Rect.Min.X+left, src.Rect.Min.Y+top, src.Rect.Min.X+right, src.Rect.Min.Y+bottom), true, nil
}

func validCrop(r image.Rectangle) bool {
	w, h := int64(r.Max.X)-int64(r.Min.X), int64(r.Max.Y)-int64(r.Min.Y)
	return w > 0 && h > 0 && w <= 32768 && h <= 32768 && w*h <= 64_000_000
}

// CropAlphaPillow copies the source rectangle to an origin-zero image. Pixels
// outside the source are zero, exactly as Pillow crop() for mode L. This
// explicit API rejects empty crops as unsupported by game recipes.
func CropAlphaPillow(src *image.Alpha, rect image.Rectangle) (*image.Alpha, error) {
	if e := validateAlpha(src); e != nil {
		return nil, e
	}
	if !validCrop(rect) {
		return nil, ErrInvalidRaster
	}
	dst := image.NewAlpha(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	over := rect.Intersect(src.Rect)
	for y := over.Min.Y; y < over.Max.Y; y++ {
		si := (y-src.Rect.Min.Y)*src.Stride + over.Min.X - src.Rect.Min.X
		di := (y-rect.Min.Y)*dst.Stride + over.Min.X - rect.Min.X
		copy(dst.Pix[di:di+over.Dx()], src.Pix[si:si+over.Dx()])
	}
	return dst, nil
}

// CropNRGBAPillow copies straight-alpha RGBA channels, including RGB under
// transparent pixels. An out-of-source area is zero, never edge-extended.
func CropNRGBAPillow(src *image.NRGBA, rect image.Rectangle) (*image.NRGBA, error) {
	if e := validateNRGBA(src); e != nil {
		return nil, e
	}
	if !validCrop(rect) {
		return nil, ErrInvalidRaster
	}
	dst := image.NewNRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	over := rect.Intersect(src.Rect)
	for y := over.Min.Y; y < over.Max.Y; y++ {
		si := (y-src.Rect.Min.Y)*src.Stride + 4*(over.Min.X-src.Rect.Min.X)
		di := (y-rect.Min.Y)*dst.Stride + 4*(over.Min.X-rect.Min.X)
		copy(dst.Pix[di:di+4*over.Dx()], src.Pix[si:si+4*over.Dx()])
	}
	return dst, nil
}
