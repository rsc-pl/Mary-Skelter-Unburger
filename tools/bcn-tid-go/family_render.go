package main

// legacy: Complete source-authored MS2 Camp Top 2 family render, using private
// pinned SFNT sources, straight RGBA, bcn Q6 and the legacy safe GDAT writer.
// This is opt-in and never edits an installed game or any source file.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const campTop2TIDSHA = "6cac6e1c439db75ac43084e3452fb959c11984030b178f7b6a23c2ba445d8f39"
const campTop2ID = "MS2_CAMP_TOP2_CHARACTER_NAMES"

// The contract references only existing canonical assets. The executor checks
// their layouts, names, sizes, fonts and SHAs instead of embedding a parallel
// copy of all text/typography configuration in production code.
type familyAsset struct {
	ID         string `json:"id"`
	Renderer   string `json:"renderer"`
	Typography string `json:"typography_preset"`
	Archive    int    `json:"archive"`
	Index      int    `json:"gdat_index"`
	Inner      string `json:"cl3_inner_name"`
	Band       []int  `json:"clear_band"`
	Rows       []struct {
		Text string `json:"text"`
		From string `json:"from"`
		Rect []int  `json:"rect"`
	} `json:"rows"`
	Sources []struct {
		SHA    string `json:"tid_sha256"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Format string `json:"format"`
	} `json:"source_variants"`
}
type familyAssets struct {
	Assets []familyAsset `json:"assets"`
}
type familyFont struct {
	Path string `json:"repo_path"`
	SHA  string `json:"reference_sha256"`
}
type familyPreset struct {
	Face        string  `json:"font_face"`
	Size        int     `json:"nominal_size"`
	MinSize     int     `json:"min_size"`
	PadX        int     `json:"padding_x"`
	PadY        int     `json:"padding_y"`
	HS          float64 `json:"horizontal_scale"`
	VS          float64 `json:"vertical_scale"`
	Fill        []int   `json:"fill_rgba"`
	StrokeWidth int     `json:"stroke_width"`
	Stroke      []int   `json:"stroke_rgba"`
}
type familyTypo struct {
	Fonts   map[string]familyFont   `json:"fonts"`
	Presets map[string]familyPreset `json:"presets"`
}
type familyJob struct {
	Version      int    `json:"version"`
	ProjectRoot  string `json:"project_root"`
	Source       string `json:"source"`
	SourceSHA256 string `json:"source_sha256"`
	Output       string `json:"output"`
	Quality      int    `json:"quality"`
	MaxAlpha     int    `json:"max_alpha"`
	MaxVisible   int    `json:"max_visible"`
}
type familyResponse struct {
	Status     string          `json:"status"`
	Family     string          `json:"family"`
	FontSHA256 string          `json:"font_sha256"`
	Rows       int             `json:"rows"`
	GDAT       GDATPNGResponse `json:"gdat"`
}

func familyReadPinned(p, hash string) ([]byte, error) {
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	if digest(b) != hash {
		return nil, fmt.Errorf("pinned SHA mismatch for %s", p)
	}
	return b, nil
}
func loadCampTop2(root string) (familyAsset, []byte, familyPreset, error) {
	var asset familyAsset
	var m familyAssets
	var t familyTypo
	data, e := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if e != nil {
		return asset, nil, familyPreset{}, e
	}
	if e = json.Unmarshal(data, &m); e != nil {
		return asset, nil, familyPreset{}, e
	}
	found := 0
	for _, a := range m.Assets {
		if a.ID == campTop2ID {
			asset = a
			found++
		}
	}
	if found != 1 || asset.Renderer != "ms2_camp_top2_character_names_font_v3" || asset.Typography != "camp_top2_vertical_neofara" || asset.Archive != 55 || asset.Index != 1463 || asset.Inner != "camp_top_2.tid" || len(asset.Rows) != 14 || len(asset.Sources) != 1 || len(asset.Band) != 4 || asset.Sources[0].SHA != campTop2TIDSHA || asset.Sources[0].Format != "DXT5" || asset.Sources[0].Width != 2048 || asset.Sources[0].Height != 2048 || asset.Band[0] != 1420 || asset.Band[1] != 816 || asset.Band[2] != 2036 || asset.Band[3] != 1112 {
		return asset, nil, familyPreset{}, errors.New("canonical camp_top2 source/layout mismatch")
	}
	data, e = os.ReadFile(filepath.Join(root, "graphics", "ui_typography.json"))
	if e != nil {
		return asset, nil, familyPreset{}, e
	}
	if e = json.Unmarshal(data, &t); e != nil {
		return asset, nil, familyPreset{}, e
	}
	p, ok := t.Presets[asset.Typography]
	if !ok || p.Face != "neofara" || p.Size < 24 || p.MinSize < 8 || p.MinSize > p.Size || p.HS <= 0 || p.VS <= 0 || len(p.Fill) != 4 || p.Fill[0] != 255 || p.Fill[1] != 255 || p.Fill[2] != 255 || p.Fill[3] != 255 {
		return asset, nil, p, errors.New("invalid/changed camp_top2 typography")
	}
	cfg, ok := t.Fonts[p.Face]
	if !ok || !strings.HasPrefix(cfg.Path, "third_party/fonts/") || filepath.IsAbs(cfg.Path) || strings.Contains(cfg.Path, "..") || len(cfg.SHA) != 64 {
		return asset, nil, p, errors.New("unsafe font configuration")
	}
	font, e := familyReadPinned(filepath.Join(root, cfg.Path), cfg.SHA)
	return asset, font, p, e
}

// Raster for a complete 296x44 lane. All 14 lane alpha masks are checked for
// clipping before any GDAT output is staged or published.
func rasterCampLane(sfnt *graphicsgo.SFNT, text string, p familyPreset) (*image.Alpha, error) {
	const tw, th = 296, 44
	const w, h = 512, 120
	for size := p.Size; size >= p.MinSize; size-- {
		alpha, e := sfnt.RasterLatinRunSupersampled(text, size, 2, 4, w, h, 36, 83)
		if e != nil {
			return nil, e
		}
		bounds := image.Rectangle{Min: image.Pt(w, h), Max: image.Pt(0, 0)}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if alpha.AlphaAt(x, y).A != 0 {
					if x < bounds.Min.X {
						bounds.Min.X = x
					}
					if x+1 > bounds.Max.X {
						bounds.Max.X = x + 1
					}
					if y < bounds.Min.Y {
						bounds.Min.Y = y
					}
					if y+1 > bounds.Max.Y {
						bounds.Max.Y = y + 1
					}
				}
			}
		}
		if bounds.Empty() || bounds.Min.X < 3 || bounds.Max.X >= w-3 || bounds.Min.Y < 3 || bounds.Max.Y >= h-3 {
			return nil, fmt.Errorf("glyph clipped/blank: %q bounds=%v size=%d", text, bounds, size)
		}
		sw := int(math.Round(float64(bounds.Dx()) * p.HS))
		sh := int(math.Round(float64(bounds.Dy()) * p.VS))
		if sw > tw-2*p.PadX || sh > th-2*p.PadY {
			continue
		}
		if sw < 1 || sh < 1 {
			return nil, errors.New("glyph size invalid")
		}
		crop := image.NewAlpha(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
		for y := 0; y < bounds.Dy(); y++ {
			copy(crop.Pix[y*crop.Stride:y*crop.Stride+bounds.Dx()], alpha.Pix[(bounds.Min.Y+y)*alpha.Stride+bounds.Min.X:(bounds.Min.Y+y)*alpha.Stride+bounds.Max.X])
		}
		scaled, e := graphicsgo.ResizeLanczosAlpha(crop, sw, sh)
		if e != nil {
			return nil, e
		}
		result := image.NewAlpha(image.Rect(0, 0, tw, th))
		ox, oy := (tw-sw)/2, (th-sh)/2
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				result.SetAlpha(ox+x, oy+y, scaled.AlphaAt(x, y))
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("cannot fit source text: %q", text)
}
func renderCampTarget(tid, font []byte, asset familyAsset, p familyPreset) ([]byte, error) {
	meta, e := mskit.ParseTIDMeta(tid)
	if e != nil {
		return nil, e
	}
	if meta.Width != 2048 || meta.Height != 2048 || meta.Format != "DXT5" {
		return nil, errors.New("unsupported source TID")
	}
	decoded, e := mskit.DecodeDXT5RGBA(tid[128:], meta.Width, meta.Height)
	if e != nil {
		return nil, e
	}
	sfnt, e := graphicsgo.ParseSFNT(font)
	if e != nil {
		return nil, e
	}
	region := image.Rect(asset.Band[0], asset.Band[1], asset.Band[2], asset.Band[3])
	if region.Dx() != 616 || region.Dy() != 296 {
		return nil, errors.New("unsafe band")
	}
	// Clear the complete source-proven 616x296 band; white RGB bleed under
	// transparent pixels prevents dark DXT5 letter-edge halos.
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			i := (y*meta.Width + x) * 4
			decoded[i], decoded[i+1], decoded[i+2], decoded[i+3] = 255, 255, 255, 0
		}
	}
	for n, row := range asset.Rows {
		lane, e := rasterCampLane(sfnt, row.Text, p)
		if e != nil {
			return nil, e
		}
		nonzero := 0
		for y := 0; y < 44; y++ {
			for x := 0; x < 296; x++ {
				a := lane.AlphaAt(x, y).A
				if a == 0 {
					continue
				}
				nonzero++
				// Pillow ROTATE_90: horizontal x maps to inverted vertical y.
				targetX := region.Min.X + n*44 + y
				targetY := region.Min.Y + 295 - x
				if targetX < region.Min.X+2 || targetX >= region.Max.X-2 || targetY < region.Min.Y+2 || targetY >= region.Max.Y-2 {
					return nil, fmt.Errorf("clipped lane %d", n)
				}
				i := (targetY*meta.Width + targetX) * 4
				decoded[i], decoded[i+1], decoded[i+2], decoded[i+3] = 255, 255, 255, a
			}
		}
		if nonzero == 0 {
			return nil, fmt.Errorf("blank lane %d", n)
		}
	}
	return decoded, nil
}
func executeCampFamilyGDAT(manifestPath string) (familyResponse, error) {
	var out familyResponse
	data, e := os.ReadFile(manifestPath)
	if e != nil {
		return out, e
	}
	var spec familyJob
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e = d.Decode(&spec); e != nil {
		return out, e
	}
	if spec.Version != 1 || spec.ProjectRoot == "" || spec.Source == "" || spec.Output == "" || spec.Quality != 6 || spec.MaxAlpha < 0 || spec.MaxAlpha > 255 || spec.MaxVisible < 0 || spec.MaxVisible > 255 {
		return out, errors.New("requires version 1, pinned paths, Q6 and bounded quality thresholds")
	}
	dir, e := filepath.Abs(filepath.Dir(manifestPath))
	if e != nil {
		return out, e
	}
	root, e := checkedPath(dir, spec.ProjectRoot)
	if e != nil {
		return out, e
	}
	src, e := checkedPath(dir, spec.Source)
	if e != nil {
		return out, e
	}
	dst, e := checkedPath(dir, spec.Output)
	if e != nil {
		return out, e
	}
	if src == dst {
		return out, errors.New("in-place output forbidden")
	}
	asset, font, p, e := loadCampTop2(root)
	if e != nil {
		return out, e
	}
	old, e := os.ReadFile(src)
	if e != nil {
		return out, e
	}
	if !checkedHash(old, spec.SourceSHA256) {
		return out, errors.New("archive SHA mismatch")
	}
	tid, _, e := mskit.ExtractGraphicsTID(old, asset.Index, asset.Inner)
	if e != nil {
		return out, e
	}
	if digest(tid) != campTop2TIDSHA {
		return out, errors.New("source TID SHA mismatch")
	}
	target, e := renderCampTarget(tid, font, asset, p)
	if e != nil {
		return out, e
	}
	// Internal temp PNG is a format-neutral bridge into the shared multi-GDAT
	// writer. This is not a second persistent asset container or a game output.
	tmp, e := os.MkdirTemp("", "ms-camp-top2-q6-*")
	if e != nil {
		return out, e
	}
	defer os.RemoveAll(tmp)
	pngfile := filepath.Join(tmp, "rendered.png")
	pic := &image.NRGBA{Pix: target, Stride: 2048 * 4, Rect: image.Rect(0, 0, 2048, 2048)}
	var buf bytes.Buffer
	if e = png.Encode(&buf, pic); e != nil {
		return out, e
	}
	if e = os.WriteFile(pngfile, buf.Bytes(), 0600); e != nil {
		return out, e
	}
	batch := GDATPNGManifest{Version: 1, Jobs: []GDATPNGJob{{Source: src, SourceSHA256: spec.SourceSHA256, Output: dst, Edits: []GDATPNGEdit{{GDATIndex: asset.Index, Inner: asset.Inner, SourceTIDSHA256: campTop2TIDSHA, TargetPNG: pngfile, TargetPNGSHA256: digest(buf.Bytes()), Quality: 6, MaxAlpha: &spec.MaxAlpha, MaxVisible: &spec.MaxVisible}}}}}
	raw, e := json.Marshal(batch)
	if e != nil {
		return out, e
	}
	f := filepath.Join(tmp, "jobs.json")
	if e = os.WriteFile(f, raw, 0600); e != nil {
		return out, e
	}
	result, e := RunGDATPNGBatch(f)
	if e != nil {
		return out, e
	}
	out = familyResponse{Status: "PASS_RENDER14_SOURCE_FONT_BCN_Q6_GDAT_OFFGAME", Family: campTop2ID, FontSHA256: digest(font), Rows: 14, GDAT: result}
	return out, nil
}
