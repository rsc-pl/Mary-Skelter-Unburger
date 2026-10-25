package main

// legacy interrupted-recovery laboratory. This is intentionally NOT in
// renderProvenFamily: it produces *candidates* for source/owner comparison,
// not an approved product patch. It never writes a game DAT or TID.
//
// The earlier rejected draft displaced dark-blue effects too far below the
// bright face. Keep face, stroke, extrusion and shadow as independent masks;
// this makes adjusting only the effects possible without moving white glyphs.
import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
)

const resultID = "MS2_RESULT_NAMES"
const resultSHA = "3af42766364633da20762a59c3c3054f52179548ae0604a8265d773a0a980a14"

type resultPreset struct {
	Face            string   `json:"font_face"`
	Nominal         int      `json:"nominal_size"`
	Minimum         int      `json:"min_size"`
	HScale          float64  `json:"horizontal_scale"`
	HMinimum        float64  `json:"min_horizontal_scale"`
	VScale          float64  `json:"vertical_scale"`
	Shear           float64  `json:"shear_x"`
	StrokeWidth     int      `json:"stroke_width"`
	Stroke          [4]uint8 `json:"stroke_rgba"`
	FillTop         [4]uint8 `json:"fill_gradient_top_rgba"`
	FillBottom      [4]uint8 `json:"fill_gradient_bottom_rgba"`
	ExtrusionWidth  int      `json:"extrusion_stroke_width"`
	ExtrusionTop    [4]uint8 `json:"extrusion_gradient_top_rgba"`
	ExtrusionBottom [4]uint8 `json:"extrusion_gradient_bottom_rgba"`
	ExtrusionOffset [2]int   `json:"extrusion_offset"`
	ShadowOffset    [2]int   `json:"shadow_offset"`
	Shadow          [4]uint8 `json:"shadow_rgba"`
	ShadowWidth     int      `json:"shadow_stroke_width"`
	ShadowBlur      float64  `json:"shadow_blur_radius"`
	Bleed           [3]uint8 `json:"transparent_bleed_rgb"`
}

type resultAsset struct {
	familyAsset
}

// Delta is in final-resolution pixels. Normalization/clipping of each layer is
// tested independently. The default offsets in typography.json stay untouched.
type resultAlignment struct {
	ExtrusionDY int
	ShadowDY    int
	ExtrusionDX int
	ShadowDX    int
	// Research-only style knobs, not manifest/default production changes.
	StrokeGrow       int
	ExtrusionDepth   int
	HorizontalWiden  float64
	NeutralShear     bool
	FaceGradientBBox bool
	// Independent research-only mask radii in final pixels. 0 retains
	// the original legacy baseline; no canonical typography edits.
	OutlineRadius int
	// When positive, overrides OutlineRadius at quarter-pixel accuracy
	// on the 4x supersampled mask (e.g. 3 means a 0.75px outline).
	OutlineRadiusHi   int
	ExtrusionRadius   int
	FaceDY            int
	OnlyModifiedNames bool
	// legacy, private exploratory additional ambient black translucent shadow.
	// Kept independent of the blue extrusion and the inherited typography preset.
	AmbientBlackAlpha uint8
	AmbientBlackDX    int
	AmbientBlackDY    int
	AmbientBlackBlur  float64
}

