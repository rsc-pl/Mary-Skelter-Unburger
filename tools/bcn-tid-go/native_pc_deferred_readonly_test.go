package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/mskit"
)

// Genuine source TIDs, synthetic full-length GDAT envelopes. No fonts or
// rendered game output are required; validates the opt-in main PC bridge.
func TestDeferredPCBridgeAllNineteenRealSourceTIDsReadOnly(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mf struct {
		Assets []struct {
			ID       string `json:"id"`
			Dataset  string `json:"dataset"`
			Archive  int    `json:"archive"`
			Index    int    `json:"gdat_index"`
			Inner    string `json:"cl3_inner_name"`
			Variants []struct {
				SHA string `json:"tid_sha256"`
			} `json:"source_variants"`
			Targets []struct {
				Index int    `json:"gdat_index"`
				Inner string `json:"inner_name"`
				SHA   string `json:"tid_sha256"`
			} `json:"targets"`
		} `json:"assets"`
	}
	if err = json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	ids := []string{"MS2_CAMP_TOP2_CHARACTER_NAMES", "MS2_CAMP_TOP_LEGACY_NAMES",
		"MS2_BONUS_CHARACTER_NAMES", "MS2_JOB_CHARACTER_NAMES",
		"MSF_EN_BONUS_CHARACTER_NAMES", "MS2_BONUS_CHARACTER_SELECT_NAMES",
		"MSF_EN_BONUS_CHARACTER_SELECT_NAMES", "MSF_EN_HEROINE_ROOM_NAMES",
		"MSF_EN_CAMP_CHARACTER_NAMES", "MSF_EN_REN_BONUS_CHARACTER_NAMES",
		"MS2_HEROINE_ROOM_LIST_BLUE", "MS2_MYROOM_TITLE", "MS2_DLC_JOB_LABELS",
		"MSF_EN_FINALE_CHARACTER_GRID", "MS2_JOB_CLASS_24PX", "MS2_JOB_CLASS_MIDDLE",
		"MS2_KESSIKI_SKILL_LABELS", "MS2_RESULT_NAMES", "MS2_BATTLE_NAMES"}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	source := map[string][]byte{}
	if err = filepath.WalkDir(filepath.Join(root, ".local", "review", "graphics"), func(path string, d os.DirEntry, er error) error {
		if er != nil {
			return er
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tid") {
			return nil
		}
		b, er := os.ReadFile(path)
		if er != nil {
			return er
		}
		source[digest(b)] = b
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	groups := map[int][][]byte{55: make([][]byte, 1863), 73: make([][]byte, 225)}
	for num, entries := range groups {
		for i := range entries {
			entries[i] = []byte(fmt.Sprintf("UNRELATED_%d_%d", num, i))
		}
	}
	n := 0
	for _, a := range mf.Assets {
		if !selected[a.ID] {
			continue
		}
		n++
		if a.Archive != 55 && a.Archive != 73 {
			t.Fatalf("unexpected source archive %s %d", a.ID, a.Archive)
		}
		members := a.Targets
		if len(members) == 0 {
			if len(a.Variants) != 1 {
				t.Fatalf("missing source pin %s", a.ID)
			}
			members = append(members, struct {
				Index int    `json:"gdat_index"`
				Inner string `json:"inner_name"`
				SHA   string `json:"tid_sha256"`
			}{a.Index, a.Inner, a.Variants[0].SHA})
		}
		for _, m := range members {
			tid := source[m.SHA]
			if tid == nil {
				t.Fatalf("missing authentic source %s %s", a.ID, m.SHA)
			}
			cl := testCL3WithTID(t, tid)
			at := bytes.Index(cl, []byte("result_2.tid"))
			if at < 0 {
				t.Fatal("CL3 fixture lacks name")
			}
			clear(cl[at : at+mskit.CL3NameSize])
			copy(cl[at:], m.Inner)
			if m.Index >= len(groups[a.Archive]) {
				t.Fatalf("index exceeds fixture %s %d", a.ID, m.Index)
			}
			if !bytes.HasPrefix(groups[a.Archive][m.Index], []byte("UNRELATED_")) {
				t.Fatalf("two families alias index %d", m.Index)
			}
			groups[a.Archive][m.Index] = cl
		}
	}
	if n != 19 {
		t.Fatalf("got %d source families", n)
	}
	dir := t.TempDir()
	ms2 := filepath.Join(dir, "ms2", "masterWindows")
	finale := filepath.Join(dir, "finale", "masterWindows")
	for _, path := range []string{ms2, finale} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for n, entries := range groups {
		b, er := mskit.BuildGDAT(entries)
		if er != nil {
			t.Fatal(er)
		}
		dest := ms2
		if n == 73 {
			dest = finale
		}
		if er := os.WriteFile(filepath.Join(dest, fmt.Sprintf("GxArchivedFile%03d.dat", n)), b, 0600); er != nil {
			t.Fatal(er)
		}
	}
	opts := maintainer.NativeProvenPCGraphicsOptions{MS2Master: ms2, FinaleMaster: finale, ValidateOnly: true}
	defaultReport, er := maintainer.NativeProvenPCGraphics(root, opts)
	if er != nil || defaultReport.Families != 17 || len(defaultReport.Archives) != 2 {
		t.Fatalf("default %+v %v", defaultReport, er)
	}
	opts.IncludeDeferredNameBanks = true
	report, er := maintainer.NativeProvenPCGraphics(root, opts)
	if er != nil || report.Families != 19 || len(report.Archives) != 2 || report.Status != "PASS_READ_ONLY_ORIGINAL_SOURCE" {
		t.Fatalf("deferred %+v %v", report, er)
	}
	found := 0
	for _, archive := range report.Archives {
		for _, id := range archive.Families {
			if id == "MS2_RESULT_NAMES" || id == "MS2_BATTLE_NAMES" {
				found++
			}
		}
	}
	if found != 2 {
		t.Fatal("two namebanks not found in PC report")
	}
	// The previously authenticated TID must not be replaceable with a subtly
	// modified BC7 stream, even when the encompassing synthetic GDAT parses.
	data := groups[55]
	for _, a := range mf.Assets {
		if a.ID == "MS2_RESULT_NAMES" {
			damaged := append([]byte(nil), source[a.Variants[0].SHA]...)
			damaged[len(damaged)-1] ^= 1
			cl := testCL3WithTID(t, damaged)
			at := bytes.Index(cl, []byte("result_2.tid"))
			if at < 0 {
				t.Fatal("CL3 test fixture")
			}
			clear(cl[at : at+mskit.CL3NameSize])
			copy(cl[at:], a.Inner)
			data[a.Index] = cl
		}
	}
	modified, er := mskit.BuildGDAT(data)
	if er != nil {
		t.Fatal(er)
	}
	if er = os.WriteFile(filepath.Join(ms2, "GxArchivedFile055.dat"), modified, 0600); er != nil {
		t.Fatal(er)
	}
	_, er = maintainer.NativeProvenPCGraphics(root, opts)
	if er == nil || !strings.Contains(er.Error(), "stale/different original source TID SHA") {
		t.Fatalf("tampered original accepted: %v", er)
	}
}
