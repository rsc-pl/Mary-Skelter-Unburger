package main

// legacy: Three owner-reviewed pink Go atlas renderers. The owner accepted minor
// cosmetic Python/Go/DXT5 differences at ordinary viewing size after seeing
// comparisons. Approval is tied to the *exact* RGBA targets below, the pinned
// pristine TIDs/font and geometry. It does not relax unrelated family guards.
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

const (
	pink24ID     = "MS2_JOB_CLASS_24PX"
	pinkMiddleID = "MS2_JOB_CLASS_MIDDLE"
	pinkSkillID  = "MS2_KESSIKI_SKILL_LABELS"
)

type pinkChange struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rect []int  `json:"rect"`
}
type pinkAsset struct {
	ID         string `json:"id"`
	Renderer   string `json:"renderer"`
	Typography string `json:"typography_preset"`
	Archive    int    `json:"archive"`
	Index      int    `json:"gdat_index"`
	Inner      string `json:"cl3_inner_name"`
	FontSize   int    `json:"font_nominal_size"`
	Source     []struct {
		SHA    string `json:"tid_sha256"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Format string `json:"format"`
	} `json:"source_variants"`
	Replacements []pinkChange `json:"replacements"`
}
type pinkPreset struct {
	Face         string  `json:"font_face"`
	Nominal      int     `json:"nominal_size"`
	Min          int     `json:"min_size"`
	Fill         []uint8 `json:"fill_rgba"`
	Stroke       []uint8 `json:"stroke_rgba"`
	StrokeWidth  int     `json:"stroke_width"`
	HScale       float64 `json:"horizontal_scale"`
	HMin         float64 `json:"min_horizontal_scale"`
	VScale       float64 `json:"vertical_scale"`
	Shear        float64 `json:"shear_x"`
	Shadow       []uint8 `json:"shadow_rgba"`
	ShadowOffset []int   `json:"shadow_offset"`
	Bleed        []uint8 `json:"transparent_bleed_rgb"`
}
type pinkConfig struct {
	Presets map[string]pinkPreset `json:"presets"`
	Fonts   map[string]familyFont     `json:"fonts"`
}
type pinkApproval struct {
	width, height, labels int
	source, target        string
	preset                string
}

func pinkApprovalFor(id string) (pinkApproval, bool) {
	switch id {
	case pink24ID:
		return pinkApproval{2048, 512, 9, "687a6b6bd576b686784c8325ebfe6334312c8629b879a2b97ebf4fbcd54991d3", "b860758e8c0f8523a5b833e98fa2d115363a2d1fa9012f3409a16eeb82375559", "pink_block_small_neofara"}, true
	case pinkMiddleID:
		return pinkApproval{1024, 1024, 9, "f0171ef248287210ec12c2e73d9e4a282e33cce53e06b51211c9ba41ebbccaad", "57c142e188550d9261854c2e31d2e9b4961bbb8e3d1fbf13214eee49ff0434b6", "pink_block_small_neofara"}, true
	case pinkSkillID:
		return pinkApproval{1024, 1024, 3, "3a57a59b004c92181f2c7210100a557aa51bbb91cd4d9d6508ddea6512fdc738", "c133a411c876d7688d40b95193788fe8ecf59f1b5d4db98c4c304397397b737f", "pink_block_large_neofara"}, true
	}
	return pinkApproval{}, false
}
func pinkColor(v []uint8) color.NRGBA { return color.NRGBA{R: v[0], G: v[1], B: v[2], A: v[3]} }
func pinkBytesHash(b []byte) string   { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func loadPinkAtlas(root, id string) (familyAsset, []byte, pinkAsset, pinkPreset, error) {
	var zero familyAsset
	approval, ok := pinkApprovalFor(id)
	if !ok {
		return zero, nil, pinkAsset{}, pinkPreset{}, errors.New("family not owner approved")
	}
	blob, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return zero, nil, pinkAsset{}, pinkPreset{}, err
	}
	var a struct {
		Assets []pinkAsset `json:"assets"`
	}
	if err = json.Unmarshal(blob, &a); err != nil {
		return zero, nil, pinkAsset{}, pinkPreset{}, err
	}
	var asset pinkAsset
	count := 0
	for _, cand := range a.Assets {
		if cand.ID == id {
			asset = cand
			count++
		}
	}
	if count != 1 || asset.Renderer != "font_atlas_v2" || asset.Archive != 55 || asset.Inner == "" || asset.Typography != approval.preset || len(asset.Source) != 1 || len(asset.Replacements) != approval.labels || asset.Source[0].SHA != approval.source || asset.Source[0].Width != approval.width || asset.Source[0].Height != approval.height || asset.Source[0].Format != "DXT5" || asset.Index < 0 {
		return zero, nil, pinkAsset{}, pinkPreset{}, errors.New("legacy source or layout manifest drift")
	}
	// Exact record mapping: no alternate atlas can inherit this approval.
	expectedIndex := 1453
	expectedInner := "jobname_24px.tid"
	if id == pinkMiddleID {
		expectedIndex = 1454
		expectedInner = "jobname_middle.tid"
	}
	if id == pinkSkillID {
		expectedIndex = 1455
		expectedInner = "name_kessiki30px.tid"
	}
	if asset.Index != expectedIndex || asset.Inner != expectedInner {
		return zero, nil, pinkAsset{}, pinkPreset{}, errors.New("unexpected legacy GDAT member")
	}
	blob, err = os.ReadFile(filepath.Join(root, "graphics", "ui_typography.json"))
	if err != nil {
		return zero, nil, pinkAsset{}, pinkPreset{}, err
	}
	var c pinkConfig
	if err = json.Unmarshal(blob, &c); err != nil {
		return zero, nil, pinkAsset{}, pinkPreset{}, err
	}
	p, ok := c.Presets[asset.Typography]
	if !ok || p.Face != "neofara" || p.Min < 12 || p.Nominal < 24 || p.HScale <= 0 || p.HMin <= 0 || p.VScale <= 0 || len(p.Stroke) != 4 || len(p.Fill) != 4 || len(p.Shadow) != 4 || len(p.ShadowOffset) != 2 || len(p.Bleed) != 3 {
		return zero, nil, pinkAsset{}, pinkPreset{}, errors.New("unsupported legacy typography")
	}
	f, ok := c.Fonts[p.Face]
	if !ok || !strings.HasPrefix(f.Path, "third_party/fonts/") || strings.Contains(f.Path, "..") || filepath.IsAbs(f.Path) || len(f.SHA) != 64 {
		return zero, nil, pinkAsset{}, pinkPreset{}, errors.New("unsafe legacy font path")
	}
	font, err := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	if err != nil {
		return zero, nil, pinkAsset{}, pinkPreset{}, err
	}
	seen := map[[4]int]bool{}
	for _, v := range asset.Replacements {
		if v.From == "" || v.To == "" || len(v.Rect) != 4 {
			return zero, nil, pinkAsset{}, pinkPreset{}, errors.New("invalid legacy replacement")
		}
		r := [4]int{v.Rect[0], v.Rect[1], v.Rect[2], v.Rect[3]}
		if r[0] < 0 || r[1] < 0 || r[2] > approval.width || r[3] > approval.height || r[0] >= r[2] || r[1] >= r[3] || seen[r] {
			return zero, nil, pinkAsset{}, pinkPreset{}, errors.New("invalid or duplicate legacy rect")
		}
		seen[r] = true
	}
	for i, x := range asset.Replacements {
		for _, y := range asset.Replacements[i+1:] {
			a, b := x.Rect, y.Rect
			if a[0] < b[2] && a[2] > b[0] && a[1] < b[3] && a[3] > b[1] {
				return zero, nil, pinkAsset{}, pinkPreset{}, errors.New("overlapping legacy rectangles")
			}
		}
	}
	return familyAsset{ID: asset.ID, Archive: asset.Archive, Index: asset.Index, Inner: asset.Inner}, font, asset, p, nil
}

func renderPinkAtlas(tid, font []byte, a pinkAsset, p pinkPreset) ([]byte, error) {
	approval, ok := pinkApprovalFor(a.ID)
	if !ok || pinkBytesHash(tid) != approval.source {
		return nil, errors.New("wrong pristine pink atlas TID")
	}
	out, meta, err := mskit.DecodeTIDDXT5RGBA(tid)
	if err != nil {
		return nil, err
	}
	if meta.Width != approval.width || meta.Height != approval.height {
		return nil, errors.New("wrong pink atlas geometry")
	}
	sf, err := graphicsgo.ParseSFNT(font)
	if err != nil {
		return nil, err
	}
	for _, v := range a.Replacements {
		r := v.Rect
		w, h := r[2]-r[0], r[3]-r[1]
		size := p.Nominal
		if a.FontSize > 0 {
			size = a.FontSize
		}
		fit := graphicsgo.R32C3FitSpec{Width: w, Height: h, Factor: 4, NominalSize: size, MinSize: p.Min, StrokeWidth: p.StrokeWidth, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}
		comp := graphicsgo.R32C3CompositeSpec{Width: w, Height: h, Factor: 4, OuterColor: pinkColor(p.Stroke), FillColor: pinkColor(p.Fill), ShadowColor: pinkColor(p.Shadow), ShadowOffset: image.Pt(p.ShadowOffset[0], p.ShadowOffset[1])}
		img, _, err := sf.RenderR32C3LatinLabelFastRadial(v.To, fit, graphicsgo.R32C3Warp{Factor: 4}, comp, 2)
		if err != nil {
			return nil, fmt.Errorf("label %q: %w", v.To, err)
		}
		// In-game text cannot touch a crop edge; alpha halos and clipping remain blockers.
		for x := 0; x < w; x++ {
			if img.NRGBAAt(x, 0).A != 0 || img.NRGBAAt(x, h-1).A != 0 {
				return nil, fmt.Errorf("legacy cropped vertical edge: %s", v.To)
			}
		}
		for y := 0; y < h; y++ {
			if img.NRGBAAt(0, y).A != 0 || img.NRGBAAt(w-1, y).A != 0 {
				return nil, fmt.Errorf("legacy cropped horizontal edge: %s", v.To)
			}
		}
		for y := r[1]; y < r[3]; y++ {
			for x := r[0]; x < r[2]; x++ {
				off := (y*meta.Width + x) * 4
				out[off], out[off+1], out[off+2], out[off+3] = p.Bleed[0], p.Bleed[1], p.Bleed[2], 0
			}
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := img.NRGBAAt(x, y)
				if c.A == 0 {
					continue
				}
				off := ((y+r[1])*meta.Width + x + r[0]) * 4
				da, sa := int(out[off+3]), int(c.A)
				oa := sa + (da*(255-sa)+127)/255
				if oa == 0 {
					continue
				}
				for k, col := range []uint8{c.R, c.G, c.B} {
					dp := int(out[off+k]) * da * (255 - sa)
					sp := int(col) * sa * 255
					out[off+k] = byte((sp + dp + oa*127) / (oa * 255))
				}
				out[off+3] = byte(oa)
			}
		}
	}
	if pinkBytesHash(out) != approval.target {
		return nil, fmt.Errorf("legacy owner-approved RGBA target differs from frozen comparative QA for %s", a.ID)
	}
	return out, nil
}
