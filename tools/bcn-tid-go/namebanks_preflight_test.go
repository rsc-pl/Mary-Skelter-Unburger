package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNameBankPreflightRejectsLateConflictsBeforeRendering(t *testing.T) {
	d := t.TempDir()
	dst := filepath.Join(d, "output.gdat")
	if err := os.WriteFile(dst, []byte("OWNER FILE"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NameFamilyManifest{Version: 1, ProjectRoot: d, Jobs: []NameFamilyJob{
		{Source: "one.gdat", Output: "first.gdat", SourceSHA256: strings.Repeat("a", 64), Families: []string{"MS2_RESULT_NAMES"}},
		{Source: "two.gdat", Output: "output.gdat", SourceSHA256: strings.Repeat("b", 64), Families: []string{"MS2_BATTLE_NAMES"}},
	}}
	b, _ := json.Marshal(m)
	p := filepath.Join(d, "m.json")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err := runNameFamilyBatch(p, func(_, _ string, _ []byte) ([]byte, error) { calls++; return nil, nil }, nil)
	if err == nil || !strings.Contains(err.Error(), "output exists") {
		t.Fatalf("missing rejection: %v", err)
	}
	if calls != 0 {
		t.Fatalf("renderer invoked before preflight: %d", calls)
	}
	content, _ := os.ReadFile(dst)
	if string(content) != "OWNER FILE" {
		t.Fatal("owner file changed")
	}
	if _, e := os.Lstat(filepath.Join(d, "first.gdat")); !os.IsNotExist(e) {
		t.Fatal("partial output on preflight rejection")
	}
}
func TestNameBankPreflightRejectsSymlinkAndCrossAlias(t *testing.T) {
	d := t.TempDir()
	symlink := filepath.Join(d, "link.gdat")
	if err := os.Symlink(filepath.Join(d, "not-here"), symlink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	mk := func(jobs []NameFamilyJob) error {
		m := NameFamilyManifest{Version: 1, ProjectRoot: d, Jobs: jobs}
		b, _ := json.Marshal(m)
		p := filepath.Join(d, "m.json")
		if err := os.WriteFile(p, b, 0600); err != nil {
			return err
		}
		_, err := runNameFamilyBatch(p, func(_, _ string, _ []byte) ([]byte, error) { t.Fatal("unexpected rendering"); return nil, nil }, nil)
		return err
	}
	hash := strings.Repeat("a", 64)
	if err := mk([]NameFamilyJob{{Source: "input.gdat", Output: "link.gdat", SourceSHA256: hash, Families: []string{"MS2_RESULT_NAMES"}}}); err == nil {
		t.Fatal("symlink allowed")
	}
	if err := mk([]NameFamilyJob{{Source: "input.gdat", Output: "other.gdat", SourceSHA256: hash, Families: []string{"MS2_RESULT_NAMES"}}, {Source: "other.gdat", Output: "third.gdat", SourceSHA256: hash, Families: []string{"MS2_BATTLE_NAMES"}}}); err == nil {
		t.Fatal("cross-job alias allowed")
	}
}
func TestNameBankPinnedSourceAndGuards(t *testing.T) {
	for id, s := range nameFamilyIdentities {
		if len(s.sha) != 64 || s.index < 0 || s.width < 1 || s.height < 1 || s.inner == "" || (s.format != "BC7" && s.format != "DXT5") {
			t.Fatalf("invalid identity %s %+v", id, s)
		}
	}
	if len(nameFamilyIdentities) != 3 {
		t.Fatal("unexpected name bank expansion")
	}
}
