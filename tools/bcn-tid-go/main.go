package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"

	"maryskelter-unburger/internal/mskit"
)

func check(path, sha string) ([]byte, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != sha {
		return nil, fmt.Errorf("SHA256 mismatch: %s", path)
	}
	return b, nil
}
func execute(args []string) (BC7Report, error) {
	var result BC7Report
	f := flag.NewFlagSet("bcn-tid-go", flag.ContinueOnError)
	src := f.String("source-tid", "", "pristine BC7 or DXT5 TID")
	target := f.String("target-png", "", "rendered complete RGBA image")
	output := f.String("out", "", "new TID, must not exist")
	oldHash := f.String("source-sha256", "", "expected original TID SHA256")
	pngHash := f.String("png-sha256", "", "expected target PNG SHA256")
	quality := f.Int("quality", 6, "BC7 quality level 1..10")
	maxAlpha := f.Int("max-alpha", 64, "reject an edited block above this alpha error")
	maxVisible := f.Int("max-visible", 64, "reject an edited block above this visible-channel error")
	if err := f.Parse(args); err != nil {
		return result, err
	}
	if f.NArg() != 0 || *src == "" || *target == "" || *output == "" || len(*oldHash) != 64 || len(*pngHash) != 64 {
		return result, errors.New("require --source-tid --target-png --out --source-sha256 --png-sha256")
	}
	for _, path := range []string{*src, *target} {
		same, _ := filepath.Abs(path)
		dst, _ := filepath.Abs(*output)
		if same == dst {
			return result, errors.New("cannot overwrite inputs")
		}
	}
	if _, e := os.Lstat(*output); e == nil {
		return result, errors.New("output already exists")
	} else if !os.IsNotExist(e) {
		return result, e
	}
	old, e := check(*src, *oldHash)
	if e != nil {
		return result, e
	}
	pngData, e := check(*target, *pngHash)
	if e != nil {
		return result, e
	}
	meta, e := mskit.ParseTIDMeta(old)
	if e != nil {
		return result, e
	}
	// PNG should contain exact RGBA channel values. image/png.Decode can return
	// NRGBA or RGBA, so transform through non-premultiplied color.NRGBAModel.
	pic, format, e := image.Decode(bytes.NewReader(pngData))
	if e != nil {
		return result, e
	}
	if format != "png" {
		return result, errors.New("target must be PNG")
	}
	bounds := pic.Bounds()
	if bounds.Dx() != meta.Width || bounds.Dy() != meta.Height {
		return result, errors.New("PNG dimension mismatch")
	}
	rgba := make([]byte, meta.Width*meta.Height*4)
	for y := 0; y < meta.Height; y++ {
		for x := 0; x < meta.Width; x++ {
			v := color.NRGBAModel.Convert(pic.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			i := (y*meta.Width + x) * 4
			rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = v.R, v.G, v.B, v.A
		}
	}
	patched, rep, e := EncodeChangedTID(old, rgba, *quality, *maxAlpha, *maxVisible)
	if e != nil {
		return result, e
	}
	// hard-link commit prevents a concurrent writer from being overwritten. Temp
	// stage is removed on every error and source inputs are never modified.
	tmp, e := os.CreateTemp(filepath.Dir(*output), ".bcn-tid-*.tmp")
	if e != nil {
		return result, e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(patched); e != nil {
		tmp.Close()
		return result, e
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		return result, e
	}
	if e = tmp.Close(); e != nil {
		return result, e
	}
	if e = os.Link(tmp.Name(), *output); e != nil {
		return result, e
	}
	return rep, nil
}
func main() {
	if len(os.Args) == 3 && os.Args[1] == "--r32c3-triad-research-manifest" {
		r, err := runTriadBatch(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "R32C3_TRIAD_RESEARCH_FAIL:", err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--name-bank-batch-manifest" {
		r, err := RunNameFamilyBatch(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "NAME_BANK_BATCH_FAIL:", err)
			os.Exit(1)
		}
		if err = json.NewEncoder(os.Stdout).Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) == 3 && os.Args[1] == "--family-batch-manifest" {
		r, err := RunSourceFamilyBatch(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "FAMILY_BATCH_FAIL:", err)
			os.Exit(1)
		}
		if err = json.NewEncoder(os.Stdout).Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--camp-legacy-gdat-manifest" {
		r, err := executeLegacyFamilyGDAT(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "CAMP_LEGACY_GDAT_FAIL:", err)
			os.Exit(1)
		}
		if err = json.NewEncoder(os.Stdout).Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--camp-top2-gdat-manifest" {
		r, err := executeCampFamilyGDAT(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "CAMP_TOP2_GDAT_FAIL:", err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--gdat-rgba-manifest" {
		r, err := RunGDATRGBABatch(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "GDAT_RGBA_BATCH_FAIL:", err)
			os.Exit(1)
		}
		if err = json.NewEncoder(os.Stdout).Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--gdat-manifest" {
		r, err := RunGDATPNGBatch(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "GDAT_BATCH_FAIL:", err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	r, e := execute(os.Args[1:])
	if e != nil {
		fmt.Fprintln(os.Stderr, "BC7_FAIL:", e)
		os.Exit(1)
	}
	if e = json.NewEncoder(os.Stdout).Encode(r); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
