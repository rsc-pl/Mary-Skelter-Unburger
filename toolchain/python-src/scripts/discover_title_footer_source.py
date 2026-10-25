#!/usr/bin/env python3
"""Read-only discovery of the real screen-space title/footer resource.

Runtime acceptance proved that title_moji is a logo-local texture, so this tool
now searches the actual copyright/overlay namespace referenced by the pristine
executables.  It never patches source files or the project.
"""
from __future__ import annotations

import argparse
import hashlib
import io
import json
import re
import shutil
import struct
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'tools'))
from mskit.gdat import Gdat, GdatError  # noqa: E402
from mskit.cl3 import Cl3, Cl3Error, ENTRY_SIZE  # noqa: E402

OVERLAY_NAME_RE = re.compile(r'(copyright|overlay|legal|license)', re.I)
TEXT_NEEDLES = (
    '/hostapp/OVERLAY/copyright.png', '../../resource/OVERLAY_EN/copyright.png',
    'Ghostlight', 'IDEA FACTORY', 'COMPILE HEART', 'All rights reserved',
    'Licensed to and published', 'copyright',
)
ENCODINGS = ('utf-8', 'utf-16le', 'cp1252')
PNG_SIG = b'\x89PNG\r\n\x1a\n'
FAST_MAX_ARCHIVE = 128 * 1024 * 1024
MAX_CL3_TABLE = 16 * 1024 * 1024


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open('rb') as fp:
        for chunk in iter(lambda: fp.read(8 * 1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def safe_name(s: str) -> str:
    return re.sub(r'[^0-9A-Za-z._-]+', '_', s).strip('_') or 'unnamed'


def scan_needles(data: bytes) -> list[dict]:
    out: list[dict] = []
    seen = set()
    low = data.lower()
    for enc in ENCODINGS:
        for text in TEXT_NEEDLES:
            try:
                needle = text.encode(enc)
            except UnicodeEncodeError:
                continue
            pos = 0
            while True:
                off = data.find(needle, pos) if enc == 'utf-16le' else low.find(needle.lower(), pos)
                if off < 0:
                    break
                key = (enc, text.lower(), off)
                if key not in seen:
                    seen.add(key)
                    out.append({'encoding': enc, 'needle': text, 'offset': off})
                pos = off + 1
    return sorted(out, key=lambda x: (x['offset'], x['encoding'], x['needle']))


def ascii_context(data: bytes, off: int, radius: int = 180) -> str:
    lo, hi = max(0, off-radius), min(len(data), off+radius)
    text = ''.join(chr(b) if 32 <= b < 127 else ' ' for b in data[lo:hi])
    return re.sub(r'\s+', ' ', text).strip()[:340]


def parse_png_at(data: bytes, off: int) -> tuple[bytes, int, int] | None:
    if data[off:off+8] != PNG_SIG:
        return None
    p = off + 8
    width = height = None
    limit = min(len(data), off + 32 * 1024 * 1024)
    while p + 12 <= limit:
        n = struct.unpack_from('>I', data, p)[0]
        typ = data[p+4:p+8]
        end = p + 12 + n
        if end > limit:
            return None
        if typ == b'IHDR' and n >= 8:
            width, height = struct.unpack_from('>II', data, p+8)
        p = end
        if typ == b'IEND':
            if width is None or height is None:
                return None
            return data[off:p], int(width), int(height)
    return None



PE_SCN_MEM_EXECUTE = 0x20000000


def parse_pe_layout(data: bytes) -> dict | None:
    """Parse only the PE fields needed for section/file-offset/VA mapping."""
    if len(data) < 0x100 or data[:2] != b'MZ':
        return None
    try:
        pe_off = struct.unpack_from('<I', data, 0x3C)[0]
        if data[pe_off:pe_off+4] != b'PE\0\0':
            return None
        coff = pe_off + 4
        machine, nsec, _, _, _, opt_size, _ = struct.unpack_from('<HHIIIHH', data, coff)
        opt = coff + 20
        magic = struct.unpack_from('<H', data, opt)[0]
        if magic == 0x20B:
            image_base = struct.unpack_from('<Q', data, opt + 24)[0]
        elif magic == 0x10B:
            image_base = struct.unpack_from('<I', data, opt + 28)[0]
        else:
            return None
        sec_off = opt + opt_size
        sections = []
        for i in range(nsec):
            rec = sec_off + i * 40
            if rec + 40 > len(data):
                return None
            name = data[rec:rec+8].split(b'\0', 1)[0].decode('ascii', 'replace')
            vsize, va, raw_size, raw_ptr = struct.unpack_from('<IIII', data, rec + 8)
            characteristics = struct.unpack_from('<I', data, rec + 36)[0]
            sections.append({
                'name': name, 'virtual_size': vsize, 'virtual_address': va,
                'raw_size': raw_size, 'raw_pointer': raw_ptr,
                'characteristics': characteristics,
                'executable': bool(characteristics & PE_SCN_MEM_EXECUTE),
            })
        return {'machine': machine, 'image_base': image_base, 'sections': sections}
    except (struct.error, IndexError):
        return None


def pe_locate_file_offset(pe: dict | None, off: int) -> dict | None:
    if not pe:
        return None
    for sec in pe['sections']:
        start = sec['raw_pointer']; end = start + sec['raw_size']
        if start <= off < end:
            rva = sec['virtual_address'] + (off - start)
            return {'section': sec['name'], 'rva': rva, 'va': pe['image_base'] + rva,
                    'section_executable': sec['executable']}
    return None


def pe_va_to_file_offset(pe: dict | None, va: int) -> int | None:
    if not pe:
        return None
    rva = va - pe['image_base']
    for sec in pe['sections']:
        span = max(sec['virtual_size'], sec['raw_size'])
        if sec['virtual_address'] <= rva < sec['virtual_address'] + span:
            delta = rva - sec['virtual_address']
            if delta >= sec['raw_size']:
                return None
            return sec['raw_pointer'] + delta
    return None


def png_visual_metrics(blob: bytes) -> dict:
    """Small structural metrics; useful to distinguish sparse overlays from opaque PE artwork."""
    try:
        from PIL import Image
        with Image.open(io.BytesIO(blob)) as im:
            rgba = im.convert('RGBA')
            alpha = rgba.getchannel('A')
            bbox = alpha.getbbox()
            hist = alpha.histogram()
            total = rgba.width * rgba.height
            transparent = hist[0] if hist else 0
            opaque = hist[255] if len(hist) > 255 else 0
            nonzero = total - transparent
            return {
                'mode': im.mode,
                'alpha_bbox': list(bbox) if bbox else None,
                'alpha_nonzero_fraction': round(nonzero / total, 6) if total else 0.0,
                'alpha_opaque_fraction': round(opaque / total, 6) if total else 0.0,
            }
    except Exception as exc:
        return {'analysis_error': str(exc)}


def _rip_target(data: bytes, off: int, va: int) -> tuple[str, int, int] | None:
    """Recognize common x64 RIP-relative loads/LEAs used for string references."""
    p = off
    # Optional one-byte REX prefix.
    if p < len(data) and 0x40 <= data[p] <= 0x4F:
        p += 1
    if p + 6 > len(data):
        return None
    op = data[p]
    if op not in (0x8D, 0x8B, 0x89):
        return None
    modrm = data[p+1]
    if (modrm & 0xC7) != 0x05:  # mod=00, r/m=101 => RIP+disp32
        return None
    disp = struct.unpack_from('<i', data, p+2)[0]
    length = (p - off) + 6
    target = va + length + disp
    kind = {0x8D: 'lea-rip', 0x8B: 'mov-load-rip', 0x89: 'mov-store-rip'}[op]
    return kind, target, length


def find_rip_xrefs(data: bytes, pe: dict | None, target_va: int) -> list[dict]:
    out = []
    if not pe:
        return out
    for sec in pe['sections']:
        if not sec['executable'] or not sec['raw_size']:
            continue
        start = sec['raw_pointer']; end = min(len(data), start + sec['raw_size'])
        base_va = pe['image_base'] + sec['virtual_address']
        for off in range(start, max(start, end - 6)):
            ins_va = base_va + (off - start)
            got = _rip_target(data, off, ins_va)
            if got and got[1] == target_va:
                kind, _, length = got
                out.append({'file_offset': off, 'va': ins_va, 'section': sec['name'],
                            'kind': kind, 'length': length,
                            'bytes': data[off:off+length].hex(' ')})
    return out


def find_pointer_slots(data: bytes, pe: dict | None, target_va: int) -> list[dict]:
    """Find data slots containing the string VA/RVA, then code xrefs to those slots."""
    if not pe:
        return []
    pats = [(struct.pack('<Q', target_va), 'va64')]
    rva = target_va - pe['image_base']
    if 0 <= rva <= 0xFFFFFFFF:
        pats.append((struct.pack('<I', rva), 'rva32'))
    found = []
    seen = set()
    for sec in pe['sections']:
        if sec['executable'] or not sec['raw_size']:
            continue
        blob = data[sec['raw_pointer']:sec['raw_pointer']+sec['raw_size']]
        for pat, kind in pats:
            pos = 0
            while True:
                i = blob.find(pat, pos)
                if i < 0: break
                off = sec['raw_pointer'] + i
                loc = pe_locate_file_offset(pe, off)
                if loc and (loc['va'], kind) not in seen:
                    seen.add((loc['va'], kind))
                    xrefs = find_rip_xrefs(data, pe, loc['va'])
                    found.append({'file_offset': off, 'va': loc['va'], 'section': sec['name'],
                                  'encoding': kind, 'code_xrefs': xrefs})
                pos = i + 1
    return found



def collect_rip_refs(data: bytes, pe: dict | None) -> list[dict]:
    """Collect recognized x64 RIP-relative references once for neighborhood tracing."""
    out = []
    if not pe:
        return out
    for sec in pe['sections']:
        if not sec['executable'] or not sec['raw_size']:
            continue
        start = sec['raw_pointer']; end = min(len(data), start + sec['raw_size'])
        base_va = pe['image_base'] + sec['virtual_address']
        for off in range(start, max(start, end - 6)):
            ins_va = base_va + (off - start)
            got = _rip_target(data, off, ins_va)
            if not got:
                continue
            kind, target, length = got
            out.append({'file_offset': off, 'va': ins_va, 'section': sec['name'],
                        'kind': kind, 'length': length, 'target_va': target,
                        'bytes': data[off:off+length].hex(' ')})
    return out


def find_relative_slots(data: bytes, pe: dict | None, target_va: int) -> list[dict]:
    """Find common signed rel32 data references resolving exactly to target_va.

    Some MSVC/static tables store image-relative relationships instead of absolute
    pointers.  Search both slot-relative and end-of-dword-relative forms; report
    evidence only and never treat a hit as a proven loader by itself.
    """
    if not pe:
        return []
    found = []
    seen = set()
    for sec in pe['sections']:
        if sec['executable'] or not sec['raw_size']:
            continue
        start = sec['raw_pointer']; end = min(len(data), start + sec['raw_size'])
        base_va = pe['image_base'] + sec['virtual_address']
        for off in range(start, max(start, end - 4)):
            disp = struct.unpack_from('<i', data, off)[0]
            slot_va = base_va + (off - start)
            for bias, kind in ((0, 'rel32-slot'), (4, 'rel32-next')):
                if slot_va + bias + disp != target_va:
                    continue
                key = (off, kind)
                if key in seen:
                    continue
                seen.add(key)
                loc = pe_locate_file_offset(pe, off)
                found.append({'file_offset': off, 'va': slot_va,
                              'section': loc['section'] if loc else sec['name'],
                              'encoding': kind, 'disp': disp,
                              'code_xrefs': find_rip_xrefs(data, pe, slot_va)})
    return found


def find_absolute_code_literals(data: bytes, pe: dict | None, target_va: int) -> list[dict]:
    """Find literal target VA/RVA bytes inside executable sections.

    This catches uncommon mov-imm/address materialization that the RIP decoder
    intentionally does not attempt to fully disassemble.
    """
    if not pe:
        return []
    pats = [(struct.pack('<Q', target_va), 'va64')]
    rva = target_va - pe['image_base']
    if 0 <= rva <= 0xFFFFFFFF:
        pats.append((struct.pack('<I', rva), 'rva32'))
    out = []
    seen = set()
    for sec in pe['sections']:
        if not sec['executable'] or not sec['raw_size']:
            continue
        blob = data[sec['raw_pointer']:sec['raw_pointer']+sec['raw_size']]
        for pat, kind in pats:
            pos = 0
            while True:
                i = blob.find(pat, pos)
                if i < 0:
                    break
                off = sec['raw_pointer'] + i
                key = (off, kind)
                if key not in seen:
                    seen.add(key)
                    va = pe['image_base'] + sec['virtual_address'] + i
                    out.append({'file_offset': off, 'va': va, 'section': sec['name'],
                                'encoding': kind, 'bytes': pat.hex(' ')})
                pos = i + 1
    return out


def ascii_strings_near(data: bytes, pe: dict | None, center_off: int, radius: int = 0x500) -> list[dict]:
    """Return NUL-terminated printable ASCII strings around a target file offset."""
    lo = max(0, center_off - radius); hi = min(len(data), center_off + radius)
    out = []
    i = lo
    while i < hi:
        if not (32 <= data[i] < 127):
            i += 1
            continue
        start = i
        while i < hi and 32 <= data[i] < 127:
            i += 1
        if i < len(data) and data[i] == 0 and i - start >= 3:
            raw = data[start:i]
            loc = pe_locate_file_offset(pe, start)
            item = {'file_offset': start, 'text': raw.decode('ascii', 'replace')}
            if loc:
                item.update(loc)
            out.append(item)
        i += 1
    return out[:120]


def find_nearby_rip_refs(data: bytes, pe: dict | None, target_va: int,
                         radius: int = 0x800) -> list[dict]:
    """Find code references into the same local rdata neighborhood as target_va."""
    out = []
    for xr in collect_rip_refs(data, pe):
        delta = xr['target_va'] - target_va
        if abs(delta) > radius:
            continue
        item = dict(xr)
        item['target_delta'] = delta
        toff = pe_va_to_file_offset(pe, xr['target_va'])
        if toff is not None:
            item['target_file_offset'] = toff
            item['target_context'] = ascii_context(data, toff, radius=90)
            loc = pe_locate_file_offset(pe, toff)
            if loc:
                item['target_section'] = loc['section']
        out.append(item)
    out.sort(key=lambda x: (abs(x['target_delta']), x['va']))
    return out[:160]

def objdump_excerpt(exe: Path, va: int, radius: int = 0x80) -> str | None:
    objdump = shutil.which('objdump')
    if not objdump:
        return None
    start = max(0, va - radius); stop = va + radius
    try:
        cp = subprocess.run([objdump, '-d', '-Mintel', f'--start-address=0x{start:x}',
                             f'--stop-address=0x{stop:x}', str(exe)],
                            capture_output=True, text=True, timeout=20, check=False)
    except Exception:
        return None
    if cp.returncode != 0:
        return None
    lines = [ln for ln in cp.stdout.splitlines() if ln.strip()]
    return '\n'.join(lines[-80:])


def trace_exact_path_xrefs(game: str, exe: Path, data: bytes, pe: dict | None,
                           hits: list[dict], out_root: Path, *, deep_trace: bool = False) -> list[dict]:
    traces = []
    out_dir = out_root / game.lower() / 'exe_xrefs'
    out_dir.mkdir(parents=True, exist_ok=True)
    seen_hits = set()
    for hit in hits:
        if 'copyright.png' not in hit['needle'].lower():
            continue
        hkey = (hit['offset'], hit['needle'].lower())
        if hkey in seen_hits:
            continue
        seen_hits.add(hkey)
        loc = pe_locate_file_offset(pe, hit['offset'])
        if not loc:
            continue
        direct = find_rip_xrefs(data, pe, loc['va'])
        pointers = find_pointer_slots(data, pe, loc['va'])
        item = {'needle': hit['needle'], 'encoding': hit['encoding'], 'file_offset': hit['offset'],
                **loc, 'direct_xrefs': direct, 'pointer_slots': pointers}
        if deep_trace:
            item['relative_slots'] = find_relative_slots(data, pe, loc['va'])
            item['absolute_code_literals'] = find_absolute_code_literals(data, pe, loc['va'])
            item['nearby_strings'] = ascii_strings_near(data, pe, hit['offset'])
            item['nearby_rip_refs'] = find_nearby_rip_refs(data, pe, loc['va'])
            deep_path = out_dir / f'deep_trace_{hit["offset"]:08x}.json'
            deep_path.write_text(json.dumps({
                'needle': hit['needle'], 'file_offset': hit['offset'], **loc,
                'relative_slots': item['relative_slots'],
                'absolute_code_literals': item['absolute_code_literals'],
                'nearby_strings': item['nearby_strings'],
                'nearby_rip_refs': item['nearby_rip_refs'],
            }, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
            item['deep_trace_report'] = str(deep_path)
        excerpts = []
        all_xrefs = list(direct)
        for ptr in pointers:
            all_xrefs.extend(ptr.get('code_xrefs', []))
        if deep_trace:
            for ptr in item.get('relative_slots', []):
                all_xrefs.extend(ptr.get('code_xrefs', []))
            all_xrefs.extend(item.get('nearby_rip_refs', [])[:24])
            all_xrefs.extend(item.get('absolute_code_literals', [])[:12])
        seen = set()
        for xr in all_xrefs:
            if xr['va'] in seen: continue
            seen.add(xr['va'])
            ex = objdump_excerpt(exe, xr['va'])
            if ex:
                dst = out_dir / f'xref_{xr["va"]:016x}.txt'
                dst.write_text(ex + '\n', encoding='utf-8')
                excerpts.append({'xref_va': xr['va'], 'output': str(dst)})
        item['disassembly'] = excerpts
        traces.append(item)
    return traces

def scan_embedded_pngs(game: str, exe: Path, out_root: Path, *, deep_trace: bool = False) -> dict:
    rep = {'game': game, 'path': str(exe)}
    if not exe.is_file():
        rep['missing'] = True
        return rep
    data = exe.read_bytes()
    pe = parse_pe_layout(data)
    out_dir = out_root / game.lower() / 'exe_png'
    out_dir.mkdir(parents=True, exist_ok=True)
    found = []
    pos = 0
    while True:
        off = data.find(PNG_SIG, pos)
        if off < 0:
            break
        parsed = parse_png_at(data, off)
        if parsed:
            blob, w, h = parsed
            dst = out_dir / f'embedded_{off:08x}_{w}x{h}.png'
            dst.write_bytes(blob)
            item = {'offset': off, 'width': w, 'height': h, 'bytes': len(blob),
                    'sha256': hashlib.sha256(blob).hexdigest(), 'output': str(dst),
                    'visual': png_visual_metrics(blob)}
            loc = pe_locate_file_offset(pe, off)
            if loc:
                item.update(loc)
            found.append(item)
            pos = off + len(blob)
        else:
            pos = off + 8
    hits = scan_needles(data)
    for hit in hits:
        hit['context'] = ascii_context(data, hit['offset'])
    traces = trace_exact_path_xrefs(game, exe, data, pe, hits, out_root, deep_trace=deep_trace)
    rep.update({'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest(),
                'pe': pe, 'needle_hits': hits, 'embedded_pngs': found,
                'copyright_path_traces': traces})
    return rep


def loose_overlay_candidates(game: str, master: Path, out_root: Path) -> dict:
    root = master.parent
    rep = {'game': game, 'root': str(root), 'files': []}
    if not root.is_dir():
        rep['missing'] = True
        return rep
    out_dir = out_root / game.lower() / 'loose'
    out_dir.mkdir(parents=True, exist_ok=True)
    for p in root.rglob('*'):
        if not p.is_file():
            continue
        rel = str(p.relative_to(root)).replace('\\', '/')
        if not OVERLAY_NAME_RE.search(rel) and p.name.lower() != 'copyright.png':
            continue
        item = {'path': str(p), 'relative': rel, 'bytes': p.stat().st_size}
        if p.stat().st_size <= 32 * 1024 * 1024:
            item['sha256'] = sha256_file(p)
            if p.suffix.lower() == '.png' or p.name.lower() == 'copyright.png':
                dst = out_dir / safe_name(rel)
                dst.write_bytes(p.read_bytes())
                item['copy'] = str(dst)
        rep['files'].append(item)
    return rep


def _cl3_names_prefix(g: Gdat, idx: int) -> tuple[list[dict], int] | None:
    """Read only enough decompressed bytes to recover a CL3 file-name table."""
    try:
        pre = g.read_payload(idx, want=0x2000)
    except Exception:
        return None
    pos = pre.find(b'CL3')
    if pos < 0 or pos + 0x18 > len(pre) or pre[pos+3:pos+4] not in (b'L', b'B'):
        return None
    endian = '<' if pre[pos+3:pos+4] == b'L' else '>'
    try:
        _, _, sections_count, sections_offset, _ = struct.unpack_from(endian+'IIIII', pre, pos+4)
    except struct.error:
        return None
    if not (0 < sections_count <= 64):
        return None
    sec_need = sections_offset + sections_count * 0x50
    if sec_need > len(pre):
        try:
            pre = g.read_payload(idx, want=sec_need)
        except Exception:
            return None
    coll_count = coll_off = None
    for i in range(sections_count):
        rec = sections_offset + i*0x50
        if rec + 0x50 > len(pre):
            return None
        name = pre[rec:rec+0x20].split(b'\0',1)[0]
        if name == b'FILE_COLLECTION':
            coll_count = struct.unpack_from(endian+'I', pre, rec+0x20)[0]
            coll_off = struct.unpack_from(endian+'I', pre, rec+0x28)[0]
            break
    if coll_count is None or coll_off is None or coll_count > 10000:
        return None
    need = coll_off + coll_count * ENTRY_SIZE
    if need <= 0 or need > MAX_CL3_TABLE:
        return None
    if need > len(pre):
        try:
            pre = g.read_payload(idx, want=need)
        except Exception:
            return None
    names = []
    for i in range(coll_count):
        rec = coll_off + i*ENTRY_SIZE
        if rec + ENTRY_SIZE > len(pre):
            return None
        raw = pre[rec:rec+0x200].split(b'\0',1)[0]
        try:
            name = raw.decode('utf-8')
        except UnicodeDecodeError:
            name = raw.decode('latin-1', 'replace')
        local_off, size = struct.unpack_from(endian+'II', pre, rec+0x204)
        names.append({'cl3_index': i, 'name': name, 'local_offset': local_off, 'size': size})
    return names, coll_off


def archive_overlay_scan(game: str, master: Path, out_root: Path, *, broad: bool) -> dict:
    rep = {'game': game, 'master': str(master), 'broad': broad, 'archives': [], 'matches': []}
    if not master.is_dir():
        rep['missing'] = True
        return rep
    paths = sorted(master.glob('GxArchivedFile*.dat'), key=lambda p: (p.stat().st_size, p.name))
    if not broad:
        paths = [p for p in paths if p.stat().st_size <= FAST_MAX_ARCHIVE]
    out_dir = out_root / game.lower() / 'archive_overlay'
    out_dir.mkdir(parents=True, exist_ok=True)
    exact_found = False
    for path in paths:
        arep = {'path': str(path), 'bytes': path.stat().st_size, 'entries_scanned': 0, 'candidate_names': 0}
        try:
            with Gdat(path) as g:
                arep['gdat_entries'] = g.count
                for idx in range(g.count):
                    parsed = _cl3_names_prefix(g, idx)
                    if not parsed:
                        continue
                    names, _ = parsed
                    arep['entries_scanned'] += 1
                    hits = [n for n in names if OVERLAY_NAME_RE.search(n['name']) or Path(n['name']).name.lower() == 'copyright.png']
                    if not hits:
                        continue
                    arep['candidate_names'] += len(hits)
                    # Fully decompress only the matching entry, then extract only matching members.
                    try:
                        payload = g.read_payload(idx)
                        c = Cl3(payload)
                    except (Exception, Cl3Error):
                        c = None
                    for n in hits:
                        item = {'archive': path.name, 'gdat_index': idx, **n}
                        base = Path(n['name']).name.lower()
                        if c is not None and n['cl3_index'] < len(c.files):
                            blob = c.file_data(n['cl3_index'])
                            dst = out_dir / f'{path.stem}_g{idx:05d}_c{n["cl3_index"]:03d}_{safe_name(Path(n["name"]).name)}'
                            dst.write_bytes(blob)
                            item.update({'sha256': hashlib.sha256(blob).hexdigest(), 'output': str(dst)})
                            if blob.startswith(PNG_SIG):
                                parsed_png = parse_png_at(blob, 0)
                                if parsed_png:
                                    _, w, h = parsed_png
                                    item['png'] = {'width': w, 'height': h}
                        rep['matches'].append(item)
                        if base == 'copyright.png':
                            exact_found = True
        except GdatError as exc:
            arep['error'] = str(exc)
        rep['archives'].append(arep)
        if exact_found:
            break
    rep['exact_copyright_found'] = exact_found
    return rep


def main() -> int:
    ap = argparse.ArgumentParser(description='Read-only title copyright/overlay source discovery.')
    ap.add_argument('--ms2-master', type=Path, required=True)
    ap.add_argument('--msf-master', type=Path, required=True)
    ap.add_argument('--ms2-exe', type=Path, required=True)
    ap.add_argument('--msf-exe', type=Path)
    ap.add_argument('--broad', action='store_true', help='scan CL3 name tables in all archives, not only small/system archives')
    ap.add_argument('--trace', action='store_true', help='trace PE references to copyright.png and skip archive scans for a fast loader-focused pass')
    ap.add_argument('--out', type=Path, default=ROOT/'.local'/'title_footer_discovery')
    args = ap.parse_args()
    out = args.out
    if out.exists():
        import shutil; shutil.rmtree(out)
    out.mkdir(parents=True)
    report = {
        'note': 'Read-only overlay discovery. Runtime-rejected title_moji is intentionally not a footer candidate.',
        'executables': [scan_embedded_pngs('MS2', args.ms2_exe, out, deep_trace=args.trace),
                        scan_embedded_pngs('MSF', args.msf_exe, out, deep_trace=args.trace) if args.msf_exe else {'game':'MSF','missing':True}],
        'loose': [loose_overlay_candidates('MS2', args.ms2_master, out),
                  loose_overlay_candidates('MSF', args.msf_master, out)],
        'archive_overlay': ([] if args.trace else [
            archive_overlay_scan('MS2', args.ms2_master, out, broad=args.broad),
            archive_overlay_scan('MSF', args.msf_master, out, broad=args.broad),
        ]),
    }
    rp = out/'report.json'
    rp.write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n', encoding='utf-8')
    print('TITLE COPYRIGHT/OVERLAY SOURCE DISCOVERY')
    print(f'Report: {rp}')
    for exe in report['executables']:
        if exe.get('missing'):
            print(f"{exe['game']} EXE: missing/skip")
            continue
        print(f"{exe['game']} EXE: sha256={exe['sha256']} embedded PNGs={len(exe['embedded_pngs'])} text hits={len(exe['needle_hits'])}")
        for h in exe['needle_hits']:
            if 'copyright.png' in h['needle'].lower() or h['needle'] in ('COMPILE HEART','IDEA FACTORY'):
                print(f"  0x{h['offset']:08x} {h['encoding']} {h['needle']}")
                print(f"    context: {h['context']}")
        for p in exe['embedded_pngs'][:20]:
            sec=f" section={p.get('section','?')}"
            vis=p.get('visual',{})
            cov=vis.get('alpha_nonzero_fraction')
            covs=f" alpha_nonzero={cov:.3f}" if isinstance(cov,(int,float)) else ''
            print(f"  embedded PNG 0x{p['offset']:08x} {p['width']}x{p['height']} {p['bytes']} B{sec}{covs} -> {p['output']}")
        for t in exe.get('copyright_path_traces', []):
            print(f"  path trace 0x{t['file_offset']:08x} VA=0x{t['va']:016x} section={t['section']} direct_xrefs={len(t['direct_xrefs'])} pointer_slots={len(t['pointer_slots'])}")
            for xr in t['direct_xrefs'][:12]:
                print(f"    direct xref VA=0x{xr['va']:016x} {xr['kind']} bytes={xr['bytes']}")
            for ptr in t['pointer_slots'][:12]:
                print(f"    pointer {ptr['encoding']} VA=0x{ptr['va']:016x} section={ptr['section']} code_xrefs={len(ptr['code_xrefs'])}")
                for xr in ptr['code_xrefs'][:6]:
                    print(f"      xref VA=0x{xr['va']:016x} {xr['kind']} bytes={xr['bytes']}")
            if args.trace:
                print(f"    deep relative_slots={len(t.get('relative_slots', []))} absolute_code_literals={len(t.get('absolute_code_literals', []))} nearby_rip_refs={len(t.get('nearby_rip_refs', []))} nearby_strings={len(t.get('nearby_strings', []))}")
                for rs in t.get('relative_slots', [])[:8]:
                    print(f"      relative {rs['encoding']} VA=0x{rs['va']:016x} section={rs['section']} code_xrefs={len(rs.get('code_xrefs', []))}")
                for xr in t.get('nearby_rip_refs', [])[:12]:
                    print(f"      nearby xref VA=0x{xr['va']:016x} -> target_delta={xr['target_delta']:+#x} {xr['kind']} context={xr.get('target_context','')[:120]}")
                if t.get('deep_trace_report'):
                    print(f"    deep trace report: {t['deep_trace_report']}")
            for ex in t.get('disassembly', [])[:18]:
                print(f"    disassembly: {ex['output']}")
    for loose in report['loose']:
        print(f"{loose['game']} loose overlay candidates: {len(loose.get('files',[]))}")
        for f in loose.get('files',[]):
            print(f"  {f['relative']} ({f['bytes']} B)")
    for ar in report['archive_overlay']:
        print(f"{ar['game']} archive member-name scan: mode={'BROAD' if ar['broad'] else 'FAST'} archives={len(ar.get('archives',[]))} matches={len(ar.get('matches',[]))} exact_copyright={ar.get('exact_copyright_found',False)}")
        for m in ar.get('matches',[]):
            extra=''
            if m.get('png'):
                extra=f" PNG={m['png']['width']}x{m['png']['height']}"
            print(f"  {m['archive']} g{m['gdat_index']:05d}/c{m['cl3_index']:03d} {m['name']} ({m['size']} B){extra}")
            if m.get('output'):
                print(f"    copy: {m['output']}")
    if report['archive_overlay'] and not any(ar.get('exact_copyright_found') for ar in report['archive_overlay']):
        print(f"No exact copyright.png archive member found in {'BROAD' if args.broad else 'FAST'} scan.")
        if not args.broad:
            print('If no source is proven, run: bash scripts/discover_title_footer_source.sh --broad')
    if args.trace:
        print('TRACE mode: archive scans skipped; PE section/xref evidence written under exe_xrefs/.')
    print('No files were modified.')
    return 0

if __name__ == '__main__':
    raise SystemExit(main())
