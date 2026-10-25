"""Canonical PC -> Western Switch runtime adapter (offline preparation only).

This is not a second translation tree. Stable source-key alignment is immutable
metadata; new EN is ALWAYS read from current project translations/*.jsonl.
Source Switch records remain read-only. Native rebuild and release are gated
separately; this tool does not claim completeness or change public packages.
"""
from __future__ import annotations

import argparse
from collections import Counter
import gzip
import hashlib
import json
import os
from pathlib import Path
import sys

FILES = {
    'ms2': ('MS2_STORY.jsonl','MS2_SYSTEM_BIN_055.jsonl','MS2_SYSTEM_ENC_052.jsonl','MS2_SYSTEM_ENC_061.jsonl'),
    'finale': ('MSF_STORY.jsonl','MSF_ARCHIVE_088.jsonl','MSF_SYSTEM_ENC_055.jsonl','MSF_SYSTEM_ENC_072.jsonl','MSF_SYSTEM_ENC_088.jsonl','MSF_SYSTEM_GDAT0.jsonl'),
}
DATASETS = {'ms2':'MS2_SWITCH_WESTERN','finale':'MSF_SWITCH_WESTERN'}
MAP_ROOT = Path(__file__).resolve().parents[4] / 'toolchain' / 'switch_port' / 'maps'


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def text_digest(s: str) -> str:
    return digest(s.encode('utf-8'))


def rows(path):
    with Path(path).open('r',encoding='utf-8') as f:
        for ln,line in enumerate(f,1):
            if line.strip():
                try: yield json.loads(line)
                except ValueError as e: raise ValueError(f'{path}:{ln}: {e}') from e


def load_links(game, map_root=MAP_ROOT):
    man = json.loads((map_root/'manifest.json').read_text(encoding='utf-8'))
    if man.get('schema_version') != 1 or man.get('release_ready') is not False:
        raise ValueError('Invalid/non-research structural map manifest')
    entry=man['games'][game]; mapped={}
    path=map_root/entry['map_file']
    if digest(path.read_bytes())!=entry['map_sha256']:
        raise ValueError(f'{game}: stable map SHA mismatch')
    with gzip.open(path,'rt',encoding='utf-8') as f:
        for line in f:
            if not line.strip():continue
            rec=json.loads(line)
            key=rec['target_key']
            if (key in mapped or not key.startswith(DATASETS[game]+'/') or
                    rec['pc_file'] not in FILES[game] or
                    not (isinstance(rec['field_ordinal'],int) and rec['field_ordinal']>=0) or
                    len(rec['old_text_sha256'])!=64):
                raise ValueError(f'{game}: duplicate/malformed source mapping: {key}')
            mapped[key]=rec
    if len(mapped)!=entry['mapped_fields']:
        raise ValueError(f'{game}: map entry count mismatch')
    ids={(v['pc_file'],v['pc_record_id'],v['field_ordinal']) for v in mapped.values()}
    if len(ids)!=len(mapped):
        raise ValueError(f'{game}: two targets for one PC field')
    return entry,mapped


def load_pc(root,game,needed):
    pc={}; digests={}
    for name in FILES[game]:
        file=root/'translations'/name
        h=hashlib.sha256()
        with file.open('rb') as f:
            for buf in iter(lambda:f.read(1024*1024),b''):h.update(buf)
        digests[name]=h.hexdigest()
        for rec in rows(file):
            key=(name,rec['id'])
            if key in pc:raise ValueError(f'duplicate PC id {key}')
            if key in needed:pc[key]=rec
    if set(pc)!=needed:
        raise ValueError(f'{game}: missing {len(needed-set(pc))} canonical PC records')
    return pc,digests


