package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"maryskelter-unburger/internal/mskit"
)

type graphicsDXT5CLIReport struct {
	Status            string               `json:"status"`
	Width             int                  `json:"width"`
	Height            int                  `json:"height"`
	ChangedBlocks     []mskit.TextureBlock `json:"changed_blocks"`
	ChangedBlockCount int                  `json:"changed_block_count"`
	PayloadSHA256     string               `json:"payload_sha256"`
}

func fileBytes(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}

func bytesSHA256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func runGraphicsEncodeDXT5(root string, args []string) {
	fs := flag.NewFlagSet("graphics-encode-dxt5", flag.ExitOnError)
	sourceTID := fs.String("source-tid", "", "source DXT5 TID path")
	originalRGBA := fs.String("original-rgba", "", "decoded source RGBA path")
	renderedRGBA := fs.String("rendered-rgba", "", "rendered RGBA path")
	out := fs.String("out", "", "output DXT5 payload path")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *sourceTID == "" || *originalRGBA == "" || *renderedRGBA == "" || *out == "" {
		fatal(fmt.Errorf("--source-tid, --original-rgba, --rendered-rgba and --out are required"))
	}

	tid, err := fileBytes(*sourceTID)
	must(err)
	meta, err := mskit.ParseTIDMeta(tid)
	must(err)
	if meta.Format != "DXT5" {
		fatal(fmt.Errorf("source TID format is %s, expected DXT5", meta.Format))
	}
	expectedPayload, err := mskit.TexturePayloadSize(meta.Width, meta.Height)
	must(err)
	if len(tid) != 0x80+expectedPayload {
		fatal(fmt.Errorf("source TID payload size mismatch: %d != %d", len(tid)-0x80, expectedPayload))
	}
	sourcePayload := tid[0x80:]
	original, err := fileBytes(*originalRGBA)
	must(err)
	rendered, err := fileBytes(*renderedRGBA)
	must(err)
	payload, changed, err := mskit.EncodeDXT5RGBA(rendered, sourcePayload, original, meta.Width, meta.Height)
	must(err)
	if err := os.WriteFile(*out, payload, 0o644); err != nil {
		fatal(fmt.Errorf("write DXT5 payload: %w", err))
	}
	rep := graphicsDXT5CLIReport{
		Status: "PASS", Width: meta.Width, Height: meta.Height,
		ChangedBlocks: changed, ChangedBlockCount: len(changed), PayloadSHA256: bytesSHA256(payload),
	}
	raw, err := json.Marshal(rep)
	must(err)
	fmt.Println(string(raw))
}

type graphicsBC7CLIReport struct {
	Status            string               `json:"status"`
	Width             int                  `json:"width"`
	Height            int                  `json:"height"`
	ChangedBlocks     []mskit.TextureBlock `json:"changed_blocks"`
	ChangedBlockCount int                  `json:"changed_block_count"`
	PayloadSHA256     string               `json:"payload_sha256"`
}

func runGraphicsSpliceBC7(root string, args []string) {
	fs := flag.NewFlagSet("graphics-splice-bc7", flag.ExitOnError)
	sourceTID := fs.String("source-tid", "", "source BC7 TID path")
	originalRGBA := fs.String("original-rgba", "", "decoded source RGBA path")
	renderedRGBA := fs.String("rendered-rgba", "", "rendered RGBA path")
	candidate := fs.String("candidate-bc7", "", "full candidate BC7 payload path")
	out := fs.String("out", "", "output spliced BC7 payload path")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *sourceTID == "" || *originalRGBA == "" || *renderedRGBA == "" || *candidate == "" || *out == "" {
		fatal(fmt.Errorf("--source-tid, --original-rgba, --rendered-rgba, --candidate-bc7 and --out are required"))
	}

	tid, err := fileBytes(*sourceTID)
	must(err)
	meta, err := mskit.ParseTIDMeta(tid)
	must(err)
	if meta.Format != "BC7" {
		fatal(fmt.Errorf("source TID format is %s, expected BC7", meta.Format))
	}
	expectedPayload, err := mskit.TexturePayloadSize(meta.Width, meta.Height)
	must(err)
	if len(tid) != 0x80+expectedPayload {
		fatal(fmt.Errorf("source TID payload size mismatch: %d != %d", len(tid)-0x80, expectedPayload))
	}
	sourcePayload := tid[0x80:]
	original, err := fileBytes(*originalRGBA)
	must(err)
	rendered, err := fileBytes(*renderedRGBA)
	must(err)
	cand, err := fileBytes(*candidate)
	must(err)
	if len(cand) != expectedPayload {
		fatal(fmt.Errorf("candidate BC7 payload size mismatch: %d != %d", len(cand), expectedPayload))
	}
	changed, err := mskit.ChangedTextureBlocksRGBA(original, rendered, meta.Width, meta.Height)
	must(err)
	payload, err := mskit.SpliceTextureBlocks(sourcePayload, cand, meta.Width, meta.Height, changed)
	must(err)
	if err := os.WriteFile(*out, payload, 0o644); err != nil {
		fatal(fmt.Errorf("write BC7 payload: %w", err))
	}
	rep := graphicsBC7CLIReport{
		Status: "PASS", Width: meta.Width, Height: meta.Height,
		ChangedBlocks: changed, ChangedBlockCount: len(changed), PayloadSHA256: bytesSHA256(payload),
	}
	raw, err := json.Marshal(rep)
	must(err)
	fmt.Println(string(raw))
}

