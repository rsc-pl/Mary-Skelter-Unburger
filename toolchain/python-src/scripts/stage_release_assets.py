#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def copy_exact(src: Path, dst: Path, sha: str | None = None, size: int | None = None) -> dict:
    if not src.is_file():
        raise RuntimeError(f'missing asset input: {src}')
    got_size = src.stat().st_size
    got_sha = sha256_file(src)
    if size is not None and got_size != size:
        raise RuntimeError(f'asset size mismatch {src}: {got_size} != {size}')
    if sha is not None and got_sha.lower() != sha.lower():
        raise RuntimeError(f'asset SHA mismatch {src}: {got_sha} != {sha}')
    dst.parent.mkdir(parents=True, exist_ok=True)
    tmp = dst.with_name(dst.name + '.tmp')
    shutil.copy2(src, tmp)
    os.replace(tmp, dst)
    return {'size': got_size, 'sha256': got_sha}


def main() -> None:
    ap = argparse.ArgumentParser(description='Stage all Mary Skelter release payload objects for one .unburger-assets v3 container')
    ap.add_argument('--gdat', default=str(ROOT / '.local' / 'gdat_payload'))
    ap.add_argument('--movie-audio', default=str(ROOT / '.local' / 'movie_audio_cache'))
    ap.add_argument('--out', default=str(ROOT / '.local' / 'release_asset_stage'))
    args = ap.parse_args()
    gdat = Path(args.gdat).resolve()
    movie = Path(args.movie_audio).resolve()
    out = Path(args.out).resolve()
    if out == ROOT or ROOT not in out.parents:
        raise SystemExit('ERROR: --out must stay inside the project tree')
    shutil.rmtree(out, ignore_errors=True)
    objects = []

    gmanifest_path = gdat / 'GDAT_PATCH_MANIFEST.json'
    gmanifest = json.loads(gmanifest_path.read_text(encoding='utf-8'))
    copy_exact(gmanifest_path, out / 'inputs' / 'gdat' / 'GDAT_PATCH_MANIFEST.json')
    for archive in gmanifest.get('archives', []):
        for entry in archive.get('changed_entries', []):
            rel = Path(entry['blob_file'])
            src = gdat / rel
            dst_rel = Path('gdat') / rel
            info = copy_exact(src, out / 'objects' / dst_rel, size=int(entry['stored_bytes']))
            objects.append({
                'kind': 'gdat_entry', 'game': archive['game'], 'archive': archive['relative_path'],
                'entry_index': entry['index'], 'logical_path': dst_rel.as_posix(),
                'source_file': (out / 'objects' / dst_rel).relative_to(ROOT).as_posix(),
                **info,
            })

    movie_manifest_path = movie / 'MOVIE_AUDIO_PAYLOAD_INPUT.json'
    movie_manifest = json.loads(movie_manifest_path.read_text(encoding='utf-8'))
    declared_games = movie_manifest.get('movie_audio_games', ['finale'])
    if declared_games != ['finale']:
        raise RuntimeError(f'movie payload must be Finale-only, got movie_audio_games={declared_games!r}')
    copy_exact(movie_manifest_path, out / 'inputs' / 'movie' / 'MOVIE_AUDIO_PAYLOAD_INPUT.json')
    for obj in movie_manifest.get('objects', []):
        if obj.get('game') != 'finale':
            raise RuntimeError(f'non-Finale movie-audio release object rejected: {obj.get("game")!r}')
        src = ROOT / obj['file']
        suffix = src.suffix
        logical = Path('movie_audio') / obj['game'] / (Path(obj['target_movie']).stem + f'.a{obj["audio_ordinal"]}{suffix}')
        info = copy_exact(src, out / 'objects' / logical, sha=obj['sha256'], size=int(obj['size']))
        objects.append({
            'kind': 'movie_jp_audio', 'game': obj['game'], 'target_movie': obj['target_movie'],
            'audio_ordinal': obj['audio_ordinal'], 'codec': obj.get('codec'),
            'source_movie_en_sha256': obj['source_movie_en_sha256'],
            'source_movie_jp_sha256': obj['source_movie_jp_sha256'],
            'decoded_pcm_sha256_en': obj['decoded_pcm_sha256_en'],
            'decoded_pcm_sha256_jp': obj['decoded_pcm_sha256_jp'],
            'logical_path': logical.as_posix(),
            'source_file': (out / 'objects' / logical).relative_to(ROOT).as_posix(),
            **info,
        })

    objects.sort(key=lambda x: (x['kind'], x.get('game',''), x['logical_path']))
    total = sum(int(o['size']) for o in objects)
    stage_manifest = {
        'schema_version': 1,
        'container_format': '.unburger-assets v3',
        'container_count': 1,
        'runtime_external_dependencies': [],
        'movie_audio_games': ['finale'],
        'ms2_movie_policy': 'preserve MOVIE_EN; no MS2 movie-audio release objects; purge the whole verified MOVIE_EN_SUB directory plus Voice_EN.bra when selected',
        'objects': objects,
        'total_plain_bytes': total,
    }
    out.mkdir(parents=True, exist_ok=True)
    (out / 'ASSET_STAGE_MANIFEST.json').write_text(json.dumps(stage_manifest, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')
    print(f'STAGED OBJECTS: {len(objects)}')
    print(f'PLAIN BYTES:    {total}')
    print(f'MANIFEST:       {out / "ASSET_STAGE_MANIFEST.json"}')
    print('Target: one encrypted/compressed .unburger-assets v3 container; no secondary release asset archive.')


if __name__ == '__main__':
    main()
