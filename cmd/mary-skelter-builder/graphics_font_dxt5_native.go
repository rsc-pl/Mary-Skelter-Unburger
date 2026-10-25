package main

// opt-in native-Go font -> RGBA -> DXT5 -> TID pilot.
// This command intentionally requires a clean, source-derived background PNG
// for the edited rectangle. It never tries to erase old glyphs heuristically.
// The resulting TID is an intermediate for the independently SHA-pinned // graphics-batch-tid-go writer, NOT an approved production asset.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"maryskelter-unburger/internal/graphicsgo"
	"maryskelter-unburger/internal/mskit"
)

type fontTIDReport struct {
	Status                   string `json:"status"`
	Scope                    string `json:"scope"`
	OriginalSHA256           string `json:"original_sha256"`
	OutputSHA256             string `json:"output_sha256"`
	Width                    int    `json:"width"`
	Height                   int    `json:"height"`
	ChangedBlocks            int    `json:"changed_blocks"`
	AlphaChangedPixels       int    `json:"alpha_changed_pixels"`
	PixelsOutsideRectChanged int    `json:"pixels_outside_rect_changed"`
	CompressionNote          string `json:"compression_note"`
}

func SHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func ReadPinned(p, expected string) ([]byte, error) {
	if len(expected) != 64 {
		return nil, errors.New("mandatory 64-character source SHA-256")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if SHA(data) != strings.ToLower(expected) {
		return nil, fmt.Errorf("source SHA-256 mismatch: %s", p)
	}
	return data, nil
}
func Rectangle(s string) (image.Rectangle, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return image.Rectangle{}, errors.New("rect requires x,y,w,h")
	}
	n := make([]int, 4)
	for i, p := range parts {
		v, e := strconv.Atoi(p)
		if e != nil {
			return image.Rectangle{}, e
		}
		n[i] = v
	}
	if n[0] < 0 || n[1] < 0 || n[2] <= 0 || n[3] <= 0 || n[2] > 1024 || n[3] > 256 {
		return image.Rectangle{}, errors.New("unsafe rectangle size")
	}
	if n[0]%4 != 0 || n[1]%4 != 0 || n[2]%4 != 0 || n[3]%4 != 0 {
		return image.Rectangle{}, errors.New("source rectangle must align to complete 4x4 DXT5 blocks")
	}
	return image.Rect(n[0], n[1], n[0]+n[2], n[1]+n[3]), nil
}

