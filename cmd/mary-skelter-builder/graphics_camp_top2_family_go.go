package main

// candidate complete MS2 camp_top_2 name-family rendering in pure Go.
// The entire source-proven band is alpha-cleared before all 14 upright source
// strings are rendered horizontally and transposed 90 degrees, matching the
// accepted family layout. Nothing here writes to an installed game.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
)

type Asset struct {
	ID         string `json:"id"`
	Renderer   string `json:"renderer"`
	Typography string `json:"typography_preset"`
	Band       []int  `json:"clear_band"`
	Rows       []struct {
		Text string `json:"text"`
	} `json:"rows"`
	Variants []struct {
		SHA    string `json:"tid_sha256"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Format string `json:"format"`
	} `json:"source_variants"`
}
type Manifest struct {
	Assets []Asset `json:"assets"`
}
type Font struct {
	RepoPath string `json:"repo_path"`
	SHA      string `json:"reference_sha256"`
}
type Typography struct {
	Fonts   map[string]Font `json:"fonts"`
	Presets map[string]struct {
		Face    string  `json:"font_face"`
		Size    int     `json:"nominal_size"`
		MinSize int     `json:"min_size"`
		PadX    int     `json:"padding_x"`
		PadY    int     `json:"padding_y"`
		HScale  float64 `json:"horizontal_scale"`
		VScale  float64 `json:"vertical_scale"`
		Fill    []int   `json:"fill_rgba"`
	} `json:"presets"`
}
type RowReport struct {
	Text         string `json:"text"`
	PixelSize    int    `json:"font_pixel_size"`
	GlyphBounds  [2]int `json:"raster_fit_width_height"`
	Lane         [4]int `json:"lane_xyxy"`
	NonzeroAlpha int    `json:"nonzero_alpha"`
}
type Report struct {
	Status            string      `json:"status"`
	Scope             string      `json:"scope"`
	SourceSHA         string      `json:"source_tid_sha256"`
	FontSHA           string      `json:"font_sha256"`
	ResultSHA         string      `json:"result_tid_sha256"`
	ChangedBlocks     int         `json:"changed_blocks"`
	OutsideBandBlocks int         `json:"outside_band_changed_blocks"`
	EmptyLaneCount    int         `json:"empty_lanes"`
	Rows              []RowReport `json:"rows"`
}

// Rasters into a generous temporary canvas so glyphs can be measured and
// scaled *before* any clipping against actual 296x44 family lanes.
func RasterFit(sfnt *graphicsgo.SFNT, text string, targetW, targetH, nominal, minimum, padX, padY int, hScale, vScale float64) (*image.Alpha, RowReport, error) {
	info := RowReport{Text: text}
	if len(text) == 0 || targetW != 296 || targetH != 44 || nominal < minimum || minimum < 8 || padX < 2 || padY < 2 || hScale <= 0 || vScale <= 0 {
		return nil, info, errors.New("unsafe family layout/preset")
	}
	const tempW, tempH = 512, 120
	for size := nominal; size >= minimum; size-- {
		a, err := sfnt.RasterLatinRunSupersampled(text, size, 2, 4, tempW, tempH, 36, 83)
		if err != nil {
			return nil, info, fmt.Errorf("%q: raster: %w", text, err)
		}
		bounds := image.Rectangle{Min: image.Pt(tempW, tempH), Max: image.Pt(0, 0)}
		nonzero := 0
		for y := 0; y < tempH; y++ {
			for x := 0; x < tempW; x++ {
				if a.AlphaAt(x, y).A > 0 {
					nonzero++
					if x < bounds.Min.X {
						bounds.Min.X = x
					}
					if x >= bounds.Max.X {
						bounds.Max.X = x + 1
					}
					if y < bounds.Min.Y {
						bounds.Min.Y = y
					}
					if y >= bounds.Max.Y {
						bounds.Max.Y = y + 1
					}
				}
			}
		}
		if nonzero == 0 || bounds.Empty() || bounds.Min.X < 3 || bounds.Max.X >= tempW-3 || bounds.Min.Y < 3 || bounds.Max.Y >= tempH-3 {
			return nil, info, fmt.Errorf("%q: raster blank or clipping (bounds=%v; nonzero=%d)", text, bounds, nonzero)
		}
		w, h := bounds.Dx(), bounds.Dy()
		scaledW := int(math.Round(float64(w) * hScale))
		scaledH := int(math.Round(float64(h) * vScale))
		if scaledW > targetW-2*padX || scaledH > targetH-2*padY {
			continue
		}
		if scaledW < 1 || scaledH < 1 {
			continue
		}
		crop := image.NewAlpha(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			copy(crop.Pix[y*crop.Stride:y*crop.Stride+w], a.Pix[(bounds.Min.Y+y)*a.Stride+bounds.Min.X:(bounds.Min.Y+y)*a.Stride+bounds.Min.X+w])
		}
		scaled, err := graphicsgo.ResizeLanczosAlpha(crop, scaledW, scaledH)
		if err != nil {
			return nil, info, err
		}
		full := image.NewAlpha(image.Rect(0, 0, targetW, targetH))
		offX, offY := (targetW-scaledW)/2, (targetH-scaledH)/2
		for y := 0; y < scaledH; y++ {
			for x := 0; x < scaledW; x++ {
				full.SetAlpha(offX+x, offY+y, scaled.AlphaAt(x, y))
			}
		}
		info.PixelSize = size
		info.GlyphBounds = [2]int{scaledW, scaledH}
		info.NonzeroAlpha = nonzero
		return full, info, nil
	}
	return nil, info, fmt.Errorf("%q cannot fit %dx%d without clipping above minimum font size", text, targetW, targetH)
}

func Render(source, font []byte, texts []string, nominal, minimum, padX, padY int, hs, vs float64, region image.Rectangle) ([]byte, *image.NRGBA, Report, error) {
	rep := Report{Scope: "source-proven MS2_CAMP_TOP2_CHARACTER_NAMES; CANDIDATE ONLY, no runtime approval", SourceSHA: SHA(source), FontSHA: SHA(font)}
	meta, err := mskit.ParseTIDMeta(source)
	if err != nil {
		return nil, nil, rep, err
	}
	if meta.Format != "DXT5" || meta.Width != 2048 || meta.Height != 2048 || region != image.Rect(1420, 816, 2036, 1112) || len(texts) != 14 {
		return nil, nil, rep, errors.New("unsupported source/family layout")
	}
	sfnt, err := graphicsgo.ParseSFNT(font)
	if err != nil {
		return nil, nil, rep, err
	}
	const lane = 44
	mask := image.NewAlpha(image.Rect(0, 0, region.Dx(), region.Dy()))
	for i, text := range texts {
		horiz, info, e := RasterFit(sfnt, text, region.Dy(), lane, nominal, minimum, padX, padY, hs, vs)
		if e != nil {
			return nil, nil, rep, e
		}
		// Pillow ROTATE_90: output x=original y, y=(original width-1)-original x.
		count := 0
		for y := 0; y < lane; y++ {
			for x := 0; x < region.Dy(); x++ {
				v := horiz.AlphaAt(x, y).A
				if v != 0 {
					count++
				}
				mask.SetAlpha(i*lane+y, region.Dy()-1-x, color.Alpha{A: v})
			}
		}
		if count == 0 {
			return nil, nil, rep, fmt.Errorf("empty lane %s", text)
		}
		info.NonzeroAlpha = count
		info.Lane = [4]int{region.Min.X + i*lane, region.Min.Y, region.Min.X + (i+1)*lane, region.Max.Y}
		rep.Rows = append(rep.Rows, info)
	}
	// All channels white in transparent pixels too, so BC3's independent RGB
	// compression does not blend in black/cyan background fringes at the edge.
	clean := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	for i := 0; i < len(clean.Pix); i += 4 {
		clean.Pix[i], clean.Pix[i+1], clean.Pix[i+2], clean.Pix[i+3] = 255, 255, 255, 0
	}
	output, preview, texReport, err := CreateTID(source, region, clean, mask, color.NRGBA{255, 255, 255, 255})
	if err != nil {
		return nil, nil, rep, err
	}
	rep.ResultSHA = SHA(output)
	rep.ChangedBlocks = texReport.ChangedBlocks
	rep.OutsideBandBlocks = texReport.PixelsOutsideRectChanged
	rep.Status = "PASS_SOURCE_GEOMETRY_ONLY"
	return output, preview, rep, nil
}

func executeCampTop2(root string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("graphics-camp-top2-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sourcePath := fs.String("source-tid", "", "original source camp_top_2.tid; hash checked against graphics manifest")
	outputPath := fs.String("out-tid", "", "new TID output (no-clobber)")
	previewPath := fs.String("preview", "", "optional 616x296 PNG crop")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *sourcePath == "" || *outputPath == "" {
		return errors.New("usage: graphics-camp-top2-go --source-tid source.tid --out-tid new.tid [--preview crop.png]")
	}
	if filepath.Clean(*sourcePath) == filepath.Clean(*outputPath) || (*previewPath != "" && (filepath.Clean(*previewPath) == filepath.Clean(*outputPath) || filepath.Clean(*previewPath) == filepath.Clean(*sourcePath))) {
		return errors.New("refuse input/output collision")
	}
	for _, dest := range []string{*outputPath, *previewPath} {
		if dest == "" {
			continue
		}
		if _, err := os.Lstat(dest); err == nil {
			return fmt.Errorf("output already exists: %s", dest)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	var man Manifest
	raw, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &man); err != nil {
		return err
	}
	var asset *Asset
	for i := range man.Assets {
		if man.Assets[i].ID == "MS2_CAMP_TOP2_CHARACTER_NAMES" {
			asset = &man.Assets[i]
			break
		}
	}
	if asset == nil || asset.Renderer != "ms2_camp_top2_character_names_font_v3" || asset.Typography != "camp_top2_vertical_neofara" || len(asset.Variants) != 1 || len(asset.Rows) != 14 || len(asset.Band) != 4 {
		return errors.New("canonical family manifest mismatch")
	}
	if asset.Variants[0].SHA != "6cac6e1c439db75ac43084e3452fb959c11984030b178f7b6a23c2ba445d8f39" || asset.Variants[0].Format != "DXT5" || asset.Band[0] != 1420 || asset.Band[1] != 816 || asset.Band[2] != 2036 || asset.Band[3] != 1112 {
		return errors.New("canonical source/layout drift")
	}
	source, err := ReadPinned(*sourcePath, asset.Variants[0].SHA)
	if err != nil {
		return err
	}
	var typo Typography
	typRaw, err := os.ReadFile(filepath.Join(root, "graphics", "ui_typography.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(typRaw, &typo); err != nil {
		return err
	}
	p, ok := typo.Presets[asset.Typography]
	if !ok || p.Face != "neofara" || len(p.Fill) != 4 || p.Fill[0] != 255 || p.Fill[1] != 255 || p.Fill[2] != 255 || p.Fill[3] != 255 {
		return errors.New("unexpected source typography")
	}
	fontCfg, ok := typo.Fonts[p.Face]
	if !ok || fontCfg.RepoPath == "" {
		return errors.New("font not configured")
	}
	fontPath := filepath.Join(root, fontCfg.RepoPath)
	if !strings.HasPrefix(filepath.Clean(fontPath), filepath.Clean(root)+string(os.PathSeparator)) {
		return errors.New("unsafe font path")
	}
	font, err := ReadPinned(fontPath, fontCfg.SHA)
	if err != nil {
		return err
	}
	texts := make([]string, len(asset.Rows))
	for i, row := range asset.Rows {
		texts[i] = row.Text
	}
	region := image.Rect(asset.Band[0], asset.Band[1], asset.Band[2], asset.Band[3])
	result, preview, rep, err := Render(source, font, texts, p.Size, p.MinSize, p.PadX, p.PadY, p.HScale, p.VScale, region)
	if err != nil {
		return err
	}
	if *previewPath != "" {
		var buffer bytes.Buffer
		if err = png.Encode(&buffer, preview); err != nil {
			return err
		}
		if err = PublishNew(*previewPath, buffer.Bytes()); err != nil {
			return err
		}
	}
	if err = PublishNew(*outputPath, result); err != nil {
		if *previewPath != "" {
			_ = os.Remove(*previewPath)
		}
		return err
	}
	return json.NewEncoder(out).Encode(rep)
}
