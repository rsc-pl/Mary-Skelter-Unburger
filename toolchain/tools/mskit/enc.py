"""Orochi `.enc` / `.bin` text containers (the system-message tables).

Both are wrapped in the same 0x80-byte Orochi wrapper header that every other
resource in this engine uses (magic tag stored byte-reversed at 0x00, then a
big-endian BILZ block at 0x80 — see docs/FORMAT_ENC_AND_SYSTEM_TEXT.md).

`enc` payload — a row table:

    u32 count
    count x { u32 size_and_flags, u32 aux, u32 offset }     (12-byte records)
    zero padding up to the first row offset
    rows, each aligned to 0x40, containing NUL-terminated UTF-8 strings and
    opaque field bytes.  `size_and_flags & 0x7fffffff` is the row payload size;
    bit 31 is a flag.  `aux` is copied verbatim on rebuild (semantics unproven).

`bin` payload — a string-offset table:

    u32 first_string_offset (== 16 * count)
    count x { u64 offset, u64 zero }
    NUL-terminated UTF-8 strings addressed by those offsets.
"""
from __future__ import annotations

import struct
import zlib

WRAPPER_HEADER_SIZE = 0x80
BILZ_OFFSET = 0x80
BILZ_DATA_OFFSET = 0x90
ROW_ALIGN = 0x40
ROW_RECORD_SIZE = 12
BIN_RECORD_SIZE = 16

ALGO_STORED = 0
ALGO_ZLIB = 7


class EncError(Exception):
    pass


def wrapper_tag(entry_bytes: bytes) -> str | None:
    """Format tag of a wrapped entry ('enc', 'bin', 'csh', 'CL3', …) or None."""
    if len(entry_bytes) < 4 or entry_bytes[0] != 0:
        return None
    tag = bytes(reversed(entry_bytes[0:4]))
    if not tag.endswith(b"\0"):
        return None
    name = tag[:-1]
    if not name or not all(0x20 <= c < 0x7F for c in name):
        return None
    return name.decode("ascii")


class Wrapped:
    """Common 0x80-header + BILZ wrapper handling (read + re-emit)."""

    def __init__(self, entry_bytes: bytes | None = None, *, header: bytes | None = None,
                 payload: bytes | None = None, raw_body: bytes | None = None,
                 trailing: bytes = b"", usize: int | None = None, csize: int | None = None,
                 algo: int | None = None):
        if entry_bytes is not None:
            if len(entry_bytes) < BILZ_DATA_OFFSET:
                raise EncError("entry shorter than the wrapper header")
            self.entry = entry_bytes
            self.header = entry_bytes[:WRAPPER_HEADER_SIZE]
            self.tag = wrapper_tag(entry_bytes)
            if entry_bytes[BILZ_OFFSET:BILZ_OFFSET + 4] != b"BILZ":
                raise EncError("no BILZ block at 0x80")
            self.usize, self.csize, self.algo = struct.unpack_from(
                ">III", entry_bytes, BILZ_OFFSET + 4)
            body_end = BILZ_DATA_OFFSET + self.csize
            if body_end > len(entry_bytes):
                raise EncError("BILZ body exceeds the entry")
            self.raw_body = entry_bytes[BILZ_DATA_OFFSET:body_end]
            self.trailing = entry_bytes[body_end:]
            self.payload = (zlib.decompress(self.raw_body) if self.algo == ALGO_ZLIB
                            else self.raw_body)
            return
        if header is None or payload is None:
            raise EncError("either entry_bytes or (header, payload) is required")
        self.entry = None
        self.header = header
        self.tag = wrapper_tag(header)
        self.payload = payload
        self.raw_body = raw_body if raw_body is not None else b""
        self.trailing = trailing
        self.usize = len(payload) if usize is None else usize
        self.csize = len(self.raw_body) if csize is None else csize
        self.algo = ALGO_ZLIB if algo is None else algo

    def to_bytes(self, new_payload: bytes | None = None, level: int = 7) -> bytes:
        if new_payload is None or (self.entry is not None and new_payload == self.payload):
            if self.entry is not None:
                return self.entry  # byte-exact copy-through
            new_payload = self.payload
        body = zlib.compress(new_payload, level)
        return (self.header + b"BILZ" + struct.pack(">III", len(new_payload), len(body),
                                                    ALGO_ZLIB) + body + self.trailing)


