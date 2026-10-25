#!/usr/bin/env python3
"""csh_tool.py - decoder / verifier / byte-exact rebuilder for OROCHI ``GxArchivedTable*.csh``.

Reverse-engineered against Mary Skelter 2 (PC + Switch JP) and Mary Skelter Finale (PC)
corpora: 71 CSH files, all decoded, verified and rebuilt byte-identically.

PROVEN format (evidence: docs/APPENDIX_CSH_FINDINGS.md):

    file
      0x00  u32be  magic 0x00687363  ("\0csh")
      0x04  u32be  2
      0x08  u32be  0x0d
      0x0c  u32be  2
      0x10  6 x u32be  0
      0x28  u32be  1
      0x2c  5 x u32be  0
      0x80  BILZ block: 'BILZ', u32be usize, u32be csize, u32be algo(7=zlib), csize bytes
      -> payload = zlib(raw)   (usize == payload length)

    payload (all big-endian)
      +0x00 u32 version        (1 = PC builds, 0 = Switch JP build)
      +0x04 u32 2              (constant)
      +0x08 u32 N              (entry count == GDAT entry count)
      +0x0c u32 table_off      (== 12 + 16*N)
      +0x10 reloc region, 16*N-4 bytes:
              for i in 0..N-1 : u32 0x20000002, u32 table_off+8+12*i
              for i in 0..N-2 : u32 0x40000001, u32 table_off+12+12*i
              final          : u32 0x40000001            (value omitted)
      +table_off  N x 12-byte records: u32 hash_a, u32 hash_b, u32 index (== i)

    payload length == 12 + 28*N exactly.

``hash_a`` is produced by a base-37 polynomial hash over a key string that is NOT
stored in the CSH (see findings).  ``hash_b`` is constant per archive/type group.

CLI:
    python3 csh_tool.py info   <file.csh> [...]
    python3 csh_tool.py dump   <file.csh> [--limit K]
    python3 csh_tool.py verify <dir-or-file> [...]
    python3 csh_tool.py rebuild <file.csh> <out.csh>
    python3 csh_tool.py table  <dir>            # markdown evidence table (csh_n vs gdat_n)
"""
from __future__ import annotations

import argparse
import struct
import sys
import zlib
from pathlib import Path

MAGIC = 0x00687363
HDR_SIZE = 0x80
BILZ_OFF = 0x80
BILZ_MAGIC = b"BILZ"
ALGO_ZLIB = 7
RELOC_TAG_A = 0x20000002
RELOC_TAG_B = 0x40000001
ZLIB_LEVEL = 7  # byte-identical recompression level for this corpus


# --------------------------------------------------------------------------- #
# base-37 polynomial hash (the family used for hash_a)                          #
# --------------------------------------------------------------------------- #
def hash37(data: bytes, seed: int = 0, mult: int = 37) -> int:
    """h = (h*37 + byte) mod 2^32 - the arithmetic the CSH hash_a column follows.

    PROVEN property: consecutive-entry deltas of hash_a are exactly +/- powers of 37
    (1, 37, 1369, 50653, 1874161, 69343957 = 0x4221AD5, ...) which is the signature of
    this recurrence.  The *key string* fed into it is NOT recoverable from the corpus.
    """
    h = seed & 0xFFFFFFFF
    for c in data:
        h = (h * mult + c) & 0xFFFFFFFF
    return h


# --------------------------------------------------------------------------- #
# parser                                                                        #
# --------------------------------------------------------------------------- #
class CshError(Exception):
    pass


