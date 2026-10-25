package maintainer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSourceConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	ms2 := filepath.Join(root, "Steam Library", "Mary Skelter 2")
	msf := filepath.Join(root, "Steam Library", "Mary Skelter Finale")
	for _, g := range []string{ms2, msf} {
		if err := os.MkdirAll(filepath.Join(g, "masterWindows"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ms2, "MarySkelter2.exe"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(msf, "MarySkelterFinale.exe"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ConfigureSources(root, ms2, msf, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "native Go") {
		t.Fatalf("unexpected output %q", out.String())
	}
	got, err := ReadSourcePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.MS2Game != ms2 || got.FinaleGame != msf {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	raw, err := os.ReadFile(sourceConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `\\ `) {
		t.Fatalf("new config should use shell quotes, got %q", raw)
	}
}

func TestReadLegacyPrintfQSourceConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".local"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "export MS2_PC_GAME=/tmp/Mary\\ Skelter\\ 2\n" +
		"export MSF_PC_GAME=/tmp/Mary\\ Skelter\\ Finale\n" +
		"export MS2_PC_MASTER=/tmp/Mary\\ Skelter\\ 2/masterWindows\n" +
		"export MSF_PC_MASTER=/tmp/Mary\\ Skelter\\ Finale/masterWindows\n" +
		"export MS2_PC_EXE=/tmp/Mary\\ Skelter\\ 2/MarySkelter2.exe\n"
	if err := os.WriteFile(sourceConfigPath(root), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSourcePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.MS2Game != "/tmp/Mary Skelter 2" || got.FinaleGame != "/tmp/Mary Skelter Finale" {
		t.Fatalf("legacy decode mismatch: %+v", got)
	}
}

func TestNativeSourceConfigHandlesSingleQuote(t *testing.T) {
	root := t.TempDir()
	ms2 := filepath.Join(root, "Player's Library", "Mary Skelter 2")
	msf := filepath.Join(root, "Player's Library", "Mary Skelter Finale")
	for _, g := range []string{ms2, msf} {
		if err := os.MkdirAll(filepath.Join(g, "masterWindows"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ms2, "MarySkelter2.exe"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureSources(root, ms2, msf, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSourcePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.MS2Game != ms2 || got.FinaleGame != msf {
		t.Fatalf("single quote round trip mismatch: %+v", got)
	}
}