func loadResult(root string) (familyAsset, resultPreset, []byte, error) {
	var zero familyAsset
	manifest, err := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if err != nil {
		return zero, resultPreset{}, nil, err
	}
	var families familyAssets
	if err := json.Unmarshal(manifest, &families); err != nil {
		return zero, resultPreset{}, nil, err
	}
	var a familyAsset
	found := 0
	for _, candidate := range families.Assets {
		if candidate.ID == resultID {
			a = candidate
			found++
		}
	}
	if found != 1 || a.Renderer != "ms2_result_font_v2" || a.Typography != "result_nameplate" ||
		a.Archive != 55 || a.Index != 1333 || a.Inner != "result_2.tid" || len(a.Rows) != 13 ||
		len(a.Sources) != 1 || a.Sources[0].SHA != resultSHA ||
		a.Sources[0].Width != 2048 || a.Sources[0].Height != 2048 || a.Sources[0].Format != "BC7" {
		return zero, resultPreset{}, nil, errors.New("legacy result original mapping/typography drift")
	}
	expected := []string{"TSUU", "NINGYOHIME", "HAMELN", "GRETEL", "RAPUNZEL", "KAGUYAHIME", "OYAYUBIHIME", "SHIRAYUKIHIME", "NEMURIHIME", "CINDERELLA", "AKAZUKIN", "ALICE", "JACK"}
	for i, row := range a.Rows {
		if row.Text != expected[i] || len(row.Rect) != 4 || row.Rect[0] < 0 || row.Rect[1] < 0 || row.Rect[2] < 16 || row.Rect[3] < 16 || row.Rect[0]+row.Rect[2] > 2048 || row.Rect[1]+row.Rect[3] > 2048 {
			return zero, resultPreset{}, nil, fmt.Errorf("legacy result row %d source geometry drift", i)
		}
		rr := image.Rect(row.Rect[0], row.Rect[1], row.Rect[0]+row.Rect[2], row.Rect[1]+row.Rect[3])
		for j := 0; j < i; j++ {
			r2 := a.Rows[j].Rect
			if rr.Overlaps(image.Rect(r2[0], r2[1], r2[0]+r2[2], r2[1]+r2[3])) {
				return zero, resultPreset{}, nil, errors.New("result row rectangles overlap")
			}
		}
	}
	typ, err := os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if err != nil {
		return zero, resultPreset{}, nil, err
	}
	var ui struct {
		Presets map[string]resultPreset `json:"presets"`
		Fonts   map[string]familyFont       `json:"fonts"`
	}
	if err := json.Unmarshal(typ, &ui); err != nil {
		return zero, resultPreset{}, nil, err
	}
	p, ok := ui.Presets[a.Typography]
	if !ok || p.Face != "avionic" || p.Nominal != 58 || p.Minimum != 40 || p.StrokeWidth != 1 || p.ExtrusionWidth != 1 || p.ShadowWidth != 1 ||
		p.HScale <= 0 || p.HMinimum <= 0 || p.HMinimum > p.HScale || p.VScale <= 0 || math.Abs(p.Shear) > 1 || p.ShadowBlur < 0 || p.ShadowBlur > 8 {
		return zero, resultPreset{}, nil, errors.New("legacy source result typography drift")
	}
	f, ok := ui.Fonts[p.Face]
	if !ok || !strings.HasPrefix(f.Path, "third_party/fonts/") || strings.Contains(f.Path, "..") || filepath.IsAbs(f.Path) || len(f.SHA) != 64 {
		return zero, resultPreset{}, nil, errors.New("unsafe result source font")
	}
	font, err := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	return a, p, font, err
}

func resultAlpha(src *image.Alpha, w, h int, x0, y0 int) (*image.Alpha, error) {
	if w < 1 || h < 1 || src == nil {
		return nil, errors.New("invalid result mask")
	}
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < src.Rect.Dy(); y++ {
		ty := y + y0
		if ty < 0 || ty >= h {
			continue
		}
		for x := 0; x < src.Rect.Dx(); x++ {
			tx := x + x0
			if tx >= 0 && tx < w {
				out.Pix[ty*out.Stride+tx] = src.Pix[y*src.Stride+x]
			}
		}
	}
	return out, nil
}

func resultColor(r [4]uint8) color.NRGBA { return color.NRGBA{R: r[0], G: r[1], B: r[2], A: r[3]} }

