"""BILZ wrapper (Orochi compressed blob).

Layout (BIG-endian unless noted):

    offset +0x00  char[4]  'BILZ'
    offset +0x04  u32be    uncompressed_size
    offset +0x08  u32be    compressed_size
    offset +0x0c  u32be    algorithm   (7 = zlib, 0 = stored)

Observed in this corpus the wrapper does not necessarily start at offset 0 of
the owning GDAT entry: MSF 064 entries carry 0x80 bytes of opaque prefix before
the 'BILZ' magic.  Those bytes are preserved verbatim by this module.
"""
from __future__ import annotations

import struct
import zlib

MAGIC = b"BILZ"
HEADER_SIZE = 16
ALGO_STORED = 0
ALGO_ZLIB = 7

# GDAT entries in this corpus were seen with a 0x80-byte prefix before BILZ.
DEFAULT_SCAN_LIMIT = 0x10000


class BilzError(Exception):
    pass


class BilzHeader:
    __slots__ = ("offset", "usize", "csize", "algo")

    def __init__(self, offset: int, usize: int, csize: int, algo: int):
        self.offset = offset
        self.usize = usize
        self.csize = csize
        self.algo = algo

    def __repr__(self) -> str:  # pragma: no cover - debug helper
        return (f"BilzHeader(offset={self.offset:#x}, usize={self.usize}, "
                f"csize={self.csize}, algo={self.algo})")

    @property
    def payload_offset(self) -> int:
        return self.offset + HEADER_SIZE

    def as_dict(self) -> dict:
        return {"bilz_offset": self.offset, "uncompressed_size": self.usize,
                "compressed_size": self.csize, "algorithm": self.algo}


def find_header(buf: bytes, start: int = 0, scan_limit: int = DEFAULT_SCAN_LIMIT) -> BilzHeader | None:
    """Locate a plausible BILZ header in `buf` (searching up to `scan_limit`)."""
    pos = start
    end = min(len(buf), start + scan_limit)
    while True:
        p = buf.find(MAGIC, pos, end)
        if p < 0:
            return None
        if p + HEADER_SIZE <= len(buf):
            usize, csize, algo = struct.unpack_from(">III", buf, p + 4)
            if 0 < usize < 2 ** 31 and 0 < csize < 2 ** 31 and algo in (ALGO_STORED, ALGO_ZLIB):
                return BilzHeader(p, usize, csize, algo)
        pos = p + 1


def inflate(data: bytes, algo: int) -> bytes:
    if algo == ALGO_STORED:
        return data
    if algo == ALGO_ZLIB:
        return zlib.decompress(data)
    raise BilzError(f"unsupported BILZ algorithm {algo}")


def decompress_stream(fp, header: BilzHeader, want: int | None = None,
                      chunk: int = 1 << 20) -> bytes:
    """Stream `header` from an open file object; decompress at most `want` bytes."""
    fp.seek(header.payload_offset)
    if header.algo == ALGO_STORED:
        return fp.read(header.csize if want is None else min(want, header.csize))
    if header.algo != ALGO_ZLIB:
        raise BilzError(f"unsupported BILZ algorithm {header.algo}")
    if want is None:
        return zlib.decompress(fp.read(header.csize))
    dec = zlib.decompressobj()
    out = bytearray()
    remaining = header.csize
    while remaining > 0 and len(out) < want:
        block = fp.read(min(chunk, remaining))
        if not block:
            break
        remaining -= len(block)
        try:
            out += dec.decompress(block, want - len(out))
        except zlib.error as exc:  # pragma: no cover - corruption path
            raise BilzError(f"zlib error: {exc}") from exc
        if dec.eof:
            break
    return bytes(out)


def compress(data: bytes, algo: int = ALGO_ZLIB, level: int = 9) -> bytes:
    """Produce the BILZ body (everything after the magic) for `data`."""
    if algo == ALGO_STORED:
        return struct.pack(">III", len(data), len(data), ALGO_STORED) + data
    if algo == ALGO_ZLIB:
        body = zlib.compress(data, level)
        return struct.pack(">III", len(data), len(body), ALGO_ZLIB) + body
    raise BilzError(f"unsupported BILZ algorithm {algo}")


def wrap(data: bytes, algo: int = ALGO_ZLIB, level: int = 9) -> bytes:
    return MAGIC + compress(data, algo, level)
