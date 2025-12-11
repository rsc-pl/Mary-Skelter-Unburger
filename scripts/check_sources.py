#!/usr/bin/env python3
from pathlib import Path
import argparse,csv,hashlib,sys,json,re
ap=argparse.ArgumentParser()
ap.add_argument('--manifest',required=True)
ap.add_argument('--ms2',required=True)
ap.add_argument('--msf',required=True)
ap.add_argument('--change-manifest',help='If supplied, hash only archives containing actual edits')
a=ap.parse_args()
roots={'MS2_PC_LOCALIZED':Path(a.ms2),'MSF_PC_LOCALIZED':Path(a.msf),'MSF_PC_SYSTEM':Path(a.msf)}
wanted=None
if a.change_manifest:
    x=json.load(open(a.change_manifest,encoding='utf-8')); wanted=set()
    for ds,v in x.items():
        for ch in v.get('changes',[]):
            m=re.search(r'/a(\d{3})/',ch['record_key'])
            if m: wanted.add((ds,f'GxArchivedFile{int(m.group(1)):03d}.dat'))
    # MSF_PC_SYSTEM and MSF_PC_LOCALIZED use the same physical master; expected hash rows are stored under localized.
    normalized=set()
    for ds,fn in wanted:
        normalized.add(('MSF_PC_LOCALIZED' if ds=='MSF_PC_SYSTEM' else ds,fn))
    wanted=normalized
bad=[];checked=0
with open(a.manifest,encoding='utf-8',newline='') as f:
    r=csv.DictReader(f,delimiter='\t')
    for row in r:
        key=(row['dataset'],row['file'])
        if wanted is not None and key not in wanted: continue
        root=roots.get(row['dataset'])
        if root is None: continue
        p=root/row['file']
        if not p.exists(): bad.append(f'MISSING {p}'); continue
        size=p.stat().st_size
        if size!=int(row['bytes']): bad.append(f'SIZE {p}: {size} != {row["bytes"]}'); continue
        h=hashlib.sha256()
        with p.open('rb') as fp:
            for b in iter(lambda:fp.read(8*1024*1024),b''): h.update(b)
        got=h.hexdigest()
        if got!=row['sha256']: bad.append(f'SHA256 {p}: {got} != {row["sha256"]}')
        checked+=1
if wanted is not None and checked != len(wanted):
    have_msg=f'expected_to_check={len(wanted)} checked={checked}'
else: have_msg=f'checked={checked}'
print(f'{have_msg} mismatches={len(bad)}')
for x in bad: print(x,file=sys.stderr)
raise SystemExit(1 if bad or (wanted is not None and checked!=len(wanted)) else 0)
