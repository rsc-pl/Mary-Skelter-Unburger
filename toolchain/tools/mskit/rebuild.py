"""Rebuild PC archives from an edited raw export.

Layer order (innermost first):

    text record  ->  GBNL blob  ->  STCM2  ->  CL3  ->  BILZ  ->  GDAT

Design rules:

* the source tree is opened read-only and is never written to;
* any GDAT entry that has no edited record is copied out **verbatim** (its
  original compressed bytes), so an unchanged archive rebuilds byte-identically;
* a GBNL blob is patched with minimal byte churn (see mskit.edit);
* a Data structure that has to grow is re-emitted after the end of the STCM and
  every discovered reference to it is repointed (mskit.stcm.rebuild).
"""
from __future__ import annotations

from collections import defaultdict
from pathlib import Path

from . import bilz, cl3, corpus, edit, enc, gbnl, gdat, records, stcm


class RebuildError(Exception):
    pass


def master_dirname(dataset: str) -> str:
    return "masterNX64" if corpus.DATASETS[dataset]["platform"] == "Switch" else "masterWindows"


def _group_edits(edited_records) -> dict:
    """Group edited records by archive -> gdat entry -> payload kind.

    Layout:
        {archive: {gdat_index: {"gnl": {cl3_index: {(offset): {field: bytes}}},
                                "enc_row": {(row, str_offset): bytes},
                                "bin_string": {string_index: bytes}}}}
    """
    out: dict[int, dict[int, dict]] = defaultdict(lambda: defaultdict(
        lambda: {"gnl": defaultdict(lambda: defaultdict(dict)),
                 "enc_row": {}, "bin_string": {}}))
    for rec in edited_records:
        raw_old = bytes.fromhex(rec["raw_hex"])
        new = records.encode_text(rec["text"], rec["encoding"])
        if new == raw_old:
            continue
        meta = rec.get("repack_metadata") or {}
        chain = rec.get("format_chain") or []
        blob_type = meta.get("blob_type")
        archive = int(rec["archive_number"])
        gidx = int(rec["gdat_index"])
        entry = out[archive][gidx]
        if blob_type == "gnl":
            if not any(k in chain for k in ("GBNL", "GBIN", "GSTR")):
                raise RebuildError(f"{rec['record_key']}: unsupported chain {chain}")
            cl3_index = rec.get("cl3_inner_index")
            cl3_key = -1 if cl3_index is None else int(cl3_index)
            # `blob_offset` is None when the record's blob *is* the whole container
            # (a GBIN/GSTR stored directly as the GDAT entry, or directly as a CL3
            # member) - keyed as -1 so the rebuild knows there is nothing to index.
            blob_offset = meta.get("blob_offset")
            blob_key = -1 if blob_offset is None else int(blob_offset)
            entry["gnl"][cl3_key][blob_key][
                (int(rec["record_index"]), int(rec["field_index"]))] = new
        elif blob_type == "enc_row":
            entry["enc_row"][(int(meta["row"]), int(meta["str_offset"]))] = new
        elif blob_type == "bin_string":
            entry["bin_string"][int(meta["string_index"])] = new
        else:
            raise RebuildError(f"{rec['record_key']}: unsupported blob_type {blob_type!r}")
    return out


def rebuild_dataset(dataset: str, edited_jsonl: Path, out_dir: Path, *,
                    master: Path | None = None, archives=None, level: int = 9,
                    force_rewrite: bool = False, mode: str = "relayout",
                    progress=None) -> dict:
    """Rebuild only the archives that contain edits (or all, with force_rewrite)."""
    out_dir = Path(out_dir)
    target_dir = out_dir / master_dirname(dataset)
    target_dir.mkdir(parents=True, exist_ok=True)
    grouped = _group_edits(records.read_jsonl(edited_jsonl)) if edited_jsonl else {}

    by_archive: dict[int, dict] = defaultdict(dict)
    for archive, entries in grouped.items():
        by_archive[archive].update(entries)

    if master is None:
        master = corpus.master_dir(dataset)
    archives_paths = sorted(Path(master).glob("GxArchivedFile*.dat"))
    if archives:
        wanted = set(archives)
        archives_paths = [p for p in archives_paths if corpus.archive_number(p) in wanted]

    report = {"dataset": dataset, "archives": [], "total_edited_entries": 0,
              "total_edited_records": 0}
    for path in archives_paths:
        number = corpus.archive_number(path)
        edits_for_archive = by_archive.get(number) or {}
        if not edits_for_archive and not force_rewrite:
            continue
        with gdat.Gdat(path) as g:
            new_entries: list[tuple[int, bytes]] = []
            changed = 0
            for e in g.entries:
                payload_edits = edits_for_archive.get(e.index)
                original_entry = g.read(e)
                if not payload_edits:
                    new_entries.append((e.index, original_entry))
                    continue
                if payload_edits.get("enc_row") or payload_edits.get("bin_string"):
                    new_entry = _apply_system(original_entry, payload_edits, e.index)
                    report["total_edited_records"] += (len(payload_edits.get("enc_row", {}))
                                                       + len(payload_edits.get("bin_string", {})))
                    if new_entry == original_entry:
                        new_entries.append((e.index, original_entry))
                        continue
                    new_entries.append((e.index, new_entry))
                    changed += 1
                    continue
                payload = g.read_payload(e)
                bh, _, prefix_len = g.payload_info(e)
                prefix = original_entry[:prefix_len]
                new_payload, n_records = _apply_entry(payload, payload_edits["gnl"], e.index, mode)
                report["total_edited_records"] += n_records
                if new_payload == payload:
                    new_entries.append((e.index, original_entry))
                    continue
                algo = bh.algo if bh else bilz.ALGO_ZLIB
                new_entries.append((e.index, prefix + bilz.wrap(new_payload, algo, level)))
                changed += 1
            report["total_edited_entries"] += changed
            blob = gdat.build(new_entries)
        target = target_dir / path.name
        target.write_bytes(blob)
        report["archives"].append({
            "archive_number": number, "file": path.name, "entries_changed": changed,
            "source_size": path.stat().st_size, "rebuilt_size": target.stat().st_size,
            "byte_identical": blob == path.read_bytes(),
        })
        if progress:
            progress(path.name, changed)
    return report


