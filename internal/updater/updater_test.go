package updater

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func encodeSteamHeader(decoded []byte, seed uint32) []byte {
	raw := append([]byte(nil), decoded...)
	binary.LittleEndian.PutUint32(raw[:4], seed)
	prev := seed
	for off := 4; off < len(raw); off += 4 {
		plain := binary.LittleEndian.Uint32(decoded[off : off+4])
		enc := plain ^ prev
		binary.LittleEndian.PutUint32(raw[off:off+4], enc)
		prev = enc
	}
	return raw
}

func syntheticSteamStubProfile(t *testing.T) ([]byte, []byte, Profile) {
	t.Helper()
	const (
		peOff    = 0x80
		optOff   = 0x98
		secOff   = 0x188
		textRaw  = 0x400
		textRVA  = 0x1000
		textSize = 0x200
		bindRaw  = 0x600
		bindRVA  = 0x2000
		entryRVA = 0x2100
		entryOff = 0x700
		origEP   = 0x1010
	)
	buf := make([]byte, 0x800)
	copy(buf[:2], []byte("MZ"))
	binary.LittleEndian.PutUint32(buf[0x3c:0x40], peOff)
	copy(buf[peOff:peOff+4], []byte("PE\x00\x00"))
	coff := peOff + 4
	binary.LittleEndian.PutUint16(buf[coff:coff+2], 0x8664)
	binary.LittleEndian.PutUint16(buf[coff+2:coff+4], 2)
	binary.LittleEndian.PutUint16(buf[coff+16:coff+18], 0xf0)
	binary.LittleEndian.PutUint16(buf[optOff:optOff+2], 0x20b)
	binary.LittleEndian.PutUint32(buf[optOff+16:optOff+20], entryRVA)
	binary.LittleEndian.PutUint64(buf[optOff+24:optOff+32], 0x140000000)

	copy(buf[secOff:secOff+8], []byte(".text\x00\x00\x00"))
	binary.LittleEndian.PutUint32(buf[secOff+8:secOff+12], textSize)
	binary.LittleEndian.PutUint32(buf[secOff+12:secOff+16], textRVA)
	binary.LittleEndian.PutUint32(buf[secOff+16:secOff+20], textSize)
	binary.LittleEndian.PutUint32(buf[secOff+20:secOff+24], textRaw)
	b := secOff + 40
	copy(buf[b:b+8], []byte(".bind\x00\x00\x00"))
	binary.LittleEndian.PutUint32(buf[b+8:b+12], 0x200)
	binary.LittleEndian.PutUint32(buf[b+12:b+16], bindRVA)
	binary.LittleEndian.PutUint32(buf[b+16:b+20], 0x200)
	binary.LittleEndian.PutUint32(buf[b+20:b+24], bindRaw)

	plainText := make([]byte, textSize)
	for i := range plainText {
		plainText[i] = byte((i*29 + 7) & 0xff)
	}
	copy(plainText[0x20:0x23], []byte{1, 2, 3})
	plain := append(append([]byte(nil), plainText...), make([]byte, aes.BlockSize)...)
	key := make([]byte, 32)
	realIV := make([]byte, aes.BlockSize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	for i := range realIV {
		realIV[i] = byte(0xa0 + i)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, realIV).CryptBlocks(encrypted, plain)
	encryptedIV := make([]byte, aes.BlockSize)
	block.Encrypt(encryptedIV, realIV)
	copy(buf[textRaw:textRaw+textSize], encrypted[16:])

	decoded := make([]byte, 0xf0)
	binary.LittleEndian.PutUint32(decoded[4:8], steamStub31Signature)
	binary.LittleEndian.PutUint64(decoded[8:16], 0x140000000)
	binary.LittleEndian.PutUint64(decoded[32:40], origEP)
	binary.LittleEndian.PutUint64(decoded[72:80], textRVA)
	binary.LittleEndian.PutUint64(decoded[80:88], textSize)
	copy(decoded[88:120], key)
	copy(decoded[120:136], encryptedIV)
	copy(decoded[136:152], encrypted[:16])
	copy(buf[entryOff-0xf0:entryOff], encodeSteamHeader(decoded, 0x13579bdf))
	copy(buf[entryOff:], []byte{0xe8, 0, 0, 0, 0, 0x50, 0x53, 0x51, 0x52, 0x56, 0x57, 0x55, 0x41, 0x50})

	destub := append([]byte(nil), buf...)
	copy(destub[textRaw:textRaw+textSize], plainText)
	binary.LittleEndian.PutUint32(destub[optOff+16:optOff+20], origEP)
	final := append([]byte(nil), destub...)
	copy(final[textRaw+0x20:textRaw+0x23], []byte{4, 5, 6})
	profile := Profile{
		Key: "test", DisplayName: "Test", ExeName: "Game.exe", BundleDir: "TEST_PATCH",
		OriginalSHA: digest(buf), DestubSHA: digest(destub), FinalSHA: digest(final),
		Patches: []BytePatch{{RVA: textRVA + 0x20, Expected: []byte{1, 2, 3}, Replacement: []byte{4, 5, 6}, Label: "fixture patch"}},
	}
	return buf, final, profile
}

func TestPatchExecutableSteamStubAndDestubbed(t *testing.T) {
	original, expected, p := syntheticSteamStubProfile(t)
	got, state, err := PatchExecutable(original, p)
	if err != nil {
		t.Fatal(err)
	}
	if state != "original" || digest(got) != p.FinalSHA || string(got) != string(expected) {
		t.Fatalf("original patch mismatch: state=%s sha=%s", state, digest(got))
	}
	destub, err := destubSteamStub31(original)
	if err != nil {
		t.Fatal(err)
	}
	got, state, err = PatchExecutable(destub, p)
	if err != nil || state != "destubbed" || digest(got) != p.FinalSHA {
		t.Fatalf("destubbed patch mismatch: state=%s sha=%s err=%v", state, digest(got), err)
	}
	got, state, err = PatchExecutable(expected, p)
	if err != nil || state != "final" || string(got) != string(expected) {
		t.Fatalf("final idempotence mismatch: state=%s err=%v", state, err)
	}
}

func TestPatchExecutableLatePatchUpgradeAndNormalizedFinal(t *testing.T) {
	original, baseFinal, p := syntheticSteamStubProfile(t)
	pe, err := parsePE(baseFinal)
	if err != nil {
		t.Fatal(err)
	}
	lateRVA := uint32(0x1000 + 0x30)
	lateOff, err := rvaToOffset(lateRVA, pe.sections)
	if err != nil {
		t.Fatal(err)
	}
	expectedLate := baseFinal[lateOff]
	replacementLate := byte(0x90)
	if expectedLate == replacementLate {
		replacementLate = 0x91
	}
	p.LatePatches = []BytePatch{{RVA: lateRVA, Expected: []byte{expectedLate}, Replacement: []byte{replacementLate}, Label: "late fixture patch"}}

	// Original -> base patches -> guarded late patch.
	got, state, err := PatchExecutable(original, p)
	if err != nil || state != "original" {
		t.Fatalf("original late patch: state=%s err=%v", state, err)
	}
	if got[lateOff] != replacementLate || classifyExecutable(got, p) != "final" {
		t.Fatalf("late-patched output not recognized as final")
	}

	// Previously installed PHASE/base-final -> late patch only, no pristine restore.
	got2, state, err := PatchExecutable(baseFinal, p)
	if err != nil || state != "base-final" || string(got2) != string(got) {
		t.Fatalf("base-final upgrade mismatch: state=%s err=%v", state, err)
	}

	// Re-run is idempotent even though the new whole-file SHA is not hard-coded.
	got3, state, err := PatchExecutable(got, p)
	if err != nil || state != "final" || string(got3) != string(got) {
		t.Fatalf("late final idempotence mismatch: state=%s err=%v", state, err)
	}

	// Any unrelated byte drift remains unsupported after normalization.
	drift := append([]byte(nil), got...)
	drift[lateOff+5] ^= 0x01
	if classifyExecutable(drift, p) != "unknown" {
		t.Fatal("unrelated executable drift was incorrectly accepted")
	}
}

func TestBuildPlanInstallAndRollback(t *testing.T) {
	tmp := t.TempDir()
	game := filepath.Join(tmp, "game")
	bundleRoot := filepath.Join(tmp, "dist")
	bundle := filepath.Join(bundleRoot, "TEST_PATCH")
	backup := filepath.Join(tmp, "backup")
	originalExe, finalExe, p := syntheticSteamStubProfile(t)
	oldDat, newDat := []byte("old dat"), []byte("new dat")
	legacy := []byte("legacy changed")
	p.LegacyFiles = []LegacyFile{{RelativePath: "masterWindows/GxArchivedFile099.dat", PristineSHA: digest([]byte("pristine"))}}
	write(t, filepath.Join(game, p.ExeName), originalExe)
	write(t, filepath.Join(game, "masterWindows", "GxArchivedFile001.dat"), oldDat)
	write(t, filepath.Join(game, "masterWindows", "GxArchivedFile099.dat"), legacy)
	write(t, filepath.Join(bundle, "masterWindows", "GxArchivedFile001.dat"), newDat)
	m, _ := json.Marshal(PatchManifest{Files: []string{"GxArchivedFile001.dat"}})
	write(t, filepath.Join(bundle, "PATCH_MANIFEST.json"), m)

	old := Profiles["test"]
	Profiles["test"] = p
	defer func() {
		if old.Key == "" {
			delete(Profiles, "test")
		} else {
			Profiles["test"] = old
		}
	}()

	plan, err := BuildPlan(bundleRoot, map[string]string{"test": game})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 2 || len(plan.Operations[0].Generated) == 0 || len(plan.Warnings) != 1 {
		t.Fatalf("unexpected plan: ops=%d warnings=%v", len(plan.Operations), plan.Warnings)
	}
	if _, err := Install(plan, map[string]string{"test": game}, backup, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(game, p.ExeName))
	if string(got) != string(finalExe) {
		t.Fatal("generated executable not installed")
	}
	got, _ = os.ReadFile(filepath.Join(game, "masterWindows", "GxArchivedFile001.dat"))
	if string(got) != string(newDat) {
		t.Fatal("dat not installed")
	}
	if _, err := os.Stat(filepath.Join(backup, "receipt.json")); err != nil {
		t.Fatal(err)
	}

	// Drift a DAT after preflight: the generated EXE must be rolled back too.
	write(t, filepath.Join(game, p.ExeName), originalExe)
	write(t, filepath.Join(game, "masterWindows", "GxArchivedFile001.dat"), oldDat)
	write(t, filepath.Join(bundle, "masterWindows", "GxArchivedFile001.dat"), newDat)
	plan, err = BuildPlan(bundleRoot, map[string]string{"test": game})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(bundle, "masterWindows", "GxArchivedFile001.dat"), []byte("drift after plan"))
	if _, err = Install(plan, map[string]string{"test": game}, "", false); err == nil {
		t.Fatal("expected verification failure")
	}
	got, _ = os.ReadFile(filepath.Join(game, p.ExeName))
	if string(got) != string(originalExe) {
		t.Fatal("executable rollback failed")
	}
	got, _ = os.ReadFile(filepath.Join(game, "masterWindows", "GxArchivedFile001.dat"))
	if string(got) != string(oldDat) {
		t.Fatal("dat rollback failed")
	}
}

