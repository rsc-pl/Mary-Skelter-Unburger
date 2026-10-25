#!/usr/bin/env python3
"""Shared deterministic PE/SteamStub mechanics for Mary Skelter PC QoL patches."""
from __future__ import annotations

from dataclasses import dataclass
import hashlib
import shutil
import struct
import subprocess
import tempfile
from pathlib import Path
from typing import Iterable

STEAMSTUB31_SIG = 0xC0DEC0DF


@dataclass(frozen=True)
class BytePatch:
    rva: int
    expected: bytes
    replacement: bytes
    label: str
    message: str

    def __post_init__(self) -> None:
        if not self.expected:
            raise ValueError(f"{self.label}: empty expected byte sequence")
        if len(self.expected) != len(self.replacement):
            raise ValueError(f"{self.label}: patch length mismatch")


@dataclass(frozen=True)
class ExeProfile:
    key: str
    display_name: str
    input_filename: str
    default_output_filename: str
    original_sha256: str
    destub_sha256: str
    final_sha256: str
    patches: tuple[BytePatch, ...]
    late_patches: tuple[BytePatch, ...] = ()


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
    for section in sections:
        if section["vaddr"] <= rva < section["vaddr"] + max(section["vsize"], section["rsize"]):
            return section["rptr"] + (rva - section["vaddr"])
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
    """Decrypt SteamStub payload using cryptography or an openssl fallback."""
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
    """Remove the SteamStub 3.1 x64 wrapper used by both supported PC games."""
    _pe, opt, entry, imagebase, sections = parse_pe(buf)
    entry_off = rva_to_off(entry, sections)

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
    for section in sections:
        if section["vaddr"] <= code_va < section["vaddr"] + max(section["vsize"], section["rsize"]):
            code = section
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

    # Preserve the already-runtime-tested behavior: restore decrypted .text and OEP,
    # leaving .bind physically present but unused.
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


def _matches_late_final(data: bytes, profile: ExeProfile) -> bool:
    if not profile.late_patches:
        return False
    buf = bytearray(data)
    try:
        for patch in profile.late_patches:
            _pe, _opt, _entry, _base, sections = parse_pe(buf)
            off = rva_to_off(patch.rva, sections)
            got = bytes(buf[off:off+len(patch.replacement)])
            if got != patch.replacement or len(patch.expected) != len(patch.replacement):
                return False
            buf[off:off+len(patch.expected)] = patch.expected
    except ValueError:
        return False
    return sha256(bytes(buf)) == profile.final_sha256


def classify(data: bytes, profile: ExeProfile) -> str:
    digest = sha256(data)
    if digest == profile.original_sha256:
        return "original-steamstub"
    if digest == profile.destub_sha256:
        return "destubbed-unpatched"
    if digest == profile.final_sha256:
        return "qol-base-final" if profile.late_patches else "qol-final"
    if _matches_late_final(data, profile):
        return "qol-final"
    return "unknown"


def _probe_supported_restored_image(buf: bytearray, patches: Iterable[BytePatch]) -> None:
    for patch in patches:
        patch_rva(bytearray(buf), patch.rva, patch.expected, patch.expected, f"probe {patch.label}")


def patch_executable(data: bytes, profile: ExeProfile, *, force_unknown: bool = False) -> tuple[bytes, str]:
    """Apply one game's profile; return final bytes and original classification."""
    state = classify(data, profile)
    if state == "qol-final":
        return data, state
    if state == "unknown" and not force_unknown:
        raise ValueError("unsupported/unknown EXE hash")

    buf = bytearray(data)
    if state == "original-steamstub":
        destub(buf)
        got = sha256(bytes(buf))
        if got != profile.destub_sha256:
            raise RuntimeError(
                f"destub verification failed: got {got}, expected {profile.destub_sha256}"
            )
    elif state == "unknown":
        _probe_supported_restored_image(buf, profile.patches)

    if state in ("original-steamstub", "destubbed-unpatched", "unknown"):
        for patch in profile.patches:
            patch_rva(buf, patch.rva, patch.expected, patch.replacement, patch.label)
        if state in ("original-steamstub", "destubbed-unpatched"):
            got = sha256(bytes(buf))
            if got != profile.final_sha256:
                raise RuntimeError(f"base-final verification failed: got {got}, expected {profile.final_sha256}")

    for patch in profile.late_patches:
        patch_rva(buf, patch.rva, patch.expected, patch.replacement, patch.label)

    out = bytes(buf)
    if profile.late_patches:
        if not _matches_late_final(out, profile):
            raise RuntimeError("late-patched final verification failed")
    elif state in ("original-steamstub", "destubbed-unpatched"):
        got = sha256(out)
        if got != profile.final_sha256:
            raise RuntimeError(f"final verification failed: got {got}, expected {profile.final_sha256}")
    return out, state
