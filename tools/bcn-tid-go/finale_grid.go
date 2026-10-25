package main

// legacy experimental source-glyph reconstruction of Finale's baked name grid.
// Extracts original shipped letter pixels, never substitutes an unrelated font.
// Promotion into the production family allowlist requires source codec QA.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maryskelter-unburger/internal/mskit"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const finaleGridID = "MSF_EN_FINALE_CHARACTER_GRID"
const finaleGridLayoutSHA = "c57f063c953478e8f86b03efe5e5c861f2f982dba2cbaeba5b4ea811f8426e9e"
const finaleGridSHA = "b29bd53fa0b7d06d79a3a1c3bffbe8183a047ec6b720ee473d962a8781e2e94a"

type gridCell struct {
	Source string `json:"source"`
	Rect   []int  `json:"rect"`
}
type gridReplacement struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rect []int  `json:"rect"`
}
type gridSource struct {
	SHA    string `json:"tid_sha256"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Format string `json:"format"`
}
type gridAsset struct {
	ID           string                `json:"id"`
	Renderer     string                `json:"renderer"`
	Archive      int                   `json:"archive"`
	Index        int                   `json:"gdat_index"`
	Inner        string                `json:"cl3_inner_name"`
	Face         string                `json:"face_mode"`
	TargetH      int                   `json:"target_height"`
	Gap          int                   `json:"gap"`
	Space        int                   `json:"space_width"`
	Clear        string                `json:"clear_mode"`
	Sources      []gridSource      `json:"source_variants"`
	Cells        []gridCell        `json:"glyph_cells"`
	Replacements []gridReplacement `json:"replacements"`
}

func loadFinaleGrid(root string) (gridAsset, error) {
	var a gridAsset
	b, e := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if e != nil {
		return a, e
	}
	var mf struct {
		Assets []json.RawMessage `json:"assets"`
	}
	if e = json.Unmarshal(b, &mf); e != nil {
		return a, e
	}
	n := 0
	for _, v := range mf.Assets {
		var key struct {
			ID string `json:"id"`
		}
		if e = json.Unmarshal(v, &key); e != nil {
			return a, e
		}
		if key.ID == finaleGridID {
			n++
			if e = json.Unmarshal(v, &a); e != nil {
				return a, e
			}
		}
	}
	if n != 1 || a.Renderer != "raster_atlas_v1" || a.Archive != 73 || a.Index != 71 || a.Inner != "bonus_character_f.tid" || a.Face != "white" || a.TargetH != 32 || a.Gap != -1 || a.Space != 12 || a.Clear != "transparent" || len(a.Cells) != 13 || len(a.Replacements) != 8 || len(a.Sources) != 1 || a.Sources[0] != (gridSource{finaleGridSHA, 2048, 1024, "DXT5"}) {
		return a, errors.New("Finale grid manifest/source layout drift")
	}
	var layout strings.Builder
	for _, v := range a.Cells {
		if v.Source == "" || !validGridRect(v.Rect) {
			return a, errors.New("invalid grid glyph donor")
		}
		fmt.Fprintf(&layout, "%s|%d,%d,%d,%d\n", v.Source, v.Rect[0], v.Rect[1], v.Rect[2], v.Rect[3])
	}
	for _, v := range a.Replacements {
		if v.To == "" || !validGridRect(v.Rect) {
			return a, errors.New("invalid grid target")
		}
		fmt.Fprintf(&layout, "%s>%s|%d,%d,%d,%d\n", v.From, v.To, v.Rect[0], v.Rect[1], v.Rect[2], v.Rect[3])
	}
	hash := sha256.Sum256([]byte(layout.String()))
	if hex.EncodeToString(hash[:]) != finaleGridLayoutSHA {
		return a, errors.New("Finale raster source glyph/target layout SHA drift")
	}
	return a, nil
}
func validGridRect(r []int) bool {
	return len(r) == 4 && r[0] >= 0 && r[1] >= 0 && r[2] <= 2048 && r[3] <= 1024 && r[0] < r[2] && r[1] < r[3]
}

type gridGlyph struct {
	W, H int
	Pix  []byte
}

func gridFace(p []byte) bool { return p[3] > 30 && p[0] > 150 && p[1] > 150 && p[2] > 150 }
func gridRuns(proj []int) [][2]int {
	var runs [][2]int
	start := -1
	for i := 0; i <= len(proj); i++ {
		ok := i < len(proj) && proj[i] > 0
		if ok && start < 0 {
			start = i
		} else if !ok && start >= 0 {
			runs = append(runs, [2]int{start, i})
			start = -1
		}
	}
	return runs
}
func gridExtract(rgba []byte, width int, r []int, text string) (map[byte]gridGlyph, error) {
	if strings.Contains(text, " ") {
		return nil, errors.New("donor must be single word")
	}
	w, h := r[2]-r[0], r[3]-r[1]
	proj := make([]int, w)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			p := rgba[((r[1]+y)*width+r[0]+x)*4:]
			if gridFace(p) {
				proj[x]++
			}
		}
	}
	runs := gridRuns(proj)
	if len(runs) != len(text) {
		return nil, fmt.Errorf("donor %s glyph count %d != %d", text, len(runs), len(text))
	}
	out := make(map[byte]gridGlyph)
	for k, ch := range []byte(text) {
		from, to := runs[k][0], runs[k][1]
		if k > 0 {
			from = (runs[k-1][1] + runs[k][0]) / 2
		}
		if k+1 < len(runs) {
			to = (runs[k][1] + runs[k+1][0]) / 2
		}
		from = max(0, from-2)
		to = min(w, to+2)
		mask := make([]bool, (to-from)*h)
		sw := to - from
		for y := 0; y < h; y++ {
			for x := 0; x < sw; x++ {
				p := rgba[((r[1]+y)*width+r[0]+from+x)*4:]
				if gridFace(p) {
					for yy := max(0, y-2); yy <= min(h-1, y+2); yy++ {
						for xx := max(0, x-2); xx <= min(sw-1, x+2); xx++ {
							mask[yy*sw+xx] = true
						}
					}
				}
			}
		}
		x0, y0, x1, y1 := sw, h, 0, 0
		for y := 0; y < h; y++ {
			for x := 0; x < sw; x++ {
				if mask[y*sw+x] {
					x0 = min(x0, x)
					x1 = max(x1, x+1)
					y0 = min(y0, y)
					y1 = max(y1, y+1)
				}
			}
		}
		if x0 >= x1 || y0 >= y1 {
			return nil, fmt.Errorf("empty donor %c from %s", ch, text)
		}
		x0 = max(0, x0-1)
		y0 = max(0, y0-1)
		x1 = min(sw, x1+1)
		y1 = min(h, y1+1)
		g := gridGlyph{W: x1 - x0, H: y1 - y0, Pix: make([]byte, (x1-x0)*(y1-y0)*4)}
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				if mask[y*sw+x] {
					dst := ((y-y0)*g.W + (x - x0)) * 4
					src := ((r[1]+y)*width + r[0] + from + x) * 4
					copy(g.Pix[dst:dst+4], rgba[src:src+4])
				}
			}
		}
		out[ch] = g
	}
	return out, nil
}
func gridScale(g gridGlyph, w, h int) gridGlyph {
	if w == g.W && h == g.H {
		return g
	}
	dst := gridGlyph{W: w, H: h, Pix: make([]byte, w*h*4)}
	for y := 0; y < h; y++ {
		sy := (float64(y)+.5)*float64(g.H)/float64(h) - .5
		y0 := int(math.Floor(sy))
		fy := sy - float64(y0)
		if y0 < 0 {
			y0 = 0
			fy = 0
		}
		y1 := min(g.H-1, y0+1)
		for x := 0; x < w; x++ {
			sx := (float64(x)+.5)*float64(g.W)/float64(w) - .5
			x0 := int(math.Floor(sx))
			fx := sx - float64(x0)
			if x0 < 0 {
				x0 = 0
				fx = 0
			}
			x1 := min(g.W-1, x0+1)
			dstp := (y*w + x) * 4
			for c := 0; c < 4; c++ {
				v := float64(g.Pix[(y0*g.W+x0)*4+c])*(1-fx)*(1-fy) + float64(g.Pix[(y0*g.W+x1)*4+c])*fx*(1-fy) + float64(g.Pix[(y1*g.W+x0)*4+c])*(1-fx)*fy + float64(g.Pix[(y1*g.W+x1)*4+c])*fx*fy
				dst.Pix[dstp+c] = uint8(math.Round(v))
			}
		}
	}
	return dst
}
func gridCompose(glyphs map[byte]gridGlyph, text string, h, gap, space int) (gridGlyph, error) {
	var parts []gridGlyph
	w := 0
	for i := 0; i < len(text); i++ {
		if text[i] == ' ' {
			parts = append(parts, gridGlyph{W: space, H: h})
			w += space
		} else {
			g, ok := glyphs[text[i]]
			if !ok {
				return gridGlyph{}, fmt.Errorf("missing original glyph %c", text[i])
			}
			sw := max(1, int(math.Round(float64(g.W)*float64(h)/float64(g.H))))
			z := gridScale(g, sw, h)
			parts = append(parts, z)
			w += sw
		}
	}
	w += gap * max(0, len(parts)-1)
	if w <= 0 {
		return gridGlyph{}, errors.New("negative label width")
	}
	out := gridGlyph{W: w, H: h, Pix: make([]byte, w*h*4)}
	at := 0
	for i, g := range parts {
		for y := 0; y < g.H; y++ {
			for x := 0; x < g.W; x++ {
				dst := ((y * out.W) + (at + x)) * 4
				src := (y*g.W + x) * 4
				if g.Pix != nil && g.Pix[src+3] > 0 {
					copy(out.Pix[dst:dst+4], g.Pix[src:src+4])
				}
			}
		}
		at += g.W
		if i+1 < len(parts) {
			at += gap
		}
	}
	return out, nil
}
func renderFinaleGrid(tid []byte, a gridAsset) ([]byte, error) {
	if digest(tid) != finaleGridSHA {
		return nil, errors.New("stale Finale grid source")
	}
	rgba, meta, e := mskit.DecodeTIDDXT5RGBA(tid)
	if e != nil {
		return nil, e
	}
	if meta.Width != 2048 || meta.Height != 1024 || meta.Format != "DXT5" {
		return nil, errors.New("grid format mismatch")
	}
	// Original single-word cells have unambiguous character-column splits.
	donors := map[string]bool{"MARY": true, "HAMELN": true, "ALICE": true, "OTSUU": true, "THUMBELINA": true, "PYRE": true, "KAGUYA": true, "RAPUNZEL": true}
	var widths = map[byte][]gridGlyph{}
	for _, cell := range a.Cells {
		if !donors[cell.Source] {
			continue
		}
		letters, err := gridExtract(rgba, meta.Width, cell.Rect, cell.Source)
		if err != nil {
			return nil, err
		}
		for k, v := range letters {
			widths[k] = append(widths[k], v)
		}
	}
	glyphs := map[byte]gridGlyph{}
	for k, candidates := range widths {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].W < candidates[j].W })
		glyphs[k] = candidates[len(candidates)/2]
	}
	type layer struct {
		x, y  int
		glyph gridGlyph
	}
	layers := make([]layer, 0, len(a.Replacements))
	for _, repl := range a.Replacements {
		r := repl.Rect
		g, err := gridCompose(glyphs, repl.To, a.TargetH, a.Gap, a.Space)
		if err != nil {
			return nil, err
		}
		rw, rh := r[2]-r[0]-4, r[3]-r[1]-4
		factor := min(1.0, math.Min(float64(rw)/float64(g.W), float64(rh)/float64(g.H)))
		if factor < 1 {
			g = gridScale(g, max(1, int(math.Round(float64(g.W)*factor))), max(1, int(math.Round(float64(g.H)*factor))))
		}
		x, y := r[0]+(r[2]-r[0]-g.W)/2, r[1]+(r[3]-r[1]-g.H)/2
		if x < r[0] || y < r[1] || x+g.W > r[2] || y+g.H > r[3] {
			return nil, errors.New("grid label clipped")
		}
		layers = append(layers, layer{x, y, g})
	}
	// Clear the union first: overlapping replacement rectangles must not erase
	// glyphs drawn by preceding labels.
	for _, repl := range a.Replacements {
		r := repl.Rect
		for y := r[1]; y < r[3]; y++ {
			for x := r[0]; x < r[2]; x++ {
				off := (y*meta.Width + x) * 4
				rgba[off+3] = 0
			}
		}
	}
	used := make([]bool, meta.Width*meta.Height)
	for _, lay := range layers {
		for y := 0; y < lay.glyph.H; y++ {
			for x := 0; x < lay.glyph.W; x++ {
				s := (y*lay.glyph.W + x) * 4
				if lay.glyph.Pix[s+3] == 0 {
					continue
				}
				ix := (lay.y+y)*meta.Width + lay.x + x
				if used[ix] {
					return nil, fmt.Errorf("overlapping raster ink at %d,%d", lay.x+x, lay.y+y)
				}
				used[ix] = true
				copy(rgba[ix*4:ix*4+4], lay.glyph.Pix[s:s+4])
			}
		}
	}
	return rgba, nil
}
