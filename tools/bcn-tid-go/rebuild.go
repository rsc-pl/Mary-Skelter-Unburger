package main

// Opt-in native-Go BC7 changed-block encoder. The source TID and unrelated
// blocks remain byte-identical. The result is NOT auto-deployed to any game.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/woozymasta/bcn"
	"maryskelter-unburger/internal/mskit"
)

type BC7Report struct {
	Format              string  `json:"format"`
	SourceSHA256        string  `json:"source_sha256"`
	TargetRGBA_SHA256   string  `json:"target_rgba_sha256"`
	OutputSHA256        string  `json:"output_sha256"`
	Width               int     `json:"width"`
	Height              int     `json:"height"`
	Quality             int     `json:"quality"`
	TotalBlocks         int     `json:"total_blocks"`
	ChangedBlocks       int     `json:"changed_blocks"`
	UnchangedBlocks     int     `json:"unchanged_blocks"`
	BC7FallbackAttempts int     `json:"bc7_fallback_attempts"`
	BC7FallbackSelected int     `json:"bc7_fallback_selected"`
	BC7CandidateTrials  int     `json:"bc7_candidate_trials"`
	BC3FallbackAttempts int     `json:"bc3_fallback_attempts"`
	BC3FallbackSelected int     `json:"bc3_fallback_selected"`
	BC3PaletteAttempts  int     `json:"bc3_palette_attempts"`
	BC3PaletteSelected  int     `json:"bc3_palette_selected"`
	BC3WideAttempts     int     `json:"bc3_wide_attempts"`
	BC3WideSelected     int     `json:"bc3_wide_selected"`
	BC3JointAttempts    int     `json:"bc3_joint_attempts"`
	BC3JointSelected    int     `json:"bc3_joint_selected"`
	MaxAlphaError       int     `json:"max_alpha_error"`
	MaxVisibleError     int     `json:"max_visible_error"`
	EncodeMilliseconds  float64 `json:"encode_ms"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func pxDiff(a, b []byte) bool {
	if a[3] != b[3] {
		return true
	}
	return a[3] != 0 && !bytes.Equal(a[:3], b[:3])
}
func tile4(rgba []byte, w, h, x, y int) [64]byte {
	var t [64]byte
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			sx, sy := x+xx, y+yy
			if sx >= w {
				sx = w - 1
			}
			if sy >= h {
				sy = h - 1
			}
			copy(t[(yy*4+xx)*4:], rgba[(sy*w+sx)*4:][:4])
		}
	}
	return t
}
func absI(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
func visibleDiff(want, got [64]byte) (alpha, visible int) {
	for i := 0; i < 64; i += 4 {
		a, b := int(want[i+3]), int(got[i+3])
		if d := absI(a - b); d > alpha {
			alpha = d
		}
		for c := 0; c < 3; c++ {
			w := (int(want[i+c])*a + 127) / 255
			g := (int(got[i+c])*b + 127) / 255
			black := absI(w - g)
			white := absI((w + 255 - a) - (g + 255 - b))
			if black > visible {
				visible = black
			}
			if white > visible {
				visible = white
			}
		}
	}
	return
}

// EncodeChangedBC7 keeps the complete original TID header and every unchanged
// compressed block. Limit checks are against TARGET pixels, not against the
// original image. A negative limit is invalid; 255 permits maximum deviations.
// EncodeChangedTID is the shared BC7/DXT5 changed-block producer. Its
// independent mskit decoder (not bcn itself) validates the output of each
// compressed block before it may be published.
func EncodeChangedTID(source, target []byte, quality, maxAlpha, maxVisible int) ([]byte, BC7Report, error) {
	var report BC7Report
	if quality < 1 || quality > 10 || maxAlpha < 0 || maxAlpha > 255 || maxVisible < 0 || maxVisible > 255 {
		return nil, report, errors.New("invalid BC7 quality or quality-guard limits")
	}
	meta, e := mskit.ParseTIDMeta(source)
	if e != nil {
		return nil, report, e
	}
	if meta.Format != "BC7" && meta.Format != "DXT5" {
		return nil, report, fmt.Errorf("source format %s is neither BC7 nor DXT5", meta.Format)
	}
	if meta.Width > 4096 || meta.Height > 4096 {
		return nil, report, errors.New("texture too large for bounded BC7 encoder")
	}
	count, e := mskit.TexturePayloadSize(meta.Width, meta.Height)
	if e != nil || len(source) != 128+count {
		return nil, report, errors.New("source TID size/metadata mismatch")
	}
	if len(target) != meta.Width*meta.Height*4 {
		return nil, report, errors.New("target RGBA size mismatch")
	}
	var originalRGBA []byte
	if meta.Format == "BC7" {
		originalRGBA, e = mskit.DecodeBC7RGBA(source[128:], meta.Width, meta.Height)
	} else {
		originalRGBA, e = mskit.DecodeDXT5RGBA(source[128:], meta.Width, meta.Height)
	}
	if e != nil {
		return nil, report, fmt.Errorf("pristine %s decode: %w", meta.Format, e)
	}
	payload := append([]byte(nil), source[128:]...)
	cols, rows := (meta.Width+3)/4, (meta.Height+3)/4
	report = BC7Report{Format: meta.Format, SourceSHA256: digest(source), TargetRGBA_SHA256: digest(target), Width: meta.Width, Height: meta.Height, Quality: quality, TotalBlocks: cols * rows}
	start := time.Now()
	for by := 0; by < rows; by++ {
		for bx := 0; bx < cols; bx++ {
			x, y := bx*4, by*4
			changed := false
			for yy := y; yy < y+4 && yy < meta.Height && !changed; yy++ {
				for xx := x; xx < x+4 && xx < meta.Width; xx++ {
					at := (yy*meta.Width + xx) * 4
					if pxDiff(originalRGBA[at:at+4], target[at:at+4]) {
						changed = true
						break
					}
				}
			}
			if !changed {
				report.UnchangedBlocks++
				continue
			}
			block := tile4(target, meta.Width, meta.Height, x, y)
			var enc []byte
			var err error
			if meta.Format == "BC7" {
				enc, err = bcn.EncodeBC7WithOptions(block[:], 4, 4, &bcn.EncodeOptions{QualityLevel: quality, Workers: 1})
			} else {
				enc, err = bcn.EncodeBC3WithOptions(block[:], 4, 4, &bcn.EncodeOptions{QualityLevel: quality, Workers: 1})
			}
			if err != nil {
				return nil, report, fmt.Errorf("encode %s block (%d,%d): %w", meta.Format, bx, by, err)
			}
			if len(enc) != 16 {
				return nil, report, errors.New("encoder returned wrong block size")
			}
			var actual [64]byte
			if meta.Format == "BC7" {
				actual, _, err = mskit.DecodeBC7Block(enc)
			} else {
				var decoded []byte
				decoded, err = mskit.DecodeDXT5RGBA(enc, 4, 4)
				if err == nil {
					copy(actual[:], decoded)
				}
			}
			if err != nil {
				return nil, report, fmt.Errorf("independent decode (%d,%d): %w", bx, by, err)
			}
			a, v := visibleDiff(block, actual)
			// A second encoder is useful for hard multicolor/gradient BC3
			// blocks. It is attempted ONLY where bcn Q6 fails a guard;
			// its output is independently decoded and accepted only if it
			// improves guard compliance. Neither path can waive the guard.
			if meta.Format == "DXT5" && (a > maxAlpha || v > maxVisible) {
				report.BC3FallbackAttempts++
				alt, altErr := mskit.EncodeBC3Block(block[:])
				if altErr == nil && len(alt) == 16 {
					altRGBA, decodeErr := mskit.DecodeDXT5RGBA(alt, 4, 4)
					if decodeErr == nil {
						var altPixels [64]byte
						copy(altPixels[:], altRGBA)
						aa, av := visibleDiff(block, altPixels)
						if (aa <= maxAlpha && av <= maxVisible) ||
							(aa <= a && av < v) || (aa < a && av <= v) {
							enc, a, v = alt, aa, av
							report.BC3FallbackSelected++
						}
					}
				}
			}
			// legacy: bounded palette-minimax only for BC3 blocks that still fail.
			// The compressed alpha bytes are kept exact; independent validation
			// remains mandatory, and the quality guard is NEVER bypassed.
			if meta.Format == "DXT5" && (a > maxAlpha || v > maxVisible) {
				report.BC3PaletteAttempts++
				better, aa, vv, improveErr := MinimaxRGBBC3AlphaPreserving(block, enc)
				if improveErr != nil {
					return nil, report, fmt.Errorf("BC3 palette candidate (%d,%d): %w", bx, by, improveErr)
				}
				if aa <= a && vv < v {
					enc, a, v = better, aa, vv
					report.BC3PaletteSelected++
				}
			}
			// legacy: bounded multistart minimax is a fourth-stage fallback only.
			// Independently decode/measure, never bypass quality guards.
			if meta.Format == "DXT5" && (a > maxAlpha || v > maxVisible) {
				report.BC3WideAttempts++
				wide, aa, vv, wideErr := MinimaxRGBBC3ExpandedSearch(block, enc)
				if wideErr != nil {
					return nil, report, fmt.Errorf("BC3 wide minimax candidate (%d,%d): %w", bx, by, wideErr)
				}
				if aa <= a && vv < v {
					enc, a, v = wide, aa, vv
					report.BC3WideSelected++
				}
			}
			// Joint BC3 alpha-index and RGB565 palette search, only
			// after the earlier strictly measured encoders still fail.
			if meta.Format == "DXT5" && (a > maxAlpha || v > maxVisible) {
				report.BC3JointAttempts++
				candidate, aa, vv, jointErr := MinimaxJointBC3(block, enc)
				if jointErr != nil {
					return nil, report, fmt.Errorf("BC3 joint candidate (%d,%d): %w", bx, by, jointErr)
				}
				if (aa <= maxAlpha && vv <= maxVisible) || (aa <= a && vv < v) || (aa < a && vv <= v) {
					enc, a, v = candidate, aa, vv
					report.BC3JointSelected++
				}
			}
			// legacy: only problematic BC7 blocks receive deterministic minimax
			// candidates. Every candidate is independently decoded against the
			// UNCHANGED target. Strict quality bounds are NEVER relaxed.
			if meta.Format == "BC7" && (a > maxAlpha || v > maxVisible) {
				report.BC7FallbackAttempts++
				better, attempts, fallbackErr := MinimaxBC7(block, enc, maxAlpha, maxVisible)
				report.BC7CandidateTrials += attempts
				if fallbackErr != nil {
					return nil, report, fmt.Errorf("BC7 minimax candidate (%d,%d): %w", bx, by, fallbackErr)
				}
				if better.a <= maxAlpha && better.v <= maxVisible {
					enc, a, v = better.encoded, better.a, better.v
					report.BC7FallbackSelected++
				}
			}
			if a > report.MaxAlphaError {
				report.MaxAlphaError = a
			}
			if v > report.MaxVisibleError {
				report.MaxVisibleError = v
			}
			if a > maxAlpha || v > maxVisible {
				return nil, report, fmt.Errorf("quality guard rejected block (%d,%d): alpha=%d visible=%d thresholds=%d/%d", bx, by, a, v, maxAlpha, maxVisible)
			}
			offset := (by*cols + bx) * 16
			copy(payload[offset:offset+16], enc)
			report.ChangedBlocks++
		}
	}
	report.EncodeMilliseconds = float64(time.Since(start).Microseconds()) / 1000
	out, e := mskit.RebuildTID(source, payload)
	if e != nil {
		return nil, report, e
	}
	if len(out) != len(source) || !bytes.Equal(out[:128], source[:128]) {
		return nil, report, errors.New("TID header or length drift")
	}
	for idx := 0; idx < report.TotalBlocks; idx++ {
		a, b := idx*16, idx*16+16
		if bytes.Equal(payload[a:b], source[128+a:128+b]) {
			continue
		}
		x, y := (idx%cols)*4, (idx/cols)*4
		changed := false
		for yy := y; yy < y+4 && yy < meta.Height && !changed; yy++ {
			for xx := x; xx < x+4 && xx < meta.Width; xx++ {
				at := (yy*meta.Width + xx) * 4
				if pxDiff(originalRGBA[at:at+4], target[at:at+4]) {
					changed = true
					break
				}
			}
		}
		if !changed {
			return nil, report, fmt.Errorf("untouched compressed block %d changed", idx)
		}
	}
	if meta.Format == "BC7" {
		_, e = mskit.DecodeBC7RGBA(out[128:], meta.Width, meta.Height)
	} else {
		_, e = mskit.DecodeDXT5RGBA(out[128:], meta.Width, meta.Height)
	}
	if e != nil {
		return nil, report, fmt.Errorf("re-parse %s: %w", meta.Format, e)
	}
	report.OutputSHA256 = digest(out)
	return out, report, nil
}

// Compatibility entry point for callers that specifically require BC7.
func EncodeChangedBC7(source, target []byte, quality, maxAlpha, maxVisible int) ([]byte, BC7Report, error) {
	meta, e := mskit.ParseTIDMeta(source)
	if e != nil {
		return nil, BC7Report{}, e
	}
	if meta.Format != "BC7" {
		return nil, BC7Report{}, fmt.Errorf("BC7-only API rejects %s", meta.Format)
	}
	return EncodeChangedTID(source, target, quality, maxAlpha, maxVisible)
}
