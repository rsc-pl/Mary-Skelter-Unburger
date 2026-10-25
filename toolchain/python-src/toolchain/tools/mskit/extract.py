"""Resource classification, census and text extraction."""
from __future__ import annotations

import json
from collections import Counter
from pathlib import Path

from . import bilz, cl3, corpus, enc, gbnl, gdat, records, stcm

MAX_SNIFF = 0x10000


class ExtractStats:
    def __init__(self) -> None:
        self.entries = 0
        self.kinds: Counter = Counter()
        self.records = 0
        self.by_role: Counter = Counter()
        self.decode_errors = 0
        self.unparsed = Counter()
        self.text_resources = 0

    def as_dict(self) -> dict:
        return {"entries": self.entries, "kinds": dict(self.kinds), "records": self.records,
                "by_role": dict(self.by_role), "decode_errors": self.decode_errors,
                "unparsed": dict(self.unparsed), "text_resources": self.text_resources}


def sniff(payload: bytes) -> str:
    """Cheap content classification of a decompressed GDAT entry payload."""
    if len(payload) >= 4 and payload[:3] == b"CL3" and payload[3:4] in (b"L", b"B"):
        return "cl3"
    if payload[:4] == b"GDAT":
        return "gdat_nested"
    if payload[:3] == b"GST":
        return "gstr"
    if payload[:3] == b"GBN" or (len(payload) > 0x40 and payload[-0x40:-0x3d] == b"GBN"):
        return "gbin"
    if payload[:5] == b"STCM2":
        return "stcm"
    if payload[:4] == b"BILZ":
        return "bilz"
    head4 = payload[:4]
    printable = bytes(c if 32 <= c < 127 else 0x2E for c in head4)
    return f"other:{printable.decode('ascii')}"


def entry_tag(g: gdat.Gdat, index: int) -> str | None:
    """Format tag from the 0x80-byte Orochi wrapper header of an entry."""
    prefix = g.read_prefix(index, 4)
    return enc.wrapper_tag(prefix) if len(prefix) == 4 else None


def inner_kind(blob: bytes) -> str:
    if blob[:5] == b"STCM2":
        return "stcm"
    if blob[:3] == b"GST":
        return "gstr"
    if len(blob) > 0x40 and blob[-0x40:-0x3d] == b"GBN":
        return "gbin"
    if blob[:4] == b"MAL0":
        return "mal"
    if blob[:4] == b"ACB\x01":
        return "acb"
    if blob[:4] == b"TID\x00":
        return "tid"
    return "other"


# --------------------------------------------------------------------- scan
def scan_entry(g: gdat.Gdat, index: int) -> dict:
    """Inventory row for one GDAT entry (no text decoding)."""
    e = g.entries[index]
    bh, _, prefix_len = g.payload_info(index)
    row = {"gdat_index": index, "entry_offset": e.offset, "entry_size": e.size,
           "bilz": bh.as_dict() if bh else None, "prefix_len": prefix_len}
    try:
        payload = g.read_payload(index)
    except Exception as exc:  # noqa: BLE001
        row.update(kind="unreadable", note=repr(exc)[:120])
        return row
    row["payload_size"] = len(payload)
    kind = sniff(payload)
    row["kind"] = kind
    if kind == "cl3":
        try:
            c = cl3.Cl3(payload)
        except cl3.Cl3Error as exc:
            row.update(kind="cl3_invalid", note=str(exc)[:120])
            return row
        row["cl3_files"] = [{"index": f.index, "name": f.name, "size": f.size,
                             "kind": inner_kind(c.file_data(f.index))} for f in c.files]
        row["inner_kinds"] = dict(Counter(f["kind"] for f in row["cl3_files"]))
        row["inner_names"] = [f.name for f in c.files]
    elif kind in ("gstr", "gbin", "stcm"):
        row["script"] = True
    return row


