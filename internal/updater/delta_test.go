package updater

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDeltaRoundTripWithShiftedContent(t *testing.T) {
	td := t.TempDir()
	src := filepath.Join(td, "source.dat")
	dst := filepath.Join(td, "target.dat")
	patch := filepath.Join(td, "patch.msdelta")
	out := filepath.Join(td, "out.dat")

	blockA := bytes.Repeat([]byte("AAAA0123456789"), 20000)
	blockB := bytes.Repeat([]byte("BBBBabcdefghij"), 20000)
	blockC := bytes.Repeat([]byte("CCCCklmnopqrst"), 20000)
	source := append(append(append([]byte{}, blockA...), blockB...), blockC...)
	target := append([]byte{}, blockA...)
	target = append(target, []byte("inserted content that shifts everything after it\n")...)
	target = append(target, blockB...)
	target = append(target, []byte("small changed region")...)
	target = append(target, blockC...)
	if err := os.WriteFile(src, source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, target, 0o644); err != nil {
		t.Fatal(err)
	}

	h, stats, err := BuildDelta(src, dst, patch)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CopiedBytes == 0 || stats.LiteralBytes == 0 {
		t.Fatalf("expected mixed copy/literal delta: %+v", stats)
	}
	if stats.PatchBytes >= int64(len(target)) {
		t.Fatalf("delta not compact: patch=%d target=%d", stats.PatchBytes, len(target))
	}
	gotH, err := ApplyDelta(src, patch, out)
	if err != nil {
		t.Fatal(err)
	}
	if gotH.TargetSHA256 != h.TargetSHA256 {
		t.Fatalf("header mismatch")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, target) {
		t.Fatal("delta roundtrip mismatch")
	}
}

func TestDeltaRejectsWrongSource(t *testing.T) {
	td := t.TempDir()
	src := filepath.Join(td, "source")
	dst := filepath.Join(td, "target")
	patch := filepath.Join(td, "patch")
	out := filepath.Join(td, "out")
	os.WriteFile(src, bytes.Repeat([]byte("source"), 50000), 0o644)
	os.WriteFile(dst, bytes.Repeat([]byte("target"), 50000), 0o644)
	if _, _, err := BuildDelta(src, dst, patch); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(src, []byte("wrong"), 0o644)
	if _, err := ApplyDelta(src, patch, out); err == nil {
		t.Fatal("expected source identity failure")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("output survived failed apply: %v", err)
	}
}

func TestBuildDeltaPayloadManifest(t *testing.T) {
	td := t.TempDir()
	cache := filepath.Join(td, "cache")
	dist := filepath.Join(td, "dist")
	out := filepath.Join(td, "payload")
	p := Profile{Key: "testdelta", DisplayName: "Test", BundleDir: "TEST_PATCH"}
	old, had := Profiles[p.Key]
	Profiles[p.Key] = p
	defer func() {
		if had {
			Profiles[p.Key] = old
		} else {
			delete(Profiles, p.Key)
		}
	}()

	name := "GxArchivedFile001.dat"
	source := bytes.Repeat([]byte("same same same 1234567890\n"), 20000)
	target := append([]byte{}, source...)
	copy(target[150000:150050], bytes.Repeat([]byte{0x5a}, 50))
	write(t, filepath.Join(cache, "pristine", p.Key, "masterWindows", name), source)
	write(t, filepath.Join(dist, p.BundleDir, "masterWindows", name), target)
	raw, _ := json.Marshal(PatchManifest{Files: []string{name}})
	write(t, filepath.Join(dist, p.BundleDir, "PATCH_MANIFEST.json"), raw)

	m, err := BuildDeltaPayload(cache, dist, out, []string{p.Key})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.TotalBytes <= 0 {
		t.Fatalf("bad manifest: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(out, "DAT_DELTA_MANIFEST.json")); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(out, filepath.FromSlash(m.Entries[0].PatchFile))
	rebuilt := filepath.Join(td, "rebuilt")
	if _, err := ApplyDelta(filepath.Join(cache, "pristine", p.Key, "masterWindows", name), patch, rebuilt); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(rebuilt)
	if !bytes.Equal(got, target) {
		t.Fatal("payload delta did not reconstruct target")
	}
}
