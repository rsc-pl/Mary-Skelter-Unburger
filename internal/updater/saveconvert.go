package updater

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ms2SwitchMainSize = 0x2856E8
	ms2PCMainSize     = 0x2857A0
	ms2SlotTableSize  = 0x11F8
	ms2SaveSlotSize   = 0x9C
	ms2PCWrapperSize  = 12
)

type SavePlatform string

const (
	SavePlatformSwitch SavePlatform = "switch"
	SavePlatformPC     SavePlatform = "pc"
)

type saveInsertion struct{ Offset, Amount int }

var ms2SaveInsertions = func() []saveInsertion {
	out := make([]saveInsertion, 0, 24)
	for i := 0; i < 20; i++ {
		out = append(out, saveInsertion{Offset: 0x10 + i*0x798 + 0x280, Amount: 8})
	}
	out = append(out,
		saveInsertion{Offset: 0x98AC, Amount: 4},
		saveInsertion{Offset: 0x12CF8, Amount: 12},
		saveInsertion{Offset: 0x22004C, Amount: 4},
		saveInsertion{Offset: 0x2816C0, Amount: 4},
	)
	sort.Slice(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out
}()

type SaveFileReport struct {
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
	MD5    string `json:"md5,omitempty"`
}

type SaveConversionReport struct {
	Format          string                    `json:"format"`
	Direction       string                    `json:"direction"`
	SourceDirectory string                    `json:"source_directory"`
	OutputDirectory string                    `json:"output_directory"`
	SourceSlot      string                    `json:"source_slot"`
	Source          map[string]SaveFileReport `json:"source"`
	Converted       map[string]SaveFileReport `json:"converted"`
	OldMainMD5      string                    `json:"old_main_md5"`
	NewMainMD5      string                    `json:"new_main_md5"`
	PreservedID     string                    `json:"preserved_identifier_0x08_0x17"`
	RoundTrip       bool                      `json:"roundtrip_byte_identical"`
	Notes           []string                  `json:"notes"`
}

type ms2SaveTriplet struct {
	SlotPath, MainPath, SaveSlotPath string
	SlotName, MainName, SaveSlotName string
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func md5Bytes(b []byte) [md5.Size]byte { return md5.Sum(b) }

func ms2DwordSum(data []byte) (uint32, error) {
	if len(data)%4 != 0 {
		return 0, fmt.Errorf("inner file size %d is not DWORD-aligned", len(data))
	}
	var total uint32
	for i := 0; i < len(data); i += 4 {
		total += binary.LittleEndian.Uint32(data[i : i+4])
	}
	return total, nil
}

func ms2PCWrap(inner []byte) ([]byte, error) {
	checksum, err := ms2DwordSum(inner)
	if err != nil {
		return nil, err
	}
	out := make([]byte, ms2PCWrapperSize+len(inner))
	binary.LittleEndian.PutUint64(out[0:8], 1)
	binary.LittleEndian.PutUint32(out[8:12], checksum)
	copy(out[12:], inner)
	return out, nil
}

func ms2PCUnwrap(blob []byte) ([]byte, error) {
	if len(blob) < ms2PCWrapperSize {
		return nil, fmt.Errorf("file too small for PC wrapper")
	}
	marker := binary.LittleEndian.Uint64(blob[0:8])
	if marker != 1 {
		return nil, fmt.Errorf("bad PC wrapper marker: %#x (expected 1)", marker)
	}
	inner := append([]byte(nil), blob[12:]...)
	actual, err := ms2DwordSum(inner)
	if err != nil {
		return nil, err
	}
	stored := binary.LittleEndian.Uint32(blob[8:12])
	if stored != actual {
		return nil, fmt.Errorf("bad PC wrapper checksum: stored=%08x actual=%08x", stored, actual)
	}
	return inner, nil
}

func ms2SwitchMainToPC(sw []byte) ([]byte, error) {
	if len(sw) != ms2SwitchMainSize {
		return nil, fmt.Errorf("Switch main save has size %#x; expected %#x", len(sw), ms2SwitchMainSize)
	}
	if binary.LittleEndian.Uint32(sw[:4]) != ms2SwitchMainSize {
		return nil, fmt.Errorf("Switch main save declares size %#x; expected %#x", binary.LittleEndian.Uint32(sw[:4]), ms2SwitchMainSize)
	}
	out := make([]byte, 0, ms2PCMainSize)
	src := 0
	for _, ins := range ms2SaveInsertions {
		if ins.Offset < src || ins.Offset > len(sw) {
			return nil, fmt.Errorf("invalid insertion map at %#x", ins.Offset)
		}
		out = append(out, sw[src:ins.Offset]...)
		out = append(out, make([]byte, ins.Amount)...)
		src = ins.Offset
	}
	out = append(out, sw[src:]...)
	if len(out) != ms2PCMainSize {
		return nil, fmt.Errorf("converted main size %#x != %#x", len(out), ms2PCMainSize)
	}
	binary.LittleEndian.PutUint32(out[:4], ms2PCMainSize)
	return out, nil
}

func ms2PCMainToSwitch(pc []byte) ([]byte, error) {
	if len(pc) != ms2PCMainSize {
		return nil, fmt.Errorf("PC inner main size %#x; expected %#x", len(pc), ms2PCMainSize)
	}
	if binary.LittleEndian.Uint32(pc[:4]) != ms2PCMainSize {
		return nil, fmt.Errorf("PC main save declares size %#x; expected %#x", binary.LittleEndian.Uint32(pc[:4]), ms2PCMainSize)
	}
	type pos struct{ Off, Amount int }
	positions := make([]pos, 0, len(ms2SaveInsertions))
	added := 0
	for _, ins := range ms2SaveInsertions {
		positions = append(positions, pos{Off: ins.Offset + added, Amount: ins.Amount})
		added += ins.Amount
	}
	out := append([]byte(nil), pc...)
	for i := len(positions) - 1; i >= 0; i-- {
		p := positions[i]
		for _, b := range out[p.Off : p.Off+p.Amount] {
			if b != 0 {
				return nil, fmt.Errorf("expected zero x64 padding at PC offset %#x", p.Off)
			}
		}
		out = append(out[:p.Off], out[p.Off+p.Amount:]...)
	}
	if len(out) != ms2SwitchMainSize {
		return nil, fmt.Errorf("depadding produced size %#x; expected %#x", len(out), ms2SwitchMainSize)
	}
	binary.LittleEndian.PutUint32(out[:4], ms2SwitchMainSize)
	return out, nil
}

func findMS2SaveTriplet(dir string) (ms2SaveTriplet, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		if err != nil {
			return ms2SaveTriplet{}, fmt.Errorf("input directory: %w", err)
		}
		return ms2SaveTriplet{}, fmt.Errorf("input must be a directory")
	}
	slot := filepath.Join(dir, "SaveDataBE.slot")
	if _, err := os.Stat(slot); err != nil {
		return ms2SaveTriplet{}, fmt.Errorf("missing SaveDataBE.slot")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "SaveDataBE_*.sav"))
	if err != nil {
		return ms2SaveTriplet{}, err
	}
	var saves []string
	for _, p := range matches {
		if filepath.Base(p) != "SaveDataBE.slot" {
			saves = append(saves, p)
		}
	}
	if len(saves) != 1 {
		return ms2SaveTriplet{}, fmt.Errorf("expected exactly one SaveDataBE_XXXX.sav, found %d", len(saves))
	}
	main := saves[0]
	savslot := strings.TrimSuffix(main, ".sav") + ".savslot"
	if _, err := os.Stat(savslot); err != nil {
		return ms2SaveTriplet{}, fmt.Errorf("missing matching %s", filepath.Base(savslot))
	}
	return ms2SaveTriplet{
		SlotPath: slot, MainPath: main, SaveSlotPath: savslot,
		SlotName: filepath.Base(slot), MainName: filepath.Base(main), SaveSlotName: filepath.Base(savslot),
	}, nil
}

