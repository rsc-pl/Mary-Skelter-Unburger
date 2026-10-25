package updater

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const steamStub31Signature uint32 = 0xC0DEC0DF

type peSection struct {
	name        string
	virtualSize uint32
	virtualAddr uint32
	rawSize     uint32
	rawPtr      uint32
}

type peImage struct {
	optionalHeader int
	entryRVA       uint32
	imageBase      uint64
	sections       []peSection
}

func sha256Bytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func parsePE(data []byte) (peImage, error) {
	if len(data) < 0x40 || string(data[:2]) != "MZ" {
		return peImage{}, fmt.Errorf("not an MZ executable")
	}
	peOff := int(binary.LittleEndian.Uint32(data[0x3c:0x40]))
	if peOff < 0 || peOff+24 > len(data) || string(data[peOff:peOff+4]) != "PE\x00\x00" {
		return peImage{}, fmt.Errorf("not a PE executable")
	}
	coff := peOff + 4
	nsects := int(binary.LittleEndian.Uint16(data[coff+2 : coff+4]))
	sizeOpt := int(binary.LittleEndian.Uint16(data[coff+16 : coff+18]))
	opt := coff + 20
	if opt+sizeOpt > len(data) || sizeOpt < 32 || binary.LittleEndian.Uint16(data[opt:opt+2]) != 0x20b {
		return peImage{}, fmt.Errorf("expected PE32+ / x64 executable")
	}
	entry := binary.LittleEndian.Uint32(data[opt+16 : opt+20])
	imageBase := binary.LittleEndian.Uint64(data[opt+24 : opt+32])
	secOff := opt + sizeOpt
	if nsects < 1 || secOff+nsects*40 > len(data) {
		return peImage{}, fmt.Errorf("invalid PE section table")
	}
	sections := make([]peSection, 0, nsects)
	for i := 0; i < nsects; i++ {
		o := secOff + i*40
		nameEnd := o
		for nameEnd < o+8 && data[nameEnd] != 0 {
			nameEnd++
		}
		sections = append(sections, peSection{
			name:        string(data[o:nameEnd]),
			virtualSize: binary.LittleEndian.Uint32(data[o+8 : o+12]),
			virtualAddr: binary.LittleEndian.Uint32(data[o+12 : o+16]),
			rawSize:     binary.LittleEndian.Uint32(data[o+16 : o+20]),
			rawPtr:      binary.LittleEndian.Uint32(data[o+20 : o+24]),
		})
	}
	return peImage{optionalHeader: opt, entryRVA: entry, imageBase: imageBase, sections: sections}, nil
}

func rvaToOffset(rva uint32, sections []peSection) (int, error) {
	for _, s := range sections {
		span := s.virtualSize
		if s.rawSize > span {
			span = s.rawSize
		}
		if rva >= s.virtualAddr && rva < s.virtualAddr+span {
			return int(s.rawPtr + (rva - s.virtualAddr)), nil
		}
	}
	return 0, fmt.Errorf("RVA %#x is not mapped", rva)
}

func steamXORHeader(raw []byte) error {
	if len(raw) != 0xf0 {
		return fmt.Errorf("SteamStub 3.1 x64 header must be 0xF0 bytes")
	}
	key := binary.LittleEndian.Uint32(raw[:4])
	for off := 4; off < len(raw); off += 4 {
		v := binary.LittleEndian.Uint32(raw[off : off+4])
		binary.LittleEndian.PutUint32(raw[off:off+4], v^key)
		key = v
	}
	return nil
}

func decryptSteamPayload(key, encryptedIV, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 || len(encryptedIV) != aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid SteamStub AES parameters")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	realIV := make([]byte, aes.BlockSize)
	block.Decrypt(realIV, encryptedIV)
	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, realIV).CryptBlocks(out, ciphertext)
	return out, nil
}

