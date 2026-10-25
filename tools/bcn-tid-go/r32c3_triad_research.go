package main

// legacy: strictly opt-in shared R32C3 font renderer research for three
// still-open PC graphics families. This NEVER promotes them into the 17-family
// production allowlist. Full private-font/Python-reference/BCn QA is required.
import (
	"crypto/sha256"
	"encoding/hex"
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

var pendingR32C3 = map[string]struct {
	SHA, Preset, Inner, Format          string
	Archive, Index, Width, Height, Rows int
	RowsSHA                             string
}{
	"MS2_PARTY_FORMATION_NAMES":  {"5eac8fae322a0d260941fb89cf735b4bfa44184c0822eb2d37430c21a2c8599e", "party_formation_pink", "camp_hensei.tid", "BC7", 55, 1438, 2048, 2048, 12, "30f2af99eec74bf3da3682eadd6f22b7a523783ee2064b9f794fa28dbe55c04a"},
	"MS2_EQUIPMENT_HEADER_NAMES": {"d540e4d718490eb990313830782662aed075650eb56c19266d4c8f3965dcd7f3", "equipment_header_chiaro", "commonBE.tid", "BC7", 55, 1468, 4096, 4096, 13, "1267d181f85c6a92cdeadf0b9875c13e5547ea27d7d69778c5b187c193cbbe6b"},
	"MSF_EQUIPMENT_HEADER_NAMES": {"e8ac0ad32138bb600d29bb2f895103c5c17d9fe7f0688af0c879336a7b3fa070", "equipment_header_chiaro", "commonBE.tid", "DXT5", 74, 8, 4096, 4096, 13, "1267d181f85c6a92cdeadf0b9875c13e5547ea27d7d69778c5b187c193cbbe6b"},
}

type r32TriadPreset struct {
	Face           string  `json:"font_face"`
	NominalSize    int     `json:"nominal_size"`
	MinSize        int     `json:"min_size"`
	HScale         float64 `json:"horizontal_scale"`
	HMin           float64 `json:"min_horizontal_scale"`
	VScale         float64 `json:"vertical_scale"`
	Shear          float64 `json:"shear_x"`
	Stroke         int     `json:"stroke_width"`
	Fill           []uint8 `json:"fill_rgba"`
	GradientTop    []uint8 `json:"fill_gradient_top_rgba"`
	GradientBottom []uint8 `json:"fill_gradient_bottom_rgba"`
	StrokeColor    []uint8 `json:"stroke_rgba"`
	Shadow         []uint8 `json:"shadow_rgba"`
	ShadowOffset   []int   `json:"shadow_offset"`
	Bleed          []uint8 `json:"transparent_bleed_rgb"`
}

type r32TriadConfig struct {
	Asset  familyAsset
	Font   []byte
	Preset r32TriadPreset
}

func r32Color(v []uint8) (color.NRGBA, error) {
	if len(v) != 4 {
		return color.NRGBA{}, errors.New("expected four RGBA channels")
	}
	return color.NRGBA{R: v[0], G: v[1], B: v[2], A: v[3]}, nil
}
func loadR32C3Triad(root, id string, requireFont bool) (r32TriadConfig, error) {
	var zero r32TriadConfig
	expected, ok := pendingR32C3[id]
	if !ok {
		return zero, fmt.Errorf("not an legacy research family: %s", id)
	}
	blob, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return zero, err
	}
	var manifest familyAssets
	if err = json.Unmarshal(blob, &manifest); err != nil {
		return zero, err
	}
	var a familyAsset
	count := 0
	for _, candidate := range manifest.Assets {
		if candidate.ID == id {
			a = candidate
			count++
		}
	}
	if count != 1 || a.Renderer != "ms2_name_family_font_v1" || a.Typography != expected.Preset || a.Archive != expected.Archive || a.Index != expected.Index || a.Inner != expected.Inner || len(a.Rows) != expected.Rows || len(a.Sources) != 1 || a.Sources[0].SHA != expected.SHA || a.Sources[0].Format != expected.Format || a.Sources[0].Width != expected.Width || a.Sources[0].Height != expected.Height {
		return zero, fmt.Errorf("%s: source/layout contract drift", id)
	}
	// Reject changes to exact source labels and placement, including reorder.
	var lines strings.Builder
	for i, row := range a.Rows {
		if row.Text == "" || len(row.Rect) != 4 {
			return zero, fmt.Errorf("%s row %d invalid", id, i)
		}
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		if r.Empty() || r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > expected.Width || r.Max.Y > expected.Height || r.Dx() < 10 || r.Dy() < 10 {
			return zero, fmt.Errorf("%s row %d outside original atlas", id, i)
		}
		fmt.Fprintf(&lines, "%s|%d,%d,%d,%d\n", row.Text, row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
	}
	hash := sha256.Sum256([]byte(lines.String()))
	if hex.EncodeToString(hash[:]) != expected.RowsSHA {
		return zero, fmt.Errorf("%s: row contents/geometry drift", id)
	}
	blob, err = os.ReadFile(filepath.Join(root, "graphics", "ui_typography.json"))
	if err != nil {
		return zero, err
	}
	var ty struct {
		Fonts   map[string]familyFont         `json:"fonts"`
		Presets map[string]r32TriadPreset `json:"presets"`
	}
	if err = json.Unmarshal(blob, &ty); err != nil {
		return zero, err
	}
	p, ok := ty.Presets[expected.Preset]
	if !ok || p.NominalSize < 20 || p.MinSize < 10 || p.MinSize > p.NominalSize || p.HScale <= 0 || p.HMin <= 0 || p.HMin > p.HScale || p.VScale <= 0 || p.Stroke < 0 || len(p.ShadowOffset) != 2 || len(p.Shadow) != 4 || len(p.StrokeColor) != 4 || len(p.Bleed) != 3 {
		return zero, fmt.Errorf("%s: invalid typography", id)
	}
	if len(p.Fill) != 4 && (len(p.GradientTop) != 4 || len(p.GradientBottom) != 4) {
		return zero, fmt.Errorf("%s: missing face color", id)
	}
	if id == "MS2_PARTY_FORMATION_NAMES" && p.Face != "avionic" || id != "MS2_PARTY_FORMATION_NAMES" && p.Face != "chiaro" {
		return zero, fmt.Errorf("%s: font identity drift", id)
	}
	fc, ok := ty.Fonts[p.Face]
	if !ok || !strings.HasPrefix(fc.Path, "third_party/fonts/") || filepath.IsAbs(fc.Path) || strings.Contains(fc.Path, "..") || len(fc.SHA) != 64 {
		return zero, fmt.Errorf("%s: invalid pinned font", id)
	}
	var font []byte
	if requireFont {
		font, err = familyReadPinned(filepath.Join(root, fc.Path), fc.SHA)
		if err != nil {
			return zero, err
		}
	}
	return r32TriadConfig{a, font, p}, nil
}

func renderR32C3Triad(root, id string, tid []byte) ([]byte, error) {
	cfg, err := loadR32C3Triad(root, id, true)
	if err != nil {
		return nil, err
	}
	expected := pendingR32C3[id]
	if digest(tid) != expected.SHA {
		return nil, fmt.Errorf("%s: genuine source TID SHA mismatch", id)
	}
	var rgba []byte
	var meta mskit.TIDMeta
	if expected.Format == "BC7" {
		rgba, meta, err = mskit.DecodeTIDBC7RGBA(tid)
	} else {
		rgba, meta, err = mskit.DecodeTIDDXT5RGBA(tid)
	}
	if err != nil {
		return nil, err
	}
	if meta.Width != expected.Width || meta.Height != expected.Height || meta.Format != expected.Format {
		return nil, errors.New("decoded source format changed")
	}
	sfnt, err := graphicsgo.ParseSFNT(cfg.Font)
	if err != nil {
		return nil, err
	}
	p := cfg.Preset
	fill, _ := r32Color(p.Fill)
	st, _ := r32Color(p.StrokeColor)
	shadow, _ := r32Color(p.Shadow)
	grad := len(p.GradientTop) == 4
	top, bottom := color.NRGBA{}, color.NRGBA{}
	if grad {
		top, _ = r32Color(p.GradientTop)
		bottom, _ = r32Color(p.GradientBottom)
	}
	resultImages := make([]*image.NRGBA, len(cfg.Asset.Rows))
	occupied := make(map[int]bool)
	for i, row := range cfg.Asset.Rows {
		rect := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		w, h := rect.Dx(), rect.Dy()
		spec := graphicsgo.R32C3FitSpec{Width: w, Height: h, Factor: 4, NominalSize: p.NominalSize, MinSize: p.MinSize, StrokeWidth: p.Stroke, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}
		comp := graphicsgo.R32C3CompositeSpec{Width: w, Height: h, Factor: 4, StrokeColor: st, OuterColor: st, FillColor: fill, ShadowColor: shadow, ShadowOffset: image.Pt(p.ShadowOffset[0], p.ShadowOffset[1]), GradientEnabled: grad, GradientTop: top, GradientBottom: bottom}
		// This still uses unhinted native masks. Not a FreeType-parity claim.
		im, _, e := sfnt.RenderR32C3LatinLabelFastRadial(row.Text, spec, graphicsgo.R32C3Warp{Factor: 4}, comp, 2)
		if e != nil {
			return nil, fmt.Errorf("%s row %d (%s): %w", id, i, row.Text, e)
		}
		ink := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				a := im.NRGBAAt(x, y).A
				if a == 0 {
					continue
				}
				if x == 0 || y == 0 || x == w-1 || y == h-1 {
					return nil, fmt.Errorf("%s row %d clipped", id, i)
				}
				at := (rect.Min.Y+y)*meta.Width + rect.Min.X + x
				if occupied[at] {
					return nil, fmt.Errorf("%s row %d overlap", id, i)
				}
				occupied[at] = true
				ink++
			}
		}
		if ink < 25 {
			return nil, fmt.Errorf("%s row %d nearly blank", id, i)
		}
		resultImages[i] = im
	}
	// Clear the exact author-declared rows in a single pass before overcompositing.
	for _, row := range cfg.Asset.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				i := (y*meta.Width + x) * 4
				rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = p.Bleed[0], p.Bleed[1], p.Bleed[2], 0
			}
		}
	}
	for j, row := range cfg.Asset.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		im := resultImages[j]
		for y := 0; y < r.Dy(); y++ {
			for x := 0; x < r.Dx(); x++ {
				c := im.NRGBAAt(x, y)
				if c.A == 0 {
					continue
				}
				idx := ((r.Min.Y+y)*meta.Width + r.Min.X + x) * 4
				rgba[idx], rgba[idx+1], rgba[idx+2], rgba[idx+3] = c.R, c.G, c.B, c.A
			}
		}
	}
	return rgba, nil
}
