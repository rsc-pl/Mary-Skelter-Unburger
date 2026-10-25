#!/usr/bin/env python3
"""Materialize exact decoded-ENC translation targets from local game archives.

The generic ENC scanner is intentionally conservative and content-sensitive. The
translation corpus instead carries proven structural locators
(archive/GDAT/row/decoded offset). This script replaces scanner-derived records at
those locators with exact source records read directly from configured vanilla
archives. Slot capacities come from public structural metadata; no game text is
stored there.
"""
from __future__ import annotations

import argparse
import csv
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'tools'))

from mskit import corpus, enc, gdat, records  # noqa: E402

KEY_FIELD_RE = re.compile(r'/f(\d+)$')


def source_locator(rec: dict):
    if rec.get('field_role') != 'enc_string':
        return None
    meta = rec.get('repack_metadata') or {}
    if 'row' not in meta or 'str_offset' not in meta:
        return None
    return (
        rec['source_dataset'], int(rec['archive_number']), int(rec['gdat_index']),
        int(meta['row']), int(meta['str_offset']),
    )


def load_slots(path: Path) -> dict[tuple, int]:
    out = {}
    with path.open(encoding='utf-8', newline='') as fp:
        for row in csv.DictReader(fp, delimiter='\t'):
            loc = (
                row['dataset'], int(row['archive']), int(row['gdat_index']),
                int(row['row']), int(row['decoded_offset']),
            )
            if loc in out:
                raise SystemExit(f'duplicate decoded-ENC slot manifest locator: {loc!r}')
            out[loc] = int(row['slot_capacity'])
    return out


def load_targets(translations: Path) -> dict[tuple, dict]:
    out = {}
    for path in sorted(translations.glob('*.jsonl')):
        with path.open(encoding='utf-8') as fp:
            for line in fp:
                if not line.strip():
                    continue
                obj = json.loads(line)
                src = obj.get('source') or {}
                if 'decoded_offset' not in src:
                    continue
                for fld in obj.get('fields', []):
                    loc = (
                        fld['target_dataset'], int(src['localized_archive']),
                        int(src['localized_gdat_index']), int(src['row']),
                        int(src['decoded_offset']),
                    )
                    if loc in out:
                        raise SystemExit(f'duplicate decoded-ENC translation locator: {loc!r}')
                    match = KEY_FIELD_RE.search(fld['localized_record_key'])
                    if not match:
                        raise SystemExit(
                            f'cannot recover field ordinal from {fld["localized_record_key"]!r}')
                    out[loc] = {
                        'working_id': obj['id'], 'kind': fld['kind'],
                        'localized': fld['LOCALIZED'],
                        'record_key': fld['localized_record_key'],
                        'field_index': int(match.group(1)),
                    }
    return out


