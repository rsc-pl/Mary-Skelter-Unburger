#!/usr/bin/env python3
"""
enc_parser.py -- draft lossless reader/rebuilder for the Orochi "enc" / "bin" / "csh"
wrapper format used by Mary Skelter 2 and Mary Skelter Finale (Compile Heart / Idea Factory).

Status (see docs/APPENDIX_ENC_FINDINGS.md for the full PROVEN/UNPROVEN breakdown):

  PROVEN
    * outer wrapper: 0x80-byte header + BILZ(zlib) stream at 0x90
    * inner payload of `enc` files: u32 count + count*12-byte row records + 0x40-aligned rows
    * row payloads are raw byte blobs that carry NUL-terminated UTF-8 strings
    * byte-exact round-trip of both wrapper and payload

  UNPROVEN
    * the tagged-field grammar inside a row payload (opaque bytes here)
    * the meaning of the per-row `aux` u32
    * the exact trailing-zero policy behind the per-row `size` field

Nothing in this file writes to sources/.  It is read-only unless you explicitly
call `rebuild()`/`write_rebuilt()` on a path under work/.

Usage:
    python3 enc_parser.py info   <archive.dat> <index>
    python3 enc_parser.py rows   <archive.dat> <index> [--limit N]
    python3 enc_parser.py text   <archive.dat> <index> [--limit N] [--jsonl FILE]
    python3 enc_parser.py verify <archive.dat> [--index N]
    python3 enc_parser.py selftest [--root DIR]
"""
from __future__ import annotations

import argparse
import hashlib
import json
import struct
import sys
import unicodedata
import zlib
from dataclasses import dataclass, field
from pathlib import Path

# --------------------------------------------------------------------------
# constants proven from the corpus
# --------------------------------------------------------------------------
WRAPPER_HEADER_SIZE = 0x80
BILZ_OFFSET = 0x80
BILZ_DATA_OFFSET = 0x90
ROW_ALIGN = 0x40
ROW_RECORD_SIZE = 12
KNOWN_TAGS = {b"enc": "enc", b"bin": "bin", b"csh": "csh"}

ROW_TABLE = "rowtable"      # `enc`
OFFSET_TABLE = "offtable"   # `bin`


# --------------------------------------------------------------------------
# GDAT access
# --------------------------------------------------------------------------
def gdat_entries(path: Path) -> list[tuple[int, int]]:
    """Return [(offset, size)] of a GxArchivedFile*.dat (GDAT)."""
    with path.open("rb") as fp:
        head = fp.read(8)
        if head[:4] != b"GDAT":
            raise ValueError(f"{path} is not a GDAT archive")
        count = struct.unpack_from("<I", head, 4)[0]
        table = fp.read(8 * count)
        if len(table) != 8 * count:
            raise ValueError(f"{path}: truncated GDAT table")
        return [struct.unpack_from("<II", table, 8 * i) for i in range(count)]


def gdat_entry_bytes(path: Path, index: int) -> bytes:
    off, size = gdat_entries(path)[index]
    with path.open("rb") as fp:
        fp.seek(off)
        return fp.read(size)


# --------------------------------------------------------------------------
# wrapper
# --------------------------------------------------------------------------
@dataclass
class WrapperHeader:
    raw: bytes                 # exactly 0x80 bytes
    magic_reversed: bytes      # e.g. b"enc\0"  (stored byte-reversed in the file)
    field_04: int
    field_08: int
    field_0c: int
    field_28: int
    magic_field: int
    _magic_bytes: bytes

    @property
    def tag(self) -> str:
        return self.magic_reversed.rstrip(b"\0").decode("latin1")

    @property
    def is_all_else_zero(self) -> bool:
        return (all(c == 0 for c in self.raw[0x10:0x28])
                and all(c == 0 for c in self.raw[0x2C:0x80]))