class EncString:
    __slots__ = ("row_index", "offset", "raw", "terminated")

    def __init__(self, row_index, offset, raw, terminated):
        self.row_index = row_index
        self.offset = offset
        self.raw = raw
        self.terminated = terminated


class EncRow:
    __slots__ = ("index", "size_field", "aux", "offset", "payload", "padding")

    def __init__(self, index, size_field, aux, offset, payload, padding):
        self.index = index
        self.size_field = size_field
        self.aux = aux
        self.offset = offset
        self.payload = payload
        self.padding = padding

    @property
    def length(self) -> int:
        return self.size_field & 0x7FFFFFFF

    @property
    def flag31(self) -> bool:
        return bool(self.size_field & 0x80000000)


def _texty(c: int) -> bool:
    return (0x20 <= c < 0x7F) or c in (0x09, 0x0A, 0x0D) or c >= 0x80


def scan_strings(payload: bytes, min_chars: int = 2, min_letters: int = 3):
    """Conservative lossless UTF-8 string scan (byte offsets preserved)."""
    out = []
    n = len(payload)
    i = 0
    while i < n:
        if not _texty(payload[i]):
            i += 1
            continue
        j = i
        while j < n and _texty(payload[j]):
            j += 1
        chunk = payload[i:j]
        end = chunk.find(b"\0")
        raw = chunk if end < 0 else chunk[:end]
        terminated = end >= 0
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError:
            i = j
            continue
        letters = sum(1 for ch in text if ch.isalpha())
        if len(text) >= min_chars and letters >= min_letters and "\ufffd" not in text:
            other = sum(1 for ch in text if not (ch.isalnum() or ch in " \t\n\r"
                                                 "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
                                                 "\u3000\u3001\u3002\uff01\uff1f\uff08\uff09"
                                                 "\u3010\u3011\u300c\u300d\u300e\u300f\u30fb"
                                                 "\u2015\u2026\u2018\u2019\u201c\u201d"))
            if other / max(1, len(text)) <= 0.25:
                out.append((i, raw, terminated))
        i = j if j > i else i + 1
    return out