// Image is intentionally an RGBA *color* overlay on an explicitly cleared crop.
// Alpha changes are measured BEFORE lossy DXT5 recompression. A margin guard
// refuses letters clipped or touching the edit boundary.
func Compose(srcRGBA []byte, w, h int, rect image.Rectangle, clean image.Image, mask *image.Alpha, ink color.NRGBA) ([]byte, int, error) {
	if w < 4 || h < 4 || w > 4096 || h > 4096 || len(srcRGBA) != w*h*4 || !rect.In(image.Rect(0, 0, w, h)) {
		return nil, 0, errors.New("source geometry mismatch")
	}
	cw, ch := rect.Dx(), rect.Dy()
	if clean == nil || clean.Bounds().Dx() != cw || clean.Bounds().Dy() != ch || mask == nil || mask.Rect.Dx() != cw || mask.Rect.Dy() != ch {
		return nil, 0, errors.New("clean crop or mask mismatch")
	}
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			if (x < 2 || y < 2 || x >= cw-2 || y >= ch-2) && mask.AlphaAt(mask.Rect.Min.X+x, mask.Rect.Min.Y+y).A > 0 {
				return nil, 0, errors.New("glyph touches two-pixel safety margin (would clip)")
			}
		}
	}
	output := append([]byte(nil), srcRGBA...)
	nAlpha := 0
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			off := ((rect.Min.Y+y)*w + rect.Min.X + x) * 4
			base := color.NRGBAModel.Convert(clean.At(clean.Bounds().Min.X+x, clean.Bounds().Min.Y+y)).(color.NRGBA)
			a := mask.AlphaAt(mask.Rect.Min.X+x, mask.Rect.Min.Y+y).A
			if a > 0 {
				v := ink
				v.A = uint8((uint32(ink.A)*uint32(a) + 127) / 255)
				base = graphicsgo.OverPixels(base, v)
			}
			if output[off+3] != base.A {
				nAlpha++
			}
			output[off], output[off+1], output[off+2], output[off+3] = base.R, base.G, base.B, base.A
		}
	}
	return output, nAlpha, nil
}
func CreateTID(tid []byte, region image.Rectangle, clean image.Image, mask *image.Alpha, ink color.NRGBA) ([]byte, *image.NRGBA, fontTIDReport, error) {
	rep := fontTIDReport{Scope: "opt-in DXT5 label pilot; NOT production/runtime approved", CompressionNote: "DXT5 4x4 blocks inside requested rectangle may differ due to lossy encoding"}
	meta, err := mskit.ParseTIDMeta(tid)
	if err != nil {
		return nil, nil, rep, err
	}
	if meta.Format != "DXT5" {
		return nil, nil, rep, fmt.Errorf("DXT5 only, got %s", meta.Format)
	}
	sz, err := mskit.TexturePayloadSize(meta.Width, meta.Height)
	if err != nil || len(tid) != 128+sz {
		return nil, nil, rep, errors.New("invalid DXT5 TID payload")
	}
	original, _, err := mskit.DecodeTIDDXT5RGBA(tid)
	if err != nil {
		return nil, nil, rep, err
	}
	rendered, alphaChanged, err := Compose(original, meta.Width, meta.Height, region, clean, mask, ink)
	if err != nil {
		return nil, nil, rep, err
	}
	payload, changed, err := mskit.EncodeDXT5RGBA(rendered, tid[128:], original, meta.Width, meta.Height)
	if err != nil {
		return nil, nil, rep, err
	}
	out, err := mskit.RebuildTID(tid, payload)
	if err != nil {
		return nil, nil, rep, err
	}
	// Enforce exact untouched source BLOCKS outside the permitted rectangle.
	bx := (meta.Width + 3) / 4
	for i := 0; i < len(payload)/16; i++ {
		x, y := (i%bx)*4, (i/bx)*4
		if !image.Pt(x, y).In(region) && !strings.EqualFold(hex.EncodeToString(payload[i*16:(i+1)*16]), hex.EncodeToString(tid[128+i*16:128+(i+1)*16])) {
			return nil, nil, rep, errors.New("compressed block outside target was changed")
		}
	}
	// Verify decoded output is loadable; preview depicts ACTUAL lossy TID output.
	decoded, _, err := mskit.DecodeTIDDXT5RGBA(out)
	if err != nil {
		return nil, nil, rep, err
	}
	preview := image.NewNRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
	for y := 0; y < region.Dy(); y++ {
		from := ((y+region.Min.Y)*meta.Width + region.Min.X) * 4
		copy(preview.Pix[y*preview.Stride:y*preview.Stride+region.Dx()*4], decoded[from:from+region.Dx()*4])
	}
	rep.Status = "PASS"
	rep.Width = meta.Width
	rep.Height = meta.Height
	rep.ChangedBlocks = len(changed)
	rep.AlphaChangedPixels = alphaChanged
	rep.OriginalSHA256 = SHA(tid)
	rep.OutputSHA256 = SHA(out)
	return out, preview, rep, nil
}
func PublishNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	success = true
	return nil
}
func executeRender(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("graphics-render-label-dxt5-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	orig := fs.String("source-tid", "", "original DXT5 TID")
	origHash := fs.String("source-sha256", "", "original TID sha256")
	font := fs.String("font", "", "original private SFNT font")
	fontHash := fs.String("font-sha256", "", "font sha256")
	cleanPath := fs.String("clean-png", "", "clean image for EXACT target rect; never auto erase letters")
	cleanHash := fs.String("clean-sha256", "", "clean PNG sha256")
	text := fs.String("text", "", "ASCII text")
	size := fs.Int("size", 30, "font pixel size")
	rect := fs.String("rect", "", "aligned edit rect x,y,w,h")
	x := fs.Int("x", 8, "baseline local x")
	y := fs.Int("y", 48, "baseline local y")
	dest := fs.String("out", "", "new output TID")
	preview := fs.String("preview", "", "new PNG preview (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *orig == "" || *font == "" || *cleanPath == "" || *dest == "" || *text == "" || *rect == "" {
		return errors.New("source, font, clean-png, SHA guards, text, rect, output required")
	}
	if filepath.Clean(*orig) == filepath.Clean(*dest) || filepath.Clean(*font) == filepath.Clean(*dest) || filepath.Clean(*cleanPath) == filepath.Clean(*dest) {
		return errors.New("in-place output refused")
	}
	if _, err := os.Lstat(*dest); err == nil {
		return errors.New("TID output already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if *preview != "" {
		if filepath.Clean(*preview) == filepath.Clean(*dest) || filepath.Clean(*preview) == filepath.Clean(*orig) {
			return errors.New("preview/output collides with input")
		}
		if _, err := os.Lstat(*preview); err == nil {
			return errors.New("preview already exists")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	region, err := Rectangle(*rect)
	if err != nil {
		return err
	}
	tid, err := ReadPinned(*orig, *origHash)
	if err != nil {
		return err
	}
	fontBytes, err := ReadPinned(*font, *fontHash)
	if err != nil {
		return err
	}
	cleanBytes, err := ReadPinned(*cleanPath, *cleanHash)
	if err != nil {
		return err
	}
	c, err := png.Decode(strings.NewReader(string(cleanBytes)))
	if err != nil {
		return fmt.Errorf("clean background not PNG: %w", err)
	}
	sfnt, err := graphicsgo.ParseSFNT(fontBytes)
	if err != nil {
		return err
	}
	mask, err := sfnt.RasterLatinRunSupersampled(*text, *size, 4, 4, region.Dx(), region.Dy(), *x, *y)
	if err != nil {
		return err
	}
	replacement, img, rep, err := CreateTID(tid, region, c, mask, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	if err != nil {
		return err
	}
	if *preview != "" {
		var buf strings.Builder // native encoded PNG, capped by edit size
		if err = png.Encode(&buf, img); err != nil {
			return err
		}
		if err = PublishNew(*preview, []byte(buf.String())); err != nil {
			return err
		}
	}
	if err = PublishNew(*dest, replacement); err != nil {
		if *preview != "" {
			os.Remove(*preview)
		}
		return err
	}
	return json.NewEncoder(out).Encode(rep)
}
func runRender(args []string) {
	if err := executeRender(args, os.Stdout); err != nil {
		fatal(err)
	}
}