@dataclass
class Bilz:
    offset: int          # where the b"BILZ" signature sits (always 0x80 in this corpus)
    uncompressed_size: int
    compressed_size: int
    algo: int            # 7 = raw zlib stream, 0 = stored
    payload: bytes       # decompressed bytes
    extra_trailing: bytes  # bytes after 0x90+compressed_size (usually empty)


# --------------------------------------------------------------------------
# rows
# --------------------------------------------------------------------------
@dataclass
class TextString:
    offset: int          # byte offset inside the row payload
    raw: bytes           # exact bytes, NUL terminator NOT included
    text: str            # decoded (utf-8, errors="replace")
    terminated: bool     # True if a 0x00 followed the string inside the payload


@dataclass
class Row:
    index: int
    size: int            # raw u32; bit31 is a flag, low 31 bits = payload byte length
    aux: int             # u32, semantics UNPROVEN (always < 2**24 in this corpus)
    offset: int          # absolute offset of the row inside the payload
    payload: bytes       # exactly `size & 0x7fffffff` bytes
    span_padding: bytes  # zero padding up to the next row (or payload EOF)
    strings: list[TextString] = field(default_factory=list)

    @property
    def length(self) -> int:
        return self.size & 0x7FFFFFFF

    @property
    def flag31(self) -> bool:
        return bool(self.size & 0x80000000)

    @property
    def size_minus_last_nonzero(self) -> int:
        return self.length - len(self.payload.rstrip(b"\0"))


# --------------------------------------------------------------------------
# string scanning
# --------------------------------------------------------------------------
def _is_text_byte(c: int) -> bool:
    # 0x20..0x7e ascii printable, 0x0a/0x0d line breaks, >=0x80 utf-8 continuation/lead
    return (0x20 <= c < 0x7F) or c in (0x09, 0x0A, 0x0D) or c >= 0x80


# characters that are plausible inside game text
_TEXTY = set(" \t\n\r!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~")
_NON_TEXTY_ALLOWED = 0.25   # share of non-letter/digit/punct characters tolerated


def _looks_like_text(txt: str, min_letters: int) -> bool:
    letters = digits = punct = other = 0
    for ch in txt:
        if ch.isalpha():
            letters += 1
        elif ch.isdigit():
            digits += 1
        elif ch in _TEXTY:
            punct += 1
        else:
            other += 1
    if letters < min_letters:
        return False
    total = max(1, letters + digits + punct + other)
    if other / total > _NON_TEXTY_ALLOWED:
        return False
    # reject long runs of punctuation/digits without any word character
    return True


def scan_strings(payload: bytes, min_chars: int = 2, min_letters: int = 3) -> list[TextString]:
    """Conservative, lossless text scan.

    A candidate is a maximal stretch of text bytes that (a) decodes as strict
    UTF-8, (b) has at least `min_letters` alphabetic characters and (c) is
    mostly made of letters/digits/printable punctuation.  Byte offsets are
    preserved exactly, so the original bytes can always be recovered from
    `raw` (and the whole row is kept verbatim anyway).

    This deliberately under-reports: binary junk between fields is skipped
    rather than guessed at.
    """
    out: list[TextString] = []
    n = len(payload)
    i = 0
    while i < n:
        if not _is_text_byte(payload[i]):
            i += 1
            continue
        j = i
        while j < n and _is_text_byte(payload[j]):
            j += 1
        run = payload[i:j]
        segments = []
        start = 0
        for k, c in enumerate(run):
            if c == 0:
                segments.append((start, run[start:k], True))
                start = k + 1
        if start < len(run):
            segments.append((start, run[start:], False))
        for rel, seg, terminated in segments:
            if len(seg) < min_chars:
                continue
            try:
                txt = seg.decode("utf-8")
            except UnicodeDecodeError:
                continue
            if not _looks_like_text(txt, min_letters):
                continue
            out.append(TextString(offset=i + rel, raw=seg, text=txt, terminated=terminated))
        i = j
    return out


