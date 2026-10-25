"""GBNL / GBIN / GSTR — the format that actually stores dialogue text.

Layout (all integers little- or big-endian as flagged by the header):

    HeaderFooter (0x40 bytes)
      +0x00 char[3] magic   'GBN' (footer, at EOF-0x40) | 'GST' (header, at 0)
      +0x03 char    endian  'L' | 'B'
      +0x04 u16     field_04  (always 1)
      +0x06 u16     field_06  (always 0)
      +0x08 u32     field_08  (always 16)
      +0x0c u32     field_0c  (always 4)
      +0x10 u32     flags      (1 = string table present)
      +0x14 u32     descr_offset
      +0x18 u32     count_msgs
      +0x1c u32     msg_descr_size
      +0x20 u32     count_types
      +0x24 u32     offset_types
      +0x28 u32     field_28
      +0x2c u32     offset_msgs    (string table base)
      +0x30 u32     field_30       (8 => text is UTF-8, else Shift-JIS)
      +0x34 3 x u32 zero

    messages: count_msgs structs of msg_descr_size bytes at descr_offset
    type descriptors: count_types x {u16 type, u16 offset} at offset_types,
        type codes: 0=INT32 1=INT8 2=INT16 3=FLOAT 5=STRING
    string table at offset_msgs: NUL-terminated strings, deduplicated; a STRING
        field holds an offset into it (0xffffffff = null).
    GBN payloads align every section to 16 bytes; GST payloads do not.

The int8 handling mirrors neptools' Gbnl::Parse_/Pad: a type-1 descriptor opens
an "int8 in progress" state which the next gap resolves into either a single
byte or a fixed-length string.
"""
from __future__ import annotations

import struct

MAGIC_FOOTER = b"GBN"
MAGIC_HEADER = b"GST"
HEADER_SIZE = 0x40
ALIGN = 16

T_INT32, T_INT8, T_INT16, T_FLOAT, T_STRING = 0, 1, 2, 3, 5
TYPE_CODE = {"i32": T_INT32, "i8": T_INT8, "i16": T_INT16, "f32": T_FLOAT, "str": T_STRING}
TYPE_SIZE = {"i8": 1, "i16": 2, "i32": 4, "i64": 8, "f32": 4, "str": 4}


class GbnlError(Exception):
    pass


def _align(x: int, a: int = ALIGN) -> int:
    return (x + a - 1) // a * a


class Field:
    """One field of one message struct."""
    __slots__ = ("kind", "size", "offset", "str_raw", "str_offset", "is_null")

    def __init__(self, kind, size, offset):
        self.kind = kind
        self.size = size
        self.offset = offset
        self.str_raw: bytes | None = None
        self.str_offset: int | None = None
        self.is_null = False

    def __repr__(self) -> str:  # pragma: no cover
        return f"Field({self.kind}, off={self.offset}, raw={self.str_raw!r})"


