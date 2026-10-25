package main

// legacy: third original-source-pinned full family. No private source is embedded
// in a release binary: source TID and font are loaded from owner worktree with
// exact sha256 checks. Draw labels using the canonical R32C3 layered styling,
// fail closed on changed manifest/preset, and never write to the source GDAT.
import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
)

const bonusWhiteID = "MS2_BONUS_CHARACTER_NAMES"
const bonusWhiteSHA = "8f4638abe267aad2b7d07b36232268d64cc4e208d79b07afb5a3e53d4fb0f246"

type bonusWhitePreset struct {
	Face         string  `json:"font_face"`
	Nominal      int     `json:"nominal_size"`
	Min          int     `json:"min_size"`
	HScale       float64 `json:"horizontal_scale"`
	HMin         float64 `json:"min_horizontal_scale"`
	VScale       float64 `json:"vertical_scale"`
	Shear        float64 `json:"shear_x"`
	Rotation     float64 `json:"rotation_degrees"`
	InnerWidth   int     `json:"r32c3_inner_width"`
	OuterWidth   int     `json:"r32c3_outer_width"`
	Inner        []uint8 `json:"r32c3_inner_rgba"`
	Outer        []uint8 `json:"r32c3_outer_rgba"`
	Fill         []uint8 `json:"fill_rgba"`
	Shadow       []uint8 `json:"shadow_rgba"`
	ShadowOffset []int   `json:"shadow_offset"`
	Bleed        []uint8 `json:"transparent_bleed_rgb"`
}