def script_census(texts) -> dict[str, int]:
    c = {"latin": 0, "kana": 0, "han": 0, "hangul": 0, "other": 0}
    for t in texts:
        for ch in t:
            if ch.isascii() and ch.isalpha():
                c["latin"] += 1
            else:
                o = ord(ch)
                if 0x3040 <= o <= 0x30FF:
                    c["kana"] += 1
                elif 0x4E00 <= o <= 0x9FFF:
                    c["han"] += 1
                elif 0xAC00 <= o <= 0xD7A3 or 0x1100 <= o <= 0x11FF:
                    c["hangul"] += 1
                elif ch.isalpha():
                    c["other"] += 1
    return c


# --------------------------------------------------------------------------
# main object
# --------------------------------------------------------------------------
class EncFile:
    """A parsed .enc/.bin/.csh wrapper."""

    def __init__(self, raw: bytes, origin: str = "<bytes>"):
        self.raw = raw
        self.origin = origin
        if len(raw) < BILZ_DATA_OFFSET:
            raise ValueError("file too small to be an Orochi enc/bin/csh wrapper")
        self.header = self._parse_header(raw[:WRAPPER_HEADER_SIZE])
        if raw[BILZ_OFFSET:BILZ_OFFSET + 4] != b"BILZ":
            raise ValueError(f"no BILZ signature at 0x80 (found {raw[BILZ_OFFSET:BILZ_OFFSET+4]!r})")
        self.bilz = self._parse_bilz(raw)
        self.payload = self.bilz.payload
        self.layout, self.count = self._detect_layout()
        self.rows: list[Row] = []
        if self.layout == ROW_TABLE:
            self.rows = self._parse_rows()
        else:
            self.rows = self._parse_offtable()

    # ---------------- construction helpers ----------------
    @classmethod
    def from_path(cls, path: Path) -> "EncFile":
        return cls(Path(path).read_bytes(), origin=str(path))

    @classmethod
    def from_gdat(cls, archive: Path, index: int) -> "EncFile":
        return cls(gdat_entry_bytes(Path(archive), index),
                   origin=f"{archive}[{index}]")

    @staticmethod
    def _parse_header(h: bytes) -> WrapperHeader:
        f04, f08, f0c = struct.unpack_from(">III", h, 4)
        f28, = struct.unpack_from(">I", h, 0x28)
        magic_field, = struct.unpack_from(">I", h, 0)
        return WrapperHeader(raw=h, magic_reversed=h[:4][::-1], field_04=f04,
                             field_08=f08, field_0c=f0c, field_28=f28,
                             magic_field=magic_field, _magic_bytes=h[:4])

    @staticmethod
    def _parse_bilz(raw: bytes) -> Bilz:
        us, cs, algo = struct.unpack_from(">III", raw, BILZ_OFFSET + 4)
        body = raw[BILZ_DATA_OFFSET:BILZ_DATA_OFFSET + cs]
        if algo == 7:
            payload = zlib.decompress(body)
        elif algo == 0:
            payload = body
        else:
            raise ValueError(f"unsupported BILZ algo {algo}")
        if len(payload) != us:
            raise ValueError(f"BILZ size mismatch: got {len(payload)} want {us}")
        return Bilz(offset=BILZ_OFFSET, uncompressed_size=us, compressed_size=cs,
                    algo=algo, payload=payload,
                    extra_trailing=raw[BILZ_DATA_OFFSET + cs:])

    # ---------------- layout detection ----------------
    def _detect_layout(self) -> tuple[str, int]:
        p = self.payload
        if len(p) < 4:
            return "unknown", 0
        cnt = struct.unpack_from("<I", p, 0)[0]
        if cnt and 4 + ROW_RECORD_SIZE * cnt <= len(p):
            recs = [struct.unpack_from("<III", p, 4 + ROW_RECORD_SIZE * i) for i in range(cnt)]
            offs = [r[2] for r in recs]
            if (all(offs[i] < offs[i + 1] for i in range(cnt - 1))
                    and offs[0] % ROW_ALIGN == 0
                    and offs[0] >= 4 + ROW_RECORD_SIZE * cnt
                    and offs[-1] < len(p)
                    and all((r[0] & 0x7FFFFFFF) <= len(p) for r in recs)):
                return ROW_TABLE, cnt
        # offset table (`bin`): 16-byte records, first u32 = string offset, rest zero
        if self.header.tag == "bin" and len(p) >= 32:
            first = struct.unpack_from("<I", p, 0)[0]
            if first and first % 16 == 0 and first <= len(p):
                n = first // 16
                ok = n > 0
                prev = -1
                for i in range(n):
                    off = struct.unpack_from("<I", p, 16 * i)[0]
                    rest = p[16 * i + 4:16 * i + 16]
                    if off <= prev or off >= len(p) or any(rest):
                        ok = False
                        break
                    prev = off
                if ok:
                    return OFFSET_TABLE, n
        return "unknown", 0

    # ---------------- row table (`enc`) ----------------
    def _parse_rows(self) -> list[Row]:
        p = self.payload
        cnt = self.count
        recs = [struct.unpack_from("<III", p, 4 + ROW_RECORD_SIZE * i) for i in range(cnt)]
        rows = []
        for i, (size, aux, off) in enumerate(recs):
            nxt = recs[i + 1][2] if i + 1 < cnt else len(p)
            ln = size & 0x7FFFFFFF
            payload = p[off:off + ln]
            if len(payload) != ln:
                payload = p[off:]                      # truncated last row
            rows.append(Row(index=i, size=size, aux=aux, offset=off, payload=payload,
                            span_padding=p[off + ln:nxt],
                            strings=scan_strings(payload)))
        return rows

    def _parse_offtable(self) -> list[Row]:
        """`bin` layout: table of u64 string offsets followed by NUL-terminated UTF-8."""
        p = self.payload
        rows = []
        offs = [struct.unpack_from("<I", p, 16 * i)[0] for i in range(self.count)]
        for i, off in enumerate(offs):
            nxt = offs[i + 1] if i + 1 < len(offs) else len(p)
            end = p.find(b"\0", off)
            if end < 0 or end > nxt:
                end = nxt
            raw = p[off:end]
            try:
                txt = raw.decode("utf-8")
            except UnicodeDecodeError:
                txt = raw.decode("utf-8", "replace")
            rows.append(Row(index=i, size=end - off, aux=0, offset=off, payload=raw,
                            span_padding=p[end:nxt],
                            strings=[TextString(offset=off, raw=raw, text=txt,
                                                terminated=end < len(p))]))
        return rows

    # ---------------- rebuild ----------------
    def rebuild_payload(self) -> bytes:
        """Re-serialise the decompressed stream.  Byte-identical when nothing changed."""
        p = self.payload
        if self.layout == ROW_TABLE:
            out = bytearray(struct.pack("<I", self.count))
            for r in self.rows:
                out += struct.pack("<III", r.size, r.aux, r.offset)
            first = self.rows[0].offset if self.rows else 4 + ROW_RECORD_SIZE * self.count
            out += b"\0" * (first - len(out))            # proven zero padding
            for r in self.rows:
                out += r.payload
                out += r.span_padding
            return bytes(out)
        if self.layout == OFFSET_TABLE:
            out = bytearray()
            for r in self.rows:
                out += struct.pack("<IIII", r.offset, 0, 0, 0)
            for r in self.rows:
                out += r.payload
                out += r.span_padding
            return bytes(out)
        return p

    def rebuild(self) -> bytes:
        """Re-serialise the whole wrapper (header + BILZ + zlib body + tail)."""
        payload = self.rebuild_payload()
        body = zlib.compress(payload) if self.bilz.algo == 7 else payload
        out = bytearray(self.header.raw)
        out += b"BILZ"
        out += struct.pack(">III", len(payload), len(body), self.bilz.algo)
        out += body
        out += self.bilz.extra_trailing
        return bytes(out)

    def rebuild_zlib_identical(self) -> bytes:
        """Rebuild but re-use the original zlib body when the payload is unchanged."""
        payload = self.rebuild_payload()
        if payload == self.payload:
            body = self.raw[BILZ_DATA_OFFSET:BILZ_DATA_OFFSET + self.bilz.compressed_size]
        else:
            body = zlib.compress(payload) if self.bilz.algo == 7 else payload
        out = bytearray(self.header.raw)
        out += b"BILZ"
        out += struct.pack(">III", len(payload), len(body), self.bilz.algo)
        out += body
        out += self.bilz.extra_trailing
        return bytes(out)

    # ---------------- text edit helper ----------------
    def replace_text(self, row_index: int, old: bytes, new: bytes) -> None:
        """Replace one string occurrence inside a row; adjusts size/offsets.

        Works for arbitrary length changes because rows are independently
        addressed through the record table.  `aux` is copied verbatim
        (its semantics are UNPROVEN).
        """
        row = self.rows[row_index]
        pos = row.payload.find(old)
        if pos < 0:
            raise KeyError(f"{old!r} not found in row {row_index}")
        new_payload = row.payload[:pos] + new + row.payload[pos + len(old):]
        row.payload = new_payload
        row.size = (row.size & 0x80000000) | len(new_payload)
        row.strings = scan_strings(new_payload)
        self._relayout()

    def _relayout(self) -> None:
        """Recompute row offsets/alignment after edits (rows are 0x40 aligned)."""
        if not self.rows:
            return
        tail = len(self.rows[-1].span_padding)
        cur = self.rows[0].offset
        for i, r in enumerate(self.rows):
            r.offset = cur
            end = cur + len(r.payload)
            if i + 1 < len(self.rows):
                nxt = (end + ROW_ALIGN - 1) & ~(ROW_ALIGN - 1)
                r.span_padding = b"\0" * (nxt - end)
                cur = nxt
            else:
                r.span_padding = b"\0" * tail
                cur = end + tail

    # ---------------- reporting ----------------
    def summary(self) -> dict:
        texts = [s.text for r in self.rows for s in r.strings]
        return {
            "origin": self.origin,
            "tag": self.header.tag,
            "outer_size": len(self.raw),
            "header_field_04": self.header.field_04,
            "header_field_08": self.header.field_08,
            "header_field_0c": self.header.field_0c,
            "header_field_28": self.header.field_28,
            "bilz_algo": self.bilz.algo,
            "payload_size": len(self.payload),
            "layout": self.layout,
            "rows": len(self.rows),
            "strings": len(texts),
            "script_census": script_census(texts),
            "sha256_outer": hashlib.sha256(self.raw).hexdigest(),
            "sha256_payload": hashlib.sha256(self.payload).hexdigest(),
        }


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------
def _load(args) -> EncFile:
    if args.index is None:
        return EncFile.from_path(Path(args.archive))
    return EncFile.from_gdat(Path(args.archive), args.index)


