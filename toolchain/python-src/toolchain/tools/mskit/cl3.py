"""CL3 container (`CL3L` little-endian / `CL3B` big-endian).

Layout (offsets are relative to the start of the CL3 payload, i.e. the
decompressed BILZ body of a GDAT entry — the "wrapper" coordinate mode that the
whole Mary Skelter corpus uses):

    +0x00  char[3]  'CL3'
    +0x03  char     'L' | 'B'
    +0x04  u32      field_04   (0 in every observed file)
    +0x08  u32      field_08   (3 in every observed file)
    +0x0c  u32      sections_count
    +0x10  u32      sections_offset
    +0x14  u32      field_14

    Section record (0x50 bytes) at sections_offset + i*0x50:
        +0x00  char[0x20] name (NUL padded)
        +0x20  u32  count
        +0x24  u32  data_size      (logical/allocation bound, NOT a hard EOF)
        +0x28  u32  data_offset
        +0x2c  9 x u32 reserved (zero in every observed file)

    FILE_COLLECTION data at data_offset:
        count x FileEntry (0x230 bytes):
            +0x000  char[0x200] name (NUL padded, UTF-8)
            +0x200  u32 field_200
            +0x204  u32 local_offset   (relative to the FILE_COLLECTION data offset)
            +0x208  u32 size
            +0x20c  u32 link_start
            +0x210  u32 link_count
        then the file payloads themselves.

`FILE_LINK` count is zero in the resources inspected so far (the section still
exists and its data_offset tracks the end of the collection block).
"""
from __future__ import annotations

import struct

MAGIC = b"CL3"
SECTION_SIZE = 0x50
ENTRY_SIZE = 0x230
NAME_SIZE = 0x200
COLLECTION = "FILE_COLLECTION"
FILE_LINK = "FILE_LINK"


class Cl3Error(Exception):
    pass


def _cstr(raw: bytes, encoding: str = "utf-8") -> str:
    raw = raw.split(b"\0", 1)[0]
    try:
        return raw.decode(encoding)
    except UnicodeDecodeError:
        return raw.decode("latin-1")


def encode_name(name: str, size: int = NAME_SIZE) -> bytes:
    raw = name.encode("utf-8") + b"\0"
    if len(raw) > size:
        raise Cl3Error(f"name too long: {name!r}")
    return raw + b"\0" * (size - len(raw))


class Section:
    __slots__ = ("index", "name", "count", "data_size", "data_offset", "reserved", "record_offset")

    def __init__(self, index, name, count, data_size, data_offset, reserved, record_offset):
        self.index = index
        self.name = name
        self.count = count
        self.data_size = data_size
        self.data_offset = data_offset
        self.reserved = reserved
        self.record_offset = record_offset

    def __repr__(self) -> str:  # pragma: no cover
        return (f"Section({self.index}, {self.name!r}, count={self.count}, "
                f"size={self.data_size}, off={self.data_offset:#x})")


class FileEntry:
    __slots__ = ("index", "name", "field_200", "local_offset", "size", "link_start", "link_count")

    def __init__(self, index, name, field_200, local_offset, size, link_start, link_count):
        self.index = index
        self.name = name
        self.field_200 = field_200
        self.local_offset = local_offset
        self.size = size
        self.link_start = link_start
        self.link_count = link_count

    def as_dict(self) -> dict:
        return {"name": self.name, "local_offset": self.local_offset, "size": self.size,
                "field_200": self.field_200, "link_start": self.link_start,
                "link_count": self.link_count}

    def __repr__(self) -> str:  # pragma: no cover
        return f"FileEntry({self.index}, {self.name!r}, off={self.local_offset:#x}, size={self.size})"


