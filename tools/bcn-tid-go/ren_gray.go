package main

// legacy candidate: original-source Finale Ren bonus dark Chiaro R32C3 names.
// Strictly opt-in: the 12 SHA/geometry-bound rows are prepared in private
// memory; no source TID or installed game is modified.
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

const renGrayID = "MSF_EN_REN_BONUS_CHARACTER_NAMES"
const renGraySHA = "8cc0a7ca15187410135bdaae6fdd291c3f63c24bc795b244ebc373bb0d89972f"
const renGrayRowsSHA = "d6889cec7da41406ef870a276fc8315d76bf0e83044c207d8674e8dd3211b54b"

type renGrayPreset struct {
	Face         string  `json:"font_face"`
	Nominal      int     `json:"nominal_size"`
	Min          int     `json:"min_size"`
	HScale       float64 `json:"horizontal_scale"`
	HMin         float64 `json:"min_horizontal_scale"`
	VScale       float64 `json:"vertical_scale"`
	Shear        float64 `json:"shear_x"`
	Rotation     float64 `json:"rotation_degrees"`
	Fill         []uint8 `json:"fill_rgba"`
	Stroke       []uint8 `json:"stroke_rgba"`
	StrokeWidth  int     `json:"stroke_width"`
	Shadow       []uint8 `json:"shadow_rgba"`
	ShadowOffset []int   `json:"shadow_offset"`
	Bleed        []uint8 `json:"transparent_bleed_rgb"`
}

