#!/usr/bin/env python3
"""Regression tests for GBNL shared-string editing.

The real corpora contain deduplicated string-table entries where multiple fields
start from one localized string but intentionally diverge in the fidelity patch
(e.g. "Sister Thumbelina!" -> "Oyayubi-neesama!" / "Oyayubi-oneesama!").
Those fields must be repointed; the shared source slot must never be overwritten.
"""
from __future__ import annotations

import struct
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "toolchain" / "tools"))

from mskit import edit  # noqa: E402
from mskit.gbnl import Field  # noqa: E402


class FakeGbnl:
    endian = "<"
    descr_offset = 0
    msg_descr_size = 4
    offset_msgs = 16
    header_offset = 96
    is_gstl = False

    def __init__(self, old: bytes, refs: int = 2):
        buf = bytearray(self.header_offset + 0x40)
        for i in range(refs):
            struct.pack_into("<I", buf, i * 4, 0)
        buf[self.offset_msgs:self.offset_msgs + len(old) + 1] = old + b"\0"
        self.data = bytes(buf)
        self._old = old
        self.messages = []
        for _ in range(refs):
            fld = Field("str", 4, 0)
            fld.str_offset = 0
            fld.str_raw = old
            self.messages.append([fld])

    def string_table(self):
        return {0: self._old}

    def strings_end(self):
        return self.offset_msgs + len(self._old) + 1

    def tail_limit(self):
        return self.header_offset


def cstring(blob: bytes, table_base: int, rel: int) -> bytes:
    pos = table_base + rel
    end = blob.index(0, pos)
    return blob[pos:end]


def test_divergent_shared_strings_are_repointed():
    g = FakeGbnl(b"Sister Thumbelina!", refs=2)
    edits = {
        (0, 0): b"Oyayubi-neesama!",
        (1, 0): b"Oyayubi-oneesama!",
    }
    plan = edit.plan_edits(g, edits)
    assert plan["overwrite"] == [], plan
    out, _grew = edit.apply_edits(g, edits)
    p0 = struct.unpack_from("<I", out, 0)[0]
    p1 = struct.unpack_from("<I", out, 4)[0]
    assert p0 != p1
    assert cstring(out, g.offset_msgs, p0) == edits[(0, 0)]
    assert cstring(out, g.offset_msgs, p1) == edits[(1, 0)]
    assert cstring(out, g.offset_msgs, 0) == b"Sister Thumbelina!"


def test_editing_one_reference_does_not_mutate_the_other():
    g = FakeGbnl(b"Snow White...", refs=2)
    edits = {(0, 0): b"Shirayuki..."}
    plan = edit.plan_edits(g, edits)
    assert plan["overwrite"] == [], plan
    out, _grew = edit.apply_edits(g, edits)
    p0 = struct.unpack_from("<I", out, 0)[0]
    p1 = struct.unpack_from("<I", out, 4)[0]
    assert p0 != 0
    assert p1 == 0
    assert cstring(out, g.offset_msgs, p0) == b"Shirayuki..."
    assert cstring(out, g.offset_msgs, p1) == b"Snow White..."


def test_unshared_fitting_string_still_overwrites_in_place():
    g = FakeGbnl(b"abcdefghij", refs=1)
    edits = {(0, 0): b"short"}
    plan = edit.plan_edits(g, edits)
    assert len(plan["overwrite"]) == 1, plan
    assert plan["repoint"] == [], plan
    out, grew = edit.apply_edits(g, edits)
    assert not grew
    p0 = struct.unpack_from("<I", out, 0)[0]
    assert p0 == 0
    assert cstring(out, g.offset_msgs, p0) == b"short"


def main():
    test_divergent_shared_strings_are_repointed()
    test_editing_one_reference_does_not_mutate_the_other()
    test_unshared_fitting_string_still_overwrites_in_place()
    print("GBNL shared-string regression: PASS")


if __name__ == "__main__":
    main()
