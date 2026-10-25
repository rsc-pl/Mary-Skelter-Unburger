package maintainer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"maryskelter-unburger/internal/mskit"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func nativeContainerFixture(t *testing.T, wrapped bool) ([]byte, []byte, []byte) {
	t.Helper()
	tid := make([]byte, 144)
	copy(tid[:4], []byte{'T', 'I', 'D', 0x90})
	binary.LittleEndian.PutUint32(tid[0x44:0x48], 4)
	binary.LittleEndian.PutUint32(tid[0x48:0x4c], 4)
	copy(tid[0x64:0x68], "DXT5")
	for i := 128; i < 144; i++ {
		tid[i] = byte(i)
	}
	replacement := append([]byte(nil), tid...)
	replacement[130] ^= 0x44
	const coll = 0xD0
	size := mskit.CL3EntrySize + len(tid)
	cl3 := make([]byte, coll+size)
	copy(cl3[0x10:], "CL3L")
	binary.LittleEndian.PutUint32(cl3[0x18:0x1c], 1)
	binary.LittleEndian.PutUint32(cl3[0x1c:0x20], 2)
	binary.LittleEndian.PutUint32(cl3[0x20:0x24], 0x30)
	sec := cl3[0x30:]
	copy(sec[:0x20], "FILE_COLLECTION")
	binary.LittleEndian.PutUint32(sec[0x20:0x24], 1)
	binary.LittleEndian.PutUint32(sec[0x24:0x28], uint32(size))
	binary.LittleEndian.PutUint32(sec[0x28:0x2c], coll)
	second := cl3[0x30+mskit.CL3SectionSize:]
	copy(second[:0x20], "FILE_LINK")
	binary.LittleEndian.PutUint32(second[0x28:0x2c], uint32(coll+size))
	rec := cl3[coll:]
	copy(rec[:mskit.CL3NameSize], "sample.tid")
	binary.LittleEndian.PutUint32(rec[0x204:0x208], mskit.CL3EntrySize)
	binary.LittleEndian.PutUint32(rec[0x208:0x20c], uint32(len(tid)))
	copy(cl3[coll+mskit.CL3EntrySize:], tid)
	entry := cl3
	if wrapped {
		w, e := mskit.WrapBILZGo(cl3, mskit.BILZAlgoZlib, 9)
		if e != nil {
			t.Fatal(e)
		}
		entry = append([]byte("PRIVATE-PREFIX"), w...)
	}
	gd, e := mskit.BuildGDAT([][]byte{[]byte("UNRELATED-LEFT"), entry, []byte("UNRELATED-RIGHT")})
	if e != nil {
		t.Fatal(e)
	}
	return gd, tid, replacement
}
func TestNativeGraphicsContainerAndNoClobber(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "zlib"}[wrapped], func(t *testing.T) {
			original, _, replacement := nativeContainerFixture(t, wrapped)
			d := t.TempDir()
			src := filepath.Join(d, "source.gdat")
			out := filepath.Join(d, "nested", "patched.gdat")
			if e := os.WriteFile(src, original, 0600); e != nil {
				t.Fatal(e)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(original))
			report, e := PatchGraphicsTIDGoFile(src, out, hash, 1, "SAMPLE.TID", replacement)
			if e != nil {
				t.Fatal(e)
			}
			if report.BILZ != wrapped {
				t.Fatalf("wrapper drift %+v", report)
			}
			st, e := os.Stat(out)
			if e != nil || st.Mode().Perm() != 0o644 {
				t.Fatalf("output permissions drift: %v %v", st, e)
			}
			got, e := os.ReadFile(out)
			if e != nil {
				t.Fatal(e)
			}
			before, _ := mskit.ParseGDAT(original)
			after, e := mskit.ParseGDAT(got)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(before.Entries[0], after.Entries[0]) || !bytes.Equal(before.Entries[2], after.Entries[2]) {
				t.Fatal("other GDAT records changed")
			}
			tid, _, e := mskit.ExtractGraphicsTID(got, 1, "sample.tid")
			if e != nil || !bytes.Equal(tid, replacement) {
				t.Fatalf("wrong result: %v", e)
			}
			untouched, e := os.ReadFile(src)
			if e != nil || !bytes.Equal(untouched, original) {
				t.Fatal("source altered")
			}
			if _, e = PatchGraphicsTIDGoFile(src, out, hash, 1, "sample.tid", replacement); e == nil || !strings.Contains(e.Error(), "exists") {
				t.Fatalf("existing dest clobbered: %v", e)
			}
			if _, e = PatchGraphicsTIDGoFile(src, src, hash, 1, "sample.tid", replacement); e == nil || !strings.Contains(e.Error(), "invalid target") {
				t.Fatalf("source clobbered: %v", e)
			}
			if runtime.GOOS != "windows" {
				link := filepath.Join(d, "link.gdat")
				if e := os.Symlink(src, link); e != nil {
					t.Fatal(e)
				}
				if _, e = PatchGraphicsTIDGoFile(src, link, hash, 1, "sample.tid", replacement); e == nil {
					t.Fatal("output symlink replaced")
				}
			}
			if e := os.WriteFile(out, []byte("OWNER"), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = PatchGraphicsTIDGoFile(src, out, hash, 1, "sample.tid", replacement); e == nil {
				t.Fatal("manual file overwritten")
			}
			current, _ := os.ReadFile(out)
			if string(current) != "OWNER" {
				t.Fatal("owner data changed")
			}
		})
	}
}
func TestNativeGraphicsContainerUnknownBILZTailRejected(t *testing.T) {
	original, _, replacement := nativeContainerFixture(t, true)
	g, e := mskit.ParseGDAT(original)
	if e != nil {
		t.Fatal(e)
	}
	g.Entries[1] = append(append([]byte(nil), g.Entries[1]...), []byte("UNKNOWN-TRAILER")...)
	unsafe, e := mskit.BuildGDAT(g.Entries)
	if e != nil {
		t.Fatal(e)
	}
	d := t.TempDir()
	src := filepath.Join(d, "source.gdat")
	out := filepath.Join(d, "patched.gdat")
	if e := os.WriteFile(src, unsafe, 0600); e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(unsafe))
	if _, e = PatchGraphicsTIDGoFile(src, out, hash, 1, "sample.tid", replacement); e == nil || !strings.Contains(e.Error(), "unknown trailing bytes") {
		t.Fatalf("unknown trailer allowed %v", e)
	}
	if _, e = os.Lstat(out); !os.IsNotExist(e) {
		t.Fatal("unsafe output created")
	}
}

