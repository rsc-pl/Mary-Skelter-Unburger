package main

// legacy research-only: exact-source 15-plate native Go compositor.  The
// legacy owner-provisionally-accepted background algorithm remains unchanged.
// This file deliberately does not encode TIDs, rewrite GDATs or patch games.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
	"math"
	"os"
	"path/filepath"
)

type battlePreset struct {
	Face            string   `json:"font_face"`
	Nominal         int      `json:"nominal_size"`
	Minimum         int      `json:"min_size"`
	PaddingX        int      `json:"padding_x"`
	PaddingY        int      `json:"padding_y"`
	HScale          float64  `json:"horizontal_scale"`
	HMin            float64  `json:"min_horizontal_scale"`
	VScale          float64  `json:"vertical_scale"`
	Shear           float64  `json:"shear_x"`
	Rotation        float64  `json:"rotation_degrees"`
	FillTop         [4]uint8 `json:"fill_gradient_top_rgba"`
	FillBottom      [4]uint8 `json:"fill_gradient_bottom_rgba"`
	StrokeWidth     int      `json:"stroke_width"`
	Stroke          [4]uint8 `json:"stroke_rgba"`
	ExtrusionOffset [2]int   `json:"extrusion_offset"`
	ExtrusionTop    [4]uint8 `json:"extrusion_gradient_top_rgba"`
	ExtrusionBottom [4]uint8 `json:"extrusion_gradient_bottom_rgba"`
	ExtrusionWidth  int      `json:"extrusion_stroke_width"`
	ShadowOffset    [2]int   `json:"shadow_offset"`
	Shadow          [4]uint8 `json:"shadow_rgba"`
	ShadowWidth     int      `json:"shadow_stroke_width"`
	ShadowBlur      float64  `json:"shadow_blur_radius"`
}

type battleFont struct {
	Path string `json:"repo_path"`
	SHA  string `json:"reference_sha256"`
}
type battleLetterAsset struct {
	ID          string          `json:"id"`
	Renderer    string          `json:"renderer"`
	Typography  string          `json:"typography_preset"`
	Rows        []battleRow `json:"rows"`
	TextBox     []int           `json:"text_box"`
	FinalScale  float64         `json:"final_scale"`
	Supersample int             `json:"supersample"`
}
type battleLetterSummary struct {
	SourceSHA              string   `json:"source_sha256"`
	Rows                   int      `json:"rows"`
	FontsPinned            bool     `json:"font_sha_verified"`
	ReRendered             []string `json:"rerendered_rows"`
	PixelsChanged          []int    `json:"changed_pixels_by_row"`
	NoChangedOutside15Rows bool     `json:"no_changes_outside_15_rows"`
	AllRowsChanged         bool     `json:"all_15_rows_changed"`
	NoAlphaCrop            []bool   `json:"new_label_no_alpha_crop_by_row"`
	ZeroDonorCoordinates   int      `json:"background_positions_without_original_donor"`
	NextGate               string   `json:"status"`
}