class Gbnl:
    """Parsed GBNL blob.  `data` is kept verbatim for lossless patching."""

    def __init__(self, data: bytes, validate: bool = True):
        self.data = data
        if len(data) < HEADER_SIZE:
            raise GbnlError("blob shorter than header")
        if data[:3] == MAGIC_HEADER:
            self.is_gstl = True
            self.header_offset = 0
        elif data[-HEADER_SIZE:-HEADER_SIZE + 3] == MAGIC_FOOTER:
            self.is_gstl = False
            self.header_offset = len(data) - HEADER_SIZE
        else:
            raise GbnlError("no GST header / GBN footer")
        h = data[self.header_offset:self.header_offset + HEADER_SIZE]
        self.endian = "<" if h[3:4] == b"L" else ">"
        (self.field_04, self.field_06) = struct.unpack_from(self.endian + "HH", h, 4)
        (self.field_08, self.field_0c, self.flags, self.descr_offset, self.count_msgs,
         self.msg_descr_size, self.count_types, self.offset_types, self.field_28,
         self.offset_msgs, self.field_30) = struct.unpack_from(self.endian + "11I", h, 8)
        if validate:
            if self.field_04 != 1 or self.field_06 != 0:
                raise GbnlError(f"unexpected header words {self.field_04}/{self.field_06}")
            if self.field_08 != 16 or self.field_0c != 4:
                raise GbnlError(f"unexpected header words {self.field_08}/{self.field_0c}")
            if self.msg_descr_size == 0 or self.count_msgs == 0:
                raise GbnlError("empty message table")
            need = self.descr_offset + self.msg_descr_size * self.count_msgs
            if need > len(data):
                raise GbnlError(f"message table outside blob ({need} > {len(data)})")
        self.items = self._build_items()
        self.messages: list[list[Field]] = []
        self._parse_messages()

    # ------------------------------------------------------------- structure
    def _build_items(self) -> list[dict]:
        """Reproduce neptools' type-descriptor interpretation.

        Every emitted item carries the *declared* absolute offset inside the
        message struct (or, for items synthesised out of a padding gap, the
        position where neptools' builder would put them).  Reading messages by
        these offsets — rather than by a running cursor — is what makes the
        STRING fields land on the right bytes.
        """
        items: list[dict] = []
        e = self.endian
        calc = 0
        pos = 0
        pending: int | None = None

        def resolve_pending(next_offset: int) -> None:
            nonlocal pos, pending
            diff = next_offset - calc
            if diff <= 3:
                items.append({"kind": "i8", "size": 1, "offset": pending})
                pos = max(pos, pending + 1)
            else:
                size = diff + 1
                items.append({"kind": "fixstr", "size": size, "offset": pending})
                pos = max(pos, pending + size)
            pending = None

        for i in range(self.count_types):
            rec = self.offset_types + i * 4
            if rec + 4 > len(self.data):
                raise GbnlError("type descriptor outside blob")
            tcode, toff = struct.unpack_from(e + "HH", self.data, rec)
            if toff < calc:
                raise GbnlError(f"type {i} not ascending ({toff} < {calc})")
            if pending is not None:
                resolve_pending(toff)
            if toff > pos:
                items.append({"kind": "pad", "size": toff - pos, "offset": pos})
                pos = toff
            if tcode == T_INT8:
                pending = toff
                calc = toff + 1
                continue
            size = {T_INT32: 4, T_INT16: 2, T_FLOAT: 4, T_STRING: 4}.get(tcode)
            if size is None:
                raise GbnlError(f"unsupported type code {tcode}")
            kind = {T_INT32: "i32", T_INT16: "i16", T_FLOAT: "f32", T_STRING: "str"}[tcode]
            items.append({"kind": kind, "size": size, "offset": toff})
            pos = toff + size
            calc = toff + size
        if pending is not None:
            resolve_pending(self.msg_descr_size)
        if pos < self.msg_descr_size:
            items.append({"kind": "pad", "size": self.msg_descr_size - pos, "offset": pos})
        return items
    def _parse_messages(self) -> None:
        e = self.endian
        for m in range(self.count_msgs):
            base = self.descr_offset + m * self.msg_descr_size
            fields: list[Field] = []
            for it in self.items:
                size = it["size"]
                kind = it["kind"]
                cur = it["offset"]
                fld = Field(kind, size, cur)
                if kind == "str":
                    off, = struct.unpack_from(e + "I", self.data, base + cur)
                    if off == 0xFFFFFFFF:
                        fld.is_null = True
                    else:
                        fld.str_offset = off
                        fld.str_raw = self._read_cstring(self.offset_msgs + off)
                elif kind == "fixstr":
                    raw = self.data[base + cur: base + cur + size]
                    end = raw.find(b"\0")
                    fld.str_raw = raw if end < 0 else raw[:end]
                fields.append(fld)
            self.messages.append(fields)

    def _read_cstring(self, pos: int) -> bytes:
        end = self.data.find(b"\0", pos)
        if end < 0:
            raise GbnlError(f"unterminated string at {pos:#x}")
        return self.data[pos:end]

    # ------------------------------------------------------------------ query
    @property
    def has_strings(self) -> bool:
        return self.flags == 1 and self.offset_msgs != 0

    def text_fields(self):
        """Yield (msg_index, field_index, raw_bytes) for every real string."""
        for mi, fields in enumerate(self.messages):
            for fi, fld in enumerate(fields):
                if fld.kind in ("str", "fixstr") and not fld.is_null:
                    yield mi, fi, (fld.str_raw or b"")

    def string_table(self) -> dict[int, bytes]:
        """offset -> string, for every string actually referenced."""
        out: dict[int, bytes] = {}
        for flds in self.messages:
            for fld in flds:
                if fld.str_offset is not None:
                    out.setdefault(fld.str_offset, fld.str_raw or b"")
        return out

    def strings_end(self) -> int:
        """Absolute file offset of the first byte past the last referenced string.

        NOTE: this is an *absolute* position inside the blob (``offset_msgs``
        plus the largest referenced table offset plus its terminator), because
        every caller needs it to place appended strings.
        """
        end = 0
        for off, s in self.string_table().items():
            end = max(end, off + len(s) + 1)
        return self.offset_msgs + end if end else self.offset_msgs

    def tail_limit(self) -> int:
        """Offset of the aligned boundary that ends the string region."""
        if self.is_gstl:
            body_end = self.header_offset  # header is at 0, no footer
            return body_end
        return self.header_offset  # GBN footer starts here

    # ------------------------------------------------------------------ patch
    def patch_string(self, msg_index: int, field_index: int, new: bytes) -> tuple[bytes, bool]:
        """Return (new_blob, grew) replacing one string with minimal byte changes.

        * identical length  -> in-place overwrite
        * shorter           -> in-place overwrite + zero fill of the old slot
        * longer            -> reuse an identical existing table entry if there is
                               one, else append a new entry in the alignment
                               padding, else grow the blob (padding is
                               recomputed and the footer is re-emitted last).
        """
        fld = self.messages[msg_index][field_index]
        if fld.kind not in ("str", "fixstr"):
            raise GbnlError("field is not a string")
        if fld.is_null:
            raise GbnlError("cannot patch a null string field")
        if fld.kind == "fixstr":
            if len(new) + 1 > fld.size:
                raise GbnlError("fixed-length string cannot grow")
            buf = bytearray(self.data)
            base = self.descr_offset + msg_index * self.msg_descr_size + fld.offset
            buf[base:base + fld.size] = new + b"\0" * (fld.size - len(new))
            return bytes(buf), False

        old = fld.str_raw or b""
        abs_pos = self.offset_msgs + fld.str_offset
        e = self.endian
        if len(new) <= len(old):
            buf = bytearray(self.data)
            buf[abs_pos:abs_pos + len(new)] = new
            buf[abs_pos + len(new):abs_pos + len(old) + 1] = b"\0" * (len(old) + 1 - len(new))
            return bytes(buf), False

        field_pos = self.descr_offset + msg_index * self.msg_descr_size + fld.offset

        # 1. reuse an identical existing string (the original generator dedups)
        for off, s in self.string_table().items():
            if s == new:
                buf = bytearray(self.data)
                struct.pack_into(e + "I", buf, field_pos, off)
                return bytes(buf), False

        used_end = max(self.strings_end(), self.offset_msgs)
        limit = self.tail_limit()
        need = len(new) + 1
        if used_end + need <= limit:
            buf = bytearray(self.data)
            buf[used_end:used_end + need] = new + b"\0"
            struct.pack_into(e + "I", buf, field_pos, used_end - self.offset_msgs)
            return bytes(buf), False
        # 2. grow: keep [0 .. used_end) verbatim, append the new string, re-pad,
        #    re-emit the 0x40 header/footer (values unchanged).
        head = bytes(self.data[:used_end])
        body = head + new + b"\0"
        if not self.is_gstl:
            body += b"\0" * (_align(len(body)) - len(body))
            body += self.data[self.header_offset:]
        else:
            body += b"\0" * (_align(len(body)) - len(body))
        buf = bytearray(body)
        struct.pack_into(e + "I", buf, field_pos, used_end - self.offset_msgs)
        return bytes(buf), True