func destubSteamStub31(input []byte) ([]byte, error) {
	buf := append([]byte(nil), input...)
	pe, err := parsePE(buf)
	if err != nil {
		return nil, err
	}
	entryOff, err := rvaToOffset(pe.entryRVA, pe.sections)
	if err != nil {
		return nil, err
	}
	sig := []byte{0xe8, 0, 0, 0, 0, 0x50, 0x53, 0x51, 0x52, 0x56, 0x57, 0x55, 0x41, 0x50}
	if entryOff+len(sig) > len(buf) || string(buf[entryOff:entryOff+len(sig)]) != string(sig) {
		return nil, fmt.Errorf("entry point does not match expected SteamStub 3.1 x64 signature at %#x", pe.entryRVA)
	}
	hdrOff := entryOff - 0xf0
	if hdrOff < 0 {
		return nil, fmt.Errorf("invalid SteamStub header location")
	}
	hdr := append([]byte(nil), buf[hdrOff:entryOff]...)
	if err := steamXORHeader(hdr); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(hdr[4:8]) != steamStub31Signature {
		return nil, fmt.Errorf("unexpected decoded SteamStub signature")
	}
	if binary.LittleEndian.Uint64(hdr[8:16]) != pe.imageBase {
		return nil, fmt.Errorf("SteamStub image base mismatch")
	}
	origEP := binary.LittleEndian.Uint64(hdr[32:40])
	codeVA := binary.LittleEndian.Uint64(hdr[72:80])
	codeRawSize := binary.LittleEndian.Uint64(hdr[80:88])
	var code *peSection
	for i := range pe.sections {
		s := &pe.sections[i]
		span := uint64(s.virtualSize)
		if uint64(s.rawSize) > span {
			span = uint64(s.rawSize)
		}
		if codeVA >= uint64(s.virtualAddr) && codeVA < uint64(s.virtualAddr)+span {
			code = s
			break
		}
	}
	if code == nil || code.name != ".text" || uint64(code.rawSize) != codeRawSize {
		return nil, fmt.Errorf("SteamStub code section mismatch")
	}
	start, end := int(code.rawPtr), int(code.rawPtr+code.rawSize)
	if start < 0 || end > len(buf) {
		return nil, fmt.Errorf("SteamStub .text range is outside file")
	}
	enc := make([]byte, 0, 16+(end-start))
	enc = append(enc, hdr[136:152]...)
	enc = append(enc, buf[start:end]...)
	dec, err := decryptSteamPayload(hdr[88:120], hdr[120:136], enc)
	if err != nil {
		return nil, err
	}
	if len(dec) < int(code.rawSize) {
		return nil, fmt.Errorf("decrypted SteamStub payload is unexpectedly short")
	}
	copy(buf[start:end], dec[:code.rawSize])
	binary.LittleEndian.PutUint32(buf[pe.optionalHeader+16:pe.optionalHeader+20], uint32(origEP))
	return buf, nil
}

func patchRVA(buf []byte, patch BytePatch) error {
	pe, err := parsePE(buf)
	if err != nil {
		return err
	}
	off, err := rvaToOffset(patch.RVA, pe.sections)
	if err != nil {
		return err
	}
	if off+len(patch.Expected) > len(buf) {
		return fmt.Errorf("%s: patch is outside file", patch.Label)
	}
	got := buf[off : off+len(patch.Expected)]
	if string(got) != string(patch.Expected) {
		return fmt.Errorf("%s: expected %x at RVA %#x, got %x", patch.Label, patch.Expected, patch.RVA, got)
	}
	copy(buf[off:off+len(patch.Replacement)], patch.Replacement)
	return nil
}

func matchesLatePatchedFinal(input []byte, p Profile) bool {
	if len(p.LatePatches) == 0 {
		return false
	}
	buf := append([]byte(nil), input...)
	for _, patch := range p.LatePatches {
		if len(patch.Expected) != len(patch.Replacement) {
			return false
		}
		pe, err := parsePE(buf)
		if err != nil {
			return false
		}
		off, err := rvaToOffset(patch.RVA, pe.sections)
		if err != nil || off+len(patch.Replacement) > len(buf) {
			return false
		}
		if string(buf[off:off+len(patch.Replacement)]) != string(patch.Replacement) {
			return false
		}
		copy(buf[off:off+len(patch.Expected)], patch.Expected)
	}
	return sha256Bytes(buf) == p.FinalSHA
}

func classifyExecutable(input []byte, p Profile) string {
	sha := sha256Bytes(input)
	switch sha {
	case p.OriginalSHA:
		return "original"
	case p.DestubSHA:
		return "destubbed"
	case p.FinalSHA:
		if len(p.LatePatches) == 0 {
			return "final"
		}
		return "base-final"
	default:
		if matchesLatePatchedFinal(input, p) {
			return "final"
		}
		return "unknown"
	}
}

func PatchExecutable(input []byte, p Profile) ([]byte, string, error) {
	state := classifyExecutable(input, p)
	if state == "final" {
		return append([]byte(nil), input...), state, nil
	}
	if state == "unknown" {
		return nil, state, fmt.Errorf("%s executable is unsupported", p.DisplayName)
	}
	buf := append([]byte(nil), input...)
	if state == "original" {
		var err error
		buf, err = destubSteamStub31(buf)
		if err != nil {
			return nil, state, err
		}
		if got := sha256Bytes(buf); got != p.DestubSHA {
			return nil, state, fmt.Errorf("%s destub verification failed: %s != %s", p.DisplayName, got, p.DestubSHA)
		}
	}
	if state == "original" || state == "destubbed" {
		for _, patch := range p.Patches {
			if err := patchRVA(buf, patch); err != nil {
				return nil, state, err
			}
		}
		if got := sha256Bytes(buf); got != p.FinalSHA {
			return nil, state, fmt.Errorf("%s base-final executable verification failed: %s != %s", p.DisplayName, got, p.FinalSHA)
		}
	}
	for _, patch := range p.LatePatches {
		if err := patchRVA(buf, patch); err != nil {
			return nil, state, err
		}
	}
	if len(p.LatePatches) == 0 {
		if got := sha256Bytes(buf); got != p.FinalSHA {
			return nil, state, fmt.Errorf("%s final executable verification failed: %s != %s", p.DisplayName, got, p.FinalSHA)
		}
	} else if !matchesLatePatchedFinal(buf, p) {
		return nil, state, fmt.Errorf("%s late-patched final executable verification failed", p.DisplayName)
	}
	return buf, state, nil
}
