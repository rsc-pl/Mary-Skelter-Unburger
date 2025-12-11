#!/usr/bin/env python3
"""Mary Skelter 2 PC QoL patcher.

Target: Steam PC MarySkelter2.exe, SHA-256
07b97dd3b4aa9e50249a2fcfb619b31822eb7219ebeae150b9b9c421a5b5834c

What it does:
  1. Detects and removes SteamStub 3.1 x64 wrapper in-place in a copy:
     - decodes the 0xF0 SteamStub header,
     - AES-256-CBC decrypts .text,
     - restores the original entry point,
     - leaves .bind physically present but unused (same known-good method as tested build).
  2. Skips startup logos by forcing LogoStage state 27.
  3. Bypasses the boot voice-language prompt at its creation site, continuing through
     the exact descriptor normally used after a valid choice. Default voice state 0 = Japanese.

Requires Python 3 and either:
  - the `cryptography` Python package, OR
  - an `openssl` executable in PATH.
No game data files are modified.
"""
from __future__ import annotations

import argparse
import hashlib
import shutil
import struct
import subprocess
import sys
import tempfile
from pathlib import Path

TARGET_SHA256 = "07b97dd3b4aa9e50249a2fcfb619b31822eb7219ebeae150b9b9c421a5b5834c"
DESTUB_SHA256 = "c24df22f43524e236925bf38ab8cf9d6171ba3518e6f1b5828bdf740d95cffbc"
FINAL_SHA256 = "81b6641fadff2767bb23492b1c6b0a9cac645a7496c65ed9d8eef2144ca94fa8"
STEAMSTUB31_SIG = 0xC0DEC0DF

# Confirmed patch sites in the restored/original game image.
LOGO_RVA = 0x25B2BB
LOGO_EXPECTED = bytes.fromhex("8b4140")
LOGO_PATCH = bytes.fromhex("6a1b58")  # push 0x1b ; pop rax => state 27

VOICE_BOOT_RVA = 0x2AA27A
VOICE_EXPECTED = bytes.fromhex("488d0daf1b7100")  # LEA RCX, descriptor 0x1409BBE30 (VA prompt)
VOICE_PATCH = bytes.fromhex("488d0def1a7100")     # LEA RCX, descriptor 0x1409BBD70 (normal continuation)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def parse_pe(buf: bytearray):
    if buf[:2] != b"MZ":
        raise ValueError("not an MZ executable")
    pe = struct.unpack_from("<I", buf, 0x3C)[0]
    if buf[pe:pe+4] != b"PE\0\0":
        raise ValueError("not a PE executable")
    coff = pe + 4
    nsects = struct.unpack_from("<H", buf, coff + 2)[0]
    sizeopt = struct.unpack_from("<H", buf, coff + 16)[0]
    opt = coff + 20
    if struct.unpack_from("<H", buf, opt)[0] != 0x20B:
        raise ValueError("expected PE32+ / x64 executable")
    entry = struct.unpack_from("<I", buf, opt + 16)[0]
    imagebase = struct.unpack_from("<Q", buf, opt + 24)[0]
    sec = opt + sizeopt
    sections = []
    for i in range(nsects):
        o = sec + i * 40
        name = bytes(buf[o:o+8]).split(b"\0", 1)[0].decode("ascii", "replace")
        vsize, vaddr, rsize, rptr = struct.unpack_from("<IIII", buf, o + 8)
        sections.append({"name": name, "vsize": vsize, "vaddr": vaddr,
                         "rsize": rsize, "rptr": rptr, "hoff": o})
    return pe, opt, entry, imagebase, sections


def rva_to_off(rva: int, sections) -> int:
    for s in sections:
        if s["vaddr"] <= rva < s["vaddr"] + max(s["vsize"], s["rsize"]):
            return s["rptr"] + (rva - s["vaddr"])
    raise ValueError(f"RVA {rva:#x} is not mapped")


def steamxor_header(raw: bytearray) -> None:
    if len(raw) != 0xF0:
        raise ValueError("SteamStub 3.1 x64 header must be 0xF0 bytes")
    key = struct.unpack_from("<I", raw, 0)[0]
    for off in range(4, len(raw), 4):
        val = struct.unpack_from("<I", raw, off)[0]
        struct.pack_into("<I", raw, off, val ^ key)
        key = val