def stage(game, root, native_path, out, map_root=MAP_ROOT):
    """Atomic local staging: fail closed if original Switch extraction drifts."""
    root=Path(root);native_path=Path(native_path);out=Path(out)
    entry,mappings=load_links(game,map_root)
    sha=hashlib.sha256()
    with native_path.open('rb') as f:
        for block in iter(lambda:f.read(1024*1024),b''):sha.update(block)
    if sha.hexdigest()!=entry['native_records_sha256']:
        raise ValueError(f'{game}: pristine Western native records SHA mismatch')
    needed={(m['pc_file'],m['pc_record_id']) for m in mappings.values()}
    pc,translation_hashes=load_pc(root,game,needed)
    targets={}
    for key,m in mappings.items():
        rec=pc[(m['pc_file'],m['pc_record_id'])]
        fields=rec['fields']; ordinal=m['field_ordinal']
        if ordinal>=len(fields):raise ValueError(f'{key}: PC field index out of range')
        field=fields[ordinal]
        if not (isinstance(field.get('LOCALIZED'),str) and isinstance(field.get('EN'),str) and
                str(field.get('mapping_status','')).startswith('EXACT_')):
            raise ValueError(f'{key}: PC field no longer exact/valid')
        if text_digest(field['LOCALIZED'])!=m['old_text_sha256']:
            raise ValueError(f'{key}: original localized source drift')
        if '\x00' in field['EN']:
            raise ValueError(f'{key}: embedded NUL in canonical English text')
        targets[key]=field['EN']
    out.parent.mkdir(parents=True,exist_ok=True)
    tmp=out.with_name(out.name+'.tmp')
    if tmp.exists():raise ValueError(f'previous interrupted temporary stage exists: {tmp}')
    seen=set(); counts=Counter(); archives=set()
    try:
        with tmp.open('w',encoding='utf-8') as fout:
            for native in rows(native_path):
                key=native.get('record_key')
                if key not in targets:continue
                if key in seen:raise ValueError(f'{game}: duplicate Western record key {key}')
                seen.add(key)
                m=mappings[key]
                old=native.get('text'); new=targets[key]
                if (native.get('source_dataset')!=DATASETS[game] or
                    native.get('decode_errors') or not isinstance(old,str) or
                    text_digest(old)!=m['old_text_sha256']):
                    raise ValueError(f'{key}: native original text identity drift')
                enc=native.get('encoding')
                if native.get('repack_metadata',{}).get('blob_type') not in ('gnl','enc_row','bin_string'):
                    raise ValueError(f'{key}: unsupported native edit chain')
                try:
                    old_raw=bytes.fromhex(native['raw_hex'])
                    if old.encode(enc,'strict')!=old_raw:
                        raise ValueError(f'{key}: source raw-byte parity failure')
                    new_raw=new.encode(enc,'strict')
                except (UnicodeError,ValueError,LookupError,TypeError,KeyError) as ex:
                    raise ValueError(f'{key}: native strict encoding failure: {ex}') from ex
                if new==old:
                    counts['mapped_unchanged']+=1;continue
                if not new_raw or b'\x00' in new_raw:
                    raise ValueError(f'{key}: invalid encoded target (NUL or empty)')
                native['text']=new
                fout.write(json.dumps(native,sort_keys=True,ensure_ascii=False)+'\n')
                counts['changed']+=1
                archives.add(int(native['archive_number']))
            if seen!=mappings.keys():
                raise ValueError(f'{game}: {len(mappings.keys()-seen)} mapped Switch fields missing from extraction')
            fout.flush();os.fsync(fout.fileno())
        os.replace(tmp,out)
    except BaseException:
        tmp.unlink(missing_ok=True)
        raise
    report={'schema_version':1,'game':game,'status':'NATIVE_CANDIDATE_STAGED_NOT_RELEASE_READY',
            'source_records_sha256':entry['native_records_sha256'],
            'mapped_fields':len(mappings),'edited_fields':counts['changed'],
            'unchanged_mapped_fields':counts['mapped_unchanged'],
            'edited_archives':sorted(archives),
            'translation_inputs_sha256':translation_hashes,
            'alignment_sha256':entry['map_sha256'],
            'generated_edits_sha256':digest(out.read_bytes()),
            'warning':'Unmatched Switch records, format fit and native rebuild/runtime are not yet approved.'}
    report_path=out.with_suffix('.report.json')
    report_path.write_text(json.dumps(report,indent=2,sort_keys=True)+'\n',encoding='utf-8')
    return report


def main(argv=None):
    p=argparse.ArgumentParser(description='Research-only live canonical translation adapter; no TUI, no release')
    p.add_argument('--project-root',type=Path,required=True)
    p.add_argument('--game',choices=['ms2','finale','both'],default='both')
    p.add_argument('--native-root',type=Path)
    p.add_argument('--out-dir',type=Path)
    a=p.parse_args(argv)
    root=a.project_root.resolve()
    native_root=a.native_root or root/'.local/switch_text_port'
    out_dir=a.out_dir or root/'.local/research/switch-live-stage'
    try:
        for g in ('ms2','finale') if a.game=='both' else (a.game,):
            rep=stage(g,root,native_root/g/'western-native-records.jsonl',out_dir/f'{g}-staged-native-candidates.jsonl')
            print(f"{g.upper()}: {rep['edited_fields']} live PC-derived edits across {len(rep['edited_archives'])} native archives; status=CANDIDATE")
    except (OSError,ValueError,KeyError) as exc:
        print(f'SWITCH LIVE STAGING BLOCKED: {exc}',file=sys.stderr)
        return 1
    return 0

if __name__=='__main__':raise SystemExit(main())