func verifyMS2Inner(slot, main, savslot []byte, platform SavePlatform) error {
	if len(slot) != ms2SlotTableSize {
		return fmt.Errorf("SaveDataBE.slot size %#x; expected %#x", len(slot), ms2SlotTableSize)
	}
	expectedMain := ms2SwitchMainSize
	if platform == SavePlatformPC {
		expectedMain = ms2PCMainSize
	}
	if len(main) != expectedMain {
		return fmt.Errorf("main save size %#x; expected %#x", len(main), expectedMain)
	}
	if binary.LittleEndian.Uint32(main[:4]) != uint32(expectedMain) {
		return fmt.Errorf("main embedded size %#x; expected %#x", binary.LittleEndian.Uint32(main[:4]), expectedMain)
	}
	if len(savslot) != ms2SaveSlotSize || string(savslot[:8]) != "SAVE0001" {
		return fmt.Errorf("savslot format mismatch")
	}
	actual := md5Bytes(main)
	if string(savslot[0x18:0x28]) != string(actual[:]) {
		return fmt.Errorf("savslot MD5 mismatch: stored=%x actual=%x", savslot[0x18:0x28], actual)
	}
	return nil
}

func readMS2SaveInner(t ms2SaveTriplet, platform SavePlatform) (slot, main, savslot []byte, err error) {
	read := func(p string) ([]byte, error) { return os.ReadFile(p) }
	slot, err = read(t.SlotPath)
	if err != nil {
		return nil, nil, nil, err
	}
	main, err = read(t.MainPath)
	if err != nil {
		return nil, nil, nil, err
	}
	savslot, err = read(t.SaveSlotPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if platform == SavePlatformPC {
		if slot, err = ms2PCUnwrap(slot); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", t.SlotName, err)
		}
		if main, err = ms2PCUnwrap(main); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", t.MainName, err)
		}
		if savslot, err = ms2PCUnwrap(savslot); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", t.SaveSlotName, err)
		}
	}
	if err = verifyMS2Inner(slot, main, savslot, platform); err != nil {
		return nil, nil, nil, err
	}
	return slot, main, savslot, nil
}

