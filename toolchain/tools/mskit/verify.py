"""Verification: re-extract records from rebuilt archives and compare.

Record keys in the historical raw exports are not fully stable identifiers:

* old ENC exports used ``f000`` for every string found in a row, so thousands
  of distinct records share one record_key;
* GBNL records inside STCM include the physical Data-item offset in table_key.
  A valid compact relayout changes that offset when an earlier item grows.

The verifier therefore compares records by a *logical locator* derived from the
stable container structure (archive/GDAT/CL3 + resource ordinal + row/field),
not by legacy record_key or physical byte offset.  The source raw_hex is also
not expected to equal rebuilt raw_hex after an edit; rebuilt bytes are checked
against the expected text instead.
"""
from __future__ import annotations

import json
import os
from collections import defaultdict
from pathlib import Path

from . import extract, records


def overlay_master(rebuilt_master: Path, source_master: Path, dest: Path) -> Path:
    """Build a tree where rebuilt archives override the source ones."""
    dest = Path(dest)
    dest.mkdir(parents=True, exist_ok=True)
    rebuilt_master, source_master = Path(rebuilt_master), Path(source_master)
    for src in sorted(source_master.glob("GxArchivedFile*.dat")):
        target = dest / src.name
        if not target.exists():
            os.link(src, target)
    for src in sorted(rebuilt_master.glob("GxArchivedFile*.dat")):
        target = dest / src.name
        if target.exists():
            target.unlink()
        os.link(src, target)
    for extra in sorted(rebuilt_master.glob("*")):
        if extra.is_file() and not (dest / extra.name).exists():
            os.link(extra, dest / extra.name)
    return dest


def _filtered(path: Path, archives=None) -> list[dict]:
    wanted = set(archives) if archives else None
    out = []
    for rec in records.read_jsonl(path):
        if wanted is not None and int(rec["archive_number"]) not in wanted:
            continue
        out.append(rec)
    return out


def _rank_maps(rows: list[dict]):
    """Ranks physical offsets so logical identity survives relocation.

    GBNL/STCM Data items keep their relative order under the compact relayout,
    while ENC strings keep their order within each row.  Ranking their physical
    offsets independently in source and rebuilt exports gives a stable ordinal.
    """
    blobs: dict[tuple, set[int]] = defaultdict(set)
    encs: dict[tuple, set[int]] = defaultdict(set)
    strings: dict[tuple, set[int]] = defaultdict(set)
    for r in rows:
        role = r.get("field_role")
        meta = r.get("repack_metadata") or {}
        base = (int(r["archive_number"]), int(r["gdat_index"]),
                -1 if r.get("cl3_inner_index") is None else int(r["cl3_inner_index"]))
        if role == "gnl_str":
            off = meta.get("blob_offset")
            if off is not None:
                blobs[base].add(int(off))
        elif role == "stcm_string":
            off = meta.get("blob_offset")
            if off is not None:
                strings[base].add(int(off))
        elif role == "enc_string":
            key = (int(r["archive_number"]), int(r["gdat_index"]), int(meta["row"]))
            encs[key].add(int(meta["str_offset"]))
    return (
        {k: {off: i for i, off in enumerate(sorted(v))} for k, v in blobs.items()},
        {k: {off: i for i, off in enumerate(sorted(v))} for k, v in encs.items()},
        {k: {off: i for i, off in enumerate(sorted(v))} for k, v in strings.items()},
    )


def _logical_index(rows: list[dict]) -> tuple[dict[tuple, dict], list[dict]]:
    blob_rank, enc_rank, string_rank = _rank_maps(rows)
    out: dict[tuple, dict] = {}
    collisions = []
    for r in rows:
        role = r.get("field_role")
        meta = r.get("repack_metadata") or {}
        archive = int(r["archive_number"])
        gidx = int(r["gdat_index"])
        cl3 = -1 if r.get("cl3_inner_index") is None else int(r["cl3_inner_index"])
        if role == "gnl_str":
            off = meta.get("blob_offset")
            ordinal = 0 if off is None else blob_rank[(archive, gidx, cl3)][int(off)]
            key = (archive, gidx, cl3, role, ordinal,
                   int(r["record_index"]), int(r["field_index"]))
        elif role == "stcm_string":
            off = meta.get("blob_offset")
            ordinal = 0 if off is None else string_rank[(archive, gidx, cl3)][int(off)]
            key = (archive, gidx, cl3, role, ordinal)
        elif role == "enc_string":
            row = int(meta["row"])
            ordinal = enc_rank[(archive, gidx, row)][int(meta["str_offset"])]
            key = (archive, gidx, cl3, role, row, ordinal)
        elif role == "bin_string":
            key = (archive, gidx, cl3, role, int(meta.get("string_index", r["record_index"])))
        else:
            # Future record kinds retain their full structural fields.  Do not
            # fall back to record_key because it may embed a physical offset.
            key = (archive, gidx, cl3, role, r.get("container_key"),
                   int(r["record_index"]), int(r["field_index"]))
        if key in out:
            collisions.append({"logical_key": repr(key),
                               "first": out[key].get("record_key"),
                               "second": r.get("record_key")})
        else:
            out[key] = r
    return out, collisions


