package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/mskit"
)

// Integration of the PUBLIC Go maintainer orchestration with the isolated,
// genuine offline Q6 Go codec on all eight SHA-pinned *original* TID bytes.
// Only the outer GDATs are synthetic; this is NOT full real master DAT QA.
func TestPCBuilderBridgeEightSourceFamilies(t *testing.T) {
	if os.Getenv("MS__REAL") != "1" {
		t.Skip("private original TIDs, opt-in")
	}
	bin := os.Getenv("MS__BIN")
	if bin == "" {
		t.Fatal("MS__BIN missing")
	}
	root, e := filepath.Abs(filepath.Join("..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	ms2 := filepath.Join(dir, "ms2", "masterWindows")
	finale := filepath.Join(dir, "finale", "masterWindows")
	for _, d := range []string{ms2, finale} {
		if e = os.MkdirAll(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	pristineBySHA := map[string][]byte{}
	e = filepath.WalkDir(filepath.Join(root, ".local"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".tid" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sha := digest(b)
		pristineBySHA[sha] = b
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	groups := []struct {
		master  string
		num     int
		ids     []string
		entries int
	}{
		{ms2, 55, []string{campTop2ID, legacyID, bonusWhiteID, jobWhiteID, ms2SelectID}, 1855},
		{finale, 73, []string{msfBonusID, msfSelectID, roomPinkID}, 223},
	}
	for _, group := range groups {
		entries := make([][]byte, group.entries)
		for i := range entries {
			entries[i] = []byte(fmt.Sprintf("UNRELATED__%d", i))
		}
		for _, id := range group.ids {
			a, e := func() (familyAsset, error) {
				switch id {
				case campTop2ID:
					v, _, _, err := loadCampTop2(root)
					return v, err
				case legacyID:
					v, _, _, err := loadLegacyFamily(root)
					return v, err
				case bonusWhiteID:
					v, _, _, err := readBonusWhite(root)
					return v, err
				case jobWhiteID:
					v, _, _, err := readJobWhite(root)
					return v, err
				case roomPinkID:
					v, _, _, err := loadRoomPink(root)
					return v, err
				default:
					v, _, _, err := readWhiteFamily(root, id)
					return v, err
				}
			}()
			if e != nil {
				t.Fatal(e)
			}
			tid := pristineBySHA[assetSourceHash(id)]
			if tid == nil {
				t.Fatalf("source %s missing from private corpus", id)
			}
			cl := testCL3WithTID(t, tid)
			pos := bytes.Index(cl, []byte("result_2.tid"))
			if pos < 0 {
				t.Fatal("CL3 test template missing")
			}
			clear(cl[pos : pos+mskit.CL3NameSize])
			copy(cl[pos:], a.Inner)
			entries[a.Index] = cl
		}
		g, e := mskit.BuildGDAT(entries)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(group.master, fmt.Sprintf("GxArchivedFile%03d.dat", group.num)), g, 0600); e != nil {
			t.Fatal(e)
		}
	}
	validate, e := maintainer.NativeProvenPCGraphics(root, maintainer.NativeProvenPCGraphicsOptions{MS2Master: ms2, FinaleMaster: finale, ValidateOnly: true})
	if e != nil || validate.Families != 8 || validate.Status != "PASS_READ_ONLY_ORIGINAL_SOURCE" {
		t.Fatalf("read-only bridge: %+v %v", validate, e)
	}
	t.Logf("legacy source preflight PASS: 8 original SHA-pinned TIDs across 2 synthetic full-length GDAT envelopes")
	if os.Getenv("MS__VALIDATE_ONLY") == "1" {
		return
	}
	out := filepath.Join(dir, "candidates")
	rep, e := maintainer.NativeProvenPCGraphics(root, maintainer.NativeProvenPCGraphicsOptions{MS2Master: ms2, FinaleMaster: finale, OutputDir: out, RendererBinary: bin})
	if e != nil {
		t.Fatal(e)
	}
	if rep.Status != "PASS_CANDIDATE_NOT_GAME_RUNTIME" || rep.Families != 8 || len(rep.Archives) != 2 {
		t.Fatalf("unexpected bridge report %+v", rep)
	}
	preserved := 0
	for _, a := range rep.Archives {
		b, e := os.ReadFile(a.Source)
		if e != nil {
			t.Fatal(e)
		}
		after, e := os.ReadFile(a.Output)
		if e != nil {
			t.Fatal(e)
		}
		beforeDat, e := mskit.ParseGDAT(b)
		if e != nil {
			t.Fatal(e)
		}
		afterDat, e := mskit.ParseGDAT(after)
		if e != nil {
			t.Fatal(e)
		}
		if len(beforeDat.Entries) != len(afterDat.Entries) {
			t.Fatal("GDAT record count changed")
		}
		changed := 0
		for i := range beforeDat.Entries {
			if bytes.Equal(beforeDat.Entries[i], afterDat.Entries[i]) {
				preserved++
			} else {
				changed++
			}
		}
		if changed != len(a.Families) {
			t.Fatalf("%s changed %d records instead of %d", a.Dataset, changed, len(a.Families))
		}
	}
	t.Logf("PC_BRIDGE_SOURCE_PASS source-backed TIDs=8 synthetic-original-envelope=2 unaffected-records=%d", preserved)
	// Reapplication cannot overwrite current candidate outputs.
	if _, e = maintainer.NativeProvenPCGraphics(root, maintainer.NativeProvenPCGraphicsOptions{MS2Master: ms2, FinaleMaster: finale, OutputDir: out, RendererBinary: bin}); e == nil {
		t.Fatal("existing candidates overwritten")
	}
	// Ensure the original outputs were still readable.
	for _, a := range rep.Archives {
		if _, e = os.ReadFile(a.Output); e != nil {
			t.Fatal(e)
		}
	}
}

func assetSourceHash(id string) string {
	switch id {
	case roomPinkID:
		return roomPinkSHA
	case campTop2ID:
		return campTop2TIDSHA
	case legacyID:
		return legacySHA
	case bonusWhiteID:
		return bonusWhiteSHA
	case jobWhiteID:
		return jobWhiteSHA
	case ms2SelectID, msfSelectID, msfBonusID:
		return whiteSpecs[id].sha
	}
	return ""
}