func TestDetectGamesFromConfiguredSteamRoot(t *testing.T) {
	steam := t.TempDir()
	common := filepath.Join(steam, "steamapps", "common")
	ms2 := filepath.Join(common, "Mary Skelter 2")
	finale := filepath.Join(common, "Mary Skelter Finale")
	write(t, filepath.Join(ms2, Profiles["ms2"].ExeName), []byte("exe"))
	write(t, filepath.Join(ms2, "masterWindows", "marker"), []byte("x"))
	write(t, filepath.Join(finale, Profiles["finale"].ExeName), []byte("exe"))
	write(t, filepath.Join(finale, "masterWindows", "marker"), []byte("x"))
	t.Setenv("MARY_SKELTER_STEAM_DIR", steam)

	got := DetectGames()
	if len(got["ms2"]) != 1 || got["ms2"][0] != ms2 {
		t.Fatalf("MS2 detection = %v", got["ms2"])
	}
	if len(got["finale"]) != 1 || got["finale"][0] != finale {
		t.Fatalf("Finale detection = %v", got["finale"])
	}
}

func TestCanonicalCachedExecutables(t *testing.T) {
	paths := map[string]string{
		"ms2":    os.Getenv("MARY_SKELTER_TEST_MS2_EXE"),
		"finale": os.Getenv("MARY_SKELTER_TEST_FINALE_EXE"),
	}
	ran := 0
	for key, path := range paths {
		if path == "" {
			continue
		}
		ran++
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out, state, err := PatchExecutable(data, Profiles[key])
		if err != nil {
			t.Fatalf("%s (%s): %v", key, state, err)
		}
		if got := classifyExecutable(out, Profiles[key]); got != "final" {
			t.Fatalf("%s patched executable state = %s, want final", key, got)
		}
	}
	if ran == 0 {
		t.Skip("canonical proprietary EXEs not supplied")
	}
}

