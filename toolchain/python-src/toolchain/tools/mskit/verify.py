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
from collections import defaultdict
from pathlib import Path

from . import enc, extract, gdat, records


# Verification reads complete rebuilt GDAT archives directly from the output
# directory and original archives directly from the pristine source directory.
# No synthetic master tree, links, or copies are needed.


def _check_rebuilt_archive_set(rebuilt_master: Path, source_master: Path, archives) -> list[int]:
    """Require an independent full GDAT rebuild for every selected archive.

    Missing rebuilt archives are errors, never silently substituted with
    pristine input: doing so could falsely PASS an incomplete patch.
    """
    rebuilt_master, source_master = Path(rebuilt_master), Path(source_master)
    if archives is None:
        selected = sorted(int(path.stem[-3:]) for path in rebuilt_master.glob('GxArchivedFile[0-9][0-9][0-9].dat'))
    else:
        selected = sorted(set(int(number) for number in archives))
    if not selected:
        raise ValueError(f'no rebuilt GDAT archives found in {rebuilt_master}')
    for number in selected:
        filename = f'GxArchivedFile{number:03d}.dat'
        if not (source_master / filename).is_file():
            raise FileNotFoundError(f'missing pristine input for verification: {source_master / filename}')
        if not (rebuilt_master / filename).is_file():
            raise FileNotFoundError(f'missing independently rebuilt archive (no source substitution): {rebuilt_master / filename}')
    return selected


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


def _logical_index(rows: list[dict], *, rank_rows: list[dict] | None = None) -> tuple[dict[tuple, dict], list[dict]]:
    """Index records by stable logical identity.

    ``rank_rows`` may be a complete physical extraction while ``rows`` is only
    a selected subset.  This matters when archive
    052 is rebuilt as a whole, but the historical/raw target corpus contains
    only the player-facing rows selected for the public translation corpus.
    Ranking offsets inside the subset would renumber those strings and falsely
    pair them with unrelated source/technical strings from the full rebuild.

    When a complete source extraction is available, derive all physical-order
    ranks from it and apply those ranks to the selected expected records.
    """
    rank_basis = rows if rank_rows is None else rank_rows
    blob_rank, enc_rank, string_rank = _rank_maps(rank_basis)
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


def _record_is_edited(rec: dict) -> bool:
    """True when the expected record contains an intentional text edit."""
    try:
        return records.encode_text(rec["text"], rec["encoding"]) != bytes.fromhex(rec["raw_hex"])
    except Exception:
        # Let the normal verification path surface malformed records.
        return True



def _archive_path(master: Path, archive: int) -> Path:
    return Path(master) / f"GxArchivedFile{int(archive):03d}.dat"


def _edited_entry_keys(rows: list[dict]) -> set[tuple[int, int]]:
    """Return (archive, GDAT index) pairs containing intentional edits."""
    return {(int(r["archive_number"]), int(r["gdat_index"]))
            for r in rows if _record_is_edited(r)}


def _verify_unedited_entries(source_master: Path, rebuilt_master: Path, archives,
                               edited_rows: list[dict]) -> list[dict]:
    """Require every GDAT entry without an authorized edit to remain byte-identical.

    The rebuilt archives are complete GDAT files.  Repackers copy untouched entries
    verbatim, so comparing raw entry bytes is both cheap and stronger than relying on
    a text scanner to notice accidental changes in opaque/source-language content.
    """
    edited_entries = _edited_entry_keys(edited_rows)
    problems = []
    for archive in sorted(set(int(a) for a in (archives or []))):
        sp = _archive_path(source_master, archive)
        rp = _archive_path(rebuilt_master, archive)
        if not sp.exists() or not rp.exists():
            problems.append({"archive": archive, "error": "source or rebuilt archive missing"})
            continue
        with gdat.Gdat(sp) as sg, gdat.Gdat(rp) as rg:
            if sg.count != rg.count:
                problems.append({"archive": archive, "error": "GDAT entry count changed",
                                 "source_count": sg.count, "rebuilt_count": rg.count})
                continue
            for i in range(sg.count):
                if (archive, i) in edited_entries:
                    continue
                sb = sg.read(i)
                rb = rg.read(i)
                if sb != rb:
                    problems.append({"archive": archive, "gdat_index": i,
                                     "error": "unedited GDAT entry changed",
                                     "source_size": len(sb), "rebuilt_size": len(rb)})
                    if len(problems) >= 50:
                        return problems
    return problems


