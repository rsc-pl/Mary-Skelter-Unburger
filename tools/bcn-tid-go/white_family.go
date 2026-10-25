package main

// legacy: source-bound shared white-name renderer for MS2 and Finale. All inputs
// are private, exact original TIDs and SHA-pinned licensed SFNT bytes. The
// routine composites row ink after clearing the union of source-defined row
// rectangles: overlapping rectangles must never erase text drawn earlier.
import (
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

const (
	ms2SelectID  = "MS2_BONUS_CHARACTER_SELECT_NAMES"
	msfSelectID  = "MSF_EN_BONUS_CHARACTER_SELECT_NAMES"
	msfBonusID   = "MSF_EN_BONUS_CHARACTER_NAMES"
	ms2SelectSHA = "4f0a5e8fb9f01b96590ed14226e8c33c44f9c993f3faa0ca6135168ea2bae34b"
	msfSelectSHA = "1dee0ba6333d836942b181695e08d8e26de5edbf72109bf9f29fd9176e49fb51"
	msfBonusSHA  = "abeeb446fe1674ff21750a3eea060809b887b73ac13e2ad02beaddae24b6aa92"
)

type whiteFamilySpec struct {
	id, sha, inner, preset                string
	archive, index, rows                  int
	size, min                             int
	rotation, shear, hscale, hmin, vscale float64
	innerWidth, outerWidth                int
}

var whiteSpecs = map[string]whiteFamilySpec{
	ms2SelectID: {ms2SelectID, ms2SelectSHA, "Bonus_Character_CharacterSelect.tid", "r32c3_white_oblique_small", 55, 1335, 26, 69, 20, 4, -.29, .99, .42, .96, 1, 1},
	msfSelectID: {msfSelectID, msfSelectSHA, "Bonus_Character_CharacterSelect.tid", "r32c3_finale_bonus_select", 73, 1, 26, 76, 22, 3.4, -.31, .99, .4, .95, 1, 1},
	msfBonusID:  {msfBonusID, msfBonusSHA, "BONUS_CHARACTER.tid", "r32c3_finale_bonus_name", 73, 18, 12, 64, 24, 2.6, -.22, .97, .54, .96, 3, 1},
}

var whiteNames = []string{"JACK", "ALICE", "AKAZUKIN", "OYAYUBIHIME", "SHIRAYUKIHIME", "KADOWAKI\nTOWA", "NEMURIHIME", "GRETEL", "RAPUNZEL", "CINDERELLA", "KAGUYAHIME", "HAMELN", "PROFESSOR\nTOOJIMA", "HARU", "UESHIMA MIKO", "CLARA", "MICHIRU", "CHIAKI", "HIKARI", "MAMORU", "TSUU", "WANDERING\nMERCHANT", "ORDER\nFOLLOWER", "LIBERATOR", "RESEARCHER", "NINGYOHIME"}
var finaleNames = []string{"Tsuu", "Oyayubihime", "Ningyohime", "Shirayukihime", "Hameln", "Nemurihime", "Gretel", "Cinderella", "Rapunzel", "Akazukin", "Kaguyahime", "Alice"}

func readWhiteFamily(root, id string) (familyAsset, []byte, bonusWhitePreset, error) {
	var zero familyAsset
	spec, exists := whiteSpecs[id]
	if !exists {
		return zero, nil, bonusWhitePreset{}, fmt.Errorf("unproven legacy family %s", id)
	}
	raw, err := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if err != nil {
		return zero, nil, bonusWhitePreset{}, err
	}
	var mf familyAssets
	if err = json.Unmarshal(raw, &mf); err != nil {
		return zero, nil, bonusWhitePreset{}, err
	}
	var a familyAsset
	found := 0
	for _, v := range mf.Assets {
		if v.ID == id {
			a = v
			found++
		}
	}
	if found != 1 || a.Renderer != "ms2_name_family_font_v1" || a.Typography != spec.preset || a.Archive != spec.archive || a.Index != spec.index || a.Inner != spec.inner || len(a.Rows) != spec.rows || len(a.Sources) != 1 || a.Sources[0].SHA != spec.sha || a.Sources[0].Width != 2048 || a.Sources[0].Height != 2048 || a.Sources[0].Format != "DXT5" {
		return zero, nil, bonusWhitePreset{}, fmt.Errorf("%s source manifest drift", id)
	}
	// Fix both the canonical strings and all 4 rectangle coordinates. A
	// source-text/layout drift must not be mistaken for a validated family.
	layoutSHA := "7556ad2bd82ad7f916e1db3f1f8c6d892da1954f0277dae98635f2e27405dce0"
	if id == msfBonusID {
		layoutSHA = "d6889cec7da41406ef870a276fc8315d76bf0e83044c207d8674e8dd3211b54b"
	}
	var layout strings.Builder
	for _, r := range a.Rows {
		fmt.Fprintf(&layout, "%s|%d,%d,%d,%d\n", r.Text, r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
	}
	h := sha256.Sum256([]byte(layout.String()))
	if hex.EncodeToString(h[:]) != layoutSHA {
		return zero, nil, bonusWhitePreset{}, errors.New("legacy original row layout SHA changed")
	}
	names := whiteNames
	if id == msfBonusID {
		names = finaleNames
	}
	for i, v := range a.Rows {
		if v.Text != names[i] || len(v.Rect) != 4 {
			return zero, nil, bonusWhitePreset{}, fmt.Errorf("%s row %d changed", id, i)
		}
		r := image.Rect(v.Rect[0], v.Rect[1], v.Rect[2], v.Rect[3])
		if r.Empty() || r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > 2048 || r.Max.Y > 2048 || r.Dx() < 55 || r.Dy() < 55 {
			return zero, nil, bonusWhitePreset{}, fmt.Errorf("%s row %d geometry", id, i)
		}
	}
	raw, err = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if err != nil {
		return zero, nil, bonusWhitePreset{}, err
	}
	var cfg struct {
		Fonts   map[string]familyFont          `json:"fonts"`
		Presets map[string]bonusWhitePreset `json:"presets"`
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return zero, nil, bonusWhitePreset{}, err
	}
	p, ok := cfg.Presets[spec.preset]
	if !ok || p.Face != "avionic" || p.Nominal != spec.size || p.Min != spec.min || p.Rotation != spec.rotation || p.Shear != spec.shear || p.HScale != spec.hscale || p.HMin != spec.hmin || p.VScale != spec.vscale || p.InnerWidth != spec.innerWidth || p.OuterWidth != spec.outerWidth || len(p.Inner) != 4 || len(p.Outer) != 4 || len(p.Fill) != 4 || len(p.Shadow) != 4 || len(p.ShadowOffset) != 2 || len(p.Bleed) != 3 {
		return zero, nil, bonusWhitePreset{}, fmt.Errorf("%s typography drift", id)
	}
	f, ok := cfg.Fonts[p.Face]
	if !ok || !strings.HasPrefix(f.Path, "third_party/fonts/") || strings.Contains(f.Path, "..") || filepath.IsAbs(f.Path) || len(f.SHA) != 64 {
		return zero, nil, bonusWhitePreset{}, errors.New("legacy invalid pinned font")
	}
	font, err := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	return a, font, p, err
}

func renderWhiteFamily(tid, font []byte, a familyAsset, p bonusWhitePreset) ([]byte, error) {
	spec, ok := whiteSpecs[a.ID]
	if !ok || digest(tid) != spec.sha {
		return nil, errors.New("legacy original source TID SHA mismatch")
	}
	rgba, meta, err := mskit.DecodeTIDDXT5RGBA(tid)
	if err != nil {
		return nil, err
	}
	if meta.Width != 2048 || meta.Height != 2048 || meta.Format != "DXT5" {
		return nil, errors.New("legacy source TID dimensions or format drift")
	}
	sf, err := graphicsgo.ParseSFNT(font)
	if err != nil {
		return nil, err
	}
	layers := make([]*image.NRGBA, len(a.Rows))
	used := make([]uint8, meta.Width*meta.Height)
	for i, row := range a.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		label := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		lines := strings.Split(row.Text, "\n")
		if len(lines) < 1 || len(lines) > 2 {
			return nil, fmt.Errorf("legacy unexpected line count %s", row.Text)
		}
		for n, line := range lines {
			top := n * r.Dy() / len(lines)
			bottom := (n + 1) * r.Dy() / len(lines)
			if line == "" || bottom-top < 50 {
				return nil, fmt.Errorf("legacy empty/too short line %s", row.Text)
			}
			fit := graphicsgo.R32C3FitSpec{Width: r.Dx(), Height: bottom - top, Factor: 4, NominalSize: p.Nominal, MinSize: p.Min, InnerWidth: p.InnerWidth, StrokeWidth: p.OuterWidth, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}
			comp := graphicsgo.R32C3CompositeSpec{Width: r.Dx(), Height: bottom - top, Factor: 4, InnerColor: asNRGBA(p.Inner), OuterColor: asNRGBA(p.Outer), FillColor: asNRGBA(p.Fill), ShadowColor: asNRGBA(p.Shadow), ShadowOffset: image.Pt(p.ShadowOffset[0], p.ShadowOffset[1])}
			pic, _, e := sf.RenderR32C3LatinLabelFastRadial(line, fit, graphicsgo.R32C3Warp{Factor: 4, RotationDegrees: p.Rotation}, comp, 2)
			if e != nil {
				return nil, fmt.Errorf("legacy %s/%s render: %w", a.ID, row.Text, e)
			}
			// A two-line label must not be silently cropped at its *internal*
			// line split. The outer-row clipping test alone cannot catch this.
			for y := 0; y < pic.Rect.Dy(); y++ {
				for x := 0; x < pic.Rect.Dx(); x++ {
					if pic.Pix[y*pic.Stride+x*4+3] != 0 && (x == 0 || y == 0 || x == pic.Rect.Dx()-1 || y == pic.Rect.Dy()-1) {
						return nil, fmt.Errorf("legacy %s/%s line %d clipped at %d,%d", a.ID, row.Text, n, x, y)
					}
				}
				copy(label.Pix[(top+y)*label.Stride:(top+y)*label.Stride+4*r.Dx()], pic.Pix[y*pic.Stride:y*pic.Stride+4*r.Dx()])
			}
		}
		ink := 0
		for y := 0; y < r.Dy(); y++ {
			for x := 0; x < r.Dx(); x++ {
				k := y*label.Stride + x*4
				if label.Pix[k+3] == 0 {
					continue
				}
				if x == 0 || y == 0 || x == r.Dx()-1 || y == r.Dy()-1 {
					return nil, fmt.Errorf("legacy clipped %s %s", a.ID, row.Text)
				}
				ix := (y+r.Min.Y)*meta.Width + x + r.Min.X
				if used[ix] != 0 {
					return nil, fmt.Errorf("legacy glyph/shadow overlap %s at (%d,%d)", a.ID, x+r.Min.X, y+r.Min.Y)
				}
				used[ix] = 1
				ink++
			}
		}
		if ink < 75 {
			return nil, fmt.Errorf("legacy missing ink %s %s", a.ID, row.Text)
		}
		layers[i] = label
	}
	cleared := make([]bool, meta.Width*meta.Height)
	for _, row := range a.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				k := y*meta.Width + x
				if !cleared[k] {
					cleared[k] = true
					off := k * 4
					clear(rgba[off : off+4])
				}
			}
		}
	}
	for i, row := range a.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		label := layers[i]
		for y := 0; y < r.Dy(); y++ {
			for x := 0; x < r.Dx(); x++ {
				pos := y*label.Stride + x*4
				if label.Pix[pos+3] != 0 {
					dst := ((y+r.Min.Y)*meta.Width + x + r.Min.X) * 4
					copy(rgba[dst:dst+4], label.Pix[pos:pos+4])
				}
			}
		}
	}
	return rgba, nil
}