func TestRequiredEmptyMarkerLifecycle(t *testing.T) {
	tmp := t.TempDir()
	game := filepath.Join(tmp, "game")
	bundleRoot := filepath.Join(tmp, "dist")
	bundle := filepath.Join(bundleRoot, "TEST_PATCH")
	originalExe, _, p := syntheticSteamStubProfile(t)
	p.RequiredMarkers = []string{"Minigame.dat"}
	oldDat, newDat := []byte("old dat"), []byte("new dat")
	write(t, filepath.Join(game, p.ExeName), originalExe)
	write(t, filepath.Join(game, "masterWindows", "GxArchivedFile001.dat"), oldDat)
	write(t, filepath.Join(bundle, "masterWindows", "GxArchivedFile001.dat"), newDat)
	m, _ := json.Marshal(PatchManifest{Files: []string{"GxArchivedFile001.dat"}})
	write(t, filepath.Join(bundle, "PATCH_MANIFEST.json"), m)

	old := Profiles["test"]
	Profiles["test"] = p
	defer func() {
		if old.Key == "" {
			delete(Profiles, "test")
		} else {
			Profiles["test"] = old
		}
	}()

	plan, err := BuildPlan(bundleRoot, map[string]string{"test": game})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range plan.Operations {
		if op.RelativePath == "Minigame.dat" {
			found = true
			if op.Action != "CREATE" || op.Generated == nil || len(op.Generated) != 0 || op.SourceSHA != emptySHA256 {
				t.Fatalf("bad marker op: %+v", op)
			}
		}
	}
	if !found {
		t.Fatal("missing required marker operation")
	}

	// Force a later DAT verification failure. The newly-created marker must be removed by rollback.
	write(t, filepath.Join(bundle, "masterWindows", "GxArchivedFile001.dat"), []byte("drift after plan"))
	if _, err := Install(plan, map[string]string{"test": game}, "", false); err == nil {
		t.Fatal("expected rollback-triggering failure")
	}
	if _, err := os.Stat(filepath.Join(game, "Minigame.dat")); !os.IsNotExist(err) {
		t.Fatalf("marker survived rollback: %v", err)
	}

	// Existing marker content is valid because the game contract is presence-only; preserve it.
	write(t, filepath.Join(game, "Minigame.dat"), []byte("user marker"))
	write(t, filepath.Join(bundle, "masterWindows", "GxArchivedFile001.dat"), newDat)
	plan, err = BuildPlan(bundleRoot, map[string]string{"test": game})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range plan.Operations {
		if op.RelativePath == "Minigame.dat" {
			t.Fatal("existing regular marker should not be replaced")
		}
	}
}

