"""STCM2 script container (`main.DAT` and friends inside CL3 resources).

    Header (0x30)
      +0x00 char[0x20] magic  ('STCM2L <build date>' — 'L'/'B' gives endianness)
      +0x20 u32 export_offset
      +0x24 u32 export_count
      +0x28 u32 field_28
      +0x2c u32 collection_link_offset

    Export entry (0x28) at export_offset: u32 type (0=CODE 1=DATA) + name[0x20]
      + u32 target_offset            (instruction or Data structure)

    Instruction header (0x10): u32 is_call, u32 opcode, u32 param_count, u32 size
      followed by param_count x Parameter (0x0c: param_0, param_4, param_8) and
      by size - 0x10 - 0x0c*param_count payload bytes.

    Data header (0x10): u32 type, u32 offset_unit, u32 field_8, u32 length
      followed by `length` payload bytes.

    Parameter encoding: TypeTag(x) = x >> 30, Value(x) = x & 0x3fffffff.
      tag 0 (MEM_OFFSET): param_0 value = absolute offset of a Data structure
      tag 2 (INDIRECT)   : value < 256, param_4 == 0x40000000
      tag 3 (SPECIAL)    : 0xffffff00..0xffffff0f read-stack,
                           0xffffff20..0xffffff27 read-4ac,
                           0xffffff40/41 instruction pointers (param_4 = offset),
                           0xffffff42 collection link, 0xffffff43 expansion

    String data (what we edit): type=0, offset_unit = padded_len/4 (> 1),
      field_8 = 1, payload = NUL-terminated string zero-padded to a multiple
      of 4 (padding <= 4 bytes).  Mirrors neptools' StringDataItem rule.

Rebuild strategy implemented here: *relocate by append*.  A Data structure
whose payload grows is written again after the end of the file (16-byte
aligned) and every reference we discovered is repointed; the original bytes
stay in place, so even an undiscovered reference keeps pointing at a valid
(unchanged) copy.  Data that shrinks or keeps its size is rewritten in place,
which keeps the rest of the file byte-identical.
"""
from __future__ import annotations

import struct
from collections import deque

HEADER_SIZE = 0x30
EXPORT_SIZE = 0x28
EXPORT_NAME_SIZE = 0x20
INSTR_HEADER_SIZE = 0x10
PARAM_SIZE = 0x0c
DATA_HEADER_SIZE = 0x10
APPEND_ALIGN = 16

TAG_MEM_OFFSET = 0
TAG_IMMEDIATE = 1
TAG_INDIRECT = 2
TAG_SPECIAL = 3

SPECIAL_READ_STACK = (0xFFFFFF00, 0xFFFFFF0F)
SPECIAL_READ_4AC = (0xFFFFFF20, 0xFFFFFF27)
SPECIAL_INSTR_PTR0 = 0xFFFFFF40
SPECIAL_INSTR_PTR1 = 0xFFFFFF41
SPECIAL_COLL_LINK = 0xFFFFFF42
SPECIAL_EXPANSION = 0xFFFFFF43

NO_RETURN_OPCODES = {0, 6}


class StcmError(Exception):
    pass


def tag(x: int) -> int:
    return x >> 30


def value(x: int) -> int:
    return x & 0x3FFFFFFF


class Export:
    __slots__ = ("index", "type", "name", "offset", "record_offset")

    def __init__(self, index, type_, name, offset, record_offset):
        self.index = index
        self.type = type_
        self.name = name
        self.offset = offset
        self.record_offset = record_offset

    def __repr__(self) -> str:  # pragma: no cover
        return f"Export({self.index}, type={self.type}, {self.name!r}, off={self.offset:#x})"