def _verify_enc_rows(source_master: Path, rebuilt_master: Path, archives,
                     edited_rows: list[dict]) -> dict:
    """Verify ENC edits against decoded rows, independent of string detection.

    ``EncFile.strings()`` intentionally uses a conservative, content-sensitive
    scanner.  A short JP literal can therefore be absent from a pristine extraction
    and become visible after it is replaced with longer English.  Such a scanner
    cannot be the structural denominator for round-trip verification.

    ENC rebuilds edit fixed, proven NUL-padded slots inside decoded rows and never
    relocate the decoded row contents.  The stable identity is therefore
    (archive, GDAT entry, row, decoded offset).  Reconstruct the exact expected
    decoded row from pristine bytes plus authorized edit specs and compare the whole
    row byte-for-byte with the rebuild.
    """
    enc_edits: dict[tuple[int, int], dict[tuple[int, int], dict]] = defaultdict(dict)
    for r in edited_rows:
        if r.get("field_role") != "enc_string":
            continue
        meta = r.get("repack_metadata") or {}
        archive = int(r["archive_number"])
        gidx = int(r["gdat_index"])
        key = (int(meta["row"]), int(meta["str_offset"]))
        old = bytes.fromhex(r["raw_hex"])
        explicit_capacity = meta.get("slot_capacity")
        spec = {
            "old": old,
            "new": records.encode_text(r["text"], r["encoding"]),
            "capacity": len(old) if explicit_capacity is None else int(explicit_capacity),
            "explicit_capacity": explicit_capacity is not None,
            "record_key": r.get("record_key"),
        }
        prev = enc_edits[(archive, gidx)].get(key)
        if prev is not None and prev != spec:
            return {"ok": False, "entries_checked": 0, "rows_checked": 0,
                    "edits_checked": 0, "problem_count": 1, "problems": [{
                        "archive": archive, "gdat_index": gidx, "row": key[0],
                        "str_offset": key[1], "error": "conflicting ENC edit specs"}]}
        enc_edits[(archive, gidx)][key] = spec

    problems = []
    problem_count = 0
    entries_checked = rows_checked = edits_checked = 0
    def report_problem(problem):
        nonlocal problem_count
        problem_count += 1
        if len(problems) < 50:
            problems.append(problem)
    for (archive, gidx), edits in sorted(enc_edits.items()):
        if archives is not None and archive not in set(int(a) for a in archives):
            continue
        sp = _archive_path(source_master, archive)
        rp = _archive_path(rebuilt_master, archive)
        try:
            with gdat.Gdat(sp) as sg, gdat.Gdat(rp) as rg:
                if gidx >= sg.count or gidx >= rg.count:
                    raise ValueError("GDAT index outside source/rebuilt archive")
                sf = enc.EncFile(sg.read(gidx))
                rf = enc.EncFile(rg.read(gidx))
        except Exception as exc:  # noqa: BLE001
            report_problem({"archive": archive, "gdat_index": gidx,
                             "error": f"cannot parse ENC entry: {exc!r}"})
            continue
        entries_checked += 1
        if sf.count != rf.count:
            report_problem({"archive": archive, "gdat_index": gidx,
                             "error": "ENC row count changed",
                             "source_count": sf.count, "rebuilt_count": rf.count})
            continue
        by_row: dict[int, list[tuple[int, dict]]] = defaultdict(list)
        for (row, off), spec in edits.items():
            by_row[row].append((off, spec))
        for row_index, (sr, rr) in enumerate(zip(sf.rows, rf.rows)):
            rows_checked += 1
            if sr.flag31 != rr.flag31:
                report_problem({"archive": archive, "gdat_index": gidx,
                                 "row": row_index, "error": "ENC row compression flag changed"})
                continue
            try:
                source_decoded = sr.decoded_payload()
                rebuilt_decoded = rr.decoded_payload()
            except Exception as exc:  # noqa: BLE001
                report_problem({"archive": archive, "gdat_index": gidx,
                                 "row": row_index, "error": f"ENC row decode failed: {exc!r}"})
                continue
            expected = bytearray(source_decoded)
            row_failed = False
            for off, spec in sorted(by_row.get(row_index, []), key=lambda x: -x[0]):
                old, new, capacity = spec["old"], spec["new"], spec["capacity"]
                label = spec.get("record_key")
                end0 = off + len(old)
                slot_end = off + capacity
                if off < 0 or end0 >= len(expected) or slot_end >= len(expected):
                    report_problem({"archive": archive, "gdat_index": gidx,
                                     "row": row_index, "str_offset": off,
                                     "record_key": label, "error": "ENC edit slot outside decoded row"})
                    row_failed = True
                    continue
                if bytes(expected[off:end0]) != old:
                    report_problem({"archive": archive, "gdat_index": gidx,
                                     "row": row_index, "str_offset": off,
                                     "record_key": label, "error": "ENC pristine old-byte mismatch",
                                     "expected_old_hex": old.hex(),
                                     "source_hex": bytes(expected[off:end0]).hex()})
                    row_failed = True
                    continue
                if expected[end0] != 0:
                    report_problem({"archive": archive, "gdat_index": gidx,
                                     "row": row_index, "str_offset": off,
                                     "record_key": label, "error": "ENC pristine literal not NUL-terminated"})
                    row_failed = True
                    continue
                # Mirror the ENC writer's guarded operations using pristine row
                # bytes, not the scanner's content-sensitive record inventory.
                # Absent explicit slot_capacity, len(old) is only the old string
                # length, NOT the proven extent of a NUL-padded Western slot.
                if capacity < len(old) or slot_end >= len(expected):
                    report_problem({"archive": archive, "gdat_index": gidx,
                                     "row": row_index, "str_offset": off,
                                     "record_key": label, "error": "ENC slot capacity violation"})
                    row_failed = True
                    continue
                if any(expected[end0:slot_end + 1]):
                    report_problem({"archive": archive, "gdat_index": gidx,
                                     "row": row_index, "str_offset": off,
                                     "record_key": label,
                                     "error": "ENC authorized slot crosses nonzero pristine bytes"})
                    row_failed = True
                    continue
                if len(new) > capacity:
                    new_end = off + len(new)
                    in_place = (
                        not spec["explicit_capacity"]
                        and new_end + 1 < len(expected)
                        and not any(expected[end0:new_end + 2])
                    )
                    if in_place:
                        expected[off:new_end] = new
                        expected[new_end] = 0
                    elif not any(expected[slot_end + 1:]):
                        # Legacy terminal-tail growth; it is not an interior
                        # relocation and is separately checked as a full row.
                        expected[off:slot_end + 1] = new + b"\0"
                    else:
                        report_problem({"archive": archive, "gdat_index": gidx,
                                         "row": row_index, "str_offset": off,
                                         "record_key": label,
                                         "error": "ENC length exceeds validated NUL space"})
                        row_failed = True
                        continue
                else:
                    expected[off:off + len(new)] = new
                    expected[off + len(new):slot_end + 1] = (
                        b"\0" * (slot_end + 1 - off - len(new)))
                edits_checked += 1
            if not row_failed and bytes(expected) != rebuilt_decoded:
                # Report the first differing decoded byte for a compact diagnostic.
                first = next((i for i, (a, b) in enumerate(zip(expected, rebuilt_decoded))
                              if a != b), min(len(expected), len(rebuilt_decoded)))
                report_problem({"archive": archive, "gdat_index": gidx,
                                 "row": row_index, "error": "ENC decoded row mismatch",
                                 "first_diff": first, "expected_size": len(expected),
                                 "rebuilt_size": len(rebuilt_decoded)})
            # Compressed rows store the decoded size in aux; a legally grown
            # terminal row may increase it. Uncompressed aux is unchanged.
            expected_aux = len(expected) if sr.flag31 else sr.aux
            if expected_aux != rr.aux:
                report_problem({"archive": archive, "gdat_index": gidx,
                                 "row": row_index, "error": "ENC row aux changed",
                                 "expected_aux": expected_aux, "source_aux": sr.aux,
                                 "rebuilt_aux": rr.aux})

    return {"ok": problem_count == 0, "entries_checked": entries_checked,
            "rows_checked": rows_checked, "edits_checked": edits_checked,
            "problem_count": problem_count, "problems": problems}

