package main

// legacy read-only source-bound research for reconstructing the pre-text blue
// plate artwork from the 15 authentic MS2_BATTLE_NAMES BC7 cells. This is not
// a finished font renderer, BC7 encoder, GDAT writer, or production code.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"maryskelter-unburger/internal/mskit"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
)

const battleSHA = "3dd5e08c9e7311acbc7cda6f5a77e65cb2f862aedc075bae3657fed83bf77379"
const battleFile = "MS2_BATTLE_NAMES__a055__g01331__battle_2.tid"
const bw = 343
const bh = 181
const cellpx = bw * bh

var battleOrder = []string{"TSUU", "NINGYOHIME", "HAMELN", "GRETEL", "RAPUNZEL", "KAGUYAHIME", "OYAYUBIHIME", "SHIRAYUKIHIME", "NEMURIHIME", "CINDERELLA", "AKAZUKIN", "ALICE", "JACK", "MAMORU", "HANSEL"}

type battleRow struct {
	Text string `json:"text"`
	Rect []int  `json:"rect"`
}
type battleBackgroundAsset struct {
	ID       string      `json:"id"`
	Rows     []battleRow `json:"rows"`
	TextBox  []int       `json:"text_box"`
	Renderer string      `json:"renderer"`
}
type battleManifest struct {
	Assets []battleBackgroundAsset `json:"assets"`
}
type battleBackgroundSummary struct {
	OriginalSHA                     string         `json:"original_sha256"`
	Rows                            int            `json:"rows"`
	OriginalWidth                   int            `json:"original_width"`
	OriginalHeight                  int            `json:"original_height"`
	SeedThreshold                   string         `json:"seed_threshold"`
	MaskDilationDiameter            int            `json:"mask_dilation_diameter"`
	MaskedPixels                    []int          `json:"masked_pixels_by_row"`
	MaskedInside                    []int          `json:"mask_pixel_counts_inside_text_box"`
	PixelsReplaced                  []int          `json:"pixels_replaced_by_row"`
	DonorDistribution               map[string]int `json:"donor_valid_count_histogram"`
	Majority4SourcePositions        int            `json:"source_positions_with_at_least_4_valid_identical_donors"`
	SourcePositionsWithNoValidDonor int            `json:"positions_without_any_valid_donor"`
	HoleFillIterations              int            `json:"neighbor_fill_iterations"`
	UnfilledHolePositions           int            `json:"unfilled_hole_positions"`
	FaceSeedsAfterInMaskedAreas     int            `json:"face_like_pixels_remaining_in_cleaned_mask"`
	UnchangedOutsideTextZone        bool           `json:"unchanged_outside_text_zone"`
	UnchangedUnmaskedPixels         bool           `json:"unchanged_unmasked_pixels"`
	ProductionStatus                string         `json:"production_status"`
}

