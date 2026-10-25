#!/usr/bin/env python3
from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CACHE_VERSION = 1
CACHE_ROOT_DEFAULT = ROOT / '.local' / 'source_cache'
RECORD_DATASETS = ('MS2_PC_LOCALIZED', 'MSF_PC_LOCALIZED', 'MSF_PC_SYSTEM')
ARCHIVE_RE = re.compile(r'/a(\d{3})/')

sys.path.insert(0, str(ROOT / 'toolchain' / 'python-src' / 'toolchain'))
from exe_qol.profiles import MS2_PROFILE, FINALE_PROFILE  # noqa: E402


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open('rb') as fp:
        for chunk in iter(lambda: fp.read(8 * 1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def digest_paths(paths: list[Path]) -> str:
    h = hashlib.sha256()
    for p in sorted(paths, key=lambda x: str(x.relative_to(ROOT))):
        rel = str(p.relative_to(ROOT)).replace(os.sep, '/')
        h.update(rel.encode('utf-8') + b'\0')
        h.update(p.read_bytes())
        h.update(b'\0')
    return h.hexdigest()


def translation_source_identity(translations: Path | None = None) -> str:
    """Hash only source-side identity, never EN edits.

    Generic scanner output is independent of translations. Exact decoded-ENC
    materialization depends on the proven target locator, LOCALIZED bytes and
    the field ordinal encoded in localized_record_key.
    """
    translations = translations or (ROOT / 'translations')
    h = hashlib.sha256()
    for path in sorted(translations.glob('*.jsonl')):
        h.update(path.name.encode('utf-8') + b'\0')
        with path.open(encoding='utf-8') as fp:
            for line in fp:
                if not line.strip():
                    continue
                obj = json.loads(line)
                src = obj.get('source') or {}
                for fld in obj.get('fields', []):
                    row = {
                        'target_dataset': fld['target_dataset'],
                        'localized_record_key': fld['localized_record_key'],
                        'LOCALIZED': fld['LOCALIZED'],
                    }
                    if 'decoded_offset' in src:
                        row['decoded_locator'] = {
                            'localized_archive': src['localized_archive'],
                            'localized_gdat_index': src['localized_gdat_index'],
                            'row': src['row'],
                            'decoded_offset': src['decoded_offset'],
                        }
                    h.update(json.dumps(row, ensure_ascii=False, sort_keys=True,
                                        separators=(',', ':')).encode('utf-8'))
                    h.update(b'\n')
    return h.hexdigest()


def derived_fingerprint() -> str:
    code = list((ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'tools' / 'mskit').rglob('*.py'))
    code += [
        ROOT / 'toolchain' / 'python-src' / 'scripts' / 'materialize_decoded_enc_targets.py',
        ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'manifests' / 'decoded_enc_slots.tsv',
    ]
    h = hashlib.sha256()
    h.update(f'cache-derived-v{CACHE_VERSION}\n'.encode())
    h.update(digest_paths(code).encode())
    h.update(b'\n')
    h.update(translation_source_identity().encode())
    return h.hexdigest()


def required_archives() -> tuple[dict[str, set[int]], dict[str, set[int]]]:
    """Return (all pristine archives, archives needed for text extraction)."""
    all_req = {'MS2': set(), 'MSF': set()}
    text_req = {ds: set() for ds in RECORD_DATASETS}
    for path in sorted((ROOT / 'translations').glob('*.jsonl')):
        with path.open(encoding='utf-8') as fp:
            for line in fp:
                if not line.strip():
                    continue
                obj = json.loads(line)
                for fld in obj.get('fields', []):
                    ds = fld['target_dataset']
                    if ds not in text_req:
                        continue
                    m = ARCHIVE_RE.search(fld['localized_record_key'])
                    if not m:
                        raise SystemExit(f'cannot recover archive from {fld["localized_record_key"]!r}')
                    archive = int(m.group(1))
                    text_req[ds].add(archive)
                    all_req['MS2' if ds.startswith('MS2_') else 'MSF'].add(archive)

    gm = json.loads((ROOT / 'graphics' / 'manifest.json').read_text(encoding='utf-8'))
    for asset in gm.get('assets', []):
        if not asset.get('enabled'):
            continue
        ds = asset['dataset']
        all_req['MS2' if ds.startswith('MS2_') else 'MSF'].add(int(asset['archive']))
    return all_req, text_req


def expected_archive_manifest() -> dict[tuple[str, str], dict]:
    out: dict[tuple[str, str], dict] = {}
    path = ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'manifests' / 'expected_pc_archive_sha256.tsv'
    with path.open(encoding='utf-8', newline='') as fp:
        for row in csv.DictReader(fp, delimiter='\t'):
            game = 'MS2' if row['dataset'].startswith('MS2_') else 'MSF'
            key = (game, row['file'])
            item = {'bytes': int(row['bytes']), 'sha256': row['sha256']}
            prior = out.get(key)
            if prior and prior != item:
                raise SystemExit(f'conflicting source manifest entries for {key!r}')
            out[key] = item
    return out


def source_spec() -> dict:
    all_req, text_req = required_archives()
    expected = expected_archive_manifest()
    files = []
    for game in ('MS2', 'MSF'):
        for archive in sorted(all_req[game]):
            name = f'GxArchivedFile{archive:03d}.dat'
            exp = expected.get((game, name))
            if not exp:
                raise SystemExit(f'missing public pristine hash for {game} {name}')
            files.append({'game': game, 'kind': 'archive', 'name': name, **exp})
    files.append({
        'game': 'MS2', 'kind': 'exe', 'name': 'MarySkelter2.exe',
        'sha256': MS2_PROFILE.original_sha256,
    })
    # Finale EXE is optional in the public workflow, but cache it when available.
    files.append({
        'game': 'MSF', 'kind': 'exe_optional', 'name': 'MarySkelterFinale.exe',
        'sha256': FINALE_PROFILE.original_sha256,
    })
    canonical = {
        'cache_version': CACHE_VERSION,
        'files': files,
        'text_archives': {k: sorted(v) for k, v in text_req.items()},
    }
    fp = hashlib.sha256(json.dumps(canonical, sort_keys=True,
                                   separators=(',', ':')).encode()).hexdigest()
    canonical['fingerprint'] = fp
    return canonical


def cached_paths(cache_root: Path) -> dict[str, Path]:
    return {
        'ms2_master': cache_root / 'pristine' / 'ms2' / 'masterWindows',
        'msf_master': cache_root / 'pristine' / 'finale' / 'masterWindows',
        'ms2_exe': cache_root / 'pristine' / 'ms2' / 'MarySkelter2.exe',
        'msf_exe': cache_root / 'pristine' / 'finale' / 'MarySkelterFinale.exe',
        'records': cache_root / 'records',
        'meta': cache_root / 'cache.json',
    }


def quick_cache_valid(cache_root: Path, spec: dict) -> tuple[bool, dict | None, str]:
    cp = cached_paths(cache_root)
    try:
        meta = json.loads(cp['meta'].read_text(encoding='utf-8'))
    except Exception:
        return False, None, 'cache metadata missing/unreadable'
    if meta.get('cache_version') != CACHE_VERSION:
        return False, meta, 'cache format version changed'
    if meta.get('source_fingerprint') != spec['fingerprint']:
        return False, meta, 'required pristine source set changed'
    listed = {x.get('relative_path'): x for x in meta.get('pristine_files', [])}
    required = set()
    for item in spec['files']:
        if item['kind'] == 'exe_optional':
            continue
        base = 'ms2' if item['game'] == 'MS2' else 'finale'
        if item['kind'] == 'archive':
            rel = f'pristine/{base}/masterWindows/{item["name"]}'
        else:
            rel = f'pristine/{base}/{item["name"]}'
        required.add(rel)
    missing = required - set(listed)
    if missing:
        return False, meta, f'cache metadata missing required pristine file: {sorted(missing)[0]}'
    for rel, item in listed.items():
        p = cache_root / rel
        if not p.is_file():
            return False, meta, f'missing cached pristine file: {rel}'
        if 'bytes' in item and p.stat().st_size != int(item['bytes']):
            return False, meta, f'cached pristine size drift: {rel}'
    return True, meta, 'cache source set ready'


def copy_reflink(src: Path, dst: Path) -> None:
    dst.parent.mkdir(parents=True, exist_ok=True)
    cp = shutil.which('cp')
    if cp:
        proc = subprocess.run(
            [cp, '--reflink=auto', '--preserve=mode,timestamps', '--', str(src), str(dst)],
            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True,
        )
        if proc.returncode == 0:
            return
    shutil.copy2(src, dst)


def source_arg_paths(args) -> dict[str, Path | None]:
    ms2_exe = args.ms2_exe
    if not ms2_exe and args.ms2_game:
        ms2_exe = args.ms2_game / 'MarySkelter2.exe'
    msf_exe = args.msf_exe
    if not msf_exe and args.msf_game:
        msf_exe = args.msf_game / 'MarySkelterFinale.exe'
    return {
        'MS2_master': args.ms2_master,
        'MSF_master': args.msf_master,
        'MS2_exe': ms2_exe,
        'MSF_exe': msf_exe,
    }


def seed_pristine(temp_root: Path, spec: dict, args, *, existing_root: Path | None = None, existing_meta: dict | None = None) -> list[dict]:
    srcs = source_arg_paths(args)
    out = []
    prior = {x.get('relative_path'): x for x in (existing_meta or {}).get('pristine_files', [])}
    for item in spec['files']:
        game = item['game']
        if item['kind'] == 'archive':
            master = srcs[f'{game}_master']
            src = master / item['name'] if master else None
            rel = Path('pristine') / ('ms2' if game == 'MS2' else 'finale') / 'masterWindows' / item['name']
        else:
            src = srcs[f'{game}_exe']
            rel = Path('pristine') / ('ms2' if game == 'MS2' else 'finale') / item['name']

        prior_item = prior.get(str(rel).replace(os.sep, '/'))
        prior_path = existing_root / rel if existing_root is not None else None
        if (prior_item and prior_path is not None and prior_path.is_file()
                and prior_item.get('sha256') == item['sha256']
                and ('bytes' not in item or prior_path.stat().st_size == int(item['bytes']))):
            dst = temp_root / rel
            print(f'SOURCE CACHE: reusing verified {game} {item["name"]}')
            copy_reflink(prior_path, dst)
            out.append({
                'relative_path': str(rel).replace(os.sep, '/'),
                'sha256': item['sha256'],
                'bytes': dst.stat().st_size,
                'kind': item['kind'],
                'game': game,
            })
            continue

        if item['kind'] == 'exe_optional' and (not src or not src.is_file()):
            continue
        if not src or not src.is_file():
            raise SystemExit(
                f'missing pristine source input needed to seed/extend cache: {src or item["name"]}')
        if 'bytes' in item and src.stat().st_size != int(item['bytes']):
            raise SystemExit(
                f'{src}: source size does not match supported pristine release; '
                f'got={src.stat().st_size} expected={item["bytes"]}')
        dst = temp_root / rel
        print(f'SOURCE CACHE: caching {game} {item["name"]}')
        copy_reflink(src, dst)
        got = sha256_file(dst)
        if got != item['sha256']:
            raise SystemExit(
                f'{src}: source SHA-256 does not match supported pristine release; '
                f'got={got} expected={item["sha256"]}')
        out.append({
            'relative_path': str(rel).replace(os.sep, '/'),
            'sha256': got,
            'bytes': dst.stat().st_size,
            'kind': item['kind'],
            'game': game,
        })
    return out


def run_checked(cmd: list[str]) -> None:
    subprocess.run(cmd, cwd=ROOT, check=True)


def build_records_cache(cache_root: Path, spec: dict) -> dict[str, dict]:
    cp = cached_paths(cache_root)
    records_dir = cp['records']
    tmp = records_dir.with_name(records_dir.name + '.tmp')
    shutil.rmtree(tmp, ignore_errors=True)
    tmp.mkdir(parents=True)
    tool = ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'tools' / 'ms_text_tool'
    masters = {
        'MS2_PC_LOCALIZED': cp['ms2_master'],
        'MSF_PC_LOCALIZED': cp['msf_master'],
        'MSF_PC_SYSTEM': cp['msf_master'],
    }
    for ds in RECORD_DATASETS:
        archives = [str(x) for x in spec['text_archives'][ds]]
        run_checked([
            sys.executable, str(tool), 'extract', '--dataset', ds,
            '--master', str(masters[ds]), '--archives', *archives,
            '--out', str(tmp / f'{ds}.jsonl'),
        ])
    run_checked([
        sys.executable, str(ROOT / 'toolchain' / 'python-src' / 'scripts' / 'materialize_decoded_enc_targets.py'),
        '--records', str(tmp), '--ms2-master', str(cp['ms2_master']),
        '--msf-master', str(cp['msf_master']),
    ])

    # Validate every working target against the generated cache without applying EN.
    by_ds = {ds: {} for ds in RECORD_DATASETS}
    for path in sorted((ROOT / 'translations').glob('*.jsonl')):
        with path.open(encoding='utf-8') as fp:
            for line in fp:
                if not line.strip():
                    continue
                obj = json.loads(line)
                for fld in obj.get('fields', []):
                    ds = fld['target_dataset']
                    if ds in by_ds:
                        by_ds[ds][fld['localized_record_key']] = fld['LOCALIZED']
    record_meta = {}
    for ds in RECORD_DATASETS:
        wanted = by_ds[ds]
        found = set()
        count = 0
        path = tmp / f'{ds}.jsonl'
        with path.open(encoding='utf-8') as fp:
            for line in fp:
                if not line.strip():
                    continue
                count += 1
                obj = json.loads(line)
                k = obj['record_key']
                if k in wanted:
                    if obj['text'] != wanted[k]:
                        raise SystemExit(
                            f'{ds}: cached LOCALIZED drift for {k}: '
                            f'raw={obj["text"]!r} working={wanted[k]!r}')
                    found.add(k)
        missing = set(wanted) - found
        if missing:
            raise SystemExit(
                f'{ds}: source cache missing {len(missing)} working targets; '
                f'first={next(iter(missing))}')
        canonical = json.loads((ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'manifests' / 'canonical_counts.json').read_text())
        expected_mapped = canonical.get('expected_mapped_fields_by_dataset', {}).get(ds)
        if expected_mapped is not None and len(found) != int(expected_mapped):
            raise SystemExit(
                f'{ds}: cached mapped field count {len(found)} != canonical {expected_mapped}')
        record_meta[ds] = {
            'records': count,
            'mapped_working_fields': len(found),
            'bytes': path.stat().st_size,
            'sha256': sha256_file(path),
        }
        print(f'SOURCE CACHE: {ds} records={count} mapped={len(found)}')
    shutil.rmtree(records_dir, ignore_errors=True)
    tmp.replace(records_dir)
    return record_meta


def reuse_derived_records(existing_root: Path, existing_meta: dict, temp_root: Path, current_derived: str) -> dict[str, dict] | None:
    """Reuse validated derived records when only the pristine graphics source set expands."""
    if existing_meta.get('derived_fingerprint') != current_derived:
        return None
    old_records=cached_paths(existing_root)['records']
    meta=existing_meta.get('record_files') or {}
    if not all((old_records/f'{ds}.jsonl').is_file() and ds in meta for ds in RECORD_DATASETS):
        return None
    new_records=cached_paths(temp_root)['records']
    new_records.mkdir(parents=True,exist_ok=True)
    out={}
    for ds in RECORD_DATASETS:
        src=old_records/f'{ds}.jsonl'; dst=new_records/f'{ds}.jsonl'
        copy_reflink(src,dst)
        got=sha256_file(dst)
        if meta[ds].get('sha256') and got != meta[ds]['sha256']:
            raise SystemExit(f'{ds}: existing derived source-record cache SHA drift')
        out[ds]=dict(meta[ds]); out[ds]['sha256']=got; out[ds]['bytes']=dst.stat().st_size
    print('SOURCE CACHE: reused derived records; only pristine graphics inputs changed')
    return out


def ensure_cache(args) -> None:
    cache_root = args.cache_root.resolve()
    spec = source_spec()
    source_ok, meta, reason = quick_cache_valid(cache_root, spec)
    if args.refresh_pristine:
        source_ok = False
        reason = 'explicit pristine refresh requested'
    if source_ok:
        cp = cached_paths(cache_root)
        if not cp['msf_exe'].is_file():
            srcs = source_arg_paths(args)
            candidate = srcs.get('MSF_exe')
            if candidate and candidate.is_file():
                got = sha256_file(candidate)
                if got != FINALE_PROFILE.original_sha256:
                    raise SystemExit(
                        f'{candidate}: Finale EXE SHA-256 does not match supported pristine release; '
                        f'got={got} expected={FINALE_PROFILE.original_sha256}')
                copy_reflink(candidate, cp['msf_exe'])
                rel = str(cp['msf_exe'].relative_to(cache_root)).replace(os.sep, '/')
                meta.setdefault('pristine_files', []).append({
                    'relative_path': rel, 'sha256': got, 'bytes': cp['msf_exe'].stat().st_size,
                    'kind': 'exe_optional', 'game': 'MSF',
                })
                cp['meta'].write_text(json.dumps(meta, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
                print('SOURCE CACHE: added configured pristine Finale EXE')
        current_derived = derived_fingerprint()
        records_ok = (
            meta.get('derived_fingerprint') == current_derived
            and all((cached_paths(cache_root)['records'] / f'{ds}.jsonl').is_file()
                    for ds in RECORD_DATASETS)
        )
        if records_ok:
            print(f'SOURCE CACHE: HIT ({cache_root})')
            return
        print(f'SOURCE CACHE: pristine HIT; regenerating derived records ({reason})')
        record_meta = build_records_cache(cache_root, spec)
        meta['derived_fingerprint'] = current_derived
        meta['record_files'] = record_meta
        tmpmeta = cached_paths(cache_root)['meta'].with_suffix('.json.tmp')
        tmpmeta.write_text(json.dumps(meta, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        tmpmeta.replace(cached_paths(cache_root)['meta'])
        print('SOURCE CACHE: derived records refreshed from cached pristine inputs')
        return

    print(f'SOURCE CACHE: MISS ({reason}); seeding pristine inputs once')
    parent = cache_root.parent
    parent.mkdir(parents=True, exist_ok=True)
    tmp_root = Path(tempfile.mkdtemp(prefix=cache_root.name + '.tmp.', dir=parent))
    try:
        old_meta = meta or {}
        pristine = seed_pristine(tmp_root, spec, args, existing_root=cache_root if cache_root.exists() else None, existing_meta=old_meta)
        current_derived = derived_fingerprint()
        record_meta = None
        if cache_root.exists():
            record_meta = reuse_derived_records(cache_root, old_meta, tmp_root, current_derived)
        if record_meta is None:
            record_meta = build_records_cache(tmp_root, spec)
        meta = {
            'cache_version': CACHE_VERSION,
            'source_fingerprint': spec['fingerprint'],
            'derived_fingerprint': current_derived,
            'pristine_files': pristine,
            'record_files': record_meta,
        }
        (tmp_root / 'cache.json').write_text(
            json.dumps(meta, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        old = cache_root.with_name(cache_root.name + '.previous')
        shutil.rmtree(old, ignore_errors=True)
        if cache_root.exists():
            cache_root.replace(old)
        tmp_root.replace(cache_root)
        shutil.rmtree(old, ignore_errors=True)
    except BaseException:
        shutil.rmtree(tmp_root, ignore_errors=True)
        raise
    print(f'SOURCE CACHE: READY ({cache_root})')


def print_env(cache_root: Path) -> None:
    cp = cached_paths(cache_root.resolve())
    if not cp['meta'].is_file():
        raise SystemExit('source cache is not initialized')
    pairs = {
        'MS2_PC_MASTER': cp['ms2_master'],
        'MSF_PC_MASTER': cp['msf_master'],
        'MS2_PC_EXE': cp['ms2_exe'],
        'MARY_SKELTER_SOURCE_RECORD_CACHE': cp['records'],
        'MARY_SKELTER_SOURCE_CACHE': cache_root.resolve(),
    }
    if cp['msf_exe'].is_file():
        pairs['MSF_PC_EXE'] = cp['msf_exe']
        pairs['FINALE_PC_EXE'] = cp['msf_exe']
    for key, value in pairs.items():
        print(f'export {key}={sh_quote(str(value))}')
    print('export MARY_SKELTER_SOURCE_CACHE_READY=1')


def sh_quote(s: str) -> str:
    return "'" + s.replace("'", "'\\''") + "'"


def status(cache_root: Path) -> int:
    spec = source_spec()
    ok, meta, reason = quick_cache_valid(cache_root.resolve(), spec)
    if not ok:
        print(f'SOURCE CACHE: NOT READY ({reason})')
        return 1
    current = derived_fingerprint()
    rec_ok = (
        meta.get('derived_fingerprint') == current
        and all((cached_paths(cache_root.resolve())['records'] / f'{ds}.jsonl').is_file()
                for ds in RECORD_DATASETS)
    )
    print(f'SOURCE CACHE: pristine READY; derived={"READY" if rec_ok else "STALE"}')
    return 0 if rec_ok else 2


def main() -> int:
    ap = argparse.ArgumentParser(description='Persistent pristine/derived source cache manager')
    ap.add_argument('--cache-root', type=Path, default=CACHE_ROOT_DEFAULT)
    sub = ap.add_subparsers(dest='cmd', required=True)
    s = sub.add_parser('ensure')
    s.add_argument('--ms2-master', type=Path)
    s.add_argument('--msf-master', type=Path)
    s.add_argument('--ms2-game', type=Path)
    s.add_argument('--msf-game', type=Path)
    s.add_argument('--ms2-exe', type=Path)
    s.add_argument('--msf-exe', type=Path)
    s.add_argument('--refresh-pristine', action='store_true')
    sub.add_parser('env')
    sub.add_parser('status')
    args = ap.parse_args()
    if args.cmd == 'ensure':
        ensure_cache(args)
        return 0
    if args.cmd == 'env':
        print_env(args.cache_root)
        return 0
    return status(args.cache_root)


if __name__ == '__main__':
    raise SystemExit(main())