class EncFile(Wrapped):
    """Parsed `enc` payload (row table)."""

    def __init__(self, entry_bytes: bytes | None = None, **kw):
        super().__init__(entry_bytes, **kw)
        if self.tag != "enc":
            raise EncError(f"wrapper tag is {self.tag!r}, not 'enc'")
        data = self.payload
        if len(data) < 4:
            raise EncError("payload too short")
        self.count, = struct.unpack_from("<I", data, 0)
        need = 4 + self.count * ROW_RECORD_SIZE
        if need > len(data):
            raise EncError(f"row table outside payload ({need} > {len(data)})")
        self.rows: list[EncRow] = []
        records = []
        for i in range(self.count):
            size_field, aux, offset = struct.unpack_from("<III", data, 4 + i * ROW_RECORD_SIZE)
            records.append((size_field, aux, offset))
        for i, (size_field, aux, offset) in enumerate(records):
            length = size_field & 0x7FFFFFFF
            if offset + length > len(data):
                raise EncError(f"row {i} payload outside the payload")
            payload = data[offset:offset + length]
            nxt = records[i + 1][2] if i + 1 < len(records) else len(data)
            padding = data[offset + length:nxt]
            self.rows.append(EncRow(i, size_field, aux, offset, payload, padding))

    def strings(self) -> list[EncString]:
        out = []
        for row in self.rows:
            for off, raw, term in scan_strings(row.payload):
                out.append(EncString(row.index, off, raw, term))
        return out

    def rebuild(self, edits: dict[tuple[int, int], bytes], level: int = 7) -> bytes:
        """Return new entry bytes; `edits` = {(row_index, string_offset): new_bytes}."""
        if not edits:
            return self.entry
        rows = [bytearray(r.payload) for r in self.rows]
        for (ri, off), new in sorted(edits.items(), key=lambda kv: (kv[0][0], -kv[0][1])):
            row = self.rows[ri]
            payload = rows[ri]
            # original slot: string bytes + optional NUL terminator
            end = payload.find(b"\0", off)
            if end < 0:
                end = len(payload)
            else:
                end += 1
            slot = end - off
            if len(new) + (1 if slot > 0 else 0) <= slot:
                payload[off:off + len(new)] = new
                payload[off + len(new):end] = b"\0" * (end - off - len(new))
            else:
                payload[off:end] = new + b"\0"
        new_payload = bytearray()
        new_payload += struct.pack("<I", self.count)
        table = bytearray()
        body = bytearray()
        cursor = 4 + self.count * ROW_RECORD_SIZE
        offsets = []
        for i, row in enumerate(self.rows):
            pad = (-cursor) % ROW_ALIGN
            body += b"\0" * pad
            cursor += pad
            offsets.append(cursor)
            body += rows[i]
            cursor += len(rows[i])
        for i, row in enumerate(self.rows):
            length = len(rows[i])
            size_field = (row.size_field & 0x80000000) | length
            table += struct.pack("<III", size_field, row.aux, offsets[i])
        new_payload += table + body
        return self.to_bytes(bytes(new_payload), level)


class BinFile(Wrapped):
    """Parsed `bin` payload (block of 16-byte records addressing a string pool).

    Measured layout (all 7 `bin` files in the corpus):

        +0x00  u32    block hint (0x750 / 0x5f7 in the samples; semantics unproven)
        +0x10  N x { u64 string_offset, u64 zero }   while offsets stay valid and
                                                     non-decreasing
        +first_string  NUL-terminated UTF-8 strings (the pool), addressed by the
                       records above; the pool begins at the first record's offset.
    """

    def __init__(self, entry_bytes: bytes | None = None, **kw):
        super().__init__(entry_bytes, **kw)
        if self.tag != "bin":
            raise EncError(f"wrapper tag is {self.tag!r}, not 'bin'")
        data = self.payload
        if len(data) < 0x20:
            raise EncError("payload too short")
        self.block_hint, = struct.unpack_from("<I", data, 0)
        self.offsets: list[int] = []
        pos = 0x10
        while pos + 16 <= len(data):
            off, zero = struct.unpack_from("<QQ", data, pos)
            if off == 0 or off >= len(data) or (self.offsets and off < self.offsets[-1]):
                break
            self.offsets.append(off)
            pos += 16
        self.table_end = pos
        self.pool_start = self.offsets[0] if self.offsets else pos
        if self.pool_start < pos:
            raise EncError("string pool overlaps the record table")

    def strings(self):
        out = []
        for i, off in enumerate(self.offsets):
            end = self.payload.find(b"\0", off)
            if end < 0:
                end = len(self.payload)
            out.append((i, self.payload[off:end]))
        return out

    def rebuild(self, edits: dict[int, bytes], level: int = 7) -> bytes:
        """Return new entry bytes; `edits` = {string_index: new_bytes}."""
        if not edits:
            return self.entry
        strings = [raw for _, raw in self.strings()]
        for i, new in edits.items():
            strings[i] = new
        table = bytearray(self.payload[:self.table_end])
        pool = bytearray()
        cursor = self.pool_start
        for i, s in enumerate(strings):
            struct.pack_into("<Q", table, 0x10 + i * 16, cursor)
            pool += s + b"\0"
            cursor += len(s) + 1
        between = self.payload[self.table_end:self.pool_start]
        return self.to_bytes(bytes(table) + between + bytes(pool), level)