def verify_dataset(dataset: str, rebuilt_master: Path, expected_jsonl: Path, *,
                   out_report: Path | None = None, archives=None,
                   source_master: Path | None = None) -> dict:
    """Re-extract rebuilt archives and compare by stable logical identity."""
    work_root = Path(rebuilt_master).parent
    if source_master is not None:
        work_root = overlay_master(Path(rebuilt_master), Path(source_master),
                                   work_root / "_overlay")
    tmp = work_root / "_verify_records.jsonl"
    stats = extract.extract_dataset(dataset, tmp, master=work_root, archives=archives)

    got_rows = _filtered(tmp, archives)
    want_rows = _filtered(expected_jsonl, archives)
    got, got_collisions = _logical_index(got_rows)
    want, want_collisions = _logical_index(want_rows)

    missing = sorted(set(want) - set(got), key=repr)
    extra = sorted(set(got) - set(want), key=repr)
    changed = []
    raw_mismatch = []
    for key in sorted(set(want) & set(got), key=repr):
        a, b = want[key], got[key]
        if a["text"] != b["text"]:
            changed.append({"logical_key": repr(key),
                            "expected_record_key": a.get("record_key"),
                            "rebuilt_record_key": b.get("record_key"),
                            "expected_text": a["text"], "got_text": b["text"]})
            continue
        try:
            expected_raw = records.encode_text(a["text"], b["encoding"])
            got_raw = bytes.fromhex(b["raw_hex"])
        except Exception as exc:  # noqa: BLE001
            raw_mismatch.append({"logical_key": repr(key), "error": repr(exc)})
            continue
        if expected_raw != got_raw:
            raw_mismatch.append({"logical_key": repr(key),
                                 "record_key": b.get("record_key"),
                                 "expected_hex": expected_raw.hex(),
                                 "got_hex": got_raw.hex()})

    report = {
        "dataset": dataset,
        "rebuilt_master": str(rebuilt_master),
        "archives_checked": sorted(set(archives)) if archives else None,
        "expected_records": len(want_rows),
        "rebuilt_records": len(got_rows),
        "expected_logical_records": len(want),
        "rebuilt_logical_records": len(got),
        "expected_logical_collisions": len(want_collisions),
        "rebuilt_logical_collisions": len(got_collisions),
        "missing_keys": len(missing),
        "extra_keys": len(extra),
        "changed_records": len(changed),
        "raw_mismatches": len(raw_mismatch),
        "missing_sample": [repr(x) for x in missing[:10]],
        "extra_sample": [repr(x) for x in extra[:10]],
        "collision_sample": (want_collisions + got_collisions)[:10],
        "raw_mismatch_sample": raw_mismatch[:10],
        "changed": changed[:200],
        "extract_stats": stats.as_dict(),
        "ok": (not missing and not extra and not changed and not raw_mismatch
               and not want_collisions and not got_collisions),
    }
    if out_report:
        Path(out_report).write_text(json.dumps(report, ensure_ascii=False, indent=2),
                                    encoding="utf-8")
    return report


def diff_against_source(dataset: str, rebuilt_master: Path, original_master: Path,
                        *, archives=None, out_report: Path | None = None) -> dict:
    """Describe rebuilt/source differences using stable logical locators."""
    a_path = Path(rebuilt_master).parent / "_verify_rebuilt.jsonl"
    b_path = Path(rebuilt_master).parent / "_verify_source.jsonl"
    extract.extract_dataset(dataset, a_path, master=Path(rebuilt_master), archives=archives)
    extract.extract_dataset(dataset, b_path, master=Path(original_master), archives=archives)
    a_rows, b_rows = _filtered(a_path, archives), _filtered(b_path, archives)
    a, ac = _logical_index(a_rows)
    b, bc = _logical_index(b_rows)
    diffs = []
    for key in sorted(set(a) & set(b), key=repr):
        if a[key]["text"] != b[key]["text"] or a[key]["raw_hex"] != b[key]["raw_hex"]:
            diffs.append({"logical_key": repr(key), "source_text": b[key]["text"],
                          "rebuilt_text": a[key]["text"]})
    report = {
        "dataset": dataset, "source_records": len(b_rows), "rebuilt_records": len(a_rows),
        "source_logical_records": len(b), "rebuilt_logical_records": len(a),
        "source_collisions": len(bc), "rebuilt_collisions": len(ac),
        "missing": [repr(x) for x in sorted(set(b) - set(a), key=repr)[:10]],
        "extra": [repr(x) for x in sorted(set(a) - set(b), key=repr)[:10]],
        "changed_records": len(diffs), "changed": diffs[:200],
        "archive_bytes": {},
    }
    for p in sorted(Path(rebuilt_master).glob("GxArchivedFile*.dat")):
        src = Path(original_master) / p.name
        report["archive_bytes"][p.name] = {
            "source_size": src.stat().st_size if src.exists() else None,
            "rebuilt_size": p.stat().st_size,
            "byte_identical": src.exists() and src.read_bytes() == p.read_bytes(),
        }
    if out_report:
        Path(out_report).write_text(json.dumps(report, ensure_ascii=False, indent=2),
                                    encoding="utf-8")
    return report