class DataItem:
    __slots__ = ("offset", "type", "offset_unit", "field_8", "length", "payload", "is_string", "declared_size")

    def __init__(self, offset, type_, offset_unit, field_8, length, payload):
        self.offset = offset
        self.type = type_
        self.offset_unit = offset_unit
        self.field_8 = field_8
        self.length = length
        self.payload = payload
        self.is_string = False
        self.declared_size = DATA_HEADER_SIZE + length

    def as_dict(self) -> dict:
        return {"offset": self.offset, "type": self.type, "offset_unit": self.offset_unit,
                "field_8": self.field_8, "length": self.length, "is_string": self.is_string}


class Instruction:
    __slots__ = ("offset", "is_call", "opcode", "param_count", "size", "params", "payload")

    def __init__(self, offset, is_call, opcode, param_count, size, params, payload):
        self.offset = offset
        self.is_call = is_call
        self.opcode = opcode
        self.param_count = param_count
        self.size = size
        self.params = params
        self.payload = payload

    def as_dict(self) -> dict:
        return {"offset": self.offset, "is_call": self.is_call, "opcode": self.opcode,
                "param_count": self.param_count, "size": self.size}


class Ref:
    """A place in the file that stores an absolute file offset."""
    __slots__ = ("word_offset", "target", "kind")

    def __init__(self, word_offset, target, kind):
        self.word_offset = word_offset
        self.target = target
        self.kind = kind

    def __repr__(self) -> str:  # pragma: no cover
        return f"Ref(@{self.word_offset:#x} -> {self.target:#x} via {self.kind})"