func readBonusWhite(root string) (familyAsset, []byte, bonusWhitePreset, error) {
	var zero familyAsset
	raw, err := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if err != nil {
		return zero, nil, bonusWhitePreset{}, err
	}
	var docs familyAssets
	if err = json.Unmarshal(raw, &docs); err != nil {
		return zero, nil, bonusWhitePreset{}, err
	}
	var a familyAsset
	matches := 0
	for _, one := range docs.Assets {
		if one.ID == bonusWhiteID {
			a = one
			matches++
		}
	}
	if matches != 1 || a.Renderer != "ms2_name_family_font_v1" || a.Typography != "r32c3_white_oblique" || a.Archive != 55 || a.Index != 1400 || a.Inner != "BONUS_CHARACTER.tid" || len(a.Rows) != 12 || len(a.Sources) != 1 || a.Sources[0].SHA != bonusWhiteSHA || a.Sources[0].Width != 2048 || a.Sources[0].Height != 2048 || a.Sources[0].Format != "DXT5" {
		return zero, nil, bonusWhitePreset{}, errors.New("legacy white source manifest drift")
	}
	names := []string{"TSUU", "OYAYUBIHIME", "NINGYOHIME", "SHIRAYUKIHIME", "HAMELN", "NEMURIHIME", "GRETEL", "CINDERELLA", "RAPUNZEL", "AKAZUKIN", "KAGUYAHIME", "ALICE"}
	for i, r := range a.Rows {
		if r.Text != names[i] || len(r.Rect) != 4 {
			return zero, nil, bonusWhitePreset{}, fmt.Errorf("legacy white row %d changed", i)
		}
		rc := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		if rc.Empty() || rc.Min.X < 0 || rc.Min.Y < 0 || rc.Max.X > 2048 || rc.Max.Y > 2048 || rc.Dx() < 50 || rc.Dy() < 50 {
			return zero, nil, bonusWhitePreset{}, fmt.Errorf("legacy white row %d bad bounds", i)
		}
		for j := 0; j < i; j++ {
			q := a.Rows[j].Rect
			if rc.Overlaps(image.Rect(q[0], q[1], q[2], q[3])) {
				return zero, nil, bonusWhitePreset{}, fmt.Errorf("legacy overlapping white rows %d %d", i, j)
			}
		}
	}
	raw, err = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if err != nil {
		return zero, nil, bonusWhitePreset{}, err
	}
	var typ struct {
		Fonts   map[string]familyFont          `json:"fonts"`
		Presets map[string]bonusWhitePreset `json:"presets"`
	}
	if err = json.Unmarshal(raw, &typ); err != nil {
		return zero, nil, bonusWhitePreset{}, err
	}
	p, ok := typ.Presets[a.Typography]
	if !ok || p.Face != "avionic" || p.Nominal != 62 || p.Min != 24 || p.InnerWidth != 3 || p.OuterWidth != 1 || p.Rotation != 4 || p.HScale != 0.98 || p.HMin != 0.54 || p.VScale != 0.96 || p.Shear != -0.27 || len(p.Inner) != 4 || len(p.Outer) != 4 || len(p.Fill) != 4 || len(p.Shadow) != 4 || len(p.ShadowOffset) != 2 || len(p.Bleed) != 3 {
		return zero, nil, bonusWhitePreset{}, errors.New("legacy white typography drift")
	}
	fontInfo, ok := typ.Fonts[p.Face]
	if !ok || !strings.HasPrefix(fontInfo.Path, "third_party/fonts/") || filepath.IsAbs(fontInfo.Path) || strings.Contains(fontInfo.Path, "..") || len(fontInfo.SHA) != 64 {
		return zero, nil, bonusWhitePreset{}, errors.New("legacy invalid font source")
	}
	font, err := familyReadPinned(filepath.Join(root, fontInfo.Path), fontInfo.SHA)
	return a, font, p, err
}
func asNRGBA(c []uint8) color.NRGBA { return color.NRGBA{R: c[0], G: c[1], B: c[2], A: c[3]} }
func renderBonusWhite(tid, fontData []byte, a familyAsset, p bonusWhitePreset) ([]byte, error) {
	if digest(tid) != bonusWhiteSHA {
		return nil, errors.New("legacy white TID SHA mismatch")
	}
	rgba, meta, err := mskit.DecodeTIDDXT5RGBA(tid)
	if err != nil {
		return nil, err
	}
	if meta.Width != 2048 || meta.Height != 2048 || meta.Format != "DXT5" {
		return nil, errors.New("legacy white geometry/format mismatch")
	}
	font, err := graphicsgo.ParseSFNT(fontData)
	if err != nil {
		return nil, err
	}
	for j, row := range a.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		fit := graphicsgo.R32C3FitSpec{Width: r.Dx(), Height: r.Dy(), Factor: 4, NominalSize: p.Nominal, MinSize: p.Min, InnerWidth: p.InnerWidth, StrokeWidth: p.OuterWidth, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}
		layer := graphicsgo.R32C3CompositeSpec{Width: r.Dx(), Height: r.Dy(), Factor: 4, InnerColor: asNRGBA(p.Inner), OuterColor: asNRGBA(p.Outer), FillColor: asNRGBA(p.Fill), ShadowColor: asNRGBA(p.Shadow), ShadowOffset: image.Pt(p.ShadowOffset[0], p.ShadowOffset[1])}
		label, _, e := font.RenderR32C3LatinLabelFastRadial(row.Text, fit, graphicsgo.R32C3Warp{Factor: 4, RotationDegrees: p.Rotation}, layer, 2)
		if e != nil {
			return nil, fmt.Errorf("legacy white %s: %w", row.Text, e)
		}
		if label.Rect.Dx() != r.Dx() || label.Rect.Dy() != r.Dy() {
			return nil, fmt.Errorf("legacy white row %d dimensions", j)
		}
		nonzero := 0
		for y := 0; y < r.Dy(); y++ {
			for x := 0; x < r.Dx(); x++ {
				pos := y*label.Stride + 4*x
				idx := ((r.Min.Y+y)*meta.Width + r.Min.X + x) * 4
				if label.Pix[pos+3] != 0 {
					if x == 0 || y == 0 || x == r.Dx()-1 || y == r.Dy()-1 {
						return nil, fmt.Errorf("legacy white label %s clipped at (%d,%d)", row.Text, x, y)
					}
					nonzero++
				}
				copy(rgba[idx:idx+4], label.Pix[pos:pos+4])
			}
		}
		if nonzero < 80 {
			return nil, fmt.Errorf("legacy blank white label %s", row.Text)
		}
	}
	return rgba, nil
}