// Original shipped plates have a steep white-to-pale-blue face gradient over
// glyph height. The legacy Python palette uses the *slot* height instead, so
// trial both without changing the canonical manifest.
func resultGradientGlyph(mask *image.Alpha, top, bottom color.NRGBA) (*image.NRGBA, error) {
	bb, ok, err := graphicsgo.BBoxAlpha(mask)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("empty face gradient")
	}
	out := image.NewNRGBA(mask.Rect)
	for y := 0; y < mask.Rect.Dy(); y++ {
		k := float64(y-bb.Min.Y) / float64(max(1, bb.Dy()-1))
		if k < 0 {
			k = 0
		}
		if k > 1 {
			k = 1
		}
		lerp := func(a, b uint8) uint8 { return uint8(math.RoundToEven((1-k)*float64(a) + k*float64(b))) }
		c := color.NRGBA{R: lerp(top.R, bottom.R), G: lerp(top.G, bottom.G), B: lerp(top.B, bottom.B), A: lerp(top.A, bottom.A)}
		for x := 0; x < mask.Rect.Dx(); x++ {
			i := y*mask.Stride + x
			v := mask.Pix[i]
			off := y*out.Stride + x*4
			out.Pix[off], out.Pix[off+1], out.Pix[off+2], out.Pix[off+3] = c.R, c.G, c.B, uint8(uint32(v)*uint32(c.A)/255)
		}
	}
	return out, nil
}

