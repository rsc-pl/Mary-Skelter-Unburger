package maintainer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func mergeGDAT(t *testing.T, values ...string) []byte {
	t.Helper()
	var entries [][]byte
	for _, v := range values {
		entries = append(entries, []byte(v))
	}
	raw, err := mskit.BuildGDAT(entries)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestMergeDistinctEntriesAndIdempotentEvidence(t *testing.T) {
	orig := mergeGDAT(t, "source-zero", "one", "two", "three", "four")
	left := mergeGDAT(t, "source-zero", "text", "two", "three", "four")
	right := mergeGDAT(t, "source-zero", "one", "two", "graphic", "four")
	merged, r, err := MergePCGDATVariants(orig, [][]byte{left, right}, mergeSHA(orig))
	if err != nil {
		t.Fatal(err)
	}
	want := mergeGDAT(t, "source-zero", "text", "two", "graphic", "four")
	if !bytes.Equal(merged, want) || r.TotalEntries != 5 || r.UnchangedEntries != 3 || len(r.ChangedEntries) != 2 || r.OutputSHA256 != mergeSHA(merged) {
		t.Fatalf("merge mismatch: %+v", r)
	}
	again, r2, err := MergePCGDATVariants(orig, [][]byte{right, left}, mergeSHA(orig))
	if err != nil || !bytes.Equal(again, merged) || len(r2.ChangedEntries) != 2 {
		t.Fatalf("order dependence: %v", err)
	}
	// Duplicate identical new entries are allowed, never counted as a new edit.
	both := mergeGDAT(t, "source-zero", "text", "two", "graphic", "four")
	_, r3, err := MergePCGDATVariants(orig, [][]byte{left, both}, mergeSHA(orig))
	if err != nil || len(r3.DuplicateIdenticalEntries) != 1 || r3.DuplicateIdenticalEntries[0] != 1 {
		t.Fatalf("duplicate: %+v %v", r3, err)
	}
}
func TestMergeRejectsConflictAndBadSource(t *testing.T) {
	orig := mergeGDAT(t, "one", "two", "three")
	a := mergeGDAT(t, "A", "two", "three")
	b := mergeGDAT(t, "B", "two", "three")
	for _, tc := range []struct {
		name     string
		source   []byte
		variants [][]byte
		pin      string
	}{
		{"conflict", orig, [][]byte{a, b}, mergeSHA(orig)},
		{"invalid pin", orig, [][]byte{a, a}, strings.Repeat("0", 64)},
		{"empty pin", orig, [][]byte{a, a}, ""},
		{"no-op", orig, [][]byte{a, orig}, mergeSHA(orig)},
		{"corrupt candidate", orig, [][]byte{a, []byte("junk")}, mergeSHA(orig)},
		{"different entry count", orig, [][]byte{a, mergeGDAT(t, "A", "two")}, mergeSHA(orig)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, _, err := MergePCGDATVariants(tc.source, tc.variants, tc.pin); err == nil || out != nil {
				t.Fatalf("accepted %s", tc.name)
			}
		})
	}
}
func TestBundleCombinesDisjointDatasetDAT(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, ".local", "build")
	source := mergeGDAT(t, "pristine0", "pristine1", "pristine2", "pristine3")
	filename := "GxArchivedFile055.dat"
	pristineDir := filepath.Join(root, ".local", "source_cache", "pristine", "finale", "masterWindows")
	if err := os.MkdirAll(pristineDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pristineDir, filename), source, 0644); err != nil {
		t.Fatal(err)
	}
	expectedDir := filepath.Join(root, "toolchain", "manifests")
	if err := os.MkdirAll(expectedDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("dataset\tfile\tbytes\tsha256\nMSF_PC_LOCALIZED\t%s\t%d\t%s\n", filename, len(source), mergeSHA(source))
	if err := os.WriteFile(filepath.Join(expectedDir, "expected_pc_archive_sha256.tsv"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	writeJSONTest(t, filepath.Join(build, "change_manifest.json"), map[string]any{"MSF_PC_LOCALIZED": map[string]any{"changed_fields": 2}, "MSF_PC_SYSTEM": map[string]any{"changed_fields": 1}})
	for _, ds := range []string{"MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"} {
		writeJSONTest(t, filepath.Join(build, "reports", ds+".rebuild.json"), map[string]any{"archives": []map[string]any{{"archive_number": 55, "byte_identical": false}}})
		dir := filepath.Join(build, "rebuilt", ds, "masterWindows")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		one := mergeGDAT(t, "text", "pristine1", "pristine2", "pristine3")
		if ds == "MSF_PC_SYSTEM" {
			one = mergeGDAT(t, "pristine0", "pristine1", "graphic", "pristine3")
		}
		if err := os.WriteFile(filepath.Join(dir, filename), one, 0644); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(root, "build-dist")
	if err := BuildPatchBundles(root, dst, nil); err != nil {
		t.Fatal(err)
	}
	final, err := os.ReadFile(filepath.Join(dst, "MSF_EN_FIDELITY_PATCH", "masterWindows", filename))
	if err != nil {
		t.Fatal(err)
	}
	want := mergeGDAT(t, "text", "pristine1", "graphic", "pristine3")
	if !bytes.Equal(final, want) {
		t.Fatalf("incorrect merged actual production bundle")
	}
	// A stale pristine cache, even with valid-looking candidates, must stop.
	if err := os.WriteFile(filepath.Join(pristineDir, filename), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := BuildPatchBundles(root, filepath.Join(root, "refuse"), nil); err == nil {
		t.Fatal("stale source accepted")
	}
}
func TestBundleRefusesConflictingSameEntry(t *testing.T) {
	orig := mergeGDAT(t, "source", "unchanged")
	a := mergeGDAT(t, "A", "unchanged")
	b := mergeGDAT(t, "B", "unchanged")
	if _, _, err := MergePCGDATVariants(orig, [][]byte{a, b}, mergeSHA(orig)); err == nil {
		t.Fatal("lost one of two edits")
	}
}