func battleBright(p []byte, at int) bool {
	r, g, b, a := int(p[at]), int(p[at+1]), int(p[at+2]), int(p[at+3])
	return a > 20 && r > 135 && g > 165 && b > 180 && r+g+b > 530
}
func battleDilate(src []bool, radius int) []bool {
	// Exact square morphological dilation, equivalent to Pillow MaxFilter(2r+1).
	row := make([]bool, cellpx)
	dst := make([]bool, cellpx)
	for y := 0; y < bh; y++ {
		base := y * bw
		n := 0
		for k := 0; k <= radius && k < bw; k++ {
			if src[base+k] {
				n++
			}
		}
		for x := 0; x < bw; x++ {
			if x > 0 {
				if x+radius < bw && src[base+x+radius] {
					n++
				}
				if x-radius-1 >= 0 && src[base+x-radius-1] {
					n--
				}
			}
			row[base+x] = n > 0
		}
	}
	for x := 0; x < bw; x++ {
		n := 0
		for k := 0; k <= radius && k < bh; k++ {
			if row[k*bw+x] {
				n++
			}
		}
		for y := 0; y < bh; y++ {
			if y > 0 {
				if y+radius < bh && row[(y+radius)*bw+x] {
					n++
				}
				if y-radius-1 >= 0 && row[(y-radius-1)*bw+x] {
					n--
				}
			}
			dst[y*bw+x] = n > 0
		}
	}
	return dst
}
func battleRGBAKey(p []byte, off int) uint32 {
	return uint32(p[off])<<24 | uint32(p[off+1])<<16 | uint32(p[off+2])<<8 | uint32(p[off+3])
}
func battleRGBASet(p []byte, off int, v uint32) {
	p[off] = byte(v >> 24)
	p[off+1] = byte(v >> 16)
	p[off+2] = byte(v >> 8)
	p[off+3] = byte(v)
}
func battleRecover(root string, saveDir string) (battleBackgroundSummary, error) {
	st := battleBackgroundSummary{OriginalSHA: battleSHA, Rows: 15, SeedThreshold: "alpha>20,r>135,g>165,b>180,sum>530", MaskDilationDiameter: 31, DonorDistribution: map[string]int{}, UnchangedOutsideTextZone: true, UnchangedUnmaskedPixels: true, ProductionStatus: "RESEARCH HOLD: no fonts applied, no BC7/GDAT, no owner runtime"}
	battleOriginalPath, err := privateassets.Resolve(root, "source-battle-tid")
	if err != nil {
		return st, err
	}
	data, err := os.ReadFile(battleOriginalPath)
	if err != nil {
		return st, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != battleSHA {
		return st, errors.New("original battle TID SHA-256 mismatch")
	}
	rgba, meta, err := mskit.DecodeTIDBC7RGBA(data)
	if err != nil {
		return st, err
	}
	if meta.Width != 2048 || meta.Height != 4096 || meta.Format != "BC7" {
		return st, fmt.Errorf("source dimensions/format changed: %+v", meta)
	}
	st.OriginalWidth = meta.Width
	st.OriginalHeight = meta.Height
	raw, err := os.ReadFile(filepath.Join(root, "graphics/manifest.json"))
	if err != nil {
		return st, err
	}
	var manifest battleManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return st, err
	}
	var rows []battleRow
	count := 0
	for _, a := range manifest.Assets {
		if a.ID == "MS2_BATTLE_NAMES" {
			rows = a.Rows
			count++
			if a.Renderer != "ms2_battle_nameplates_font_v3" || len(a.TextBox) != 4 || a.TextBox[0] != 0 || a.TextBox[1] != 24 || a.TextBox[2] != 343 || a.TextBox[3] != 145 {
				return st, errors.New("battle mapping drift")
			}
		}
	}
	if count != 1 || len(rows) != 15 {
		return st, errors.New("expected exactly 15 battle rows")
	}
	cells := make([][]byte, 15)
	masks := make([][]bool, 15)
	for i, row := range rows {
		if row.Text != battleOrder[i] || len(row.Rect) != 4 {
			return st, fmt.Errorf("battle row %d text/rect drift", i)
		}
		x0, y0, x1, y1 := row.Rect[0], row.Rect[1], row.Rect[2], row.Rect[3]
		if x1-x0 != bw || y1-y0 != bh || x0 < 0 || y0 < 0 || x1 > meta.Width || y1 > meta.Height {
			return st, fmt.Errorf("battle row %d dimensions drift", i)
		}
		cell := make([]byte, cellpx*4)
		seed := make([]bool, cellpx)
		for y := 0; y < bh; y++ {
			copy(cell[y*bw*4:(y+1)*bw*4], rgba[((y0+y)*meta.Width+x0)*4:((y0+y)*meta.Width+x0+bw)*4])
		}
		for at := 0; at < cellpx; at++ {
			seed[at] = battleBright(cell, at*4)
		}
		masks[i] = battleDilate(seed, 15)
		for p := range masks[i] {
			y := p / bw
			if y < 24 || y >= 145 {
				masks[i][p] = false
			}
		}
		cells[i] = cell
		n := 0
		for _, m := range masks[i] {
			if m {
				n++
			}
		}
		st.MaskedPixels = append(st.MaskedPixels, n)
		st.MaskedInside = append(st.MaskedInside, n)
	}
	// Choose modal exact pixel among original cells in which THAT location isn't
	// potentially touched by their own lettering. Never use a masked donor.
	patch := make([]byte, cellpx*4)
	known := make([]bool, cellpx)
	for pos := 0; pos < cellpx; pos++ {
		votes := map[uint32]int{}
		max := 0
		var chosen uint32
		count := 0
		for i := 0; i < 15; i++ {
			if masks[i][pos] {
				continue
			}
			count++
			v := battleRGBAKey(cells[i], pos*4)
			votes[v]++
			if votes[v] > max {
				max = votes[v]
				chosen = v
			}
		}
		st.DonorDistribution[fmt.Sprintf("%d", count)]++
		if count > 0 {
			known[pos] = true
			battleRGBASet(patch, pos*4, chosen)
			if max >= 4 {
				st.Majority4SourcePositions++
			}
		}
		if count == 0 {
			st.SourcePositionsWithNoValidDonor++
		}
	}
	// Fill only tiny donor gaps, propagating from reliable cells. Synchronous 8-
	// neighborhood average, avoiding any donor's old glyph RGB at blind spots.
	for iteration := 0; iteration < 96; iteration++ {
		fill := make([]bool, cellpx)
		next := make([]byte, cellpx*4)
		filled := 0
		for pos := 0; pos < cellpx; pos++ {
			if known[pos] {
				continue
			}
			x, y := pos%bw, pos/bw
			var sums [4]int
			cnt := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					xx, yy := x+dx, y+dy
					if xx < 0 || yy < 0 || xx >= bw || yy >= bh || !known[yy*bw+xx] {
						continue
					}
					off := (yy*bw + xx) * 4
					cnt++
					for c := 0; c < 4; c++ {
						sums[c] += int(patch[off+c])
					}
				}
			}
			if cnt > 0 {
				fill[pos] = true
				filled++
				for c := 0; c < 4; c++ {
					next[pos*4+c] = byte((sums[c] + cnt/2) / cnt)
				}
			}
		}
		if filled == 0 {
			break
		}
		for pos, yes := range fill {
			if yes {
				known[pos] = true
				copy(patch[pos*4:pos*4+4], next[pos*4:pos*4+4])
			}
		}
		st.HoleFillIterations++
	}
	for _, k := range known {
		if !k {
			st.UnfilledHolePositions++
		}
	}
	// Commit cleaned copies ONLY under their own old lettering masks. No atlas or
	// font assets are changed; output PNGs are private visual QA, not game assets.
	cleaned := make([][]byte, 15)
	for i := 0; i < 15; i++ {
		o := make([]byte, len(cells[i]))
		copy(o, cells[i])
		count := 0
		for pos, use := range masks[i] {
			if !use {
				continue
			}
			off := pos * 4
			before := battleRGBAKey(o, off)
			after := battleRGBAKey(patch, off)
			if before != after {
				count++
			}
			battleRGBASet(o, off, after)
			if battleBright(o, off) {
				st.FaceSeedsAfterInMaskedAreas++
			}
		}
		for p := 0; p < cellpx; p++ {
			same := battleRGBAKey(o, p*4) == battleRGBAKey(cells[i], p*4)
			if !masks[i][p] && !same {
				st.UnchangedUnmaskedPixels = false
			}
			if y := p / bw; y < 24 || y >= 145 {
				if !same {
					st.UnchangedOutsideTextZone = false
				}
			}
		}
		st.PixelsReplaced = append(st.PixelsReplaced, count)
		cleaned[i] = o
	}
	if saveDir != "" {
		if err = os.MkdirAll(saveDir, 0700); err != nil {
			return st, err
		}
		sheet := image.NewRGBA(image.Rect(0, 0, bw*4, bh*4))
		for i, cell := range cleaned {
			cx := i % 4 * bw
			cy := i / 4 * bh
			for y := 0; y < bh; y++ {
				for x := 0; x < bw; x++ {
					at := (y*bw + x) * 4
					sheet.SetRGBA(cx+x, cy+y, color.RGBA{cell[at], cell[at+1], cell[at+2], cell[at+3]})
				}
			}
		}
		f, err := os.Create(filepath.Join(saveDir, "BATTLE_CLEANED_PLATES_PRIVATE_QA.png"))
		if err != nil {
			return st, err
		}
		err = png.Encode(f, sheet)
		closeErr := f.Close()
		if err != nil {
			return st, err
		}
		if closeErr != nil {
			return st, closeErr
		}
		report, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return st, err
		}
		if err = os.WriteFile(filepath.Join(saveDir, "BATTLE_CLEANUP_SOURCE_QA.json"), append(report, '\n'), 0600); err != nil {
			return st, err
		}
	}
	return st, nil
}
