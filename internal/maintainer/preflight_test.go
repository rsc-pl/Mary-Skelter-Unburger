package maintainer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckChangedSourcesPassAndTamper(t *testing.T) {
	root := t.TempDir()
	ms2 := filepath.Join(root, "ms2")
	msf := filepath.Join(root, "msf")
	data := []byte("canonical archive fixture\n")
	p := filepath.Join(ms2, "GxArchivedFile055.dat")
	writeTestFile(t, p, data)
	h := sha256.Sum256(data)
	manifest := "dataset\tfile\tbytes\tsha256\nMS2_PC_LOCALIZED\tGxArchivedFile055.dat\t" +
		strconv.FormatInt(int64(len(data)), 10) + "\t" + hex.EncodeToString(h[:]) + "\n"
	writeTestFile(t, filepath.Join(root, "toolchain", "manifests", "expected_pc_archive_sha256.tsv"), []byte(manifest))
	cm := map[string]recordSummary{"MS2_PC_LOCALIZED": {Changes: []recordChange{{RecordKey: "MS2_PC_LOCALIZED/a055/g00001/top/blob0/r00000/f002"}}}}
	raw, err := json.Marshal(cm)
	if err != nil {
		t.Fatal(err)
	}
	change := filepath.Join(root, "change.json")
	writeTestFile(t, change, raw)
	var out bytes.Buffer
	c := CachedSources{MS2Master: ms2, FinaleMaster: msf}
	if err := CheckChangedSources(root, c, change, &out); err != nil {
		t.Fatalf("pass fixture rejected: %v", err)
	}
	if !strings.Contains(out.String(), "expected_to_check=1 checked=1 mismatches=0") {
		t.Fatalf("unexpected output: %q", out.String())
	}
	writeTestFile(t, p, []byte("tampered\n"))
	if err := CheckChangedSources(root, c, change, nil); err == nil {
		t.Fatal("tampered source unexpectedly accepted")
	}
}

func TestPythonStyleJSONLine(t *testing.T) {
	got, err := pythonStyleJSONLine(map[string]any{"z": "あ<&", "a": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a": 1, "z": "あ<&"}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