class Cl3:
    """Parsed CL3 payload. `data` is the verbatim payload; parsing is lazy-safe."""

    def __init__(self, data: bytes, validate: bool = True):
        self.data = data
        self.sections: list[Section] = []
        self.files: list[FileEntry] = []
        pos = data.find(MAGIC)
        if pos < 0:
            raise Cl3Error("no CL3 magic in payload")
        if pos + 0x18 > len(data):
            raise Cl3Error("truncated CL3 header")
        echar = data[pos + 3:pos + 4]
        if echar not in (b"L", b"B"):
            raise Cl3Error(f"bad CL3 endian char {echar!r}")
        self.magic_offset = pos
        self.endian = "<" if echar == b"L" else ">"
        (self.field_04, self.field_08, self.sections_count,
         self.sections_offset, self.field_14) = struct.unpack_from(self.endian + "IIIII", data, pos + 4)
        if validate:
            if self.field_04 != 0:
                raise Cl3Error(f"unexpected field_04={self.field_04}")
            if not (0 < self.sections_count <= 64):
                raise Cl3Error(f"implausible sections_count={self.sections_count}")
        self.section_base = 0  # wrapper coordinates
        self._parse_sections()
        self.collection = self.section(COLLECTION)
        if self.collection is None:
            raise Cl3Error("no FILE_COLLECTION section")
        self._parse_files()

    # ------------------------------------------------------------------ parse
    def _parse_sections(self) -> None:
        e, base = self.endian, self.section_base
        for i in range(self.sections_count):
            rec = base + self.sections_offset + i * SECTION_SIZE
            if rec + SECTION_SIZE > len(self.data):
                raise Cl3Error(f"section {i} record outside payload")
            name = _cstr(self.data[rec:rec + 0x20], "ascii")
            vals = struct.unpack_from(e + "12I", self.data, rec + 0x20)
            self.sections.append(Section(i, name, vals[0], vals[1], vals[2], list(vals[3:]), rec))

    def _parse_files(self) -> None:
        c = self.collection
        table = self.section_base + c.data_offset
        need = table + c.count * ENTRY_SIZE
        if need > len(self.data):
            raise Cl3Error(f"FileEntry table outside payload ({need} > {len(self.data)})")
        self.collection_table_offset = table
        e = self.endian
        for i in range(c.count):
            rec = table + i * ENTRY_SIZE
            name = _cstr(self.data[rec:rec + NAME_SIZE])
            f200, lo, sz, ls, lc = struct.unpack_from(e + "5I", self.data, rec + NAME_SIZE)
            self.files.append(FileEntry(i, name, f200, lo, sz, ls, lc))

    # ------------------------------------------------------------------ query
    def section(self, name: str) -> Section | None:
        for s in self.sections:
            if s.name == name:
                return s
        return None

    def file(self, index: int) -> FileEntry:
        return self.files[index]

    def file_data(self, index: int) -> bytes:
        f = self.files[index]
        start = self.section_base + self.collection.data_offset + f.local_offset
        return self.data[start:start + f.size]

    def files_named(self, name: str) -> list[FileEntry]:
        return [f for f in self.files if f.name == name]

    def describe(self) -> dict:
        return {
            "endian": self.endian,
            "field_04": self.field_04,
            "field_08": self.field_08,
            "sections": [{"name": s.name, "count": s.count, "data_size": s.data_size,
                          "data_offset": s.data_offset} for s in self.sections],
            "files": [f.as_dict() for f in self.files],
        }

    # ------------------------------------------------------------------ patch
    def rebuild(self, replacements: dict[int, bytes], block_align: int = 0x100) -> bytes:
        """Return a new payload with `replacements` (file index -> new bytes) applied.

        Unchanged files keep their exact bytes, and every FileEntry record is
        copied from the source and only patched in place — the 0x1c reserved
        bytes that follow the five structured fields of each 0x230-byte record
        are therefore preserved verbatim.
        """
        if not replacements:
            return self.data
        c = self.collection
        coll_start = self.section_base + c.data_offset
        table_start = coll_start
        old_actual = ENTRY_SIZE * len(self.files) + sum(f.size for f in self.files)
        old_block_end = min(coll_start + c.data_size, len(self.data))

        table = bytearray(self.data[table_start:table_start + ENTRY_SIZE * len(self.files)])
        body = bytearray()
        cursor = ENTRY_SIZE * len(self.files)
        offsets = []
        for f in self.files:
            payload = replacements.get(f.index)
            if payload is None:
                payload = self.file_data(f.index)
            offsets.append(cursor)
            body += payload
            cursor += len(payload)
        for i, (f, off) in enumerate(zip(self.files, offsets)):
            payload = replacements.get(f.index)
            size = len(payload) if payload is not None else f.size
            base = i * ENTRY_SIZE
            table[base:base + NAME_SIZE] = encode_name(f.name)
            struct.pack_into(self.endian + "5I", table, base + NAME_SIZE, f.field_200,
                             off, size, f.link_start, f.link_count)

        new_actual = len(table) + len(body)
        # anything stored between the last file payload and the logical block end
        # is preserved verbatim (empty in every resource measured so far)
        block_tail = self.data[coll_start + old_actual: old_block_end]
        physical_old = old_block_end - coll_start
        physical_new = new_actual + len(block_tail)
        delta = physical_new - physical_old

        head = bytearray(self.data[:coll_start])
        e = self.endian
        for s in self.sections:
            rec = s.record_offset
            if s is c:
                struct.pack_into(e + "I", head, rec + 0x24, c.data_size + delta)
            elif s.data_offset >= c.data_offset:
                struct.pack_into(e + "I", head, rec + 0x28, s.data_offset + delta)
        return bytes(head) + bytes(table) + bytes(body) + bytes(block_tail) + self.data[old_block_end:]