func TestCleanupAuditIsReadOnlyAndScoped(t *testing.T) {
	game := t.TempDir()
	voice := filepath.Join(game, "masterWindows", "Voice_EN.bra")
	movie1 := filepath.Join(game, "masterWindows", "MOVIE_EN_SUB", "400004.mp4")
	movie2 := filepath.Join(game, "masterWindows", "MOVIE_EN_SUB", "nested", "600000.mp4")
	unrelated := filepath.Join(game, "masterWindows", "Voice.bra")
	write(t, voice, []byte("english voice"))
	write(t, movie1, []byte("movie one"))
	write(t, movie2, []byte("movie two"))
	write(t, unrelated, []byte("keep me"))

	audit, err := AuditCleanupCandidates(map[string]string{"ms2": game})
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Files) != 3 {
		t.Fatalf("audit files = %d, want 3: %+v", len(audit.Files), audit.Files)
	}
	var total int64
	for _, f := range audit.Files {
		total += f.Size
	}
	if total != audit.TotalBytes {
		t.Fatalf("total=%d files=%d", audit.TotalBytes, total)
	}
	if got, _ := os.ReadFile(unrelated); string(got) != "keep me" {
		t.Fatal("audit modified unrelated file")
	}
	if got, _ := os.ReadFile(voice); string(got) != "english voice" {
		t.Fatal("audit modified candidate file")
	}
	raw, err := audit.JSON()
	if err != nil || !json.Valid(raw) {
		t.Fatalf("invalid audit JSON: %v", err)
	}
}

func TestEnglishCleanupAllowlistPlanAndRemove(t *testing.T) {
	game := t.TempDir()
	keep := filepath.Join(game, "masterWindows", "Voice.bra")
	a := filepath.Join(game, "masterWindows", "Voice_EN.bra")
	b := filepath.Join(game, "masterWindows", "MOVIE_EN_SUB", "400004.mp4")
	adata := []byte("english voice exact")
	bdata := []byte("english movie exact")
	write(t, a, adata)
	write(t, b, bdata)
	write(t, keep, []byte("keep"))

	old := englishCleanupAllowlist
	oldDirs := englishCleanupDirectories
	englishCleanupDirectories = nil
	englishCleanupAllowlist = []CleanupManifestEntry{
		{GameKey: "ms2", RelativePath: "masterWindows/Voice_EN.bra", Size: int64(len(adata)), SHA256: digest(adata)},
		{GameKey: "ms2", RelativePath: "masterWindows/MOVIE_EN_SUB/400004.mp4", Size: int64(len(bdata)), SHA256: digest(bdata)},
	}
	defer func() { englishCleanupAllowlist = old; englishCleanupDirectories = oldDirs }()

	roots := map[string]string{"ms2": game}
	plan, err := BuildCleanupPlan(roots)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 2 || plan.TotalBytes != int64(len(adata)+len(bdata)) {
		t.Fatalf("bad cleanup plan: %+v", plan)
	}
	receiptPath := filepath.Join(t.TempDir(), "cleanup_receipt.json")
	receipt, err := RemoveEnglishAssets(plan, roots, receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Files) != 2 || receipt.TotalBytes != plan.TotalBytes {
		t.Fatalf("bad cleanup receipt: %+v", receipt)
	}
	for _, path := range []string{a, b} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup target survived: %s (%v)", path, err)
		}
	}
	if got, _ := os.ReadFile(keep); string(got) != "keep" {
		t.Fatal("cleanup touched unrelated file")
	}
	if raw, err := os.ReadFile(receiptPath); err != nil || !json.Valid(raw) {
		t.Fatalf("cleanup receipt invalid: %v", err)
	}
}