# ------------------------------------------------------------------ extract
def _gnl_records(blob: bytes, *, dataset: str, game: str, platform: str, language,
                 source_file: str, source_sha: str, archive: int, gdat_index: int,
                 chain: list[str], cl3_index, cl3_name, container_key: str,
                 table_key: str, stats: ExtractStats, include_fixstr: bool = False,
                 blob_offset: int | None = None):
    try:
        g = gbnl.Gbnl(blob)
    except gbnl.GbnlError as exc:
        stats.unparsed[f"gnl:{exc}"] += 1
        return
    enc = "utf-8" if g.field_30 == 8 else "cp932"
    e = g.endian
    for mi, fields in enumerate(g.messages):
        for fi, fld in enumerate(fields):
            if fld.kind not in ("str", "fixstr"):
                continue
            if fld.kind == "fixstr" and not include_fixstr:
                continue
            if fld.is_null:
                continue
            raw = fld.str_raw or b""
            text, errs = records.decode_text(raw, enc)
            stats.decode_errors += errs
            yield records.make_record(
                dataset=dataset, game=game, platform=platform, language=language,
                source_file=source_file, source_file_sha256=source_sha,
                archive_number=archive, gdat_index=gdat_index, format_chain=chain,
                cl3_inner_index=cl3_index, cl3_inner_name=cl3_name,
                container_key=container_key, table_key=table_key,
                record_index=mi, field_index=fi, field_role=f"gnl_{fld.kind}",
                encoding=enc, text=text, raw=raw,
                control={"endian": e, "msg_descr_size": g.msg_descr_size,
                         "count_types": g.count_types, "flags": g.flags,
                         "field_28": g.field_28, "field_30": g.field_30,
                         "field_absent": False, "string_is_null": False},
                repack={"blob_type": "gnl", "blob_offset": blob_offset,
                        "struct_offset": fld.offset,
                        "table_offset": fld.str_offset,
                        "field_kind": fld.kind,
                        "field_size": fld.size},
                decode_errors=errs)


def _system_records(payload: bytes, tag: str, *, dataset: str, game: str, platform: str,
                    language, source_file: str, source_sha: str, archive: int,
                    gdat_index: int, stats: ExtractStats):
    """Records for the `enc` (row table) and `bin` (string table) system text.

    The wrapper tag lives in the entry header rather than in the payload, so the
    parser is constructed from the payload plus a synthetic header carrying the
    tag (only the tag is read from it).
    """
    header = bytes(reversed(tag.encode() + b"\0")) + b"\0" * (0x80 - 4)
    try:
        if tag == "enc":
            f = enc.EncFile(None, header=header, payload=payload)
            per_row_ordinal = {}
            for s in f.strings():
                text, errs = records.decode_text(s.raw, "utf-8")
                stats.decode_errors += errs
                detected = records.detect_language(text) or language
                ordinal = per_row_ordinal.get(s.row_index, 0)
                per_row_ordinal[s.row_index] = ordinal + 1
                yield records.make_record(
                    dataset=dataset, game=game, platform=platform, language=detected,
                    source_file=source_file, source_file_sha256=source_sha,
                    archive_number=archive, gdat_index=gdat_index,
                    format_chain=["GDAT", "BILZ", "ENC(row table)"],
                    cl3_inner_index=None, cl3_inner_name=None, container_key="top",
                    table_key=f"row{s.row_index:04d}", record_index=s.row_index,
                    field_index=ordinal, field_role="enc_string", encoding="utf-8",
                    text=text, raw=s.raw,
                    control={"terminated": s.terminated, "row_count": f.count,
                             "string_ordinal_in_row": ordinal},
                    repack={"blob_type": "enc_row", "row": s.row_index,
                            "str_offset": s.offset, "str_len": len(s.raw)},
                    decode_errors=errs)
        elif tag == "bin":
            f = enc.BinFile(None, header=header, payload=payload)
            for i, raw in f.strings():
                text, errs = records.decode_text(raw, "utf-8")
                stats.decode_errors += errs
                detected = records.detect_language(text) or language
                yield records.make_record(
                    dataset=dataset, game=game, platform=platform, language=detected,
                    source_file=source_file, source_file_sha256=source_sha,
                    archive_number=archive, gdat_index=gdat_index,
                    format_chain=["GDAT", "BILZ", "BIN(string table)"],
                    cl3_inner_index=None, cl3_inner_name=None, container_key="top",
                    table_key=f"str{i:05d}", record_index=i, field_index=0,
                    field_role="bin_string", encoding="utf-8", text=text, raw=raw,
                    control={"string_count": len(f.offsets)},
                    repack={"blob_type": "bin_string", "string_index": i,
                            "str_len": len(raw)},
                    decode_errors=errs)
    except enc.EncError as exc:
        stats.unparsed[f"{tag}:{exc}"] += 1


