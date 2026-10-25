package updater

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func syntheticGDAT(t *testing.T, entries ...[]byte) []byte {
	t.Helper()
	b, err := buildGDAT(entries)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGDATBuildRoundTripIdentity(t *testing.T) {
	src := syntheticGDAT(t, []byte("alpha"), []byte{0, 1, 2, 3}, []byte("omega"))
	view, err := parseGDAT(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := buildGDAT(view.Entries)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(src) {
		t.Fatal("copy-through GDAT rebuild was not byte-identical")
	}
}

func TestGDATPatchPreservesUnrelatedVariantEntries(t *testing.T) {
	root := t.TempDir()
	canonicalSource := filepath.Join(root, "source.dat")
	canonicalTarget := filepath.Join(root, "target.dat")
	variantSource := filepath.Join(root, "variant.dat")
	payload := filepath.Join(root, "payload")
	out := filepath.Join(root, "out.dat")

	os.WriteFile(canonicalSource, syntheticGDAT(t, []byte("keep-A"), []byte("old-managed"), []byte("keep-C")), 0o644)
	os.WriteFile(canonicalTarget, syntheticGDAT(t, []byte("keep-A"), []byte("new-managed-with-growth"), []byte("keep-C")), 0o644)
	// Different version: an unrelated entry changed, but the managed source entry is still compatible.
	os.WriteFile(variantSource, syntheticGDAT(t, []byte("variant-A"), []byte("old-managed"), []byte("keep-C")), 0o644)

	patch, err := BuildGDATPatch(canonicalSource, canonicalTarget, payload, "ms2", "masterWindows/GxArchivedFile055.dat")
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.ChangedEntries) != 1 || patch.ChangedEntries[0].Index != 1 {
		t.Fatalf("changed entries = %+v", patch.ChangedEntries)
	}
	if err := ApplyGDATPatch(variantSource, out, payload, patch); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	view, err := parseGDAT(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(view.Entries[0]) != "variant-A" {
		t.Fatalf("unrelated variant entry was overwritten: %q", view.Entries[0])
	}
	if string(view.Entries[1]) != "new-managed-with-growth" {
		t.Fatalf("managed entry not patched: %q", view.Entries[1])
	}
}

func TestGDATPatchRejectsManagedSourceDrift(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.dat")
	target := filepath.Join(root, "target.dat")
	drift := filepath.Join(root, "drift.dat")
	payload := filepath.Join(root, "payload")
	os.WriteFile(source, syntheticGDAT(t, []byte("a"), []byte("old")), 0o644)
	os.WriteFile(target, syntheticGDAT(t, []byte("a"), []byte("new")), 0o644)
	os.WriteFile(drift, syntheticGDAT(t, []byte("a"), []byte("different-source-version")), 0o644)
	patch, err := BuildGDATPatch(source, target, payload, "ms2", "masterWindows/GxArchivedFile055.dat")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyGDATPatch(drift, filepath.Join(root, "out.dat"), payload, patch); err == nil {
		t.Fatal("managed-entry source drift was accepted")
	}
}

func TestGDATPatchIdempotentOnPatchedEntry(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.dat")
	target := filepath.Join(root, "target.dat")
	payload := filepath.Join(root, "payload")
	out := filepath.Join(root, "out.dat")
	os.WriteFile(source, syntheticGDAT(t, []byte("a"), []byte("old")), 0o644)
	os.WriteFile(target, syntheticGDAT(t, []byte("a"), []byte("new")), 0o644)
	patch, err := BuildGDATPatch(source, target, payload, "ms2", "masterWindows/GxArchivedFile055.dat")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyGDATPatch(target, out, payload, patch); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	want, _ := os.ReadFile(target)
	if string(got) != string(want) {
		t.Fatal("idempotent apply changed canonical target")
	}
}

func TestGDATPatchParserRejectsGap(t *testing.T) {
	raw := syntheticGDAT(t, []byte("a"), []byte("b"))
	// Shift entry 1 forward by one byte to create a gap while staying in-bounds.
	p := 8 + 8
	off := binary.LittleEndian.Uint32(raw[p : p+4])
	binary.LittleEndian.PutUint32(raw[p:p+4], off+1)
	if _, err := parseGDAT(raw); err == nil {
		t.Fatal("GDAT gap was accepted")
	}
}

func TestBuildGDATPatchPayloadUsesMasterWindowsCacheLayout(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	bundles := filepath.Join(root, "bundles")
	out := filepath.Join(root, "out")

	oldProfiles := Profiles
	defer func() { Profiles = oldProfiles }()
	Profiles = map[string]Profile{
		"ms2": {Key: "ms2", DisplayName: "Fixture", ExeName: "fixture.exe", BundleDir: "MS2_EN_FIDELITY_PATCH"},
	}

	sourceDir := filepath.Join(cache, "pristine", "ms2", "masterWindows")
	bundleDir := filepath.Join(bundles, "MS2_EN_FIDELITY_PATCH", "masterWindows")
	os.MkdirAll(sourceDir, 0o755)
	os.MkdirAll(bundleDir, 0o755)
	name := "GxArchivedFile001.dat"
	os.WriteFile(filepath.Join(sourceDir, name), syntheticGDAT(t, []byte("same"), []byte("old")), 0o644)
	os.WriteFile(filepath.Join(bundleDir, name), syntheticGDAT(t, []byte("same"), []byte("new")), 0o644)
	manifestRaw, _ := json.Marshal(PatchManifest{Files: []string{name}})
	os.WriteFile(filepath.Join(bundles, "MS2_EN_FIDELITY_PATCH", "PATCH_MANIFEST.json"), manifestRaw, 0o644)
	os.WriteFile(filepath.Join(bundles, "MS2_EN_FIDELITY_PATCH", "fixture.exe"), []byte("unused"), 0o644)

	prior := strings.Repeat("a", 64)
	lineage := &ContentLineage{SchemaVersion: 1, GDAT: []GDATLineageItem{{
		GameKey: "ms2", Archive: "masterWindows/" + name, EntryIndex: 1, PriorSHA256: []string{prior},
	}}}
	manifest, err := BuildGDATPatchPayload(cache, bundles, out, []string{"ms2"}, lineage)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Archives) != 1 || len(manifest.Archives[0].ChangedEntries) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if got := manifest.Archives[0].ChangedEntries[0].AcceptedSourceSHA256; len(got) != 1 || got[0] != prior {
		t.Fatalf("lineage not embedded: %v", got)
	}
	if _, err := os.Stat(filepath.Join(out, "GDAT_PATCH_MANIFEST.json")); err != nil {
		t.Fatal(err)
	}
}

func TestGDATPatchAcceptsRecordedPriorModTarget(t *testing.T) {
	root := t.TempDir()
	pristine := filepath.Join(root, "pristine.dat")
	currentTarget := filepath.Join(root, "current.dat")
	priorInstall := filepath.Join(root, "prior.dat")
	payload := filepath.Join(root, "payload")
	out := filepath.Join(root, "out.dat")
	os.WriteFile(pristine, syntheticGDAT(t, []byte("keep"), []byte("shipping")), 0o644)
	os.WriteFile(currentTarget, syntheticGDAT(t, []byte("keep"), []byte("current-mod")), 0o644)
	os.WriteFile(priorInstall, syntheticGDAT(t, []byte("keep"), []byte("prior-mod")), 0o644)
	patch, err := BuildGDATPatch(pristine, currentTarget, payload, "ms2", "masterWindows/GxArchivedFile055.dat")
	if err != nil {
		t.Fatal(err)
	}
	priorView, _ := parseGDAT(mustReadTest(t, priorInstall))
	patch.ChangedEntries[0].AcceptedSourceSHA256 = []string{bytesSHA(priorView.Entries[1])}
	if err := ApplyGDATPatch(priorInstall, out, payload, patch); err != nil {
		t.Fatal(err)
	}
	outView, _ := parseGDAT(mustReadTest(t, out))
	if string(outView.Entries[1]) != "current-mod" {
		t.Fatalf("managed entry=%q", outView.Entries[1])
	}
}

func mustReadTest(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
