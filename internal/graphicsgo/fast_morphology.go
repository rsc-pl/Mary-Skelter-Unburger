package graphicsgo

// legacy RESEARCH ONLY. Efficient but exact byte-equivalent grayscale
// morphology. Historical /renderers remain byte-frozen; do not
// silently change their accepted behavior or production backend.
import (
	"fmt"
	"image"
	"math"
)

// MaxFilterAlphaFast reproduces the Pillow-style clipped square
// neighborhood maximum in O(W*H) independently of kernel radius.
func MaxFilterAlphaFast(src *image.Alpha, kernel int) (*image.Alpha, error) {
	if err := validateAlpha(src); err != nil {
		return nil, err
	}
	if kernel < 3 || kernel%2 == 0 || kernel > 255 {
		return nil, fmt.Errorf("%w: invalid kernel", ErrInvalidRaster)
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if w > 32768 || h > 32768 || int64(w)*int64(h) > 64_000_000 {
		return nil, ErrInvalidRaster
	}
	mid := image.NewAlpha(image.Rect(0, 0, w, h))
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	queue := make([]int, max(w, h))
	r := kernel / 2
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride:]
		head, tail, inserted := 0, 0, 0
		for x := 0; x < w; x++ {
			limit := min(w-1, x+r)
			for inserted <= limit {
				v := row[inserted]
				for tail > head && row[queue[tail-1]] <= v {
					tail--
				}
				queue[tail] = inserted
				tail++
				inserted++
			}
			for head < tail && queue[head] < x-r {
				head++
			}
			mid.Pix[y*mid.Stride+x] = row[queue[head]]
		}
	}
	for x := 0; x < w; x++ {
		head, tail, inserted := 0, 0, 0
		for y := 0; y < h; y++ {
			limit := min(h-1, y+r)
			for inserted <= limit {
				v := mid.Pix[inserted*mid.Stride+x]
				for tail > head && mid.Pix[queue[tail-1]*mid.Stride+x] <= v {
					tail--
				}
				queue[tail] = inserted
				tail++
				inserted++
			}
			for head < tail && queue[head] < y-r {
				head++
			}
			out.Pix[y*out.Stride+x] = mid.Pix[queue[head]*mid.Stride+x]
		}
	}
	return out, nil
}

// RadialDilateAlphaFast exactly reproduces the original experimental
// grayscale Euclidean-disk neighborhood dilation. Uses a row sliding max
// (O(W*H*radius)) rather than explicitly iterating O(radius²) neighbors.
func RadialDilateAlphaFast(src *image.Alpha, radius int) (*image.Alpha, error) {
	if err := validateAlpha(src); err != nil {
		return nil, err
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if radius < 0 || radius > 128 || int64(w)*int64(h) > 24_000_000 || int64(w)*int64(h)*int64(2*radius+1) > 400_000_000 {
		return nil, ErrInvalidRaster
	}
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	if radius == 0 {
		for y := 0; y < h; y++ {
			copy(out.Pix[y*out.Stride:y*out.Stride+w], src.Pix[y*src.Stride:y*src.Stride+w])
		}
		return out, nil
	}
	queue := make([]int, w)
	rowResult := make([]byte, w)
	xExtent := make([]int, 2*radius+1)
	for dy := -radius; dy <= radius; dy++ {
		lim := radius*radius - dy*dy
		r := int(math.Sqrt(float64(lim)))
		for (r+1)*(r+1) <= lim {
			r++
		}
		for r*r > lim {
			r--
		}
		xExtent[dy+radius] = r
	}
	for y := 0; y < h; y++ {
		dst := out.Pix[y*out.Stride : y*out.Stride+w]
		for dy := -radius; dy <= radius; dy++ {
			yy := y + dy
			if yy < 0 || yy >= h {
				continue
			}
			row := src.Pix[yy*src.Stride : yy*src.Stride+w]
			r := xExtent[dy+radius]
			head, tail, inserted := 0, 0, 0
			for x := 0; x < w; x++ {
				limit := min(w-1, x+r)
				for inserted <= limit {
					v := row[inserted]
					for tail > head && row[queue[tail-1]] <= v {
						tail--
					}
					queue[tail] = inserted
					tail++
					inserted++
				}
				for head < tail && queue[head] < x-r {
					head++
				}
				rowResult[x] = row[queue[head]]
			}
			for x, v := range rowResult {
				if v > dst[x] {
					dst[x] = v
				}
			}
		}
	}
	return out, nil
}