func VerifyMS2SaveDirectory(dir string, platform SavePlatform) error {
	if platform != SavePlatformPC && platform != SavePlatformSwitch {
		return fmt.Errorf("unsupported save platform %q", platform)
	}
	t, err := findMS2SaveTriplet(dir)
	if err != nil {
		return err
	}
	_, _, _, err = readMS2SaveInner(t, platform)
	return err
}

func writeSaveOutput(outDir string, files map[string][]byte, report SaveConversionReport, overwrite bool) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	reportPath := filepath.Join(outDir, "conversion_report.json")
	paths := make([]string, 0, len(files)+1)
	for name := range files {
		paths = append(paths, filepath.Join(outDir, name))
	}
	paths = append(paths, reportPath)
	if !overwrite {
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("%s exists; choose another output directory or enable overwrite", p)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("preflight %s: %w", p, err)
			}
		}
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	ordered := make([]string, 0, len(files))
	for name := range files {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		p := filepath.Join(outDir, name)
		tmp := p + ".tmp-unburger"
		if err := os.WriteFile(tmp, files[name], 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, p); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	tmp := reportPath + ".tmp-unburger"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, reportPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func ConvertMS2Save(inputDir, outputDir string, from, to SavePlatform, overwrite bool) (SaveConversionReport, error) {
	var zero SaveConversionReport
	if from == to || (from != SavePlatformPC && from != SavePlatformSwitch) || (to != SavePlatformPC && to != SavePlatformSwitch) {
		return zero, fmt.Errorf("conversion must be switch->pc or pc->switch")
	}
	inAbs, err := filepath.Abs(inputDir)
	if err != nil {
		return zero, err
	}
	outAbs, err := filepath.Abs(outputDir)
	if err != nil {
		return zero, err
	}
	if filepath.Clean(inAbs) == filepath.Clean(outAbs) {
		return zero, fmt.Errorf("refusing in-place save conversion; choose a separate output directory")
	}
	t, err := findMS2SaveTriplet(inAbs)
	if err != nil {
		return zero, err
	}
	slot, main, savslot, err := readMS2SaveInner(t, from)
	if err != nil {
		return zero, err
	}
	oldMD5 := md5Bytes(main)
	convertedMain := []byte(nil)
	if from == SavePlatformSwitch {
		convertedMain, err = ms2SwitchMainToPC(main)
	} else {
		convertedMain, err = ms2PCMainToSwitch(main)
	}
	if err != nil {
		return zero, err
	}
	newSaveSlot := append([]byte(nil), savslot...)
	newMD5 := md5Bytes(convertedMain)
	copy(newSaveSlot[0x18:0x28], newMD5[:])

	// Strong transformation proof in both directions before any output is written.
	var roundtrip []byte
	if from == SavePlatformSwitch {
		roundtrip, err = ms2PCMainToSwitch(convertedMain)
	} else {
		roundtrip, err = ms2SwitchMainToPC(convertedMain)
	}
	if err != nil || !bytes.Equal(roundtrip, main) {
		if err != nil {
			return zero, fmt.Errorf("internal round-trip failed: %w", err)
		}
		return zero, fmt.Errorf("internal round-trip is not byte-identical")
	}

	files := map[string][]byte{}
	if to == SavePlatformPC {
		if files[t.SlotName], err = ms2PCWrap(slot); err != nil {
			return zero, err
		}
		if files[t.MainName], err = ms2PCWrap(convertedMain); err != nil {
			return zero, err
		}
		if files[t.SaveSlotName], err = ms2PCWrap(newSaveSlot); err != nil {
			return zero, err
		}
	} else {
		files[t.SlotName] = append([]byte(nil), slot...)
		files[t.MainName] = append([]byte(nil), convertedMain...)
		files[t.SaveSlotName] = append([]byte(nil), newSaveSlot...)
	}

	report := SaveConversionReport{
		Format: "Mary Skelter 2 SaveDataBE conversion report", Direction: string(from) + "->" + string(to),
		SourceDirectory: inAbs, OutputDirectory: outAbs, SourceSlot: strings.TrimSuffix(strings.TrimPrefix(t.MainName, "SaveDataBE_"), ".sav"),
		Source: map[string]SaveFileReport{}, Converted: map[string]SaveFileReport{},
		OldMainMD5: hex.EncodeToString(oldMD5[:]), NewMainMD5: hex.EncodeToString(newMD5[:]), PreservedID: hex.EncodeToString(newSaveSlot[0x08:0x18]), RoundTrip: true,
		Notes: []string{
			"SystemData_0000.* is intentionally not generated or replaced.",
			"The 16-byte identifier at SAVSLOT offset 0x08 is preserved verbatim.",
			"Conversion never modifies the source directory in place.",
		},
	}
	sourceFiles := map[string][]byte{t.SlotName: slot, t.MainName: main, t.SaveSlotName: savslot}
	for name, data := range sourceFiles {
		r := SaveFileReport{Size: len(data), SHA256: sha256Hex(data)}
		if name == t.MainName {
			r.MD5 = hex.EncodeToString(oldMD5[:])
		}
		report.Source[name] = r
	}
	for name, data := range files {
		r := SaveFileReport{Size: len(data), SHA256: sha256Hex(data)}
		if name == t.MainName {
			r.MD5 = hex.EncodeToString(newMD5[:])
		}
		report.Converted[name] = r
	}
	if err := writeSaveOutput(outAbs, files, report, overwrite); err != nil {
		return zero, err
	}
	if err := VerifyMS2SaveDirectory(outAbs, to); err != nil {
		return zero, fmt.Errorf("converted output verification failed: %w", err)
	}
	return report, nil
}
