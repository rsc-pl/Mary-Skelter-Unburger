package main

// legacy: source-pinned MS2 white job menu, BC7; original 12 rows include
// overlapping rectangular clear areas. Render all rows first, clear their union
// once, reject *actual ink collisions*, and composite in a second pass.
// No changes to accepted game output or public production pipeline.
import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"

	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
)

const jobWhiteID = "MS2_JOB_CHARACTER_NAMES"
const jobWhiteSHA = "ff9b3806df6c5b8a829619324692a379294dc9247bf53626302af80b3c620727"

func readJobWhite(root string) (familyAsset, []byte, bonusWhitePreset, error) {
	var z familyAsset
	raw, e := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if e != nil {
		return z, nil, bonusWhitePreset{}, e
	}
	var mf familyAssets
	if e = json.Unmarshal(raw, &mf); e != nil {
		return z, nil, bonusWhitePreset{}, e
	}
	var a familyAsset
	matches := 0
	for _, one := range mf.Assets {
		if one.ID == jobWhiteID {
			a = one
			matches++
		}
	}
	if matches != 1 || a.Renderer != "ms2_name_family_font_v1" || a.Typography != "r32c3_white_oblique" || a.Archive != 55 || a.Index != 1854 || a.Inner != "menu_job.tid" || len(a.Rows) != 12 || len(a.Sources) != 1 || a.Sources[0].SHA != jobWhiteSHA || a.Sources[0].Width != 2048 || a.Sources[0].Height != 2048 || a.Sources[0].Format != "BC7" {
		return z, nil, bonusWhitePreset{}, errors.New("legacy job source manifest drift")
	}
	names := []string{"TSUU", "OYAYUBIHIME", "NINGYOHIME", "SHIRAYUKIHIME", "HAMELN", "NEMURIHIME", "GRETEL", "CINDERELLA", "RAPUNZEL", "AKAZUKIN", "KAGUYAHIME", "ALICE"}
	for i, r := range a.Rows {
		if r.Text != names[i] || len(r.Rect) != 4 {
			return z, nil, bonusWhitePreset{}, fmt.Errorf("legacy job row %d drift", i)
		}
		rect := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		if rect.Empty() || rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > 2048 || rect.Max.Y > 2048 || rect.Dx() < 50 || rect.Dy() < 50 {
			return z, nil, bonusWhitePreset{}, fmt.Errorf("legacy job row %d bounds", i)
		}
	}
	raw, e = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if e != nil {
		return z, nil, bonusWhitePreset{}, e
	}
	var tp struct {
		Fonts   map[string]familyFont          `json:"fonts"`
		Presets map[string]bonusWhitePreset `json:"presets"`
	}
	if e = json.Unmarshal(raw, &tp); e != nil {
		return z, nil, bonusWhitePreset{}, e
	}
	p, ok := tp.Presets[a.Typography]
	if !ok || p.Face != "avionic" || p.Nominal != 62 || p.Min != 24 || p.InnerWidth != 3 || p.OuterWidth != 1 || p.Rotation != 4 || p.HScale != 0.98 || p.HMin != 0.54 || p.VScale != 0.96 || p.Shear != -0.27 || len(p.Fill) != 4 || len(p.Outer) != 4 || len(p.Inner) != 4 || len(p.Shadow) != 4 || len(p.ShadowOffset) != 2 || len(p.Bleed) != 3 {
		return z, nil, bonusWhitePreset{}, errors.New("legacy white typography drift")
	}
	f, ok := tp.Fonts[p.Face]
	if !ok || len(f.SHA) != 64 || filepath.IsAbs(f.Path) || len(f.Path) < 18 || f.Path[:18] != "third_party/fonts/" {
		return z, nil, bonusWhitePreset{}, errors.New("legacy font reference invalid")
	}
	font, e := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	return a, font, p, e
}
func renderJobWhite(tid, fontData []byte, a familyAsset, p bonusWhitePreset) ([]byte, error) {
	if digest(tid) != jobWhiteSHA {
		return nil, errors.New("legacy source BC7 TID SHA mismatch")
	}
	rgba, meta, e := mskit.DecodeTIDBC7RGBA(tid)
	if e != nil {
		return nil, e
	}
	if meta.Width != 2048 || meta.Height != 2048 || meta.Format != "BC7" {
		return nil, errors.New("legacy job TID format mismatch")
	}
	sf, e := graphicsgo.ParseSFNT(fontData)
	if e != nil {
		return nil, e
	}
	labels := make([]*image.NRGBA, len(a.Rows))
	occupied := make([]uint8, meta.Width*meta.Height)
	for i, row := range a.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		fit := graphicsgo.R32C3FitSpec{Width: r.Dx(), Height: r.Dy(), Factor: 4, NominalSize: p.Nominal, MinSize: p.Min, InnerWidth: p.InnerWidth, StrokeWidth: p.OuterWidth, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}
		layers := graphicsgo.R32C3CompositeSpec{Width: r.Dx(), Height: r.Dy(), Factor: 4, InnerColor: asNRGBA(p.Inner), OuterColor: asNRGBA(p.Outer), FillColor: asNRGBA(p.Fill), ShadowColor: asNRGBA(p.Shadow), ShadowOffset: image.Pt(p.ShadowOffset[0], p.ShadowOffset[1])}
		label, _, err := sf.RenderR32C3LatinLabelFastRadial(row.Text, fit, graphicsgo.R32C3Warp{Factor: 4, RotationDegrees: p.Rotation}, layers, 2)
		if err != nil {
			return nil, fmt.Errorf("legacy job render %s: %w", row.Text, err)
		}
		if label.Rect.Dx() != r.Dx() || label.Rect.Dy() != r.Dy() {
			return nil, errors.New("legacy render size drift")
		}
		ink := 0
		for y := 0; y < r.Dy(); y++ {
			for x := 0; x < r.Dx(); x++ {
				alpha := label.Pix[y*label.Stride+x*4+3]
				if alpha == 0 {
					continue
				}
				if x == 0 || y == 0 || x == r.Dx()-1 || y == r.Dy()-1 {
					return nil, fmt.Errorf("legacy job clipped %s", row.Text)
				}
				k := (r.Min.Y+y)*meta.Width + r.Min.X + x
				if occupied[k] != 0 {
					return nil, fmt.Errorf("legacy actual glyph/shadow overlap at %d,%d: %s", r.Min.X+x, r.Min.Y+y, row.Text)
				}
				occupied[k] = 1
				ink++
			}
		}
		if ink < 80 {
			return nil, fmt.Errorf("legacy blank job %s", row.Text)
		}
		labels[i] = label
	}
	// Transparent clear is performed only after all composited text footprints
	// have passed independent overlap checks. Each cleared pixel is visited once.
	cleared := make([]bool, meta.Width*meta.Height)
	for _, row := range a.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				k := y*meta.Width + x
				if cleared[k] {
					continue
				}
				cleared[k] = true
				off := k * 4
				rgba[off], rgba[off+1], rgba[off+2], rgba[off+3] = 0, 0, 0, 0
			}
		}
	}
	for i, row := range a.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		label := labels[i]
		for y := 0; y < r.Dy(); y++ {
			for x := 0; x < r.Dx(); x++ {
				pos := y*label.Stride + x*4
				if label.Pix[pos+3] == 0 {
					continue
				}
				off := ((r.Min.Y+y)*meta.Width + r.Min.X + x) * 4
				copy(rgba[off:off+4], label.Pix[pos:pos+4])
			}
		}
	}
	return rgba, nil
}
