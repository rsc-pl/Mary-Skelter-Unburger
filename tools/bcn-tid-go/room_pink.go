package main

// Opt-in original-source research: Finale heroine-room pink R32C3 labels.
// An atlas is not a validated candidate until a complete source TID passes
// the existing 64/64 changed-block encoder plus GDAT roundtrip.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
	"os"
	"path/filepath"
	"strings"
)

const roomPinkID = "MSF_EN_HEROINE_ROOM_NAMES"
const roomPinkSHA = "5fc97e0deede947bab42c6afcf5a0ebeb7fdc7edaa7f46e99953b5da16131349"
const roomPinkLayout = "e8127b984f07d622f59541053e843fbb2c6bdb605ec221ed27feb2b4f5b944f8"

type roomPinkPreset struct {
	Face         string  `json:"font_face"`
	Nominal      int     `json:"nominal_size"`
	Min          int     `json:"min_size"`
	HScale       float64 `json:"horizontal_scale"`
	HMin         float64 `json:"min_horizontal_scale"`
	VScale       float64 `json:"vertical_scale"`
	Shear        float64 `json:"shear_x"`
	InnerWidth   int     `json:"r32c3_inner_width"`
	OuterWidth   int     `json:"r32c3_outer_width"`
	Inner        []uint8 `json:"r32c3_inner_rgba"`
	Outer        []uint8 `json:"r32c3_outer_rgba"`
	GradTop      []uint8 `json:"fill_gradient_top_rgba"`
	GradBottom   []uint8 `json:"fill_gradient_bottom_rgba"`
	Shadow       []uint8 `json:"shadow_rgba"`
	ShadowOffset []int   `json:"shadow_offset"`
}