class Stcm:
    def __init__(self, data: bytes, validate: bool = True):
        self.data = data
        if len(data) < HEADER_SIZE or data[:4] != b"STCM":
            raise StcmError("not an STCM file")
        self.raw_magic = data[:0x20].split(b"\0")[0]
        if not self.raw_magic.startswith(b"STCM2"):
            raise StcmError(f"unexpected STCM magic {self.raw_magic[:8]!r}")
        echar = data[5:6]  # 'STCM2' + 'L'|'B'
        if echar not in (b"L", b"B"):
            raise StcmError(f"bad STCM endian char {echar!r}")
        self.endian = "<" if echar == b"L" else ">"
        (self.export_offset, self.export_count, self.field_28,
         self.collection_link_offset) = struct.unpack_from(self.endian + "IIII", data, 0x20)
        if validate:
            if self.export_count > 100_000:
                raise StcmError(f"implausible export count {self.export_count}")
            if self.export_offset + self.export_count * EXPORT_SIZE > len(data):
                raise StcmError("export table outside file")
        self.exports: list[Export] = []
        self.data_items: dict[int, DataItem] = {}
        self.instructions: dict[int, Instruction] = {}
        self.refs: list[Ref] = []
        self.collection_link: dict | None = None
        self._parse_exports()
        self._parse_collection_link()
        self._traverse()
        self._mark_strings()

    # ------------------------------------------------------------------ parse
    def _parse_exports(self) -> None:
        e = self.endian
        # header fields that hold absolute file offsets
        self.refs.append(Ref(0x20, self.export_offset, "header_export"))
        self.refs.append(Ref(0x2C, self.collection_link_offset, "header_collection_link"))
        for i in range(self.export_count):
            rec = self.export_offset + i * EXPORT_SIZE
            typ, = struct.unpack_from(e + "I", self.data, rec)
            name = self.data[rec + 4:rec + 4 + EXPORT_NAME_SIZE].split(b"\0")[0].decode("ascii", "replace")
            off, = struct.unpack_from(e + "I", self.data, rec + 0x24)
            self.exports.append(Export(i, typ, name, off, rec))
            self.refs.append(Ref(rec + 0x24, off, "export"))

    def _parse_collection_link(self) -> None:
        """CollectionLinkHeader at collection_link_offset (+ optional entries).

        Only the fields that hold absolute offsets are tracked:
        header.offset and each entry's name_0/name_1 string pointers.
        """
        cl = self.collection_link_offset
        if cl <= 0 or cl + 0x40 > len(self.data):
            return
        e = self.endian
        entries_off, count = struct.unpack_from(e + "II", self.data, cl + 4)
        self.collection_link = {"offset": entries_off, "count": count, "header": cl}
        self.refs.append(Ref(cl + 4, entries_off, "collection_link_entries"))
        if count and count < 100000 and entries_off + count * 0x20 <= len(self.data):
            for i in range(count):
                rec = entries_off + i * 0x20
                name_0, name_1 = struct.unpack_from(e + "II", self.data, rec)
                if name_0 and name_0 < len(self.data):
                    self.refs.append(Ref(rec, name_0, "collection_link_name"))
                if name_1 and name_1 < len(self.data):
                    self.refs.append(Ref(rec + 4, name_1, "collection_link_name"))

    def _read_data(self, off: int) -> DataItem:
        if off + DATA_HEADER_SIZE > len(self.data):
            raise StcmError(f"data header at {off:#x} outside file")
        e = self.endian
        typ, ou, f8, length = struct.unpack_from(e + "IIII", self.data, off)
        if off + DATA_HEADER_SIZE + length > len(self.data):
            raise StcmError(f"data payload at {off:#x} (len {length}) outside file")
        payload = self.data[off + DATA_HEADER_SIZE: off + DATA_HEADER_SIZE + length]
        return DataItem(off, typ, ou, f8, length, payload)

    def _read_instruction(self, off: int) -> Instruction:
        if off + INSTR_HEADER_SIZE > len(self.data):
            raise StcmError(f"instruction at {off:#x} outside file")
        e = self.endian
        is_call, opcode, pcount, size = struct.unpack_from(e + "IIII", self.data, off)
        if pcount > 16:
            raise StcmError(f"instruction at {off:#x}: param_count {pcount} > 16")
        head = INSTR_HEADER_SIZE + pcount * PARAM_SIZE
        if size < head or off + size > len(self.data):
            raise StcmError(f"instruction at {off:#x}: size {size} invalid")
        if is_call:
            # the opcode word is an absolute offset to the called instruction
            self.refs.append(Ref(off + 4, opcode, "call_target"))
        params = [struct.unpack_from(e + "III", self.data, off + INSTR_HEADER_SIZE + i * PARAM_SIZE)
                  for i in range(pcount)]
        payload = self.data[off + head: off + size]
        return Instruction(off, is_call, opcode, pcount, size, params, payload)

    def _traverse(self) -> None:
        queue: deque[tuple[int, str]] = deque()
        for ex in self.exports:
            queue.append((ex.offset, "data" if ex.type == 1 else "instr"))
        reported: set[str] = set()
        while queue:
            off, kind = queue.popleft()
            token = f"{kind}:{off}"
            if token in reported:
                continue
            reported.add(token)
            if off <= 0 or off >= len(self.data):
                continue
            try:
                if kind == "data":
                    if off in self.data_items:
                        continue
                    item = self._read_data(off)
                    self.data_items[off] = item
                else:
                    if off in self.instructions:
                        continue
                    instr = self._read_instruction(off)
                    self.instructions[off] = instr
                    queue.extend(self._instruction_children(instr))
            except (StcmError, struct.error):
                continue

    def _instruction_children(self, instr: Instruction) -> list[tuple[int, str]]:
        out: list[tuple[int, str]] = []
        e = self.endian
        base = instr.offset + INSTR_HEADER_SIZE
        for i, (p0, p4, p8) in enumerate(instr.params):
            t = tag(p0)
            if t == TAG_MEM_OFFSET:
                target = value(p0)
                self.refs.append(Ref(base + i * PARAM_SIZE, target, "mem_offset"))
                out.append((target, "data"))
            elif t == TAG_INDIRECT:
                # param_8 parsed normally
                if tag(p8) == TAG_MEM_OFFSET:
                    target = value(p8)
                    self.refs.append(Ref(base + i * PARAM_SIZE + 8, target, "mem_offset48"))
                    out.append((target, "data"))
            elif t == TAG_SPECIAL:
                if p0 in (SPECIAL_INSTR_PTR0, SPECIAL_INSTR_PTR1):
                    self.refs.append(Ref(base + i * PARAM_SIZE + 4, p4, "instr_ptr"))
                    out.append((p4, "instr"))
                elif p0 == SPECIAL_COLL_LINK:
                    self.refs.append(Ref(base + i * PARAM_SIZE + 4, p4, "coll_link"))
                elif p0 == SPECIAL_EXPANSION:
                    self.refs.append(Ref(base + i * PARAM_SIZE + 4, p4, "expansion"))
            else:  # IMMEDIATE
                if tag(p4) == TAG_MEM_OFFSET:
                    target = value(p4)
                    self.refs.append(Ref(base + i * PARAM_SIZE + 4, target, "mem_offset48"))
                    out.append((target, "data"))
                if tag(p8) == TAG_MEM_OFFSET:
                    target = value(p8)
                    self.refs.append(Ref(base + i * PARAM_SIZE + 8, target, "mem_offset48"))
                    out.append((target, "data"))
        if instr.is_call or instr.opcode not in NO_RETURN_OPCODES:
            out.append((instr.offset + instr.size, "instr"))
        return out

    def _mark_strings(self) -> None:
        for item in self.data_items.values():
            if item.type != 0 or item.offset_unit <= 1 or item.field_8 != 1:
                continue
            need = item.offset_unit * 4
            if len(item.payload) != need or need == 0:
                continue
            end = item.payload.find(b"\0")
            if end < 0:
                continue
            padlen = need - end - 1
            if padlen > 4 or any(item.payload[end + 1:]):
                continue
            item.is_string = True

    # ------------------------------------------------------------------ query
    def string_items(self) -> list[DataItem]:
        return [d for d in self.data_items.values() if d.is_string]

    def gnl_items(self) -> list[DataItem]:
        """Data items whose payload is a GBNL blob (GST header or GBN footer)."""
        out = []
        for d in self.data_items.values():
            if d.is_string or len(d.payload) < 0x40:
                continue
            p = d.payload
            if p[:3] == b"GST" or p[-0x40:-0x3d] == b"GBN":
                out.append(d)
        return out

    def string_bytes(self, item: DataItem) -> bytes:
        end = item.payload.find(b"\0")
        return item.payload[:end]

    def verify_structure(self) -> list[str]:
        """Return a list of structural problems (empty list = consistent).

        Used as a post-rebuild self-check: every reference must resolve to a
        known item or to a plausible string/collection-link target.
        """
        problems: list[str] = []
        known = set(self.data_items) | set(self.instructions)
        for ref in self.refs:
            t = ref.target
            if ref.kind in ("export", "mem_offset", "mem_offset48", "instr_ptr",
                            "call_target"):
                if t not in known:
                    problems.append(f"{ref.kind} @{ref.word_offset:#x} -> {t:#x} "
                                    f"is not a known item")
            elif ref.kind in ("header_export", "header_collection_link",
                              "collection_link_entries"):
                if not (0 <= t <= len(self.data)):
                    problems.append(f"{ref.kind} -> {t:#x} outside file")
            elif ref.kind == "collection_link_name":
                if not (0 <= t < len(self.data)) or self.data.find(b"\0", t) < 0:
                    problems.append(f"{ref.kind} -> {t:#x} not a string")
        for ex in self.exports:
            if ex.type == 1 and ex.offset not in self.data_items:
                problems.append(f"export {ex.name!r} -> {ex.offset:#x} is not a data item")
            if ex.type == 0 and ex.offset not in self.instructions:
                problems.append(f"export {ex.name!r} -> {ex.offset:#x} is not an instruction")
        return problems

    def text(self, item: DataItem, encoding: str = "utf-8") -> str:
        return self.string_bytes(item).decode(encoding, "replace")

    # ------------------------------------------------------------------ patch
    def relayout(self, data_edits: dict[int, bytes]) -> bytes:
        """Compact rebuild: grow items in place and shift the following bytes.

        Every absolute file offset the parser knows about (header fields, export
        entries, instruction parameters, call targets, collection-link entries
        and their string pointers) is re-pointed, so the file grows by exactly
        the number of bytes the edited items needed — nothing is duplicated.

        Byte ranges that the parser never interpreted (dead data, padding) are
        moved verbatim; nothing else is rewritten.
        """
        if not data_edits:
            return self.data
        e = self.endian
        insertions: list[tuple[int, bytes]] = []
        inplace: dict[int, bytes] = {}
        for off, payload in sorted(data_edits.items()):
            item = self.data_items.get(off)
            if item is None:
                raise StcmError(f"no data item at {off:#x}")
            inplace[off] = payload
            new_total = DATA_HEADER_SIZE + len(payload)
            if new_total > item.declared_size:
                keep = item.declared_size - DATA_HEADER_SIZE
                insertions.append((off + item.declared_size, payload[keep:]))

        out = bytearray()
        prev = 0
        for pos, blob in sorted(insertions):
            out += self.data[prev:pos]
            out += blob
            prev = pos
        out += self.data[prev:]

        def shift(x: int) -> int:
            return sum(len(b) for p, b in insertions if p <= x)

        for off, payload in inplace.items():
            item = self.data_items[off]
            # `out` already contains every growth insertion.  Any item that sits
            # after an earlier grown item has therefore moved.  Writing it at its
            # old absolute offset corrupts the shifted structure (this only shows
            # up when multiple edited Data items share one STCM and an earlier one
            # grows).  Rebase every write through the same shift function used for
            # references/targets below.
            new_off = off + shift(off)
            struct.pack_into(e + "IIII", out, new_off, item.type, item.offset_unit,
                             item.field_8, len(payload))
            room = item.declared_size - DATA_HEADER_SIZE
            head = payload[:room]
            out[new_off + DATA_HEADER_SIZE: new_off + DATA_HEADER_SIZE + len(head)] = head
            tail_start = new_off + DATA_HEADER_SIZE + len(head)
            item_end = new_off + item.declared_size
            if tail_start < item_end:  # shrinking: clear stale tail
                out[tail_start:item_end] = b"\0" * (item_end - tail_start)
        for ref in self.refs:
            struct.pack_into(e + "I", out, ref.word_offset + shift(ref.word_offset),
                             ref.target + shift(ref.target))
        return bytes(out)

    def rebuild(self, data_edits: dict[int, bytes], append_align: int = APPEND_ALIGN) -> bytes:
        """Return new STCM bytes with `data_edits` (data offset -> new payload)."""
        if not data_edits:
            return self.data
        buf = bytearray(self.data)
        mapping: dict[int, int] = {}
        appended = bytearray()

        def append_offset() -> int:
            want = (len(buf) + len(appended) + append_align - 1) // append_align * append_align
            pad = want - (len(buf) + len(appended))
            appended.extend(b"\0" * pad)
            return want

        for off, payload in data_edits.items():
            item = self.data_items.get(off)
            if item is None:
                raise StcmError(f"no data item at {off:#x}")
            new_len = len(payload)
            new_total = DATA_HEADER_SIZE + new_len
            if new_total <= item.declared_size:
                struct.pack_into(self.endian + "IIII", buf, off, item.type,
                                 item.offset_unit, item.field_8, new_len)
                buf[off + DATA_HEADER_SIZE: off + DATA_HEADER_SIZE + new_len] = payload
                leftover = item.declared_size - new_total
                if leftover:
                    start = off + DATA_HEADER_SIZE + new_len
                    buf[start:start + leftover] = b"\0" * leftover
            else:
                new_off = append_offset()
                mapping[off] = new_off
                appended += struct.pack(self.endian + "IIII", item.type, item.offset_unit,
                                        item.field_8, new_len)
                appended += payload
        for ref in self.refs:
            if ref.target in mapping:
                struct.pack_into(self.endian + "I", buf, ref.word_offset, mapping[ref.target])
        return bytes(buf) + bytes(appended)
