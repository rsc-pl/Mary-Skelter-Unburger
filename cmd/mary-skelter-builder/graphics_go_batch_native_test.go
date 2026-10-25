package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func hexSHA(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func batchTestTID(fill byte) []byte {
	b := make([]byte, 0x90)
	copy(b[:4], []byte{'T', 'I', 'D', 0x90})
	binary.LittleEndian.PutUint32(b[0x44:0x48], 4)
	binary.LittleEndian.PutUint32(b[0x48:0x4c], 4)
	copy(b[0x64:0x68], []byte("DXT5"))
	for i := 0x80; i < len(b); i++ {
		b[i] = fill
	}
	return b
}
func batchTestCL3(t *testing.T) []byte {
	t.Helper()
	const section = 0x30
	const coll = 0xD0
	names := []string{"alpha.tid", "beta.tid", "keep.bin"}
	payloads := [][]byte{batchTestTID(0x10), batchTestTID(0x20), []byte("KEEP-FOREVER")}
	size := 0x230 * len(names)
	for _, p := range payloads {
		size += len(p)
	}
	b := make([]byte, coll+size+7)
	copy(b[0x10:], []byte("CL3"))
	b[0x13] = 'L'
	binary.LittleEndian.PutUint32(b[0x18:0x1c], 3)
	binary.LittleEndian.PutUint32(b[0x1c:0x20], 2)
	binary.LittleEndian.PutUint32(b[0x20:0x24], section)
	copy(b[section:], []byte("FILE_COLLECTION"))
	binary.LittleEndian.PutUint32(b[section+0x20:section+0x24], uint32(len(names)))
	binary.LittleEndian.PutUint32(b[section+0x24:section+0x28], uint32(size))
	binary.LittleEndian.PutUint32(b[section+0x28:section+0x2c], coll)
	copy(b[section+0x50:], []byte("FILE_LINK"))
	binary.LittleEndian.PutUint32(b[section+0x50+0x28:section+0x50+0x2c], uint32(coll+size))
	cursor := 0x230 * len(names)
	for i, p := range payloads {
		q := coll + i*0x230
		copy(b[q:], []byte(names[i]))
		binary.LittleEndian.PutUint32(b[q+0x204:q+0x208], uint32(cursor))
		binary.LittleEndian.PutUint32(b[q+0x208:q+0x20c], uint32(len(p)))
		copy(b[coll+cursor:], p)
		cursor += len(p)
	}
	copy(b[coll+size:], []byte("CL3TAIL"))
	if _, err := mskit.ParseCL3(b); err != nil {
		t.Fatal(err)
	}
	return b
}
func batchTestGDAT(t *testing.T, zlib bool) []byte {
	t.Helper()
	cl := batchTestCL3(t)
	if zlib {
		var err error
		cl, err = mskit.WrapBILZGo(cl, mskit.BILZAlgoZlib, 9)
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := mskit.BuildGDAT([][]byte{[]byte("STATIC-LEFT"), cl, []byte("STATIC-RIGHT")})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func makeGraphicsManifest(t *testing.T) (string, []string) {
	t.Helper()
	d := t.TempDir()
	jobs := []graphicsBatchJob{}
	outs := []string{}
	for i := 0; i < 2; i++ {
		src := filepath.Join(d, "source"+string(rune('A'+i))+".dat")
		dst := filepath.Join(d, "result"+string(rune('A'+i))+".dat")
		dat := batchTestGDAT(t, i == 0)
		if err := os.WriteFile(src, dat, 0600); err != nil {
			t.Fatal(err)
		}
		job := graphicsBatchJob{Source: src, Output: dst, SourceSHA256: hexSHA(dat)}
		for k, name := range []string{"alpha.tid", "beta.tid"} {
			if i == 1 && k == 1 {
				break
			}
			old, _, err := mskit.ExtractGraphicsTID(dat, 1, name)
			if err != nil {
				t.Fatal(err)
			}
			newTID := batchTestTID(byte(0x50 + i*4 + k))
			repl := filepath.Join(d, "tid"+string(rune('a'+i*2+k))+".tid")
			if err = os.WriteFile(repl, newTID, 0600); err != nil {
				t.Fatal(err)
			}
			job.Edits = append(job.Edits, graphicsBatchEdit{GDATIndex: 1, Inner: name, Replacement: repl, SourceTIDSHA256: hexSHA(old), ReplacementSHA256: hexSHA(newTID)})
		}
		jobs = append(jobs, job)
		outs = append(outs, dst)
	}
	man := filepath.Join(d, "batch.json")
	raw, _ := json.Marshal(graphicsBatchManifest{Version: 1, Jobs: jobs})
	if err := os.WriteFile(man, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return man, outs
}
func TestGraphicsBatchTwoFilesAndMultipleInOneGDAT(t *testing.T) {
	man, outs := makeGraphicsManifest(t)
	var buf bytes.Buffer
	if err := executeNativeGraphicsBatch([]string{"--manifest", man}, &buf); err != nil {
		t.Fatal(err)
	}
	var rep graphicsBatchResponse
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Status != "written-unapproved-for-game" || len(rep.Files) != 2 || rep.Files[0].PatchedTIDs != 2 {
		t.Fatalf("wrong report %+v", rep)
	}
	for j, p := range outs {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		g, err := mskit.ParseGDAT(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(g.Entries[0], []byte("STATIC-LEFT")) || !bytes.Equal(g.Entries[2], []byte("STATIC-RIGHT")) {
			t.Fatal("untouched entries drift")
		}
		for k, name := range []string{"alpha.tid", "beta.tid"} {
			if j == 1 && k == 1 {
				break
			}
			got, _, err := mskit.ExtractGraphicsTID(raw, 1, name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, batchTestTID(byte(0x50+j*4+k))) {
				t.Fatalf("wrong TID %s", name)
			}
		}
	}
}
func TestGraphicsBatchRollbackAfterFirstPublish(t *testing.T) {
	man, outs := makeGraphicsManifest(t)
	err := executeNativeGraphicsBatchWithHook([]string{"--manifest", man}, &bytes.Buffer{}, func(i int) error {
		if i == 1 {
			return errors.New("injected")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("expected fault, got %v", err)
	}
	for _, p := range outs {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("partial output survived %s %v", p, err)
		}
	}
}
func TestGraphicsBatchRejectsChangedSourceWithoutPublishing(t *testing.T) {
	man, outs := makeGraphicsManifest(t)
	var m graphicsBatchManifest
	raw, _ := os.ReadFile(man)
	_ = json.Unmarshal(raw, &m)
	f, err := os.OpenFile(m.Jobs[1].Source, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("tampered")
	_ = f.Close()
	if err := executeNativeGraphicsBatch([]string{"--manifest", man}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "SHA mismatch") {
		t.Fatalf("expected stale source SHA, got %v", err)
	}
	for _, p := range outs {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatal("published before validation")
		}
	}
}
func TestGraphicsBatchNoClobberAndBadInputs(t *testing.T) {
	man, outs := makeGraphicsManifest(t)
	if err := os.WriteFile(outs[1], []byte("OWNER"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeNativeGraphicsBatch([]string{"--manifest", man}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted existing output")
	}
	b, _ := os.ReadFile(outs[1])
	if string(b) != "OWNER" {
		t.Fatal("existing output changed")
	}
	if _, err := os.Stat(outs[0]); !os.IsNotExist(err) {
		t.Fatal("published before no-clobber validation")
	}
	raw, _ := os.ReadFile(man)
	var m graphicsBatchManifest
	_ = json.Unmarshal(raw, &m)
	m.Jobs[0].Edits[0].Inner = "BETA.TID"
	m.Jobs[0].Edits[1].Inner = "beta.tid"
	wrong, _ := json.Marshal(m)
	if _, err := graphicsBatchValidate(wrong); err == nil {
		t.Fatal("duplicate casefold member accepted")
	}
}