func battleTint(r [4]uint8) color.NRGBA { return color.NRGBA{r[0], r[1], r[2], r[3]} }
func battleShift(src *image.Alpha, dx, dy, w, h int) *image.Alpha {
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < src.Rect.Dy(); y++ {
		yy := y + dy
		if yy < 0 || yy >= h {
			continue
		}
		for x := 0; x < src.Rect.Dx(); x++ {
			xx := x + dx
			if xx < 0 || xx >= w {
				continue
			}
			v := src.Pix[y*src.Stride+x]
			if v > out.Pix[yy*out.Stride+xx] {
				out.Pix[yy*out.Stride+xx] = v
			}
		}
	}
	return out
}
func battlePaint(face *graphicsgo.SFNT, text string, p battlePreset, scale float64) (*image.NRGBA, error) {
	const factor = 4
	const textW = 343
	const textH = 121
	// Reserve vertical space for 11px shadow and 3deg tilted name treatment.
	fit, e := graphicsgo.FitR32C3MaskPair(text, graphicsgo.R32C3FitSpec{Width: textW, Height: 100, Factor: factor, NominalSize: p.Nominal, MinSize: p.Minimum, StrokeWidth: p.StrokeWidth + 2, HorizontalScale: p.HScale, MinHorizontalScale: p.HMin, VerticalScale: p.VScale, ShearX: p.Shear}, func(px int) (image.Rectangle, error) { return face.MeasureLatinTextBBoxUnhinted(text, px) })
	if e != nil {
		return nil, fmt.Errorf("battle text fit %s: %w", text, e)
	}
	raw, e := face.RasterR32C3LatinMasksExperimental(text, fit, 0, 0, 2)
	if e != nil {
		return nil, e
	}
	rotated, e := graphicsgo.WarpR32C3Alpha(raw.Face, graphicsgo.R32C3Warp{HorizontalScale: fit.HorizontalScale, VerticalScale: p.VScale, ShearX: p.Shear, RotationDegrees: p.Rotation, Factor: factor})
	if e != nil {
		return nil, e
	}
	if scale <= 0 || scale > 1.2 {
		return nil, errors.New("bad battle scale")
	}
	if math.Abs(scale-1) > 1e-8 {
		rotated, e = graphicsgo.ResizeLanczosAlpha(rotated, int(math.Round(float64(rotated.Rect.Dx())*scale)), int(math.Round(float64(rotated.Rect.Dy())*scale)))
		if e != nil {
			return nil, e
		}
	}
	bb, ok, e := graphicsgo.BBoxAlpha(rotated)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, fmt.Errorf("empty battle lettering %q", text)
	}
	// A consistent relative face position for every plate and all layers.
	xAnchor := (textW*factor-bb.Dx())/2 - bb.Min.X
	yAnchor := (textH*factor-bb.Dy())/2 - bb.Min.Y - 5*factor
	w, h := textW*factor, textH*factor
	base := battleShift(rotated, xAnchor, yAnchor, w, h)
	edge, e := graphicsgo.RadialDilateAlphaFast(base, p.StrokeWidth*factor)
	if e != nil {
		return nil, e
	}
	extrusionBase, e := graphicsgo.RadialDilateAlphaFast(base, p.ExtrusionWidth*factor)
	if e != nil {
		return nil, e
	}
	shadowBase, e := graphicsgo.RadialDilateAlphaFast(base, p.ShadowWidth*factor)
	if e != nil {
		return nil, e
	}
	sh := battleShift(shadowBase, p.ShadowOffset[0]*factor, p.ShadowOffset[1]*factor, w, h)
	if p.ShadowBlur > 0 {
		sh, e = graphicsgo.GaussianBlurAlpha(sh, p.ShadowBlur*factor)
		if e != nil {
			return nil, e
		}
	}
	extr := battleShift(extrusionBase, p.ExtrusionOffset[0]*factor, p.ExtrusionOffset[1]*factor, w, h)
	hi := image.NewNRGBA(image.Rect(0, 0, w, h))
	composite := func(mask *image.Alpha, top, bottom color.NRGBA, grad bool) error {
		var layer *image.NRGBA
		var err error
		if grad {
			layer, err = graphicsgo.GradientMask(mask, top, bottom)
		} else {
			layer, err = graphicsgo.TintMask(mask, top)
		}
		if err != nil {
			return err
		}
		return graphicsgo.CompositeOver(hi, layer, image.Point{})
	}
	if e = composite(sh, battleTint(p.Shadow), color.NRGBA{}, false); e != nil {
		return nil, e
	}
	if e = composite(extr, battleTint(p.ExtrusionTop), battleTint(p.ExtrusionBottom), true); e != nil {
		return nil, e
	}
	if e = composite(edge, battleTint(p.Stroke), color.NRGBA{}, false); e != nil {
		return nil, e
	}
	if e = composite(base, battleTint(p.FillTop), battleTint(p.FillBottom), true); e != nil {
		return nil, e
	}
	low, e := graphicsgo.ResizeLanczosNRGBA(hi, textW, textH)
	if e != nil {
		return nil, e
	}
	return low, nil
}

