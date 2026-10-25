package main

// Source-locked pair of MS2 Gebryna blue title atlases.  One layered Go renderer
// serves the heroine-room list and My Room title; neither uses Python/Pillow.
// These candidates are strictly opt-in until full pristine PC DAT/game QA.
import (
	"crypto/sha256"
	"encoding/hex"
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

const (
	roomListLayoutSHA = "52636b2b0b6012b8102fe23dfb161c2dfd3cbc29eebb4d1313baf15d80721328"
	myRoomLayoutSHA   = "ccb8d8cace66d2dba5e05f90fe16c34c95c672ae747ce2924c0ee67e47256543"
	roomListID        = "MS2_HEROINE_ROOM_LIST_BLUE"
	myRoomID          = "MS2_MYROOM_TITLE"
	roomListSHA       = "11f61fb1794a7a6b75193b7cf4b7a23ba2519b9aa5ec4a0ba68da89cacbea5b5"
	myRoomSHA         = "9a575cb0fcac7538388b73ecc85bf777f5f69da9f928e3791dabddaaa5f5f976"
)

type roomPreset struct {
	Face          string  `json:"font_face"`
	NameSize      int     `json:"name_nominal_size"`
	RoomSize      int     `json:"room_nominal_size"`
	MinSize       int     `json:"min_size"`
	Horizontal    float64 `json:"horizontal_scale"`
	MinHorizontal float64 `json:"min_horizontal_scale"`
	Outer         int     `json:"outer_stroke_width"`
	Inner         int     `json:"inner_stroke_width"`
	OuterRGBA     []uint8 `json:"outer_stroke_rgba"`
	InnerRGBA     []uint8 `json:"inner_stroke_rgba"`
	Dark          []uint8 `json:"face_dark_rgba"`
	Mid           []uint8 `json:"face_mid_rgba"`
	Blue          []uint8 `json:"face_blue_rgba"`
	ShadowOffset  []int   `json:"shadow_offset"`
	ShadowRGBA    []uint8 `json:"shadow_rgba"`
	Line1Y        int     `json:"line1_y"`
	RoomY         int     `json:"room_y"`
	RoomRight     int     `json:"room_right_margin"`
	Bleed         []uint8 `json:"transparent_bleed_rgb"`
}

type roomAsset struct {
	familyAsset
	ClearRect          []int   `json:"clear_rect"`
	Name               string  `json:"name"`
	NameTargetWidth    int     `json:"name_target_width"`
	NameOffsetY        int     `json:"name_offset_y"`
	RoomVerticalScale  float64 `json:"room_vertical_scale"`
	RoomVerticalAnchor string  `json:"room_vertical_anchor"`
	TextLeftMargin     int     `json:"text_left_margin"`
	ListRows           []struct {
		Name            string `json:"name"`
		Rect            []int  `json:"rect"`
		NameTargetWidth int    `json:"name_target_width"`
	} `json:"rows"`
}

func loadRoom(root, family string) (roomAsset, []byte, roomPreset, error) {
	var zero roomAsset
	var assets struct {
		Assets []json.RawMessage `json:"assets"`
	}
	b, err := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if err != nil {
		return zero, nil, roomPreset{}, err
	}
	if err = json.Unmarshal(b, &assets); err != nil {
		return zero, nil, roomPreset{}, err
	}
	var a roomAsset
	n := 0
	for _, raw := range assets.Assets {
		var entry struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal(raw, &entry); err != nil {
			return zero, nil, roomPreset{}, err
		}
		if entry.ID == family {
			n++
			if err = json.Unmarshal(raw, &a); err != nil {
				return zero, nil, roomPreset{}, err
			}
		}
	}
	if n != 1 || a.Archive != 55 || a.Typography != "ms2_room_entry_gebryna" || len(a.Sources) != 1 {
		return zero, nil, roomPreset{}, errors.New("room manifest changed")
	}
	s := a.Sources[0]
	switch family {
	case roomListID:
		if a.Renderer != "ms2_heroine_room_blue_font_v6" || a.Index != 1853 || a.Inner != "menu_heroineroom.tid" || s.SHA != roomListSHA || s.Format != "DXT5" || s.Width != 2048 || s.Height != 2048 || len(a.ListRows) != 11 || a.TextLeftMargin != 78 || a.RoomVerticalScale != .82 || a.RoomVerticalAnchor != "bottom" {
			return zero, nil, roomPreset{}, errors.New("room list source/style changed")
		}
		for i, r := range a.ListRows {
			if r.Name == "" || r.NameTargetWidth < 200 || r.NameTargetWidth > 600 || len(r.Rect) != 4 || r.Rect[2]-r.Rect[0] != 675 || r.Rect[3]-r.Rect[1] != 127 || r.Rect[0] < 5 || r.Rect[1] < 230 || r.Rect[2] > 1357 || r.Rect[3] > 1002 {
				return zero, nil, roomPreset{}, fmt.Errorf("room list geometry %d changed", i)
			}
		}
	case myRoomID:
		if a.Renderer != "ms2_myroom_title_font_v3" || a.Index != 1860 || a.Inner != "menu_kyoju_myroom.tid" || s.SHA != myRoomSHA || s.Format != "DXT5" || s.Width != 2048 || s.Height != 1024 || a.Name != "TSUU" || len(a.ClearRect) != 4 || a.ClearRect[0] != 930 || a.ClearRect[1] != 650 || a.ClearRect[2] != 1440 || a.ClearRect[3] != 825 || a.NameTargetWidth != 342 || a.NameOffsetY != 22 || a.RoomVerticalScale != .92 || a.RoomVerticalAnchor != "top" {
			return zero, nil, roomPreset{}, errors.New("My Room source/layout changed")
		}
	default:
		return zero, nil, roomPreset{}, errors.New("unsupported Gebryna family")
	}
	layoutHash := roomListLayoutSHA
	if family == myRoomID {
		layoutHash = myRoomLayoutSHA
	}
	if roomLayoutHash(a) != layoutHash {
		return zero, nil, roomPreset{}, errors.New("room full row geometry SHA changed")
	}
	b, err = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if err != nil {
		return zero, nil, roomPreset{}, err
	}
	var ty struct {
		Fonts   map[string]familyFont     `json:"fonts"`
		Presets map[string]roomPreset `json:"presets"`
	}
	if err = json.Unmarshal(b, &ty); err != nil {
		return zero, nil, roomPreset{}, err
	}
	p, ok := ty.Presets[a.Typography]
	if !ok || p.Face != "gebryna" || p.NameSize != 73 || p.RoomSize != 62 || p.MinSize != 34 || p.Horizontal != 1.04 || p.MinHorizontal != .68 || p.Outer != 7 || p.Inner != 4 || len(p.OuterRGBA) != 4 || len(p.InnerRGBA) != 4 || len(p.Dark) != 4 || len(p.Mid) != 4 || len(p.Blue) != 4 || len(p.ShadowRGBA) != 4 || len(p.ShadowOffset) != 2 || len(p.Bleed) != 3 || p.Line1Y != 0 || p.RoomY != 52 || p.RoomRight != 10 {
		return zero, nil, p, errors.New("Gebryna typography drift")
	}
	f, ok := ty.Fonts[p.Face]
	if !ok || !strings.HasPrefix(f.Path, "third_party/fonts/") || strings.Contains(f.Path, "..") || filepath.IsAbs(f.Path) {
		return zero, nil, p, errors.New("unsafe font path")
	}
	font, err := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	return a, font, p, err
}

func roomCropAlpha(src *image.Alpha, rect image.Rectangle) *image.Alpha {
	out := image.NewAlpha(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := 0; y < rect.Dy(); y++ {
		copy(out.Pix[y*out.Stride:y*out.Stride+rect.Dx()], src.Pix[(y+rect.Min.Y)*src.Stride+rect.Min.X:][:rect.Dx()])
	}
	return out
}
func roomBounds(src *image.Alpha) image.Rectangle {
	b := image.Rectangle{Min: image.Pt(src.Rect.Dx(), src.Rect.Dy())}
	for y := 0; y < src.Rect.Dy(); y++ {
		for x := 0; x < src.Rect.Dx(); x++ {
			if src.Pix[y*src.Stride+x] != 0 {
				if x < b.Min.X {
					b.Min.X = x
				}
				if y < b.Min.Y {
					b.Min.Y = y
				}
				if x+1 > b.Max.X {
					b.Max.X = x + 1
				}
				if y+1 > b.Max.Y {
					b.Max.Y = y + 1
				}
			}
		}
	}
	return b
}
func roomCol(c []uint8, a uint8) color.NRGBA {
	return color.NRGBA{c[0], c[1], c[2], uint8((uint32(c[3])*uint32(a) + 127) / 255)}
}
func roomLayer(dst *image.NRGBA, src *image.Alpha, ox, oy int, col func(x, y int, v uint8) color.NRGBA) {
	for y := 0; y < src.Rect.Dy(); y++ {
		dy := y + oy
		if dy < 0 || dy >= dst.Rect.Dy() {
			continue
		}
		for x := 0; x < src.Rect.Dx(); x++ {
			dx := x + ox
			if dx < 0 || dx >= dst.Rect.Dx() {
				continue
			}
			v := src.Pix[y*src.Stride+x]
			if v == 0 {
				continue
			}
			i := dy*dst.Stride + dx*4
			o := graphicsgo.OverPixels(color.NRGBA{dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3]}, col(x, y, v))
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = o.R, o.G, o.B, o.A
		}
	}
}
func roomLine(face *graphicsgo.SFNT, text string, nominal, maxWidth, maxHeight, targetWidth int, p roomPreset) (*image.NRGBA, error) {
	// Reconstruct aligned face/inner/outer masks before compositing. No per-stroke
	// origin drift, and all resampling is from the same fitted source mask.
	var crop *image.Alpha
	size := nominal
	for ; size >= p.MinSize; size-- {
		mask, err := face.RasterLatinRunSupersampled(text, size, 2, 4, 720, 140, 24, 103)
		if err != nil {
			return nil, err
		}
		bounds := roomBounds(mask)
		if bounds.Empty() || bounds.Min.X < 15 || bounds.Max.X > 700 || bounds.Min.Y < 15 || bounds.Max.Y > 130 {
			return nil, fmt.Errorf("clipped/blank text %q at size %d", text, size)
		}
		if bounds.Dy()+p.Outer*2+4 > maxHeight || float64(bounds.Dx()+p.Outer*2)*p.MinHorizontal > float64(maxWidth) {
			continue
		}
		bounds = bounds.Inset(-p.Outer - 2).Intersect(mask.Rect)
		crop = roomCropAlpha(mask, bounds)
		break
	}
	if crop == nil {
		return nil, fmt.Errorf("could not fit Gebryna text %q", text)
	}
	inner, err := graphicsgo.MaxFilterAlphaFast(crop, p.Inner*2+1)
	if err != nil {
		return nil, err
	}
	outer, err := graphicsgo.MaxFilterAlphaFast(crop, p.Outer*2+1)
	if err != nil {
		return nil, err
	}
	outW := int(math.Round(float64(crop.Rect.Dx()) * p.Horizontal))
	if outW > maxWidth {
		outW = maxWidth
	}
	if targetWidth > 0 {
		outW = targetWidth
	}
	if outW <= 0 || outW > maxWidth {
		return nil, errors.New("bad width")
	}
	outH := crop.Rect.Dy()
	if outH > maxHeight {
		return nil, errors.New("bad height")
	}
	scale := func(src *image.Alpha) (*image.Alpha, error) { return graphicsgo.ResizeLanczosAlpha(src, outW, outH) }
	f, err := scale(crop)
	if err != nil {
		return nil, err
	}
	in, err := scale(inner)
	if err != nil {
		return nil, err
	}
	o, err := scale(outer)
	if err != nil {
		return nil, err
	}
	img := image.NewNRGBA(image.Rect(0, 0, outW+6, outH+6))
	shadow, err := graphicsgo.GaussianBlurAlpha(o, 0.8)
	if err != nil {
		return nil, err
	}
	roomLayer(img, shadow, p.ShadowOffset[0], p.ShadowOffset[1], func(_, _ int, v uint8) color.NRGBA { return roomCol(p.ShadowRGBA, v) })
	roomLayer(img, o, 0, 0, func(_, _ int, v uint8) color.NRGBA { return roomCol(p.OuterRGBA, v) })
	roomLayer(img, in, 0, 0, func(_, _ int, v uint8) color.NRGBA { return roomCol(p.InnerRGBA, v) })
	// Source's dark/mid/blue diagonal gradient covers the glyph face only.
	roomLayer(img, f, 0, 0, func(x, y int, v uint8) color.NRGBA {
		u := float64(x+y) / float64(outW+outH-2)
		if u < 0 {
			u = 0
		}
		if u > 1 {
			u = 1
		}
		c0, c1, t := p.Dark, p.Mid, u*2
		if u >= .5 {
			c0, c1, t = p.Mid, p.Blue, (u-.5)*2
		}
		val := func(k int) uint8 { return uint8(math.Round(float64(c0[k])*(1-t) + float64(c1[k])*t)) }
		a := uint8((uint32(val(3))*uint32(v) + 127) / 255)
		return color.NRGBA{val(0), val(1), val(2), a}
	})
	return img, nil
}
func roomResizeHeight(im *image.NRGBA, scale float64) (*image.NRGBA, error) {
	if scale == 1 {
		return im, nil
	}
	return graphicsgo.ResizeLanczosNRGBA(im, im.Rect.Dx(), int(math.Round(float64(im.Rect.Dy())*scale)))
}
func roomLabel(face *graphicsgo.SFNT, name string, width, height, targetWidth, nameOffset int, verticalScale float64, anchor string, p roomPreset) (*image.NRGBA, error) {
	if targetWidth > width-8 {
		targetWidth = width - 8
	}
	n, err := roomLine(face, name+"'S", p.NameSize, width-8, 62, targetWidth, p)
	if err != nil {
		return nil, err
	}
	rw := int(math.Round(float64(width) * .48))
	if rw < 170 {
		rw = 170
	}
	r, err := roomLine(face, "ROOM", p.RoomSize, rw, 56, 0, p)
	if err != nil {
		return nil, err
	}
	old := r.Rect.Dy()
	r, err = roomResizeHeight(r, verticalScale)
	if err != nil {
		return nil, err
	}
	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	if n.Rect.Dy()+nameOffset > height || p.RoomY+old > height+4 {
		return nil, errors.New("layer exceeds label area")
	}
	if err = graphicsgo.CompositeOver(out, n, image.Pt(4, p.Line1Y+nameOffset)); err != nil {
		return nil, err
	}
	y := p.RoomY
	switch anchor {
	case "top":
	case "bottom":
		y += old - r.Rect.Dy()
	case "center":
		y += (old - r.Rect.Dy()) / 2
	default:
		return nil, errors.New("invalid anchor")
	}
	roomX := width - p.RoomRight - r.Rect.Dx()
	// Both name and ROOM must stay independent layers, with no accidental glyph collision.
	for ry := 0; ry < r.Rect.Dy(); ry++ {
		for rx := 0; rx < r.Rect.Dx(); rx++ {
			x0, y0 := roomX+rx, y+ry
			if x0 >= 0 && x0 < width && y0 >= 0 && y0 < height && r.NRGBAAt(rx, ry).A > 0 && out.NRGBAAt(x0, y0).A > 0 {
				return nil, fmt.Errorf("two-line Gebryna text collides at %d,%d", x0, y0)
			}
		}
	}
	if err = graphicsgo.CompositeOver(out, r, image.Pt(roomX, y)); err != nil {
		return nil, err
	}
	return out, nil
}
func renderRoom(tid, font []byte, a roomAsset, p roomPreset) ([]byte, error) {
	expected := roomListSHA
	if a.ID == myRoomID {
		expected = myRoomSHA
	}
	if digest(tid) != expected {
		return nil, errors.New("pristine room TID hash mismatch")
	}
	rgba, meta, err := mskit.DecodeTIDDXT5RGBA(tid)
	if err != nil {
		return nil, err
	}
	face, err := graphicsgo.ParseSFNT(font)
	if err != nil {
		return nil, err
	}
	type patch struct {
		rc  image.Rectangle
		img *image.NRGBA
	}
	var edits []patch
	if a.ID == myRoomID {
		rc := image.Rect(a.ClearRect[0], a.ClearRect[1], a.ClearRect[2], a.ClearRect[3])
		im, e := roomLabel(face, a.Name, rc.Dx(), rc.Dy(), a.NameTargetWidth, a.NameOffsetY, a.RoomVerticalScale, a.RoomVerticalAnchor, p)
		if e != nil {
			return nil, e
		}
		edits = append(edits, patch{rc, im})
	} else {
		for _, r := range a.ListRows {
			rc := image.Rect(r.Rect[0]+a.TextLeftMargin, r.Rect[1], r.Rect[2], r.Rect[3])
			im, e := roomLabel(face, r.Name, rc.Dx(), rc.Dy(), r.NameTargetWidth, 0, a.RoomVerticalScale, a.RoomVerticalAnchor, p)
			if e != nil {
				return nil, fmt.Errorf("row %s: %w", r.Name, e)
			}
			edits = append(edits, patch{rc, im})
		}
	}
	used := make([]bool, meta.Width*meta.Height)
	for _, ed := range edits {
		if ed.rc.Min.X < 0 || ed.rc.Min.Y < 0 || ed.rc.Max.X > meta.Width || ed.rc.Max.Y > meta.Height {
			return nil, errors.New("room region outside TID")
		}
		for y := 0; y < ed.rc.Dy(); y++ {
			for x := 0; x < ed.rc.Dx(); x++ {
				if ed.img.NRGBAAt(x, y).A == 0 {
					continue
				}
				if x < 1 || y < 1 || x >= ed.rc.Dx()-1 || y >= ed.rc.Dy()-1 {
					return nil, errors.New("room glyph clipped")
				}
				at := (ed.rc.Min.Y+y)*meta.Width + ed.rc.Min.X + x
				if used[at] {
					return nil, errors.New("room ink overlaps")
				}
				used[at] = true
			}
		}
	}
	for _, ed := range edits {
		for y := ed.rc.Min.Y; y < ed.rc.Max.Y; y++ {
			for x := ed.rc.Min.X; x < ed.rc.Max.X; x++ {
				i := (y*meta.Width + x) * 4
				rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = p.Bleed[0], p.Bleed[1], p.Bleed[2], 0
			}
		}
		for y := 0; y < ed.rc.Dy(); y++ {
			for x := 0; x < ed.rc.Dx(); x++ {
				c := ed.img.NRGBAAt(x, y)
				if c.A == 0 {
					continue
				}
				i := ((y+ed.rc.Min.Y)*meta.Width + x + ed.rc.Min.X) * 4
				rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = c.R, c.G, c.B, c.A
			}
		}
	}
	return rgba, nil
}

func roomLayoutHash(a roomAsset) string {
	b, _ := json.Marshal(struct {
		List  interface{} `json:"list"`
		Clear []int       `json:"clear"`
		Name  string      `json:"name"`
		Scale float64     `json:"scale"`
	}{a.ListRows, a.ClearRect, a.Name, a.RoomVerticalScale})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