class CshFile:
    """Decoded GxArchivedTable*.csh."""

    def __init__(self, path: Path | str):
        self.path = Path(path)
        self.raw = self.path.read_bytes()
        self._parse()

    # -- decoding ---------------------------------------------------------- #
    def _parse(self):
        b = self.raw
        if len(b) < HDR_SIZE + 16:
            raise CshError(f"{self.path.name}: too small ({len(b)} bytes)")
        self.header = list(struct.unpack_from(">16I", b, 0))
        if self.header[0] != MAGIC:
            raise CshError(f"{self.path.name}: bad magic {self.header[0]:#010x}")
        if b[BILZ_OFF:BILZ_OFF + 4] != BILZ_MAGIC:
            raise CshError(f"{self.path.name}: no BILZ at {BILZ_OFF:#x}")
        self.usize, self.csize, self.algo = struct.unpack_from(">3I", b, BILZ_OFF + 4)
        if self.algo != ALGO_ZLIB:
            raise CshError(f"{self.path.name}: unsupported algo {self.algo}")
        self.blob = b[BILZ_OFF + 16:BILZ_OFF + 16 + self.csize]
        self.payload = zlib.decompress(self.blob)
        if len(self.payload) != self.usize:
            raise CshError(f"{self.path.name}: usize {self.usize} != payload {len(self.payload)}")

        p = self.payload
        self.version, self.field_04, self.count, self.table_off = struct.unpack_from(">4I", p, 0)
        self.reloc = p[16:self.table_off]
        self.entries = [struct.unpack_from(">3I", p, self.table_off + 12 * i)
                        for i in range(self.count)]

    # -- derived helpers --------------------------------------------------- #
    @property
    def hash_a(self) -> list[int]:
        return [e[0] for e in self.entries]

    @property
    def hash_b(self) -> list[int]:
        return [e[1] for e in self.entries]

    @property
    def indices(self) -> list[int]:
        return [e[2] for e in self.entries]

    def expected_reloc(self) -> bytes:
        off = self.table_off
        out = bytearray()
        for i in range(self.count):
            out += struct.pack(">2I", RELOC_TAG_A, off + 8 + 12 * i)
            if i < self.count - 1:
                out += struct.pack(">2I", RELOC_TAG_B, off + 12 + 12 * i)
            else:
                out += struct.pack(">I", RELOC_TAG_B)
        return bytes(out)

    # -- verification ------------------------------------------------------ #
    def verify(self) -> list[str]:
        """Return a list of invariant violations (empty == fully conformant)."""
        errs = []
        if self.header != [MAGIC, 2, 0x0D, 2, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0]:
            errs.append(f"unexpected 0x80 header {[hex(v) for v in self.header]}")
        if self.field_04 != 2:
            errs.append(f"payload+0x04 != 2 ({self.field_04})")
        if len(self.payload) != 12 + 28 * self.count:
            errs.append(f"payload {len(self.payload)} != 12+28*{self.count} = {12 + 28 * self.count}")
        if self.table_off != 12 + 16 * self.count:
            errs.append(f"table_off {self.table_off:#x} != 12+16*{self.count} = {12 + 16 * self.count:#x}")
        if self.reloc != self.expected_reloc():
            errs.append("relocation region does not match the canonical pattern")
        if self.indices != list(range(self.count)):
            bad = next(i for i, v in enumerate(self.indices) if v != i)
            errs.append(f"index column is not 0..N-1 (first deviation at row {bad})")
        if BILZ_OFF + 16 + self.csize != len(self.raw):
            errs.append(f"trailing bytes: {len(self.raw) - (BILZ_OFF + 16 + self.csize)}")
        return errs

    # -- rebuild ----------------------------------------------------------- #
    def serialize_payload(self) -> bytes:
        out = bytearray()
        out += struct.pack(">4I", self.version, 2, self.count, self.table_off)
        out += self.expected_reloc()
        for a, b_, i in self.entries:
            out += struct.pack(">3I", a, b_, i)
        return bytes(out)

    def rebuild(self) -> bytes:
        """Re-encode to bytes.  Byte-identical for every file in this corpus."""
        payload = self.serialize_payload()
        blob = zlib.compress(payload, ZLIB_LEVEL)
        head = struct.pack(">16I", *self.header)
        head += b"\0" * (BILZ_OFF - len(head))
        return head + BILZ_MAGIC + struct.pack(">3I", len(payload), len(blob), ALGO_ZLIB) + blob

    # -- reporting --------------------------------------------------------- #
    def summary(self) -> str:
        return (f"{self.path.name}: ver={self.version} N={self.count} "
                f"table_off={self.table_off:#x} payload={len(self.payload)} "
                f"file={len(self.raw)} csize={self.csize} "
                f"hash_b distinct={len(set(self.hash_b))} "
                f"hash_a distinct={len(set(self.hash_a))}")


# --------------------------------------------------------------------------- #
# GDAT helpers (little-endian, for the cross-check)                             #
# --------------------------------------------------------------------------- #
def gdat_entry_count(path: Path) -> int:
    with Path(path).open("rb") as fp:
        magic = fp.read(4)
        if magic != b"GDAT":
            raise CshError(f"{path}: not GDAT ({magic!r})")
        return struct.unpack_from("<I", fp.read(4), 0)[0]