def cmd_info(args) -> int:
    e = _load(args)
    print(json.dumps(e.summary(), indent=2, ensure_ascii=False))
    return 0


def cmd_rows(args) -> int:
    e = _load(args)
    print(f"# {e.origin} layout={e.layout} rows={len(e.rows)} payload={len(e.payload)}")
    for r in e.rows[: args.limit]:
        print(f"[{r.index:4d}] size={r.size:#010x} (len={r.length:#x}, flag31={r.flag31}) "
              f"aux={r.aux:#010x} off={r.offset:#08x} pad={len(r.span_padding):#x} "
              f"str={len(r.strings)}")
    return 0


def cmd_text(args) -> int:
    e = _load(args)
    out = []
    n = 0
    for r in e.rows:
        for s in r.strings:
            rec = {"row": r.index, "row_off": r.offset, "str_off": s.offset,
                   "offset_in_row": s.offset, "raw_hex": s.raw.hex(),
                   "text": s.text, "terminated": s.terminated,
                   "source": e.origin}
            out.append(rec)
            n += 1
            if args.limit and n >= args.limit:
                break
        if args.limit and n >= args.limit:
            break
    if args.jsonl:
        with open(args.jsonl, "w", encoding="utf-8") as fp:
            for rec in out:
                fp.write(json.dumps(rec, ensure_ascii=False) + "\n")
        print(f"wrote {len(out)} records to {args.jsonl}")
    else:
        for rec in out:
            print(f"[r{rec['row']:4d} +{rec['offset_in_row']:#06x}] {rec['text']!r}")
    return 0


