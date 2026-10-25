package main

// All-six MS2 DLC label font renderer, source pinned and Q6-guarded.
import (
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

const dlcJobLabelsID = "MS2_DLC_JOB_LABELS"

type dlcJobLabelTarget struct {
	Index  int    `json:"gdat_index"`
	Inner  string `json:"inner_name"`
	SHA    string `json:"tid_sha256"`
	W      int    `json:"width"`
	H      int    `json:"height"`
	Format string `json:"format"`
	From   string `json:"from"`
	To     string `json:"to"`
}
type dlcJobLabelAsset struct {
	ID       string          `json:"id"`
	Renderer string          `json:"renderer"`
	Archive  int             `json:"archive"`
	Targets  []dlcJobLabelTarget `json:"targets"`
}

func loadDLCJobLabels(root string) (dlcJobLabelAsset, []byte, error) {
	var a dlcJobLabelAsset
	b, err := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if err != nil {
		return a, nil, err
	}
	var m struct {
		Assets []json.RawMessage `json:"assets"`
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return a, nil, err
	}
	n := 0
	for _, v := range m.Assets {
		var key struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal(v, &key); err != nil {
			return a, nil, err
		}
		if key.ID == dlcJobLabelsID {
			n++
			if err = json.Unmarshal(v, &a); err != nil {
				return a, nil, err
			}
		}
	}
	if n != 1 || a.Archive != 55 || a.Renderer != "ms2_dlc_job_labels_v1" || len(a.Targets) != 6 {
		return a, nil, errors.New("legacy DLC source manifest drift")
	}
	for k, t := range a.Targets {
		if t.Index != 1541+k || t.Inner == "" || t.From == "" || t.To == "" || t.W != 296 || t.H != 56 || t.Format != "DXT5" || len(t.SHA) != 64 {
			return a, nil, errors.New("legacy DLC target layout drift")
		}
	}
	b, err = os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if err != nil {
		return a, nil, err
	}
	var fonts struct {
		Fonts map[string]familyFont `json:"fonts"`
	}
	if err = json.Unmarshal(b, &fonts); err != nil {
		return a, nil, err
	}
	cfg := fonts.Fonts["neofara"]
	if !strings.HasPrefix(cfg.Path, "third_party/fonts/") || strings.Contains(cfg.Path, "..") || filepath.IsAbs(cfg.Path) {
		return a, nil, errors.New("legacy unsafe NeoFara font")
	}
	fb, err := familyReadPinned(filepath.Join(root, cfg.Path), cfg.SHA)
	return a, fb, err
}
func renderDLCJobLabel(tid, font []byte, a dlcJobLabelTarget) ([]byte, error) {
	if digest(tid) != a.SHA {
		return nil, errors.New("legacy DLC pristine TID SHA mismatch")
	}
	rgba, meta, e := mskit.DecodeTIDDXT5RGBA(tid)
	if e != nil {
		return nil, e
	}
	if meta.Width != 296 || meta.Height != 56 || meta.Format != "DXT5" {
		return nil, errors.New("legacy DLC wrong texture shape")
	}
	sfnt, e := graphicsgo.ParseSFNT(font)
	if e != nil {
		return nil, e
	}
	fit := graphicsgo.R32C3FitSpec{Width: 296, Height: 56, Factor: 4, NominalSize: 40, MinSize: 17, InnerWidth: 2, StrokeWidth: 3, HorizontalScale: 1.0, MinHorizontalScale: .45, VerticalScale: .92, ShearX: 0}
	comp := graphicsgo.R32C3CompositeSpec{Width: 296, Height: 56, Factor: 4, InnerColor: color.NRGBA{255, 242, 0, 255}, OuterColor: color.NRGBA{242, 10, 97, 255}, GradientEnabled: true, GradientTop: color.NRGBA{255, 250, 50, 255}, GradientBottom: color.NRGBA{255, 205, 0, 255}, ShadowColor: color.NRGBA{160, 0, 58, 190}, ShadowOffset: image.Pt(1, 2), ShadowBlurRadius: .4}
	im, _, e := sfnt.RenderR32C3LatinLabelFastRadial(a.To, fit, graphicsgo.R32C3Warp{Factor: 4}, comp, 2)
	if e != nil {
		return nil, fmt.Errorf("DLC %s: %w", a.To, e)
	}
	for y := 0; y < 56; y++ {
		for x := 0; x < 296; x++ {
			o := (y*296 + x) * 4
			rgba[o], rgba[o+1], rgba[o+2], rgba[o+3] = 255, 232, 16, 0
			c := im.NRGBAAt(x, y)
			if c.A > 0 {
				rgba[o], rgba[o+1], rgba[o+2], rgba[o+3] = c.R, c.G, c.B, c.A
			}
		}
	}
	return rgba, nil
}
