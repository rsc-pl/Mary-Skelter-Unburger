package maintainer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeGraphicsPatchNoClobberHashGuard(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "pristine.dat")
	dest := filepath.Join(dir, "new.dat")
	// Invalid input still has to fail before writing anywhere.
	if err := os.WriteFile(source, []byte("not a GDAT"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("not a GDAT"))
	sha := hex.EncodeToString(sum[:])
	if _, err := PatchGraphicsTIDGoFile(source, dest, strings.Repeat("a", 64), 0, "x.tid", []byte("x")); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("stale source not rejected: %v", err)
	}
	if _, err := PatchGraphicsTIDGoFile(source, dest, sha, 0, "x.tid", []byte("x")); err == nil {
		t.Fatal("invalid GDAT accepted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("unexpected destination: %v", err)
	}
	if err := os.WriteFile(dest, []byte("OWNER-DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := PatchGraphicsTIDGoFile(source, dest, sha, 0, "x.tid", []byte("x"))
	if err == nil {
		t.Fatal("invalid GDAT accepted")
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, []byte("OWNER-DATA")) {
		t.Fatal("existing owner data overwritten")
	}
}
