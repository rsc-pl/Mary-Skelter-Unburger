package main

// legacy: Second source-driven complete-font atlas family. All source rects,
// strings, font and SHA are pinned by the canonical graphics configuration.
// Opt-in only: the product game's DATs are never written here.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
)

const legacyID = "MS2_CAMP_TOP_LEGACY_NAMES"
const legacySHA = "196169c577b99f73cf621e813566e793ee11576f0ea8a2b42b780439b9a1928a"

// All color values and source geometry remain owned by graphics manifests.
// The hard-coded bounds below are *guards* against accidental manifest drift.
func loadLegacyFamily(root string) (familyAsset, []byte, familyPreset, error) {
	var zero familyAsset
	blob, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return zero, nil, familyPreset{}, err
	}
	var manifests familyAssets
	if err = json.Unmarshal(blob, &manifests); err != nil {
		return zero, nil, familyPreset{}, err
	}
	var asset familyAsset
	n := 0
	for _, a := range manifests.Assets {
		if a.ID == legacyID {
			asset = a
			n++
		}
	}
	if n != 1 || asset.Renderer != "ms2_camp_top_legacy_names_font_v2" || asset.Typography != "camp_top_legacy_neofara" || asset.Archive != 55 || asset.Index != 1448 || asset.Inner != "camp_top.tid" || len(asset.Rows) != 2 || len(asset.Sources) != 1 || asset.Sources[0].SHA != legacySHA || asset.Sources[0].Width != 2048 || asset.Sources[0].Height != 4096 || asset.Sources[0].Format != "BC7" {
		return zero, nil, familyPreset{}, errors.New("legacy atlas source or renderer changed")
	}
	names := []string{"TSUU", "NINGYOHIME"}
	prior := []string{"OTSUU", "LITTLE MERMAID"}
	rects := [][4]int{{1835, 2352, 1882, 2466}, {1910, 2275, 1978, 2540}}
	for i, r := range asset.Rows {
		if r.Text != names[i] || r.From != prior[i] || len(r.Rect) != 4 {
			return zero, nil, familyPreset{}, fmt.Errorf("legacy name %d/layout drift", i)
		}
		for k := 0; k < 4; k++ {
			if r.Rect[k] != rects[i][k] {
				return zero, nil, familyPreset{}, fmt.Errorf("legacy rect %d drift", i)
			}
		}
	}
	blob, err = os.ReadFile(filepath.Join(root, "graphics", "ui_typography.json"))
	if err != nil {
		return zero, nil, familyPreset{}, err
	}
	var typ familyTypo
	if err = json.Unmarshal(blob, &typ); err != nil {
		return zero, nil, familyPreset{}, err
	}
	p, ok := typ.Presets[asset.Typography]
	if !ok || p.Face != "neofara" || p.Size != 42 || p.MinSize != 30 || p.PadX != 3 || p.PadY != 3 || p.StrokeWidth != 2 || p.HS <= 0 || p.HS > 1 || p.VS <= 0 || p.VS > 1 || len(p.Fill) != 4 || len(p.Stroke) != 4 || p.Fill[0] != 255 || p.Fill[1] != 255 || p.Fill[2] != 255 || p.Fill[3] != 255 || p.Stroke[0] != 8 || p.Stroke[1] != 8 || p.Stroke[2] != 8 || p.Stroke[3] != 255 {
		return zero, nil, familyPreset{}, errors.New("legacy typography changed")
	}
	f, ok := typ.Fonts[p.Face]
	if !ok || !strings.HasPrefix(f.Path, "third_party/fonts/") || filepath.IsAbs(f.Path) || strings.Contains(f.Path, "..") || len(f.SHA) != 64 {
		return zero, nil, familyPreset{}, errors.New("unsafe legacy font reference")
	}
	font, err := familyReadPinned(filepath.Join(root, f.Path), f.SHA)
	return asset, font, p, err
}

