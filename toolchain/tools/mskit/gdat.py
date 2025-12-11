"""GDAT container (`GxArchivedFile*.dat`).

Layout (little-endian):

    +0x00  char[4]  'GDAT'
    +0x04  u32      entry_count
    +0x08  u32[2]xN {offset, size}      entry_count records

Measured properties of every archive inspected so far:

* entry payloads are stored back to back, starting at 8 + 8*count;
* every entry size is a multiple of 16 (offsets therefore are too);
* there is no trailing slack: max(offset+size) == filesize.

Rebuild rule: keep the entry order, keep every payload verbatim unless its
resource was intentionally modified, then recompute the offset table.
"""
from __future__ import annotations

import struct
from pathlib import Path

MAGIC = b"GDAT"
HEADER_SIZE = 8
ENTRY_SIZE = 8
ALIGN = 16
MAX_ENTRIES = 5_000_000


class GdatError(Exception):
    pass


class GdatEntry:
    __slots__ = ("index", "offset", "size", "name")

    def __init__(self, index: int, offset: int, size: int):
        self.index = index
        self.offset = offset
        self.size = size
        self.name = f"entry{index:05d}"

    def __repr__(self) -> str:  # pragma: no cover
        return f"GdatEntry({self.index}, {self.offset:#x}, {self.size})"


class Gdat:
    """Read-only view of a GDAT archive, plus a verbatim payload store."""

    def __init__(self, path: Path | str):
        self.path = Path(path)
        self.fp = self.path.open("rb")
        head = self.fp.read(HEADER_SIZE)
        if len(head) < HEADER_SIZE or head[:4] != MAGIC:
            self.fp.close()
            raise GdatError(f"{self.path}: not a GDAT archive")
        count = struct.unpack_from("<I", head, 4)[0]
        size = self.path.stat().st_size
        if not (0 < count <= MAX_ENTRIES) or HEADER_SIZE + ENTRY_SIZE * count > size:
            self.fp.close()
            raise GdatError(f"{self.path}: implausible entry count {count}")
        table = self.fp.read(ENTRY_SIZE * count)
        self.entries = []
        for i in range(count):
            off, sz = struct.unpack_from("<II", table, i * ENTRY_SIZE)
            if off + sz > size:
                self.fp.close()
                raise GdatError(f"{self.path}: entry {i} span outside file")
            self.entries.append(GdatEntry(i, off, sz))
        self.count = count
        self.file_size = size

    # ------------------------------------------------------------------ io
    def close(self) -> None:
        if not self.fp.closed:
            self.fp.close()

    def __enter__(self) -> "Gdat":
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    def read(self, entry: GdatEntry | int) -> bytes:
        e = self.entries[entry] if isinstance(entry, int) else entry
        self.fp.seek(e.offset)
        return self.fp.read(e.size)

    def read_prefix(self, entry: GdatEntry | int, want: int) -> bytes:
        e = self.entries[entry] if isinstance(entry, int) else entry
        self.fp.seek(e.offset)
        return self.fp.read(min(want, e.size))

    # -------------------------------------------------------------- payloads
    def payload_info(self, entry: GdatEntry | int, scan_limit: int = 0x10000):
        """Return (BilzHeader|None, entry, opaque_prefix_len).

        `BilzHeader.offset` is relative to the start of the entry payload.
        """
        from . import bilz  # local import keeps the container module standalone
        e = self.entries[entry] if isinstance(entry, int) else entry
        pre = self.read_prefix(e, scan_limit)
        bh = bilz.find_header(pre)
        return bh, e, (bh.offset if bh else 0)

    def read_payload(self, entry: GdatEntry | int, want: int | None = None) -> bytes:
        """Fully (or partially) decompressed payload of one entry."""
        from . import bilz
        bh, e, _ = self.payload_info(entry)
        if bh is None:
            self.fp.seek(e.offset)
            return self.fp.read(e.size if want is None else min(want, e.size))
        if bh.offset + bilz.HEADER_SIZE + bh.csize > e.size:
            raise GdatError(f"entry {e.index}: BILZ body exceeds entry size")
        if want is not None:
            return bilz.decompress_stream(self.fp, _shift(bh, e.offset), want)
        self.fp.seek(e.offset + bh.payload_offset)
        raw = self.fp.read(bh.csize)
        return bilz.inflate(raw, bh.algo)


def _shift(bh, delta: int):
    """Copy a BilzHeader with its offsets rebased by `delta`."""
    import copy
    nb = copy.copy(bh)
    nb.offset = bh.offset + delta
    return nb


def build(entries: list[tuple[int, bytes]]) -> bytes:
    """Serialize a GDAT from (original_index, payload_bytes) pairs.

    The shipped archives store the payloads strictly back to back — measured on
    every archive in this corpus the gap between consecutive entries is zero,
    and most entry sizes are *not* multiples of 16 — so no alignment padding is
    ever inserted here.  Rebuilding unchanged entries therefore reproduces the
    source byte for byte.
    """
    out = bytearray()
    table = bytearray()
    data = bytearray()
    offset = HEADER_SIZE + ENTRY_SIZE * len(entries)
    for _, payload in entries:
        table += struct.pack("<II", offset, len(payload))
        data += payload
        offset += len(payload)
    out += MAGIC + struct.pack("<I", len(entries))
    out += table
    out += data
    return bytes(out)
