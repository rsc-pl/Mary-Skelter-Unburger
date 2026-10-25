package main

// Source-pinned, private legacy MS2 Camp gold compositor. Research only until
// full DXT5 encode, GDAT transaction and owner runtime approval. Every one of
// twelve character-name slots is rerendered for bank-wide style consistency.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
)

const campGoldFamilyID = "MS2_CAMP_CHARACTER_NAMES"
const campGoldSourceSHA = "60e36bc502a51cf763c6063476b3ec0648457f601899d0fac14ad0a7652d860c"

var campGoldNames = []string{"ALICE", "AKAZUKIN", "OYAYUBIHIME", "SHIRAYUKIHIME", "NEMURIHIME", "GRETEL", "RAPUNZEL", "CINDERELLA", "KAGUYAHIME", "HAMELN", "TSUU", "NINGYOHIME"}
var campGoldRects = [][4]int{{0, 0, 430, 88}, {430, 0, 900, 88}, {0, 88, 430, 176}, {430, 88, 900, 176}, {0, 176, 430, 264}, {430, 176, 900, 264}, {0, 264, 430, 352}, {430, 264, 900, 352}, {0, 352, 430, 440}, {430, 352, 900, 440}, {0, 440, 430, 528}, {430, 440, 900, 528}}

func loadCamp(root string) (familyAsset, []byte, bonusWhitePreset, error) {
	var empty familyAsset
	b, e := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if e != nil {
		return empty, nil, bonusWhitePreset{}, e
	}
	var mf familyAssets
	if e = json.Unmarshal(b, &mf); e != nil {
		return empty, nil, bonusWhitePreset{}, e
	}
	var a familyAsset
	count := 0
	for _, f := range mf.Assets {
		if f.ID == campGoldFamilyID {
			a = f
			count++
		}
	}
	if count != 1 || a.Renderer != "ms2_name_family_font_v1" || a.Typography != "r32c3_camp_gold" || a.Archive != 55 || a.Index != 1431 || a.Inner != "Camp_common_CHARA.tid" || len(a.Rows) != 12 || len(a.Sources) != 1 || a.Sources[0].SHA != campGoldSourceSHA || a.Sources[0].Width != 1024 || a.Sources[0].Height != 1024 || a.Sources[0].Format != "DXT5" {
		return empty, nil, bonusWhitePreset{}, errors.New("legacy camp manifest/source drift")
	}
	for i, r := range a.Rows {
		if r.Text != campGoldNames[i] || len(r.Rect) != 4 {
			return empty, nil, bonusWhitePreset{}, fmt.Errorf("camp text drift %d", i)
		}
		for j, v := range campGoldRects[i] {
			if r.Rect[j] != v {
				return empty, nil, bonusWhitePreset{}, fmt.Errorf("camp rectangle drift %d", i)
			}
		}
	}
	b, e = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if e != nil {
		return empty, nil, bonusWhitePreset{}, e
	}
	var cfg struct {
		Fonts   map[string]familyFont          `json:"fonts"`
		Presets map[string]bonusWhitePreset `json:"presets"`
	}
	if e = json.Unmarshal(b, &cfg); e != nil {
		return empty, nil, bonusWhitePreset{}, e
	}
	p, ok := cfg.Presets[a.Typography]
	if !ok || p.Face != "avionic" || p.Nominal != 70 || p.Min != 28 || p.HScale != 0.98 || p.HMin != 0.52 || p.VScale != 0.96 || p.Shear != -0.18 || p.Rotation != 0 || p.InnerWidth != 3 || p.OuterWidth != 3 || len(p.Inner) != 4 || len(p.Outer) != 4 || len(p.Fill) != 4 || len(p.Shadow) != 4 || len(p.ShadowOffset) != 2 || len(p.Bleed) != 3 || p.ShadowOffset[0] != 6 || p.ShadowOffset[1] != 7 {
		return empty, nil, p, errors.New("legacy camp gold typography drift")
	}
	fi, ok := cfg.Fonts[p.Face]
	if !ok || !strings.HasPrefix(fi.Path, "third_party/fonts/") || strings.Contains(fi.Path, "..") || filepath.IsAbs(fi.Path) || len(fi.SHA) != 64 {
		return empty, nil, p, errors.New("legacy camp unsafe font locator")
	}
	font, e := familyReadPinned(filepath.Join(root, fi.Path), fi.SHA)
	return a, font, p, e
}

func renderCamp(tid, font []byte, a familyAsset, p bonusWhitePreset) ([]byte, error) {
	if digest(tid) != campGoldSourceSHA {
		return nil, errors.New("legacy camp TID SHA mismatch")
	}
	rgba, meta, e := mskit.DecodeTIDDXT5RGBA(tid)
	if e != nil {
		return nil, e
	}
	if meta.Width != 1024 || meta.Height != 1024 || meta.Format != "DXT5" {
		return nil, errors.New("legacy camp source format drift")
	}
	face, e := graphicsgo.ParseSFNT(font)
	if e != nil {
		return nil, e
	}
	labels := make([]*image.NRGBA, 12)
	for j, r := range a.Rows {
		rc := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		w, h := rc.Dx(), rc.Dy()
		fit := graphicsgo.R32C3FitSpec{Width: w, Height: h, Factor: 4, NominalSize: p.Nominal, MinSize: p.Min, InnerWidth: p.InnerWidth, StrokeWidth: p.OuterWidth, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}
		layer := graphicsgo.R32C3CompositeSpec{Width: w, Height: h, Factor: 4, InnerColor: asNRGBA(p.Inner), OuterColor: asNRGBA(p.Outer), FillColor: asNRGBA(p.Fill), ShadowColor: asNRGBA(p.Shadow), ShadowOffset: image.Pt(p.ShadowOffset[0], p.ShadowOffset[1])}
		label, _, err := face.RenderR32C3LatinLabelFastRadial(r.Text, fit, graphicsgo.R32C3Warp{Factor: 4}, layer, 2)
		if err != nil {
			return nil, fmt.Errorf("camp row %d %s: %w", j, r.Text, err)
		}
		ink := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if label.Pix[y*label.Stride+4*x+3] > 0 {
					ink++
					if x < 1 || y < 1 || x >= w-1 || y >= h-1 {
						return nil, fmt.Errorf("camp clipped row %d %s", j, r.Text)
					}
				}
			}
		}
		if ink < 75 {
			return nil, fmt.Errorf("camp blank row %d", j)
		}
		labels[j] = label
	}
	// Source regions are non-overlapping, retain bytes outside them untouched.
	out := bytes.Clone(rgba)
	for j, r := range a.Rows {
		rc := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		label := labels[j]
		for y := 0; y < rc.Dy(); y++ {
			for x := 0; x < rc.Dx(); x++ {
				i := ((rc.Min.Y+y)*meta.Width + rc.Min.X + x) * 4
				k := y*label.Stride + x*4
				out[i], out[i+1], out[i+2], out[i+3] = p.Bleed[0], p.Bleed[1], p.Bleed[2], 0
				if label.Pix[k+3] != 0 {
					copy(out[i:i+4], label.Pix[k:k+4])
				}
			}
		}
	}
	return out, nil
}

func campGoldLayoutDigest() string {
	h := sha256.New()
	for i, r := range campGoldRects {
		fmt.Fprintf(h, "%s|%d,%d,%d,%d\n", campGoldNames[i], r[0], r[1], r[2], r[3])
	}
	return hex.EncodeToString(h.Sum(nil))
}