def aes_decrypt(key: bytes, encrypted_iv: bytes, ciphertext: bytes) -> bytes:
    # SteamStub stores an AES-encrypted IV; first decrypt it in ECB, then decrypt
    # stolen bytes + encrypted .text with AES-256-CBC/no padding.
    try:
        from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
        dec = Cipher(algorithms.AES(key), modes.ECB()).decryptor()
        real_iv = dec.update(encrypted_iv) + dec.finalize()
        dec = Cipher(algorithms.AES(key), modes.CBC(real_iv)).decryptor()
        return dec.update(ciphertext) + dec.finalize()
    except ImportError:
        openssl = shutil.which("openssl")
        if not openssl:
            raise RuntimeError(
                "Need either Python package `cryptography` or `openssl` in PATH to decrypt SteamStub."
            )
        with tempfile.TemporaryDirectory() as td_s:
            td = Path(td_s)
            (td / "iv.bin").write_bytes(encrypted_iv)
            subprocess.run([
                openssl, "enc", "-aes-256-ecb", "-d", "-nopad",
                "-K", key.hex(), "-in", str(td / "iv.bin"), "-out", str(td / "iv.dec")
            ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            real_iv = (td / "iv.dec").read_bytes()
            (td / "enc.bin").write_bytes(ciphertext)
            subprocess.run([
                openssl, "enc", "-aes-256-cbc", "-d", "-nopad",
                "-K", key.hex(), "-iv", real_iv.hex(),
                "-in", str(td / "enc.bin"), "-out", str(td / "dec.bin")
            ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            return (td / "dec.bin").read_bytes()


def destub(buf: bytearray) -> None:
    _pe, opt, entry, imagebase, sections = parse_pe(buf)
    entry_off = rva_to_off(entry, sections)

    # SteamStub 3.1 x64 entry signature used by this build.
    sig = bytes.fromhex("e800000000505351525657554150")
    if bytes(buf[entry_off:entry_off+len(sig)]) != sig:
        raise ValueError(f"entry point does not match expected SteamStub 3.1 x64 signature at {entry:#x}")

    hdr_off = entry_off - 0xF0
    if hdr_off < 0:
        raise ValueError("invalid SteamStub header location")
    hdr = bytearray(buf[hdr_off:entry_off])
    steamxor_header(hdr)

    signature = struct.unpack_from("<I", hdr, 4)[0]
    if signature != STEAMSTUB31_SIG:
        raise ValueError(f"unexpected decoded SteamStub signature {signature:#x}")

    hdr_imagebase = struct.unpack_from("<Q", hdr, 8)[0]
    orig_ep = struct.unpack_from("<Q", hdr, 32)[0]
    code_va = struct.unpack_from("<Q", hdr, 72)[0]
    code_raw_size = struct.unpack_from("<Q", hdr, 80)[0]
    aes_key = bytes(hdr[88:120])
    aes_iv = bytes(hdr[120:136])
    stolen = bytes(hdr[136:152])

    if hdr_imagebase != imagebase:
        raise ValueError(f"SteamStub image base mismatch: {hdr_imagebase:#x} != {imagebase:#x}")

    code = None
    for s in sections:
        if s["vaddr"] <= code_va < s["vaddr"] + max(s["vsize"], s["rsize"]):
            code = s
            break
    if not code or code["name"] != ".text":
        raise ValueError("SteamStub code section is not .text")
    if code["rsize"] != code_raw_size:
        raise ValueError(f"SteamStub .text raw-size mismatch {code_raw_size:#x} != {code['rsize']:#x}")

    enc = stolen + bytes(buf[code["rptr"]:code["rptr"] + code["rsize"]])
    if len(enc) % 16:
        raise ValueError("encrypted SteamStub payload is not AES-block aligned")
    dec = aes_decrypt(aes_key, aes_iv, enc)
    if len(dec) < code["rsize"]:
        raise ValueError("decrypted SteamStub payload is unexpectedly short")

    # This intentionally mirrors the known-good TEST4 build: decrypted .text and OEP
    # are restored while .bind stays in the PE as unused data.
    buf[code["rptr"]:code["rptr"] + code["rsize"]] = dec[:code["rsize"]]
    struct.pack_into("<I", buf, opt + 16, orig_ep)


def patch_rva(buf: bytearray, rva: int, expected: bytes, replacement: bytes, label: str) -> None:
    _pe, _opt, _entry, _base, sections = parse_pe(buf)
    off = rva_to_off(rva, sections)
    got = bytes(buf[off:off+len(expected)])
    if got != expected:
        raise ValueError(f"{label}: expected {expected.hex()} at RVA {rva:#x}, got {got.hex()}")
    if len(replacement) != len(expected):
        raise ValueError(f"{label}: patch length mismatch")
    buf[off:off+len(replacement)] = replacement


def classify(data: bytes) -> str:
    h = sha256(data)
    if h == TARGET_SHA256:
        return "original-steamstub"
    if h == DESTUB_SHA256:
        return "destubbed-unpatched"
    if h == FINAL_SHA256:
        return "qol-final"
    return "unknown"


def main() -> int:
    ap = argparse.ArgumentParser(description="Patch Mary Skelter 2 PC: skip startup logos + default Japanese VA without boot prompt.")
    ap.add_argument("exe", type=Path, help="input MarySkelter2.exe")
    ap.add_argument("-o", "--output", type=Path, help="output path (default: MarySkelter2.qol.exe beside input)")
    ap.add_argument("--check", action="store_true", help="only identify/verify the executable; do not write")
    ap.add_argument("--force-unknown", action="store_true", help="allow processing an unknown hash (byte/RVA guards still apply)")
    args = ap.parse_args()

    data = args.exe.read_bytes()
    state = classify(data)
    print(f"input: {args.exe}")
    print(f"size: {len(data)}")
    print(f"sha256: {sha256(data)}")
    print(f"state: {state}")

    if args.check:
        return 0 if state != "unknown" else 2
    if state == "qol-final":
        print("already final; nothing to do")
        return 0
    if state == "unknown" and not args.force_unknown:
        print("REFUSING: unsupported/unknown EXE hash. Use the exact target build or inspect manually.", file=sys.stderr)
        return 2

    buf = bytearray(data)
    if state == "original-steamstub":
        print("[1/3] removing SteamStub 3.1 x64 wrapper...")
        destub(buf)
        got = sha256(bytes(buf))
        if got != DESTUB_SHA256:
            raise RuntimeError(f"destub verification failed: got {got}, expected {DESTUB_SHA256}")
        print(f"      destub SHA-256 OK: {got}")
    elif state == "destubbed-unpatched":
        print("[1/3] input is already the verified destubbed build")
    else:
        # Unknown builds are allowed only explicitly. Detect whether the expected restored patch bytes exist.
        try:
            patch_rva(bytearray(buf), LOGO_RVA, LOGO_EXPECTED, LOGO_EXPECTED, "probe logo")
        except Exception:
            print("[1/3] unknown build does not look like the supported restored image", file=sys.stderr)
            raise

    print("[2/3] patching startup LogoStage -> final state 27...")
    patch_rva(buf, LOGO_RVA, LOGO_EXPECTED, LOGO_PATCH, "skip startup logos")

    print("[3/3] bypassing boot VA selector -> normal continuation (voice state 0 = Japanese)...")
    patch_rva(buf, VOICE_BOOT_RVA, VOICE_EXPECTED, VOICE_PATCH, "skip boot VA prompt")

    out = args.output or args.exe.with_name("MarySkelter2.qol.exe")
    out.write_bytes(buf)
    out_hash = sha256(bytes(buf))
    print(f"output: {out}")
    print(f"sha256: {out_hash}")
    if state in ("original-steamstub", "destubbed-unpatched"):
        if out_hash != FINAL_SHA256:
            raise RuntimeError(f"final verification failed: got {out_hash}, expected {FINAL_SHA256}")
        print("FINAL HASH VERIFIED")
    else:
        print("WARNING: unknown input build was forced; final known hash cannot be asserted")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as e:
        print(f"ERROR: {e}", file=sys.stderr)
        raise SystemExit(1)
