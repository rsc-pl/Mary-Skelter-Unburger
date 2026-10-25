#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def run(cmd: list[str], *, text: bool = True) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=text)


def require_tool(name: str) -> None:
    if shutil.which(name) is None:
        raise SystemExit(f'ERROR: maintainer tool not found in PATH: {name}')


def copy_cached(src: Path, dst: Path) -> None:
    if src.resolve() == dst.resolve():
        return
    dst.parent.mkdir(parents=True, exist_ok=True)
    if dst.is_file() and dst.stat().st_size == src.stat().st_size and sha256_file(dst) == sha256_file(src):
        return
    tmp = dst.with_name(dst.name + '.tmp')
    tmp.unlink(missing_ok=True)
    cp = shutil.which('cp')
    if cp:
        p = subprocess.run([cp, '--reflink=auto', '--preserve=mode,timestamps', '--', str(src), str(tmp)],
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        if p.returncode != 0:
            shutil.copy2(src, tmp)
    else:
        shutil.copy2(src, tmp)
    os.replace(tmp, dst)


def ffprobe_streams(path: Path) -> list[dict]:
    p = run([
        'ffprobe', '-v', 'error', '-show_streams',
        '-show_entries',
        'stream=index,codec_type,codec_name,codec_tag_string,profile,sample_rate,channels,channel_layout,duration,bit_rate,width,height,pix_fmt,avg_frame_rate,r_frame_rate',
        '-of', 'json', str(path),
    ])
    return json.loads(p.stdout).get('streams', [])


def decoded_audio_hash(path: Path, ordinal: int) -> str:
    p = run([
        'ffmpeg', '-v', 'error', '-i', str(path), '-map', f'0:a:{ordinal}',
        '-vn', '-sn', '-dn', '-c:a', 'pcm_s16le', '-f', 'hash', '-hash', 'sha256', '-',
    ])
    line = p.stdout.strip()
    if '=' not in line:
        raise RuntimeError(f'unexpected ffmpeg hash output for {path}: {line!r}')
    return line.split('=', 1)[1].strip().lower()


def packet_hash(path: Path, selector: str) -> dict:
    p = run([
        'ffprobe', '-v', 'error', '-select_streams', selector,
        '-show_packets', '-show_entries', 'packet=size,data_hash', '-show_data_hash', 'sha256',
        '-of', 'compact=p=0:nk=0', str(path),
    ])
    h = hashlib.sha256()
    count = 0
    for line in p.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        h.update(line.encode('utf-8'))
        h.update(b'\n')
        count += 1
    return {'sha256': h.hexdigest(), 'packets': count}


def extract_audio(src: Path, ordinal: int, codec: str, dst_base: Path) -> Path:
    # Game movies currently use MP4-compatible audio; keep the original encoded packets.
    # Fall back to Matroska only if the codec cannot be written to M4A without transcoding.
    dst_base.parent.mkdir(parents=True, exist_ok=True)
    m4a = dst_base.with_suffix(f'.a{ordinal}.m4a')
    mka = dst_base.with_suffix(f'.a{ordinal}.mka')
    for p in (m4a, mka):
        p.unlink(missing_ok=True)
    cmd = [
        'ffmpeg', '-y', '-v', 'error', '-i', str(src), '-map', f'0:a:{ordinal}',
        '-vn', '-sn', '-dn', '-c:a', 'copy', '-map_metadata', '-1', str(m4a),
    ]
    result = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if result.returncode == 0:
        return m4a
    run([
        'ffmpeg', '-y', '-v', 'error', '-i', str(src), '-map', f'0:a:{ordinal}',
        '-vn', '-sn', '-dn', '-c:a', 'copy', '-map_metadata', '-1', str(mka),
    ])
    return mka


def slim_stream(s: dict) -> dict:
    keys = ('index', 'codec_type', 'codec_name', 'codec_tag_string', 'profile', 'sample_rate', 'channels',
            'channel_layout', 'duration', 'bit_rate', 'width', 'height', 'pix_fmt', 'avg_frame_rate', 'r_frame_rate')
    return {k: s[k] for k in keys if k in s and s[k] not in (None, '')}


@dataclass
class GameInput:
    key: str
    root: Path


def process_ms2(game: GameInput) -> dict:
    """Audit the real MS2 movie-folder split without creating release audio objects."""
    mw = game.root / 'masterWindows'
    preserve_dir = mw / 'MOVIE_EN'
    purge_dir = mw / 'MOVIE_EN_SUB'
    result = {
        'game': game.key,
        'mode': 'ms2_preserve_movie_en_purge_movie_en_sub',
        'root': str(game.root),
        'preserve_relative_path': 'masterWindows/MOVIE_EN',
        'purge_relative_path': 'masterWindows/MOVIE_EN_SUB',
        'voice_purge_relative_path': 'masterWindows/Voice_EN.bra',
        'movie_audio_release_objects': 0,
        'pairs': [],
        'purge_candidates': [],
    }
    if not preserve_dir.is_dir():
        result['status'] = 'movie_en_missing'
        return result
    if not purge_dir.is_dir():
        result['status'] = 'movie_en_sub_missing'
        return result

    preserve = {p.name: p for p in sorted(preserve_dir.glob('*.mp4'))}
    purge = {p.name: p for p in sorted(purge_dir.glob('*.mp4'))}
    result['status'] = 'ok'
    result['preserve_files'] = sorted(preserve)
    result['purge_files'] = sorted(purge)
    result['only_preserve'] = sorted(set(preserve) - set(purge))
    result['only_purge'] = sorted(set(purge) - set(preserve))

    for name, src in purge.items():
        result['purge_candidates'].append({
            'relative_path': f'masterWindows/MOVIE_EN_SUB/{name}',
            'size': src.stat().st_size,
            'sha256': sha256_file(src),
        })

    # Read-only machine-verifiable evidence for the three overlapping movie IDs.
    # We compare decoded audio directly from the installed files; MS2 movie files
    # are never copied into the movie source cache and never become release objects.
    for name in sorted(set(preserve) & set(purge)):
        en = preserve[name]
        sub = purge[name]
        print(f'[ms2] fingerprint MOVIE_EN vs MOVIE_EN_SUB {name}', flush=True)
        en_streams = ffprobe_streams(en)
        sub_streams = ffprobe_streams(sub)
        en_audio = [x for x in en_streams if x.get('codec_type') == 'audio']
        sub_audio = [x for x in sub_streams if x.get('codec_type') == 'audio']
        if len(en_audio) != len(sub_audio):
            raise RuntimeError(f'ms2/{name}: audio stream count differs MOVIE_EN={len(en_audio)} MOVIE_EN_SUB={len(sub_audio)}')
        pair = {
            'name': name,
            'movie_en_relative_path': f'masterWindows/MOVIE_EN/{name}',
            'movie_en_sub_relative_path': f'masterWindows/MOVIE_EN_SUB/{name}',
            'movie_en_sha256': sha256_file(en),
            'movie_en_sub_sha256': sha256_file(sub),
            'movie_en_size': en.stat().st_size,
            'movie_en_sub_size': sub.stat().st_size,
            'movie_en_streams': [slim_stream(x) for x in en_streams],
            'movie_en_sub_streams': [slim_stream(x) for x in sub_streams],
            'audio': [],
        }
        for ordinal in range(len(en_audio)):
            a = decoded_audio_hash(en, ordinal)
            b = decoded_audio_hash(sub, ordinal)
            pair['audio'].append({
                'ordinal': ordinal,
                'decoded_pcm_sha256_movie_en': a,
                'decoded_pcm_sha256_movie_en_sub': b,
                'decoded_audio_equal': a == b,
            })
        result['pairs'].append(pair)
    return result


def process_finale(game: GameInput, source_cache: Path, audio_cache: Path, payload_inputs: list[dict], *, en_dir: Path | None = None, jp_dir: Path | None = None) -> dict:
    """Compare Finale MOVIE_EN against MOVIE_JP and retain only differing JP audio."""
    mw = game.root / 'masterWindows'
    en_dir = en_dir.resolve() if en_dir is not None else mw / 'MOVIE_EN'
    jp_dir = jp_dir.resolve() if jp_dir is not None else mw / 'MOVIE_JP'
    result = {
        'game': game.key,
        'mode': 'finale_movie_en_vs_movie_jp',
        'root': str(game.root),
        'movie_en_source': str(en_dir),
        'movie_jp_source': str(jp_dir),
        'pairs': [],
        'only_en': [],
        'only_jp': [],
    }
    if not en_dir.is_dir():
        result['status'] = 'no_movie_en'
        return result
    if not jp_dir.is_dir():
        result['status'] = 'movie_jp_missing'
        return result

    result['status'] = 'ok'
    cached_en = source_cache / game.key / 'MOVIE_EN'
    cached_jp = source_cache / game.key / 'MOVIE_JP'
    en_files = {p.name: p for p in sorted(en_dir.glob('*.mp4'))}
    jp_files = {p.name: p for p in sorted(jp_dir.glob('*.mp4'))}
    result['only_en'] = sorted(set(en_files) - set(jp_files))
    result['only_jp'] = sorted(set(jp_files) - set(en_files))
    if result['only_en']:
        raise RuntimeError(f'finale: MOVIE_EN files without MOVIE_JP counterpart: {result["only_en"]}')

    for name, src in en_files.items():
        copy_cached(src, cached_en / name)
    for name, src in jp_files.items():
        copy_cached(src, cached_jp / name)

    for name in sorted(set(en_files) & set(jp_files)):
        en = cached_en / name
        jp = cached_jp / name
        print(f'[finale] compare {name}', flush=True)
        en_streams = ffprobe_streams(en)
        jp_streams = ffprobe_streams(jp)
        en_audio = [s for s in en_streams if s.get('codec_type') == 'audio']
        jp_audio = [s for s in jp_streams if s.get('codec_type') == 'audio']
        en_video = [s for s in en_streams if s.get('codec_type') == 'video']
        jp_video = [s for s in jp_streams if s.get('codec_type') == 'video']
        if len(en_audio) != len(jp_audio):
            raise RuntimeError(f'finale/{name}: audio stream count differs EN={len(en_audio)} JP={len(jp_audio)}')

        pair = {
            'name': name,
            'en_relative_path': f'masterWindows/MOVIE_EN/{name}',
            'jp_relative_path': f'masterWindows/MOVIE_JP/{name}',
            'en_sha256': sha256_file(en),
            'jp_sha256': sha256_file(jp),
            'en_size': en.stat().st_size,
            'jp_size': jp.stat().st_size,
            'en_streams': [slim_stream(s) for s in en_streams],
            'jp_streams': [slim_stream(s) for s in jp_streams],
            'audio': [],
        }
        if en_video and jp_video:
            ev = packet_hash(en, 'v:0')
            jv = packet_hash(jp, 'v:0')
            pair['video_packet_hash_en'] = ev
            pair['video_packet_hash_jp'] = jv
            pair['video_packet_identical'] = ev == jv

        stem = Path(name).stem
        for ordinal, (es, js) in enumerate(zip(en_audio, jp_audio)):
            en_pcm = decoded_audio_hash(en, ordinal)
            jp_pcm = decoded_audio_hash(jp, ordinal)
            cmp_dir = audio_cache / 'comparison' / game.key / stem
            en_extract = extract_audio(en, ordinal, str(es.get('codec_name', '')), cmp_dir / 'en')
            jp_extract = extract_audio(jp, ordinal, str(js.get('codec_name', '')), cmp_dir / 'jp')
            equal = en_pcm == jp_pcm
            row = {
                'ordinal': ordinal,
                'codec_en': es.get('codec_name'),
                'codec_jp': js.get('codec_name'),
                'decoded_pcm_sha256_en': en_pcm,
                'decoded_pcm_sha256_jp': jp_pcm,
                'decoded_audio_equal': equal,
                'comparison_en_file': en_extract.relative_to(ROOT).as_posix(),
                'comparison_jp_file': jp_extract.relative_to(ROOT).as_posix(),
                'comparison_en_sha256': sha256_file(en_extract),
                'comparison_jp_sha256': sha256_file(jp_extract),
            }
            if not equal:
                payload_dir = audio_cache / 'payload' / game.key
                payload_dir.mkdir(parents=True, exist_ok=True)
                payload_file = payload_dir / f'{stem}.a{ordinal}{jp_extract.suffix}'
                copy_cached(jp_extract, payload_file)
                row['payload_file'] = payload_file.relative_to(ROOT).as_posix()
                row['payload_size'] = payload_file.stat().st_size
                row['payload_sha256'] = sha256_file(payload_file)
                payload_inputs.append({
                    'kind': 'movie_jp_audio',
                    'game': 'finale',
                    'target_movie': pair['en_relative_path'],
                    'audio_ordinal': ordinal,
                    'source_movie_en_sha256': pair['en_sha256'],
                    'source_movie_jp_sha256': pair['jp_sha256'],
                    'decoded_pcm_sha256_en': en_pcm,
                    'decoded_pcm_sha256_jp': jp_pcm,
                    'codec': js.get('codec_name'),
                    'file': row['payload_file'],
                    'size': row['payload_size'],
                    'sha256': row['payload_sha256'],
                })
            pair['audio'].append(row)
        result['pairs'].append(pair)
    return result


def main() -> None:
    ap = argparse.ArgumentParser(description='Prepare Mary Skelter movie evidence and Finale-only JP audio release payloads')
    ap.add_argument('--ms2-root')
    ap.add_argument('--finale-root')
    ap.add_argument('--finale-en-dir', help='explicit Finale MOVIE_EN source directory (used by maintainer wizard cache)')
    ap.add_argument('--finale-jp-dir', help='explicit Finale MOVIE_JP source directory (used by maintainer wizard cache)')
    ap.add_argument('--source-cache', default=str(ROOT / '.local' / 'movie_audio_source_cache'))
    ap.add_argument('--audio-cache', default=str(ROOT / '.local' / 'movie_audio_cache'))
    ap.add_argument('--require-complete', action='store_true', help='exit nonzero after writing reports unless every requested game completed')
    args = ap.parse_args()
    require_tool('ffmpeg')
    require_tool('ffprobe')
    if not args.ms2_root and not args.finale_root:
        raise SystemExit('ERROR: pass --ms2-root and/or --finale-root')

    source_cache = Path(args.source_cache).resolve()
    audio_cache = Path(args.audio_cache).resolve()
    source_cache.mkdir(parents=True, exist_ok=True)
    # MS2 no longer has a movie-audio source-cache role; discard stale derived copies.
    shutil.rmtree(source_cache / 'ms2', ignore_errors=True)
    # Rebuild derived comparison/payload output so obsolete objects cannot survive.
    shutil.rmtree(audio_cache, ignore_errors=True)
    audio_cache.mkdir(parents=True, exist_ok=True)
    payload_inputs: list[dict] = []
    results = []
    if args.ms2_root:
        results.append(process_ms2(GameInput('ms2', Path(args.ms2_root).resolve())))
    if args.finale_root:
        results.append(process_finale(
            GameInput('finale', Path(args.finale_root).resolve()), source_cache, audio_cache, payload_inputs,
            en_dir=Path(args.finale_en_dir) if args.finale_en_dir else None,
            jp_dir=Path(args.finale_jp_dir) if args.finale_jp_dir else None,
        ))

    report = {
        'schema_version': 3,
        'created_utc': datetime.now(timezone.utc).isoformat(),
        'source_cache': source_cache.relative_to(ROOT).as_posix() if source_cache.is_relative_to(ROOT) else str(source_cache),
        'audio_cache': audio_cache.relative_to(ROOT).as_posix() if audio_cache.is_relative_to(ROOT) else str(audio_cache),
        'policy': {
            'ms2': 'preserve MOVIE_EN; audit/purge MOVIE_EN_SUB; never require MOVIE_JP; never stage MS2 movie audio',
            'finale': 'compare MOVIE_EN with MOVIE_JP; stage only differing JP audio for native-Go remux',
        },
        'games': results,
    }
    (audio_cache / 'MOVIE_AUDIO_AUDIT.json').write_text(json.dumps(report, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')
    asset_manifest = {
        'schema_version': 2,
        'container_target': '.unburger-assets v3',
        'movie_audio_games': ['finale'],
        'objects': payload_inputs,
    }
    (audio_cache / 'MOVIE_AUDIO_PAYLOAD_INPUT.json').write_text(json.dumps(asset_manifest, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')

    finale_results = [g for g in results if g['game'] == 'finale']
    same = different = payload_bytes = 0
    for game in finale_results:
        for pair in game.get('pairs', []):
            for audio in pair.get('audio', []):
                if audio['decoded_audio_equal']:
                    same += 1
                else:
                    different += 1
                    payload_bytes += audio['payload_size']
    ms2 = next((g for g in results if g['game'] == 'ms2'), None)
    finale = next((g for g in results if g['game'] == 'finale'), None)
    complete = True
    if ms2 is not None:
        complete = complete and ms2.get('status') == 'ok'
    if finale is not None:
        complete = complete and finale.get('status') == 'ok' and not finale.get('only_en')
    summary = {
        'schema_version': 2,
        'ms2_status': ms2.get('status') if ms2 else 'not_requested',
        'ms2_movie_audio_payload_objects': 0,
        'ms2_movie_en_sub_purge_candidates': len(ms2.get('purge_candidates', [])) if ms2 else 0,
        'finale_status': finale.get('status') if finale else 'not_requested',
        'finale_movie_pairs': len(finale.get('pairs', [])) if finale else 0,
        'finale_audio_streams_identical': same,
        'finale_audio_streams_different': different,
        'movie_audio_payload_objects': len(payload_inputs),
        'movie_audio_payload_bytes': payload_bytes,
        'complete': complete,
    }
    (audio_cache / 'SUMMARY.json').write_text(json.dumps(summary, indent=2) + '\n', encoding='utf-8')
    (audio_cache / 'README_REPRODUCIBILITY.md').write_text(
        '# Movie audio source/cache reproducibility\n\n'
        'This is maintainer/build documentation, not an end-user installation step.\n\n'
        'MS2: preserve `masterWindows/MOVIE_EN`; `MOVIE_EN_SUB` is the known English-dub movie set in the '
        'project owner\'s inspected PC installation and is audited as a purge target together with `Voice_EN.bra`. '
        'Do not require `MOVIE_JP` and do not create MS2 movie-audio release objects.\n\n'
        'Finale: use an owned Steam install and temporarily select Japanese so Steam downloads '
        '`masterWindows/MOVIE_JP`. Run `scripts/prepare_movie_audio_assets.sh`; it caches Finale MOVIE_EN/MOVIE_JP, '
        'compares decoded audio, and places only differing Japanese audio in `payload/` for the single '
        '`.unburger-assets v3` release container. Do not publish full JP MP4 source files.\n\n'
        'The public end-user updater must remain self-contained and must never require Steam language switching, '
        'ffmpeg, ffprobe, Python, or a second private asset archive.\n',
        encoding='utf-8')

    print('\n=== MOVIE AUDIO CACHE SUMMARY ===')
    print(f'MS2 status:                {summary["ms2_status"]}')
    print(f'MS2 release audio objects: 0')
    print(f'MS2 MOVIE_EN_SUB targets:  {summary["ms2_movie_en_sub_purge_candidates"]}')
    print(f'Finale status:             {summary["finale_status"]}')
    print(f'Finale movie pairs:        {summary["finale_movie_pairs"]}')
    print(f'Finale audio identical:    {same}')
    print(f'Finale audio different:    {different}')
    print(f'Payload objects:           {len(payload_inputs)}')
    print(f'Payload bytes:             {payload_bytes}')
    print(f'Source cache:              {source_cache}')
    print(f'Audio cache:               {audio_cache}')
    print(f'Asset input manifest:      {audio_cache / "MOVIE_AUDIO_PAYLOAD_INPUT.json"}')
    print('Installed game files were not modified.')
    if args.require_complete and not complete:
        missing = []
        if ms2 is not None and ms2.get('status') != 'ok':
            missing.append(f"MS2={ms2.get('status')}")
        if finale is not None and (finale.get('status') != 'ok' or finale.get('only_en')):
            missing.append(f"Finale={finale.get('status')}")
        raise SystemExit('ERROR: requested movie discovery is incomplete: ' + ', '.join(missing))


if __name__ == '__main__':
    main()