def record_from_enc(*, dataset: str, archive_path: Path, archive: int, gidx: int,
                    enc_file: enc.EncFile, row_index: int, offset: int, spec: dict,
                    capacity: int, source_sha: str) -> dict:
    if row_index < 0 or row_index >= enc_file.count:
        raise SystemExit(
            f'{dataset}: a{archive:03d}/g{gidx:05d} row {row_index} outside ENC row table')
    row = enc_file.rows[row_index]
    decoded = row.decoded_payload()
    old = records.encode_text(spec['localized'], 'utf-8')
    end0 = offset + len(old)
    slot_end = offset + int(capacity)
    label = spec['record_key']
    if offset < 0 or end0 >= len(decoded) or slot_end >= len(decoded):
        raise SystemExit(f'{label}: decoded-ENC target/slot outside pristine row')
    if decoded[offset:end0] != old:
        got = bytes(decoded[offset:end0])
        raise SystemExit(
            f'{label}: LOCALIZED bytes do not match pristine decoded row at proven locator; '
            f'expected={old!r} source={got!r}')
    if decoded[end0] != 0:
        raise SystemExit(f'{label}: pristine decoded-ENC literal is not NUL-terminated')
    if capacity < len(old):
        raise SystemExit(f'{label}: pinned slot capacity is shorter than localized literal')
    if any(decoded[end0:slot_end + 1]):
        raise SystemExit(f'{label}: pinned slot capacity crosses nonzero pristine row bytes')

    ds_spec = corpus.DATASETS[dataset]
    chain = ['GDAT', 'BILZ', 'ENC(row table)']
    if row.flag31:
        chain.append('LZO1X(decoded row)')
    rec = records.make_record(
        dataset=dataset, game=ds_spec['game'], platform=ds_spec['platform'],
        language=records.detect_language(spec['localized']) or ds_spec['language'],
        source_file=archive_path.name, source_file_sha256=source_sha,
        archive_number=archive, gdat_index=gidx, format_chain=chain,
        cl3_inner_index=None, cl3_inner_name=None, container_key='top',
        table_key=f'row{row_index:04d}', record_index=row_index,
        field_index=spec['field_index'], field_role='enc_string', encoding='utf-8',
        text=spec['localized'], raw=old,
        control={
            'terminated': True, 'row_count': enc_file.count,
            'string_ordinal_in_row': spec['field_index'],
            'decoded_row': True, 'inner_lzo1x': row.flag31,
        },
        repack={
            'blob_type': 'enc_row', 'row': row_index, 'str_offset': offset,
            'str_len': len(old), 'slot_capacity': int(capacity),
        },
        decode_errors=0,
    )
    rec['record_key'] = label
    return rec


def materialize_record(*, dataset: str, master: Path, archive: int, gidx: int,
                       row_index: int, offset: int, spec: dict, capacity: int,
                       source_sha: str | None = None) -> dict:
    """Single-record helper used by the public synthetic regression."""
    archive_path = master / f'GxArchivedFile{archive:03d}.dat'
    if not archive_path.is_file():
        raise SystemExit(f'missing decoded-ENC source archive: {archive_path}')
    with gdat.Gdat(archive_path) as gd:
        if gidx < 0 or gidx >= gd.count:
            raise SystemExit(f'{dataset}: GDAT index {gidx} outside archive {archive:03d}')
        try:
            ef = enc.EncFile(gd.read(gidx))
        except Exception as exc:  # noqa: BLE001
            raise SystemExit(
                f'{dataset}: a{archive:03d}/g{gidx:05d} is not a readable ENC entry: {exc}') from exc
    return record_from_enc(
        dataset=dataset, archive_path=archive_path, archive=archive, gidx=gidx,
        enc_file=ef, row_index=row_index, offset=offset, spec=spec, capacity=capacity,
        source_sha=source_sha or records.sha256_file(archive_path),
    )