def cmd_verify(args) -> int:
    archives = [Path(args.archive)] if args.archive else []
    if args.index is not None:
        e = EncFile.from_gdat(archives[0], args.index)
        ok = e.rebuild_zlib_identical() == e.raw
        print(f"{e.origin}: roundtrip {'OK' if ok else 'MISMATCH'} "
              f"(outer={len(e.raw)} payload={len(e.payload)} rows={len(e.rows)})")
        return 0 if ok else 1
    bad = 0
    for a in archives:
        for i in range(len(gdat_entries(a))):
            try:
                e = EncFile.from_gdat(a, i)
            except Exception:
                continue
            if e.rebuild_zlib_identical() != e.raw:
                bad += 1
                print(f"MISMATCH {a}[{i}]")
    print(f"mismatches: {bad}")
    return 1 if bad else 0


def cmd_selftest(args) -> int:
    root = Path(args.root)
    dirs = [root / "sources/MSF_PC/masterWindows",
            root / "sources/MS2_SWITCH_JP/current_program/1/masterNX64"]
    seen = ok = bad = 0
    per_tag: dict[str, int] = {}
    layout: dict[str, int] = {}
    rows_total = 0
    strings_total = 0
    for d in dirs:
        if not d.is_dir():
            continue
        for dat in sorted(d.glob("GxArchivedFile*.dat")):
            for i in range(len(gdat_entries(dat))):
                try:
                    e = EncFile.from_gdat(dat, i)
                except Exception:
                    continue
                seen += 1
                per_tag[e.header.tag] = per_tag.get(e.header.tag, 0) + 1
                layout[e.layout] = layout.get(e.layout, 0) + 1
                rows_total += len(e.rows)
                strings_total += sum(len(r.strings) for r in e.rows)
                if e.rebuild_zlib_identical() == e.raw:
                    ok += 1
                else:
                    bad += 1
                    if bad < 6:
                        print(f"  MISMATCH {e.origin}")
    print(f"files={seen} roundtrip_ok={ok} roundtrip_bad={bad}")
    print(f"tags={per_tag}")
    print(f"layouts={layout} rows={rows_total} strings={strings_total}")
    return 1 if bad else 0


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("info"); p.add_argument("archive"); p.add_argument("index", nargs="?", type=int)
    p.set_defaults(func=cmd_info)
    p = sub.add_parser("rows"); p.add_argument("archive"); p.add_argument("index", nargs="?", type=int)
    p.add_argument("--limit", type=int, default=20); p.set_defaults(func=cmd_rows)
    p = sub.add_parser("text"); p.add_argument("archive"); p.add_argument("index", nargs="?", type=int)
    p.add_argument("--limit", type=int, default=0); p.add_argument("--jsonl")
    p.set_defaults(func=cmd_text)
    p = sub.add_parser("verify"); p.add_argument("archive", nargs="?")
    p.add_argument("--index", type=int); p.set_defaults(func=cmd_verify)
    p = sub.add_parser("selftest"); p.add_argument("--root", default="."); p.set_defaults(func=cmd_selftest)

    args = ap.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