// legacy works on unmodified decoded BC7 RGB+A bytes, never via a PNG roundtrip.
// The legacy mask and donor+interpolation algorithm is reconstructed verbatim.
func battleCleanCells(rgba []byte, rows []battleRow) ([][]byte, int, error) {
	cells := make([][]byte, 15)
	masks := make([][]bool, 15)
	for i, row := range rows {
		x0, y0 := row.Rect[0], row.Rect[1]
		cell := make([]byte, cellpx*4)
		seeds := make([]bool, cellpx)
		for y := 0; y < bh; y++ {
			copy(cell[y*bw*4:(y+1)*bw*4], rgba[((y0+y)*2048+x0)*4:((y0+y)*2048+x0+bw)*4])
		}
		for k := range seeds {
			seeds[k] = battleBright(cell, k*4)
		}
		mask := battleDilate(seeds, 15)
		for k := range mask {
			if k/bw < 24 || k/bw >= 145 {
				mask[k] = false
			}
		}
		cells[i], masks[i] = cell, mask
	}
	patch := make([]byte, cellpx*4)
	known := make([]bool, cellpx)
	holes := 0
	for k := 0; k < cellpx; k++ {
		counts := map[uint32]int{}
		bestCount := 0
		var best uint32
		for i := 0; i < 15; i++ {
			if masks[i][k] {
				continue
			}
			v := battleRGBAKey(cells[i], k*4)
			counts[v]++
			if counts[v] > bestCount {
				bestCount = counts[v]
				best = v
			}
		}
		if bestCount > 0 {
			known[k] = true
			battleRGBASet(patch, k*4, best)
		} else {
			holes++
		}
	}
	for iter := 0; iter < 96; iter++ {
		added := 0
		next := make([]byte, len(patch))
		newKnown := make([]bool, cellpx)
		for k := 0; k < cellpx; k++ {
			if known[k] {
				continue
			}
			x, y := k%bw, k/bw
			var sum [4]int
			count := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					nx, ny := x+dx, y+dy
					if nx < 0 || nx >= bw || ny < 0 || ny >= bh || !known[ny*bw+nx] {
						continue
					}
					count++
					at := (ny*bw + nx) * 4
					for c := 0; c < 4; c++ {
						sum[c] += int(patch[at+c])
					}
				}
			}
			if count > 0 {
				added++
				newKnown[k] = true
				for c := 0; c < 4; c++ {
					next[k*4+c] = byte((sum[c] + count/2) / count)
				}
			}
		}
		if added == 0 {
			break
		}
		for k, v := range newKnown {
			if v {
				known[k] = true
				copy(patch[k*4:k*4+4], next[k*4:k*4+4])
			}
		}
	}
	for _, v := range known {
		if !v {
			return nil, holes, errors.New("legacy interpolation left holes")
		}
	}
	for i := 0; i < 15; i++ {
		for k, use := range masks[i] {
			if use {
				copy(cells[i][k*4:k*4+4], patch[k*4:k*4+4])
			}
		}
	}
	return cells, holes, nil
}

