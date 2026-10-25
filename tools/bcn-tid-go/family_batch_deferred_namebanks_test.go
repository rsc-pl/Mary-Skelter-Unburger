package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func TestDeferredNameBankGateDoesNotPromoteCampOrDefaultFamilies(t *testing.T) {
	mk := func(opt bool, alpha, visible int, family string) error {
		raw, err := json.Marshal(familyBatchSpec{Version: 1, ProjectRoot: ".", EnableDeferredNameBanks: opt, Jobs: []familyBatchJob{{Source: "in.gdat", Output: "out.gdat", SourceSHA256: strings.Repeat("a", 64), Families: []string{family}, Quality: 6, MaxAlpha: alpha, MaxVisible: visible}}})
		if err != nil {
			return err
		}
		_, err = parseFamilyBatch(raw)
		return err
	}
	if mk(false, 64, 64, "MS2_RESULT_NAMES") == nil {
		t.Fatal("Result silently promoted to existing production families")
	}
	if mk(false, 64, 64, "MS2_BATTLE_NAMES") == nil {
		t.Fatal("Battle silently promoted")
	}
	if mk(true, 64, 64, "MS2_RESULT_NAMES") != nil || mk(true, 64, 64, "MS2_BATTLE_NAMES") != nil {
		t.Fatal("approved-style explicit opt-in rejected")
	}
	if mk(true, 64, 64, "MS2_CAMP_CHARACTER_NAMES") == nil {
		t.Fatal("Camp BC3 HOLD bypassed")
	}
	if mk(true, 64, 65, "MS2_RESULT_NAMES") == nil || mk(true, 65, 64, "MS2_BATTLE_NAMES") == nil {
		t.Fatal("namebank strict quality relaxed")
	}
	if mk(true, 64, 64, "not-a-family") == nil {
		t.Fatal("unknown family passed")
	}
}

func TestDeferredNameBanksOneNativeFamilyGDATSourceLocked(t *testing.T) {
	base, src, nb, _ := makeNameBankFixture(t) // genuine private TIDs; only source-bound offline
	nb.Jobs[0].Families = []string{"MS2_RESULT_NAMES", "MS2_BATTLE_NAMES"}
	spec := familyBatchSpec{Version: 1, ProjectRoot: nb.ProjectRoot, EnableDeferredNameBanks: true,
		Jobs: []familyBatchJob{{Source: filepath.Base(src), Output: "rendered.gdat", SourceSHA256: nb.Jobs[0].SourceSHA256, Families: nb.Jobs[0].Families, Quality: 6, MaxAlpha: 64, MaxVisible: 64}}}
	payload, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "combined.json")
	if err = os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fake := func(root, id string, gdat []byte) (deferredNameBankSource, string, []byte, error) {
		s, e := sourceDeferredNameBank(root, id, gdat)
		if e != nil {
			return s, "", nil, e
		}
		calls++
		rgba, e := syntheticBankRaster(root, id, s.source)
		return s, "qa-injected-without-private-font", rgba, e
	}
	report, err := runSourceFamilyBatchWithDeferred(path, nil, fake)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status == "" || len(report.Assets) != 2 || calls != 2 || len(report.GDAT.Jobs) != 1 || len(report.GDAT.Jobs[0].TIDs) != 2 {
		t.Fatalf("unexpected bridge result %+v calls %d", report, calls)
	}
	original, _ := os.ReadFile(src)
	out, err := os.ReadFile(filepath.Join(base, "rendered.gdat"))
	if err != nil {
		t.Fatal(err)
	}
	a, e := mskit.ParseGDAT(original)
	if e != nil {
		t.Fatal(e)
	}
	b, e := mskit.ParseGDAT(out)
	if e != nil {
		t.Fatal(e)
	}
	if len(a.Entries) != 1858 || len(b.Entries) != 1858 {
		t.Fatal("wrong synthetic source record count")
	}
	for i := range a.Entries {
		if i != 1331 && i != 1333 && !bytes.Equal(a.Entries[i], b.Entries[i]) {
			t.Fatalf("unrelated record changed: %d", i)
		}
	}
	if bytes.Equal(a.Entries[1331], b.Entries[1331]) || bytes.Equal(a.Entries[1333], b.Entries[1333]) {
		t.Fatal("one of the deferred names did not change")
	}
	// A stale output is a preflight failure, before a single renderer runs.
	calls = 0
	if _, err = runSourceFamilyBatchWithDeferred(path, nil, fake); err == nil || calls != 0 {
		t.Fatalf("unsafe repeated publication: calls=%d err=%v", calls, err)
	}
}