def generate_exact_records(dataset: str, master: Path, wanted: dict, slots: dict,
                           source_sha_by_archive: dict[int, str] | None = None,
                           require_source_sha: bool = True):
    """Yield exact records while opening each archive/GDAT entry only once."""
    source_sha_by_archive = source_sha_by_archive if source_sha_by_archive is not None else {}
    groups = defaultdict(list)
    for loc, spec in wanted.items():
        _ds, archive, gidx, row, off = loc
        groups[(archive, gidx)].append((row, off, loc, spec))

    current_archive = None
    gd = None
    try:
        for (archive, gidx), rows in sorted(groups.items()):
            archive_path = master / f'GxArchivedFile{archive:03d}.dat'
            if not archive_path.is_file():
                raise SystemExit(f'missing decoded-ENC source archive: {archive_path}')
            if current_archive != archive:
                if gd is not None:
                    gd.close()
                gd = gdat.Gdat(archive_path)
                current_archive = archive
            if gidx < 0 or gidx >= gd.count:
                raise SystemExit(f'{dataset}: GDAT index {gidx} outside archive {archive:03d}')
            try:
                ef = enc.EncFile(gd.read(gidx))
            except Exception as exc:  # noqa: BLE001
                raise SystemExit(
                    f'{dataset}: a{archive:03d}/g{gidx:05d} is not a readable ENC entry: {exc}') from exc
            sha = source_sha_by_archive.get(archive)
            if require_source_sha and not sha:
                sha = records.sha256_file(archive_path)
                source_sha_by_archive[archive] = sha
            if not sha:
                sha = 'validate-only'
            for row, off, loc, spec in sorted(rows):
                yield record_from_enc(
                    dataset=dataset, archive_path=archive_path, archive=archive, gidx=gidx,
                    enc_file=ef, row_index=row, offset=off, spec=spec,
                    capacity=slots[loc], source_sha=sha,
                )
    finally:
        if gd is not None:
            gd.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--records', type=Path,
                    help='directory containing freshly extracted dataset JSONL files')
    ap.add_argument('--ms2-master', type=Path, required=True)
    ap.add_argument('--msf-master', type=Path, required=True)
    ap.add_argument('--translations', type=Path, default=ROOT / 'translations')
    ap.add_argument('--slots', type=Path,
                    default=ROOT / 'toolchain' / 'manifests' / 'decoded_enc_slots.tsv')
    ap.add_argument('--validate-only', action='store_true',
                    help='validate all exact decoded-ENC locators without changing source records')
    args = ap.parse_args()
    if not args.validate_only and args.records is None:
        ap.error('--records is required unless --validate-only is used')

    slots = load_slots(args.slots)
    targets = load_targets(args.translations)
    if set(slots) != set(targets):
        missing_slots = set(targets) - set(slots)
        stale_slots = set(slots) - set(targets)
        raise SystemExit(
            f'decoded-ENC slot coverage mismatch: missing={len(missing_slots)} '
            f'stale={len(stale_slots)}')

    masters = {
        'MS2_PC_LOCALIZED': args.ms2_master,
        'MSF_PC_LOCALIZED': args.msf_master,
        'MSF_PC_SYSTEM': args.msf_master,
    }
    by_dataset = defaultdict(dict)
    for loc, spec in targets.items():
        by_dataset[loc[0]][loc] = spec

    if args.validate_only:
        total = 0
        for dataset in sorted(by_dataset):
            n = sum(1 for _ in generate_exact_records(
                dataset, masters[dataset], by_dataset[dataset], slots,
                require_source_sha=False))
            print(f'{dataset}: validated exact decoded-ENC targets={n}')
            total += n
        print(f'Exact decoded-ENC local-source validation: PASS ({total} targets)')
        return

    for dataset in sorted(by_dataset):
        path = args.records / f'{dataset}.jsonl'
        if not path.is_file():
            raise SystemExit(f'missing freshly extracted source records: {path}')
        wanted = by_dataset[dataset]
        tmp = path.with_suffix(path.suffix + '.exact-enc.tmp')
        source_sha_by_archive = {}
        removed = 0
        kept = 0
        with path.open(encoding='utf-8') as src, tmp.open('w', encoding='utf-8') as dst:
            for line in src:
                if not line.strip():
                    continue
                obj = json.loads(line)
                archive = int(obj['archive_number'])
                if obj.get('source_file_sha256'):
                    source_sha_by_archive.setdefault(archive, obj['source_file_sha256'])
                loc = source_locator(obj)
                if loc in wanted:
                    removed += 1
                    continue
                dst.write(line)
                kept += 1

            generated = 0
            for rec in generate_exact_records(
                    dataset, masters[dataset], wanted, slots, source_sha_by_archive):
                dst.write(json.dumps(rec, ensure_ascii=False, sort_keys=True) + '\n')
                generated += 1
        tmp.replace(path)
        print(
            f'{dataset}: exact decoded-ENC targets={len(wanted)} '
            f'replaced_scanner_records={removed} generated={generated} retained={kept}')


if __name__ == '__main__':
    main()
