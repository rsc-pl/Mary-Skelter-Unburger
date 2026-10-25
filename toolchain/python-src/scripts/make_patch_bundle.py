#!/usr/bin/env python3
from pathlib import Path
import hashlib, json, os, shutil

ROOT=Path(__file__).resolve().parents[3]
BUILD=ROOT/'.local'/'build'
DIST=Path(os.environ.get('MARY_SKELTER_DIST', ROOT/'.local'/'dist'))


def game_for_dataset(ds: str) -> str:
    if ds.startswith('MS2_'):
        return 'MS2'
    if ds.startswith('MSF_'):
        return 'MSF'
    raise SystemExit(f'unsupported patch dataset {ds}')


def sha256_file(path: Path) -> str:
    h=hashlib.sha256()
    with path.open('rb') as f:
        for chunk in iter(lambda:f.read(1024*1024),b''):
            h.update(chunk)
    return h.hexdigest()


change_manifest=json.loads((BUILD/'change_manifest.json').read_text(encoding='utf-8'))

greport_path=BUILD/'reports'/'graphics.json'
greport=None
graphics_by_dataset={}
if greport_path.is_file():
    greport=json.loads(greport_path.read_text(encoding='utf-8'))
    if greport.get('status')!='PASS':
        raise SystemExit('graphics report exists but is not PASS')
    for row in greport.get('results',[]):
        ds=row.get('dataset')
        archive=row.get('archive')
        if not ds or not isinstance(archive,int):
            raise SystemExit(f'invalid graphics report result: {row!r}')
        graphics_by_dataset.setdefault(ds,[]).append(row)

# A dataset belongs in the public bundle when either text changed or the
# canonical graphics stage changed/seeded an archive for it. This keeps
# graphics-only future surfaces packageable without inventing sidecar bundles.
datasets=set()
for ds,info in change_manifest.items():
    if int(info.get('changed_fields',0))>0:
        datasets.add(ds)
datasets.update(graphics_by_dataset)

for game in ('MS2','MSF'):
    game_datasets=sorted(ds for ds in datasets if game_for_dataset(ds)==game)
    if not game_datasets:
        continue

    bundle=DIST/f'{game}_EN_FIDELITY_PATCH'
    # make_patch_bundle is authoritative for DAT payloads; staging is normally
    # empty, but cleaning here prevents stale DATs if the script is rerun alone.
    if bundle.exists():
        shutil.rmtree(bundle)
    dest=bundle/'masterWindows'
    dest.mkdir(parents=True,exist_ok=True)

    copied_sources={}  # filename -> (dataset,path,sha256)
    dataset_rows=[]
    for ds in game_datasets:
        changed_fields=int(change_manifest.get(ds,{}).get('changed_fields',0))
        src=BUILD/'rebuilt'/ds/'masterWindows'
        archives=set()

        if changed_fields>0:
            rep_path=BUILD/'reports'/f'{ds}.rebuild.json'
            if not src.is_dir() or not rep_path.exists():
                raise SystemExit(f'missing rebuild for {ds}')
            rep=json.loads(rep_path.read_text(encoding='utf-8'))
            for a in rep.get('archives',[]):
                if not a.get('byte_identical'):
                    archives.add(int(a['archive_number']))

        for row in graphics_by_dataset.get(ds,[]):
            archives.add(int(row['archive']))

        if archives and not src.is_dir():
            raise SystemExit(f'missing rebuilt/graphics archive tree for {ds}: {src}')

        ds_files=[]
        for archive in sorted(archives):
            name=f'GxArchivedFile{archive:03d}.dat'
            p=src/name
            if not p.is_file():
                raise SystemExit(f'canonical build names missing file: {p}')
            digest=sha256_file(p)
            if name in copied_sources:
                prev_ds,prev_path,prev_digest=copied_sources[name]
                if prev_digest!=digest:
                    raise SystemExit(
                        f'conflicting canonical outputs for {name}: '
                        f'{prev_ds}={prev_digest} vs {ds}={digest}. '
                        'Overlapping dataset edits must be merged before bundling.'
                    )
            else:
                copied_sources[name]=(ds,p,digest)
            ds_files.append(name)

        dataset_rows.append({
            'dataset':ds,
            'changed_fields':changed_fields,
            'files':ds_files,
        })

    for name,(_ds,p,_digest) in sorted(copied_sources.items()):
        shutil.copy2(p,dest/name)

    files=sorted(copied_sources)
    total_changed=sum(row['changed_fields'] for row in dataset_rows)
    # Keep the old top-level dataset/changed_fields/files keys for consumers and
    # humans, while datasets[] makes multi-dataset MSF bundles unambiguous.
    payload={
        'dataset':dataset_rows[0]['dataset'] if len(dataset_rows)==1 else ' + '.join(row['dataset'] for row in dataset_rows),
        'changed_fields':total_changed,
        'files':files,
        'datasets':dataset_rows,
    }

    game_graphics=[row for ds in game_datasets for row in graphics_by_dataset.get(ds,[])]
    install=(
        'Back up the original game files first.\n'
        'Copy the files from masterWindows/ over the same names in the game masterWindows directory.\n'
        'Only DATs changed by the verified canonical build are included.\n'
    )
    if game_graphics:
        payload['graphical_text_assets']=[row['id'] for row in game_graphics]
        install += 'Canonical graphical-text fixes listed in PATCH_MANIFEST.json are already integrated into these DATs.\n'

    (bundle/'INSTALL.txt').write_text(install,encoding='utf-8')
    (bundle/'PATCH_MANIFEST.json').write_text(json.dumps(payload,indent=2)+'\n',encoding='utf-8')
    print(game,'patch files:',', '.join(files) if files else '(none)')
