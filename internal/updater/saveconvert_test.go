package updater

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func syntheticMS2SwitchSave(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	slot := make([]byte, ms2SlotTableSize)
	for i := range slot {
		slot[i] = byte((i*17 + 3) & 0xff)
	}
	main := make([]byte, ms2SwitchMainSize)
	for i := range main {
		main[i] = byte((i*29 + 11) & 0xff)
	}
	binary.LittleEndian.PutUint32(main[:4], ms2SwitchMainSize)
	savslot := make([]byte, ms2SaveSlotSize)
	copy(savslot[:8], []byte("SAVE0001"))
	for i := 0; i < 16; i++ {
		savslot[0x08+i] = byte(0xA0 + i)
	}
	h := md5.Sum(main)
	copy(savslot[0x18:0x28], h[:])
	files := map[string][]byte{
		"SaveDataBE.slot":         slot,
		"SaveDataBE_0001.sav":     main,
		"SaveDataBE_0001.savslot": savslot,
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestMS2SaveSwitchPCRoundTrip(t *testing.T) {
	root := t.TempDir()
	sw := filepath.Join(root, "switch")
	pc := filepath.Join(root, "pc")
	sw2 := filepath.Join(root, "switch2")
	orig := syntheticMS2SwitchSave(t, sw)
	r1, err := ConvertMS2Save(sw, pc, SavePlatformSwitch, SavePlatformPC, false)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.RoundTrip || r1.Direction != "switch->pc" {
		t.Fatalf("bad report: %+v", r1)
	}
	if err := VerifyMS2SaveDirectory(pc, SavePlatformPC); err != nil {
		t.Fatal(err)
	}
	r2, err := ConvertMS2Save(pc, sw2, SavePlatformPC, SavePlatformSwitch, false)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.RoundTrip || r2.Direction != "pc->switch" {
		t.Fatalf("bad report: %+v", r2)
	}
	if err := VerifyMS2SaveDirectory(sw2, SavePlatformSwitch); err != nil {
		t.Fatal(err)
	}
	for n, want := range orig {
		got, err := os.ReadFile(filepath.Join(sw2, n))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("roundtrip mismatch: %s", n)
		}
	}
	if _, err := os.Stat(filepath.Join(pc, "SystemData_0000.sav")); !os.IsNotExist(err) {
		t.Fatalf("SystemData unexpectedly generated")
	}
}

func TestMS2SaveRejectsBadPCWrapper(t *testing.T) {
	root := t.TempDir()
	sw := filepath.Join(root, "switch")
	pc := filepath.Join(root, "pc")
	syntheticMS2SwitchSave(t, sw)
	if _, err := ConvertMS2Save(sw, pc, SavePlatformSwitch, SavePlatformPC, false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(pc, "SaveDataBE.slot")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 0x80
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMS2SaveDirectory(pc, SavePlatformPC); err == nil {
		t.Fatal("corrupt PC wrapper accepted")
	}
}

func TestMS2SaveRejectsNonzeroX64Padding(t *testing.T) {
	sw := make([]byte, ms2SwitchMainSize)
	binary.LittleEndian.PutUint32(sw[:4], ms2SwitchMainSize)
	pc, err := ms2SwitchMainToPC(sw)
	if err != nil {
		t.Fatal(err)
	}
	pc[0x10+0x280] = 1
	if _, err := ms2PCMainToSwitch(pc); err == nil {
		t.Fatal("nonzero x64 padding accepted")
	}
}

func TestMS2SaveRejectsInPlaceConversion(t *testing.T) {
	d := t.TempDir()
	syntheticMS2SwitchSave(t, d)
	if _, err := ConvertMS2Save(d, d, SavePlatformSwitch, SavePlatformPC, true); err == nil {
		t.Fatal("in-place conversion accepted")
	}
}

func TestMS2SavePreflightsReportConflictBeforeWriting(t *testing.T) {
	root := t.TempDir()
	sw := filepath.Join(root, "switch")
	out := filepath.Join(root, "out")
	syntheticMS2SwitchSave(t, sw)
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "conversion_report.json"), []byte("keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertMS2Save(sw, out, SavePlatformSwitch, SavePlatformPC, false); err == nil {
		t.Fatal("report conflict unexpectedly accepted")
	}
	for _, name := range []string{"SaveDataBE.slot", "SaveDataBE_0001.sav", "SaveDataBE_0001.savslot"} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was written before full output preflight completed", name)
		}
	}
	b, err := os.ReadFile(filepath.Join(out, "conversion_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "keep-me\n" {
		t.Fatalf("existing report changed: %q", b)
	}
}