func TestEnglishCleanupFailsClosedBeforeDeletion(t *testing.T) {
	game := t.TempDir()
	a := filepath.Join(game, "masterWindows", "Voice_EN.bra")
	b := filepath.Join(game, "masterWindows", "MOVIE_EN_SUB", "400004.mp4")
	adata := []byte("voice")
	bdata := []byte("movie")
	write(t, a, adata)
	write(t, b, bdata)

	old := englishCleanupAllowlist
	oldDirs := englishCleanupDirectories
	englishCleanupDirectories = nil
	englishCleanupAllowlist = []CleanupManifestEntry{
		{GameKey: "ms2", RelativePath: "masterWindows/Voice_EN.bra", Size: int64(len(adata)), SHA256: digest(adata)},
		{GameKey: "ms2", RelativePath: "masterWindows/MOVIE_EN_SUB/400004.mp4", Size: int64(len(bdata)), SHA256: digest(bdata)},
	}
	defer func() { englishCleanupAllowlist = old; englishCleanupDirectories = oldDirs }()

	roots := map[string]string{"ms2": game}
	plan, err := BuildCleanupPlan(roots)
	if err != nil {
		t.Fatal(err)
	}
	write(t, b, []byte("drift"))
	if _, err := RemoveEnglishAssets(plan, roots, ""); err == nil {
		t.Fatal("expected cleanup preflight failure")
	}
	if got, _ := os.ReadFile(a); string(got) != string(adata) {
		t.Fatal("first file was deleted before full cleanup preflight completed")
	}
	if got, _ := os.ReadFile(b); string(got) != "drift" {
		t.Fatal("drifted file changed unexpectedly")
	}
}

func TestCleanupPotentialBytesSelectedGames(t *testing.T) {
	old := englishCleanupAllowlist
	oldDirs := englishCleanupDirectories
	englishCleanupDirectories = nil
	englishCleanupAllowlist = []CleanupManifestEntry{
		{GameKey: "ms2", Size: 10},
		{GameKey: "finale", Size: 20},
	}
	defer func() { englishCleanupAllowlist = old; englishCleanupDirectories = oldDirs }()
	if got := CleanupPotentialBytes(map[string]string{"ms2": "/x"}); got != 10 {
		t.Fatalf("MS2 potential = %d", got)
	}
	if got := CleanupPotentialBytes(map[string]string{"ms2": "/x", "finale": "/y"}); got != 30 {
		t.Fatalf("combined potential = %d", got)
	}
}

func TestCanonicalEnglishCleanupManifestSummary(t *testing.T) {
	if len(englishCleanupAllowlist) != 2 {
		t.Fatalf("cleanup allow-list entries = %d, want MS2 Voice_EN + Finale Sound_EN", len(englishCleanupAllowlist))
	}
	ms2, finale := englishCleanupAllowlist[0], englishCleanupAllowlist[1]
	if ms2.GameKey != "ms2" || ms2.RelativePath != "masterWindows/Voice_EN.bra" || ms2.Size != 282182260 {
		t.Fatalf("unexpected MS2 cleanup entry: %+v", ms2)
	}
	if finale.GameKey != "finale" || finale.RelativePath != "masterWindows/Sound_EN.bra" || finale.Size != 209778703 {
		t.Fatalf("unexpected Finale cleanup entry: %+v", finale)
	}
	if len(ms2.SHA256) != 64 || len(finale.SHA256) != 64 {
		t.Fatalf("invalid cleanup SHA entries: %+v %+v", ms2, finale)
	}
	if len(englishCleanupDirectories) != 1 {
		t.Fatalf("cleanup directory specs = %d, want MS2 MOVIE_EN_SUB", len(englishCleanupDirectories))
	}
	d := englishCleanupDirectories[0]
	want := []string{"400004.mp4", "600000.mp4", "700000.mp4"}
	if d.GameKey != "ms2" || d.RelativePath != "masterWindows/MOVIE_EN_SUB" || !reflect.DeepEqual(d.ExpectedFiles, want) {
		t.Fatalf("unexpected cleanup directory spec: %+v", d)
	}
	if got := Profiles["ms2"].CleanupAuditRoots; len(got) != 2 || got[0] != "masterWindows/Voice_EN.bra" || got[1] != "masterWindows/MOVIE_EN_SUB" {
		t.Fatalf("MS2 cleanup audit roots = %+v", got)
	}
	if got := Profiles["finale"].CleanupAuditRoots; len(got) != 1 || got[0] != "masterWindows/Sound_EN.bra" {
		t.Fatalf("Finale cleanup audit roots = %+v", got)
	}
}