def extract_entry(g: gdat.Gdat, index: int, *, dataset: str, spec: dict,
                  source_file: str, source_sha: str, archive: int,
                  stats: ExtractStats, include_fixstr: bool = False):
    """Yield records for one GDAT entry."""
    try:
        payload = g.read_payload(index)
    except Exception as exc:  # noqa: BLE001
        stats.unparsed[f"payload:{type(exc).__name__}"] += 1
        return
    kind = sniff(payload)
    stats.kinds[kind] += 1
    game, platform, language = spec["game"], spec["platform"], spec["language"]

    tag = entry_tag(g, index)
    if tag in ("enc", "bin"):
        produced = 0
        for rec in _system_records(payload, tag, dataset=dataset, game=game,
                                   platform=platform, language=language,
                                   source_file=source_file, source_sha=source_sha,
                                   archive=archive, gdat_index=index, stats=stats):
            produced += 1
            yield rec
        if produced:
            stats.text_resources += 1
        return

    if kind == "cl3":
        try:
            c = cl3.Cl3(payload)
        except cl3.Cl3Error as exc:
            stats.unparsed[f"cl3:{exc}"] += 1
            return
        produced = 0
        for f in c.files:
            blob = c.file_data(f.index)
            ik = inner_kind(blob)
            if ik == "stcm":
                try:
                    s = stcm.Stcm(blob)
                except stcm.StcmError as exc:
                    stats.unparsed[f"stcm:{exc}"] += 1
                    continue
                for item in s.gnl_items():
                    for rec in _gnl_records(
                            item.payload, dataset=dataset, game=game, platform=platform,
                            language=language, source_file=source_file, source_sha=source_sha,
                            archive=archive, gdat_index=index,
                            chain=["GDAT", "BILZ", "CL3", "STCM2", "GBNL"],
                            cl3_index=f.index, cl3_name=f.name,
                            container_key=f"c{f.index}", table_key=f"d{item.offset:06x}",
                            stats=stats, include_fixstr=include_fixstr,
                            blob_offset=item.offset):
                        produced += 1
                        yield rec
                for item in s.string_items():
                    raw = s.string_bytes(item)
                    text, errs = records.decode_text(raw, "utf-8")
                    stats.decode_errors += errs
                    produced += 1
                    yield records.make_record(
                        dataset=dataset, game=game, platform=platform, language=language,
                        source_file=source_file, source_file_sha256=source_sha,
                        archive_number=archive, gdat_index=index,
                        format_chain=["GDAT", "BILZ", "CL3", "STCM2", "string_data"],
                        cl3_inner_index=f.index, cl3_inner_name=f.name,
                        container_key=f"c{f.index}", table_key=f"d{item.offset:06x}",
                        record_index=0, field_index=0, field_role="stcm_string",
                        encoding="utf-8", text=text, raw=raw,
                        control={"offset_unit": item.offset_unit, "field_8": item.field_8},
                        repack={"blob_type": "stcm_string", "blob_offset": item.offset,
                                "struct_offset": 0, "table_offset": None,
                                "field_kind": "stcm_string", "field_size": len(item.payload)},
                        decode_errors=errs)
            elif ik in ("gstr", "gbin"):
                for rec in _gnl_records(
                        blob, dataset=dataset, game=game, platform=platform, language=language,
                        source_file=source_file, source_sha=source_sha, archive=archive,
                        gdat_index=index, chain=["GDAT", "BILZ", "CL3", ik.upper()],
                        cl3_index=f.index, cl3_name=f.name, container_key=f"c{f.index}",
                        table_key="blob0", stats=stats, include_fixstr=include_fixstr,
                        blob_offset=None):
                    produced += 1
                    yield rec
        if produced:
            stats.text_resources += 1
        return

    if kind in ("gstr", "gbin"):
        produced = 0
        for rec in _gnl_records(payload, dataset=dataset, game=game, platform=platform,
                                language=language, source_file=source_file,
                                source_sha=source_sha, archive=archive, gdat_index=index,
                                chain=["GDAT", "BILZ", kind.upper()], cl3_index=None,
                                cl3_name=None, container_key="top", table_key="blob0",
                                stats=stats, include_fixstr=include_fixstr,
                                blob_offset=None):
            produced += 1
            yield rec
        if produced:
            stats.text_resources += 1
        return

    if kind == "stcm":
        try:
            s = stcm.Stcm(payload)
        except stcm.StcmError as exc:
            stats.unparsed[f"stcm:{exc}"] += 1
            return
        for item in s.gnl_items():
            for rec in _gnl_records(item.payload, dataset=dataset, game=game,
                                    platform=platform, language=language,
                                    source_file=source_file, source_sha=source_sha,
                                    archive=archive, gdat_index=index,
                                    chain=["GDAT", "BILZ", "STCM2", "GBNL"], cl3_index=None,
                                    cl3_name=None, container_key="top",
                                    table_key=f"d{item.offset:06x}", stats=stats,
                                    include_fixstr=include_fixstr, blob_offset=item.offset):
                yield rec
        return