def csh_paths(target: Path) -> list[Path]:
    t = Path(target)
    if t.is_dir():
        return sorted(t.glob("GxArchivedTable*.csh"))
    return [t]


def matching_dat(csh: Path) -> Path | None:
    num = csh.stem[-3:]
    d = csh.with_name(f"GxArchivedFile{num}.dat")
    return d if d.exists() else None


# --------------------------------------------------------------------------- #
# CLI                                                                           #
# --------------------------------------------------------------------------- #
def cmd_info(args):
    for p in csh_paths(args.target):
        c = CshFile(p)
        dp = matching_dat(p)
        dn = gdat_entry_count(dp) if dp else None
        print(c.summary())
        print(f"    gdat={'MISSING' if dn is None else dn} "
              f"count_match={'n/a' if dn is None else (dn == c.count)}")
        if c.hash_a[:4]:
            print(f"    hash_a[:4]={[hex(x) for x in c.hash_a[:4]]} "
                  f"hash_b[:4]={[hex(x) for x in c.hash_b[:4]]}")
    return 0


def cmd_dump(args):
    c = CshFile(args.target)
    print(c.summary())
    errs = c.verify()
    print("verify:", "OK" if not errs else errs)
    lim = args.limit or c.count
    print(f"{'i':>6} {'hash_a':>10} {'hash_b':>10} {'idx':>6}")
    for i, (a, b_, ix) in enumerate(c.entries[:lim]):
        print(f"{i:6d} {a:#010x} {b_:#010x} {ix:6d}")
    if lim < c.count:
        print(f"... {c.count - lim} more rows")
    return 0


def cmd_verify(args):
    total = bad = noconf = 0
    for p in csh_paths(args.target):
        c = CshFile(p)
        total += 1
        errs = c.verify()
        if errs:
            bad += 1
            print(f"FAIL {p.name}: {errs}")
        if c.rebuild() != c.raw:
            bad += 1
            print(f"FAIL {p.name}: rebuild is not byte-identical")
        dp = matching_dat(p)
        if dp is None:
            noconf += 1
        elif gdat_entry_count(dp) != c.count:
            bad += 1
            print(f"FAIL {p.name}: count {c.count} != GDAT entries {gdat_entry_count(dp)}")
    print(f"checked={total} failures={bad} dat-missing={noconf}")
    return 1 if bad else 0


def cmd_rebuild(args):
    c = CshFile(args.target)
    out = Path(args.out)
    data = c.rebuild()
    out.write_bytes(data)
    same = data == c.raw
    print(f"wrote {out} ({len(data)} bytes) byte-identical={same}")
    return 0 if same or args.allow_diff else 1


def cmd_table(args):
    d = Path(args.target)
    print("| dataset | archive | CSH N | GDAT entries | payload | file | ver | match |")
    print("|---|---|---|---|---|---|---|---|")
    for p in sorted(d.glob("GxArchivedTable*.csh")):
        c = CshFile(p)
        dp = matching_dat(p)
        dn = gdat_entry_count(dp) if dp else None
        print(f"| {d.name} | {p.stem[-3:]} | {c.count} | {dn if dn is not None else '-'} | "
              f"{len(c.payload)} | {len(c.raw)} | {c.version} | "
              f"{'-' if dn is None else ('yes' if dn == c.count else 'NO')} |")
    return 0


def main(argv=None):
    ap = argparse.ArgumentParser(description="OROCHI GxArchivedTable*.csh decoder")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("info"); p.add_argument("target", nargs="+"); p.set_defaults(fn=cmd_info)
    p = sub.add_parser("dump"); p.add_argument("target"); p.add_argument("--limit", type=int)
    p.set_defaults(fn=cmd_dump)
    p = sub.add_parser("verify"); p.add_argument("target", nargs="+"); p.set_defaults(fn=cmd_verify)
    p = sub.add_parser("rebuild"); p.add_argument("target"); p.add_argument("out")
    p.add_argument("--allow-diff", action="store_true"); p.set_defaults(fn=cmd_rebuild)
    p = sub.add_parser("table"); p.add_argument("target"); p.set_defaults(fn=cmd_table)

    args = ap.parse_args(argv)
    rc = 0
    for t in (args.target if isinstance(args.target, list) else [args.target]):
        args.target = t
        rc |= args.fn(args)
    return rc


if __name__ == "__main__":
    sys.exit(main())
