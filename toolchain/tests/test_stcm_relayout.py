#!/usr/bin/env python3
"""Regression for multi-item STCM relayout.

If an earlier edited Data item grows, every later byte moves.  Later edited
items must therefore be written at their shifted offsets, not their source
offsets.  The old implementation corrupted the second item in exactly this
case during large story rebuilds.
"""
from __future__ import annotations

import struct
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "toolchain" / "tools"))

from mskit.stcm import DATA_HEADER_SIZE, DataItem, Ref, Stcm  # noqa: E402


def make_item(off: int, payload: bytes) -> DataItem:
    return DataItem(off, 7, 0, 0, len(payload), payload)


def test_later_edited_item_is_rebased_after_growth():
    data = bytearray(0x80)
    first = make_item(0x20, b"A" * 16)   # declared total 0x20
    second = make_item(0x40, b"B" * 16)  # declared total 0x20
    struct.pack_into("<IIII", data, 0x20, first.type, first.offset_unit, first.field_8, first.length)
    data[0x30:0x40] = first.payload
    struct.pack_into("<IIII", data, 0x40, second.type, second.offset_unit, second.field_8, second.length)
    data[0x50:0x60] = second.payload
    # A reference before both items points to the second one.
    struct.pack_into("<I", data, 0x10, 0x40)

    s = Stcm.__new__(Stcm)
    s.data = bytes(data)
    s.endian = "<"
    s.data_items = {0x20: first, 0x40: second}
    s.refs = [Ref(0x10, 0x40, "test")]

    first_new = b"a" * 24   # grows by 8 bytes
    second_new = b"c" * 16  # same size, but lives after the growth
    out = s.relayout({0x20: first_new, 0x40: second_new})

    assert len(out) == len(data) + 8
    # First item remains at 0x20 and its tail occupies the inserted bytes.
    assert struct.unpack_from("<I", out, 0x20 + 0x0C)[0] == 24
    assert out[0x30:0x48] == first_new
    # Second item moved by +8 and must be edited there.
    second_off = 0x48
    assert struct.unpack_from("<I", out, second_off + 0x0C)[0] == 16
    assert out[second_off + DATA_HEADER_SIZE:second_off + DATA_HEADER_SIZE + 16] == second_new
    # Its reference must move to the same rebased target.
    assert struct.unpack_from("<I", out, 0x10)[0] == second_off


def main():
    test_later_edited_item_is_rebased_after_growth()
    print("STCM multi-item relayout regression: PASS")


if __name__ == "__main__":
    main()
