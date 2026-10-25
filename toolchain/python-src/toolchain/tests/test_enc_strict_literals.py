#!/usr/bin/env python3
"""Regression for decoded-row ENC/LZO1X edits.

The mandatory public regression is self-contained: it builds a synthetic ENC
entry whose row uses the real inner LZO1X layer, then proves that decoded-row
edits may grow only inside an explicitly proven NUL-padded slot.  Local game
archives are exercised separately by the source extraction / rebuild bridge;
this test must not depend on a particular localized string or archive revision.
"""
from __future__ import annotations

import struct
import sys
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "toolchain" / "python-src" / "toolchain" / "tools"))

from mskit.enc import EncError, EncFile, lzo1x_compress  # noqa: E402


def enc_entry_with_lzo_row(decoded: bytes) -> bytes:
    compressed = lzo1x_compress(decoded)
    count = 1
    table_end = 4 + 12
    row_off = (table_end + 0x3F) & ~0x3F
    payload = bytearray(struct.pack("<I", count))
    payload += struct.pack("<III", 0x80000000 | len(compressed), len(decoded), row_off)
    payload += b"\0" * (row_off - len(payload))
    payload += compressed
    header = b"\0cne" + b"\0" * (0x80 - 4)
    body = zlib.compress(bytes(payload), 7)
    return header + b"BILZ" + struct.pack(">III", len(payload), len(body), 7) + body


def fixture() -> tuple[EncFile, int, bytes, int]:
    old = b"Synthetic Blood Maiden"
    off = 8
    capacity = 40
    decoded = bytearray(b"\x01\x02\x03\x04\0\0\0\0" + b"\0" * 96)
    decoded[off:off + len(old)] = old
    decoded[off + len(old):off + capacity + 1] = b"\0" * (capacity + 1 - len(old))
    return EncFile(enc_entry_with_lzo_row(bytes(decoded))), off, old, capacity


def test_lzo_decoded_row_can_grow_inside_proven_slot():
    enc, off, old, capacity = fixture()
    assert enc.rows[0].flag31
    assert enc.rows[0].decoded_payload()[off:off + len(old)] == old
    new = b"Synthetic Blood Maiden Expanded"
    assert len(new) <= capacity

    rebuilt = enc.rebuild({
        (0, off): {
            "old": old,
            "new": new,
            "slot_capacity": capacity,
            "record_key": "synthetic/lzo/row0/title",
        }
    })
    out = EncFile(rebuilt)
    assert out.rows[0].flag31
    out_decoded = out.rows[0].decoded_payload()
    assert new + b"\0" in out_decoded
    assert old + b"\0" not in out_decoded


def test_terminal_row_growth_beyond_slot_is_safe():
    enc, off, old, capacity = fixture()
    new = b"X" * 139
    rebuilt = enc.rebuild({(0, off): {
        "old": old, "new": new, "slot_capacity": capacity,
        "record_key": "synthetic/lzo/row0/terminal-growth",
    }})
    parsed = EncFile(rebuilt)
    decoded = parsed.rows[0].decoded_payload()
    assert decoded[:off] == enc.rows[0].decoded_payload()[:off]
    assert decoded[off:off+139] == new
    assert decoded[off+139] == 0
    assert parsed.rows[0].flag31
    assert parsed.rows[0].aux == len(decoded)


def test_interior_growth_beyond_slot_is_rejected():
    enc, off, old, capacity = fixture()
    decoded = bytearray(enc.rows[0].decoded_payload())
    decoded[off+capacity+3:off+capacity+7] = b"LIVE"
    enc = EncFile(enc_entry_with_lzo_row(bytes(decoded)))
    try:
        enc.rebuild({(0, off): {
            "old": old, "new": b"X" * 139, "slot_capacity": capacity,
            "record_key": "synthetic/lzo/row0/unsafe-interior",
        }})
    except EncError as exc:
        assert "interior relocation" in str(exc)
    else:
        raise AssertionError("unsafe interior ENC relocation was accepted")


def test_wrong_old_bytes_are_rejected():
    enc, off, old, capacity = fixture()
    bad_old = old[:-1] + b"X"
    try:
        enc.rebuild({
            (0, off): {
                "old": bad_old,
                "new": b"safe",
                "slot_capacity": capacity,
                "record_key": "synthetic/lzo/row0/stale",
            }
        })
    except EncError as exc:
        assert "old-byte mismatch" in str(exc)
    else:
        raise AssertionError("stale decoded ENC guard was accepted")


def main():
    test_lzo_decoded_row_can_grow_inside_proven_slot()
    test_terminal_row_growth_beyond_slot_is_safe()
    test_interior_growth_beyond_slot_is_rejected()
    test_wrong_old_bytes_are_rejected()
    print("ENC decoded-row/LZO1X synthetic regression: PASS")


if __name__ == "__main__":
    main()