func TestMixedMS2ResultBattleAndFinaleGridAtomicTwoGDATs(t *testing.T) {
	base, ms2Path, nb, _ := makeNameBankFixture(t)
	root := nb.ProjectRoot
	finalPath, err := privateassets.Resolve(root, "source-finale-character-grid")
	if err != nil {
		t.Fatal(err)
	}
	finalTID, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if digest(finalTID) != finaleGridSHA {
		t.Fatal("original Finale grid source drift")
	}
	data := make([][]byte, 222)
	for i := range data {
		data[i] = []byte{byte(i), byte(i >> 8), 'F', 'I', 'N'}
	}
	cl3 := testCL3WithTID(t, finalTID)
	off := 0xD0 + mskit.CL3EntrySize
	for i := 0; i < mskit.CL3NameSize; i++ {
		cl3[off+i] = 0
	}
	copy(cl3[off:off+mskit.CL3NameSize], "bonus_character_f.tid")
	data[71] = cl3
	finaleGDAT, err := mskit.BuildGDAT(data)
	if err != nil {
		t.Fatal(err)
	}
	finalePath := filepath.Join(base, "finale.gdat")
	if err = os.WriteFile(finalePath, finaleGDAT, 0600); err != nil {
		t.Fatal(err)
	}
	spec := familyBatchSpec{Version: 1, ProjectRoot: root, EnableDeferredNameBanks: true, Jobs: []familyBatchJob{
		{Source: filepath.Base(ms2Path), SourceSHA256: nb.Jobs[0].SourceSHA256, Output: "ms2_out.gdat", Families: []string{"MS2_RESULT_NAMES", "MS2_BATTLE_NAMES"}, Quality: 6, MaxAlpha: 64, MaxVisible: 64},
		{Source: filepath.Base(finalePath), SourceSHA256: digest(finaleGDAT), Output: "finale_out.gdat", Families: []string{finaleGridID}, Quality: 6, MaxAlpha: 64, MaxVisible: 64},
	}}
	payload, _ := json.Marshal(spec)
	path := filepath.Join(base, "mixed.json")
	if err = os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	fake := func(root, id string, gdat []byte) (deferredNameBankSource, string, []byte, error) {
		s, e := sourceDeferredNameBank(root, id, gdat)
		if e != nil {
			return s, "", nil, e
		}
		rgba, e := syntheticBankRaster(root, id, s.source)
		return s, "qa-only", rgba, e
	}
	_, err = runSourceFamilyBatchWithDeferred(path, func(i int) error {
		if i == 1 {
			return fmt.Errorf("injected failure on second GDAT output")
		}
		return nil
	}, fake)
	if err == nil {
		t.Fatal("second GDAT publication failure not propagated")
	}
	for _, p := range []string{"ms2_out.gdat", "finale_out.gdat"} {
		if _, e := os.Lstat(filepath.Join(base, p)); !os.IsNotExist(e) {
			t.Fatalf("rollback left %s: %v", p, e)
		}
	}
	report, err := runSourceFamilyBatchWithDeferred(path, nil, fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Assets) != 3 || len(report.GDAT.Jobs) != 2 {
		t.Fatalf("unexpected cross-title batch: %+v", report)
	}
	originals := []string{ms2Path, finalePath}
	output := []string{"ms2_out.gdat", "finale_out.gdat"}
	edited := [][]int{{1331, 1333}, {71}}
	for j := range originals {
		aRaw, _ := os.ReadFile(originals[j])
		bRaw, _ := os.ReadFile(filepath.Join(base, output[j]))
		a, e := mskit.ParseGDAT(aRaw)
		if e != nil {
			t.Fatal(e)
		}
		b, e := mskit.ParseGDAT(bRaw)
		if e != nil {
			t.Fatal(e)
		}
		expected := map[int]bool{}
		for _, idx := range edited[j] {
			expected[idx] = true
		}
		for k := range a.Entries {
			if !expected[k] && !bytes.Equal(a.Entries[k], b.Entries[k]) {
				t.Fatalf("job %d unrelated GDAT record %d changed", j, k)
			}
		}
	}
}

func TestDeferredNameBankTamperedOriginalRejectedBeforeRaster(t *testing.T) {
	_, src, nb, _ := makeNameBankFixture(t)
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDeferredNameBank(nb.ProjectRoot, "MS2_RESULT_NAMES", raw); err != nil {
		t.Fatalf("genuine original rejected: %v", err)
	}
	original, _, err := mskit.ExtractGraphicsTID(raw, 1333, "result_2.tid")
	if err != nil {
		t.Fatal(err)
	}
	modified := append([]byte(nil), original...)
	modified[130] ^= 0x01 // legal-size mutation; even a single changed source byte is forbidden
	altered, _, err := mskit.PatchGraphicsTIDGoSafe(raw, 1333, "result_2.tid", modified)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sourceDeferredNameBank(nb.ProjectRoot, "MS2_RESULT_NAMES", altered); err == nil || !strings.Contains(err.Error(), "SHA drift") {
		t.Fatalf("unexpected acceptance of tampered original TID: %v", err)
	}
}