func renderResultLabel(face *graphicsgo.SFNT, label string, w, h int, p resultPreset, align resultAlignment) (*image.NRGBA, error) {
	const factor = 4
	if align.ExtrusionDepth < 0 || align.ExtrusionDepth > 20 || align.StrokeGrow < 0 || align.StrokeGrow > 4 || align.HorizontalWiden < 0 || align.HorizontalWiden > 1.4 || align.OutlineRadius < 0 || align.OutlineRadius > 4 || align.OutlineRadiusHi < 0 || align.OutlineRadiusHi > 16 || align.ExtrusionDX < -6 || align.ExtrusionDX > 6 || align.ShadowDX < -6 || align.ShadowDX > 6 || align.ExtrusionRadius < 0 || align.ExtrusionRadius > 4 || align.FaceDY < -12 || align.FaceDY > 12 || align.AmbientBlackDX < -8 || align.AmbientBlackDX > 12 || align.AmbientBlackDY < -8 || align.AmbientBlackDY > 16 || align.AmbientBlackBlur < 0 || align.AmbientBlackBlur > 7 {
		return nil, errors.New("legacy invalid research style adjustment")
	}

	if align.NeutralShear {
		p.Shear = 0
	}
	fit, err := graphicsgo.FitR32C3MaskPair(label, graphicsgo.R32C3FitSpec{Width: w, Height: h, Factor: factor, NominalSize: p.Nominal, MinSize: p.Minimum, StrokeWidth: 2, HorizontalScale: p.HScale, MinHorizontalScale: p.HMinimum, VerticalScale: p.VScale, ShearX: p.Shear}, func(px int) (image.Rectangle, error) { return face.MeasureLatinTextBBoxUnhinted(label, px) })
	if err != nil {
		return nil, fmt.Errorf("legacy label %s: %w", label, err)
	}
	masks, err := face.RasterR32C3LatinMasksExperimental(label, fit, 0, 0, 2)
	if err != nil {
		return nil, err
	}
	warp := graphicsgo.R32C3Warp{HorizontalScale: fit.HorizontalScale, VerticalScale: p.VScale, ShearX: p.Shear, Factor: factor}
	rawFace, err := graphicsgo.WarpR32C3Alpha(masks.Face, warp)
	if err != nil {
		return nil, err
	}
	radius := 2 + align.StrokeGrow
	if align.OutlineRadius > 0 {
		radius = align.OutlineRadius
	}
	outlineHi := radius * factor
	if align.OutlineRadiusHi > 0 {
		outlineHi = align.OutlineRadiusHi
	}
	rawEdge, err := graphicsgo.RadialDilateAlphaFast(rawFace, outlineHi)
	if err != nil {
		return nil, err
	}
	// Widen AFTER selecting the font size, so a longer spelling cannot cause
	// the fitted font to shrink vertically just to satisfy horizontal width.
	if align.HorizontalWiden > 1 {
		faceRect, exists, e := graphicsgo.BBoxAlpha(rawFace)
		if e != nil {
			return nil, e
		}
		if !exists {
			return nil, errors.New("empty face")
		}
		factorX := math.Min(align.HorizontalWiden, float64(w*factor-16*factor)/float64(faceRect.Dx()))
		if factorX > 1 {
			nw := int(math.Round(float64(rawFace.Rect.Dx()) * factorX))
			rawFace, e = graphicsgo.ResizeLanczosAlpha(rawFace, nw, rawFace.Rect.Dy())
			if e != nil {
				return nil, e
			}
			rawEdge, e = graphicsgo.ResizeLanczosAlpha(rawEdge, nw, rawEdge.Rect.Dy())
			if e != nil {
				return nil, e
			}
		}
	}
	// Keep the dark extrusion geometry distinct from the visible cyan outline.
	// A narrower outline must NOT silently thin or enlarge the blue body.
	rawExtrude := rawEdge
	if align.ExtrusionRadius > 0 && align.ExtrusionRadius != radius {
		rawExtrude, err = graphicsgo.RadialDilateAlphaFast(rawFace, align.ExtrusionRadius*factor)
		if err != nil {
			return nil, err
		}
	}
	bb, ok, err := graphicsgo.BBoxAlpha(rawFace)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("result glyph blank")
	}
	// All masks share ONE source face anchor, even with differing layer offsets.
	// The atlas uses a short top margin to reserve its lower edge for extrusion.
	anchorX := (w*factor-bb.Dx())/2 - bb.Min.X
	anchorY := (5+align.FaceDY)*factor - bb.Min.Y
	if h > 90 {
		anchorY = ((h-55)/2+align.FaceDY)*factor - bb.Min.Y
	}
	hiW, hiH := w*factor, h*factor
	faceMask, err := resultAlpha(rawFace, hiW, hiH, anchorX, anchorY)
	if err != nil {
		return nil, err
	}
	edgeMask, err := resultAlpha(rawEdge, hiW, hiH, anchorX, anchorY)
	if err != nil {
		return nil, err
	}
	extrudeMask, err := resultAlpha(rawExtrude, hiW, hiH, anchorX, anchorY)
	if err != nil {
		return nil, err
	}
	// Source-authored effect offsets are applied AFTER obtaining the face mask.
	// A negative delta shifts only the corresponding darker layer upwards.
	shadow, err := resultAlpha(edgeMask, hiW, hiH, (p.ShadowOffset[0]+align.ShadowDX)*factor, (p.ShadowOffset[1]+align.ShadowDY)*factor)
	if err != nil {
		return nil, err
	}
	if p.ShadowBlur > 0 {
		shadow, err = graphicsgo.GaussianBlurAlpha(shadow, p.ShadowBlur*factor)
		if err != nil {
			return nil, err
		}
	}
	extrusion := image.NewAlpha(image.Rect(0, 0, hiW, hiH))
	for yStep := 0; yStep <= align.ExtrusionDepth; yStep++ {
		shifted, shiftErr := resultAlpha(extrudeMask, hiW, hiH, (p.ExtrusionOffset[0]+align.ExtrusionDX)*factor, (p.ExtrusionOffset[1]+align.ExtrusionDY+yStep)*factor)
		if shiftErr != nil {
			return nil, shiftErr
		}
		for i, v := range shifted.Pix {
			if v > extrusion.Pix[i] {
				extrusion.Pix[i] = v
			}
		}
	}
	hi := image.NewNRGBA(image.Rect(0, 0, hiW, hiH))
	add := func(layer *image.NRGBA) error { return graphicsgo.CompositeOver(hi, layer, image.Point{}) }
	// legacy: Ambient black (semi-transparent) is deliberately drawn BEFORE the
	// blue extrusion, colored outline and face. In legacy the source-offset black
	// layer mostly hid beneath those blue layers, making it almost invisible.
	// Alpha/blur/position are independent to avoid fattening the blue stroke.
	if align.AmbientBlackAlpha > 0 {
		ambient, e := resultAlpha(edgeMask, hiW, hiH, align.AmbientBlackDX*factor, align.AmbientBlackDY*factor)
		if e != nil {
			return nil, e
		}
		if align.AmbientBlackBlur > 0 {
			ambient, e = graphicsgo.GaussianBlurAlpha(ambient, align.AmbientBlackBlur*factor)
			if e != nil {
				return nil, e
			}
		}
		ambientTint, e := graphicsgo.TintMask(ambient, color.NRGBA{R: 0, G: 0, B: 0, A: align.AmbientBlackAlpha})
		if e != nil {
			return nil, e
		}
		if e = add(ambientTint); e != nil {
			return nil, e
		}
	}
	shadowTint, err := graphicsgo.TintMask(shadow, resultColor(p.Shadow))
	if err != nil {
		return nil, err
	}
	if err = add(shadowTint); err != nil {
		return nil, err
	}
	dark, err := graphicsgo.GradientMask(extrusion, resultColor(p.ExtrusionTop), resultColor(p.ExtrusionBottom))
	if err != nil {
		return nil, err
	}
	if err = add(dark); err != nil {
		return nil, err
	}
	edgeTint, err := graphicsgo.TintMask(edgeMask, resultColor(p.Stroke))
	if err != nil {
		return nil, err
	}
	if err = add(edgeTint); err != nil {
		return nil, err
	}
	bright, err := graphicsgo.GradientMask(faceMask, resultColor(p.FillTop), resultColor(p.FillBottom))
	if align.FaceGradientBBox {
		bright, err = resultGradientGlyph(faceMask, resultColor(p.FillTop), resultColor(p.FillBottom))
	}
	if err != nil {
		return nil, err
	}
	if err = add(bright); err != nil {
		return nil, err
	}
	low, err := graphicsgo.ResizeLanczosNRGBA(hi, w, h)
	if err != nil {
		return nil, err
	}
	// No edge crop/halo acceptance by assumption: reject if the baked label
	// touches outer slot pixels. This remains a QA candidate, never publish.
	for x := 0; x < w; x++ {
		if low.NRGBAAt(x, 0).A > 80 || low.NRGBAAt(x, h-1).A > 80 {
			return nil, fmt.Errorf("result %s touches horizontal crop", label)
		}
	}
	for y := 0; y < h; y++ {
		if low.NRGBAAt(0, y).A > 80 || low.NRGBAAt(w-1, y).A > 80 {
			return nil, fmt.Errorf("result %s touches vertical crop", label)
		}
	}
	return low, nil
}

