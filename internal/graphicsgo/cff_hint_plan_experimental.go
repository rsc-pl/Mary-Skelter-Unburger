package graphicsgo

// Experimental, native-Go Type2 hint PLAN extraction. This captures the
// information needed before implementing a FreeType-like CFF grid fitter.
// It does NOT move points, claim hinted parity, or affect GlyphOutlineCFF1.
// The legacy unhinted parser remains the sole caller when no recorder exists.
import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// CFFHintStem records one font-unit stem, before grid-fitting. Axis is x for
// vstem, y for hstem. Stem positions are delta encoded in Type2 charstrings.
type CFFHintStem struct {
	Axis       string
	Start, End float64
}
type CFFHintMask struct {
	Counter bool
	Active  []int
}
type CFFHintPrivate struct {
	BlueValues, OtherBlues, FamilyBlues, FamilyOtherBlues []float64
	StemSnapH, StemSnapV                                  []float64
	StdHW, StdVW, BlueScale, BlueShift, BlueFuzz          float64
}

// CFFHintGeometrySegment binds a source Type2 drawing operation to the mask
// active at the time of drawing. StartPoint and EndPoint are indices into the
// flattened GlyphContour returned by the same interpreter. Closing is the
// implicit end-to-start edge. Values are read-only metadata, not fitted points.
// An implicit mask (MaskIndex == -1) means all stems declared so far.
type CFFHintGeometrySegment struct {
	Contour, StartPoint, EndPoint int
	MaskIndex                     int
	Active                        []int
	Closing                       bool
	Operation                     string
}

// CFFHintSourcePoint retains an original Type2 font-unit control point; it is
// not a point from the 16-piece polygon approximation used by the legacy
// unhinted rasterizer. Keeping cubic controls is necessary for later hinting.
type CFFHintSourcePoint struct{ X, Y float64 }

// CFFHintSourceOperation is an ordered event in the original Type2 drawing
// stream. Move has one point, Line/Close two, Curve four (start, two cubic
// controls, end), and HintMask/CounterMask none. GeometryIndex links a draw to
// 's flattened-segment record, or -1 for other events, including degenerate
// closing edges. Source operations are RESEARCH metadata, not FreeType hinting.
type CFFHintSourceOperation struct {
	Kind           string
	Contour        int
	Points         []CFFHintSourcePoint
	MaskIndex      int
	MaskEventIndex int
	Active         []int
	GeometryIndex  int
	Degenerate     bool
}

type CFFHintPlan struct {
	SourceOperations []CFFHintSourceOperation
	Geometry         []CFFHintGeometrySegment
	GlyphID          uint16
	FDIndex          int
	Stems            []CFFHintStem
	Masks            []CFFHintMask
	Private          CFFHintPrivate
}

type cffHintRecorder struct {
	sourceOperations []CFFHintSourceOperation
	geometry         []CFFHintGeometrySegment
	maskIndex        int // zero-based explicit hintmask; -1 before any hintmask
	maskExplicit     bool
	currentActive    []int
	stemsOut         []CFFHintStem
	masks            []CFFHintMask
	x, y             float64
	widthSeen        bool
}

func (r *cffHintRecorder) stems(op byte, arguments []float64) error {
	values := arguments
	if len(values)%2 != 0 {
		if r.widthSeen || len(r.stemsOut) != 0 || len(r.masks) != 0 {
			return fontErr("CFF hint width after initial stems")
		}
		r.widthSeen = true
		values = values[1:]
	}
	axis := "x"
	coord := &r.x
	if op == 1 || op == 18 {
		axis = "y"
		coord = &r.y
	}
	if len(r.stemsOut)+len(values)/2 > 96 {
		return fontErr("CFF hint maximum 96 stems")
	}
	for j := 0; j < len(values); j += 2 {
		start := *coord + values[j]
		end := start + values[j+1]
		if math.IsNaN(start) || math.IsInf(start, 0) || math.IsNaN(end) || math.IsInf(end, 0) || end == start || math.Abs(start) > 1e6 || math.Abs(end) > 1e6 {
			return fontErr("CFF hint invalid stem")
		}
		r.stemsOut = append(r.stemsOut, CFFHintStem{Axis: axis, Start: start, End: end})
		*coord = end
	}
	return nil
}
func (r *cffHintRecorder) mask(op byte, mask []byte) error {
	if len(mask) != (len(r.stemsOut)+7)/8 {
		return fontErr("CFF hint mask length")
	}
	m := CFFHintMask{Counter: op == 20}
	for i := 0; i < len(r.stemsOut); i++ {
		if mask[i/8]&(byte(0x80)>>uint(i%8)) != 0 {
			m.Active = append(m.Active, i)
		}
	}
	// Bits beyond the declared number of stems must be off.
	for i := len(r.stemsOut); i < len(mask)*8; i++ {
		if mask[i/8]&(byte(0x80)>>uint(i%8)) != 0 {
			return fontErr("CFF hintmask unused bits")
		}
	}
	r.masks = append(r.masks, m)
	// A cntrmask sets counter-control groups, not the currently active hint
	// substitution state. Only hintmask changes which stems draw later edges.
	if !m.Counter {
		r.maskExplicit = true
		r.maskIndex = len(r.masks) - 1
		r.currentActive = append([]int(nil), m.Active...)
	}
	return nil
}