func renderLegacyMask(face *graphicsgo.SFNT, label string, p familyPreset, r image.Rectangle) (*image.Alpha, error) {
	// In a vertical strip, source order is left-to-right before a 90deg turn.
	maxW, maxH := r.Dy()-2*(p.PadY+p.StrokeWidth), r.Dx()-2*(p.PadX+p.StrokeWidth)
	if maxW < 8 || maxH < 8 {
		return nil, errors.New("legacy target too narrow")
	}
	const baseW, baseH = 512, 120
	for size := p.Size; size >= p.MinSize; size-- {
		raw, e := face.RasterLatinRunSupersampled(label, size, 2, 4, baseW, baseH, 36, 83)
		if e != nil {
			return nil, e
		}
		bbox, has, e := graphicsgo.BBoxAlpha(raw)
		if e != nil {
			return nil, e
		}
		if !has {
			return nil, fmt.Errorf("empty glyph: %q", label)
		}
		if bbox.Min.X < 2 || bbox.Min.Y < 2 || bbox.Max.X > baseW-2 || bbox.Max.Y > baseH-2 {
			return nil, fmt.Errorf("input glyph cropped: %q", label)
		}
		w, h := int(math.Round(float64(bbox.Dx())*p.HS)), int(math.Round(float64(bbox.Dy())*p.VS))
		if w > maxW || h > maxH {
			continue
		}
		small := image.NewAlpha(image.Rect(0, 0, bbox.Dx(), bbox.Dy()))
		for y := 0; y < bbox.Dy(); y++ {
			copy(small.Pix[y*small.Stride:y*small.Stride+bbox.Dx()], raw.Pix[(bbox.Min.Y+y)*raw.Stride+bbox.Min.X:(bbox.Min.Y+y)*raw.Stride+bbox.Max.X])
		}
		scaled, e := graphicsgo.ResizeLanczosAlpha(small, w, h)
		if e != nil {
			return nil, e
		}
		lane := image.NewAlpha(image.Rect(0, 0, r.Dy(), r.Dx()))
		x0, y0 := (lane.Rect.Dx()-w)/2, (lane.Rect.Dy()-h)/2
		for y := 0; y < h; y++ {
			copy(lane.Pix[(y0+y)*lane.Stride+x0:(y0+y)*lane.Stride+x0+w], scaled.Pix[y*scaled.Stride:y*scaled.Stride+w])
		}
		return lane, nil
	}
	return nil, fmt.Errorf("text %q cannot fit legacy box %v", label, r)
}

func renderLegacyTarget(tid, font []byte, asset familyAsset, p familyPreset) ([]byte, error) {
	meta, e := mskit.ParseTIDMeta(tid)
	if e != nil {
		return nil, e
	}
	if meta.Width != 2048 || meta.Height != 4096 || meta.Format != "BC7" || digest(tid) != legacySHA {
		return nil, errors.New("stale/unsupported legacy TID")
	}
	out, e := mskit.DecodeBC7RGBA(tid[128:], meta.Width, meta.Height)
	if e != nil {
		return nil, e
	}
	sfnt, e := graphicsgo.ParseSFNT(font)
	if e != nil {
		return nil, e
	}
	for _, row := range asset.Rows {
		r := image.Rect(row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3])
		if r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > meta.Width || r.Max.Y > meta.Height || r.Dx() <= 0 || r.Dy() <= 0 {
			return nil, errors.New("unsafe text area")
		}
		lane, e := renderLegacyMask(sfnt, row.Text, p, r)
		if e != nil {
			return nil, e
		}
		outer, e := graphicsgo.MaxFilterAlpha(lane, 5)
		if e != nil {
			return nil, e
		}
		count := 0
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				idx := ((y * meta.Width) + x) * 4
				// Clear only the explicitly source-authored text rectangle.
				out[idx], out[idx+1], out[idx+2], out[idx+3] = 255, 255, 255, 0
				// 90deg CCW, preserving the source's vertical orientation.
				sx := r.Max.Y - 1 - y
				sy := x - r.Min.X
				fill := lane.AlphaAt(sx, sy).A
				stroke := outer.AlphaAt(sx, sy).A
				if fill == 0 && stroke == 0 {
					continue
				}
				if x <= r.Min.X+p.PadX-1 || x >= r.Max.X-p.PadX || y <= r.Min.Y+p.PadY-1 || y >= r.Max.Y-p.PadY {
					return nil, fmt.Errorf("legacy ink clipped: %s (%d,%d)", row.Text, x, y)
				}
				// Over-composite source white glyph over dark outline into straight RGB.
				c := graphicsgo.OverPixels(color.NRGBA{R: 255, G: 255, B: 255, A: 0}, color.NRGBA{R: uint8(p.Stroke[0]), G: uint8(p.Stroke[1]), B: uint8(p.Stroke[2]), A: stroke})
				c = graphicsgo.OverPixels(c, color.NRGBA{R: uint8(p.Fill[0]), G: uint8(p.Fill[1]), B: uint8(p.Fill[2]), A: fill})
				out[idx], out[idx+1], out[idx+2], out[idx+3] = c.R, c.G, c.B, c.A
				count++
			}
		}
		if count < 80 {
			return nil, fmt.Errorf("nearly blank label %q", row.Text)
		}
	}
	return out, nil
}