def _apply_system(entry_bytes: bytes, edits: dict, gdat_index: int) -> bytes:
    """Apply `enc`/`bin` edits to a wrapped GDAT entry."""
    if edits.get("enc_row"):
        f = enc.EncFile(entry_bytes)
        return f.rebuild({k: v for k, v in edits["enc_row"].items()})
    if edits.get("bin_string"):
        f = enc.BinFile(entry_bytes)
        return f.rebuild({k: v for k, v in edits["bin_string"].items()})
    return entry_bytes


def _edit_whole_blob(blob: bytes, fields: dict, gdat_index: int, cl3_index: int):
    """Edit a blob that *is* the container (no STCM2/CL3 wrapper around it)."""
    try:
        g = gbnl.Gbnl(blob)
    except gbnl.GbnlError as exc:
        raise RebuildError(
            f"gdat entry {gdat_index} cl3 file {cl3_index}: container is neither "
            f"STCM2 nor GBNL/GBIN ({exc})") from exc
    return edit.apply_edits(g, {tuple(k): v for k, v in fields.items()})


def _apply_entry(payload: bytes, edits: dict, gdat_index: int,
                 mode: str = "relayout") -> tuple[bytes, int]:
    """Apply one GDAT entry's edits; returns (new_payload, edited_field_count)."""
    total_fields = 0
    if -1 in edits:
        # The GDAT entry payload itself is the text blob (GBIN/GSTR stored bare,
        # e.g. MS2 PC archive 051 entry 0).  No CL3, no STCM2 to relayout.
        fields = edits[-1]
        if -1 not in fields:
            raise RebuildError(
                f"gdat entry {gdat_index}: bare-container edits must be keyed by -1")
        new_blob, _grew = _edit_whole_blob(payload, fields[-1], gdat_index, -1)
        return new_blob, sum(len(f) for f in fields.values())
    c = cl3.Cl3(payload)
    file_edits: dict[int, bytes] = {}
    for cl3_index, blob_edits in edits.items():
        blob = c.file_data(cl3_index)
        if blob[:5] != b"STCM2":
            # A CL3 member can itself be the text blob (GDAT -> BILZ -> CL3 -> GBIN).
            fields = blob_edits
            if -1 in fields:
                new_blob, _grew = _edit_whole_blob(blob, fields[-1], gdat_index, cl3_index)
                total_fields += sum(len(f) for f in fields.values())
                file_edits[cl3_index] = new_blob
                continue
            raise RebuildError(
                f"gdat entry {gdat_index} cl3 file {cl3_index}: not an STCM2 container")
        s = stcm.Stcm(blob)
        data_edits: dict[int, bytes] = {}
        for blob_offset, fields in blob_edits.items():
            if blob_offset == -1:
                raise RebuildError(
                    f"gdat entry {gdat_index} cl3 file {cl3_index}: a GBNL blob "
                    f"inside an STCM2 must carry repack_metadata.blob_offset")
            item = s.data_items.get(int(blob_offset))
            if item is None:
                raise RebuildError(
                    f"no GBNL blob at {blob_offset} in gdat entry {gdat_index}")
            g = gbnl.Gbnl(item.payload)
            new_blob, _grew = edit.apply_edits(g, {tuple(k): v for k, v in fields.items()})
            total_fields += len(fields)
            if new_blob != item.payload:
                data_edits[int(blob_offset)] = new_blob
        if data_edits:
            problems_before = s.verify_structure()
            new_stcm = s.relayout(data_edits) if mode == "relayout" else s.rebuild(data_edits)
            checked = stcm.Stcm(new_stcm)
            problems_after = checked.verify_structure()
            # The corpus itself contains call targets that the traversal does not
            # classify as items, so the bar is "no regression", not "no findings":
            # the rebuild must not lose items or add new unresolved references.
            # The bar is "no regression", not "no findings": every shipped STCM in
            # the corpus already carries unresolved call targets (measured: 0 of
            # 1,903 STCMs in MSF 065/071 and MS2 PC 055 verify clean; 7-538 findings
            # each), so a rebuild is only rejected when it makes the situation
            # *worse* or drops items/instructions.
            if (len(problems_after) > len(problems_before)
                    or len(checked.data_items) < len(s.data_items)
                    or len(checked.instructions) < len(s.instructions)):
                raise RebuildError(
                    f"gdat entry {gdat_index} cl3 file {cl3_index}: rebuild regressed the "
                    f"STCM (unresolved call targets {len(problems_before)} -> "
                    f"{len(problems_after)}, data items {len(s.data_items)} -> "
                    f"{len(checked.data_items)}, instructions {len(s.instructions)} -> "
                    f"{len(checked.instructions)}; first new: "
                    f"{(problems_after or ['-'])[0]})")
            file_edits[cl3_index] = new_stcm
    if not file_edits:
        return payload, total_fields
    return c.rebuild(file_edits), total_fields