func loadRenGray(root string) (familyAsset, []byte, renGrayPreset, error) {
	var zero familyAsset
	raw, e := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if e != nil {
		return zero, nil, renGrayPreset{}, e
	}
	var m familyAssets
	if e = json.Unmarshal(raw, &m); e != nil {
		return zero, nil, renGrayPreset{}, e
	}
	var a familyAsset
	count := 0
	for _, v := range m.Assets {
		if v.ID == renGrayID {
			a = v
			count++
		}
	}
	if count != 1 || a.Renderer != "ms2_name_family_font_v1" || a.Typography != "r32c3_ren_gray" || a.Archive != 73 || a.Index != 48 || a.Inner != "ren_bonus_character.tid" || len(a.Rows) != 12 || len(a.Sources) != 1 || a.Sources[0].SHA != renGraySHA || a.Sources[0].Width != 2048 || a.Sources[0].Height != 2048 || a.Sources[0].Format != "DXT5" {
		return zero, nil, renGrayPreset{}, errors.New("Ren source contract drift")
	}
	var b strings.Builder
	for i, r := range a.Rows {
		if len(r.Rect) != 4 || r.Text == "" {
			return zero, nil, renGrayPreset{}, fmt.Errorf("Ren bad row %d", i)
		}
		rc := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		if rc.Empty() || rc.Min.X < 930 || rc.Max.X > 1580 || rc.Min.Y < 0 || rc.Max.Y > 485 || rc.Dx() < 250 || rc.Dy() < 75 {
			return zero, nil, renGrayPreset{}, fmt.Errorf("Ren row bounds %d", i)
		}
		fmt.Fprintf(&b, "%s|%d,%d,%d,%d\n", r.Text, r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
	}
	h := sha256.Sum256([]byte(b.String()))
	if hex.EncodeToString(h[:]) != renGrayRowsSHA {
		return zero, nil, renGrayPreset{}, errors.New("Ren row order or layout drift")
	}
	raw, e = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if e != nil {
		return zero, nil, renGrayPreset{}, e
	}
	var ty struct {
		Fonts   map[string]familyFont        `json:"fonts"`
		Presets map[string]renGrayPreset `json:"presets"`
	}
	if e = json.Unmarshal(raw, &ty); e != nil {
		return zero, nil, renGrayPreset{}, e
	}
	p, ok := ty.Presets[a.Typography]
	if !ok || p.Face != "chiaro" || p.Nominal != 46 || p.Min != 22 || p.HScale != .92 || p.HMin != .52 || p.VScale != .96 || p.Shear != -.18 || p.Rotation != 4 || p.StrokeWidth != 0 || len(p.Fill) != 4 || len(p.Stroke) != 4 || len(p.Shadow) != 4 || len(p.ShadowOffset) != 2 || len(p.Bleed) != 3 || p.Fill[3] != 255 {
		return zero, nil, p, errors.New("Ren typography drift")
	}
	f, ok := ty.Fonts[p.Face]
	if !ok || !strings.HasPrefix(f.Path, "third_party/fonts/") || filepath.IsAbs(f.Path) || strings.Contains(f.Path, "..") || len(f.SHA) != 64 {
		return zero, nil, p, errors.New("Ren unsafe original font")
	}
	font, e := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	return a, font, p, e
}
func renderRenGray(tid, font []byte, a familyAsset, p renGrayPreset) ([]byte, error) {
	if digest(tid) != renGraySHA {
		return nil, errors.New("Ren original TID SHA mismatch")
	}
	rgba, meta, e := mskit.DecodeTIDDXT5RGBA(tid)
	if e != nil {
		return nil, e
	}
	if meta.Width != 2048 || meta.Height != 2048 || meta.Format != "DXT5" {
		return nil, errors.New("Ren original format mismatch")
	}
	face, e := graphicsgo.ParseSFNT(font)
	if e != nil {
		return nil, e
	}
	imgs := make([]*image.NRGBA, len(a.Rows))
	used := make([]bool, meta.Width*meta.Height)
	fill := color.NRGBA{R: p.Fill[0], G: p.Fill[1], B: p.Fill[2], A: p.Fill[3]}
	shadow := color.NRGBA{R: p.Shadow[0], G: p.Shadow[1], B: p.Shadow[2], A: p.Shadow[3]}
	for i, row := range a.Rows {
		rc := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		w, h := rc.Dx(), rc.Dy()
		fit := graphicsgo.R32C3FitSpec{Width: w, Height: h, Factor: 4, NominalSize: p.Nominal, MinSize: p.Min, InnerWidth: 0, StrokeWidth: 0, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}
		comp := graphicsgo.R32C3CompositeSpec{Width: w, Height: h, Factor: 4, FillColor: fill, ShadowColor: shadow, ShadowOffset: image.Pt(p.ShadowOffset[0], p.ShadowOffset[1])}
		im, _, err := face.RenderR32C3LatinLabelFastRadial(row.Text, fit, graphicsgo.R32C3Warp{Factor: 4, RotationDegrees: p.Rotation}, comp, 2)
		if err != nil {
			return nil, fmt.Errorf("Ren row %d %s: %w", i, row.Text, err)
		}
		ink := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				j := y*im.Stride + x*4
				if im.Pix[j+3] == 0 {
					continue
				}
				if x == 0 || y == 0 || x == w-1 || y == h-1 {
					return nil, fmt.Errorf("Ren row %d clipped", i)
				}
				at := (y+rc.Min.Y)*meta.Width + x + rc.Min.X
				if used[at] {
					return nil, fmt.Errorf("Ren overlap row %d", i)
				}
				used[at] = true
				ink++
			}
		}
		if ink < 60 {
			return nil, fmt.Errorf("Ren row %d empty", i)
		}
		imgs[i] = im
	}
	for _, row := range a.Rows {
		rc := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		for y := rc.Min.Y; y < rc.Max.Y; y++ {
			for x := rc.Min.X; x < rc.Max.X; x++ {
				off := (y*meta.Width + x) * 4
				rgba[off], rgba[off+1], rgba[off+2], rgba[off+3] = p.Bleed[0], p.Bleed[1], p.Bleed[2], 0
			}
		}
	}
	for i, row := range a.Rows {
		rc := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		im := imgs[i]
		for y := 0; y < rc.Dy(); y++ {
			for x := 0; x < rc.Dx(); x++ {
				s := y*im.Stride + x*4
				if im.Pix[s+3] > 0 {
					d := ((y+rc.Min.Y)*meta.Width + rc.Min.X + x) * 4
					copy(rgba[d:d+4], im.Pix[s:s+4])
				}
			}
		}
	}
	return rgba, nil
}