func executeLegacyFamilyGDAT(manifestPath string) (familyResponse, error) {
	var resp familyResponse
	raw, e := os.ReadFile(manifestPath)
	if e != nil {
		return resp, e
	}
	var spec familyJob
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&spec); e != nil {
		return resp, e
	}
	if spec.Version != 1 || spec.ProjectRoot == "" || spec.Source == "" || spec.Output == "" || spec.Quality != 6 || spec.MaxAlpha < 0 || spec.MaxAlpha > 255 || spec.MaxVisible < 0 || spec.MaxVisible > 255 {
		return resp, errors.New("requires pinned family job with Q6 and valid thresholds")
	}
	base, e := filepath.Abs(filepath.Dir(manifestPath))
	if e != nil {
		return resp, e
	}
	root, e := checkedPath(base, spec.ProjectRoot)
	if e != nil {
		return resp, e
	}
	src, e := checkedPath(base, spec.Source)
	if e != nil {
		return resp, e
	}
	dst, e := checkedPath(base, spec.Output)
	if e != nil {
		return resp, e
	}
	if src == dst {
		return resp, errors.New("in-place output forbidden")
	}
	asset, font, p, e := loadLegacyFamily(root)
	if e != nil {
		return resp, e
	}
	gd, e := os.ReadFile(src)
	if e != nil {
		return resp, e
	}
	if !checkedHash(gd, spec.SourceSHA256) {
		return resp, errors.New("original full GDAT SHA mismatch")
	}
	tid, _, e := mskit.ExtractGraphicsTID(gd, asset.Index, asset.Inner)
	if e != nil {
		return resp, e
	}
	if digest(tid) != legacySHA {
		return resp, errors.New("original BC7 TID SHA mismatch")
	}
	rgba, e := renderLegacyTarget(tid, font, asset, p)
	if e != nil {
		return resp, e
	}
	temp, e := os.MkdirTemp("", "ms-legacy-q6-*")
	if e != nil {
		return resp, e
	}
	defer os.RemoveAll(temp)
	pic := &image.NRGBA{Pix: rgba, Stride: 2048 * 4, Rect: image.Rect(0, 0, 2048, 4096)}
	var pngbuf bytes.Buffer
	if e = png.Encode(&pngbuf, pic); e != nil {
		return resp, e
	}
	pngpath := filepath.Join(temp, "target.png")
	if e = os.WriteFile(pngpath, pngbuf.Bytes(), 0600); e != nil {
		return resp, e
	}
	job := GDATPNGManifest{Version: 1, Jobs: []GDATPNGJob{{Source: src, SourceSHA256: spec.SourceSHA256, Output: dst, Edits: []GDATPNGEdit{{GDATIndex: asset.Index, Inner: asset.Inner, SourceTIDSHA256: legacySHA, TargetPNG: pngpath, TargetPNGSHA256: digest(pngbuf.Bytes()), Quality: 6, MaxAlpha: &spec.MaxAlpha, MaxVisible: &spec.MaxVisible}}}}}
	jobdata, e := json.Marshal(job)
	if e != nil {
		return resp, e
	}
	jobs := filepath.Join(temp, "jobs.json")
	if e = os.WriteFile(jobs, jobdata, 0600); e != nil {
		return resp, e
	}
	result, e := RunGDATPNGBatch(jobs)
	if e != nil {
		return resp, e
	}
	return familyResponse{Status: "PASS_LEGACY_2_NAMES_SOURCE_FONT_REAL_BC7_Q6_GDAT_OFFGAME", Family: legacyID, FontSHA256: digest(font), Rows: 2, GDAT: result}, nil
}