func TestMS2MovieENSubWholeDirectoryPurgeAndUnknownEntryRejection(t *testing.T) {
	game := t.TempDir()
	dir := filepath.Join(game, "masterWindows", "MOVIE_EN_SUB")
	for _, name := range []string{"400004.mp4", "600000.mp4", "700000.mp4"} {
		write(t, filepath.Join(dir, name), []byte("dub-"+name))
	}
	oldFiles := englishCleanupAllowlist
	oldDirs := englishCleanupDirectories
	englishCleanupAllowlist = nil
	englishCleanupDirectories = []CleanupDirectorySpec{{
		GameKey: "ms2", RelativePath: "masterWindows/MOVIE_EN_SUB",
		ExpectedFiles: []string{"400004.mp4", "600000.mp4", "700000.mp4"},
	}}
	defer func() { englishCleanupAllowlist = oldFiles; englishCleanupDirectories = oldDirs }()

	roots := map[string]string{"ms2": game}
	plan, err := BuildCleanupPlan(roots)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Directories) != 1 || len(plan.Directories[0].Files) != 3 {
		t.Fatalf("bad MOVIE_EN_SUB plan: %+v", plan)
	}
	receipt, err := RemoveEnglishAssets(plan, roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Files) != 3 || len(receipt.Directories) != 1 {
		t.Fatalf("bad directory cleanup receipt: %+v", receipt)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("MOVIE_EN_SUB directory survived purge: %v", err)
	}

	// Recreate the known directory with one unknown extra. Fail closed before any
	// live path is renamed or deleted.
	for _, name := range []string{"400004.mp4", "600000.mp4", "700000.mp4"} {
		write(t, filepath.Join(dir, name), []byte("dub-"+name))
	}
	write(t, filepath.Join(dir, "unexpected.mp4"), []byte("do not touch"))
	if _, err := BuildCleanupPlan(roots); err == nil {
		t.Fatal("expected unknown MOVIE_EN_SUB member rejection")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "unexpected.mp4")); err != nil || string(got) != "do not touch" {
		t.Fatalf("unknown entry was touched: %q %v", got, err)
	}
}

func TestInstallWithoutPersistentBackupCommitsAndCleansRollbackState(t *testing.T) {
	tmp := t.TempDir()
	game := filepath.Join(tmp, "game")
	bundleRoot := filepath.Join(tmp, "dist")
	bundle := filepath.Join(bundleRoot, "TEST_PATCH")
	originalExe, finalExe, p := syntheticSteamStubProfile(t)
	oldDat, newDat := []byte("old dat"), []byte("new dat")
	write(t, filepath.Join(game, p.ExeName), originalExe)
	write(t, filepath.Join(game, "masterWindows", "GxArchivedFile001.dat"), oldDat)
	write(t, filepath.Join(bundle, "masterWindows", "GxArchivedFile001.dat"), newDat)
	m, _ := json.Marshal(PatchManifest{Files: []string{"GxArchivedFile001.dat"}})
	write(t, filepath.Join(bundle, "PATCH_MANIFEST.json"), m)
	old := Profiles["test"]
	Profiles["test"] = p
	defer func() {
		if old.Key == "" {
			delete(Profiles, "test")
		} else {
			Profiles["test"] = old
		}
	}()
	plan, err := BuildPlan(bundleRoot, map[string]string{"test": game})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Install(plan, map[string]string{"test": game}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(game, p.ExeName)); string(got) != string(finalExe) {
		t.Fatal("final exe missing")
	}
	if got, _ := os.ReadFile(filepath.Join(game, "masterWindows", "GxArchivedFile001.dat")); string(got) != string(newDat) {
		t.Fatal("final dat missing")
	}
	for _, op := range r.Operations {
		if op.BackupPath != "" {
			t.Fatalf("unexpected persistent backup path: %+v", op)
		}
	}
	err = filepath.WalkDir(game, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), ".unburger-rollback.") {
			t.Fatalf("transaction rollback file survived commit: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFinaleRemuxStageRequiresExactFinalizedSet(t *testing.T) {
	root := t.TempDir()
	objects := make([]ReleaseStageObject, 0, finaleExpectedRemuxMovies)
	for i := 0; i < finaleExpectedRemuxMovies; i++ {
		name := fmt.Sprintf("%06d", 100001+i)
		logical := filepath.ToSlash(filepath.Join("movie_audio", "finale", name+".a0.m4a"))
		payload := []byte("payload-" + name)
		write(t, filepath.Join(root, "objects", filepath.FromSlash(logical)), payload)
		objects = append(objects, ReleaseStageObject{
			Kind: "movie_jp_audio", Game: "finale", TargetMovie: "masterWindows/MOVIE_EN/" + name + ".mp4",
			AudioOrdinal: 0, Codec: "aac", SourceMovieENSHA256: strings.Repeat("1", 64), SourceVideoSHA256: strings.Repeat("3", 64),
			LogicalPath: logical, Size: int64(len(payload)), SHA256: digest(payload),
			RemuxTargetSize: 1234, RemuxTargetSHA256: strings.Repeat("2", 64),
		})
	}
	m := ReleaseStageManifest{SchemaVersion: 2, ContainerFormat: ".unburger-assets v3", ContainerCount: 1, Objects: objects}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "ASSET_STAGE_MANIFEST.json"), raw)
	assets, err := finaleRemuxAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != finaleExpectedRemuxMovies {
		t.Fatalf("remux assets=%d", len(assets))
	}
	if assets[0].RelativePath != "masterWindows/MOVIE_EN/100001.mp4" {
		t.Fatalf("first=%+v", assets[0])
	}

	// Removing the finalized target metadata must fail before an updater plan can
	// touch the user's Finale installation.
	m.Objects[0].RemuxTargetSHA256 = ""
	raw, _ = json.Marshal(m)
	write(t, filepath.Join(root, "ASSET_STAGE_MANIFEST.json"), raw)
	if _, err := finaleRemuxAssets(root); err == nil {
		t.Fatal("expected incomplete remux metadata rejection")
	}
}

