package main

// legacy read-only cross-family source corpus codec QA. It is opt-in and uses
// genuinely SHA-pinned TIDs, never fabricated replacement game sources.
// No original TID, archived GDAT, translations or public release is written.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

type corpusAudit struct {
	Assets          int `json:"assets"`
	SourceVariants  int `json:"source_variants"`
	MatchedVariants int `json:"matched_variants"`
	Families        []struct {
		ID      string   `json:"id"`
		Sources int      `json:"sources"`
		Found   int      `json:"found"`
		Matched []string `json:"matched"`
		Status  string   `json:"status"`
	} `json:"families"`
}
type canonicalGraphicsManifest struct {
	Assets []struct {
		ID             string `json:"id"`
		SourceVariants []struct {
			SHA string `json:"tid_sha256"`
		} `json:"source_variants"`
		Targets []struct {
			SHA string `json:"tid_sha256"`
		} `json:"targets"`
	} `json:"assets"`
}

func TestAllAuthenticSourceTexturesStrictCodec(t *testing.T) {
	if os.Getenv("MS__REAL_SOURCE_CORPUS") != "1" {
		t.Skip("opt-in: private SHA-pinned 41-TID texture corpus")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	coveragePath, err := privateassets.Resolve(root, "source-codec-coverage-report")
	if err != nil {
		t.Fatal(err)
	}
	auditRaw, err := os.ReadFile(coveragePath)
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var audit corpusAudit
	var canonical canonicalGraphicsManifest
	if err = json.Unmarshal(auditRaw, &audit); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(manifestRaw, &canonical); err != nil {
		t.Fatal(err)
	}
	guard := make(map[string]map[string]bool)
	for _, a := range canonical.Assets {
		hashes := make(map[string]bool)
		for _, s := range a.SourceVariants {
			hashes[strings.ToLower(s.SHA)] = true
		}
		for _, s := range a.Targets {
			hashes[strings.ToLower(s.SHA)] = true
		}
		guard[a.ID] = hashes
	}
	if audit.Assets != 31 || audit.SourceVariants != 46 || audit.MatchedVariants != 41 {
		t.Fatalf("the pinned legacy source census changed: %+v", audit)
	}
	limit := 41
	if raw := os.Getenv("MS__CODEC_LIMIT"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 41 {
			t.Fatal("invalid MS__CODEC_LIMIT")
		}
	}
	tested, noop, changed, bc7, bc3 := 0, 0, 0, 0, 0
	var alphaWorst, visibleWorst int
	for _, a := range audit.Families {
		if a.Found != len(a.Matched) || a.Found > a.Sources || a.Status == "source-complete" && a.Found != a.Sources {
			t.Fatalf("corrupt audit for %s", a.ID)
		}
		for _, rel := range a.Matched {
			if tested >= limit {
				break
			}
			path := filepath.Join(root, ".local", filepath.FromSlash(rel))
			fi, err := os.Lstat(path)
			if err != nil || !fi.Mode().IsRegular() {
				t.Fatalf("not a real TID regular file %s: %v", a.ID, err)
			}
			tid, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sha := digest(tid)
			if !guard[a.ID][sha] {
				t.Fatalf("%s source TID does not match canonical manifest SHA", a.ID)
			}
			meta, err := mskit.ParseTIDMeta(tid)
			if err != nil {
				t.Fatalf("%s TID metadata: %v", a.ID, err)
			}
			var rgba []byte
			switch meta.Format {
			case "BC7":
				bc7++
				rgba, err = mskit.DecodeBC7RGBA(tid[128:], meta.Width, meta.Height)
			case "DXT5":
				bc3++
				rgba, err = mskit.DecodeDXT5RGBA(tid[128:], meta.Width, meta.Height)
			default:
				t.Fatalf("%s unexpected texture codec %s", a.ID, meta.Format)
			}
			if err != nil {
				t.Fatalf("%s decode: %v", a.ID, err)
			}
			noEdit, nr, err := EncodeChangedTID(tid, rgba, 6, 64, 64)
			if err != nil || nr.ChangedBlocks != 0 || !bytes.Equal(noEdit, tid) {
				t.Fatalf("%s no-edit is not byte-identical: changed=%d err=%v", a.ID, nr.ChangedBlocks, err)
			}
			noop++
			// Exactly one 4x4 block is deliberately changed to a constant,
			// trivially representable opaque sample. This validates codec
			// wiring and unrelated-block preservation, not actual typography.
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					p := (y*meta.Width + x) * 4
					copy(rgba[p:p+4], []byte{199, 31, 177, 255})
				}
			}
			rebuilt, rep, err := EncodeChangedTID(tid, rgba, 6, 64, 64)
			if err != nil || rep.ChangedBlocks != 1 || rep.UnchangedBlocks+1 != rep.TotalBlocks {
				t.Fatalf("%s changed block: changed=%d unchanged=%d total=%d err=%v", a.ID, rep.ChangedBlocks, rep.UnchangedBlocks, rep.TotalBlocks, err)
			}
			if !bytes.Equal(rebuilt[:128], tid[:128]) || !bytes.Equal(rebuilt[144:], tid[144:]) {
				t.Fatalf("%s altered TID header or any of its other compressed blocks", a.ID)
			}
			if rep.MaxAlphaError > 64 || rep.MaxVisibleError > 64 || rep.SourceSHA256 != sha {
				t.Fatalf("%s quality/source guard drift %+v", a.ID, rep)
			}
			if rep.MaxAlphaError > alphaWorst {
				alphaWorst = rep.MaxAlphaError
			}
			if rep.MaxVisibleError > visibleWorst {
				visibleWorst = rep.MaxVisibleError
			}
			changed++
			tested++
			t.Logf("legacy FAMILY_CODEC_PASS %02d/41 id=%s format=%s size=%dx%d source=%s changed=1/%d maxAlpha=%d maxVisible=%d", tested, a.ID, meta.Format, meta.Width, meta.Height, sha, rep.TotalBlocks, rep.MaxAlphaError, rep.MaxVisibleError)
		}
	}
	if tested != limit || noop != tested || changed != tested || bc7+bc3 != tested {
		t.Fatal(fmt.Sprintf("source census incomplete: tested=%d noop=%d changed=%d BC7=%d DXT5=%d limit=%d", tested, noop, changed, bc7, bc3, limit))
	}
	t.Logf("legacy FINAL SOURCE CODEC PASS matched=%d/%d families=%d atlas BC7=%d DXT5=%d noop=%d changed=%d maxAlpha=%d maxVisible=%d limit64/64. Source only; NOT full rendered families/runtime.", tested, audit.SourceVariants, audit.Assets, bc7, bc3, noop, changed, alphaWorst, visibleWorst)
}
