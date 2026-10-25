package maintainer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPCMasterAuditNativeReadOnlyAndAdverse(t *testing.T) {
	root := t.TempDir()
	ms2 := filepath.Join(root, "ms2")
	msf := filepath.Join(root, "msf")
	for _, dir := range []string{ms2, msf} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte("owner pristine bytes")
	h := sha256.Sum256(data)
	sum := hex.EncodeToString(h[:])
	name := "GxArchivedFile055.dat"
	path := filepath.Join(ms2, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	list := []sourceSpecFile{{Game: "MS2", Kind: "archive", Name: name, Bytes: int64(len(data)), SHA256: sum}, {Game: "MSF", Kind: "archive", Name: "GxArchivedFile073.dat", Bytes: 3, SHA256: strings.Repeat("a", 64)}, {Game: "MS2", Kind: "exe", Name: "ignored.exe"}}
	rep := auditPCMasterSpecs(list, ms2, msf)
	if rep.Status != "INCOMPLETE" || rep.ExpectedArchives != 2 || rep.VerifiedArchives != 1 || rep.Items[0].Status != "PASS" || rep.Items[1].Status != "MISSING" {
		t.Fatalf("unexpected report %+v", rep)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("source was altered", err)
	}
	rep = auditPCMasterSpecs(list[:1], ms2, msf)
	if rep.Status != "PASS_SOURCE_IDENTITY_ONLY" {
		t.Fatalf("valid original rejected: %+v", rep)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(msf, "hidden"), path); err != nil {
		t.Fatal(err)
	}
	rep = auditPCMasterSpecs(list[:1], ms2, msf)
	if rep.Items[0].Status != "NOT_REGULAR_OR_SYMLINK" {
		t.Fatalf("symlink accepted: %+v", rep)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("X", len(data))), 0600); err != nil {
		t.Fatal(err)
	}
	rep = auditPCMasterSpecs(list[:1], ms2, msf)
	if rep.Items[0].Status != "SHA256_MISMATCH" {
		t.Fatalf("wrong original accepted: %+v", rep)
	}
	list[0].Bytes++
	rep = auditPCMasterSpecs(list[:1], ms2, msf)
	if rep.Items[0].Status != "SIZE_MISMATCH" || rep.Items[0].ActualSHA256 != "" {
		t.Fatalf("wrong size hashed: %+v", rep)
	}
}
func TestPCMasterAuditCanonicalCoverage(t *testing.T) {
	root := filepath.Join("..", "..")
	s, e := sourceCacheSpecForRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	var ms2, msf int
	for _, f := range s.Files {
		if f.Kind != "archive" {
			continue
		}
		if f.Game == "MS2" {
			ms2++
		} else if f.Game == "MSF" {
			msf++
		} else {
			t.Fatalf("unknown game %q", f.Game)
		}
	}
	if ms2 != 3 || msf != 7 {
		t.Fatalf("pristine required archives: MS2=%d MSF=%d", ms2, msf)
	}
}

func TestPCMasterAuditAllManifestCoverage(t *testing.T) {
	r, e := AuditPCMasterSourcesNative(filepath.Join("..", ".."), "", "", true)
	if e != nil {
		t.Fatal(e)
	}
	if r.ExpectedArchives != 46 || r.ExpectedBytes != 9820277816 || r.VerifiedArchives != 0 || r.Selection != "all-pinned-PC-DAT-manifest" {
		t.Fatalf("invalid manifest audit: %+v", r)
	}
}
