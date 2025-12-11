#!/usr/bin/env python3
from pathlib import Path
import json,os,shutil
root=Path(__file__).resolve().parents[1]
build=root/'.local'/'build'; dist=Path(os.environ.get('MARY_SKELTER_DIST', root/'.local'/'dist'))
manifest=json.loads((build/'change_manifest.json').read_text(encoding='utf-8'))
for ds,info in manifest.items():
    if info['changed_fields']<=0: continue
    game='MS2' if ds.startswith('MS2_') else 'MSF'
    src=build/'rebuilt'/ds/'masterWindows'
    rep_path=build/'reports'/f'{ds}.rebuild.json'
    if not src.is_dir() or not rep_path.exists(): raise SystemExit(f'missing rebuild for {ds}')
    rep=json.loads(rep_path.read_text(encoding='utf-8'))
    dest=dist/f'{game}_EN_FIDELITY_PATCH'/'masterWindows'; dest.mkdir(parents=True,exist_ok=True)
    copied=[]
    for a in rep.get('archives',[]):
        if a.get('byte_identical'): continue
        name=a['file']; p=src/name
        if not p.exists(): raise SystemExit(f'rebuild report names missing file: {p}')
        shutil.copy2(p,dest/name); copied.append(name)
    (dest.parent/'INSTALL.txt').write_text(
        'Back up the original game files first.\nCopy the files from masterWindows/ over the same names in the game masterWindows directory.\nOnly DATs changed by the verified rebuild are included.\n',encoding='utf-8')
    (dest.parent/'PATCH_MANIFEST.json').write_text(json.dumps({'dataset':ds,'changed_fields':info['changed_fields'],'files':copied},indent=2)+'\n',encoding='utf-8')
    print(game, 'patch files:', ', '.join(copied) if copied else '(none)')
