#!/usr/bin/env python3
from __future__ import annotations
import importlib.util
import json
import tempfile
import hashlib
from types import SimpleNamespace
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
spec = importlib.util.spec_from_file_location('source_cache', ROOT / 'toolchain' / 'python-src' / 'scripts' / 'source_cache.py')
sc = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(sc)


def write_case(root: Path, *, en: str, localized: str = 'Old') -> None:
    obj = {
        'id': 'TEST_1',
        'source': {'localized_archive': 61, 'localized_gdat_index': 4, 'row': 2, 'decoded_offset': 8},
        'fields': [{
            'target_dataset': 'MS2_PC_LOCALIZED',
            'localized_record_key': 'MS2_PC_LOCALIZED/a061/g00004/top/row0002/off00000008/f0001',
            'LOCALIZED': localized,
            'EN': en,
        }],
    }
    (root / 'x.jsonl').write_text(json.dumps(obj) + '\n', encoding='utf-8')


def main() -> None:
    all_req, text_req = sc.required_archives()
    assert text_req == {
        'MS2_PC_LOCALIZED': {52, 55, 61},
        'MSF_PC_LOCALIZED': {70, 71, 72},
        'MSF_PC_SYSTEM': {55, 88},
    }, text_req
    assert all_req == {
        'MS2': {52, 55, 61},
        'MSF': {55, 70, 71, 72, 73, 74, 88},
    }, all_req

    with tempfile.TemporaryDirectory() as td:
        d = Path(td)
        write_case(d, en='First')
        a = sc.translation_source_identity(d)
        write_case(d, en='Second')
        b = sc.translation_source_identity(d)
        assert a == b, 'EN-only edit must not invalidate pristine/derived source cache'
        write_case(d, en='Second', localized='Different shipped text')
        c = sc.translation_source_identity(d)
        assert c != b, 'source-side LOCALIZED drift must invalidate derived records'

    with tempfile.TemporaryDirectory() as td:
        base = Path(td)
        existing = base / 'existing'
        out = base / 'out'
        rel = Path('pristine/ms2/masterWindows/GxArchivedFile999.dat')
        src = existing / rel
        src.parent.mkdir(parents=True)
        src.write_bytes(b'cached-pristine')
        digest = hashlib.sha256(src.read_bytes()).hexdigest()
        spec = {'files': [{'game': 'MS2', 'kind': 'archive', 'name': src.name, 'bytes': src.stat().st_size, 'sha256': digest}]}
        meta = {'pristine_files': [{'relative_path': rel.as_posix(), 'sha256': digest, 'bytes': src.stat().st_size}]}
        args = SimpleNamespace(ms2_master=None, msf_master=None, ms2_game=None, msf_game=None, ms2_exe=None, msf_exe=None)
        got = sc.seed_pristine(out, spec, args, existing_root=existing, existing_meta=meta)
        assert (out / rel).read_bytes() == b'cached-pristine'
        assert got[0]['sha256'] == digest


    with tempfile.TemporaryDirectory() as td:
        base=Path(td); old=base/'old'; new=base/'new'
        (old/'records').mkdir(parents=True)
        meta={'derived_fingerprint':'same','record_files':{}}
        for ds in sc.RECORD_DATASETS:
            p=old/'records'/f'{ds}.jsonl'; p.write_text('{"record_key":"k"}\n')
            meta['record_files'][ds]={'sha256':sc.sha256_file(p),'bytes':p.stat().st_size,'records':1,'mapped_working_fields':0}
        got=sc.reuse_derived_records(old,meta,new,'same')
        assert got is not None
        assert all((new/'records'/f'{ds}.jsonl').is_file() for ds in sc.RECORD_DATASETS)
        assert sc.reuse_derived_records(old,meta,new/'bad','different') is None

    print('source cache contract regression: PASS')


if __name__ == '__main__':
    main()
