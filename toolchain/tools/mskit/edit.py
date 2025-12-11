"""GBNL text editing with minimal byte churn and correct dedup semantics.

The Mary Skelter GBNL string tables are *deduplicated*: several message fields
can point at the same table entry.  A naive in-place overwrite would therefore
silently change every other record sharing that entry.

`apply_edits()` therefore plans the string table first:

* an unshared string can be rewritten in place when the replacement fits;
* shared strings are never overwritten in place.  Edited fields are redirected
  to an existing identical entry or to freshly appended entries.  This is
  required even when every current reference is being edited, because those
  references may intentionally diverge to different translations;
* strings that grow are appended in the alignment padding when it is large
  enough, and otherwise force the blob to grow (the trailing 16-byte alignment
  pad and the 0x40 header/footer are re-emitted; no other byte moves).
"""
from __future__ import annotations

import struct

from .gbnl import Gbnl, GbnlError, HEADER_SIZE, _align

STR_KINDS = ("str", "fixstr")


def plan_edits(g: Gbnl, edits: dict[tuple[int, int], bytes]) -> dict:
    """Compute the byte-level plan for `edits` = {(msg, field): new_bytes}."""
    refcount: dict[int, int] = {}
    for m in g.messages:
        for f in m:
            if f.kind == "str" and f.str_offset is not None:
                refcount[f.str_offset] = refcount.get(f.str_offset, 0) + 1

    by_offset: dict[int, dict[bytes, list[tuple[int, int]]]] = {}
    fixstr_edits: list[tuple[int, int, bytes]] = []
    for (mi, fi), new in edits.items():
        fld = g.messages[mi][fi]
        if fld.kind == "fixstr":
            fixstr_edits.append((mi, fi, new))
            continue
        if fld.kind != "str":
            raise GbnlError(f"field {mi}/{fi} is not text ({fld.kind})")
        if fld.is_null:
            raise GbnlError(f"field {mi}/{fi} is a null string")
        by_offset.setdefault(fld.str_offset, {}).setdefault(new, []).append((mi, fi))

    table = g.string_table()
    existing: dict[bytes, int] = {}
    for off, s in sorted(table.items()):
        existing.setdefault(s, off)

    used_end = max(g.strings_end(), g.offset_msgs)
    limit = g.tail_limit()
    appended = bytearray()
    next_abs = used_end

    def append(s: bytes) -> int:
        """Append a string, returning its *relative* table offset."""
        nonlocal next_abs
        if s in existing:
            return existing[s]
        rel = next_abs - g.offset_msgs
        appended.extend(s + b"\0")
        next_abs += len(s) + 1
        existing[s] = rel
        return rel

    repoint: list[tuple[int, int, int]] = []   # (msg, field, new_table_offset)
    overwrite: list[tuple[int, bytes]] = []    # (absolute table pos, bytes)
    for old_off, wants in by_offset.items():
        old = table[old_off]
        refs = refcount.get(old_off, 0)
        # In-place overwrite is safe only for a truly unshared table entry.
        #
        # A previous implementation treated a shared entry as overwrite-safe
        # when *all* of its references happened to be edited.  That breaks as
        # soon as those references intentionally diverge: two different fitting
        # replacements are scheduled for the same slot, and the first shorter
        # overwrite inserts an earlier NUL.  The second overwrite then observes
        # that shortened slot and raises "overwrite longer than the original
        # slot" (or, for equal/shorter replacements, silently makes both fields
        # share the last text).  Shared entries must therefore always be
        # repointed, exactly as the format documentation specifies.
        unshared = refs == 1
        for new, fields in wants.items():
            if new == old:
                continue
            if unshared and len(new) <= len(old):
                overwrite.append((g.offset_msgs + old_off, new))
                continue
            target = append(new)
            for mi, fi in fields:
                repoint.append((mi, fi, target))

    return {"repoint": repoint, "overwrite": overwrite, "append": bytes(appended),
            "append_start": used_end, "next_abs": next_abs, "limit": limit,
            "fixstr": fixstr_edits}


def apply_edits(g: Gbnl, edits: dict[tuple[int, int], bytes]) -> tuple[bytes, bool]:
    """Apply text edits to a GBNL blob; returns (new_blob, grew)."""
    if not edits:
        return g.data, False
    plan = plan_edits(g, edits)
    e = g.endian
    buf = bytearray(g.data)

    # 1. fixed-length strings are patched in their struct slot
    for mi, fi, new in plan["fixstr"]:
        fld = g.messages[mi][fi]
        if len(new) + 1 > fld.size:
            raise GbnlError(f"fixed-length field {mi}/{fi} only holds {fld.size} bytes")
        base = g.descr_offset + mi * g.msg_descr_size + fld.offset
        buf[base:base + fld.size] = new + b"\0" * (fld.size - len(new))

    # 2. in-place overwrites of table entries
    for pos, new in plan["overwrite"]:
        old_end = buf.find(b"\0", pos)
        if old_end < 0:
            raise GbnlError("unterminated string while overwriting")
        old_len = old_end - pos
        if len(new) > old_len:
            raise GbnlError("internal: overwrite longer than the original slot")
        buf[pos:pos + len(new)] = new
        buf[pos + len(new):old_end + 1] = b"\0" * (old_len + 1 - len(new))

    # 3. repointing fields to (possibly new) table entries
    for mi, fi, new_off in plan["repoint"]:
        fld = g.messages[mi][fi]
        base = g.descr_offset + mi * g.msg_descr_size + fld.offset
        struct.pack_into(e + "I", buf, base, new_off)

    # 4. append the new string table entries, growing the blob if needed
    if plan["append"]:
        append = plan["append"]
        start = plan["append_start"]
        raw = bytes(buf[:start]) + append
        if start + len(append) <= plan["limit"]:
            out = raw + bytes(buf[start + len(append):])
            return out, False
        # grow: keep everything up to the strings, re-pad, re-emit header/footer
        if g.is_gstl:
            tail = b"\0" * (_align(len(raw)) - len(raw))
            return raw + tail, True
        tail = b"\0" * (_align(len(raw)) - len(raw))
        return raw + tail + bytes(g.data[g.header_offset:]), True

    return bytes(buf), False