func TestInstallBestEffortRemuxFailureDoesNotRollbackCriticalWork(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "game")
	critical := filepath.Join(game, "critical.bin")
	movie := filepath.Join(game, "masterWindows", "MOVIE_EN", "100001.mp4")
	payload := filepath.Join(root, "bad.m4a")
	write(t, critical, []byte("old-critical"))
	write(t, movie, []byte("original-movie"))
	write(t, payload, []byte("not-an-m4a"))
	plan := Plan{Operations: []Operation{
		{GameKey: "test", RelativePath: "critical.bin", Action: "PATCH", Generated: []byte("new-critical"), SourceSHA: digest([]byte("new-critical"))},
		{GameKey: "test", RelativePath: "masterWindows/MOVIE_EN/100001.mp4", Action: "REMUX", BestEffort: true,
			PayloadPath: payload, PayloadSHA: digest([]byte("not-an-m4a")), ExpectedBeforeSHA: digest([]byte("original-movie")), SourceSHA: strings.Repeat("f", 64)},
	}}
	backup := filepath.Join(root, "backup")
	r, err := Install(plan, map[string]string{"test": game}, backup, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(critical); string(got) != "new-critical" {
		t.Fatalf("critical work rolled back: %q", got)
	}
	if got, _ := os.ReadFile(movie); string(got) != "original-movie" {
		t.Fatalf("failed movie was not restored: %q", got)
	}
	if len(r.SoftFailures) != 1 || r.SoftFailures[0].RelativePath != "masterWindows/MOVIE_EN/100001.mp4" {
		t.Fatalf("soft failures = %+v", r.SoftFailures)
	}
	if len(r.Operations) != 1 || r.Operations[0].RelativePath != "critical.bin" {
		t.Fatalf("operations = %+v", r.Operations)
	}
	if _, err := os.Stat(filepath.Join(backup, "test", "masterWindows", "MOVIE_EN", "100001.mp4")); !os.IsNotExist(err) {
		t.Fatalf("failed remux source survived in persistent backup: %v", err)
	}
}