def archive_paths(dataset: str, root: Path | None = None, master: Path | None = None,
                  archives=None) -> list[Path]:
    if master is not None:
        files = sorted(Path(master).glob("GxArchivedFile*.dat"))
    else:
        files = corpus.archives_of(dataset, root)
    if archives:
        wanted = set(archives)
        files = [p for p in files if corpus.archive_number(p) in wanted]
    return files


def extract_dataset(dataset: str, out_path: Path, *, root: Path | None = None,
                    master: Path | None = None, inventory_path: Path | None = None,
                    archives=None, include_fixstr: bool = False,
                    progress=None) -> ExtractStats:
    spec = corpus.DATASETS[dataset]
    stats = ExtractStats()
    rows = []
    files = archive_paths(dataset, root, master, archives)

    def gen():
        for path in files:
            number = corpus.archive_number(path)
            sha = records.sha256_file(path)
            with gdat.Gdat(path) as g:
                for index in range(g.count):
                    stats.entries += 1
                    row = scan_entry(g, index)
                    row.update(dataset=dataset, archive_number=number,
                               source_file=str(path.name))
                    rows.append(row)
                    for rec in extract_entry(
                            g, index, dataset=dataset, spec=spec,
                            source_file=path.name, source_sha=sha, archive=number,
                            stats=stats, include_fixstr=include_fixstr):
                        stats.records += 1
                        stats.by_role[rec["field_role"]] += 1
                        yield rec
            if progress:
                progress(path.name, stats)

    n = records.write_jsonl(out_path, gen())
    assert n == stats.records, f"wrote {n} records, counted {stats.records}"
    if inventory_path:
        inventory_path.parent.mkdir(parents=True, exist_ok=True)
        with inventory_path.open("w", encoding="utf-8") as fp:
            cols = ["dataset", "archive_number", "gdat_index", "source_file", "kind",
                    "entry_offset", "entry_size", "payload_size", "prefix_len",
                    "inner_kinds", "inner_names", "bilz"]
            fp.write("\t".join(cols) + "\n")
            for row in rows:
                fp.write("\t".join(
                    json.dumps(row.get(c), ensure_ascii=False) if isinstance(
                        row.get(c), (dict, list)) else str(row.get(c, ""))
                    for c in cols) + "\n")
    return stats