// geometryEvent records an immutable stem-state snapshot for one drawing
// operation. The number of stems grows only while Type2 hint declarations are
// processed. An explicit hintmask may be sparse (or empty). The metadata is
// deliberately NOT used by any renderer until source-to-point parity is proven.
func (r *cffHintRecorder) geometryEvent(contour, first, last int, operation string, closing bool) {
	active := append([]int(nil), r.currentActive...)
	index := -1
	if r.maskExplicit {
		index = r.maskIndex
	} else {
		active = make([]int, len(r.stemsOut))
		for i := range active {
			active[i] = i
		}
	}
	r.geometry = append(r.geometry, CFFHintGeometrySegment{
		Contour: contour, StartPoint: first, EndPoint: last,
		MaskIndex: index, Active: active, Closing: closing,
		Operation: operation,
	})
}

// sourceEvent captures original Type2 operations *before* Bézier flattening.
// The source order includes changes in hintmask and cntrmask, zero-length
// lines, and geometrically coincident closing edges. Counter masks are events,
// but do not alter the active drawing mask.
func (r *cffHintRecorder) sourceEvent(kind string, contour int, coords []cffPt, geometryIndex, maskEventIndex int) {
	active := append([]int(nil), r.currentActive...)
	maskIndex := -1
	if r.maskExplicit {
		maskIndex = r.maskIndex
	} else {
		active = make([]int, len(r.stemsOut))
		for i := range active {
			active[i] = i
		}
	}
	points := make([]CFFHintSourcePoint, len(coords))
	for i, v := range coords {
		points[i] = CFFHintSourcePoint{X: v.x, Y: v.y}
	}
	degenerate := (kind == "line" || kind == "close") && len(coords) == 2 && coords[0] == coords[1]
	r.sourceOperations = append(r.sourceOperations, CFFHintSourceOperation{
		Kind: kind, Contour: contour, Points: points,
		MaskIndex: maskIndex, MaskEventIndex: maskEventIndex,
		Active: active, GeometryIndex: geometryIndex, Degenerate: degenerate,
	})
}

// cffPrivateBytes keeps the source's exact bounds checks and does not decode
// any font-private data into a public output or change unhinted geometry.
func cffPrivateBytes(b []byte, dict map[int][]float64) ([]byte, error) {
	priv := dict[18]
	if len(priv) == 0 {
		return nil, nil
	}
	if len(priv) != 2 {
		return nil, fontErr("CFF Private arguments")
	}
	size, e := cffInt(priv[0])
	if e != nil {
		return nil, e
	}
	start, e := cffInt(priv[1])
	if e != nil {
		return nil, e
	}
	if start > len(b) || size > len(b)-start {
		return nil, fontErr("CFF Private bounds")
	}
	return b[start : start+size], nil
}