// runGraphicsDecodeDXT5 writes native pixel data so the retained Pillow renderer
// can consume decoded source textures without invoking its DDS decoder.
func runGraphicsDecodeDXT5(root string, args []string) {
	fs := flag.NewFlagSet("graphics-decode-dxt5", flag.ExitOnError)
	sourceTID := fs.String("source-tid", "", "source DXT5 TID path")
	out := fs.String("out", "", "output top-down RGBA bytes")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *sourceTID == "" || *out == "" {
		fatal(fmt.Errorf("--source-tid and --out are required"))
	}
	tid, err := fileBytes(*sourceTID)
	must(err)
	rgba, meta, err := mskit.DecodeTIDDXT5RGBA(tid)
	must(err)
	must(os.WriteFile(*out, rgba, 0o644))
	rep := struct {
		Status     string `json:"status"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RGBAsha256 string `json:"rgba_sha256"`
	}{"PASS", meta.Width, meta.Height, bytesSHA256(rgba)}
	raw, err := json.Marshal(rep)
	must(err)
	fmt.Println(string(raw))
}

// runGraphicsTIDToDDS writes a validated DDS container (not decoded pixels).
// BC7 pixel decode still belongs to Pillow until independently ported.
func runGraphicsTIDToDDS(root string, args []string) {
	fs := flag.NewFlagSet("graphics-tid-to-dds", flag.ExitOnError)
	sourceTID := fs.String("source-tid", "", "original TID texture")
	out := fs.String("out", "", "DDS output for retained graphics renderer")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *sourceTID == "" || *out == "" {
		fatal(fmt.Errorf("--source-tid and --out are required"))
	}
	tid, err := fileBytes(*sourceTID)
	must(err)
	dds, meta, err := mskit.TIDToDDS(tid)
	must(err)
	must(os.WriteFile(*out, dds, 0o644))
	rep := struct {
		Status    string `json:"status"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Format    string `json:"format"`
		DDSSHA256 string `json:"dds_sha256"`
	}{"PASS", meta.Width, meta.Height, meta.Format, bytesSHA256(dds)}
	raw, err := json.Marshal(rep)
	must(err)
	fmt.Println(string(raw))
}

// runGraphicsDecodeBC7 is the native BC7/TID RGB+A decoder. It fails
// closed if the source contains any unverified BC7 mode (0/2).
func runGraphicsDecodeBC7(root string, args []string) {
	fs := flag.NewFlagSet("graphics-decode-bc7", flag.ExitOnError)
	sourceTID := fs.String("source-tid", "", "source BC7 TID path")
	out := fs.String("out", "", "output top-down RGBA bytes")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *sourceTID == "" || *out == "" {
		fatal(fmt.Errorf("--source-tid and --out are required"))
	}
	tid, err := fileBytes(*sourceTID)
	must(err)
	rgba, meta, err := mskit.DecodeTIDBC7RGBA(tid)
	must(err)
	must(os.WriteFile(*out, rgba, 0o644))
	rep := struct {
		Status     string `json:"status"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RGBAsha256 string `json:"rgba_sha256"`
	}{"PASS", meta.Width, meta.Height, bytesSHA256(rgba)}
	raw, err := json.Marshal(rep)
	must(err)
	fmt.Println(string(raw))
}

// runGraphicsDecodeLegacy decodes BC1/DXT1 or BC2/DXT3 source pixels in Go.
func runGraphicsDecodeLegacy(args []string, codec string) {
	fs := flag.NewFlagSet("graphics-decode-"+codec, flag.ExitOnError)
	src := fs.String("source-tid", "", "source TID")
	out := fs.String("out", "", "top-down RGBA bytes")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *src == "" || *out == "" {
		fatal(fmt.Errorf("--source-tid and --out are required"))
	}
	tid, err := fileBytes(*src)
	must(err)
	var rgba []byte
	var meta mskit.TIDMeta
	if codec == "dxt1" {
		rgba, meta, err = mskit.DecodeTIDDXT1RGBA(tid)
	} else {
		rgba, meta, err = mskit.DecodeTIDDXT3RGBA(tid)
	}
	must(err)
	must(os.WriteFile(*out, rgba, 0o644))
	rep := struct {
		Status     string `json:"status"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RGBAsha256 string `json:"rgba_sha256"`
	}{"PASS", meta.Width, meta.Height, bytesSHA256(rgba)}
	raw, err := json.Marshal(rep)
	must(err)
	fmt.Println(string(raw))
}