func battleRender(root string, reportDir string, originalTID []byte) (battleLetterSummary, []byte, error) {
	sum := battleLetterSummary{SourceSHA: battleSHA, Rows: 15, NoChangedOutside15Rows: true, NextGate: "RESEARCH/HOLD: no BC7 encode or GDAT; consolidated owner runtime later"}
	raw := originalTID
	sha := sha256.Sum256(raw)
	if hex.EncodeToString(sha[:]) != battleSHA {
		return sum, nil, errors.New("battle original SHA drift")
	}
	orig, meta, e := mskit.DecodeTIDBC7RGBA(raw)
	if e != nil {
		return sum, nil, e
	}
	if meta.Width != 2048 || meta.Height != 4096 || meta.Format != "BC7" {
		return sum, nil, errors.New("original BC7 geometry drift")
	}
	mdata, e := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if e != nil {
		return sum, nil, e
	}
	var manifest struct {
		Assets []battleLetterAsset `json:"assets"`
	}
	if e = json.Unmarshal(mdata, &manifest); e != nil {
		return sum, nil, e
	}
	var asset battleLetterAsset
	found := 0
	for _, a := range manifest.Assets {
		if a.ID == "MS2_BATTLE_NAMES" {
			asset = a
			found++
		}
	}
	if found != 1 || asset.Renderer != "ms2_battle_nameplates_font_v3" || asset.Typography != "battle_nameplate" || len(asset.Rows) != 15 || asset.FinalScale != 0.94 || asset.Supersample != 4 || len(asset.TextBox) != 4 || asset.TextBox[0] != 0 || asset.TextBox[1] != 24 || asset.TextBox[2] != 343 || asset.TextBox[3] != 145 {
		return sum, nil, errors.New("battle manifest mapping drift")
	}
	for i, row := range asset.Rows {
		if row.Text != battleOrder[i] || len(row.Rect) != 4 || row.Rect[2]-row.Rect[0] != bw || row.Rect[3]-row.Rect[1] != bh {
			return sum, nil, errors.New("battle row identity drift")
		}
	}
	pdata, e := os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if e != nil {
		return sum, nil, e
	}
	var ui struct {
		Presets map[string]battlePreset `json:"presets"`
		Fonts   map[string]battleFont   `json:"fonts"`
	}
	if e = json.Unmarshal(pdata, &ui); e != nil {
		return sum, nil, e
	}
	p, ok := ui.Presets["battle_nameplate"]
	if !ok || p.Face != "neofara" || p.Nominal != 86 || p.Minimum != 46 || p.Rotation != 3 || p.HScale != 1.08 || p.VScale != 0.95 || p.StrokeWidth != 2 || p.ExtrusionWidth != 2 || p.ShadowWidth != 2 {
		return sum, nil, errors.New("battle font preset drift")
	}
	fontMeta, ok := ui.Fonts[p.Face]
	if !ok || fontMeta.Path != "third_party/fonts/neofara/Fx-Neofara-Semi-Bold.otf" || fontMeta.SHA != "0fa94bd516f44547d8cd1495f0503b37f4d9722b5ba751549bf46cb625707141" {
		return sum, nil, errors.New("battle pinned font mapping drift")
	}
	fontBytes, e := os.ReadFile(filepath.Join(root, fontMeta.Path))
	if e != nil {
		return sum, nil, e
	}
	actual := sha256.Sum256(fontBytes)
	if hex.EncodeToString(actual[:]) != fontMeta.SHA {
		return sum, nil, errors.New("battle font SHA mismatch")
	}
	sum.FontsPinned = true
	font, e := graphicsgo.ParseSFNT(fontBytes)
	if e != nil {
		return sum, nil, e
	}
	cells, holes, e := battleCleanCells(orig, asset.Rows)
	if e != nil {
		return sum, nil, e
	}
	sum.ZeroDonorCoordinates = holes
	rendered := make([]byte, len(orig))
	copy(rendered, orig)
	for i, row := range asset.Rows {
		label, e := battlePaint(font, row.Text, p, asset.FinalScale)
		if e != nil {
			return sum, nil, e
		}
		// Reject optical cropping: no high-alpha text should touch the slot border.
		noCrop := true
		for x := 0; x < label.Rect.Dx(); x++ {
			if label.NRGBAAt(x, 0).A > 32 || label.NRGBAAt(x, label.Rect.Dy()-1).A > 32 {
				noCrop = false
			}
		}
		for y := 0; y < label.Rect.Dy(); y++ {
			if label.NRGBAAt(0, y).A > 32 || label.NRGBAAt(label.Rect.Dx()-1, y).A > 32 {
				noCrop = false
			}
		}
		sum.NoAlphaCrop = append(sum.NoAlphaCrop, noCrop)
		cell := image.NewNRGBA(image.Rect(0, 0, bw, bh))
		copy(cell.Pix, cells[i])
		if e = graphicsgo.CompositeOver(cell, label, image.Pt(0, 24)); e != nil {
			return sum, nil, e
		}
		x0, y0 := row.Rect[0], row.Rect[1]
		changed := 0
		for y := 0; y < bh; y++ {
			for x := 0; x < bw; x++ {
				at := (y*bw + x) * 4
				dst := ((y0+y)*2048 + x0 + x) * 4
				before := battleRGBAKey(orig, dst)
				after := battleRGBAKey(cell.Pix, at)
				if before != after {
					changed++
				}
				copy(rendered[dst:dst+4], cell.Pix[at:at+4])
			}
		}
		sum.PixelsChanged = append(sum.PixelsChanged, changed)
		sum.ReRendered = append(sum.ReRendered, row.Text)
	}
	sum.AllRowsChanged = true
	for _, changed := range sum.PixelsChanged {
		if changed == 0 {
			sum.AllRowsChanged = false
		}
	}
	// Compare the complete 2048x4096 atlas outside the 15 source row rectangles.
	for y := 0; y < 4096; y++ {
		for x := 0; x < 2048; x++ {
			inside := false
			for _, row := range asset.Rows {
				r := row.Rect
				if x >= r[0] && x < r[2] && y >= r[1] && y < r[3] {
					inside = true
					break
				}
			}
			if inside {
				continue
			}
			off := (y*2048 + x) * 4
			if battleRGBAKey(orig, off) != battleRGBAKey(rendered, off) {
				sum.NoChangedOutside15Rows = false
				return sum, nil, errors.New("battle changed pixels outside source row cells")
			}
		}
	}
	if reportDir != "" {
		if e = os.MkdirAll(reportDir, 0700); e != nil {
			return sum, nil, e
		}
		// Contact sheet is for source/owner visual QA only, NEVER a TID.
		contact := image.NewNRGBA(image.Rect(0, 0, 4*bw, 4*bh))
		for i, row := range asset.Rows {
			xx, yy := i%4*bw, i/4*bh
			for y := 0; y < bh; y++ {
				for x := 0; x < bw; x++ {
					at := ((row.Rect[1]+y)*2048 + row.Rect[0] + x) * 4
					off := (yy+y)*contact.Stride + (xx+x)*4
					copy(contact.Pix[off:off+4], rendered[at:at+4])
				}
			}
		}
		f, er := os.Create(filepath.Join(reportDir, "BATTLE_RENDERED_RGBA_RESEARCH.png"))
		if er != nil {
			return sum, nil, er
		}
		er = png.Encode(f, contact)
		ce := f.Close()
		if er != nil {
			return sum, nil, er
		}
		if ce != nil {
			return sum, nil, ce
		}
		// Dark QA checker is separate from the actual transparent texture.
		bg := image.NewNRGBA(contact.Rect)
		for i := 0; i < len(bg.Pix); i += 4 {
			bg.Pix[i], bg.Pix[i+1], bg.Pix[i+2], bg.Pix[i+3] = 12, 17, 32, 255
		}
		if e = graphicsgo.CompositeOver(bg, contact, image.Point{}); e != nil {
			return sum, nil, e
		}
		f, e = os.Create(filepath.Join(reportDir, "BATTLE_RENDERED_DARK_QA.png"))
		if e != nil {
			return sum, nil, e
		}
		e = png.Encode(f, bg)
		ce = f.Close()
		if e != nil {
			return sum, nil, e
		}
		if ce != nil {
			return sum, nil, ce
		}
		payload, e := json.MarshalIndent(sum, "", "  ")
		if e != nil {
			return sum, nil, e
		}
		if e = os.WriteFile(filepath.Join(reportDir, "BATTLE_RENDERER_QA.json"), append(payload, '\n'), 0600); e != nil {
			return sum, nil, e
		}
	}
	return sum, rendered, nil
}