// CFF DICT BCD real numbers (for example BlueScale=0.039625). The legacy
// cffNum deliberately ignored these where only offsets were needed.
func cffPrivateDict(b []byte) (map[int][]float64, error) {
	out := map[int][]float64{}
	var args []float64
	for at := 0; at < len(b); {
		op := int(b[at])
		if op <= 21 && op != 28 && op != 29 {
			at++
			if op == 12 {
				if at >= len(b) {
					return nil, fontErr("CFF hint private escape")
				}
				op = 1200 + int(b[at])
				at++
			}
			out[op] = append([]float64(nil), args...)
			args = nil
			continue
		}
		var v float64
		if op == 30 {
			at++
			var sb strings.Builder
			terminated := false
			for j := 0; j < 64 && !terminated; j++ {
				if at >= len(b) {
					return nil, fontErr("CFF hint BCD truncated")
				}
				item := b[at]
				at++
				for _, n := range []byte{item >> 4, item & 0xf} {
					switch n {
					case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9:
						sb.WriteByte('0' + n)
					case 10:
						sb.WriteByte('.')
					case 11:
						sb.WriteByte('E')
					case 12:
						sb.WriteString("E-")
					case 14:
						sb.WriteByte('-')
					case 15:
						terminated = true
					default:
						return nil, fontErr("CFF hint BCD invalid digit")
					}
					if terminated {
						break
					}
				}
			}
			if !terminated {
				return nil, fontErr("CFF hint BCD too long")
			}
			var err error
			v, err = strconv.ParseFloat(sb.String(), 64)
			if err != nil {
				return nil, fontErr("CFF hint BCD invalid float")
			}
		} else {
			var err error
			v, err = cffNum(b, &at)
			if err != nil {
				return nil, err
			}
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fontErr("CFF hint private nonfinite")
		}
		args = append(args, v)
		if len(args) > 96 {
			return nil, fontErr("CFF hint private DICT stack")
		}
	}
	return out, nil
}
func cffPrivateHintInfo(b []byte) (CFFHintPrivate, error) {
	var out CFFHintPrivate
	d, e := cffPrivateDict(b)
	if e != nil {
		return out, e
	}
	delta := func(k int, max int) ([]float64, error) {
		vals := d[k]
		if len(vals) > max || len(vals)%2 != 0 {
			return nil, fontErr("CFF hint blue count")
		}
		acc := 0.0
		result := make([]float64, 0, len(vals))
		for _, v := range vals {
			acc += v
			if math.Abs(acc) > 1e6 {
				return nil, fontErr("CFF hint blue overflow")
			}
			result = append(result, acc)
		}
		return result, nil
	}
	if out.BlueValues, e = delta(6, 14); e != nil {
		return out, e
	}
	if out.OtherBlues, e = delta(7, 10); e != nil {
		return out, e
	}
	if out.FamilyBlues, e = delta(8, 14); e != nil {
		return out, e
	}
	if out.FamilyOtherBlues, e = delta(9, 10); e != nil {
		return out, e
	}
	stemSnaps := func(key int) ([]float64, error) {
		vals := d[key]
		if len(vals) > 24 {
			return nil, fontErr("CFF hint StemSnap count")
		}
		acc := 0.0
		out := make([]float64, 0, len(vals))
		for _, val := range vals {
			acc += val
			if acc <= 0 || math.Abs(acc) > 1e6 {
				return nil, fontErr("CFF hint StemSnap value")
			}
			out = append(out, acc)
		}
		return out, nil
	}
	if out.StemSnapH, e = stemSnaps(1212); e != nil {
		return out, e
	}
	if out.StemSnapV, e = stemSnaps(1213); e != nil {
		return out, e
	}
	val := func(k int, def float64) (float64, error) {
		x := d[k]
		if len(x) == 0 {
			return def, nil
		}
		if len(x) != 1 {
			return 0, fontErr(fmt.Sprintf("CFF hint private %d arity", k))
		}
		return x[0], nil
	}
	if out.StdHW, e = val(10, 0); e != nil {
		return out, e
	}
	if out.StdVW, e = val(11, 0); e != nil {
		return out, e
	}
	if out.BlueScale, e = val(1209, 0.039625); e != nil {
		return out, e
	}
	if out.BlueShift, e = val(1210, 7); e != nil {
		return out, e
	}
	if out.BlueFuzz, e = val(1211, 1); e != nil {
		return out, e
	}
	return out, nil
}

// GlyphCFFHintPlan interprets the original Type2 charstring including selected
// FD subroutines, but only exposes source stem/mask metadata, without hinting.
func (f *SFNT) GlyphCFFHintPlan(gid uint16) (CFFHintPlan, error) {
	p := CFFHintPlan{GlyphID: gid}
	if f == nil || gid >= f.NumGlyphs || f.Outline != CFF1Outline {
		return p, fontErr("CFF hint glyph index or format")
	}
	refs, e := f.readCFF()
	if e != nil {
		return p, e
	}
	fd := 0
	if len(refs.fdSelect) != 0 {
		fd = int(refs.fdSelect[gid])
		refs.local = refs.fdLocals[fd]
	}
	p.FDIndex = fd
	if fd < 0 || fd >= len(refs.privateHints) {
		return p, fontErr("CFF hint private FD")
	}
	if p.Private, e = cffPrivateHintInfo(refs.privateHints[fd]); e != nil {
		return p, e
	}
	rec := &cffHintRecorder{}
	c := &cffBuilder{hintRecorder: rec}
	if e = c.execute(refs.charstrings[gid], refs, 0); e != nil {
		return p, e
	}
	if c.stemCount != len(rec.stemsOut) {
		return p, fontErr("CFF hint stem count mismatch")
	}
	p.Stems = rec.stemsOut
	p.Masks = rec.masks
	p.Geometry = rec.geometry
	p.SourceOperations = rec.sourceOperations
	return p, nil
}
