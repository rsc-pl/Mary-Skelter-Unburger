package main

// Finale Camp: 22 original-source vertical NeoFara labels. Shared lane raster
// is used, but the exact source geometry, font, colors and complete atlas SHA
// are bound independently. This path never writes an installed game.
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

const finaleCampID = "MSF_EN_CAMP_CHARACTER_NAMES"
const finaleCampSHA = "6dc75e6fc8271cc899459df6f5ab92ec4b79c03182879db186714e49313e7dd2"
const finaleCampRowsSHA = "aa5322a0f4f0d6d6196f5b04227d03ea27f7b489f2ead9aaf6bd1206ed0cecc6"

type finaleCampPreset struct {
	familyPreset
	Shadow       []uint8 `json:"shadow_rgba"`
	ShadowOffset []int   `json:"shadow_offset"`
	ShadowStroke int     `json:"shadow_stroke_width"`
}

func loadFinaleCamp(root string) (familyAsset, []byte, finaleCampPreset, error) {
	var z familyAsset
	raw, e := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if e != nil {
		return z, nil, finaleCampPreset{}, e
	}
	var m familyAssets
	if e = json.Unmarshal(raw, &m); e != nil {
		return z, nil, finaleCampPreset{}, e
	}
	a := z
	n := 0
	for _, v := range m.Assets {
		if v.ID == finaleCampID {
			a = v
			n++
		}
	}
	if n != 1 || a.Renderer != "ms2_name_family_font_v1" || a.Typography != "r32c3_finale_camp_v2" || a.Archive != 73 || a.Index != 89 || a.Inner != "camp_top_f.tid" || len(a.Rows) != 22 || len(a.Sources) != 1 || a.Sources[0].SHA != finaleCampSHA || a.Sources[0].Format != "DXT5" || a.Sources[0].Width != 2048 || a.Sources[0].Height != 2048 {
		return z, nil, finaleCampPreset{}, errors.New("Finale Camp source contract drift")
	}
	var layout strings.Builder
	for i, r := range a.Rows {
		if len(r.Rect) != 4 || r.Text == "" {
			return z, nil, finaleCampPreset{}, fmt.Errorf("camp row %d malformed", i)
		}
		box := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		if box.Empty() || box.Min.X < 820 || box.Max.X > 1345 || box.Min.Y < 1160 || box.Max.Y > 1995 || box.Dx() < 60 || box.Dx() > 85 || box.Dy() < 230 || box.Dy() > 310 {
			return z, nil, finaleCampPreset{}, fmt.Errorf("camp row %d invalid geometry", i)
		}
		fmt.Fprintf(&layout, "%s|%d,%d,%d,%d\n", r.Text, r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
	}
	h := sha256.Sum256([]byte(layout.String()))
	if hex.EncodeToString(h[:]) != finaleCampRowsSHA {
		return z, nil, finaleCampPreset{}, errors.New("Finale Camp layout SHA drift")
	}
	raw, e = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if e != nil {
		return z, nil, finaleCampPreset{}, e
	}
	// Unmarshal into separate existing font/preset structs to retain their JSON tags.
	var ty struct {
		Fonts   map[string]familyFont      `json:"fonts"`
		Presets map[string]json.RawMessage `json:"presets"`
	}
	if e = json.Unmarshal(raw, &ty); e != nil {
		return z, nil, finaleCampPreset{}, e
	}
	rawP, ok := ty.Presets[a.Typography]
	if !ok {
		return z, nil, finaleCampPreset{}, errors.New("camp style missing")
	}
	var p finaleCampPreset
	if e = json.Unmarshal(rawP, &p); e != nil {
		return z, nil, p, e
	}
	if p.Face != "neofara" || p.Size != 48 || p.MinSize != 22 || p.PadX != 2 || p.PadY != 2 || p.HS != .97 || p.VS != .97 || p.StrokeWidth != 1 || len(p.Fill) != 4 || len(p.Stroke) != 4 || p.Fill[0] != 246 || p.Fill[1] != 246 || p.Fill[2] != 246 || p.Fill[3] != 255 || len(p.Shadow) != 4 || len(p.ShadowOffset) != 2 || p.ShadowStroke != 1 {
		return z, nil, p, errors.New("Finale Camp style changed")
	}
	f, ok := ty.Fonts[p.Face]
	if !ok || !strings.HasPrefix(f.Path, "third_party/fonts/") || filepath.IsAbs(f.Path) || strings.Contains(f.Path, "..") || len(f.SHA) != 64 {
		return z, nil, p, errors.New("unsafe Finale Camp original font")
	}
	font, e := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	return a, font, p, e
}
func renderFinaleCamp(tid, font []byte, a familyAsset, p finaleCampPreset) ([]byte, error) {
	if digest(tid) != finaleCampSHA {
		return nil, errors.New("Finale Camp source TID SHA mismatch")
	}
	rgba, meta, e := mskit.DecodeTIDDXT5RGBA(tid)
	if e != nil {
		return nil, e
	}
	if meta.Width != 2048 || meta.Height != 2048 || meta.Format != "DXT5" {
		return nil, errors.New("Finale Camp format/geometry drift")
	}
	face, e := graphicsgo.ParseSFNT(font)
	if e != nil {
		return nil, e
	}
	// Pre-render every label and check physical ink overlap before clearing any original pixel.
	canvas := make([]*image.NRGBA, len(a.Rows))
	used := make([]bool, meta.Width*meta.Height)
	for i, r := range a.Rows {
		box := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		w, h := box.Dx(), box.Dy()
		lane, e := renderLegacyMask(face, r.Text, p.familyPreset, box)
		if e != nil {
			return nil, fmt.Errorf("Finale Camp row %d: %w", i, e)
		}
		stroke, e := graphicsgo.MaxFilterAlpha(lane, 3)
		if e != nil {
			return nil, e
		}
		im := image.NewNRGBA(image.Rect(0, 0, w, h))
		ink := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				// Source has CCW rotated vertical labels.
				sx := h - 1 - y
				sy := x
				f := lane.AlphaAt(sx, sy).A
				s := stroke.AlphaAt(sx, sy).A
				sh := uint8(0)
				shadowX, shadowY := sx-p.ShadowOffset[0], sy-p.ShadowOffset[1]
				if shadowX >= 0 && shadowX < h && shadowY >= 0 && shadowY < w {
					sh = stroke.AlphaAt(shadowX, shadowY).A
				}
				c := graphicsgo.OverPixels(color.NRGBA{R: 255, G: 255, B: 255, A: 0}, color.NRGBA{R: p.Shadow[0], G: p.Shadow[1], B: p.Shadow[2], A: uint8((uint16(sh)*uint16(p.Shadow[3]) + 127) / 255)})
				c = graphicsgo.OverPixels(c, color.NRGBA{R: uint8(p.Stroke[0]), G: uint8(p.Stroke[1]), B: uint8(p.Stroke[2]), A: s})
				c = graphicsgo.OverPixels(c, color.NRGBA{R: uint8(p.Fill[0]), G: uint8(p.Fill[1]), B: uint8(p.Fill[2]), A: f})
				if c.A == 0 {
					continue
				}
				ink++
				if x < 2 || x >= w-2 || y < 2 || y >= h-2 {
					return nil, fmt.Errorf("Finale Camp row %d clipped", i)
				}
				at := (y+box.Min.Y)*meta.Width + x + box.Min.X
				if used[at] {
					return nil, fmt.Errorf("Finale Camp physical overlapping labels at %d,%d", x+box.Min.X, y+box.Min.Y)
				}
				used[at] = true
				im.SetNRGBA(x, y, c)
			}
		}
		if ink < 80 {
			return nil, fmt.Errorf("Finale Camp blank row %d", i)
		}
		canvas[i] = im
	}
	cleared := make([]bool, meta.Width*meta.Height)
	for _, r := range a.Rows {
		box := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		for y := box.Min.Y; y < box.Max.Y; y++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				at := y*meta.Width + x
				if !cleared[at] {
					cleared[at] = true
					rgba[at*4], rgba[at*4+1], rgba[at*4+2], rgba[at*4+3] = 255, 255, 255, 0
				}
			}
		}
	}
	for i, r := range a.Rows {
		box := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		im := canvas[i]
		for y := 0; y < box.Dy(); y++ {
			for x := 0; x < box.Dx(); x++ {
				s := y*im.Stride + x*4
				if im.Pix[s+3] != 0 {
					d := ((y+box.Min.Y)*meta.Width + x + box.Min.X) * 4
					copy(rgba[d:d+4], im.Pix[s:s+4])
				}
			}
		}
	}
	return rgba, nil
}