def verify_dataset(dataset: str, rebuilt_master: Path, expected_jsonl: Path, *,
                   out_report: Path | None = None, archives=None,
                   source_master: Path | None = None,
                   scratch_dir: Path | None = None) -> dict:
    """Re-extract rebuilt archives and verify the intended edits.

    Historical raw-target JSONL is *not* necessarily a complete extraction of
    every rebuilt archive.  In particular, archive 052 gained exact decoded
    public rows late in the project while source/other-language/technical rows
    intentionally stayed outside the public raw-target denominator.

    With ``source_master`` (the production build path), verification therefore
    uses a complete pristine-source extraction as the structural denominator:

    * source and rebuilt must expose the same logical record set;
    * every intentionally edited expected record must decode to its requested
      text in the rebuild;
    * every other source record must remain byte/text-identical.

    This preserves the old relocation-tolerant logical ranking while avoiding
    false extra/missing records when the expected JSONL is only a public subset.
    """
    rebuilt_master = Path(rebuilt_master)
    if source_master is not None:
        archives = _check_rebuilt_archive_set(rebuilt_master, Path(source_master), archives)
    # The rebuild already produces complete GDAT archives for every changed
    # archive. Extract directly from those files, without duplicating or
    # linking source archives into a synthetic verification master.
    tmp = (Path(scratch_dir) if scratch_dir is not None else rebuilt_master.parent) / "_verify_records.jsonl"
    stats = extract.extract_dataset(dataset, tmp, master=rebuilt_master, archives=archives)

    got_rows = _filtered(tmp, archives)
    expected_rows_all = _filtered(expected_jsonl, archives)

    # Production builds always pass source_master.  Keep a conservative fallback
    # for standalone/manual verifier use.
    if source_master is None:
        got, got_collisions = _logical_index(got_rows)
        want, want_collisions = _logical_index(expected_rows_all)
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
            "expected_records": len(expected_rows_all),
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

    # Complete pristine extraction is the authority for archive structure and
    # for physical-order ranks used by selected historical/public records.
    source_tmp = (Path(scratch_dir) if scratch_dir is not None else rebuilt_master.parent) / "_verify_source_records.jsonl"
    source_stats = extract.extract_dataset(dataset, source_tmp, master=Path(source_master),
                                           archives=archives)
    source_rows = _filtered(source_tmp, archives)

    # Only intentional edits from build_records are expectations.  Old raw-target
    # corpora may contain obsolete scanner fragments that are useful provenance
    # but are not a complete/current extractor denominator.
    edited_rows = [r for r in expected_rows_all if _record_is_edited(r)]

    # ENC is special: its historical extractor uses a conservative, content-
    # sensitive scanner.  A short JP string may be invisible in pristine source
    # and become visible after a longer English replacement.  Therefore scanner
    # output cannot define ENC structural identity.  Verify ENC directly from the
    # decoded row bytes at the stable (row, decoded offset) locator.
    enc_audit = _verify_enc_rows(Path(source_master), Path(rebuilt_master), archives, edited_rows)

    # For all structured non-ENC formats, the extractor denominator is stable.
    source_nonenc_rows = [r for r in source_rows if r.get("field_role") != "enc_string"]
    got_nonenc_rows = [r for r in got_rows if r.get("field_role") != "enc_string"]
    edited_nonenc_rows = [r for r in edited_rows if r.get("field_role") != "enc_string"]
    source, source_collisions = _logical_index(source_nonenc_rows)
    got, got_collisions = _logical_index(got_nonenc_rows)
    try:
        want, want_collisions = _logical_index(edited_nonenc_rows, rank_rows=source_nonenc_rows)
    except KeyError as exc:
        report = {
            "dataset": dataset, "rebuilt_master": str(rebuilt_master),
            "archives_checked": sorted(set(archives)) if archives else None,
            "expected_records": len(expected_rows_all),
            "expected_edited_records": len(edited_rows),
            "source_records": len(source_rows), "rebuilt_records": len(got_rows),
            "missing_keys": 1, "extra_keys": 0, "changed_records": 0,
            "raw_mismatches": 0, "unexpected_source_changes": 0,
            "missing_sample": [f"non-ENC edited record rank missing from pristine extraction: {exc!r}"],
            "extra_sample": [], "collision_sample": [], "raw_mismatch_sample": [],
            "unexpected_source_change_sample": [], "changed": [],
            "enc_audit": enc_audit,
            "extract_stats": stats.as_dict(), "source_extract_stats": source_stats.as_dict(),
            "ok": False,
        }
        if out_report:
            Path(out_report).write_text(json.dumps(report, ensure_ascii=False, indent=2),
                                        encoding="utf-8")
        return report

    structural_missing = sorted(set(source) - set(got), key=repr)
    structural_extra = sorted(set(got) - set(source), key=repr)
    expected_missing = sorted(set(want) - set(got), key=repr)
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

    unexpected_source_changes = []
    for key in sorted((set(source) & set(got)) - set(want), key=repr):
        a, b = source[key], got[key]
        if a.get("text") != b.get("text") or a.get("raw_hex") != b.get("raw_hex"):
            unexpected_source_changes.append({
                "logical_key": repr(key),
                "source_record_key": a.get("record_key"),
                "rebuilt_record_key": b.get("record_key"),
                "source_text": a.get("text"),
                "rebuilt_text": b.get("text"),
            })

    # Rebuilt archives are complete GDAT containers.  Any entry that contains no
    # authorized edit must be copied byte-for-byte; this protects opaque and
    # source-language content that no text extractor sees at all.
    unexpected_entry_changes = _verify_unedited_entries(
        Path(source_master), Path(rebuilt_master), archives, edited_rows)

    report = {
        "dataset": dataset,
        "rebuilt_master": str(rebuilt_master),
        "archives_checked": sorted(set(archives)) if archives else None,
        "expected_records": len(expected_rows_all),
        "expected_edited_records": len(edited_rows),
        "source_records": len(source_rows),
        "rebuilt_records": len(got_rows),
        "source_nonenc_logical_records": len(source),
        "rebuilt_nonenc_logical_records": len(got),
        "expected_edited_nonenc_logical_records": len(want),
        "source_logical_collisions": len(source_collisions),
        "rebuilt_logical_collisions": len(got_collisions),
        "expected_logical_collisions": len(want_collisions),
        "missing_keys": len(structural_missing),
        "extra_keys": len(structural_extra),
        "expected_missing_keys": len(expected_missing),
        "changed_records": len(changed),
        "raw_mismatches": len(raw_mismatch),
        "unexpected_source_changes": len(unexpected_source_changes),
        "unexpected_unedited_entry_changes": len(unexpected_entry_changes),
        "missing_sample": [repr(x) for x in structural_missing[:10]],
        "extra_sample": [repr(x) for x in structural_extra[:10]],
        "expected_missing_sample": [repr(x) for x in expected_missing[:10]],
        "collision_sample": (source_collisions + want_collisions + got_collisions)[:10],
        "raw_mismatch_sample": raw_mismatch[:10],
        "unexpected_source_change_sample": unexpected_source_changes[:10],
        "unexpected_unedited_entry_change_sample": unexpected_entry_changes[:10],
        "enc_audit": enc_audit,
        "changed": changed[:200],
        "extract_stats": stats.as_dict(),
        "source_extract_stats": source_stats.as_dict(),
        "ok": (not structural_missing and not structural_extra and not expected_missing
               and not changed and not raw_mismatch and not unexpected_source_changes
               and not unexpected_entry_changes and enc_audit.get("ok")
               and not source_collisions and not want_collisions and not got_collisions),
    }
    if out_report:
        Path(out_report).write_text(json.dumps(report, ensure_ascii=False, indent=2),
                                    encoding="utf-8")
    return report


