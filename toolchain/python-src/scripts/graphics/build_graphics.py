#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import io
import json
import os
import shutil
import struct
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
MANIFEST = ROOT / 'graphics' / 'manifest.json'
TOOLS = ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'tools'
BUILD = ROOT / '.local' / 'build'
WORK = Path(os.environ.get('MARY_SKELTER_GRAPHICS_WORK', str(BUILD / 'graphics')))
REPORT = Path(os.environ.get('MARY_SKELTER_GRAPHICS_REPORT', str(BUILD / 'reports' / 'graphics.json')))
LABEL_ASSET_ROOT = ROOT / 'graphics' / 'assets'
UI_TYPOGRAPHY = ROOT / 'graphics' / 'ui_typography.json'
_UI_TYPO_CACHE = None
_UI_FONT_CACHE = {}

# native: bc7enc download/C wrapper permanently removed from the production
# renderer. All BC7 data goes through the verified pure-Go bcn helper.


def die(msg: str) -> None:
    raise SystemExit(f'GRAPHICS ERROR: {msg}')


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open('rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def load_manifest() -> dict:
    return json.loads(MANIFEST.read_text(encoding='utf-8'))


def tid_meta(blob: bytes) -> tuple[int, int, str]:
    if len(blob) < 0x80:
        raise ValueError('truncated TID texture')
    magic = blob[:4]
    if magic == b'TID\x90':
        w = struct.unpack_from('<I', blob, 0x44)[0]
        h = struct.unpack_from('<I', blob, 0x48)[0]
        fmt = blob[0x64:0x68].decode('ascii', 'replace').rstrip('\0 ')
    elif magic == b'TID\x91':
        # Some shipped MS2 UI atlases use the endian-swapped TID91 header.
        # Pixel payload layout is unchanged; only metadata integers/fourcc are
        # stored big-endian/reversed (e.g. 00 00 08 00 -> 2048, 5TXD -> DXT5).
        w = struct.unpack_from('>I', blob, 0x44)[0]
        h = struct.unpack_from('>I', blob, 0x48)[0]
        fmt = blob[0x64:0x68].decode('ascii', 'replace')[::-1].rstrip('\0 ')
    else:
        raise ValueError(f'unsupported TID magic {magic!r}')
    return w, h, fmt


def tid_to_dds(blob: bytes) -> bytes:
    w, h, fmt = tid_meta(blob)
    if fmt not in {'DXT1', 'DXT3', 'DXT5', 'BC7'}:
        raise ValueError(f'unsupported texture format {fmt!r}')
    payload = blob[0x80:]
    block = 8 if fmt == 'DXT1' else 16
    linear = max(1, (w + 3) // 4) * block
    flags = 0x1 | 0x2 | 0x4 | 0x1000 | 0x80000
    hdr = struct.pack('<7I', 124, flags, h, w, linear, 0, 1)
    hdr += struct.pack('<11I', *([0] * 11))
    fourcc = b'DX10' if fmt == 'BC7' else fmt.encode('ascii')
    hdr += struct.pack('<2I4s5I', 32, 0x4, fourcc, 0, 0, 0, 0, 0)
    hdr += struct.pack('<5I', 0x1000, 0, 0, 0, 0)
    dx10 = struct.pack('<5I', 98, 3, 0, 1, 0) if fmt == 'BC7' else b''
    return b'DDS ' + hdr + dx10 + payload


def decode_tid(blob: bytes):
    """Decode the input texture to RGBA.

    Go-orchestrated production uses native byte-parity-proven DXT5 and BC7
    decoders. Retained Python/Pillow remains a pixel oracle and renderer;
    native decodes DXT1/DXT3 and DXT5/BC7 through Go; Pillow draws labels.
    """
    try:
        from PIL import Image
    except ImportError as e:
        raise SystemExit('GRAPHICS ERROR: Pinned Pillow missing from verified portable runtime.') from e
    w, h, fmt = tid_meta(blob)
    helper = os.environ.get('MARY_SKELTER_GRAPHICS_TEXTURE_HELPER')
    if fmt in {'BC7', 'DXT5', 'DXT1', 'DXT3'} and helper:
        WORK.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='ms-dxt5-dec-go-', dir=WORK) as td:
            td = Path(td)
            source_path, output_path = td / 'source.tid', td / 'image.rgba'
            source_path.write_bytes(blob)
            subprocess.run([
                helper, 'graphics-decode-'+fmt.lower(),
                '--source-tid', str(source_path), '--out', str(output_path),
            ], check=True, stdout=subprocess.PIPE)
            raw = output_path.read_bytes()
        if len(raw) != w*h*4:
            die(f'Go {fmt} decoder returned {len(raw)} RGBA bytes, expected {w*h*4}')
        return Image.frombytes('RGBA', (w, h), raw)
    im = Image.open(io.BytesIO(tid_to_dds(blob))).convert('RGBA')
    im.load()
    return im


def ensure_bc7_helper():
    die('legacy network/C BC7 encoding is disabled; use bundled native Go BC7')


def _changed_4x4_blocks(original_im, rendered_im, *, label: str) -> set[tuple[int, int]]:
    """Return compressed 4x4 blocks whose visible decoded pixels changed.

    RGB differences in pixels that are fully transparent both before and after are
    deliberately ignored. This mirrors the DXT5 preservation path and prevents
    invisible bleed/padding from forcing unrelated shipped blocks to be re-encoded.
    """
    import numpy as np
    if original_im.size != rendered_im.size:
        die(f'{label} partial encoder dimension mismatch')
    before=np.asarray(original_im.convert('RGBA'),dtype=np.uint8)
    after=np.asarray(rendered_im.convert('RGBA'),dtype=np.uint8)
    alpha_diff=before[...,3] != after[...,3]
    visible=(before[...,3] > 0) | (after[...,3] > 0)
    rgb_diff=np.any(before[...,:3] != after[...,:3],axis=2) & visible
    diff=alpha_diff | rgb_diff
    ys,xs=np.nonzero(diff)
    if len(xs)==0:
        return set()
    return {(int(x)//4,int(y)//4) for x,y in zip(xs,ys)}


def encode_bc7(im, helper: Path, stem: str, original_tid: bytes | None = None, original_im=None) -> bytes:
    """Encode BC7 while preserving every unchanged shipped 4x4 block exactly.

    bc7enc still produces the candidate payload, but on the normal build path only
    blocks intersecting decoded visible changes are copied back into the original
    source payload. This avoids generational recompression of untouched UI artwork.
    """
    # native opt-in: encode only changed BC7 blocks with the pinned native Go
    # bcn helper. No network, C compiler, or silent C fallback in this mode.
    if os.environ.get('MARY_SKELTER_GRAPHICS_NATIVE_BC7') == '1':
        native = os.environ.get('MARY_SKELTER_GRAPHICS_BCN_BINARY', '')
        if not Path(native).is_file():
            die('native native BC7 encoder binary unavailable')
        if original_tid is None:
            die('native native BC7 encoder requires an original source TID')
        w, h, fmt = tid_meta(original_tid)
        if (w, h, fmt) != (im.width, im.height, 'BC7'):
            die('native source BC7 dimensions/format mismatch')
        with tempfile.TemporaryDirectory(prefix='ms-i108-bc7-go-', dir=WORK) as tmp:
            d = Path(tmp)
            src, target, output = d/'pristine.tid', d/'rendered.png', d/'encoded.tid'
            src.write_bytes(original_tid)
            im.convert('RGBA').save(target, format='PNG')
            command = [native, '--source-tid', str(src), '--source-sha256', sha256_bytes(original_tid),
                       '--target-png', str(target), '--png-sha256', sha256_file(target),
                       '--out', str(output), '--quality', '6', '--max-alpha', '64', '--max-visible', '64']
            completed = subprocess.run(command, capture_output=True, text=True)
            if completed.returncode:
                die(f'native Go BC7 encoder rejected {stem}: {completed.stderr[-1800:]}')
            try:
                report = json.loads(completed.stdout)
            except (ValueError, TypeError) as exc:
                die(f'native Go BC7 report malformed: {exc}')
            rebuilt = output.read_bytes()
            if (report.get('SourceSHA256', report.get('source_sha256')) != sha256_bytes(original_tid)
                    or report.get('OutputSHA256', report.get('output_sha256')) != sha256_bytes(rebuilt)
                    or report.get('Format', report.get('format')) != 'BC7'
                    or rebuilt[:0x80] != original_tid[:0x80]
                    or len(rebuilt) != len(original_tid)):
                die('native native BC7 output identity/header mismatch')
            return rebuilt[0x80:]
    WORK.mkdir(parents=True, exist_ok=True)
    rgba = WORK / f'{stem}.rgba'
    out = WORK / f'{stem}.bc7'
    rgba_im=im.convert('RGBA')
    rgba.write_bytes(rgba_im.tobytes())
    subprocess.run([str(helper), str(im.width), str(im.height), str(rgba), str(out)], check=True)
    candidate = out.read_bytes()
    blocks_x=(im.width+3)//4
    blocks_y=(im.height+3)//4
    expected=blocks_x*blocks_y*16
    if len(candidate) != expected:
        die(f'BC7 payload size mismatch for {stem}: {len(candidate)} != {expected}')

    if original_tid is None:
        return candidate

    ow,oh,ofmt=tid_meta(original_tid)
    if (ow,oh,ofmt)!=(im.width,im.height,'BC7'):
        die(f'BC7 source metadata mismatch for {stem}: {(ow,oh,ofmt)} != {(im.width,im.height,"BC7")}')
    source_payload=original_tid[0x80:]
    if len(source_payload)!=expected:
        die(f'BC7 source payload size mismatch for {stem}: {len(source_payload)} != {expected}')
    if original_im is None:
        original_im=decode_tid(original_tid)

    texture_helper = os.environ.get('MARY_SKELTER_GRAPHICS_TEXTURE_HELPER')
    if texture_helper:
        WORK.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='ms-bc7-go-', dir=WORK) as td:
            td = Path(td)
            source_path = td / 'source.tid'
            original_path = td / 'original.rgba'
            rendered_path = td / 'rendered.rgba'
            candidate_path = td / 'candidate.bc7'
            output_path = td / 'payload.bc7'
            source_path.write_bytes(original_tid)
            original_path.write_bytes(original_im.convert('RGBA').tobytes())
            rendered_path.write_bytes(rgba_im.tobytes())
            candidate_path.write_bytes(candidate)
            subprocess.run([
                texture_helper, 'graphics-splice-bc7',
                '--source-tid', str(source_path),
                '--original-rgba', str(original_path),
                '--rendered-rgba', str(rendered_path),
                '--candidate-bc7', str(candidate_path),
                '--out', str(output_path),
            ], check=True, stdout=subprocess.PIPE)
            payload = output_path.read_bytes()
        if len(payload) != expected:
            die(f'Go BC7 payload size mismatch for {stem}: {len(payload)} != {expected}')
        return payload

    changed=_changed_4x4_blocks(original_im,rgba_im,label='BC7')
    payload=bytearray(source_payload)
    for bx,by in sorted(changed,key=lambda p:(p[1],p[0])):
        off=(by*blocks_x+bx)*16
        payload[off:off+16]=candidate[off:off+16]
    return bytes(payload)


def _rgb565(r: int, g: int, b: int) -> int:
    return (((r * 31 + 127) // 255) << 11) | (((g * 63 + 127) // 255) << 5) | ((b * 31 + 127) // 255)


def _rgb565_expand(v: int) -> tuple[int, int, int]:
    r = (v >> 11) & 31
    g = (v >> 5) & 63
    b = v & 31
    return ((r << 3) | (r >> 2), (g << 2) | (g >> 4), (b << 3) | (b >> 2))


def _bc3_color_palette(c0: int, c1: int):
    p0 = _rgb565_expand(c0)
    p1 = _rgb565_expand(c1)
    return [
        p0, p1,
        tuple((2 * p0[k] + p1[k]) // 3 for k in range(3)),
        tuple((p0[k] + 2 * p1[k]) // 3 for k in range(3)),
    ]


def _bc3_color_error(pixels, c0: int, c1: int):
    cp = _bc3_color_palette(c0, c1)
    total = 0.0
    indices = []
    for p in pixels:
        a = int(p[3])
        if a <= 4:
            indices.append(0)
            continue
        # Straight-alpha colour errors matter in proportion to opacity. Squared
        # weighting prevents nearly-transparent AA/bleed RGB from dominating the
        # BC3 endpoints while still preserving visible edge colour.
        w = (a / 255.0) ** 2
        errs = [
            (p[0] - q[0]) ** 2 + (p[1] - q[1]) ** 2 + (p[2] - q[2]) ** 2
            for q in cp
        ]
        idx = min(range(4), key=errs.__getitem__)
        indices.append(idx)
        total += w * errs[idx]
    return total, indices


def _refine_bc3_color_endpoints(pixels, c0: int, c1: int):
    """Least-squares + local 565 refinement for the BC3 colour endpoints.

    Use a small deterministic farthest-pair encoder to eliminate Pillow's
    version-dependent DXT5 writer. It was structurally correct but visibly too
    crude on high-contrast UI glyph edges. This remains a small deterministic
    pure-Python reference algorithm suitable for a later direct Go port, while
    materially improving the changed 4x4 blocks.
    """
    coeff = ((1.0, 0.0), (0.0, 1.0), (2.0/3.0, 1.0/3.0), (1.0/3.0, 2.0/3.0))

    for _ in range(4):
        _, inds = _bc3_color_error(pixels, c0, c1)
        saa = sab = sbb = 0.0
        rhs0 = [0.0, 0.0, 0.0]
        rhs1 = [0.0, 0.0, 0.0]
        for p, idx in zip(pixels, inds):
            alpha = int(p[3])
            if alpha <= 4:
                continue
            w = (alpha / 255.0) ** 2
            a, b = coeff[idx]
            saa += w * a * a
            sab += w * a * b
            sbb += w * b * b
            for k in range(3):
                rhs0[k] += w * a * p[k]
                rhs1[k] += w * b * p[k]
        det = saa * sbb - sab * sab
        if det <= 1e-9:
            break
        e0=[]; e1=[]
        for k in range(3):
            x0=(rhs0[k]*sbb-rhs1[k]*sab)/det
            x1=(rhs1[k]*saa-rhs0[k]*sab)/det
            e0.append(max(0,min(255,round(x0))))
            e1.append(max(0,min(255,round(x1))))
        n0=_rgb565(*e0); n1=_rgb565(*e1)
        if n0 < n1:
            n0,n1=n1,n0
        if n0 == n1:
            if n0 < 0xFFFF: n0 += 1
            elif n1 > 0: n1 -= 1
        if (n0,n1)==(c0,c1):
            break
        c0,c1=n0,n1

    def neigh(v):
        r=(v>>11)&31; g=(v>>5)&63; b=v&31
        vals={v}
        for dr,dg,db in ((-1,0,0),(1,0,0),(0,-1,0),(0,1,0),(0,0,-1),(0,0,1)):
            rr=max(0,min(31,r+dr)); gg=max(0,min(63,g+dg)); bb=max(0,min(31,b+db))
            vals.add((rr<<11)|(gg<<5)|bb)
        return vals

    best_err,_=_bc3_color_error(pixels,c0,c1)
    # Cheap greedy local search around the quantized least-squares solution.
    for _ in range(3):
        best=(best_err,c0,c1)
        for n0 in neigh(c0):
            if n0 <= c1: continue
            e,_=_bc3_color_error(pixels,n0,c1)
            if e < best[0]: best=(e,n0,c1)
        for n1 in neigh(c1):
            if c0 <= n1: continue
            e,_=_bc3_color_error(pixels,c0,n1)
            if e < best[0]: best=(e,c0,n1)
        if (best[1],best[2])==(c0,c1):
            break
        best_err,c0,c1=best
    return c0,c1


def _encode_bc3_block(pixels: list[tuple[int, int, int, int]]) -> bytes:
    """Encode one 4x4 RGBA block as BC3/DXT5 deterministically."""
    if len(pixels) != 16:
        raise ValueError('BC3 block must contain exactly 16 RGBA pixels')

    alphas = [p[3] for p in pixels]
    a0 = max(alphas)
    a1 = min(alphas)
    if a0 > a1:
        ap = [
            a0, a1,
            (6 * a0 + 1 * a1) // 7,
            (5 * a0 + 2 * a1) // 7,
            (4 * a0 + 3 * a1) // 7,
            (3 * a0 + 4 * a1) // 7,
            (2 * a0 + 5 * a1) // 7,
            (1 * a0 + 6 * a1) // 7,
        ]
    else:
        ap = [
            a0, a1,
            (4 * a0 + 1 * a1) // 5,
            (3 * a0 + 2 * a1) // 5,
            (2 * a0 + 3 * a1) // 5,
            (1 * a0 + 4 * a1) // 5,
            0, 255,
        ]
    alpha_bits = 0
    for i, a in enumerate(alphas):
        idx = min(range(8), key=lambda j: abs(a - ap[j]))
        alpha_bits |= idx << (3 * i)
    alpha_part = bytes((a0, a1)) + alpha_bits.to_bytes(6, 'little')

    visible = [p for p in pixels if p[3] > 4]
    candidates = visible or pixels
    if len(candidates) == 1:
        cmax = cmin = candidates[0]
    else:
        best = -1
        cmax = cmin = candidates[0]
        for i in range(len(candidates)):
            p = candidates[i]
            for j in range(i + 1, len(candidates)):
                q = candidates[j]
                # Weight the initial axis by visibility too; transparent RGB is
                # only padding for the compressor and must not select endpoints.
                d = (p[0] - q[0]) ** 2 + (p[1] - q[1]) ** 2 + (p[2] - q[2]) ** 2
                if d > best:
                    best = d
                    cmax, cmin = p, q

    c0 = _rgb565(cmax[0], cmax[1], cmax[2])
    c1 = _rgb565(cmin[0], cmin[1], cmin[2])
    if c0 < c1:
        c0, c1 = c1, c0
    if c0 == c1:
        if c0 < 0xFFFF: c0 += 1
        elif c1 > 0: c1 -= 1
    c0,c1=_refine_bc3_color_endpoints(pixels,c0,c1)
    cp=_bc3_color_palette(c0,c1)
    color_bits=0
    for i,p in enumerate(pixels):
        if p[3] <= 4:
            idx=0
        else:
            idx=min(range(4),key=lambda j:(p[0]-cp[j][0])**2+(p[1]-cp[j][1])**2+(p[2]-cp[j][2])**2)
        color_bits |= idx << (2*i)
    color_part=struct.pack('<HHI',c0,c1,color_bits)
    return alpha_part+color_part

def _changed_bc3_blocks(original_im, rendered_im) -> set[tuple[int, int]]:
    return _changed_4x4_blocks(original_im,rendered_im,label='DXT5')

def encode_dxt5(im, original_tid: bytes | None = None, original_im=None) -> bytes:
    """Encode BC3/DXT5 deterministically.

    Production Go-orchestrated builds delegate this codec boundary to the native
    builder helper. The retained Python implementation remains the standalone/oracle
    path when no texture helper is configured. Both paths preserve unchanged shipped
    4x4 blocks and re-encode only alpha-aware visible changes.
    """
    rgba = im.convert('RGBA')
    w, h = rgba.size
    blocks_x = (w + 3) // 4
    blocks_y = (h + 3) // 4
    expected = blocks_x * blocks_y * 16

    # Production hybrid, bounded cutover: the three source-SHA-pinned pink
    # atlases have completed full-font/real-TID strict 64/64 QA. Other DXT5
    # families keep their existing Go helper until individually audited.
    # The source-specific native visual exception is unchanged; NEVER make
    # this a global BC3 quality or renderer promotion.
    strict_source_sha = {
        "687a6b6bd576b686784c8325ebfe6334312c8629b879a2b97ebf4fbcd54991d3",
        "f0171ef248287210ec12c2e73d9e4a282e33cce53e06b51211c9ba41ebbccaad",
        "3a57a59b004c92181f2c7210100a557aa51bbb91cd4d9d6508ddea6512fdc738",
    }
    if (os.environ.get('MARY_SKELTER_GRAPHICS_NATIVE_BC7') == '1'
            and original_tid is not None
            and sha256_bytes(original_tid) in strict_source_sha):
        native = Path(os.environ.get('MARY_SKELTER_GRAPHICS_BCN_BINARY', ''))
        if original_tid is None or not native.is_file():
            die('native strict DXT5 requires source TID and pinned Go BCn binary')
        if tid_meta(original_tid) != (w, h, 'DXT5'):
            die('native strict DXT5 source metadata mismatch')
        WORK.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='ms-bc3-strict-go-', dir=WORK) as tmp:
            d = Path(tmp)
            src, img, output = d / 'source.tid', d / 'rendered.png', d / 'encoded.tid'
            src.write_bytes(original_tid)
            rgba.save(img, format='PNG')
            cmd = [str(native), '--source-tid', str(src),
                   '--source-sha256', sha256_bytes(original_tid),
                   '--target-png', str(img), '--png-sha256', sha256_file(img),
                   '--out', str(output), '--quality', '6',
                   '--max-alpha', '64', '--max-visible', '64']
            proc = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            if proc.returncode:
                die(f'strict Go DXT5 rejected source: {proc.stderr[-1600:]}')
            try:
                report = json.loads(proc.stdout)
                tid = output.read_bytes()
            except (ValueError, OSError) as exc:
                die(f'strict Go DXT5 output invalid: {exc}')
            if (report.get('format') != 'DXT5' or
                    report.get('source_sha256') != sha256_bytes(original_tid) or
                    report.get('output_sha256') != sha256_bytes(tid) or
                    int(report.get('max_alpha_error', 256)) > 64 or
                    int(report.get('max_visible_error', 256)) > 64 or
                    tid[:0x80] != original_tid[:0x80] or
                    len(tid) != len(original_tid)):
                die('strict Go DXT5 identity, header or quality mismatch')
            return tid[0x80:]

    if original_tid is not None:
        ow, oh, ofmt = tid_meta(original_tid)
        if (ow, oh, ofmt) != (w, h, 'DXT5'):
            die(f'DXT5 source metadata mismatch: {(ow, oh, ofmt)} != {(w, h, "DXT5")}')
        source_payload = original_tid[0x80:]
        if len(source_payload) != expected:
            die(f'DXT5 source payload size mismatch: {len(source_payload)} != {expected}')
        if original_im is None:
            original_im = decode_tid(original_tid)

        texture_helper = os.environ.get('MARY_SKELTER_GRAPHICS_TEXTURE_HELPER')
        if texture_helper:
            WORK.mkdir(parents=True, exist_ok=True)
            original_rgba = original_im.convert('RGBA').tobytes()
            rendered_rgba = rgba.tobytes()
            with tempfile.TemporaryDirectory(prefix='ms-dxt5-go-', dir=WORK) as td:
                td = Path(td)
                source_path = td / 'source.tid'
                original_path = td / 'original.rgba'
                rendered_path = td / 'rendered.rgba'
                output_path = td / 'payload.dxt5'
                source_path.write_bytes(original_tid)
                original_path.write_bytes(original_rgba)
                rendered_path.write_bytes(rendered_rgba)
                subprocess.run([
                    texture_helper, 'graphics-encode-dxt5',
                    '--source-tid', str(source_path),
                    '--original-rgba', str(original_path),
                    '--rendered-rgba', str(rendered_path),
                    '--out', str(output_path),
                ], check=True, stdout=subprocess.PIPE)
                payload = output_path.read_bytes()
            if len(payload) != expected:
                die(f'Go DXT5 payload size mismatch: {len(payload)} != {expected}')
            return payload

        changed = _changed_bc3_blocks(original_im, rgba)
        payload = bytearray(source_payload)
    else:
        changed = {(bx, by) for by in range(blocks_y) for bx in range(blocks_x)}
        payload = bytearray(expected)

    px = rgba.load()
    for bx, by in sorted(changed, key=lambda p: (p[1], p[0])):
        block = []
        for yy in range(4):
            sy = min(h - 1, by * 4 + yy)
            for xx in range(4):
                sx = min(w - 1, bx * 4 + xx)
                block.append(px[sx, sy])
        encoded = _encode_bc3_block(block)
        off = (by * blocks_x + bx) * 16
        payload[off:off + 16] = encoded

    if len(payload) != expected:
        die(f'DXT5 payload size mismatch: {len(payload)} != {expected}')
    return bytes(payload)


def make_tid(original: bytes, payload: bytes) -> bytes:
    w, h, fmt = tid_meta(original)
    if fmt not in {'BC7', 'DXT5'}:
        die(f'current graphics encoder supports BC7/DXT5, got {fmt}')
    expected = ((w + 3) // 4) * ((h + 3) // 4) * 16
    if len(original) != 0x80 + expected or len(payload) != expected:
        die('TID payload size invariant failed')
    return original[:0x80] + payload


def white_runs(crop) -> list[tuple[int, int]]:
    im = crop.convert('RGBA')
    px = im.load()
    w, h = im.size
    cols = []
    for x in range(w):
        hit = False
        for y in range(h):
            r, g, b, a = px[x, y]
            if a > 30 and r > 120 and g > 120 and b > 120:
                hit = True
                break
        cols.append(hit)
    out = []
    start = None
    for i, val in enumerate(cols):
        if val and start is None:
            start = i
        elif not val and start is not None:
            out.append((start, i - 1))
            start = None
    if start is not None:
        out.append((start, len(cols) - 1))
    return out


def isolate_face(glyph_slice):
    """Keep shipped white/cyan/blue glyph treatment but drop baked long black extrusion.

    Renderer v1 copied each glyph's inherited black extrusion/fringe and runtime
    showed vertical bars near recomposed labels. Keep
    the colored face/outline only, then creates one controlled label-level shadow.
    """
    from PIL import Image
    im = glyph_slice.convert('RGBA')
    src = im.load()
    w, h = im.size
    out = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    dst = out.load()
    for y in range(h):
        for x in range(w):
            r, g, b, a = src[x, y]
            if a <= 10:
                continue
            keep = (
                (r > 115 and g > 115 and b > 115)
                or (b > 90 and g > 55 and b > r * 1.10 and (r + g + b) > 180)
                or (b > 120 and g > 90 and r < 190)
            )
            if keep:
                dst[x, y] = (r, g, b, a)
    box = out.getbbox()
    return out.crop(box) if box else out


def build_glyph_library(result_im, spec: dict) -> dict:
    glyphs = {}
    for text, rect in spec['glyph_sources'].items():
        x, y, w, h = rect
        crop = result_im.crop((x, y, x + w, y + h))
        runs = white_runs(crop)
        if text == 'RAPUNZEL':
            mapping = {2: 1, 3: 2, 4: 3, 5: 4, 6: 5, 7: 6}
        elif text == 'CINDERELLA':
            mapping = {i: i for i in range(8)}
        else:
            if len(runs) != len(text):
                die(f'glyph segmentation mismatch for {text}: chars={len(text)} runs={len(runs)}')
            mapping = {i: i for i in range(len(text))}
        for char_index, run_index in mapping.items():
            a0, b0 = runs[run_index]
            left = 0 if run_index == 0 else (runs[run_index - 1][1] + a0) // 2
            right = w - 1 if run_index == len(runs) - 1 else (b0 + runs[run_index + 1][0]) // 2
            glyphs.setdefault(text[char_index], isolate_face(crop.crop((left, 0, right + 1, h))))
    needed = set(''.join(r['to'] for r in spec['replacements']))
    missing = sorted(needed - set(glyphs))
    if missing:
        die(f'active result renderer lacks shipped glyphs: {missing}')
    return glyphs


def compose_label(glyphs: dict, text: str, gap: int = -2, target_h: int = 54):
    from PIL import Image
    pieces = []
    for ch in text:
        gi = glyphs[ch]
        if gi.height != target_h:
            scale = target_h / gi.height
            gi = gi.resize((max(1, round(gi.width * scale)), target_h), Image.Resampling.LANCZOS)
        pieces.append(gi)
    width = sum(p.width for p in pieces) + gap * (len(pieces) - 1)
    face = Image.new('RGBA', (max(1, width), target_h), (0, 0, 0, 0))
    x = 0
    for piece in pieces:
        face.alpha_composite(piece, (x, 0))
        x += piece.width + gap

    # One uniform dark-blue label shadow/extrusion replaces v1's inherited
    # per-glyph black tails. This preserves depth without the runtime bar artifact.
    alpha = face.getchannel('A')
    canvas = Image.new('RGBA', (face.width + 8, face.height + 10), (0, 0, 0, 0))
    for dy in range(7, 1, -1):
        shadow = Image.new('RGBA', face.size, (0, 25, 50, 0))
        shadow.putalpha(alpha.point(lambda v: int(v * 0.55)))
        canvas.alpha_composite(shadow, (2, dy))
    canvas.alpha_composite(face, (0, 0))
    box = canvas.getbbox()
    return canvas.crop(box) if box else canvas


def fit_label(label, max_w: int, max_h: int):
    from PIL import Image
    scale = min(1.0, max_w / label.width, max_h / label.height)
    if scale < 1.0:
        label = label.resize((max(1, round(label.width * scale)), max(1, round(label.height * scale))), Image.Resampling.LANCZOS)
    return label



def _face_hit(px, mode: str) -> bool:
    r, g, b, a = px
    if a <= 30:
        return False
    if mode == 'yellow':
        return r > 180 and g > 120 and b < 120
    if mode == 'white':
        return r > 150 and g > 150 and b > 150
    if mode == 'cyan':
        # Bright cyan/blue UI faces used by several MS2 baked-name atlases.
        # Keep this intentionally face-only; outlines/shadows are recovered by
        # the standard local dilation step.
        return a > 30 and g > 95 and b > 120 and b > r * 1.10 and (g + b) > 230
    if mode == 'gray':
        # Finale ren-bonus labels use an opaque neutral mid-gray face, while
        # the baked extrusion/outline is near-black. Key on the face band only
        # and recover the local outline later via mask dilation.
        hi = max(r, g, b)
        lo = min(r, g, b)
        avg = (r + g + b) / 3.0
        return a >= 180 and 78 <= avg <= 145 and (hi - lo) <= 24
    if mode == 'pink':
        # Finale battle nameplates use a pale pink/white face over a dark red
        # outline and splatter background. Select only the bright face; the
        # local dilation step recovers the shipped red edge without pulling in
        # the much larger background brush.
        return a > 30 and r > 185 and g > 65 and b > 70 and (r + g + b) > 390
    raise ValueError(f'unsupported raster face mode {mode!r}')


def _face_projection(crop, mode: str) -> list[int]:
    im = crop.convert('RGBA')
    px = im.load()
    out = []
    for x in range(im.width):
        n = 0
        for y in range(im.height):
            if _face_hit(px[x, y], mode):
                n += 1
        out.append(n)
    return out


def _split_words_projection(proj: list[int], nwords: int) -> list[tuple[int, int]]:
    used = [i for i, n in enumerate(proj) if n]
    if not used:
        return []
    lo, hi = used[0], used[-1] + 1
    if nwords <= 1:
        return [(lo, hi)]
    gaps = []
    start = None
    for x in range(lo, hi):
        if proj[x] == 0 and start is None:
            start = x
        elif proj[x] and start is not None:
            gaps.append((start, x, x - start))
            start = None
    if start is not None:
        gaps.append((start, hi, hi - start))
    internal = [g for g in gaps if g[0] > lo and g[1] < hi]
    chosen = sorted(sorted(internal, key=lambda z: z[2], reverse=True)[:nwords - 1], key=lambda z: z[0])
    if len(chosen) == nwords - 1:
        cuts = [(a + b) // 2 for a, b, _ in chosen]
    else:
        cuts = [round(lo + (hi - lo) * k / nwords) for k in range(1, nwords)]
    bounds = [lo] + cuts + [hi]
    return [(bounds[i], bounds[i + 1]) for i in range(nwords)]


def _split_chars_projection(proj: list[int], x0: int, x1: int, nchars: int) -> list[tuple[int, int]]:
    sub = [float(x) for x in proj[x0:x1]]
    w = x1 - x0
    if nchars <= 1:
        return [(x0, x1)]

    # Prefer the real blank-column boundaries when the shipped raster keeps
    # characters separated. This is both more faithful and more robust than
    # the width-regularized DP for narrow/proportional Finale display fonts.
    # Dot/fragment components such as lowercase i still share the same x run,
    # so this remains character-level rather than connected-component-level.
    runs = []
    start = None
    for i, value in enumerate(sub):
        if value > 0 and start is None:
            start = i
        elif value <= 0 and start is not None:
            runs.append((start, i))
            start = None
    if start is not None:
        runs.append((start, w))
    if len(runs) == nchars:
        bounds = [0]
        for i in range(len(runs) - 1):
            bounds.append((runs[i][1] + runs[i + 1][0]) // 2)
        bounds.append(w)
        return [(x0 + bounds[i], x0 + bounds[i + 1]) for i in range(nchars)]

    avg = w / nchars
    minw = max(3, int(avg * 0.48))
    maxw = max(minw + 1, int(avg * 1.65))
    maxp = max(1.0, max(sub, default=0.0))
    boundary_cost = [x / maxp * 6.0 for x in sub]
    inf = 1e100
    dp: list[dict[int, tuple[float, int | None]]] = [dict() for _ in range(nchars + 1)]
    dp[0][0] = (0.0, None)
    for k in range(1, nchars + 1):
        lo = max(k * minw, int(k * avg - avg * 0.9))
        hi = min(w - (nchars - k) * minw, int(k * avg + avg * 0.9))
        if k == nchars:
            lo = hi = w
        for pos in range(lo, hi + 1):
            best = (inf, None)
            plo = max((k - 1) * minw, pos - maxw)
            phi = min((k - 1) * maxw, pos - minw)
            for prev in range(plo, phi + 1):
                if prev not in dp[k - 1]:
                    continue
                segw = pos - prev
                width_penalty = ((segw - avg) / avg) ** 2 * 1.8
                cut_cost = 0.0 if pos == w else boundary_cost[min(pos, w - 1)]
                value = dp[k - 1][prev][0] + width_penalty + cut_cost
                if value < best[0]:
                    best = (value, prev)
            if best[1] is not None:
                dp[k][pos] = best
    if w not in dp[nchars]:
        bounds = [round(i * w / nchars) for i in range(nchars + 1)]
    else:
        bounds = [w]
        pos = w
        for k in range(nchars, 0, -1):
            prev = dp[k][pos][1]
            assert prev is not None
            bounds.append(prev)
            pos = prev
        bounds.sort()
    return [(x0 + bounds[i], x0 + bounds[i + 1]) for i in range(nchars)]


def _extract_raster_glyphs(crop, text: str, mode: str, mask_dilate: int = 5):
    # Raster-atlas glyph extraction must work on both transparent textures and
    # opaque UI atlases. Using RGBA.getbbox() directly on an opaque black
    # background incorrectly treats the whole rectangular cell as glyph data.
    # Instead derive character boundaries from the bright face and build a
    # shape-following mask, lightly dilated to retain the shipped outline/shadow.
    if '\n' in text:
        raise ValueError('raster glyph source must be single-line')
    from PIL import Image, ImageFilter, ImageChops
    src = crop.convert('RGBA')
    proj = _face_projection(src, mode)
    words = text.split(' ')
    word_boxes = _split_words_projection(proj, len(words))
    if len(word_boxes) != len(words):
        return []
    out = []
    spx = src.load()
    for word, (x0, x1) in zip(words, word_boxes):
        boxes = _split_chars_projection(proj, x0, x1, len(word))
        for ch, (a, b) in zip(word, boxes):
            aa = max(0, a - 2)
            bb = min(src.width, b + 2)
            w = max(1, bb - aa)
            face = Image.new('L', (w, src.height), 0)
            fp = face.load()
            for yy in range(src.height):
                for xx in range(w):
                    if _face_hit(spx[aa + xx, yy], mode):
                        fp[xx, yy] = 255
            if face.getbbox() is None:
                continue
            # Two-pixel neighborhood is enough for the common white/red/blue
            # outlines while avoiding long baked background/extrusion bars.
            k = max(3, int(mask_dilate) | 1)
            mask = face.filter(ImageFilter.MaxFilter(k))
            sl = src.crop((aa, 0, bb, src.height))
            alpha = sl.getchannel('A')
            alpha = ImageChops.multiply(alpha, mask)
            glyph = sl.copy()
            glyph.putalpha(alpha)
            box = mask.getbbox()
            if box:
                l, t, r, bot = box
                glyph = glyph.crop((max(0, l - 1), max(0, t - 1), min(glyph.width, r + 1), min(glyph.height, bot + 1)))
            if glyph.width and glyph.height and glyph.getbbox():
                out.append((ch, glyph))
    return out


def _isolate_explicit_glyph(crop, mode: str, mask_dilate: int = 5):
    from PIL import Image, ImageFilter, ImageChops
    src = crop.convert('RGBA')
    mask = Image.new('L', src.size, 0)
    mp = mask.load(); sp = src.load()
    for y in range(src.height):
        for x in range(src.width):
            if _face_hit(sp[x, y], mode):
                mp[x, y] = 255
    if mask.getbbox() is None:
        return None
    k = max(3, int(mask_dilate) | 1)
    mask = mask.filter(ImageFilter.MaxFilter(k))
    alpha = ImageChops.multiply(src.getchannel('A'), mask)
    out = src.copy(); out.putalpha(alpha)
    box = mask.getbbox()
    if box:
        l,t,r,b = box
        out = out.crop((max(0,l-1), max(0,t-1), min(out.width,r+1), min(out.height,b+1)))
    return out if out.getbbox() else None



def _extract_explicit_face_run(im, rect, mode: str, *, pad: int = 8, core_radius: int = 4, outer_radius: int = 8):
    """Extract one shipped raster glyph from an explicit face-run rectangle.

    The run rectangle encloses only the bright face of the desired glyph. We
    preserve the shipped face/inner outline exactly, then admit only nearby
    bright family-colour pixels in the outer ring. This avoids dragging the
    patterned UI background along with the glyph while retaining the original
    raster character shape and treatment.
    """
    import numpy as np
    from PIL import Image, ImageFilter
    src=im.convert('RGBA')
    x0,y0,x1,y1=map(int,rect)
    rx0=max(0,x0-pad); ry0=max(0,y0-pad); rx1=min(src.width,x1+pad); ry1=min(src.height,y1+pad)
    crop=src.crop((rx0,ry0,rx1,ry1))
    a=np.asarray(crop,dtype=np.uint8)
    r,g,b,al=[a[...,i] for i in range(4)]
    face=np.zeros(al.shape,dtype=np.uint8)
    # Seed only inside the supplied face run, never from neighbouring glyphs.
    for yy in range(crop.height):
        for xx in range(crop.width):
            sx=rx0+xx; sy=ry0+yy
            if x0 <= sx < x1 and y0 <= sy < y1 and _face_hit(tuple(int(v) for v in a[yy,xx]),mode):
                face[yy,xx]=255
    if not face.any():
        die(f'explicit raster glyph has no {mode} face pixels in {rect}')
    fm=Image.fromarray(face,'L')
    ck=max(3,core_radius*2+1); ok=max(ck,outer_radius*2+1)
    core=np.asarray(fm.filter(ImageFilter.MaxFilter(ck)))>0
    wide=np.asarray(fm.filter(ImageFilter.MaxFilter(ok)))>0
    if mode=='yellow':
        family=(al>20)&(r>105)&(g>40)&(r.astype(int)>b.astype(int)*1.15)
    else:
        # Pink/red shipped outer treatment around the white/pale face.
        family=(al>20)&(r>95)&(r.astype(int)>g.astype(int)*1.35)&(b>20)
    keep=(core | (wide & family)) & (al>4)
    out=a.copy(); out[...,3]=np.where(keep,al,0).astype(np.uint8)
    glyph=Image.fromarray(out,'RGBA')
    box=glyph.getbbox()
    return glyph.crop(box) if box else glyph


def _compose_token_raster_label(glyphs: dict, text: str, target_h: int, gap: int, space_width: int):
    """Compose exact shipped glyphs, supporting family ligatures such as 'S."""
    from PIL import Image
    tokens=[]; i=0
    while i < len(text):
        if text[i]==' ':
            tokens.append(None); i+=1; continue
        if text.startswith("'S",i) and "'S" in glyphs:
            tokens.append("'S"); i+=2; continue
        tokens.append(text[i]); i+=1
    pieces=[]
    for tok in tokens:
        if tok is None:
            pieces.append(None); continue
        if tok not in glyphs:
            die(f'missing explicit shipped raster token {tok!r} for {text!r}')
        g=glyphs[tok]
        scale=target_h/max(1,g.height)
        pieces.append(g.resize((max(1,round(g.width*scale)),target_h),Image.Resampling.LANCZOS))
    width=sum(space_width if q is None else q.width for q in pieces)+gap*max(0,len(pieces)-1)
    out=Image.new('RGBA',(max(1,width),target_h),(0,0,0,0)); x=0
    for n,q in enumerate(pieces):
        if q is None: x+=space_width
        else: out.alpha_composite(q,(x,0)); x+=q.width
        if n != len(pieces)-1: x+=gap
    box=out.getbbox()
    return out.crop(box) if box else out

def _sample_face_rgba(glyphs: dict):
    # Pick the brightest opaque shipped face pixel without assuming a white face.
    # This keeps synthetic fallbacks in-family for white, yellow and gray atlases.
    ref = glyphs.get('N') or glyphs.get('H') or glyphs.get('E') or next(iter(glyphs.values()))
    best = None
    best_score = -1
    for px in ref.convert('RGBA').getdata():
        r,g,b,a = px
        if a <= 100:
            continue
        score = max(r,g,b) * 2 + r + g + b
        if score > best_score:
            best_score = score
            best = (r,g,b,255)
    return best or (245,245,245,255)


def _synth_basic_char_from_glyphs(glyphs: dict, char: str, target_h: int, outline_rgba=None):
    """Synthetic fallback for a small set of missing Latin glyphs.

    This is deliberately simple and is used only when the shipped style family
    lacks a required glyph entirely. Family-wide polish may replace it later.
    """
    from PIL import Image, ImageDraw, ImageFilter
    h=max(10,int(target_h))
    if char == 'N':
        w=max(7,round(h*0.62)); stroke=max(2,round(h*0.12))
        m=Image.new('L',(w,h),0); d=ImageDraw.Draw(m)
        pad=max(1,stroke//2)
        d.line((pad,pad,pad,h-pad-1),fill=255,width=stroke)
        d.line((w-pad-1,pad,w-pad-1,h-pad-1),fill=255,width=stroke)
        d.line((pad,pad,w-pad-1,h-pad-1),fill=255,width=stroke)
    elif char in {'k','K'}:
        w=max(7,round(h*0.56)); stroke=max(2,round(h*0.105))
        m=Image.new('L',(w,h),0); d=ImageDraw.Draw(m)
        pad=max(1,stroke//2); mid=round(h*0.52)
        d.line((pad,pad,pad,h-pad-1),fill=255,width=stroke)
        upper_y = pad if char == 'K' else pad+round(h*0.16)
        d.line((pad,mid,w-pad-1,upper_y),fill=255,width=stroke)
        d.line((pad,mid,w-pad-1,h-pad-1),fill=255,width=stroke)
    elif char == 'T':
        w=max(7,round(h*0.62)); stroke=max(2,round(h*0.12))
        m=Image.new('L',(w,h),0); d=ImageDraw.Draw(m); pad=max(1,stroke//2)
        d.line((pad,pad,w-pad-1,pad),fill=255,width=stroke)
        d.line((w//2,pad,w//2,h-pad-1),fill=255,width=stroke)
    elif char == 'b':
        w=max(7,round(h*0.50)); stroke=max(2,round(h*0.10))
        m=Image.new('L',(w,h),0); d=ImageDraw.Draw(m); pad=max(1,stroke//2)
        d.line((pad,pad,pad,h-pad-1),fill=255,width=stroke)
        top=round(h*0.38); bot=h-pad-1
        d.arc((pad-stroke,top,w-pad-1,bot),-90,90,fill=255,width=stroke)
        d.arc((pad-stroke,top,w-pad-1,bot),90,270,fill=255,width=stroke)
    elif char in {'B','C','M','P','U','W'}:
        w=max(8,round(h*0.62)); stroke=max(2,round(h*0.105)); pad=max(1,stroke//2)
        m=Image.new('L',(w,h),0); d=ImageDraw.Draw(m)
        if char == 'B':
            d.line((pad,pad,pad,h-pad-1),fill=255,width=stroke)
            d.arc((pad-stroke,pad,w-pad-1,h//2+stroke),-90,90,fill=255,width=stroke)
            d.arc((pad-stroke,h//2-stroke,w-pad-1,h-pad-1),-90,90,fill=255,width=stroke)
        elif char == 'C':
            d.arc((pad,pad,w-pad-1,h-pad-1),55,305,fill=255,width=stroke)
        elif char == 'M':
            d.line((pad,h-pad-1,pad,pad),fill=255,width=stroke)
            d.line((pad,pad,w//2,round(h*0.45)),fill=255,width=stroke)
            d.line((w//2,round(h*0.45),w-pad-1,pad),fill=255,width=stroke)
            d.line((w-pad-1,pad,w-pad-1,h-pad-1),fill=255,width=stroke)
        elif char == 'P':
            d.line((pad,pad,pad,h-pad-1),fill=255,width=stroke)
            d.arc((pad-stroke,pad,w-pad-1,round(h*0.55)),-90,90,fill=255,width=stroke)
        elif char == 'U':
            d.line((pad,pad,pad,round(h*0.68)),fill=255,width=stroke)
            d.line((w-pad-1,pad,w-pad-1,round(h*0.68)),fill=255,width=stroke)
            d.arc((pad,round(h*0.45),w-pad-1,h-pad-1),0,180,fill=255,width=stroke)
        elif char == 'W':
            d.line((pad,pad,round(w*0.28),h-pad-1),fill=255,width=stroke)
            d.line((round(w*0.28),h-pad-1,w//2,round(h*0.56)),fill=255,width=stroke)
            d.line((w//2,round(h*0.56),round(w*0.72),h-pad-1),fill=255,width=stroke)
            d.line((round(w*0.72),h-pad-1,w-pad-1,pad),fill=255,width=stroke)
    elif char == 'S':
        w=max(8,round(h*0.58)); stroke=max(2,round(h*0.105))
        m=Image.new('L',(w,h),0); d=ImageDraw.Draw(m); pad=max(1,stroke//2)
        # Two opposing arcs plus a short middle connector form a compact S.
        d.arc((pad,pad,w-pad-1,round(h*0.58)),75,285,fill=255,width=stroke)
        d.arc((pad,round(h*0.42),w-pad-1,h-pad-1),255,105,fill=255,width=stroke)
        d.line((round(w*0.25),h//2,round(w*0.75),h//2),fill=255,width=max(1,stroke-1))
    else:
        raise ValueError(f'unsupported synthetic basic glyph {char!r}')
    outline=m.filter(ImageFilter.MaxFilter(5))
    face=_sample_face_rgba(glyphs)
    if outline_rgba is None:
        outline_rgba=(20,20,20,255)
    out=Image.new('RGBA',(w,h),(0,0,0,0))
    ol=Image.new('RGBA',(w,h),tuple(outline_rgba[:3])+(0,)); ol.putalpha(outline)
    out.alpha_composite(ol)
    fg=Image.new('RGBA',(w,h),face); fg.putalpha(m)
    out.alpha_composite(fg)
    return out


def _synth_z_from_glyphs(glyphs: dict, target_h: int):
    # Fallback for style families that contain no shipped Z.
    # Build a simple slanted Z from the shipped face color/outline palette;
    # final family-wide polish may replace it if an exact source glyph is found.
    from PIL import Image, ImageDraw, ImageFilter
    ref = glyphs.get('N') or glyphs.get('E') or next(iter(glyphs.values()))
    h = max(8, target_h)
    w = max(6, round(h * 0.62))
    # Sample a bright face pixel from the donor.
    face = (245,245,245,255)
    for px in ref.convert('RGBA').getdata():
        r,g,b,a = px
        if a > 100 and r > 150 and g > 150 and b > 150:
            face = (r,g,b,255); break
    m = Image.new('L',(w,h),0); d = ImageDraw.Draw(m)
    stroke=max(2,round(h*0.13))
    d.line((stroke//2,stroke//2,w-stroke//2-1,stroke//2),fill=255,width=stroke)
    d.line((w-stroke//2-1,stroke//2,stroke//2,h-stroke//2-1),fill=255,width=stroke)
    d.line((stroke//2,h-stroke//2-1,w-stroke//2-1,h-stroke//2-1),fill=255,width=stroke)
    outline=m.filter(ImageFilter.MaxFilter(5))
    out=Image.new('RGBA',(w,h),(0,0,0,0))
    dark=Image.new('RGBA',(w,h),(20,20,20,0)); dark.putalpha(outline)
    out.alpha_composite(dark)
    fg=Image.new('RGBA',(w,h),face); fg.putalpha(m)
    out.alpha_composite(fg)
    return out


def _build_raster_glyph_library(original_im, spec: dict) -> dict:
    import statistics
    candidates = {}
    for item in spec.get('glyph_rects', []):
        x0,y0,x1,y1 = item['rect']
        glyph = _isolate_explicit_glyph(original_im.crop((x0,y0,x1,y1)), spec['face_mode'], int(spec.get('mask_dilate', 5)))
        if glyph is not None:
            candidates.setdefault(item['char'], []).append(glyph)
    source_rotation = int(spec.get('source_rotation', 0))
    for cell in spec.get('glyph_cells', []):
        x0, y0, x1, y1 = cell['rect']
        crop = original_im.crop((x0, y0, x1, y1))
        if source_rotation:
            crop = crop.rotate(source_rotation, expand=True)
        for ch, glyph in _extract_raster_glyphs(crop, cell['source'], spec['face_mode'], int(spec.get('mask_dilate', 5))):
            candidates.setdefault(ch, []).append(glyph)
    glyphs = {}
    for ch, variants in candidates.items():
        median_width = statistics.median(g.width for g in variants)
        glyphs[ch] = min(variants, key=lambda g: abs(g.width - median_width))
    if spec.get('derive_exclamation'):
        if 'I' not in glyphs or '.' not in glyphs:
            die(f'{spec["id"]}: cannot derive ! without I and . glyphs')
        from PIL import Image
        target_h = int(spec['target_height'])
        gi, gp = glyphs['I'], glyphs['.']
        scale_i = (target_h * 0.70) / gi.height
        stem = gi.resize((max(1, round(gi.width * scale_i)), max(1, round(gi.height * scale_i))), Image.Resampling.LANCZOS)
        scale_p = (target_h * 0.16) / gp.height
        dot = gp.resize((max(1, round(gp.width * scale_p)), max(1, round(gp.height * scale_p))), Image.Resampling.LANCZOS)
        width = max(stem.width, dot.width) + 4
        bang = Image.new('RGBA', (width, target_h), (0, 0, 0, 0))
        bang.alpha_composite(stem, ((width - stem.width) // 2, 0))
        bang.alpha_composite(dot, ((width - dot.width) // 2, target_h - dot.height))
        glyphs['!'] = bang
    needed = set(''.join(r['to'] for r in spec['replacements'])) - {' '}
    if 'Z' in needed and 'Z' not in glyphs and spec.get('synthesize_z'):
        glyphs['Z'] = _synth_z_from_glyphs(glyphs, int(spec['target_height']))
    synth_outline = spec.get('synthetic_outline_rgba')
    for ch in spec.get('synthesize_chars', []):
        if ch in needed and ch not in glyphs:
            glyphs[ch] = _synth_basic_char_from_glyphs(glyphs, ch, int(spec['target_height']), synth_outline)
    missing = sorted(needed - set(glyphs))
    if missing:
        die(f'{spec["id"]}: missing raster glyphs: {missing}')
    return glyphs


def _compose_raster_label(glyphs: dict, text: str, target_h: int, gap: int, space_width: int):
    from PIL import Image
    pieces = []
    for ch in text:
        if ch == ' ':
            pieces.append(None)
            continue
        glyph = glyphs[ch]
        scale = target_h / glyph.height
        glyph = glyph.resize((max(1, round(glyph.width * scale)), target_h), Image.Resampling.LANCZOS)
        pieces.append(glyph)
    width = sum(space_width if p is None else p.width for p in pieces) + gap * max(0, len(pieces) - 1)
    out = Image.new('RGBA', (max(1, width), target_h), (0, 0, 0, 0))
    x = 0
    for i, piece in enumerate(pieces):
        if piece is None:
            x += space_width
        else:
            out.alpha_composite(piece, (x, 0))
            x += piece.width
        if i != len(pieces) - 1:
            x += gap
    box = out.getbbox()
    return out.crop(box) if box else out


def _dominant_rgba(crop):
    """Return a stable dominant background color for a small label cell.

    Colors are quantized before counting so compression/antialias noise does not
    defeat the mode. This is intended only for cells whose background is flat
    (transparent or solid black); background-bearing artwork uses a dedicated
    renderer instead.
    """
    from collections import Counter
    im = crop.convert('RGBA')
    counts = Counter()
    for r, g, b, a in im.getdata():
        q = (r // 8 * 8, g // 8 * 8, b // 8 * 8, a // 8 * 8)
        counts[q] += 1
    r, g, b, a = counts.most_common(1)[0][0]
    # Preserve canonical transparent black exactly when alpha quantizes near 0.
    if a < 16:
        return (0, 0, 0, 0)
    return (r, g, b, min(255, a + 7))


def render_multiline_raster_labels_v1(original_im, spec: dict):
    """Recompose transparent/flat-background two-line UI labels.

    Each replacement supplies a full destination rect plus a list of one or
    more lines. Glyphs come only from shipped raster donors in the same atlas.
    This is used for room-name assets where the possessive name and ROOM are
    laid out on separate lines.
    """
    from PIL import Image
    glyphs = _build_raster_glyph_library(original_im, spec)
    out = original_im.copy()
    gap = int(spec.get('gap', -2))
    space_width = int(spec.get('space_width', 14))
    line_gap = int(spec.get('line_gap', -6))
    target_heights = spec.get('line_target_heights')
    default_h = int(spec.get('target_height', 54))
    clear_mode = spec.get('clear_mode', 'transparent')

    for item in spec['replacements']:
        x0, y0, x1, y1 = item['rect']
        if clear_mode == 'transparent':
            bg = (0, 0, 0, 0)
        elif clear_mode == 'dominant':
            bg = _dominant_rgba(original_im.crop((x0, y0, x1, y1)))
        else:
            die(f'{spec["id"]}: unsupported multiline clear_mode {clear_mode!r}')
        out.paste(Image.new('RGBA', (x1-x0, y1-y0), bg), (x0, y0))
        lines = item.get('lines') or [item['to']]
        rendered = []
        for i, line in enumerate(lines):
            th = int(target_heights[i] if target_heights and i < len(target_heights) else default_h)
            rendered.append(_compose_raster_label(glyphs, line, th, gap, space_width))
        total_h = sum(im.height for im in rendered) + line_gap * max(0, len(rendered)-1)
        max_w = max((im.width for im in rendered), default=1)
        box_w = max(1, x1-x0-4); box_h = max(1, y1-y0-4)
        scale = min(1.0, box_w/max_w, box_h/max(1,total_h))
        if scale < 1.0:
            rendered = [im.resize((max(1,round(im.width*scale)), max(1,round(im.height*scale))), Image.Resampling.LANCZOS) for im in rendered]
            total_h = sum(im.height for im in rendered) + line_gap * max(0, len(rendered)-1)
        yy = y0 + (y1-y0-total_h)//2
        for im in rendered:
            xx = x0 + (x1-x0-im.width)//2
            out.alpha_composite(im, (xx, yy))
            yy += im.height + line_gap
    return out


def render_raster_atlas_v1(original_im, spec: dict):
    from PIL import Image
    glyphs = _build_raster_glyph_library(original_im, spec)
    out = original_im.copy()
    target_h = int(spec['target_height'])
    gap = int(spec.get('gap', -2))
    space_width = int(spec.get('space_width', max(6, target_h // 4)))
    clear_mode = spec.get('clear_mode', 'transparent')
    for item in spec['replacements']:
        x0, y0, x1, y1 = item['rect']
        cx0, cy0, cx1, cy1 = item.get('clear_rect', item['rect'])
        rx0, ry0, rx1, ry1 = item.get('render_rect', item['rect'])
        if clear_mode == 'transparent':
            bg = (0, 0, 0, 0)
        elif clear_mode == 'dominant':
            bg = _dominant_rgba(original_im.crop((cx0, cy0, cx1, cy1)))
        else:
            die(f'{spec["id"]}: unsupported clear_mode {clear_mode!r}')
        out.paste(Image.new('RGBA', (cx1 - cx0, cy1 - cy0), bg), (cx0, cy0))
        label = _compose_raster_label(glyphs, item['to'], target_h, gap, space_width)
        output_rotation = int(item.get('output_rotation', spec.get('output_rotation', 0)))
        if output_rotation:
            label = label.rotate(output_rotation, expand=True)
        max_w = max(1, rx1 - rx0 - 4)
        max_h = max(1, ry1 - ry0 - 4)
        scale = min(1.0, max_w / label.width, max_h / label.height)
        if scale < 1.0:
            label = label.resize((max(1, round(label.width * scale)), max(1, round(label.height * scale))), Image.Resampling.LANCZOS)
        out.alpha_composite(label, (rx0 + (rx1 - rx0 - label.width) // 2, ry0 + (ry1 - ry0 - label.height) // 2))
    return out




def render_font_atlas_v2(original_im, spec: dict):
    """Rebuild selected baked UI labels from a pinned font face, never raster cutouts.

    This renderer is intentionally conservative: it only clears declared replacement
    rectangles, then draws the canonical replacement text with a source-matched
    typography preset.  It supports transparent/flat backgrounds and 90-degree
    atlas rotations while keeping every pixel outside the declared rectangles
    byte-identical at the decoded RGBA level.
    """
    from PIL import Image
    out = original_im.copy().convert('RGBA')
    preset = _ui_preset(spec['typography_preset'])
    allow = []
    clear_mode = spec.get('clear_mode', 'transparent')
    for item in spec['replacements']:
        cx0, cy0, cx1, cy1 = map(int, item.get('clear_rect', item['rect']))
        rx0, ry0, rx1, ry1 = map(int, item.get('render_rect', item['rect']))
        clear_rect = (cx0, cy0, cx1, cy1)
        allow.append(clear_rect)
        if clear_mode == 'transparent':
            _clear_rgba_rect_with_bleed(out, clear_rect)
        elif clear_mode == 'dominant':
            bg = _dominant_rgba(original_im.crop(clear_rect))
            out.paste(Image.new('RGBA', (cx1-cx0, cy1-cy0), bg), (cx0, cy0))
        elif clear_mode == 'rgba':
            rgba = tuple(map(int, spec.get('clear_rgba', [0,0,0,0])))
            out.paste(Image.new('RGBA', (cx1-cx0, cy1-cy0), rgba), (cx0, cy0))
        else:
            die(f'{spec["id"]}: unsupported font-atlas clear_mode {clear_mode!r}')

        text = item.get('text', item.get('to'))
        if not text:
            die(f'{spec["id"]}: font-atlas replacement has no text')
        rotation = int(item.get('output_rotation', spec.get('output_rotation', 0))) % 360
        box_w, box_h = max(1, rx1-rx0), max(1, ry1-ry0)
        local = dict(preset)
        if 'font_nominal_size' in spec:
            local['nominal_size'] = int(spec['font_nominal_size'])
        if 'font_min_size' in spec:
            local['min_size'] = int(spec['font_min_size'])
        if rotation in (90, 270):
            label = _make_layered_ui_label((box_h, box_w), text, local, bleed_radius=int(spec.get('bleed_radius', 6)))
            label = label.rotate(rotation, expand=True, resample=Image.Resampling.BICUBIC)
        else:
            label = _make_layered_ui_label((box_w, box_h), text, local, bleed_radius=int(spec.get('bleed_radius', 6)))
            if rotation:
                label = label.rotate(rotation, expand=True, resample=Image.Resampling.BICUBIC)

        if label.width > box_w or label.height > box_h:
            scale = min(box_w/max(1,label.width), box_h/max(1,label.height))
            label = label.resize((max(1,round(label.width*scale)), max(1,round(label.height*scale))), Image.Resampling.LANCZOS)
        x = rx0 + (box_w-label.width)//2 + int(item.get('offset_x', spec.get('offset_x', 0)))
        y = ry0 + (box_h-label.height)//2 + int(item.get('offset_y', spec.get('offset_y', 0)))
        out.alpha_composite(label, (x, y))
    _assert_diff_inside_rects(original_im, out, allow, spec['id'])
    return out


def _recolor_selected_glyph(glyph):
    from PIL import Image, ImageFilter
    src = glyph.convert('RGBA')
    # Recover the bright face from the normal white-italic donor, then recreate
    # the selected variant as a black face with a compact white outline.
    face = Image.new('L', src.size, 0)
    fp = face.load(); sp = src.load()
    for y in range(src.height):
        for x in range(src.width):
            r,g,b,a = sp[x,y]
            if a > 30 and r > 150 and g > 150 and b > 150:
                fp[x,y] = 255
    if face.getbbox() is None:
        face = src.getchannel('A')
    outline = face.filter(ImageFilter.MaxFilter(5))
    out = Image.new('RGBA', src.size, (0,0,0,0))
    ol = Image.new('RGBA', src.size, (255,255,255,0)); ol.putalpha(outline)
    out.alpha_composite(ol)
    fg = Image.new('RGBA', src.size, (5,5,5,0)); fg.putalpha(face)
    out.alpha_composite(fg)
    box = out.getbbox()
    return out.crop(box) if box else out


def _selector_text_mask(crop):
    from PIL import Image, ImageFilter
    src = crop.convert('RGBA')
    mask = Image.new('L', src.size, 0)
    mp=mask.load(); sp=src.load()
    for y in range(src.height):
        for x in range(src.width):
            r,g,b,a=sp[x,y]
            # Selected labels have a near-black face on a cyan/white background.
            if a > 30 and max(r,g,b) < 85:
                mp[x,y]=255
    return mask.filter(ImageFilter.MaxFilter(11))


def _clean_selector_selected_background(row_crops, row_index):
    from PIL import Image
    base = row_crops[row_index].convert('RGBA').copy()
    masks = [_selector_text_mask(c) for c in row_crops]
    bm = masks[row_index]
    bp=base.load(); bmp=bm.load()
    donors=[c.convert('RGBA') for c in row_crops]
    dps=[c.load() for c in donors]; mps=[m.load() for m in masks]
    w,h=base.size
    for y in range(h):
        for x in range(w):
            if bmp[x,y] == 0:
                continue
            chosen=None
            for j in range(len(donors)):
                if j == row_index:
                    continue
                if mps[j][x,y] == 0:
                    chosen=dps[j][x,y]
                    break
            if chosen is not None:
                bp[x,y]=chosen
    return base


def render_ms2_myroom_tsuu_direct_v1(original_im, spec: dict):
    """Direct raster reuse for OTSUU'S ROOM -> TSUU'S ROOM.

    This asset needs only removal of the leading O. Reusing the shipped raster
    subimages avoids unnecessary font reconstruction and keeps the original
    blue/white treatment intact. Minor line overlap/edge polish is deferred.
    """
    from PIL import Image
    out = original_im.copy()
    x0,y0,x1,y1 = spec['clear_rect']
    out.paste(Image.new('RGBA',(x1-x0,y1-y0),(0,0,0,0)),(x0,y0))
    parts=[]
    for key in ('top_source_rect','room_source_rect'):
        a,b,c,d = spec[key]
        part = original_im.crop((a,b,c,d))
        box = part.getbbox()
        if box:
            part = part.crop(box)
        parts.append(part)
    top,room = parts
    for part,yy in ((top,int(spec['top_y'])),(room,int(spec['room_y']))):
        xx = x0 + (x1-x0-part.width)//2
        out.alpha_composite(part,(xx,yy))
    return out


def render_ms2_living_quarters_selector_v1(original_im, spec: dict):
    from PIL import Image
    # Build a glyph bank only from the normal white-italic labels. These rows
    # collectively contain every Latin glyph required by the canonical names.
    glyphs = _build_raster_glyph_library(original_im, spec)
    selected_glyphs = {ch: _recolor_selected_glyph(g) for ch,g in glyphs.items()}
    out = original_im.copy()

    normal_x0, normal_x1 = spec['normal_x_range']
    sel_x0, sel_x1 = spec['selected_x_range']
    row_rects = spec['row_rects']
    selected_rows = [original_im.crop((sel_x0,y0,sel_x1,y1)) for y0,y1 in row_rects]
    # Donor-row reconstruction requires aligned crops. Manifest rows are kept
    # equal-height for this renderer; fail early instead of silently sampling
    # mismatched coordinates.
    sizes={c.size for c in selected_rows}
    if len(sizes) != 1:
        die(f'{spec["id"]}: selector row crops must have equal sizes, got {sorted(sizes)}')

    target_h = int(spec.get('target_height', 46))
    gap = int(spec.get('gap', -1))
    space_width = int(spec.get('space_width', 13))
    for item in spec['replacements']:
        idx=int(item['row'])
        y0,y1=row_rects[idx]
        # Normal variant: transparent field, so full cleanup is safe.
        out.paste(Image.new('RGBA',(normal_x1-normal_x0,y1-y0),(0,0,0,0)),(normal_x0,y0))
        lab=_compose_raster_label(glyphs,item['to'],target_h,gap,space_width)
        maxw=normal_x1-normal_x0-8; maxh=y1-y0-6
        scale=min(1.0,maxw/lab.width,maxh/lab.height)
        if scale<1.0:
            lab=lab.resize((max(1,round(lab.width*scale)),max(1,round(lab.height*scale))),Image.Resampling.LANCZOS)
        out.alpha_composite(lab,(normal_x0+(normal_x1-normal_x0-lab.width)//2,y0+(y1-y0-lab.height)//2))

        # Selected/highlight variant: reconstruct the cyan/white row background
        # from sibling rows before placing the selected black/white label.
        clean=_clean_selector_selected_background(selected_rows,idx)
        out.paste(clean,(sel_x0,y0))
        slab=_compose_raster_label(selected_glyphs,item['to'],target_h,gap,space_width)
        maxw=sel_x1-sel_x0-8; maxh=y1-y0-6
        scale=min(1.0,maxw/slab.width,maxh/slab.height)
        if scale<1.0:
            slab=slab.resize((max(1,round(slab.width*scale)),max(1,round(slab.height*scale))),Image.Resampling.LANCZOS)
        out.alpha_composite(slab,(sel_x0+(sel_x1-sel_x0-slab.width)//2,y0+(y1-y0-slab.height)//2))
    return out


def _synth_ms2_room_apostrophe(height: int = 60):
    """Small apostrophe matching the compact donor weight.

    menu_job.tid supplies every required uppercase Latin letter but no apostrophe.
    Keep the fallback explicit and local to the room-list family; the shared
    polish pass may later replace it with a manually refined shipped-raster cut.
    """
    from PIL import Image, ImageDraw
    h=max(24,int(height)); w=max(10,round(h*0.30))
    out=Image.new('RGBA',(w,h),(0,0,0,0))
    d=ImageDraw.Draw(out)
    # Slanted black-backed tick; the family cyan outline is applied later.
    d.polygon([(round(w*.28),round(h*.05)),(round(w*.90),round(h*.05)),
               (round(w*.72),round(h*.34)),(round(w*.17),round(h*.34))], fill=(0,0,0,255))
    d.polygon([(round(w*.39),round(h*.08)),(round(w*.79),round(h*.08)),
               (round(w*.62),round(h*.27)),(round(w*.28),round(h*.27))], fill=(245,245,245,255))
    return out


def _add_rgba_outline(label, rgba, radius: int):
    from PIL import Image, ImageFilter
    radius=max(1,int(radius))
    # MaxFilter requires an odd kernel size.
    kernel=radius if radius % 2 else radius+1
    pad=max(2,kernel//2+2)
    out=Image.new('RGBA',(label.width+2*pad,label.height+2*pad),(0,0,0,0))
    alpha=label.getchannel('A')
    dilated=alpha.filter(ImageFilter.MaxFilter(kernel))
    mask=Image.new('L',out.size,0); mask.paste(dilated,(pad,pad))
    layer=Image.new('RGBA',out.size,tuple(rgba))
    out.alpha_composite(Image.composite(layer,Image.new('RGBA',out.size,(0,0,0,0)),mask))
    out.alpha_composite(label,(pad,pad))
    return out


def render_ms2_heroine_room_list_v1(original_im, donor_im, spec: dict):
    """Rebuild menu_heroineroom.tid from a clean donor alphabet.

    Earlier attempts tried to segment the connected cyan-outline glyphs directly
    from this texture and produced visibly damaged letters. Borrow
    the already-clean, all-caps white raster alphabet from the adjacent shipped
    menu_job.tid atlas, adds one explicitly tracked apostrophe fallback, and
    reconstructs the cyan outer treatment around the composed labels. Only the
    six localized-name rectangles are cleared/repainted; every other pixel must
    remain byte-equivalent at the decoded RGBA level before texture encoding.
    """
    from PIL import Image
    import numpy as np

    donor_spec={
        'id': spec['id'] + '_DONOR',
        'face_mode': spec.get('donor_face_mode','white'),
        'target_height': int(spec.get('name_target_height',58)),
        'gap': int(spec.get('gap',-1)),
        'space_width': int(spec.get('space_width',12)),
        'mask_dilate': int(spec.get('donor_mask_dilate',3)),
        'glyph_cells': spec['donor_glyph_cells'],
        # Exclude the apostrophe from the donor requirement; it is synthesized
        # explicitly below. ROOM is included because it is composed separately.
        'replacements': [
            {'from':'', 'to': (item['name'].replace("'",'') + item.get('room','ROOM')),
             'rect':[0,0,1,1]}
            for item in spec['replacements']
        ],
    }
    glyphs=_build_raster_glyph_library(donor_im,donor_spec)
    glyphs["'"]=_synth_ms2_room_apostrophe(int(spec.get('apostrophe_canvas_height',60)))

    out=original_im.copy()
    name_h=int(spec.get('name_target_height',58))
    room_h=int(spec.get('room_target_height',54))
    gap=int(spec.get('gap',-1)); space_width=int(spec.get('space_width',12))
    outline_rgba=tuple(spec.get('outline_rgba',[0,180,255,255]))
    outline_radius=int(spec.get('outline_radius',7))
    name_x=int(spec.get('name_x_offset',4)); name_y=int(spec.get('name_y_offset',2))
    room_y=int(spec.get('room_y_offset',62)); room_margin=int(spec.get('room_right_margin',12))

    allow=np.zeros((original_im.height,original_im.width),dtype=bool)
    for item in spec['replacements']:
        x0,y0,x1,y1=map(int,item['rect'])
        allow[y0:y1,x0:x1]=True
        out.paste(Image.new('RGBA',(x1-x0,y1-y0),(0,0,0,0)),(x0,y0))

        name=_compose_raster_label(glyphs,item['name'],name_h,gap,space_width)
        name=_add_rgba_outline(name,outline_rgba,outline_radius)
        max_name_w=max(1,x1-x0-2*name_x)
        if name.width>max_name_w:
            name=fit_label(name,max_name_w,min(72,y1-y0))
        room=_compose_raster_label(glyphs,item.get('room','ROOM'),room_h,gap,space_width)
        room=_add_rgba_outline(room,outline_rgba,outline_radius)

        nx=x0+name_x; ny=y0+name_y
        rx=x1-room.width-room_margin; ry=y0+room_y
        if nx< x0 or ny<y0 or nx+name.width>x1 or ny+name.height>y1:
            die(f'{spec["id"]}: name label does not fit target rect {item["to"]}')
        if rx<x0 or ry<y0 or rx+room.width>x1 or ry+room.height>y1:
            die(f'{spec["id"]}: ROOM label does not fit target rect {item["to"]}')
        out.alpha_composite(name,(nx,ny)); out.alpha_composite(room,(rx,ry))

    before=np.asarray(original_im); after=np.asarray(out)
    diff=np.any(before!=after,axis=2)
    outside=int(np.count_nonzero(diff & ~allow))
    if outside:
        die(f'{spec["id"]}: changed {outside} decoded pixels outside declared replacement rectangles')
    return out


def _load_label_asset(relpath: str):
    from PIL import Image
    p = LABEL_ASSET_ROOT / relpath
    if not p.is_file():
        die(f'missing canonical label asset: {p.relative_to(ROOT)}')
    im = Image.open(p).convert('RGBA')
    im.load()
    return im


def _fit_bitmap_label(label, max_w: int, max_h: int):
    from PIL import Image
    scale = min(1.0, max_w / max(1, label.width), max_h / max(1, label.height))
    if scale < 1.0:
        label = label.resize(
            (max(1, round(label.width * scale)), max(1, round(label.height * scale))),
            Image.Resampling.LANCZOS,
        )
    return label



def _load_ui_typography() -> dict:
    global _UI_TYPO_CACHE
    if _UI_TYPO_CACHE is None:
        if not UI_TYPOGRAPHY.is_file():
            die(f'missing UI typography config: {UI_TYPOGRAPHY.relative_to(ROOT)}')
        _UI_TYPO_CACHE = json.loads(UI_TYPOGRAPHY.read_text(encoding='utf-8'))
    return _UI_TYPO_CACHE


def _ui_font_cfg(face_name: str = 'italic') -> dict:
    cfg=_load_ui_typography()
    fonts=cfg.get('fonts')
    if fonts:
        if face_name not in fonts:
            die(f'unknown UI font face {face_name!r}')
        return fonts[face_name]
    return cfg['font']


def _resolve_ui_font_path(face_name: str = 'italic') -> Path:
    """Resolve the exact pinned UI face, preferring the repository-vendored copy."""
    import hashlib, zipfile
    cfg = _ui_font_cfg(face_name)
    env_name = cfg.get('env_override', 'MARY_SKELTER_UI_FONT')
    expected = str(cfg.get('reference_sha256','')).lower()
    base = cfg['preferred_basename']
    repo_rel = cfg.get('repo_path')
    candidates = []
    if repo_rel:
        repo_candidate = ROOT / repo_rel
        if repo_candidate.is_file() and (not expected or hashlib.sha256(repo_candidate.read_bytes()).hexdigest().lower() == expected):
            return repo_candidate
        candidates.append(repo_candidate)
    if os.environ.get(env_name):
        candidates.append(Path(os.environ[env_name]).expanduser())
    vendor = ROOT / '.local' / 'graphics_vendor' / 'fonts' / base
    candidates.extend([
        vendor,
        Path.home() / '.local' / 'share' / 'fonts' / base,
        Path('/usr/share/fonts/opentype') / base,
        Path('/usr/share/fonts/truetype') / base,
        Path('/usr/local/share/fonts') / base,
    ])
    fc = shutil.which('fc-match')
    if fc:
        try:
            got = subprocess.run(
                [fc, '-f', '%{file}\n', cfg.get('fontconfig_pattern', cfg['family'])],
                check=True, capture_output=True, text=True,
            ).stdout.strip().splitlines()
            candidates.extend(Path(x) for x in got if x.strip())
        except Exception:
            pass

    def valid(path: Path) -> bool:
        if not path.is_file(): return False
        if not expected: return True
        return hashlib.sha256(path.read_bytes()).hexdigest().lower() == expected

    for path in candidates:
        if valid(path): return path

    archive_name=cfg.get('source_archive_basename'); member=cfg.get('source_archive_member')
    if archive_name and member:
        for zpath in (Path.home()/'Downloads'/archive_name, ROOT/'.local'/archive_name):
            if not zpath.is_file(): continue
            try:
                with zipfile.ZipFile(zpath) as zf: data=zf.read(member)
                if expected and hashlib.sha256(data).hexdigest().lower()!=expected: continue
                vendor.parent.mkdir(parents=True,exist_ok=True); vendor.write_bytes(data)
                return vendor
            except Exception:
                pass
    die(f"UI font not found or SHA mismatch: {cfg['postscript_name']}. Expected vendored path {repo_rel!r}; optional override: {env_name}.")


def _ui_font(size: int, face_name: str = 'italic'):
    from PIL import ImageFont
    path = _resolve_ui_font_path(face_name)
    key = (str(path), int(size))
    if key not in _UI_FONT_CACHE:
        _UI_FONT_CACHE[key] = ImageFont.truetype(str(path), int(size))
    return _UI_FONT_CACHE[key]


def _ui_preset(name: str) -> dict:
    presets = _load_ui_typography().get('presets', {})
    if name not in presets:
        die(f'unknown UI typography preset {name!r}')
    return presets[name]


def _fit_ui_font(text: str, max_w: int, max_h: int, nominal: int, min_size: int, stroke_width: int = 0, face_name: str = 'italic'):
    from PIL import Image, ImageDraw
    draw = ImageDraw.Draw(Image.new('L', (4, 4), 0))
    for size in range(int(nominal), int(min_size) - 1, -1):
        font = _ui_font(size, face_name)
        box = draw.textbbox((0, 0), text, font=font, stroke_width=int(stroke_width))
        if box[2] - box[0] <= max_w and box[3] - box[1] <= max_h:
            return font, box, size
    die(f'UI text cannot fit target rectangle: {text!r} ({max_w}x{max_h})')


def _clear_rgba_rect_with_bleed(out, rect, rgb=(0, 0, 0)):
    from PIL import Image
    x0, y0, x1, y1 = map(int, rect)
    out.paste(Image.new('RGBA', (x1-x0, y1-y0), tuple(map(int, rgb)) + (0,)), (x0, y0))


def _bleed_transparent_rgb(im, radius: int = 8):
    from PIL import Image
    """Propagate edge RGB into transparent pixels without changing alpha.

    BC3 compresses colour independently from alpha. Leaving arbitrary cyan/black
    RGB immediately outside antialiased glyph edges creates the dirty JPEG-like
    halos around transparent glyph edges. This deterministic nearest-ring
    propagation gives the compressor edge-compatible colour while preserving the
    exact alpha mask.
    """
    import numpy as np
    arr=np.asarray(im.convert('RGBA')).copy()
    alpha=arr[...,3]
    known=alpha > 0
    rgb=arr[...,:3].astype(np.uint16)
    h,w=known.shape
    for _ in range(max(0,int(radius))):
        fill=~known
        if not fill.any(): break
        sums=np.zeros((h,w,3),dtype=np.uint32)
        cnt=np.zeros((h,w),dtype=np.uint16)
        for dy,dx in ((-1,0),(1,0),(0,-1),(0,1),(-1,-1),(-1,1),(1,-1),(1,1)):
            ys0=max(0,-dy); ys1=min(h,h-dy)
            xs0=max(0,-dx); xs1=min(w,w-dx)
            yd0=ys0+dy; yd1=ys1+dy
            xd0=xs0+dx; xd1=xs1+dx
            km=known[ys0:ys1,xs0:xs1]
            sums[yd0:yd1,xd0:xd1] += rgb[ys0:ys1,xs0:xs1] * km[...,None]
            cnt[yd0:yd1,xd0:xd1] += km
        take=fill & (cnt>0)
        if not take.any(): break
        rgb[take]=(sums[take]//cnt[take,None]).astype(np.uint16)
        known[take]=True
    arr[...,:3]=np.clip(rgb,0,255).astype(np.uint8)
    return Image.fromarray(arr,'RGBA')


def _transform_ui_text_layer(layer, preset: dict, target_w: int):
    """Apply declarative geometry after rasterizing the configured source face.

    The English UI frequently uses a normal font face with non-uniform scaling
    and a mild oblique transform.  Applying those transforms after rasterizing
    keeps glyph height stable while allowing long canonical names to compress
    horizontally instead of shrinking vertically.
    """
    from PIL import Image
    sx=float(preset.get('horizontal_scale',1.0))
    min_sx=float(preset.get('min_horizontal_scale',sx))
    sy=float(preset.get('vertical_scale',1.0))
    if sx <= 0 or min_sx <= 0 or sy <= 0:
        die('UI typography scales must be positive')
    nw=max(1,round(layer.width*sx)); nh=max(1,round(layer.height*sy))
    layer=layer.resize((nw,nh),Image.Resampling.LANCZOS)
    max_w=max(1,int(target_w))
    if layer.width>max_w:
        fit=max_w/layer.width
        # Initial font fitting guarantees that the requested minimum scale is
        # sufficient, so this should not cross below the declarative floor.
        allowed=max(min_sx/sx,fit)
        if fit < min_sx/sx - 1e-6:
            die(f'UI transformed text exceeds target width at min_horizontal_scale: {layer.width}>{max_w}')
        layer=layer.resize((max(1,round(layer.width*fit)),layer.height),Image.Resampling.LANCZOS)
    shear=float(preset.get('shear_x',0.0))
    if abs(shear)>1e-6:
        extra=max(2,int(abs(shear)*layer.height)+4)
        # Keep the same transform convention used by the source-match measurements.
        # Negative values produce the right-leaning oblique used by the room UI.
        transformed=layer.transform(
            (layer.width+extra,layer.height),Image.Transform.AFFINE,
            (1,-shear,-extra//2,0,1,0),resample=Image.Resampling.BICUBIC,
        )
        bbox=transformed.getbbox()
        if bbox: transformed=transformed.crop(bbox)
        layer=transformed
    rotation=float(preset.get('rotation_degrees',0.0))
    if abs(rotation)>1e-6:
        layer=layer.rotate(rotation,resample=Image.Resampling.BICUBIC,expand=True)
        bbox=layer.getbbox()
        if bbox: layer=layer.crop(bbox)
    return layer




def _make_avionic_raw_label(text: str, max_w: int, max_h: int, preset: dict):
    """Render Avionic room labels without the demo-font apostrophe watermark.

    The supplied Avionic face replaces apostrophe-like codepoints with a DEMO
    mark. Room labels need a normal apostrophe, so this path draws all letter
    runs with Avionic and inserts a tiny slanted punctuation mark explicitly.
    No other glyph is substituted.
    """
    from PIL import Image, ImageDraw
    face=preset.get('font_face','avionic')
    stroke=int(preset.get('stroke_width',0))
    nominal=int(preset['nominal_size']); minimum=int(preset.get('min_size',12))
    fill=tuple(preset['fill_rgba']); stroke_fill=tuple(preset.get('stroke_rgba',fill))
    parts=text.split("'")
    # Only the room selector needs one apostrophe. Fall back if the string shape changes.
    if len(parts)!=2:
        return None
    left,right=parts
    probe=ImageDraw.Draw(Image.new('L',(4,4),0))
    chosen=None
    for size in range(nominal,minimum-1,-1):
        font=_ui_font(size,face)
        lw=float(probe.textlength(left,font=font)); rw=float(probe.textlength(right,font=font))
        apost_w=max(3,round(size*0.11)); gap=max(2,round(size*0.035))
        box=probe.textbbox((0,0),left+right,font=font,stroke_width=stroke,anchor='lt')
        h=box[3]-box[1]
        total=round(lw+rw+apost_w+2*gap)+2*stroke
        if total<=max_w and h+2*stroke<=max_h:
            chosen=(font,size,lw,rw,apost_w,gap,h); break
    if chosen is None:
        die(f'Avionic room text cannot fit target rectangle: {text!r} ({max_w}x{max_h})')
    font,size,lw,rw,apost_w,gap,h=chosen
    pad=max(8,stroke+8)
    raw=Image.new('RGBA',(max(1,round(lw+rw+apost_w+2*gap))+2*pad,h+2*pad),(0,0,0,0))
    d=ImageDraw.Draw(raw)
    kwargs={'font':font,'fill':fill,'anchor':'lt'}
    if stroke:
        kwargs.update(stroke_width=stroke,stroke_fill=stroke_fill)
    y=pad
    d.text((pad,y),left,**kwargs)
    ax=pad+round(lw)+gap
    # Small source-like slanted apostrophe near the cap height.
    ah=max(5,round(h*0.27)); aw=max(3,apost_w); ay=pad+max(0,round(h*0.03))
    outer=[(ax+max(1,aw//3),ay),(ax+aw,ay),(ax+max(1,round(aw*0.66)),ay+ah),(ax,ay+ah)]
    if stroke:
        r=max(1,stroke)
        ob=[(x-r,y-r) for x,y in outer]
        # Expanded simple quad gives the same crisp white-outline logic as the text.
        ob=[(ax,ay-r),(ax+aw+r,ay-r),(ax+max(1,round(aw*0.66))+r,ay+ah+r),(ax-r,ay+ah+r)]
        d.polygon(ob,fill=stroke_fill)
    d.polygon(outer,fill=fill)
    rx=ax+aw+gap
    d.text((rx,y),right,**kwargs)
    bbox=raw.getbbox()
    return raw.crop(bbox) if bbox else raw

def _make_centered_ui_label(size, text: str, preset: dict, *, bleed_radius: int = 8):
    from PIL import Image, ImageDraw
    w0,h0=map(int,size)
    px=int(preset.get('padding_x',0)); py=int(preset.get('padding_y',0))
    stroke=int(preset.get('stroke_width',0))
    sx=float(preset.get('horizontal_scale',1.0)); min_sx=float(preset.get('min_horizontal_scale',sx))
    sy=float(preset.get('vertical_scale',1.0))
    # Fit against the minimum allowed horizontal compression and the requested
    # vertical scale.  This keeps normal labels at one stable source-like height.
    fit_w=max(1,round((w0-2*px)/max(min_sx,1e-6)))
    fit_h=max(1,round((h0-2*py)/max(sy,1e-6)))
    face_name=preset.get('font_face','chiaro')
    raw=None
    if face_name in ('avionic','avionic_regular') and "'" in text and not preset.get('shadow_offset'):
        raw=_make_avionic_raw_label(text,fit_w,fit_h,preset)
    if raw is None:
        font,box,_=_fit_ui_font(
            text,fit_w,fit_h,
            int(preset['nominal_size']),int(preset.get('min_size',12)),stroke,face_name,
        )
        tw,th=box[2]-box[0],box[3]-box[1]
        pad=max(8,stroke+8)
        raw=Image.new('RGBA',(tw+pad*2,th+pad*2),(0,0,0,0))
        draw=ImageDraw.Draw(raw)
        xx=pad-box[0]; yy=pad-box[1]
        shoff=preset.get('shadow_offset')
        if shoff:
            shx,shy=map(int,shoff)
            shstroke=int(preset.get('shadow_stroke_width',stroke))
            col=tuple(preset.get('shadow_rgba',[0,0,0,160]))
            draw.text((xx+shx,yy+shy),text,font=font,fill=col,stroke_width=shstroke,stroke_fill=col)
        kwargs=dict(font=font,fill=tuple(preset['fill_rgba']))
        if stroke:
            kwargs.update(stroke_width=stroke,stroke_fill=tuple(preset['stroke_rgba']))
        draw.text((xx,yy),text,**kwargs)
        bbox=raw.getbbox()
        if bbox: raw=raw.crop(bbox)
    avail=max(1,w0-2*px)
    raw=_transform_ui_text_layer(raw,preset,avail)
    layer=Image.new('RGBA',(w0,h0),(0,0,0,0))
    align=preset.get('align_x','center')
    if align=='left': x=px+int(preset.get('offset_x',0))
    elif align=='right': x=w0-px-raw.width+int(preset.get('offset_x',0))
    else: x=(w0-raw.width)//2+int(preset.get('offset_x',0))
    y=(h0-raw.height)//2+int(preset.get('offset_y',0))
    x=max(0,min(w0-raw.width,x)); y=max(0,min(h0-raw.height,y))
    layer.alpha_composite(raw,(x,y))
    return _bleed_transparent_rgb(layer,bleed_radius)


def _title_footer_text() -> str:
    version=(ROOT / "version.txt").read_text(encoding="utf-8").strip()
    if not version or any(ch in version for ch in "\r\n"):
        die("version.txt is empty or malformed")
    return f"UNBURGER MOD V{version}"


def render_title_footer_chiaro_v1(original_im, spec: dict):
    """Render the public mod/version footer into a proven transparent title strip.

    The title logo pixels are immutable.  The configured target rectangle must be
    completely alpha-clear in the supported source texture; any source drift aborts
    before drawing.
    """
    from PIL import ImageChops
    out=original_im.copy().convert("RGBA")
    x0,y0,x1,y1=map(int,spec["footer_rect"])
    if not (0 <= x0 < x1 <= out.width and 0 <= y0 < y1 <= out.height):
        die(f'{spec["id"]}: footer rectangle outside texture')
    source_crop=original_im.crop((x0,y0,x1,y1)).convert("RGBA")
    if source_crop.getchannel("A").getbbox() is not None:
        die(f'{spec["id"]}: proven title footer strip is no longer alpha-clear')
    preset=_ui_preset(spec.get("typography_preset","title_footer_chiaro"))
    label=_make_centered_ui_label((x1-x0,y1-y0),_title_footer_text(),preset,bleed_radius=4)
    out.alpha_composite(label,(x0,y0))
    diff=ImageChops.difference(original_im.convert("RGBA"),out)
    bbox=diff.getbbox()
    if bbox is None:
        die(f'{spec["id"]}: footer renderer produced no pixel changes')
    if bbox[0] < x0 or bbox[1] < y0 or bbox[2] > x1 or bbox[3] > y1:
        die(f'{spec["id"]}: footer renderer changed pixels outside target rectangle: {bbox}')
    return out


def render_title_copyright_footer_chiaro_v2(original_im, spec: dict):
    """Add the mod/version line to the source-pinned title copyright sprite.

    The original legal artwork is preserved by scaling the complete shipped
    copyright sprite uniformly into the upper portion of its existing UV cell.
    The remaining lower portion receives the versioned Chiaro footer.  Nothing
    outside the configured copyright rectangle may change.
    """
    from PIL import Image, ImageChops

    out = original_im.copy().convert("RGBA")
    x0, y0, x1, y1 = map(int, spec["copyright_rect"])
    if not (0 <= x0 < x1 <= out.width and 0 <= y0 < y1 <= out.height):
        die(f'{spec["id"]}: copyright rectangle outside texture')

    source_crop = original_im.crop((x0, y0, x1, y1)).convert("RGBA")
    w, h = source_crop.size
    legal_scale = float(spec.get("legal_scale", 0.70))
    if not (0.50 <= legal_scale <= 0.85):
        die(f'{spec["id"]}: legal_scale outside safe range')

    legal_w = max(1, int(round(w * legal_scale)))
    legal_h = max(1, int(round(h * legal_scale)))
    legal = source_crop.resize((legal_w, legal_h), Image.Resampling.LANCZOS)

    cell = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    cell.alpha_composite(legal, ((w - legal_w) // 2, 0))

    footer_y = max(0, legal_h - 1)
    footer_h = h - footer_y
    if footer_h < 16:
        die(f'{spec["id"]}: copyright cell leaves too little footer space')

    preset = dict(_ui_preset(spec.get("typography_preset", "title_footer_chiaro")))
    preset["nominal_size"] = int(spec.get("footer_nominal_size", preset.get("nominal_size", 24)))
    preset["min_size"] = int(spec.get("footer_min_size", min(18, preset["nominal_size"])))
    preset["padding_y"] = 0
    label = _make_centered_ui_label((w, footer_h), _title_footer_text(), preset, bleed_radius=3)
    cell.alpha_composite(label, (0, footer_y))

    # Replace the whole source-pinned UV cell so the old legal art does not remain
    # underneath the rescaled copy.
    out.paste(cell, (x0, y0))
    diff = ImageChops.difference(original_im.convert("RGBA"), out)
    bbox = diff.getbbox()
    if bbox is None:
        die(f'{spec["id"]}: copyright footer renderer produced no pixel changes')
    if bbox[0] < x0 or bbox[1] < y0 or bbox[2] > x1 or bbox[3] > y1:
        die(f'{spec["id"]}: copyright footer changed pixels outside target rectangle: {bbox}')
    return out


def _gradient_rgba_from_alpha(alpha, top_rgba, bottom_rgba):
    from PIL import Image
    import numpy as np
    a=np.asarray(alpha.convert('L'))
    h,w=a.shape
    ys=np.where(a>0)[0]
    if len(ys): y0,y1=int(ys.min()),int(ys.max())
    else: y0,y1=0,max(0,h-1)
    den=max(1,y1-y0)
    top=np.array(top_rgba,dtype=np.float32); bot=np.array(bottom_rgba,dtype=np.float32)
    t=np.clip((np.arange(h,dtype=np.float32)-y0)/den,0,1)[:,None]
    cols=top[None,:]*(1-t)+bot[None,:]*t
    arr=np.zeros((h,w,4),dtype=np.uint8)
    arr[:,:,:]=np.clip(cols[:,None,:],0,255).astype(np.uint8)
    arr[:,:,3]=(a.astype(np.uint16)*arr[:,:,3].astype(np.uint16)//255).astype(np.uint8)
    return Image.fromarray(arr,'RGBA')



def _diagonal_three_stop_rgba_from_alpha(alpha, dark_rgba, mid_rgba, blue_rgba):
    """Fill an alpha mask with the shipped MS2 room-title diagonal dark->blue face."""
    from PIL import Image
    import numpy as np
    a=np.asarray(alpha.convert('L'))
    h,w=a.shape
    yy,xx=np.mgrid[0:h,0:w].astype(np.float32)
    nz=np.where(a>0)
    if len(nz[0]):
        y0,y1=float(nz[0].min()),float(nz[0].max())
        x0,x1=float(nz[1].min()),float(nz[1].max())
    else:
        y0,y1,x0,x1=0.0,float(max(1,h-1)),0.0,float(max(1,w-1))
    yn=np.clip((yy-y0)/max(1.0,y1-y0),0,1)
    xn=np.clip((xx-x0)/max(1.0,x1-x0),0,1)
    # Shipped face is nearly black toward the upper/right side and gains
    # progressively more blue toward the lower/left diagonal.
    t=np.clip(0.72*yn + 0.28*(1.0-xn),0,1)
    c0=np.array(dark_rgba,dtype=np.float32)
    c1=np.array(mid_rgba,dtype=np.float32)
    c2=np.array(blue_rgba,dtype=np.float32)
    arr=np.zeros((h,w,4),dtype=np.uint8)
    lo=t<=0.55
    u=np.where(lo,t/0.55,(t-0.55)/0.45)
    cols=np.empty((h,w,4),dtype=np.float32)
    cols[lo]=c0+(c1-c0)*u[lo,None]
    cols[~lo]=c1+(c2-c1)*u[~lo,None]
    arr[:,:,:]=np.clip(cols,0,255).astype(np.uint8)
    arr[:,:,3]=(a.astype(np.uint16)*arr[:,:,3].astype(np.uint16)//255).astype(np.uint8)
    return Image.fromarray(arr,'RGBA')


def _make_ms2_room_entry_label(size, name: str, preset: dict, *, name_target_width: int | None = None, name_offset_y: int = 0, room_vertical_scale: float = 1.0, room_vertical_anchor: str = "top"):
    """Rebuild one two-line MS2 in-room title from Gebryna using shipped blue layering."""
    from PIL import Image, ImageDraw, ImageFilter
    w0,h0=map(int,size)
    outer=int(preset['outer_stroke_width'])
    inner=int(preset['inner_stroke_width'])
    min_size=int(preset.get('min_size',28))
    face_name=preset.get('font_face','gebryna')
    sx=float(preset.get('horizontal_scale',1.0))
    min_sx=float(preset.get('min_horizontal_scale',sx))

    def make_line(text: str, nominal: int, max_w: int, max_h: int):
        # Fit source face first, then apply the same non-uniform horizontal scaling
        # used elsewhere in the graphics pipeline.
        font,box,_=_fit_ui_font(
            text,
            max(1,round(max_w/max(min_sx,1e-6))),
            max_h,
            nominal,min_size,outer,face_name,
        )
        tw,th=box[2]-box[0],box[3]-box[1]
        pad=max(outer+8,12)
        raw=Image.new('RGBA',(tw+pad*2,th+pad*2),(0,0,0,0))
        # Three aligned masks: outer blue edge, inner pale-cyan edge, dark face.
        def mask(stroke):
            m=Image.new('L',raw.size,0)
            d=ImageDraw.Draw(m)
            d.text((pad-box[0],pad-box[1]),text,font=font,fill=255,
                   stroke_width=stroke,stroke_fill=255)
            b=m.getbbox()
            return m.crop(b) if b else m
        fm=mask(0); im=mask(inner); om=mask(outer)
        # Masks may crop to slightly different bounds. Re-render without per-mask crop
        # so all layers share one coordinate system.
        full_face=Image.new('L',raw.size,0); full_inner=Image.new('L',raw.size,0); full_outer=Image.new('L',raw.size,0)
        for mm,sw in ((full_face,0),(full_inner,inner),(full_outer,outer)):
            ImageDraw.Draw(mm).text((pad-box[0],pad-box[1]),text,font=font,fill=255,
                                    stroke_width=sw,stroke_fill=255)
        bbox=full_outer.getbbox()
        if not bbox:
            return Image.new('RGBA',(1,1),(0,0,0,0))
        full_face=full_face.crop(bbox); full_inner=full_inner.crop(bbox); full_outer=full_outer.crop(bbox)

        layer=Image.new('RGBA',full_outer.size,(0,0,0,0))
        # Soft navy depth beneath the outer edge.
        shoff=tuple(map(int,preset.get('shadow_offset',[2,3])))
        shadow=Image.new('RGBA',(layer.width+abs(shoff[0])+6,layer.height+abs(shoff[1])+6),(0,0,0,0))
        sm=full_outer.filter(ImageFilter.GaussianBlur(0.8))
        scol=tuple(preset.get('shadow_rgba',[0,10,42,190]))
        ssolid=Image.new('RGBA',shadow.size,(scol[0],scol[1],scol[2],0))
        sm2=Image.new('L',shadow.size,0); sm2.paste(sm,(max(0,shoff[0]),max(0,shoff[1])))
        if len(scol)>3 and scol[3]!=255:
            sm2=sm2.point(lambda a:(int(a)*int(scol[3])+127)//255)
        ssolid.putalpha(sm2); shadow.alpha_composite(ssolid)

        # Rebase the three masks into the shadow-sized canvas.
        def expanded(mask):
            e=Image.new('L',shadow.size,0); e.paste(mask,(0,0)); return e
        om2,im2,fm2=expanded(full_outer),expanded(full_inner),expanded(full_face)
        outer_col=tuple(preset['outer_stroke_rgba'])
        inner_col=tuple(preset['inner_stroke_rgba'])
        ol=Image.new('RGBA',shadow.size,outer_col); ol.putalpha(om2)
        il=Image.new('RGBA',shadow.size,inner_col); il.putalpha(im2)
        fg=_diagonal_three_stop_rgba_from_alpha(
            fm2,preset['face_dark_rgba'],preset['face_mid_rgba'],preset['face_blue_rgba']
        )
        shadow.alpha_composite(ol); shadow.alpha_composite(il); shadow.alpha_composite(fg)
        bb=shadow.getbbox()
        out=shadow.crop(bb) if bb else shadow
        # Non-uniform horizontal matching.
        nw=max(1,round(out.width*sx))
        out=out.resize((nw,out.height),Image.Resampling.LANCZOS)
        if out.width>max_w:
            f=max_w/out.width
            if f < min_sx/max(sx,1e-6)-1e-6:
                # Last-resort fitting is still preferable to clipping a canonical name.
                f=max(min_sx/max(sx,1e-6),f)
            out=out.resize((max(1,round(out.width*min(1.0,f))),out.height),Image.Resampling.LANCZOS)
        return out

    line1=make_line(name+"'S",int(preset['name_nominal_size']),w0-12,62)
    if name_target_width is not None and line1.width > 0:
        target=max(1,min(int(name_target_width),w0-8))
        if line1.width != target:
            line1=line1.resize((target,line1.height),Image.Resampling.LANCZOS)
    room=make_line("ROOM",int(preset['room_nominal_size']),max(170,round(w0*0.48)),56)
    room_scale=float(room_vertical_scale)
    room_old_h=room.height
    if abs(room_scale-1.0)>1e-6:
        # Keep ROOM width intact.  Runtime showed that shrinking from the top
        # leaves its upper edge colliding with long names, so the heroine-room
        # family may bottom-anchor the height reduction instead.
        room=room.resize((room.width,max(1,round(room.height*room_scale))),Image.Resampling.LANCZOS)

    out=Image.new('RGBA',(w0,h0),(0,0,0,0))
    y1=int(preset.get('line1_y',0))+int(name_offset_y)
    x1=4
    out.alpha_composite(line1,(x1,max(0,y1)))
    rx=max(0,w0-int(preset.get('room_right_margin',10))-room.width)
    ry=int(preset.get('room_y',52))
    if room.height != room_old_h:
        delta=room_old_h-room.height
        if room_vertical_anchor == 'bottom':
            ry += delta
        elif room_vertical_anchor == 'center':
            ry += delta//2
        elif room_vertical_anchor != 'top':
            die(f'unsupported ROOM vertical anchor: {room_vertical_anchor}')
    out.alpha_composite(room,(rx,max(0,ry)))
    return _bleed_transparent_rgb(out,10)


def _offset_alpha(alpha, offset):
    from PIL import Image
    sx,sy=map(int,offset)
    out=Image.new('L',alpha.size,0)
    out.paste(alpha,(sx,sy))
    return out


def _make_layered_ui_label(size, text: str, preset: dict, *, bleed_radius: int = 8):
    """Layered nameplate renderer with optional sampled vertical gradients."""
    from PIL import Image
    # Build aligned transformed masks through the same geometry path as normal UI.
    def mask_for(stroke_width: int):
        q=dict(preset)
        q['fill_rgba']=[255,255,255,255]
        q['stroke_width']=int(stroke_width)
        q['stroke_rgba']=[255,255,255,255]
        q.pop('shadow_offset',None); q.pop('shadow_rgba',None); q.pop('shadow_stroke_width',None)
        q.pop('extrusion_offset',None); q.pop('extrusion_rgba',None); q.pop('extrusion_stroke_width',None)
        lab=_make_centered_ui_label(size,text,q,bleed_radius=0)
        return lab.getchannel('A')

    face=mask_for(0)
    face_stroke=mask_for(int(preset.get('stroke_width',0)))
    extrusion=mask_for(int(preset.get('extrusion_stroke_width',0)))
    shadow=mask_for(int(preset.get('shadow_stroke_width',0)))
    layer=Image.new('RGBA',tuple(map(int,size)),(0,0,0,0))

    if preset.get('shadow_offset'):
        sm=_offset_alpha(shadow,preset['shadow_offset'])
        blur=float(preset.get('shadow_blur_radius',0.0))
        if blur > 0:
            from PIL import ImageFilter
            sm=sm.filter(ImageFilter.GaussianBlur(blur))
        col=tuple(preset.get('shadow_rgba',[0,0,0,220]))
        # Respect the configured shadow alpha. Pillow putalpha() replaces the
        # image alpha channel, so multiply the glyph mask explicitly instead
        # of silently turning a sampled half-alpha shadow fully opaque.
        ca=int(col[3]) if len(col)>3 else 255
        if ca != 255:
            sm=sm.point(lambda a: (int(a)*ca + 127)//255)
        solid=Image.new('RGBA',layer.size,(int(col[0]),int(col[1]),int(col[2]),255)); solid.putalpha(sm)
        layer.alpha_composite(solid)

    if preset.get('extrusion_offset'):
        em=_offset_alpha(extrusion,preset['extrusion_offset'])
        if 'extrusion_gradient_top_rgba' in preset:
            ex=_gradient_rgba_from_alpha(em,preset['extrusion_gradient_top_rgba'],preset['extrusion_gradient_bottom_rgba'])
        else:
            ex=Image.new('RGBA',layer.size,tuple(preset.get('extrusion_rgba',[30,178,244,255]))); ex.putalpha(em)
        layer.alpha_composite(ex)

    sw=int(preset.get('stroke_width',0))
    if sw:
        st=Image.new('RGBA',layer.size,tuple(preset['stroke_rgba'])); st.putalpha(face_stroke)
        layer.alpha_composite(st)

    if 'fill_gradient_top_rgba' in preset:
        fg=_gradient_rgba_from_alpha(face,preset['fill_gradient_top_rgba'],preset['fill_gradient_bottom_rgba'])
    else:
        fg=Image.new('RGBA',layer.size,tuple(preset['fill_rgba'])); fg.putalpha(face)
    layer.alpha_composite(fg)
    return _bleed_transparent_rgb(layer,bleed_radius)

def _draw_centered_ui_text(out, rect, text: str, preset: dict):
    from PIL import ImageDraw
    x0, y0, x1, y1 = map(int, rect)
    px = int(preset.get('padding_x', 0)); py = int(preset.get('padding_y', 0))
    stroke = int(preset.get('stroke_width', 0))
    font, box, used_size = _fit_ui_font(
        text, (x1-x0)-2*px, (y1-y0)-2*py,
        int(preset['nominal_size']), int(preset.get('min_size', 12)), stroke, preset.get('font_face','italic'),
    )
    w, h = box[2]-box[0], box[3]-box[1]
    xx = x0 + ((x1-x0)-w)//2 - box[0]
    yy = y0 + ((y1-y0)-h)//2 - box[1]
    kwargs = dict(font=font, fill=tuple(preset['fill_rgba']))
    if stroke:
        kwargs.update(stroke_width=stroke, stroke_fill=tuple(preset['stroke_rgba']))
    ImageDraw.Draw(out).text((xx, yy), text, **kwargs)
    return used_size


def render_ms2_affection_font_v2(original_im, spec: dict):
    """Build-time font replacement for the full Affection List name family."""
    from PIL import Image
    out=original_im.copy(); allow=[]
    for item in spec['rows']:
        rect=tuple(map(int,item['rect'])); allow.append(rect)
        x0,y0,x1,y1=rect
        bg=_dominant_rgba(original_im.crop(rect))
        out.paste(Image.new('RGBA',(x1-x0,y1-y0),bg),(x0,y0))
        preset=_ui_preset(item.get('typography_preset',spec['typography_preset']))
        lab=_make_centered_ui_label((x1-x0,y1-y0),item['text'],preset,bleed_radius=6)
        out.alpha_composite(lab,(x0,y0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_camp_top_legacy_names_font_v2(original_im, spec: dict):
    """Rebuild the two legacy vertical camp-top names used by Equipment/Status."""
    from PIL import Image
    out=original_im.copy(); allow=[]; preset=_ui_preset(spec['typography_preset'])
    for item in spec['rows']:
        rect=tuple(map(int,item['rect'])); allow.append(rect)
        x0,y0,x1,y1=rect
        # These legacy labels live on transparent atlas space.  Filling the
        # rectangle with the dominant colour creates the black boxes seen in
        # R32C1 previews, so clear only to transparent RGB-bleed instead.
        _clear_rgba_rect_with_bleed(out, rect)
        # Atlas stores these labels 90 degrees CCW from runtime orientation.
        horiz=_make_centered_ui_label((y1-y0,x1-x0),item['text'],preset,bleed_radius=6)
        rot=horiz.transpose(Image.Transpose.ROTATE_90)
        out.alpha_composite(rot,(x0,y0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out




def _r32c3_core():
    import importlib.util
    if not hasattr(_r32c3_core, "_cache"):
        p=ROOT/'toolchain'/'python-src'/'scripts'/'graphics'/'r32c3_typography.py'
        if not p.is_file(): die(f'R32C3 renderer module missing: {p}')
        s=importlib.util.spec_from_file_location('ms_r32c3_core',p)
        mod=importlib.util.module_from_spec(s); s.loader.exec_module(mod)
        _r32c3_core._cache=mod
    return _r32c3_core._cache

def render_ms2_name_family_font_v1(original_im, spec: dict):
    return _r32c3_core().render_family(original_im,spec,_ui_preset,_ui_font,
                                     _assert_diff_inside_rects,_bleed_transparent_rgb)

def render_ms2_result_font_v2(original_im, spec: dict):
    """Rebuild the complete result-screen name family with one layered treatment."""
    from PIL import Image
    out=original_im.copy(); allow=[]; preset=_ui_preset(spec['typography_preset'])
    # Result-name sprites are supplied through rows. Keep the
    # replacement fallback so old archived manifests remain inspectable.
    items=spec.get('rows')
    if items is None:
        items=[{'text':item['to'],'rect':item['rect']} for item in spec['replacements']]
    for item in items:
        x,y,w,h=map(int,item['rect']); rect=(x,y,x+w,y+h); allow.append(rect)
        out.paste(Image.new('RGBA',(w,h),(0,0,0,0)),(x,y))
        lab=_make_layered_ui_label((w,h),item['text'],preset,bleed_radius=7)
        out.alpha_composite(lab,(x,y))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_room_selector_font_v4(original_im, spec: dict):
    """Build-time font renderer for the complete normal+selected room selector."""
    from PIL import Image
    out=original_im.copy(); allow=[]
    normal=_ui_preset(spec['normal_typography_preset'])
    selected=_ui_preset(spec['selected_typography_preset'])
    nx0,nx1=map(int,spec['normal_x_range']); sx0,sx1=map(int,spec['selected_x_range'])
    row_rects=[list(map(int,r)) for r in spec['row_rects']]
    if len(row_rects)!=len(spec['rows']): die(f'{spec["id"]}: row/label count mismatch')
    selected_bg=_load_label_asset(spec['selected_background_asset'])
    expected_bg=(sx1-sx0,row_rects[0][1]-row_rects[0][0])
    if selected_bg.size != expected_bg:
        die(f'{spec["id"]}: selected background asset size {selected_bg.size} != {expected_bg}')
    for i,item in enumerate(spec['rows']):
        y0,y1=row_rects[i]; text=item['text']
        nr=(nx0,y0,nx1,y1); sr=(sx0,y0,sx1,y1); allow.extend([nr,sr])
        # Replace the entire normal text cell so no old glyph fringe survives.
        # Idle rows stay opaque black rather than
        # transparent, matching the shipped selector appearance.
        nl=_make_centered_ui_label((nx1-nx0,y1-y0),text,normal,bleed_radius=10)
        base=Image.new('RGBA',(nx1-nx0,y1-y0),tuple(normal.get('background_rgba',[0,0,0,255])))
        base.alpha_composite(nl)
        out.paste(base,(nx0,y0))
        # Selected background is a clean shipped-derived row without old text.
        out.paste(selected_bg,(sx0,y0))
        sl=_make_centered_ui_label((sx1-sx0,y1-y0),text,selected,bleed_radius=8)
        out.alpha_composite(sl,(sx0,y0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def _make_heroine_room_blue_label(size, name: str, preset: dict):
    from PIL import Image, ImageDraw
    w0,h0=map(int,size)
    layer=Image.new('RGBA',(w0,h0),(0,0,0,0)); draw=ImageDraw.Draw(layer)
    lines=[name+"'S",'ROOM']
    centers=[int(preset['name_center_y']),int(preset['room_center_y'])]
    nominals=[int(preset['name_nominal_size']),int(preset['room_nominal_size'])]
    for text,cy,nominal in zip(lines,centers,nominals):
        outer=int(preset['outer_stroke_width'])
        font,box,_=_fit_ui_font(text,w0-30,60,nominal,int(preset['min_size']),outer,preset.get('font_face','italic'))
        tw,th=box[2]-box[0],box[3]-box[1]
        xx=(w0-tw)//2-box[0]; yy=cy-th//2-box[1]
        draw.text((xx,yy),text,font=font,fill=tuple(preset['fill_rgba']),
                  stroke_width=outer,stroke_fill=tuple(preset['outer_stroke_rgba']))
        draw.text((xx,yy),text,font=font,fill=tuple(preset['fill_rgba']),
                  stroke_width=int(preset['inner_stroke_width']),stroke_fill=tuple(preset['inner_stroke_rgba']))
    return _bleed_transparent_rgb(layer,10)


def render_ms2_heroine_room_blue_font_v4(original_im, spec: dict):
    """Build-time font renderer for all room labels in menu_heroineroom."""
    out=original_im.copy(); allow=[]; preset=_ui_preset(spec['typography_preset'])
    # Clear whole label columns, not just old glyph-sized regions. This removes
    # inherited outline/fringe pixels regardless of old line height.
    clear_regions=spec.get('clear_regions')
    if clear_regions:
        for rect in clear_regions:
            rect=tuple(map(int,rect)); allow.append(rect); _clear_rgba_rect_with_bleed(out,rect)
    for item in spec['rows']:
        rect=tuple(map(int,item['rect'])); allow.append(rect)
        x0,y0,x1,y1=rect
        label=_make_heroine_room_blue_label((x1-x0,y1-y0),item['name'],preset)
        out.paste(label,(x0,y0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out



def render_ms2_heroine_room_blue_font_v5(original_im, spec: dict):
    """Rebuild the complete main-MS2 in-room title family in the actual g1853 Title layer."""
    from PIL import Image
    out=original_im.copy(); allow=[]
    preset=_ui_preset(spec['typography_preset'])
    left_margin=int(spec.get('text_left_margin',78))
    for item in spec['rows']:
        x0,y0,x1,y1=map(int,item['rect'])
        allow.append((x0,y0,x1,y1))
        # Preserve the small left-side shipped ornament/spacing and clear only the
        # baked title area. The room artwork itself is a separate runtime layer.
        cx0=min(x1,x0+left_margin)
        out.paste(Image.new('RGBA',(x1-cx0,y1-y0),(0,0,0,0)),(cx0,y0))
        lab=_make_ms2_room_entry_label((x1-cx0,y1-y0),item['name'],preset)
        out.alpha_composite(lab,(cx0,y0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_heroine_room_blue_font_v6(original_im, spec: dict):
    """Rebuild Gebryna using shipped-width name sizing."""
    from PIL import Image
    out=original_im.copy(); allow=[]
    preset=_ui_preset(spec['typography_preset'])
    left_margin=int(spec.get('text_left_margin',78))
    for item in spec['rows']:
        x0,y0,x1,y1=map(int,item['rect'])
        allow.append((x0,y0,x1,y1))
        cx0=min(x1,x0+left_margin)
        out.paste(Image.new('RGBA',(x1-cx0,y1-y0),(0,0,0,0)),(cx0,y0))
        lab=_make_ms2_room_entry_label(
            (x1-cx0,y1-y0), item['name'], preset,
            name_target_width=int(item['name_target_width']),
            room_vertical_scale=float(spec.get('room_vertical_scale',1.0)),
            room_vertical_anchor=str(spec.get('room_vertical_anchor','top')),
        )
        out.alpha_composite(lab,(cx0,y0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_myroom_title_font_v2(original_im, spec: dict):
    """Rebuild the TSUU'S ROOM title in g1860 with the same Gebryna family as g1853."""
    from PIL import Image
    out=original_im.copy()
    rect=tuple(map(int,spec['clear_rect']))
    x0,y0,x1,y1=rect
    out.paste(Image.new('RGBA',(x1-x0,y1-y0),(0,0,0,0)),(x0,y0))
    preset=_ui_preset(spec['typography_preset'])
    lab=_make_ms2_room_entry_label((x1-x0,y1-y0),spec['name'],preset)
    out.alpha_composite(lab,(x0,y0))
    _assert_diff_inside_rects(original_im,out,[rect],spec['id'])
    return out


def render_ms2_myroom_title_font_v3(original_im, spec: dict):
    """Preserve the established treatment; size TSUU to shipped width and lower only its name line."""
    from PIL import Image
    out=original_im.copy()
    rect=tuple(map(int,spec['clear_rect']))
    x0,y0,x1,y1=rect
    out.paste(Image.new('RGBA',(x1-x0,y1-y0),(0,0,0,0)),(x0,y0))
    preset=_ui_preset(spec['typography_preset'])
    lab=_make_ms2_room_entry_label(
        (x1-x0,y1-y0),spec['name'],preset,
        name_target_width=int(spec['name_target_width']),
        name_offset_y=int(spec.get('name_offset_y',0)),
        room_vertical_scale=float(spec.get('room_vertical_scale',1.0)),
        room_vertical_anchor=str(spec.get('room_vertical_anchor','top')),
    )
    out.alpha_composite(lab,(x0,y0))
    _assert_diff_inside_rects(original_im,out,[rect],spec['id'])
    return out


def _raster_family_library_from_rows(original_im, texts, rects, face_mode: str, mask_dilate: int):
    """Build one deterministic glyph alphabet from shipped row labels."""
    import statistics
    candidates={}
    for text,rect in zip(texts,rects):
        glyphs=_extract_raster_glyphs(original_im.crop(tuple(map(int,rect))),text,face_mode,mask_dilate)
        if len(glyphs)!=len(text.replace(' ','')):
            die(f'raster row segmentation mismatch for {text!r}: {len(glyphs)}')
        for ch,glyph in glyphs:
            candidates.setdefault(ch,[]).append(glyph)
    out={}
    for ch,variants in candidates.items():
        med=statistics.median(g.width/max(1,g.height) for g in variants)
        out[ch]=min(variants,key=lambda g:abs(g.width/max(1,g.height)-med))
    return out


def _face_bbox_for_mode(im, mode: str):
    import numpy as np
    a=np.asarray(im.convert('RGBA'),dtype=np.uint8)
    r,g,b,al=[a[...,i] for i in range(4)]
    if mode=='white': hit=(al>30)&(r>150)&(g>150)&(b>150)
    elif mode=='yellow': hit=(al>30)&(r>180)&(g>120)&(b<120)
    elif mode=='cyan': hit=(al>30)&(g>95)&(b>120)&(b>r*1.10)&((g.astype(int)+b.astype(int))>230)
    else: raise ValueError(mode)
    ys,xs=np.nonzero(hit)
    if len(xs)==0: return None
    return (int(xs.min()),int(ys.min()),int(xs.max()+1),int(ys.max()+1))


def render_ms1_residence_names_raster_v2(original_im, spec: dict):
    """Change only localized Residence rows using explicit shipped glyph runs."""
    from PIL import Image
    out=original_im.copy(); allow=[]
    nr=[tuple(map(int,r)) for r in spec['normal_rects']]
    sr=[tuple(map(int,r)) for r in spec['selected_rects']]
    source=list(spec['source_rows']); target=[r['text'] for r in spec['rows']]
    if not (len(nr)==len(sr)==len(source)==len(target)):
        die(f"{spec['id']}: Residence row count mismatch")

    # The original condensed face has several touching letter pairs, so generic
    # equal-width segmentation is unsafe. These are explicit bright-face runs
    # measured from unmodified shipped labels. Selected rows use the same runs
    # shifted +32 px by the shipped selected-state layout.
    recipe={
        'A': (1,28,46), 'B': (5,166,181), 'D': (2,64,80), 'E': (2,48,62),
        'G': (2,160,176), 'H': (2,186,202), 'I': (2,108,114), 'K': (0,76,91),
        'L': (1,48,61), 'M': (0,176,193), 'N': (4,48,64), 'O': (0,140,156),
        'R': (2,30,45), 'S': (4,30,45), 'T': (12,70,84), 'U': (11,80,96),
        'Y': (13,134,149), 'Z': (7,119,148), "'S": (1,106,125),
    }
    def build_lib(rects,mode,shift):
        lib={}
        for ch,(row,xa,xb) in recipe.items():
            x0,y0,x1,y1=rects[row]
            rr=(x0+xa+shift,y0,x0+xb+shift,y1)
            lib[ch]=_extract_explicit_face_run(original_im,rr,mode,pad=6,core_radius=3,outer_radius=5)
        return lib
    nlib=build_lib(nr,'white',0)
    slib=build_lib(sr,'yellow',32)
    needed=set(''.join(new for old,new in zip(source,target) if old!=new))-{' ',"'"}
    # Apostrophe is carried by the exact shipped 'S ligature.
    for label,lib in (('normal',nlib),('selected',slib)):
        miss=sorted(ch for ch in needed if ch not in lib and ch!='S')
        if miss: die(f"{spec['id']}: {label} shipped raster alphabet missing {miss}")

    nbg=_load_label_asset(spec['normal_background_asset']); sbg=_load_label_asset(spec['selected_background_asset'])
    states=[
        (nr,nlib,nbg,'white',int(spec.get('normal_target_height',30)),int(spec.get('normal_gap',-3)),int(spec.get('normal_space_width',8)),tuple(spec.get('normal_face_origin',[30,24]))),
        (sr,slib,sbg,'yellow',int(spec.get('selected_target_height',29)),int(spec.get('selected_gap',-2)),int(spec.get('selected_space_width',8)),tuple(spec.get('selected_face_origin',[62,24]))),
    ]
    changed=0
    for i,(old,newtext) in enumerate(zip(source,target)):
        if old==newtext:
            continue
        changed+=1
        for rect,lib,bg,mode,target_h,gap,space,face_origin in [(st[0][i],st[1],st[2],st[3],st[4],st[5],st[6],st[7]) for st in states]:
            x0,y0,x1,y1=rect; allow.append(rect)
            if bg.size!=(x1-x0,y1-y0): die(f"{spec['id']}: Residence background size drift")
            out.paste(bg,(x0,y0))
            lab=_compose_token_raster_label(lib,newtext,target_h,gap,space)
            max_w=(x1-x0)-int(face_origin[0])-8
            if lab.width>max_w:
                f=max_w/max(1,lab.width)
                lab=lab.resize((max(1,round(lab.width*f)),max(1,round(lab.height*f))),Image.Resampling.LANCZOS)
            fb=_face_bbox_for_mode(lab,mode)
            if not fb: die(f"{spec['id']}: cannot locate {mode} face in rebuilt {newtext!r}")
            xx=x0+int(face_origin[0])-fb[0]; yy=y0+int(face_origin[1])-fb[1]
            out.alpha_composite(lab,(xx,yy))
    if changed!=int(spec.get('expected_changed_rows',6)):
        die(f"{spec['id']}: expected {spec.get('expected_changed_rows',6)} changed rows, got {changed}")
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms1_residence_names_font_v1(original_im, spec: dict):
    """Rebuild the included first game's Residence normal+selected room-name family."""
    from PIL import Image
    out=original_im.copy(); allow=[]
    nbg=_load_label_asset(spec['normal_background_asset'])
    sbg=_load_label_asset(spec['selected_background_asset'])
    npreset=_ui_preset(spec['normal_typography_preset'])
    spreset=_ui_preset(spec['selected_typography_preset'])
    nr=[tuple(map(int,r)) for r in spec['normal_rects']]
    sr=[tuple(map(int,r)) for r in spec['selected_rects']]
    if not (len(nr)==len(sr)==len(spec['rows'])):
        die(f"{spec['id']}: Residence row count mismatch")
    for i,item in enumerate(spec['rows']):
        for rect,bg,preset in ((nr[i],nbg,npreset),(sr[i],sbg,spreset)):
            x0,y0,x1,y1=rect; allow.append(rect)
            if bg.size!=(x1-x0,y1-y0):
                die(f"{spec['id']}: Residence background size drift")
            out.paste(bg,(x0,y0))
            lab=_make_centered_ui_label((x1-x0,y1-y0),item['text'],preset,bleed_radius=8)
            out.alpha_composite(lab,(x0,y0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_camp_top2_character_names_font_v3(original_im, spec: dict):
    """Build-time font renderer for the TID91 equipment/status CharacterName layer."""
    from PIL import Image
    out=original_im.copy(); preset=_ui_preset(spec['typography_preset'])
    band=tuple(map(int,spec['clear_band']))
    x0,y0,x1,y1=band
    _clear_rgba_rect_with_bleed(out,band)
    allow=[band]
    rows=spec['rows']; lane_w=(x1-x0)//len(rows)
    if lane_w*len(rows)!=(x1-x0):
        die(f'{spec["id"]}: clear band width must divide evenly by row count')
    for i,item in enumerate(rows):
        rx0=x0+i*lane_w; rx1=rx0+lane_w
        # Render horizontally on an exact (height x lane-width) canvas, then do
        # a lossless 90-degree pixel transpose. Equal lanes guarantee uniform
        # spacing and the full-band clear removes every old vertical-name remnant.
        horiz=_make_centered_ui_label((y1-y0,lane_w),item['text'],preset,bleed_radius=8)
        rot=horiz.transpose(Image.Transpose.ROTATE_90)
        out.alpha_composite(rot,(rx0,y0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def _assert_diff_inside_rects(original_im, rendered_im, rects, asset_id: str):
    import numpy as np
    before = np.asarray(original_im.convert('RGBA'))
    after = np.asarray(rendered_im.convert('RGBA'))
    diff = np.any(before != after, axis=2)
    allow = np.zeros(diff.shape, dtype=bool)
    for x0, y0, x1, y1 in rects:
        allow[int(y0):int(y1), int(x0):int(x1)] = True
    outside = int(np.count_nonzero(diff & ~allow))
    if outside:
        die(f'{asset_id}: changed {outside} decoded pixels outside declared bitmap-label rectangles')
    return int(np.count_nonzero(diff))


def render_ms2_room_selector_bitmap_v2(original_im, spec: dict):
    """Re-author the complete living-quarters selector with frozen whole-label PNGs.

    A per-glyph compositor can produce poor kerning/baseline and leave the
    selected state visually inconsistent. Replace every normal
    and selected row label as one pre-authored raster. Build/runtime require no
    font files; the future Go patcher can embed the same PNGs verbatim.
    """
    from PIL import Image
    out = original_im.copy()
    normal_x0, normal_x1 = map(int, spec['normal_x_range'])
    sel_x0, sel_x1 = map(int, spec['selected_x_range'])
    row_rects = [list(map(int, r)) for r in spec['row_rects']]
    if len(row_rects) != len(spec['rows']):
        die(f'{spec["id"]}: row/label count mismatch')
    selected_bg = _load_label_asset(spec['selected_background_asset'])
    expected_bg = (sel_x1-sel_x0, row_rects[0][1]-row_rects[0][0])
    if selected_bg.size != expected_bg:
        die(f'{spec["id"]}: selected background asset size {selected_bg.size} != {expected_bg}')
    allow=[]
    for i, item in enumerate(spec['rows']):
        y0, y1 = row_rects[i]
        allow.extend([(normal_x0,y0,normal_x1,y1),(sel_x0,y0,sel_x1,y1)])

        out.paste(Image.new('RGBA',(normal_x1-normal_x0,y1-y0),(0,0,0,0)),(normal_x0,y0))
        lab = _load_label_asset(item['normal_asset'])
        lab = _fit_bitmap_label(lab, normal_x1-normal_x0-8, y1-y0-8)
        out.alpha_composite(lab,(normal_x0+(normal_x1-normal_x0-lab.width)//2,
                                 y0+(y1-y0-lab.height)//2))

        # Use a frozen clean selected-row artwork asset. It was authored once
        # from the shipped row and contains no localized text, so the normal
        # build/Go port never need heuristic text removal or OpenCV.
        out.paste(selected_bg,(sel_x0,y0))
        slab = _load_label_asset(item['selected_asset'])
        slab = _fit_bitmap_label(slab, sel_x1-sel_x0-12, y1-y0-8)
        out.alpha_composite(slab,(sel_x0+(sel_x1-sel_x0-slab.width)//2,
                                  y0+(y1-y0-slab.height)//2))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_heroine_room_blue_bitmap_v2(original_im, spec: dict):
    """Replace every baked room label in menu_heroineroom with whole-label PNGs."""
    from PIL import Image
    out=original_im.copy(); allow=[]
    for item in spec['rows']:
        x0,y0,x1,y1=map(int,item['rect']); allow.append((x0,y0,x1,y1))
        out.paste(Image.new('RGBA',(x1-x0,y1-y0),(0,0,0,0)),(x0,y0))
        lab=_load_label_asset(item['asset'])
        lab=_fit_bitmap_label(lab,x1-x0-8,y1-y0-8)
        out.alpha_composite(lab,(x0+(x1-x0-lab.width)//2,y0+(y1-y0-lab.height)//2))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_camp_top2_character_names_v1(original_im, spec: dict):
    """Re-author the equipment/status character-card name source in camp_top_2.

    The shipped TID91 atlas stores this CharacterName family vertically. Every
    label in the family is replaced for typographic consistency; unchanged
    canonical names are deliberately redrawn alongside localized-name fixes.
    """
    from PIL import Image
    out=original_im.copy(); allow=[]
    for item in spec['rows']:
        x0,y0,x1,y1=map(int,item['rect']); allow.append((x0,y0,x1,y1))
        out.paste(Image.new('RGBA',(x1-x0,y1-y0),(0,0,0,0)),(x0,y0))
        lab=_load_label_asset(item['asset'])
        lab=_fit_bitmap_label(lab,x1-x0-4,y1-y0-8)
        out.alpha_composite(lab,(x0+(x1-x0-lab.width)//2,y0+4))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_dlc_job_labels_v1(images_by_key: dict, spec: dict):
    """Rebuild the six MS2 DLC job-name strips as one style family.

    The first three shipped English labels provide the real raster alphabet for
    this yellow/red family. Only the few Latin glyphs absent from those donors
    are synthesized, explicitly tracked for the later family-wide polish pass.
    """
    import statistics
    from PIL import Image
    candidates = {}
    by_name = {t['inner_name']: t for t in spec['targets']}
    for donor in spec.get('glyph_donors', []):
        name = donor['inner_name']
        im = images_by_key[name]
        for ch, glyph in _extract_raster_glyphs(im, donor['source'], spec['face_mode'], int(spec.get('mask_dilate', 5))):
            candidates.setdefault(ch, []).append(glyph)
    glyphs = {}
    for ch, variants in candidates.items():
        med = statistics.median(g.width for g in variants)
        glyphs[ch] = min(variants, key=lambda g: abs(g.width - med))
    outline = spec.get('synthetic_outline_rgba')
    for ch in spec.get('synthesize_chars', []):
        if ch not in glyphs:
            glyphs[ch] = _synth_basic_char_from_glyphs(glyphs, ch, int(spec['target_height']), outline)
    needed = set(''.join(t['to'] for t in spec['targets'])) - {' '}
    missing = sorted(needed - set(glyphs))
    if missing:
        die(f'{spec["id"]}: missing DLC job glyphs: {missing}')

    out = {}
    target_h = int(spec['target_height'])
    gap = int(spec.get('gap', -1))
    space_width = int(spec.get('space_width', max(6, target_h // 4)))
    for item in spec['targets']:
        original = images_by_key[item['inner_name']]
        canvas = Image.new('RGBA', original.size, (0, 0, 0, 0))
        label = _compose_raster_label(glyphs, item['to'], target_h, gap, space_width)
        max_w = max(1, original.width - 4)
        max_h = max(1, original.height - 4)
        scale = min(1.0, max_w / label.width, max_h / label.height)
        if scale < 1.0:
            label = label.resize((max(1, round(label.width * scale)), max(1, round(label.height * scale))), Image.Resampling.LANCZOS)
        canvas.alpha_composite(label, ((original.width - label.width)//2, (original.height - label.height)//2))
        out[item['inner_name']] = canvas
    return out



def _make_ms2_heroine_banner_base(size=(1024, 256)):
    """Deterministically reconstruct the shared MS2 Heroine Room banner artwork.

    The shipped room-title textures bake the localized label into the same DXT5
    pixels as the striped banner, so old text cannot be removed losslessly from
    one file. Replace only the localized-name members with a clean
    reconstruction of the common banner motif, then draws labels from shipped
    same-family raster glyphs. This guarantees there is no old-label ghosting.
    """
    from PIL import Image, ImageDraw, ImageChops
    w, h = size
    if (w, h) != (1024, 256):
        die(f'heroine banner renderer expects 1024x256, got {w}x{h}')
    out = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    poly = [(5, 40), (630, 16), (605, 146), (5, 166)]
    mask = Image.new('L', (w, h), 0)
    ImageDraw.Draw(mask).polygon(poly, fill=255)
    base = Image.new('RGBA', (w, h), (48, 0, 18, 245))
    out = Image.composite(base, out, mask)

    stripes = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    sd = ImageDraw.Draw(stripes)
    for x in range(-120, 780, 112):
        sd.polygon([(x, 15), (x + 52, 15), (x - 18, 175), (x - 70, 175)], fill=(132, 0, 52, 220))
        sd.polygon([(x + 52, 15), (x + 104, 15), (x + 34, 175), (x - 18, 175)], fill=(24, 0, 11, 150))
    # The shipped banner darkens toward the pointed right edge.
    grad = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    gp = grad.load()
    for xx in range(w):
        aa = int(max(0, min(120, (xx - 380) * 0.22)))
        if aa:
            for yy in range(h):
                gp[xx, yy] = (0, 0, 0, aa)
    stripes.alpha_composite(grad)
    stripes.putalpha(ImageChops.multiply(stripes.getchannel('A'), mask))
    out.alpha_composite(stripes)

    d = ImageDraw.Draw(out)
    d.line([(6, 40), (630, 16)], fill=(156, 0, 63, 255), width=4)
    d.line([(5, 165), (605, 145)], fill=(183, 0, 75, 255), width=4)
    d.line([(35, 23), (610, 13)], fill=(130, 0, 54, 180), width=2)
    return out


def _compose_heroine_banner_label(glyphs: dict, text: str, target_h: int, gap: int, space_width: int):
    """Compose a shipped-raster title and add one family-level magenta extrusion.

    Individual donor glyphs keep their original white/pink faces. A shared glow
    hides tiny donor-edge differences without reintroducing the inherited old
    word-level background pixels that caused the earlier ghosting trials.
    """
    from PIL import Image, ImageFilter
    face = _compose_raster_label(glyphs, text, target_h, gap, space_width)
    alpha = face.getchannel('A')
    glow = alpha.filter(ImageFilter.MaxFilter(11))
    canvas = Image.new('RGBA', (face.width + 10, face.height + 12), (0, 0, 0, 0))
    sh = Image.new('RGBA', face.size, (230, 0, 95, 0))
    sh.putalpha(glow.point(lambda v: int(v * 0.75)))
    canvas.alpha_composite(sh, (3, 6))
    canvas.alpha_composite(face, (0, 0))
    box = canvas.getbbox()
    return canvas.crop(box) if box else canvas


def _extract_heroine_face_glyph(im, rect, *, pad: int = 4):
    """Extract only the shipped pale/pink face of one first-game title glyph.

    The older generic explicit-run helper intentionally admitted nearby pixels
    to retain unknown outline treatments.  That is unsafe for this family: the
    diagonal magenta banner sits immediately behind the letters, so admitting
    every near-face pixel drags rectangular banner fragments into each glyph.
    Here the *shape and face shading* remain the shipped raster glyph, while the
    family outline/glow is rebuilt from that exact raster mask below.
    """
    import numpy as np
    from PIL import Image, ImageFilter
    src=im.convert('RGBA')
    x0,y0,x1,y1=map(int,rect)
    rx0=max(0,x0-pad); ry0=max(0,y0-pad); rx1=min(src.width,x1+pad); ry1=min(src.height,y1+pad)
    crop=src.crop((rx0,ry0,rx1,ry1))
    a=np.asarray(crop,dtype=np.uint8)
    r,g,b,al=[a[...,i].astype(np.int16) for i in range(4)]
    yy,xx=np.mgrid[ry0:ry1,rx0:rx1]
    inside=(xx>=x0)&(xx<x1)&(yy>=y0)&(yy<y1)
    # The shipped face is white through pale pink.  Keep one AA fringe, but do
    # not admit the dark outline or the patterned banner itself.
    face=inside&(al>12)&(r>150)&(g>72)&(b>105)&((r+g+b)>400)
    if not face.any():
        die(f'heroine raster glyph has no face pixels in {rect}')
    near=np.asarray(Image.fromarray((face*255).astype(np.uint8),'L').filter(ImageFilter.MaxFilter(3)))>0
    aa=near&(al>6)&(r>112)&(g>48)&(b>78)&((r+g+b)>265)
    keep=face|aa
    out=a.copy(); out[...,3]=np.where(keep,al,0).astype(np.uint8)
    glyph=Image.fromarray(out,'RGBA')
    box=glyph.getbbox()
    return glyph.crop(box) if box else glyph


def _style_heroine_face_glyph(face):
    """Reapply the shipped first-game dark edge and magenta halo to a raster face."""
    from PIL import Image, ImageFilter
    alpha=face.getchannel('A')
    dark=alpha.filter(ImageFilter.MaxFilter(7))
    outer=alpha.filter(ImageFilter.MaxFilter(11))
    glow=outer.filter(ImageFilter.GaussianBlur(1.7))
    pad=9
    w=face.width+pad*2; h=face.height+pad*2
    def expand(mask):
        m=Image.new('L',(w,h),0); m.paste(mask,(pad,pad)); return m
    out=Image.new('RGBA',(w,h),(0,0,0,0))
    gm=expand(glow).point(lambda v:(int(v)*155+127)//255)
    g=Image.new('RGBA',(w,h),(224,0,88,255)); g.putalpha(gm); out.alpha_composite(g)
    om=expand(outer)
    o=Image.new('RGBA',(w,h),(146,0,55,255)); o.putalpha(om); out.alpha_composite(o)
    dm=expand(dark)
    d=Image.new('RGBA',(w,h),(31,0,17,255)); d.putalpha(dm); out.alpha_composite(d)
    out.alpha_composite(face,(pad,pad))
    box=out.getbbox()
    return out.crop(box) if box else out


def _heroine_banner_text_mask(im, *, protect_room: bool = True):
    """Mask shipped first-game title lettering while preserving ROOM itself.

    The title and ROOM use the same pale face.  Build one effect mask from all
    pale title pixels, then subtract only the *actual* ROOM effect derived from
    its lower-right face.  This avoids the old rectangular protection zone that
    left fragments of long source names behind g1839.
    """
    import numpy as np
    from PIL import Image, ImageFilter
    a=np.asarray(im.convert('RGBA'),dtype=np.uint8)
    r,g,b,al=[a[...,i].astype(np.int16) for i in range(4)]
    face=(al>12)&(r>150)&(g>72)&(b>105)&((r+g+b)>400)
    yy,xx=np.indices(face.shape)
    seed=Image.fromarray((face.astype(np.uint8)*255),'L')
    mask=np.asarray(seed.filter(ImageFilter.MaxFilter(23)))>0
    mask &= (yy<184)&(xx<720)
    if protect_room:
        room_face=face&(xx>=400)&(yy>=122)
        room_seed=Image.fromarray((room_face.astype(np.uint8)*255),'L')
        room_effect=np.asarray(room_seed.filter(ImageFilter.MaxFilter(23)))>0
        mask &= ~room_effect
    return mask

def _heroine_banner_source_clean(images_by_key: dict, by_index: dict):
    """Recover the shared shipped banner background from the 11 title siblings.

    All eleven textures use the same banner artwork at the same coordinates.
    For each pixel we therefore choose the exact RGBA mode only from siblings in
    which that coordinate is outside the title-effect mask.  This is materially
    safer than the earlier shifted/guide reconstruction: genuine shipped pixels
    win whenever any clean sibling exposes them.  Only coordinates covered by
    lettering in every sibling fall back to the old family geometry guide, and
    those tiny holes are subsequently covered by the new raster title.
    """
    import numpy as np
    from PIL import Image
    ordered=[images_by_key[by_index[i]].convert('RGBA') for i in sorted(by_index)]
    arr=np.stack([np.asarray(im,dtype=np.uint8) for im in ordered],axis=0)
    masks=np.stack([_heroine_banner_text_mask(im,protect_room=True) for im in ordered],axis=0)
    vals=(arr[...,0].astype(np.uint32)<<24)|(arr[...,1].astype(np.uint32)<<16)|(arr[...,2].astype(np.uint32)<<8)|arr[...,3].astype(np.uint32)
    valid=~masks
    h,w=vals.shape[1:]
    best_count=np.zeros((h,w),dtype=np.uint8)
    best_val=np.zeros((h,w),dtype=np.uint32)
    # N=11, so an explicit candidate pass is small, deterministic and avoids a
    # dependency on scipy/opencv in the public builder.
    for i in range(vals.shape[0]):
        cand=vals[i]
        count=((vals==cand[None,...])&valid).sum(axis=0).astype(np.uint8)
        count=np.where(valid[i],count,0).astype(np.uint8)
        take=count>best_count
        best_count[take]=count[take]
        best_val[take]=cand[take]
    guide=np.asarray(_make_ms2_heroine_banner_base(ordered[0].size).convert('RGBA'),dtype=np.uint8)
    out=guide.copy()
    recovered=np.stack([(best_val>>24)&255,(best_val>>16)&255,(best_val>>8)&255,best_val&255],axis=-1).astype(np.uint8)
    take=best_count>0
    out[take]=recovered[take]
    return Image.fromarray(out,'RGBA')


def _extract_heroine_exact_glyph(source_im, clean_im, rect, *, pad: int = 14):
    """Isolate one complete shipped title glyph treatment against clean artwork.

    Unlike the earlier face-only reconstruction this retains the *actual* white/
    pink face, dark edge, magenta extrusion/glow and antialiasing from the donor
    raster.  The clean sibling-derived background lets us remove banner pixels
    instead of rebuilding the glyph's effects synthetically.
    """
    import numpy as np
    from PIL import Image, ImageFilter
    src=source_im.convert('RGBA'); clean=clean_im.convert('RGBA')
    x0,y0,x1,y1=map(int,rect)
    rx0=max(0,x0-pad); ry0=max(0,y0-pad); rx1=min(src.width,x1+pad); ry1=min(src.height,y1+pad)
    a=np.asarray(src.crop((rx0,ry0,rx1,ry1)),dtype=np.uint8)
    c=np.asarray(clean.crop((rx0,ry0,rx1,ry1)),dtype=np.uint8)
    rr,gg,bb,aa=[a[...,i].astype(np.int16) for i in range(4)]
    yy,xx=np.mgrid[ry0:ry1,rx0:rx1]
    inside=(xx>=x0)&(xx<x1)&(yy>=y0)&(yy<y1)
    face=inside&(aa>12)&(rr>150)&(gg>72)&(bb>105)&((rr+gg+bb)>400)
    if not face.any():
        die(f'heroine exact raster glyph has no face pixels in {rect}')
    near=np.asarray(Image.fromarray((face.astype(np.uint8)*255),'L').filter(ImageFilter.MaxFilter(27)))>0
    diff=np.max(np.abs(a.astype(np.int16)-c.astype(np.int16)),axis=2)>7
    keep=near&diff&(aa>2)
    out=a.copy(); out[...,3]=np.where(keep,a[...,3],0).astype(np.uint8)
    glyph=Image.fromarray(out,'RGBA')
    box=glyph.getbbox()
    return glyph.crop(box) if box else glyph


def _heroine_face_only_segment(glyph):
    """Keep exact shipped face shading from a segmented glyph, without banner fragments."""
    import numpy as np
    from PIL import Image, ImageFilter
    a=np.asarray(glyph.convert('RGBA'),dtype=np.uint8)
    r,g,b,al=[a[...,i] for i in range(4)]
    face=(al>8)&(r>145)&(g>65)&(b>100)&((r.astype(int)+g.astype(int)+b.astype(int))>385)
    seed=Image.fromarray((face.astype(np.uint8)*255),'L')
    near=np.asarray(seed.filter(ImageFilter.MaxFilter(3)))>0
    keep=near&(al>4)&(r>105)&(g>40)&(b>70)
    out=a.copy(); out[...,3]=np.where(keep,al,0).astype(np.uint8)
    im=Image.fromarray(out,'RGBA')
    box=im.getbbox()
    return im.crop(box) if box else im


def _heroine_curated_main_glyphs(images_by_key: dict, by_index: dict):
    """Build the large-title alphabet from known clean shipped donor runs.

    Generic extraction is used only to segment donor words whose glyph order is
    known.  We then keep the exact face raster and rebuild the common family
    edge/halo, preventing adjacent diagonal banner pixels from entering glyphs.
    """
    def segmented(idx,text,rect):
        gs=_extract_raster_glyphs(images_by_key[by_index[idx]].crop(rect),text,'pink',3)
        if len(gs)!=len(text.replace(' ','')):
            die(f'heroine donor segmentation mismatch g{idx} {text!r}: {len(gs)}')
        return [(c,_heroine_face_only_segment(g)) for c,g in gs]

    sets={}
    for idx,text,rect in [
        (1829,"ALICE'S",(20,0,420,105)),
        (1831,"THUMBELINA'S",(0,0,640,105)),
        (1834,"GRETEL'S",(40,0,420,105)),
        (1835,"RAPUNZEL'S",(20,0,610,105)),
        (1836,"CINDERELLA'S",(20,0,640,105)),
        (1837,"KAGUYA'S",(10,0,520,105)),
        (1838,"HAMELN'S",(20,0,520,105)),
    ]:
        sets[(idx,text)]=segmented(idx,text,rect)

    out={}
    def take(idx,text,ch,n=0,key=None):
        hits=[g for c,g in sets[(idx,text)] if c==ch]
        if len(hits)<=n: die(f'heroine donor missing {ch!r} in g{idx} {text!r}')
        out[key or ch]=_style_heroine_face_glyph(hits[n])

    for ch in ['A','L','I','C','E','S']:
        take(1829,"ALICE'S",ch)
    take(1834,"GRETEL'S",'G'); take(1834,"GRETEL'S",'T')
    for ch in ['R','D']:
        take(1836,"CINDERELLA'S",ch)
    take(1831,"THUMBELINA'S",'B')
    for ch in ['U','P','Z']:
        take(1835,"RAPUNZEL'S",ch)
    for ch in ['H','M','N']:
        take(1838,"HAMELN'S",ch)
    take(1837,"KAGUYA'S",'K'); take(1837,"KAGUYA'S",'Y')

    # O from shipped ROOM is a clean, unambiguous donor; the SNOW projection
    # can be mis-segmented as part of R/W because of this angular raster face.
    room=_extract_raster_glyphs(images_by_key[by_index[1829]].crop((410,105,640,190)),'ROOM','pink',3)
    ohits=[g for c,g in room if c=='O']
    if not ohits: die('heroine ROOM donor missing O')
    out['O']=_style_heroine_face_glyph(_heroine_face_only_segment(ohits[0]))

    # Keep apostrophe and S as one shipped-layout token so the punctuation is
    # not stretched to full cap height by the compositor.
    out["'S"]=_style_heroine_face_glyph(
        _extract_heroine_face_glyph(images_by_key[by_index[1829]],(329,10,406,105),pad=2)
    )
    return out


def _heroine_small_top_glyphs(images_by_key: dict, by_index: dict):
    src=images_by_key[by_index[1839]].crop((10,0,345,76))
    gs=_extract_raster_glyphs(src,'OTSUU AND','pink',3)
    if len(gs)!=8: die(f'heroine g1839 top donor segmentation mismatch: {len(gs)}')
    out={}
    for ch,g in gs:
        out.setdefault(ch,_style_heroine_face_glyph(_heroine_face_only_segment(g)))
    return out


def render_ms2_heroine_room_banners_raster_v3(images_by_key: dict, spec: dict):
    """Rebuild only changed first-game titles from shipped raster glyphs.

    Unchanged textures are never emitted.  The title face comes from shipped
    raster glyphs; the common dark edge/magenta halo is rebuilt from those exact
    masks.  ROOM remains source artwork.
    """
    import numpy as np
    from PIL import Image

    changed=set(int(x) for x in spec.get('changed_gdat_indices',[1830,1831,1832,1833,1837,1839]))
    by_index={int(t['gdat_index']):t['inner_name'] for t in spec['targets']}
    clean_im=_heroine_banner_source_clean(images_by_key,by_index)
    clean=np.asarray(clean_im,dtype=np.uint8)
    main=_heroine_curated_main_glyphs(images_by_key,by_index)
    top=_heroine_small_top_glyphs(images_by_key,by_index)

    out={}
    for item in spec['replacements']:
        idx=int(item['gdat_index'])
        if idx not in changed:
            continue
        name=item['inner_name']; original=images_by_key[name].convert('RGBA')
        mask=_heroine_banner_text_mask(original,protect_room=True)
        a=np.asarray(original,dtype=np.uint8).copy(); a[mask]=clean[mask]
        if idx==1839:
            # This source has two localized title lines packed tightly above ROOM.
            # Clear the full title band from sibling-derived shipped artwork, then
            # restore only the actual ROOM effect pixels. This removes the last
            # LITTLE MERMAID'S underlayer without flattening the banner/frame.
            yy,xx=np.indices(mask.shape)
            title_band=(yy<136)&(xx<650)
            a[title_band]=clean[title_band]
            oa=np.asarray(original,dtype=np.uint8)
            r,g,b,al=[oa[...,i].astype(np.int16) for i in range(4)]
            room_face=(al>12)&(r>150)&(g>72)&(b>105)&((r+g+b)>400)&(xx>=400)&(yy>=122)
            from PIL import ImageFilter
            room_effect=np.asarray(Image.fromarray((room_face.astype(np.uint8)*255),'L').filter(ImageFilter.MaxFilter(23)))>0
            a[room_effect]=oa[room_effect]
        canvas=Image.fromarray(a,'RGBA')
        for pi,part in enumerate(item['parts']):
            if part['text']=='ROOM':
                continue
            x0,y0,x1,y1=map(int,part['rect'])
            is_top=(idx==1839 and pi==0)
            lib=top if is_top else main
            gap=(-2 if is_top else int(spec.get('gap',-6)))
            space=(14 if is_top else int(spec.get('space_width',24)))
            label=_compose_token_raster_label(lib,part['text'],int(part['height']),gap,space)
            hs=float(spec.get('top_horizontal_scale',0.94) if is_top else spec.get('horizontal_scale',0.80))
            if abs(hs-1.0)>1e-6:
                label=label.resize((max(1,round(label.width*hs)),label.height),Image.Resampling.LANCZOS)
            max_w=max(1,x1-x0); max_h=max(1,y1-y0)
            sc=min(1.0,max_w/max(1,label.width),max_h/max(1,label.height))
            if sc<1.0:
                label=label.resize((max(1,round(label.width*sc)),max(1,round(label.height*sc))),Image.Resampling.LANCZOS)
            canvas.alpha_composite(label,(x0,y0+(max_h-label.height)//2))
        out[name]=canvas
    if set(out)!={by_index[i] for i in changed}:
        die(f"{spec['id']}: raster-v3 changed output set drift")
    return out

def render_ms2_heroine_room_banners_font_v2(images_by_key: dict, spec: dict):
    return _r32c3_core().render_banners(images_by_key,spec,_make_ms2_heroine_banner_base,
                                      _ui_preset,_ui_font,_bleed_transparent_rgb)

def render_ms2_heroine_room_banners_v1(images_by_key: dict, spec: dict):
    """Render the six localized MS2 Heroine Room title banners as one family."""
    import statistics
    from PIL import Image

    candidates = {}
    for donor in spec['glyph_sources']:
        im = images_by_key[donor['inner_name']]
        x0, y0, x1, y1 = donor['rect']
        crop = im.crop((x0, y0, x1, y1))
        glyphs = _extract_raster_glyphs(crop, donor['source'], 'white', int(spec.get('mask_dilate', 11)))
        if len(glyphs) != len(donor['source'].replace(' ', '')):
            die(f'{spec["id"]}: title donor segmentation mismatch for {donor["source"]!r}')
        for ch, glyph in glyphs:
            candidates.setdefault(ch, []).append(glyph)

    glyphs = {}
    for ch, variants in candidates.items():
        med = statistics.median(g.width / max(1, g.height) for g in variants)
        glyphs[ch] = min(variants, key=lambda g: abs(g.width / max(1, g.height) - med))
    needed = set(''.join(part['text'] for item in spec['replacements'] for part in item['parts'])) - {' '}
    missing = sorted(needed - set(glyphs))
    if missing:
        die(f'{spec["id"]}: missing shipped title glyphs: {missing}')

    out = {}
    for item in spec['replacements']:
        canvas = _make_ms2_heroine_banner_base(images_by_key[item['inner_name']].size)
        for part in item['parts']:
            x0, y0, x1, y1 = part['rect']
            label = _compose_heroine_banner_label(
                glyphs, part['text'], int(part['height']), int(spec.get('gap', -6)), int(spec.get('space_width', 24))
            )
            max_w = max(1, x1 - x0)
            max_h = max(1, y1 - y0)
            scale = min(1.0, max_w / max(1, label.width), max_h / max(1, label.height))
            if scale < 1.0:
                label = label.resize(
                    (max(1, round(label.width * scale)), max(1, round(label.height * scale))),
                    Image.Resampling.LANCZOS,
                )
            xx = x0 + (x1 - x0 - label.width) // 2
            yy = y0 + (y1 - y0 - label.height) // 2
            canvas.alpha_composite(label, (xx, yy))
        out[item['inner_name']] = canvas
    return out

def render_result_v2(original_im, spec: dict):
    from PIL import Image
    glyphs = build_glyph_library(original_im, spec)
    out = original_im.copy()
    for item in spec['replacements']:
        x, y, w, h = item['rect']
        out.paste(Image.new('RGBA', (w, h), (0, 0, 0, 0)), (x, y))
        label = compose_label(glyphs, item['to'])
        label = fit_label(label, int(w * 0.94), int(h * 0.85))
        out.alpha_composite(label, (x + (w - label.width) // 2, y + (h - label.height) // 2))
    return out


def _container_helper() -> str | None:
    p = os.environ.get('MARY_SKELTER_GRAPHICS_CONTAINER_HELPER')
    return p if p else None


def extract_tid(dat: Path, gdat_index: int, inner_name: str):
    helper = _container_helper()
    if helper:
        cp = subprocess.run(
            [helper, 'graphics-extract-tid', '--dat', str(dat), '--gdat-index', str(gdat_index), '--inner', inner_name],
            cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
        )
        if cp.returncode != 0:
            die(f'Go graphics container extract failed for {dat.name} g{gdat_index}/{inner_name}: {cp.stderr.decode("utf-8", "replace").strip()}')
        return cp.stdout, None
    sys.path.insert(0, str(TOOLS))
    from mskit import gdat, cl3
    with gdat.Gdat(dat) as gd:
        payload = gd.read_payload(gdat_index)
    container = cl3.Cl3(payload)
    hits = [f for f in container.files if f.name.lower() == inner_name.lower()]
    if len(hits) != 1:
        die(f'{dat.name} g{gdat_index}: expected one {inner_name}, found {len(hits)}')
    f = hits[0]
    return container.file_data(f.index), f.index


def patch_dat(base_dat: Path, out_dat: Path, asset: dict, new_tid: bytes) -> dict:
    helper = _container_helper()
    if helper:
        # The native path is explicitly opt-in until full pristine-master
        # game validation. SHA-pinning applies to the exact CURRENT input DAT:
        # grouped graphics may sequentially edit the same staged archive.
        native = os.environ.get('MARY_SKELTER_GRAPHICS_NATIVE_BILZ') == '1'
        argv = [helper, 'graphics-patch-tid-go' if native else 'graphics-patch-tid',
                '--dat', str(base_dat), '--out', str(out_dat),
                '--gdat-index', str(int(asset['gdat_index'])), '--inner', asset['cl3_inner_name']]
        if native:
            argv.extend(('--source-sha256', sha256_file(base_dat)))
        cp = subprocess.run(
            argv, cwd=ROOT, input=new_tid, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, check=False,
        )
        if cp.returncode != 0:
            die(f'Go graphics container patch failed for {base_dat.name} g{asset["gdat_index"]}/{asset["cl3_inner_name"]}: {cp.stderr.decode("utf-8", "replace").strip()}')
        try:
            rep = json.loads(cp.stdout.decode('utf-8'))
            if native:
                old_key, new_key = 'source_tid_sha256', 'result_tid_sha256'
                if (rep.get('status') != 'PASS' or
                        rep.get('source_sha256') != argv[-1] or
                        rep.get(new_key) != sha256_bytes(new_tid) or
                        rep.get('gdat_index') != int(asset['gdat_index']) or
                        rep.get('result_sha256') != sha256_file(out_dat)):
                    die('native graphics container report/source/output verification failed')
                # Canonical reporting contract of the existing renderer.
                return {'gdat_index': rep['gdat_index'],
                        'cl3_index': rep['cl3_index'],
                        'inner_name': asset['cl3_inner_name'],
                        'old_tid_sha256': rep[old_key],
                        'new_tid_sha256': rep[new_key]}
            return rep
        except (ValueError, KeyError, TypeError) as exc:
            die(f'Go graphics container patch returned invalid JSON: {exc}: {cp.stdout[:200]!r}')

    sys.path.insert(0, str(TOOLS))
    from mskit import gdat, cl3, bilz

    target = int(asset['gdat_index'])
    name = asset['cl3_inner_name']
    changed = None
    entries = []
    with gdat.Gdat(base_dat) as gd:
        for entry in gd.entries:
            original_entry = gd.read(entry)
            if entry.index != target:
                entries.append((entry.index, original_entry))
                continue
            payload = gd.read_payload(entry)
            before = cl3.Cl3(payload)
            hits = [f for f in before.files if f.name.lower() == name.lower()]
            if len(hits) != 1:
                die(f'g{entry.index}: expected one {name}, found {len(hits)}')
            file_index = hits[0].index
            old_tid = before.file_data(file_index)
            if tid_meta(old_tid) != tid_meta(new_tid):
                die(f'g{entry.index}/{name}: TID metadata changed')
            new_payload = before.rebuild({file_index: new_tid})
            after = cl3.Cl3(new_payload)
            if len(before.files) != len(after.files):
                die(f'g{entry.index}/{name}: CL3 file count changed')
            for bf, af in zip(before.files, after.files):
                if bf.name != af.name:
                    die(f'g{entry.index}/{name}: CL3 name/order drift')
                if bf.index == file_index:
                    continue
                if before.file_data(bf.index) != after.file_data(af.index):
                    die(f'g{entry.index}/{name}: non-target CL3 member changed: {bf.name}')
            bh, _, prefix_len = gd.payload_info(entry)
            rebuilt_entry = original_entry[:prefix_len] + bilz.wrap(new_payload, bh.algo, 9) if bh else new_payload
            entries.append((entry.index, rebuilt_entry))
            changed = {
                'gdat_index': entry.index,
                'cl3_index': file_index,
                'inner_name': name,
                'old_tid_sha256': sha256_bytes(old_tid),
                'new_tid_sha256': sha256_bytes(new_tid),
            }
        blob = gdat.build(entries)
    if changed is None:
        die(f'target GDAT entry g{target} was not found')
    out_dat.parent.mkdir(parents=True, exist_ok=True)
    out_dat.write_bytes(blob)
    with gdat.Gdat(base_dat) as before, gdat.Gdat(out_dat) as after:
        if before.count != after.count:
            die('GDAT entry count changed')
        for i in range(before.count):
            if i == target:
                continue
            if before.read(i) != after.read(i):
                die(f'unexpected non-target GDAT change at entry {i}')
    return changed

def _ensure_rebuilt_archive(dataset: str, archive: int) -> tuple[Path, bool]:
    dat = BUILD / 'rebuilt' / dataset / 'masterWindows' / f'GxArchivedFile{archive:03d}.dat'
    if dat.is_file():
        return dat, False
    if os.environ.get('MARY_SKELTER_GRAPHICS_GO_ORCHESTRATED'):
        die(f'{dataset}: Go orchestrator did not prepare required archive {archive:03d}')
    if dataset.startswith('MS2_'):
        master = os.environ.get('MS2_PC_MASTER')
    elif dataset.startswith('MSF_'):
        master = os.environ.get('MSF_PC_MASTER')
    else:
        master = None
    if not master:
        die(f'{dataset}: graphics-only archive {archive:03d} is not in the text rebuild and source master is unavailable')
    src = Path(master) / f'GxArchivedFile{archive:03d}.dat'
    if not src.is_file():
        die(f'{dataset}: missing pristine graphics source archive: {src}')
    dat.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(src, dat)
    print(f'GRAPHICS: seeded graphics-only archive from pristine source: {src.name}')
    return dat, True



def _synth_mixedcase_n(glyphs: dict):
    """Create the one missing capital N for Finale's thin mixed-case room font.

    The shipped ren_bonus_character atlas contains every required lower-case
    glyph but no capital N.  This fallback deliberately mirrors the restrained
    neutral-gray face/dark extrusion of the same atlas instead of borrowing a
    glyph from a different UI family.  Shared polish may replace it later.
    """
    from PIL import Image, ImageDraw, ImageFilter
    ref = glyphs.get('H') or glyphs.get('M') or next(iter(glyphs.values()))
    # Sample a representative neutral bright face and a dark opaque outline.
    face = (105, 105, 105, 255)
    dark = (34, 34, 34, 255)
    best_face = -1
    best_dark = 10**9
    for r, g, b, a in ref.convert('RGBA').getdata():
        if a < 100:
            continue
        spread = max(r, g, b) - min(r, g, b)
        avg = (r + g + b) / 3.0
        if spread <= 24 and 72 <= avg <= 160 and avg > best_face:
            best_face = avg
            face = (r, g, b, 255)
        if spread <= 28 and 8 <= avg <= 80 and avg < best_dark:
            best_dark = avg
            dark = (r, g, b, 255)
    h = max(28, int(round(ref.height * 0.98)))
    w = max(20, int(round(h * 0.76)))
    m = Image.new('L', (w, h), 0)
    d = ImageDraw.Draw(m)
    pad = 3
    stroke = max(2, int(round(h * 0.075)))
    d.line((pad, 2, pad, h - 3), fill=255, width=stroke)
    d.line((w - pad - 1, 2, w - pad - 1, h - 3), fill=255, width=stroke)
    d.line((pad, 2, w - pad - 1, h - 3), fill=255, width=stroke)
    outline = m.filter(ImageFilter.MaxFilter(3))
    out = Image.new('RGBA', (w + 2, h + 3), (0, 0, 0, 0))
    sh = Image.new('RGBA', (w, h), dark); sh.putalpha(outline)
    out.alpha_composite(sh, (1, 2))
    fg = Image.new('RGBA', (w, h), face); fg.putalpha(m)
    out.alpha_composite(fg, (0, 0))
    box = out.getbbox()
    return out.crop(box) if box else out


def _build_mixedcase_glyph_library(original_im, spec: dict) -> dict:
    import statistics
    candidates = {}
    for cell in spec.get('glyph_cells', []):
        x0, y0, x1, y1 = cell['rect']
        crop = original_im.crop((x0, y0, x1, y1))
        extracted = _extract_raster_glyphs(
            crop, cell['source'], spec['face_mode'], int(spec.get('mask_dilate', 3))
        )
        expected = len(cell['source'].replace(' ', ''))
        if len(extracted) != expected:
            die(f'{spec["id"]}: mixed-case source {cell["source"]!r} segmented as {len(extracted)} glyphs; expected {expected}')
        for ch, glyph in extracted:
            candidates.setdefault(ch, []).append(glyph)
    glyphs = {}
    for ch, variants in candidates.items():
        median_width = statistics.median(g.width for g in variants)
        glyphs[ch] = min(variants, key=lambda g: abs(g.width - median_width))
    if spec.get('synthesize_capital_n') and 'N' not in glyphs:
        glyphs['N'] = _synth_mixedcase_n(glyphs)
    needed = set(''.join(r['to'] for r in spec['replacements'])) - {' '}
    missing = sorted(needed - set(glyphs))
    if missing:
        die(f'{spec["id"]}: missing mixed-case raster glyphs: {missing}')
    return glyphs


def _compose_mixedcase_label(glyphs: dict, text: str, gap: int, space_width: int, descenders: str):
    """Compose a proportional mixed-case label without normalizing glyph heights.

    The older generic atlas renderer intentionally normalizes every glyph to one
    target height, which is appropriate for all-caps atlases but visibly damages
    Finale's proportional title-case font.  This compositor preserves the
    shipped cap/x-height relationship and aligns descenders to one baseline.
    """
    from PIL import Image
    pieces = []
    for ch in text:
        if ch == ' ':
            pieces.append((None, 0))
        else:
            pieces.append((glyphs[ch], 7 if ch in descenders else 0))
    baseline = max((g.height for g, d in pieces if g is not None and d == 0), default=32) + 2
    top = min((baseline + d - g.height for g, d in pieces if g is not None), default=0)
    bottom = max((baseline + d for g, d in pieces if g is not None), default=baseline)
    height = max(1, bottom - top + 2)
    width = sum(space_width if g is None else g.width for g, _ in pieces) + gap * max(0, len(pieces) - 1)
    out = Image.new('RGBA', (max(1, width), height), (0, 0, 0, 0))
    x = 0
    for i, (glyph, descent) in enumerate(pieces):
        if glyph is None:
            x += space_width
        else:
            y = baseline + descent - glyph.height - top
            out.alpha_composite(glyph, (x, y))
            x += glyph.width
        if i != len(pieces) - 1:
            x += gap
    box = out.getbbox()
    return out.crop(box) if box else out


def render_mixedcase_raster_atlas_v1(original_im, spec: dict):
    """Render Finale's thin title-case character list from shipped raster glyphs."""
    from PIL import Image
    glyphs = _build_mixedcase_glyph_library(original_im, spec)
    out = original_im.copy()
    gap = int(spec.get('gap', 1))
    space_width = int(spec.get('space_width', 10))
    descenders = str(spec.get('descenders', 'gpyqj'))
    clear_rgba = tuple(spec.get('clear_rgba', [0, 0, 0, 0]))
    for item in spec['replacements']:
        cx0, cy0, cx1, cy1 = item.get('clear_rect', item['rect'])
        out.paste(Image.new('RGBA', (cx1 - cx0, cy1 - cy0), clear_rgba), (cx0, cy0))
        label = _compose_mixedcase_label(glyphs, item['to'], gap, space_width, descenders)
        x0, y0, x1, y1 = item.get('render_rect', item['rect'])
        preferred = float(item.get('scale', 1.0))
        fit = min((x1 - x0 - 4) / max(1, label.width), (y1 - y0 - 4) / max(1, label.height))
        scale = min(preferred, fit)
        if abs(scale - 1.0) > 1e-6:
            label = label.resize(
                (max(1, round(label.width * scale)), max(1, round(label.height * scale))),
                Image.Resampling.LANCZOS,
            )
        out.alpha_composite(label, (x0 + (x1 - x0 - label.width) // 2, y0 + (y1 - y0 - label.height) // 2))
    return out

def _exact_mode_template(crops):
    """Return deterministic per-pixel RGBA mode plus its occurrence count.

    Battle plates share the same underlying art but contain different names.
    Using many shipped sibling plates lets us recover the common artwork
    without external painting. Ties are resolved lexicographically so builds
    remain deterministic across Python/platform versions.
    """
    from PIL import Image
    if not crops:
        raise ValueError('mode template requires at least one crop')
    size = crops[0].size
    if any(im.size != size for im in crops):
        raise ValueError('mode template crop size mismatch')
    rgba = [im.convert('RGBA') for im in crops]
    pix = [list(im.getdata()) for im in rgba]
    out = []
    conf = []
    for i in range(size[0] * size[1]):
        counts = {}
        for p in pix:
            value = p[i]
            counts[value] = counts.get(value, 0) + 1
        best_count = max(counts.values())
        best = min(value for value, count in counts.items() if count == best_count)
        out.append(best)
        conf.append(best_count)
    im = Image.new('RGBA', size)
    im.putdata(out)
    return im, conf


def _battle_common_background(original_im, spec: dict):
    """Reconstruct one shipped battle-nameplate background deterministically."""
    from PIL import ImageFilter
    cells = [original_im.crop(tuple(rect)) for rect in spec['background_cells']]
    base, conf = _exact_mode_template(cells)
    median_rect = tuple(spec['background_median_rect'])
    k = int(spec.get('background_median_size', 31))
    if k % 2 == 0:
        k += 1
    filtered = base.crop(median_rect).filter(ImageFilter.MedianFilter(k))
    out = base.copy()
    out.paste(filtered, median_rect)
    threshold = int(spec.get('background_conf_restore', 0))
    if threshold:
        op = out.load(); bp = base.load(); w = base.width
        x0,y0,x1,y1 = median_rect
        for y in range(y0,y1):
            for x in range(x0,x1):
                if conf[y*w+x] >= threshold:
                    op[x,y] = bp[x,y]
    black_need = int(spec.get('background_black_majority', 0))
    if black_need:
        op = out.load()
        rgba_cells = [im.convert('RGBA') for im in cells]
        cps = [im.load() for im in rgba_cells]
        for y in range(base.height):
            for x in range(base.width):
                vals = [p[x,y] for p in cps]
                dark = [v for v in vals if v[3] > 60 and v[0] < 25 and v[1] < 35 and v[2] < 45]
                if len(dark) >= black_need:
                    dark = sorted(dark)
                    op[x,y] = dark[len(dark)//2]
    return out



def _scale_ui_preset_for_supersampling(preset: dict, factor: int) -> dict:
    q=dict(preset)
    for key in ('nominal_size','min_size','padding_x','padding_y','stroke_width','extrusion_stroke_width','shadow_stroke_width','offset_x','offset_y'):
        if key in q:
            q[key]=int(round(float(q[key])*factor))
    for key in ('extrusion_offset','shadow_offset'):
        if key in q:
            q[key]=[int(round(float(v)*factor)) for v in q[key]]
    if 'shadow_blur_radius' in q:
        q['shadow_blur_radius']=float(q['shadow_blur_radius'])*factor
    return q


def _make_layered_ui_label_supersampled(size, text: str, preset: dict, *, factor: int = 4, bleed_radius: int = 8):
    """Render text transforms at high resolution and downsample exactly once.

    The earlier implementation rasterized at final size and then sheared/rotated, which introduced
    visible stair-stepping on the battle names.  Keeping the entire geometry path
    at 4x and doing one LANCZOS downsample preserves the shipped-like tilt while
    producing smooth edges.
    """
    from PIL import Image
    w,h=map(int,size); f=max(1,int(factor))
    # Do not run transparent-RGB bleed on the enormous supersampled canvas;
    # it is a post-AA compressor aid, so applying it once after the final
    # downsample is equivalent for our purpose and dramatically cheaper.
    hi=_make_layered_ui_label((w*f,h*f),text,_scale_ui_preset_for_supersampling(preset,f),bleed_radius=0)
    lo=hi.resize((w,h),Image.Resampling.LANCZOS)
    return _bleed_transparent_rgb(lo,bleed_radius)


def _battle_name_effect_mask(cell):
    """Mask shipped battle lettering including face, edge, extrusion and shadow."""
    import numpy as np
    from PIL import Image, ImageFilter
    a=np.asarray(cell.convert('RGBA'),dtype=np.uint8)
    r,g,b,al=[a[...,i].astype(np.int16) for i in range(4)]
    # Bright white/pale-blue face only; the local dilation reaches the cyan edge,
    # dark-blue extrusion and black shadow without swallowing the splash artwork.
    face=(al>20)&(r>135)&(g>165)&(b>180)&((r+g+b)>530)
    seed=Image.fromarray((face.astype(np.uint8)*255),'L')
    return np.asarray(seed.filter(ImageFilter.MaxFilter(31)))>0


def _fill_rgba_holes_from_neighbors(arr, known, max_iter: int = 96):
    """Deterministic dependency-free fill for the few all-text donor holes."""
    import numpy as np
    rgba=arr.astype(np.float32).copy(); k=known.copy(); h,w=k.shape
    for _ in range(max_iter):
        missing=~k
        if not missing.any(): break
        sums=np.zeros((h,w,4),dtype=np.float32); cnt=np.zeros((h,w),dtype=np.float32)
        for dy,dx in ((-1,0),(1,0),(0,-1),(0,1),(-1,-1),(-1,1),(1,-1),(1,1)):
            ys0=max(0,-dy); ys1=min(h,h-dy); xs0=max(0,-dx); xs1=min(w,w-dx)
            yd0=ys0+dy; yd1=ys1+dy; xd0=xs0+dx; xd1=xs1+dx
            km=k[ys0:ys1,xs0:xs1]
            sums[yd0:yd1,xd0:xd1]+=rgba[ys0:ys1,xs0:xs1]*km[...,None]
            cnt[yd0:yd1,xd0:xd1]+=km
        take=missing&(cnt>0)
        if not take.any(): break
        rgba[take]=sums[take]/cnt[take,None]
        k[take]=True
    return np.clip(rgba,0,255).astype(np.uint8), k


def _battle_text_zone_clean_patch(original_im, spec: dict):
    """Recover only the old-text zone from the 15 shipped sibling cells."""
    import numpy as np
    from PIL import Image
    cells=[original_im.crop(tuple(map(int,item['rect']))).convert('RGBA') for item in spec['rows']]
    stack=np.stack([np.asarray(c,dtype=np.uint8) for c in cells],axis=0)
    extra_names=set(spec.get('extra_cleanup_names',[]))
    extra_size=int(spec.get('extra_cleanup_filter_size',21))
    if extra_size < 3 or extra_size % 2 == 0:
        die(f"{spec['id']}: extra battle cleanup filter must be odd and >=3")
    masks_list=[]
    for item,c in zip(spec['rows'],cells):
        m=_battle_name_effect_mask(c)
        if item.get('text') in extra_names:
            from PIL import ImageFilter
            mm=Image.fromarray((m.astype(np.uint8)*255),'L').filter(ImageFilter.MaxFilter(extra_size))
            m=np.asarray(mm)>0
        masks_list.append(m)
    masks=np.stack(masks_list,axis=0)
    n,h,w,_=stack.shape
    vals=(stack[...,0].astype(np.uint32)<<24)|(stack[...,1].astype(np.uint32)<<16)|(stack[...,2].astype(np.uint32)<<8)|stack[...,3].astype(np.uint32)
    valid=~masks
    best_count=np.zeros((h,w),dtype=np.uint8); best_val=np.zeros((h,w),dtype=np.uint32)
    for i in range(n):
        cand=vals[i]
        count=((vals==cand[None,...])&valid).sum(axis=0).astype(np.uint8)
        count=np.where(valid[i],count,0).astype(np.uint8)
        take=count>best_count
        best_count[take]=count[take]; best_val[take]=cand[take]
    recovered=np.stack([(best_val>>24)&255,(best_val>>16)&255,(best_val>>8)&255,best_val&255],axis=-1).astype(np.uint8)
    known=best_count>0
    # Seed unresolved pixels from the median donor colour, then propagate only
    # from known background pixels.  The result is used solely under old text.
    med=np.median(stack,axis=0).astype(np.uint8)
    recovered[~known]=med[~known]
    filled,_=_fill_rgba_holes_from_neighbors(recovered,known)
    bx0,by0,bx1,by1=map(int,spec.get('text_box',[0,24,343,145]))
    zone=np.zeros((h,w),dtype=bool); zone[by0:by1,bx0:bx1]=True
    # Return the full cell-sized patch plus one mask per source cell so callers
    # replace exactly the old text treatment and nothing else.
    return Image.fromarray(filled,'RGBA'), [m&zone for m in masks]


def render_ms2_battle_nameplates_font_v3(original_im, spec: dict):
    """Render a nameplate on the current, editable public background PNG.

    The public graphics/assets/ file is the source of truth. Its content is
    deliberately not pinned to a fixed hash: artists may refine it without a
    manifest or code edit. Switch graphics reuse fingerprints this entire
    source tree, so any changed bytes also invalidate the cached result.
    """
    from PIL import Image
    out = original_im.copy()
    preset = _ui_preset(spec['typography_preset'])
    background = _load_label_asset(spec['background_asset'])
    bx0, by0, bx1, by1 = map(int, spec.get('text_box', [0, 24, 343, 145]))
    scale = float(spec.get('final_scale', 0.94))
    ss = int(spec.get('supersample', 4))
    allow = []
    for item in spec['rows']:
        x0, y0, x1, y1 = map(int, item['rect'])
        rect = (x0, y0, x1, y1)
        if background.size != (x1 - x0, y1 - y0):
            die(f"{spec['id']}: public nameplate background dimensions "
                f"{background.size} != {(x1-x0, y1-y0)}")
        allow.append(rect)
        # Replace ONLY this declared nameplate rectangle. Pixels outside it
        # are inherited verbatim from the source game image.
        cell = background.copy()
        label = _make_layered_ui_label_supersampled(
            (bx1 - bx0, by1 - by0), item['text'], preset,
            factor=ss, bleed_radius=6)
        if abs(scale - 1.0) > 1e-6:
            nw = max(1, round(label.width * scale))
            nh = max(1, round(label.height * scale))
            label = label.resize((nw, nh), Image.Resampling.LANCZOS)
        xx = bx0 + (bx1 - bx0 - label.width) // 2
        yy = by0 + (by1 - by0 - label.height) // 2
        cell.alpha_composite(label, (xx, yy))
        out.paste(cell, (x0, y0))
    _assert_diff_inside_rects(original_im, out, allow, spec['id'])
    return out


def render_ms2_battle_nameplates_font_v2(original_im, spec: dict):
    """Rebuild the complete MS2 battle-name family with one tuned treatment."""
    out=original_im.copy()
    preset=_ui_preset(spec['typography_preset'])
    background=_load_label_asset(spec['background_asset'])
    bx0,by0,bx1,by1=map(int,spec.get('text_box',[0,24,343,145]))
    allow=[]
    for item in spec['rows']:
        x0,y0,x1,y1=map(int,item['rect'])
        if (x1-x0,y1-y0)!=background.size:
            die(f"{spec['id']}: battle target rect size drift")
        rect=(x0,y0,x1,y1); allow.append(rect)
        out.paste(background,(x0,y0))
        label=_make_layered_ui_label((bx1-bx0,by1-by0),item['text'],preset,bleed_radius=6)
        out.alpha_composite(label,(x0+bx0,y0+by0))
    _assert_diff_inside_rects(original_im,out,allow,spec['id'])
    return out


def render_ms2_battle_nameplates_v1(original_im, spec: dict):
    """Rebuild the seven localized MS2 battle nameplates from shipped raster art."""
    from PIL import Image, ImageFilter
    background = _battle_common_background(original_im, spec)
    if spec.get('background_strategy') == 'robust_clean':
        import numpy as np
        cells=[np.asarray(original_im.crop(tuple(r)).convert('RGBA'),dtype=np.uint8) for r in spec['background_cells']]
        stack=np.stack(cells,axis=0)
        med=np.median(stack,axis=0).astype(np.uint8)
        bx0,by0,bx1,by1=map(int,spec.get('text_box',[0,39,343,131]))
        # Median across many independently named cells removes most letter pixels while
        # retaining the common blue splash. A small median filter removes isolated dark
        # remnants without smearing the rest of the nameplate artwork.
        patch=Image.fromarray(med[by0:by1,bx0:bx1], 'RGBA').filter(ImageFilter.MedianFilter(5))
        background.paste(patch,(bx0,by0))
    glyphs = _build_raster_glyph_library(original_im, spec)
    out = original_im.copy()
    gap = int(spec.get('gap', -2))
    space_width = int(spec.get('space_width', 16))
    target_h = int(spec.get('target_height', 76))
    text_box = tuple(spec.get('text_box', [0,39,343,131]))
    for item in spec['replacements']:
        x0,y0,x1,y1 = item['rect']
        if (x1-x0, y1-y0) != background.size:
            die(f'{spec["id"]}: battle target rect size drift')
        out.paste(background, (x0,y0))
        label = _compose_raster_label(glyphs, item['to'], target_h, gap, space_width)
        bx0,by0,bx1,by1 = text_box
        label = fit_label(label, bx1-bx0-4, by1-by0-4)
        xx = x0 + bx0 + (bx1-bx0-label.width)//2
        yy = y0 + by0 + (by1-by0-label.height)//2
        out.alpha_composite(label, (xx,yy))
    return out


def render_msf_battle_nameplates_v1(original_im, spec: dict):
    """Rebuild Finale battle character labels while preserving title-case proportions."""
    background = _battle_common_background(original_im, spec)
    glyphs = _build_raster_glyph_library(original_im, spec)
    out = original_im.copy()
    gap = int(spec.get('gap', -1))
    space_width = int(spec.get('space_width', 12))
    descenders = str(spec.get('descenders', 'gpyqj'))
    text_box = tuple(spec.get('text_box', [0,28,400,148]))
    for item in spec['replacements']:
        x0,y0,x1,y1 = item['rect']
        if (x1-x0, y1-y0) != background.size:
            die(f'{spec["id"]}: Finale battle target rect size drift')
        out.paste(background, (x0,y0))
        label = _compose_mixedcase_label(glyphs, item['to'], gap, space_width, descenders)
        bx0,by0,bx1,by1 = text_box
        label = fit_label(label, bx1-bx0-4, by1-by0-4)
        xx = x0 + bx0 + (bx1-bx0-label.width)//2
        yy = y0 + by0 + (by1-by0-label.height)//2
        out.alpha_composite(label, (xx,yy))
    return out



def render_msf_battle_nameplates_font_v2(original_im, spec: dict):
    return _r32c3_core().render_battle(original_im,spec,_battle_common_background,
                                     _ui_preset,_ui_font,_assert_diff_inside_rects)

def apply_asset(asset: dict, helper: Path) -> dict:
    dataset = asset['dataset']
    archive = int(asset['archive'])
    dat, seeded_graphics_only = _ensure_rebuilt_archive(dataset, archive)

    source_tid, _ = extract_tid(dat, int(asset['gdat_index']), asset['cl3_inner_name'])
    got_sha = sha256_bytes(source_tid)
    variants = asset.get('source_variants', [])
    matched = next((v for v in variants if v['tid_sha256'] == got_sha), None)
    if matched is None:
        supported = ', '.join(v['tid_sha256'] for v in variants)
        die(f'{asset["id"]}: unsupported source TID {got_sha}; supported: {supported}. Do not patch an unknown game update blindly.')
    if tid_meta(source_tid) != (matched['width'], matched['height'], matched['format']):
        die(f'{asset["id"]}: source TID metadata disagrees with manifest variant')

    original = decode_tid(source_tid)
    renderer = asset['renderer']
    if renderer == 'result_nameplate_v2':
        rendered = render_result_v2(original, asset)
    elif renderer == 'raster_atlas_v1':
        rendered = render_raster_atlas_v1(original, asset)
    elif renderer == 'font_atlas_v2':
        rendered = render_font_atlas_v2(original, asset)
    elif renderer == 'multiline_raster_labels_v1':
        rendered = render_multiline_raster_labels_v1(original, asset)
    elif renderer == 'ms2_living_quarters_selector_v1':
        rendered = render_ms2_living_quarters_selector_v1(original, asset)
    elif renderer == 'ms2_myroom_tsuu_direct_v1':
        rendered = render_ms2_myroom_tsuu_direct_v1(original, asset)
    elif renderer == 'ms2_myroom_title_font_v2':
        rendered = render_ms2_myroom_title_font_v2(original, asset)
    elif renderer == 'ms2_myroom_title_font_v3':
        rendered = render_ms2_myroom_title_font_v3(original, asset)
    elif renderer == 'ms1_residence_names_font_v1':
        rendered = render_ms1_residence_names_font_v1(original, asset)
    elif renderer == 'ms1_residence_names_raster_v2':
        rendered = render_ms1_residence_names_raster_v2(original, asset)
    elif renderer == 'mixedcase_raster_atlas_v1':
        rendered = render_mixedcase_raster_atlas_v1(original, asset)
    elif renderer == 'ms2_battle_nameplates_v1':
        rendered = render_ms2_battle_nameplates_v1(original, asset)
    elif renderer == 'ms2_battle_nameplates_font_v2':
        rendered = render_ms2_battle_nameplates_font_v2(original, asset)
    elif renderer == 'ms2_battle_nameplates_font_v3':
        rendered = render_ms2_battle_nameplates_font_v3(original, asset)
    elif renderer == 'msf_battle_nameplates_v1':
        rendered = render_msf_battle_nameplates_v1(original, asset)
    elif renderer == 'msf_battle_nameplates_font_v2':
        rendered = render_msf_battle_nameplates_font_v2(original, asset)
    elif renderer == 'ms2_heroine_room_list_v1':
        donor = asset['donor']
        donor_tid, _ = extract_tid(dat, int(donor['gdat_index']), donor['cl3_inner_name'])
        donor_sha = sha256_bytes(donor_tid)
        if donor_sha != donor['tid_sha256']:
            die(f'{asset["id"]}: unsupported donor TID {donor_sha}; expected {donor["tid_sha256"]}. Check graphics build ordering/update layout.')
        if tid_meta(donor_tid) != (int(donor['width']), int(donor['height']), donor['format']):
            die(f'{asset["id"]}: donor TID metadata drift')
        donor_im = decode_tid(donor_tid)
        rendered = render_ms2_heroine_room_list_v1(original, donor_im, asset)
    elif renderer == 'ms2_room_selector_font_v4':
        rendered = render_ms2_room_selector_font_v4(original, asset)
    elif renderer == 'ms2_heroine_room_blue_font_v4':
        rendered = render_ms2_heroine_room_blue_font_v4(original, asset)
    elif renderer == 'ms2_heroine_room_blue_font_v5':
        rendered = render_ms2_heroine_room_blue_font_v5(original, asset)
    elif renderer == 'ms2_heroine_room_blue_font_v6':
        rendered = render_ms2_heroine_room_blue_font_v6(original, asset)
    elif renderer == 'ms2_camp_top2_character_names_font_v3':
        rendered = render_ms2_camp_top2_character_names_font_v3(original, asset)
    elif renderer == 'ms2_affection_font_v2':
        rendered = render_ms2_affection_font_v2(original, asset)
    elif renderer == 'ms2_camp_top_legacy_names_font_v2':
        rendered = render_ms2_camp_top_legacy_names_font_v2(original, asset)
    elif renderer == 'ms2_name_family_font_v1':
        rendered = render_ms2_name_family_font_v1(original, asset)
    elif renderer == 'ms2_result_font_v2':
        rendered = render_ms2_result_font_v2(original, asset)
    elif renderer == 'title_footer_chiaro_v1':
        rendered = render_title_footer_chiaro_v1(original, asset)
    elif renderer == 'title_copyright_footer_chiaro_v2':
        rendered = render_title_copyright_footer_chiaro_v2(original, asset)
    else:
        die(f'unsupported enabled renderer {renderer}')

    preview_dir = WORK / 'previews'
    preview_dir.mkdir(parents=True, exist_ok=True)
    original.save(preview_dir / f'{asset["id"]}_original.png')
    rendered.save(preview_dir / f'{asset["id"]}_rendered.png')

    fmt = matched['format']
    if fmt == 'BC7':
        payload = encode_bc7(rendered, helper, asset['id'], source_tid, original)
    elif fmt == 'DXT5':
        payload = encode_dxt5(rendered, source_tid, original)
    else:
        die(f'{asset["id"]}: enabled source format {fmt} is not writable')
    new_tid = make_tid(source_tid, payload)
    # Decoder acceptance check before archive replacement.
    roundtrip = decode_tid(new_tid)
    roundtrip.save(preview_dir / f'{asset["id"]}_roundtrip.png')
    if roundtrip.size != original.size:
        die(f'{asset["id"]}: encoded texture dimension drift')

    tmp = WORK / f'{dat.name}.tmp'
    changed = patch_dat(dat, tmp, asset, new_tid)
    before_sha = sha256_file(dat)
    after_sha = sha256_file(tmp)
    os.replace(tmp, dat)
    return {
        'id': asset['id'],
        'archive': archive,
        'archive_sha256_before_graphics': before_sha,
        'archive_sha256_after_graphics': after_sha,
        'source_tid_sha256': got_sha,
        'rendered_tid_sha256': sha256_bytes(new_tid),
        'changed_entry': changed,
        'renderer': asset['renderer'],
                'dataset': dataset,
        'graphics_only_archive_seeded': seeded_graphics_only,
    }



def apply_group_asset(asset: dict, helper: Path) -> dict:
    renderer = asset['renderer']
    if renderer not in {'ms2_dlc_job_labels_v1', 'ms2_heroine_room_banners_v1', 'ms2_heroine_room_banners_font_v2', 'ms2_heroine_room_banners_raster_v3'}:
        die(f'unsupported grouped renderer {renderer}')
    dataset = asset['dataset']
    archive = int(asset['archive'])
    dat, seeded_graphics_only = _ensure_rebuilt_archive(dataset, archive)

    source_tids = {}
    originals = {}
    for t in asset['targets']:
        tid, _ = extract_tid(dat, int(t['gdat_index']), t['inner_name'])
        got = sha256_bytes(tid)
        if got != t['tid_sha256']:
            die(f'{asset["id"]}/{t["inner_name"]}: unsupported source TID {got}; expected {t["tid_sha256"]}')
        if tid_meta(tid) != (int(t['width']), int(t['height']), t['format']):
            die(f'{asset["id"]}/{t["inner_name"]}: TID metadata drift')
        source_tids[t['inner_name']] = tid
        originals[t['inner_name']] = decode_tid(tid)

    if renderer == 'ms2_dlc_job_labels_v1':
        rendered = render_ms2_dlc_job_labels_v1(originals, asset)
    elif renderer == 'ms2_heroine_room_banners_v1':
        rendered = render_ms2_heroine_room_banners_v1(originals, asset)
    elif renderer == 'ms2_heroine_room_banners_raster_v3':
        rendered = render_ms2_heroine_room_banners_raster_v3(originals, asset)
    else:
        rendered = render_ms2_heroine_room_banners_font_v2(originals, asset)

    preview_dir = WORK / 'previews'
    preview_dir.mkdir(parents=True, exist_ok=True)
    changes=[]
    before_all = sha256_file(dat)
    target_by_name = {t['inner_name']: t for t in asset['targets']}
    for name, out in rendered.items():
        if name not in target_by_name:
            die(f'{asset["id"]}: renderer returned unknown grouped target {name}')
        t = target_by_name[name]
        original = originals[name]
        original.save(preview_dir / f'{asset["id"]}_{name}_original.png')
        out.save(preview_dir / f'{asset["id"]}_{name}_rendered.png')
        fmt=t['format']
        if fmt == 'DXT5':
            payload=encode_dxt5(out, source_tids[name], original)
        elif fmt == 'BC7':
            payload=encode_bc7(out,helper,f'{asset["id"]}_{t["gdat_index"]}',source_tids[name],original)
        else:
            die(f'{asset["id"]}/{name}: unsupported writable format {fmt}')
        new_tid=make_tid(source_tids[name],payload)
        rt=decode_tid(new_tid)
        if rt.size != original.size:
            die(f'{asset["id"]}/{name}: encoded texture dimension drift')
        rt.save(preview_dir / f'{asset["id"]}_{name}_roundtrip.png')
        child={'gdat_index':int(t['gdat_index']),'cl3_inner_name':name}
        tmp=WORK / f'{dat.name}.{t["gdat_index"]}.tmp'
        changed=patch_dat(dat,tmp,child,new_tid)
        os.replace(tmp,dat)
        changes.append(changed)

    if renderer == 'ms2_heroine_room_banners_raster_v3':
        changed_idx=set(int(x) for x in asset.get('changed_gdat_indices', []))
        expected_changed={t['inner_name'] for t in asset['targets'] if int(t['gdat_index']) in changed_idx}
    elif renderer in {'ms2_heroine_room_banners_v1', 'ms2_heroine_room_banners_font_v2'}:
        expected_changed={r.get('inner_name') for r in asset.get('replacements', [])}
    else:
        expected_changed=set(rendered)
    if renderer in {'ms2_heroine_room_banners_v1', 'ms2_heroine_room_banners_font_v2', 'ms2_heroine_room_banners_raster_v3'} and set(rendered) != expected_changed:
        die(f'{asset["id"]}: grouped title output set drift: {sorted(rendered)} != {sorted(expected_changed)}')
    return {
        'id':asset['id'], 'archive':archive, 'dataset':dataset,
        'archive_sha256_before_graphics':before_all,
        'archive_sha256_after_graphics':sha256_file(dat),
        'changed_entries':changes, 'renderer':asset['renderer'],
        'graphics_only_archive_seeded':seeded_graphics_only,
    }

def main() -> None:
    manifest = load_manifest()
    enabled = [a for a in manifest['assets'] if a.get('enabled')]
    if not enabled:
        print('GRAPHICS: no enabled canonical assets')
        return
    if not os.environ.get('MARY_SKELTER_GRAPHICS_GO_ORCHESTRATED'):
        if WORK.exists():
            shutil.rmtree(WORK)
    WORK.mkdir(parents=True, exist_ok=True)
    if os.environ.get('MARY_SKELTER_GRAPHICS_NATIVE_BC7') == '1':
        native = Path(os.environ.get('MARY_SKELTER_GRAPHICS_BCN_BINARY', ''))
        if not native.is_file() or not os.access(native, os.X_OK):
            die('native native BC7 binary unavailable; no C fallback')
        helper = None
        encoder_meta = {'helper': 'pure_go_bcn_opt_in', 'quality': 6,
                        'max_alpha': 64, 'max_visible': 64}
    else:
        helper, encoder_meta = ensure_bc7_helper()
    results = []
    print('=== GRAPHICS CANONICAL ===')
    for asset in enabled:
        print(f'GRAPHICS: {asset["id"]}')
        if asset.get('grouped'):
            results.append(apply_group_asset(asset, helper))
        else:
            results.append(apply_asset(asset, helper))
    REPORT.parent.mkdir(parents=True, exist_ok=True)
    REPORT.write_text(json.dumps({
        'enabled_assets': len(enabled),
        'encoder': encoder_meta,
        'results': results,
        'status': 'PASS',
    }, indent=2) + '\n', encoding='utf-8')
    print(f'GRAPHICS: PASS ({len(results)} canonical asset(s)); report: {REPORT}')


if __name__ == '__main__':
    main()