func TestLegacyGraphicsContainerNeverOverwritesOwnerFile(t *testing.T) {
	original, _, replacement := nativeContainerFixture(t, false)
	d := t.TempDir()
	src := filepath.Join(d, "src.gdat")
	out := filepath.Join(d, "existing.gdat")
	if e := os.WriteFile(src, original, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(out, []byte("ORIGINAL-USER-DATA"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := PatchGraphicsTIDFile(d, src, out, 1, "sample.tid", replacement); e == nil {
		t.Fatal("legacy path overwrote owner output")
	}
	if got, _ := os.ReadFile(out); string(got) != "ORIGINAL-USER-DATA" {
		t.Fatal("legacy path corrupted owner file")
	}
	if _, e := PatchGraphicsTIDFile(d, src, src, 1, "sample.tid", replacement); e == nil {
		t.Fatal("legacy path overwrote its input")
	}
	good := filepath.Join(d, "new.gdat")
	if _, e := PatchGraphicsTIDFile(d, src, good, 1, "sample.tid", replacement); e != nil {
		t.Fatal(e)
	}
	if got, _, e := mskit.ExtractGraphicsTID(mustReadNativeContainer(t, good), 1, "sample.tid"); e != nil || !bytes.Equal(got, replacement) {
		t.Fatalf("legacy plain container drift %v", e)
	}
}
func mustReadNativeContainer(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestNativeGraphicsContainerConcurrentNoClobber(t *testing.T) {
	original, _, replacement := nativeContainerFixture(t, true)
	d := t.TempDir()
	src := filepath.Join(d, "source.gdat")
	dest := filepath.Join(d, "output.gdat")
	if err := os.WriteFile(src, original, 0600); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(original))
	const count = 8
	var wg sync.WaitGroup
	successes := make(chan bool, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := PatchGraphicsTIDGoFile(src, dest, hash, 1, "sample.tid", replacement)
			successes <- (e == nil)
		}()
	}
	wg.Wait()
	close(successes)
	wins := 0
	for ok := range successes {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent publish succeeded %d times, expected exactly one", wins)
	}
	got, _, err := mskit.ExtractGraphicsTID(mustReadNativeContainer(t, dest), 1, "sample.tid")
	if err != nil || !bytes.Equal(got, replacement) {
		t.Fatalf("concurrent output corrupt: %v", err)
	}
}