def diff_against_source(dataset: str, rebuilt_master: Path, original_master: Path,
                        *, archives=None, out_report: Path | None = None) -> dict:
    """Describe rebuilt/source differences using stable logical locators.

    Non-ENC structured text can be compared through extractor logical keys.  ENC
    scanner cardinality is content-sensitive, so report ENC differences at decoded
    row level instead of presenting newly-visible English strings as structural
    extra/missing records.
    """
    a_path = Path(rebuilt_master).parent / "_verify_rebuilt.jsonl"
    b_path = Path(rebuilt_master).parent / "_verify_source.jsonl"
    extract.extract_dataset(dataset, a_path, master=Path(rebuilt_master), archives=archives)
    extract.extract_dataset(dataset, b_path, master=Path(original_master), archives=archives)
    a_rows, b_rows = _filtered(a_path, archives), _filtered(b_path, archives)
    a_nonenc = [r for r in a_rows if r.get("field_role") != "enc_string"]
    b_nonenc = [r for r in b_rows if r.get("field_role") != "enc_string"]
    a, ac = _logical_index(a_nonenc)
    b, bc = _logical_index(b_nonenc)
    diffs = []
    for key in sorted(set(a) & set(b), key=repr):
        if a[key]["text"] != b[key]["text"] or a[key]["raw_hex"] != b[key]["raw_hex"]:
            diffs.append({"logical_key": repr(key), "source_text": b[key]["text"],
                          "rebuilt_text": a[key]["text"]})

    enc_changed_rows = []
    enc_errors = []
    wanted_archives = sorted(set(int(x) for x in (archives or [])))
    if not wanted_archives:
        wanted_archives = sorted(
            int(p.stem.replace("GxArchivedFile", ""))
            for p in Path(rebuilt_master).glob("GxArchivedFile*.dat"))
    for archive in wanted_archives:
        rp = _archive_path(rebuilt_master, archive)
        sp = _archive_path(original_master, archive)
        if not rp.exists() or not sp.exists():
            continue
        with gdat.Gdat(sp) as sg, gdat.Gdat(rp) as rg:
            if sg.count != rg.count:
                enc_errors.append({"archive": archive, "error": "GDAT entry count changed"})
                continue
            for i in range(sg.count):
                stag = extract.entry_tag(sg, i)
                rtag = extract.entry_tag(rg, i)
                if stag != "enc" and rtag != "enc":
                    continue
                if stag != rtag:
                    enc_errors.append({"archive": archive, "gdat_index": i,
                                       "error": "ENC tag changed",
                                       "source_tag": stag, "rebuilt_tag": rtag})
                    continue
                try:
                    sf, rf = enc.EncFile(sg.read(i)), enc.EncFile(rg.read(i))
                except Exception as exc:  # noqa: BLE001
                    enc_errors.append({"archive": archive, "gdat_index": i,
                                       "error": repr(exc)})
                    continue
                if sf.count != rf.count:
                    enc_errors.append({"archive": archive, "gdat_index": i,
                                       "error": "ENC row count changed",
                                       "source_count": sf.count, "rebuilt_count": rf.count})
                    continue
                for ri, (sr, rr) in enumerate(zip(sf.rows, rf.rows)):
                    try:
                        sb, rb = sr.decoded_payload(), rr.decoded_payload()
                    except Exception as exc:  # noqa: BLE001
                        enc_errors.append({"archive": archive, "gdat_index": i,
                                           "row": ri, "error": repr(exc)})
                        continue
                    if sb != rb:
                        enc_changed_rows.append({"archive": archive,
                                                 "gdat_index": i, "row": ri,
                                                 "source_size": len(sb),
                                                 "rebuilt_size": len(rb)})

    report = {
        "dataset": dataset, "source_records": len(b_rows), "rebuilt_records": len(a_rows),
        "source_nonenc_logical_records": len(b), "rebuilt_nonenc_logical_records": len(a),
        "source_collisions": len(bc), "rebuilt_collisions": len(ac),
        "missing_nonenc": [repr(x) for x in sorted(set(b) - set(a), key=repr)[:10]],
        "extra_nonenc": [repr(x) for x in sorted(set(a) - set(b), key=repr)[:10]],
        "changed_nonenc_records": len(diffs), "changed": diffs[:200],
        "enc_scanner_source_records": sum(1 for r in b_rows if r.get("field_role") == "enc_string"),
        "enc_scanner_rebuilt_records": sum(1 for r in a_rows if r.get("field_role") == "enc_string"),
        "enc_changed_rows": len(enc_changed_rows),
        "enc_changed_row_sample": enc_changed_rows[:20],
        "enc_errors": enc_errors[:20],
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