func TestRunReceiptIndependentFromBackupAndSummary(t *testing.T) {
	patch := Receipt{
		SchemaVersion: 3,
		Operations: []Operation{
			{GameKey: "ms2", RelativePath: "MarySkelter2.exe", Action: "PATCH"},
			{GameKey: "finale", RelativePath: "masterWindows/MOVIE_EN/100001.mp4", Action: "REMUX"},
		},
		SoftFailures: []OperationFailure{{GameKey: "finale", RelativePath: "masterWindows/MOVIE_EN/100002.mp4", Action: "REMUX", Error: "fixture failure"}},
	}
	cleanup := CleanupReceipt{SchemaVersion: 2, Files: []CleanupManifestEntry{{GameKey: "ms2", RelativePath: "masterWindows/Voice_EN.bra", Size: 10}}, TotalBytes: 10}
	r := NewRunReceipt("repair", "0.1.0", nil, map[string]string{"ms2": "/game"}, patch, true, cleanup, nil, "")
	if r.Mode != "repair" || r.PersistentBackup != "" || r.Cleanup == nil || len(r.Patch.SoftFailures) != 1 {
		t.Fatalf("bad run receipt: %+v", r)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := WriteRunReceipt(path, r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !json.Valid(raw) {
		t.Fatalf("invalid run receipt: %v", err)
	}
	summary := FormatRunSummary(patch, true, cleanup, nil)
	for _, want := range []string{"1 critical operation(s)", "1 remuxed, 1 unresolved", "1 file(s), 10 B reclaimed"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
}

func TestFormatPlanDoesNotClaimCurrentWhenMovieUnresolved(t *testing.T) {
	p := Plan{SoftFailures: []OperationFailure{{GameKey: "finale", RelativePath: "masterWindows/MOVIE_EN/100001.mp4", Action: "REMUX", Error: "unsupported state"}}}
	out := FormatPlan(p)
	if strings.Contains(out, "Already current") {
		t.Fatalf("unresolved movie incorrectly reported current: %s", out)
	}
	if !strings.Contains(out, "No critical files need changes") || !strings.Contains(out, "WARN") {
		t.Fatalf("missing unresolved-state wording: %s", out)
	}
}

func TestDefaultRunReceiptPathIsPerUserState(t *testing.T) {
	path, err := DefaultRunReceiptPath("repair")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) == "" || !strings.Contains(filepath.ToSlash(path), "mary-skelter-unburger/receipts/") || !strings.HasSuffix(path, "-repair.json") {
		t.Fatalf("unexpected receipt path: %s", path)
	}
}

func TestProgressCallbacksAreDeterministic(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "game")
	write(t, filepath.Join(game, "a.bin"), []byte("old-a"))
	write(t, filepath.Join(game, "b.bin"), []byte("old-b"))
	plan := Plan{Operations: []Operation{
		{GameKey: "test", RelativePath: "a.bin", Action: "PATCH", Generated: []byte("new-a"), SourceSHA: digest([]byte("new-a"))},
		{GameKey: "test", RelativePath: "b.bin", Action: "PATCH", Generated: []byte("new-b"), SourceSHA: digest([]byte("new-b"))},
	}}
	var events []ProgressEvent
	if _, err := installWithAssets(plan, map[string]string{"test": game}, "", false, nil, func(e ProgressEvent) {
		events = append(events, e)
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 { // ready, start+verified per operation
		t.Fatalf("events=%d: %+v", len(events), events)
	}
	if events[0].Done != 0 || events[0].Total != 2 || events[len(events)-1].Done != 2 || events[len(events)-1].Total != 2 {
		t.Fatalf("bad progress endpoints: first=%+v last=%+v", events[0], events[len(events)-1])
	}
	if events[len(events)-1].Detail != "Verified" {
		t.Fatalf("last detail=%q", events[len(events)-1].Detail)
	}
}

func TestCleanupProgressCountsPhysicalFiles(t *testing.T) {
	game := t.TempDir()
	aData := []byte("voice")
	write(t, filepath.Join(game, "masterWindows", "Voice_EN.bra"), aData)
	for _, name := range []string{"400004.mp4", "600000.mp4", "700000.mp4"} {
		write(t, filepath.Join(game, "masterWindows", "MOVIE_EN_SUB", name), []byte("dub-"+name))
	}
	oldFiles := englishCleanupAllowlist
	oldDirs := englishCleanupDirectories
	englishCleanupAllowlist = []CleanupManifestEntry{{GameKey: "ms2", RelativePath: "masterWindows/Voice_EN.bra", Size: int64(len(aData)), SHA256: digest(aData)}}
	englishCleanupDirectories = []CleanupDirectorySpec{{GameKey: "ms2", RelativePath: "masterWindows/MOVIE_EN_SUB", ExpectedFiles: []string{"400004.mp4", "600000.mp4", "700000.mp4"}}}
	defer func() { englishCleanupAllowlist = oldFiles; englishCleanupDirectories = oldDirs }()

	roots := map[string]string{"ms2": game}
	var scan []ProgressEvent
	plan, err := BuildCleanupPlanWithProgress(roots, func(e ProgressEvent) { scan = append(scan, e) })
	if err != nil {
		t.Fatal(err)
	}
	if got := scan[len(scan)-1]; got.Done != 4 || got.Total != 4 {
		t.Fatalf("cleanup scan endpoint=%+v", got)
	}
	var remove []ProgressEvent
	if _, err := RemoveEnglishAssetsWithProgress(plan, roots, "", func(e ProgressEvent) { remove = append(remove, e) }); err != nil {
		t.Fatal(err)
	}
	if got := remove[len(remove)-1]; got.Done != 4 || got.Total != 4 || got.Detail != "Committed" {
		t.Fatalf("cleanup remove endpoint=%+v", got)
	}
}
