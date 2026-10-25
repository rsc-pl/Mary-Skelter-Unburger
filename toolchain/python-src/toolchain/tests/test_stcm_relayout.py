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

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "toolchain" / "python-src" / "toolchain" / "tools"))

from mskit.stcm import DATA_HEADER_SIZE, DataItem, Ref, Stcm, StcmError  # noqa: E402


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
    # This legacy unit fixture predates physical-code parsing and directly
    # constructs Stcm with __new__.  Mark coverage complete so the test remains
    # focused on rebasing later edited items.
    s.code_scan_complete = True
    s.code_scan_error = None

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



def make_dead_code_fixture() -> tuple[bytes, int, int, int]:
    """STCM with a dead instruction carrying an absolute instruction pointer.

    The exported main instruction is opcode 0 (no fallthrough), so the legacy
    export-reachability walk sees only that one instruction.  Two more physical
    instructions remain in CODE_START..EXPORT_DATA; the middle one carries
    SPECIAL_INSTR_PTR1 to the final instruction.
    """
    data_off = 0x70
    code_marker = 0x90
    code_start = code_marker + len(b"CODE_START_\0")
    first = code_start
    hidden = first + 0x10
    target = hidden + 0x1C
    export_marker = target + 0x10
    export_off = export_marker + len(b"EXPORT_DATA\0")
    collection_off = export_off + 2 * 0x28
    blob = bytearray(collection_off + 0x40)

    blob[:0x20] = b"STCM2L TEST" + b"\0" * (0x20 - len(b"STCM2L TEST"))
    struct.pack_into("<IIII", blob, 0x20, export_off, 2, 0, collection_off)

    payload = b"A" * 16
    struct.pack_into("<IIII", blob, data_off, 7, 0, 0, len(payload))
    blob[data_off + 0x10:data_off + 0x20] = payload

    blob[code_marker:code_start] = b"CODE_START_\0"
    struct.pack_into("<IIII", blob, first, 0, 0, 0, 0x10)
    struct.pack_into("<IIII", blob, hidden, 0, 3, 1, 0x1C)
    struct.pack_into("<III", blob, hidden + 0x10, 0xFFFFFF41, target, 0x40000000)
    struct.pack_into("<IIII", blob, target, 0, 0, 0, 0x10)

    blob[export_marker:export_off] = b"EXPORT_DATA\0"
    # DATA export
    struct.pack_into("<I", blob, export_off, 1)
    blob[export_off + 4:export_off + 8] = b"DATA"
    struct.pack_into("<I", blob, export_off + 0x24, data_off)
    # CODE export
    rec = export_off + 0x28
    struct.pack_into("<I", blob, rec, 0)
    blob[rec + 4:rec + 8] = b"main"
    struct.pack_into("<I", blob, rec + 0x24, first)
    # Empty collection-link header.
    struct.pack_into("<II", blob, collection_off + 4, 0, 0)
    return bytes(blob), data_off, hidden, target


def test_dead_code_instruction_pointer_is_relocated():
    data, data_off, hidden, target = make_dead_code_fixture()
    s = Stcm(data)

    # Semantic extraction stays unchanged: only the exported no-return opcode is
    # reachable.  Physical relocation coverage sees all three instructions.
    assert len(s.instructions) == 1
    assert len(s.physical_instructions) == 3
    assert s.code_scan_complete
    assert s.verify_structure() == []
    word_off = hidden + 0x10 + 4
    assert any(r.word_offset == word_off and r.target == target and r.kind == "instr_ptr"
               for r in s.refs)

    out = s.relayout({data_off: b"a" * 24})  # +8 before CODE_START
    fixed = Stcm(out)
    assert fixed.code_scan_complete
    assert fixed.verify_structure() == []
    assert len(fixed.instructions) == 1
    assert len(fixed.physical_instructions) == 3

    # The hidden instruction and its target both moved by +8, and its word1 must
    # move with the target.  This is the exact bug class that crashed MS2 g1077.
    new_hidden = hidden + 8
    new_target = target + 8
    assert struct.unpack_from("<III", out, new_hidden + 0x10) == (
        0xFFFFFF41, new_target, 0x40000000)


def test_growth_requires_complete_physical_code_scan():
    data = bytearray(0x80)
    item = make_item(0x20, b"A" * 16)
    struct.pack_into("<IIII", data, 0x20, item.type, item.offset_unit, item.field_8, item.length)
    data[0x30:0x40] = item.payload

    s = Stcm.__new__(Stcm)
    s.data = bytes(data)
    s.endian = "<"
    s.data_items = {0x20: item}
    s.refs = []
    s.code_scan_complete = False
    s.code_scan_error = "synthetic incomplete scan"

    try:
        s.relayout({0x20: b"a" * 24})
    except StcmError as exc:
        assert "refusing unsafe STCM growth" in str(exc)
    else:  # pragma: no cover
        raise AssertionError("unsafe growth was not rejected")


def main():
    test_later_edited_item_is_rebased_after_growth()
    test_dead_code_instruction_pointer_is_relocated()
    test_growth_requires_complete_physical_code_scan()
    print("STCM relayout regressions: PASS")


if __name__ == "__main__":
    main()