func loadRoomPink(root string) (familyAsset, []byte, roomPinkPreset, error) {
	var zero familyAsset
	raw, err := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if err != nil {
		return zero, nil, roomPinkPreset{}, err
	}
	var manifest familyAssets
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return zero, nil, roomPinkPreset{}, err
	}
	var a familyAsset
	matches := 0
	for _, v := range manifest.Assets {
		if v.ID == roomPinkID {
			a = v
			matches++
		}
	}
	if matches != 1 || a.Renderer != "ms2_name_family_font_v1" || a.Typography != "r32c3_room_pink_fsf" || a.Archive != 73 || a.Index != 222 || a.Inner != "kyoten_heroineroom_f.tid" || len(a.Rows) != 15 || len(a.Sources) != 1 || a.Sources[0].SHA != roomPinkSHA || a.Sources[0].Format != "DXT5" || a.Sources[0].Width != 2048 || a.Sources[0].Height != 2048 {
		return zero, nil, roomPinkPreset{}, errors.New("room source/manifest changed")
	}
	var layout strings.Builder
	for i, r := range a.Rows {
		if len(r.Rect) != 4 || r.Text == "" {
			return zero, nil, roomPinkPreset{}, fmt.Errorf("room row %d invalid", i)
		}
		rect := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		if rect.Empty() || rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > 2048 || rect.Max.Y > 2048 || rect.Dx() < 250 || rect.Dy() < 80 {
			return zero, nil, roomPinkPreset{}, fmt.Errorf("room row %d geometry drift", i)
		}
		fmt.Fprintf(&layout, "%s|%d,%d,%d,%d\n", r.Text, r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
	}
	h := sha256.Sum256([]byte(layout.String()))
	if hex.EncodeToString(h[:]) != roomPinkLayout {
		return zero, nil, roomPinkPreset{}, errors.New("room row layout SHA drift")
	}
	raw, err = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if err != nil {
		return zero, nil, roomPinkPreset{}, err
	}
	var ty struct {
		Fonts   map[string]familyFont         `json:"fonts"`
		Presets map[string]roomPinkPreset `json:"presets"`
	}
	if err = json.Unmarshal(raw, &ty); err != nil {
		return zero, nil, roomPinkPreset{}, err
	}
	p, ok := ty.Presets[a.Typography]
	if !ok || p.Face != "chiaro" || p.Nominal != 46 || p.Min != 18 || p.HScale != .98 || p.HMin != .04 || p.VScale != .98 || p.Shear != 0 || p.InnerWidth != 4 || p.OuterWidth != 4 || len(p.Inner) != 4 || len(p.Outer) != 4 || len(p.GradTop) != 4 || len(p.GradBottom) != 4 || len(p.Shadow) != 4 || len(p.ShadowOffset) != 2 {
		return zero, nil, p, errors.New("room canonical typography drift")
	}
	f, ok := ty.Fonts[p.Face]
	if !ok || !strings.HasPrefix(f.Path, "third_party/fonts/") || strings.Contains(f.Path, "..") || filepath.IsAbs(f.Path) || len(f.SHA) != 64 {
		return zero, nil, p, errors.New("room unsafe font")
	}
	font, err := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	return a, font, p, err
}
func roomNRGBA(v []uint8) color.NRGBA { return color.NRGBA{R: v[0], G: v[1], B: v[2], A: v[3]} }
func renderRoomPink(tid, fontBytes []byte, a familyAsset, p roomPinkPreset) ([]byte, error) {
	if digest(tid) != roomPinkSHA {
		return nil, errors.New("room pristine TID SHA mismatch")
	}
	rgba, meta, err := mskit.DecodeTIDDXT5RGBA(tid)
	if err != nil {
		return nil, err
	}
	if meta.Width != 2048 || meta.Height != 2048 || meta.Format != "DXT5" {
		return nil, errors.New("room TID geometry/format drift")
	}
	face, err := graphicsgo.ParseSFNT(fontBytes)
	if err != nil {
		return nil, err
	}
	images := make([]*image.NRGBA, len(a.Rows))
	used := make([]bool, meta.Width*meta.Height)
	for i, r := range a.Rows {
		rect := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		w, h := rect.Dx(), rect.Dy()
		fit := graphicsgo.R32C3FitSpec{Width: w, Height: h, Factor: 4, NominalSize: p.Nominal, MinSize: p.Min, InnerWidth: p.InnerWidth, StrokeWidth: p.OuterWidth, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}
		comp := graphicsgo.R32C3CompositeSpec{Width: w, Height: h, Factor: 4, OuterColor: roomNRGBA(p.Outer), InnerColor: roomNRGBA(p.Inner), GradientEnabled: true, GradientTop: roomNRGBA(p.GradTop), GradientBottom: roomNRGBA(p.GradBottom), ShadowColor: roomNRGBA(p.Shadow), ShadowOffset: image.Pt(p.ShadowOffset[0], p.ShadowOffset[1])}
		im, _, e := face.RenderR32C3LatinLabelFastRadial(r.Text, fit, graphicsgo.R32C3Warp{Factor: 4}, comp, 2)
		if e != nil {
			return nil, fmt.Errorf("room %s: %w", r.Text, e)
		}
		ink := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				k := y*im.Stride + x*4
				if im.Pix[k+3] == 0 {
					continue
				}
				ink++
				if x == 0 || y == 0 || x == w-1 || y == h-1 {
					return nil, fmt.Errorf("room row %d clipped", i)
				}
				ix := (y+rect.Min.Y)*meta.Width + x + rect.Min.X
				if used[ix] {
					return nil, fmt.Errorf("room row %d overlaps another label at %d,%d", i, rect.Min.X+x, rect.Min.Y+y)
				}
				used[ix] = true
			}
		}
		if ink < 100 {
			return nil, fmt.Errorf("room row %d missing ink", i)
		}
		images[i] = im
	}
	// Clear all declared source rectangles once; never erase an earlier label.
	cleared := make([]bool, meta.Width*meta.Height)
	for _, r := range a.Rows {
		rect := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				idx := y*meta.Width + x
				if !cleared[idx] {
					cleared[idx] = true
					copy(rgba[idx*4:idx*4+4], []byte{0, 0, 0, 0})
				}
			}
		}
	}
	for i, r := range a.Rows {
		rect := image.Rect(r.Rect[0], r.Rect[1], r.Rect[2], r.Rect[3])
		im := images[i]
		for y := 0; y < rect.Dy(); y++ {
			for x := 0; x < rect.Dx(); x++ {
				s := y*im.Stride + x*4
				if im.Pix[s+3] != 0 {
					d := ((rect.Min.Y+y)*meta.Width + rect.Min.X + x) * 4
					copy(rgba[d:d+4], im.Pix[s:s+4])
				}
			}
		}
	}
	return rgba, nil
}
