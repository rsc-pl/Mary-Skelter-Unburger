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

import ctypes
import ctypes.util
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


# ENC row payloads with bit 31 set in ``size_field`` use a second, inner
# compression layer: LZO1X.  ``aux`` is the exact decompressed row size.
# This is independent from the outer BILZ wrapper (zlib/stored).
_LZO = None

def _lzo_lib():
    global _LZO
    if _LZO is not None:
        return _LZO
    name = ctypes.util.find_library("lzo2")
    if not name:
        raise EncError("liblzo2 is required for compressed ENC rows")
    lib = ctypes.CDLL(name)
    lib.lzo1x_decompress_safe.argtypes = [
        ctypes.c_void_p, ctypes.c_size_t, ctypes.c_void_p,
        ctypes.POINTER(ctypes.c_size_t), ctypes.c_void_p,
    ]
    lib.lzo1x_decompress_safe.restype = ctypes.c_int
    lib.lzo1x_1_compress.argtypes = [
        ctypes.c_void_p, ctypes.c_size_t, ctypes.c_void_p,
        ctypes.POINTER(ctypes.c_size_t), ctypes.c_void_p,
    ]
    lib.lzo1x_1_compress.restype = ctypes.c_int
    _LZO = lib
    return lib

def lzo1x_decompress(data: bytes, expected_size: int) -> bytes:
    if expected_size < 0:
        raise EncError(f"invalid LZO expected size {expected_size}")
    lib = _lzo_lib()
    src = (ctypes.c_ubyte * len(data)).from_buffer_copy(data)
    dst = (ctypes.c_ubyte * max(1, expected_size))()
    out_len = ctypes.c_size_t(expected_size)
    rc = lib.lzo1x_decompress_safe(
        src, len(data), dst, ctypes.byref(out_len), None)
    if rc != 0:
        raise EncError(f"LZO1X decompression failed with code {rc}")
    if out_len.value != expected_size:
        raise EncError(
            f"LZO1X size mismatch: decoded {out_len.value}, expected {expected_size}")
    return bytes(dst[:out_len.value])