// In-memory only. Candidate RGBA is not serialized into public art, game files
// or production build inputs. An external private QA runner may encode a PNG.
func renderResultAtlas(root string, tid []byte, align resultAlignment) ([]byte, error) {
	if digest(tid) != resultSHA {
		return nil, errors.New("legacy result source TID SHA mismatch")
	}
	a, p, font, err := loadResult(root)
	if err != nil {
		return nil, err
	}
	rgba, meta, err := mskit.DecodeTIDBC7RGBA(tid)
	if err != nil {
		return nil, err
	}
	if meta.Width != 2048 || meta.Height != 2048 {
		return nil, errors.New("legacy result atlas dimensions mismatch")
	}
	sf, err := graphicsgo.ParseSFNT(font)
	if err != nil {
		return nil, err
	}
	for _, row := range a.Rows {
		if align.OnlyModifiedNames && row.From == row.Text {
			continue
		}
		r := row.Rect
		x, y, w, h := r[0], r[1], r[2], r[3]
		label, err := renderResultLabel(sf, row.Text, w, h, p, align)
		if err != nil {
			return nil, err
		}
		for yy := 0; yy < h; yy++ {
			for xx := 0; xx < w; xx++ {
				off := ((yy+y)*meta.Width + (xx + x)) * 4
				v := label.NRGBAAt(xx, yy)
				rgba[off], rgba[off+1], rgba[off+2], rgba[off+3] = v.R, v.G, v.B, v.A
				if v.A == 0 {
					rgba[off], rgba[off+1], rgba[off+2] = p.Bleed[0], p.Bleed[1], p.Bleed[2]
				}
			}
		}
	}
	return rgba, nil
}