def lzo1x_compress(data: bytes) -> bytes:
    lib = _lzo_lib()
    # LZO1X worst-case bound from the reference implementation.  Work memory is
    # deliberately over-allocated; lzo1x_1_compress needs far less than 1 MiB.
    bound = len(data) + len(data) // 16 + 64 + 3
    src = (ctypes.c_ubyte * len(data)).from_buffer_copy(data)
    dst = (ctypes.c_ubyte * max(1, bound))()
    out_len = ctypes.c_size_t(bound)
    work = (ctypes.c_ubyte * (1 << 20))()
    rc = lib.lzo1x_1_compress(
        src, len(data), dst, ctypes.byref(out_len), work)
    if rc != 0:
        raise EncError(f"LZO1X compression failed with code {rc}")
    return bytes(dst[:out_len.value])


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

    def decoded_payload(self) -> bytes:
        """Return the logical row bytes, decoding inner LZO1X when flagged."""
        if not self.flag31:
            return self.payload
        return lzo1x_decompress(self.payload, self.aux)


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
        # `_texty` deliberately excludes NUL, so `chunk.find(b"\0")` can't
        # tell us whether this literal is actually NUL-terminated.  Record the
        # real boundary byte instead.  This matters for safe edits: many ENC
        # rows contain readable UTF-8 runs embedded inside an opaque tagged
        # stream, and those runs must NOT be treated as standalone strings.
        terminated = j < n and payload[j] == 0
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
            decoded = row.decoded_payload()
            for off, raw, term in scan_strings(decoded):
                out.append(EncString(row.index, off, raw, term))
        return out

    def rebuild(self, edits: dict, level: int = 7) -> bytes:
        """Return rebuilt entry bytes using decoded-row offsets.

        For compressed rows the edit is applied to the LZO1X-decoded row and
        that row alone is recompressed.  Unedited inner rows are copied through
        byte-for-byte.

        Production edit specs may include ``slot_capacity`` (independently
        proven bytes before the terminator). When capacity is absent, the old
        literal length is merely a fallback, NOT a proven layout limit. An
        interior literal may grow IN PLACE into immediately following NUL bytes
        (terminator and one spare NUL must remain before the next nonzero byte).
        The decoded row size, every later offset and all nonzero data remain
        unchanged. True interior relocation is still forbidden. Terminal
        growth into an all-NUL tail retains the native behavior.
        """
        if not edits:
            return self.entry

        by_row = {}
        for key, spec in edits.items():
            ri, off = key
            by_row.setdefault(ri, []).append((off, spec))

        encoded_rows: list[bytes] = []
        size_fields: list[int] = []
        aux_values: list[int] = []

        for row in self.rows:
            row_edits = by_row.get(row.index)
            if not row_edits:
                encoded_rows.append(row.payload)
                size_fields.append(row.size_field)
                aux_values.append(row.aux)
                continue

            decoded = bytearray(row.decoded_payload())
            for off, spec in sorted(row_edits, key=lambda x: -x[0]):
                if not isinstance(spec, dict):
                    raise EncError(
                        f"row={row.index} off={off}: decoded ENC edits require guarded specs")
                new = spec["new"]
                old = spec["old"]
                label = spec.get("record_key", f"row={row.index} off={off}")
                if bytes(decoded[off:off + len(old)]) != old:
                    raise EncError(f"{label}: ENC decoded-row old-byte mismatch")
                end0 = off + len(old)
                if end0 >= len(decoded) or decoded[end0] != 0:
                    raise EncError(f"{label}: ENC decoded-row literal is not NUL-terminated")

                capacity = int(spec.get("slot_capacity", len(old)))
                if capacity < len(old):
                    raise EncError(f"{label}: slot_capacity shorter than original literal")
                slot_end = off + capacity
                if slot_end >= len(decoded):
                    raise EncError(f"{label}: ENC slot exceeds decoded row")
                # The proven slot is NUL padded; never move an opaque byte.
                if any(decoded[end0:slot_end + 1]):
                    raise EncError(f"{label}: ENC slot expansion crosses nonzero row data")
                if len(new) > capacity:
                    # Do NOT mistake len(old) for a proven slot limit: the two
                    # Western a073 user samples have 196/57 NUL bytes AFTER
                    # their old terminators. Grow without moving a single byte.
                    # Respect explicit capacities; those can encode real bounds.
                    new_end = off + len(new)
                    in_place = (
                        "slot_capacity" not in spec
                        and new_end + 1 < len(decoded)
                        and not any(decoded[end0:new_end + 2])
                    )
                    if in_place:
                        decoded[off:new_end] = new
                        decoded[new_end] = 0
                    elif not any(decoded[slot_end + 1:]):
                        # native terminal-only growth; downstream all-zero tail.
                        decoded[off:slot_end + 1] = new + b"\0"
                    else:
                        spare = 0
                        cursor = end0 + 1
                        while cursor < len(decoded) and decoded[cursor] == 0:
                            spare += 1
                            cursor += 1
                        raise EncError(
                            f"{label}: new ENC text needs {len(new)} bytes but "
                            f"declared/default capacity is {capacity}; immediate "
                            f"NUL padding after terminator is {spare} bytes; "
                            "interior relocation has unproven references")
                else:
                    decoded[off:off + len(new)] = new
                    decoded[off + len(new):slot_end + 1] = (
                        b"\0" * (slot_end + 1 - off - len(new)))

            logical = bytes(decoded)
            if row.flag31:
                encoded = lzo1x_compress(logical)
                size_field = 0x80000000 | len(encoded)
                aux = len(logical)
            else:
                encoded = logical
                size_field = len(encoded)
                aux = row.aux
            encoded_rows.append(encoded)
            size_fields.append(size_field)
            aux_values.append(aux)

        new_payload = bytearray(struct.pack("<I", self.count))
        table = bytearray()
        body = bytearray()
        cursor = 4 + self.count * ROW_RECORD_SIZE
        offsets = []
        for encoded in encoded_rows:
            pad = (-cursor) % ROW_ALIGN
            body += b"\0" * pad
            cursor += pad
            offsets.append(cursor)
            body += encoded
            cursor += len(encoded)
        for i in range(self.count):
            table += struct.pack(
                "<III", size_fields[i], aux_values[i], offsets[i])
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

