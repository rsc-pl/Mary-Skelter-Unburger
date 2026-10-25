#!/usr/bin/env python3
"""Mary Skelter 2 - Nintendo Switch -> PC save converter.

Converts the SaveDataBE family from the Switch layout (inner main save size
0x2856E8) to the Ghostlight PC layout (inner main save size 0x2857A0).

The conversion is based on direct comparison of the Switch ARM64 NSO and the
PC x64 executable constructors/copy routines. The 0xB8-byte size difference is
padding/alignment only; no gameplay fields are invented.

Input directory must contain:
  SaveDataBE.slot
  SaveDataBE_XXXX.sav
  SaveDataBE_XXXX.savslot

Output contains the same three names wrapped in the PC 12-byte file envelope.
Do NOT replace PC SystemData_0000.sav with any Switch file.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import struct
import sys
from pathlib import Path

SW_MAIN_SIZE = 0x2856E8
PC_MAIN_SIZE = 0x2857A0
SLOT_TABLE_SIZE = 0x11F8  # 4600
SAVSLOT_SIZE = 0x9C       # 156
PC_WRAPPER_SIZE = 12

# Original Switch-file offsets at which PC padding is inserted.
# For the first table there are 20 records of 0x798 bytes; x64 adds 8 bytes
# at relative record offset 0x280, making each PC record 0x7A0 bytes.
INSERTIONS = [(0x10 + i * 0x798 + 0x280, 8) for i in range(20)] + [
    (0x98AC, 4),
    (0x12CF8, 12),
    (0x22004C, 4),
    (0x2816C0, 4),
]
INSERTIONS.sort()


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def md5(data: bytes) -> bytes:
    return hashlib.md5(data).digest()


def dword_sum(data: bytes) -> int:
    """PC outer-wrapper checksum: sum of LE uint32 words modulo 2^32."""
    if len(data) % 4:
        raise ValueError(f"inner file size {len(data)} is not DWORD-aligned")
    total = 0
    for (word,) in struct.iter_unpack('<I', data):
        total = (total + word) & 0xFFFFFFFF
    return total


def pc_wrap(inner: bytes) -> bytes:
    return struct.pack('<QI', 1, dword_sum(inner)) + inner


def pc_unwrap(blob: bytes) -> bytes:
    if len(blob) < PC_WRAPPER_SIZE:
        raise ValueError('file too small for PC wrapper')
    marker, checksum = struct.unpack_from('<QI', blob, 0)
    inner = blob[PC_WRAPPER_SIZE:]
    if marker != 1:
        raise ValueError(f'bad PC wrapper marker: {marker:#x} (expected 1)')
    actual = dword_sum(inner)
    if checksum != actual:
        raise ValueError(f'bad PC wrapper checksum: stored={checksum:08x}, actual={actual:08x}')
    return inner


def switch_main_to_pc(sw: bytes) -> bytes:
    if len(sw) != SW_MAIN_SIZE:
        raise ValueError(f'Switch main save has size {len(sw):#x}; expected {SW_MAIN_SIZE:#x}')
    declared = struct.unpack_from('<I', sw, 0)[0]
    if declared != SW_MAIN_SIZE:
        raise ValueError(f'Switch main save declares size {declared:#x}; expected {SW_MAIN_SIZE:#x}')

    out = bytearray()
    src = 0
    for off, amount in INSERTIONS:
        if off < src or off > len(sw):
            raise AssertionError(f'invalid insertion map at {off:#x}')
        out += sw[src:off]
        out += b'\x00' * amount
        src = off
    out += sw[src:]

    if len(out) != PC_MAIN_SIZE:
        raise AssertionError(f'converted main size {len(out):#x} != {PC_MAIN_SIZE:#x}')
    struct.pack_into('<I', out, 0, PC_MAIN_SIZE)
    return bytes(out)


def pc_main_to_switch_for_roundtrip(pc: bytes) -> bytes:
    """Internal verifier: remove known x64-only padding and restore Switch size."""
    if len(pc) != PC_MAIN_SIZE:
        raise ValueError(f'PC inner main size {len(pc):#x}; expected {PC_MAIN_SIZE:#x}')

    # Translate original Switch offsets to their positions in the expanded PC
    # file, then remove in descending order so earlier offsets remain stable.
    positions = []
    added = 0
    for sw_off, amount in INSERTIONS:
        pc_off = sw_off + added
        positions.append((pc_off, amount))
        added += amount

    out = bytearray(pc)
    for pc_off, amount in reversed(positions):
        pad = out[pc_off:pc_off + amount]
        if pad != b'\x00' * amount:
            raise ValueError(
                f'expected zero x64 padding at PC offset {pc_off:#x}, found {pad.hex()}'
            )
        del out[pc_off:pc_off + amount]

    if len(out) != SW_MAIN_SIZE:
        raise AssertionError('round-trip depadding produced wrong size')
    struct.pack_into('<I', out, 0, SW_MAIN_SIZE)
    return bytes(out)


def find_triplet(src: Path):
    if not src.is_dir():
        raise ValueError('input must be a directory containing extracted Switch save files')
    slot_table = src / 'SaveDataBE.slot'
    saves = sorted(src.glob('SaveDataBE_*.sav'))
    saves = [p for p in saves if p.name != 'SaveDataBE.slot']
    if not slot_table.exists():
        raise ValueError('missing SaveDataBE.slot')
    if len(saves) != 1:
        raise ValueError(f'expected exactly one SaveDataBE_XXXX.sav, found {len(saves)}')
    save = saves[0]
    savslot = src / (save.stem + '.savslot')
    if not savslot.exists():
        raise ValueError(f'missing matching {savslot.name}')
    return slot_table, save, savslot


def convert(src: Path, outdir: Path, overwrite: bool = False) -> dict:
    slot_path, save_path, savslot_path = find_triplet(src)
    slot = slot_path.read_bytes()
    sw_main = save_path.read_bytes()
    savslot = bytearray(savslot_path.read_bytes())

    if len(slot) != SLOT_TABLE_SIZE:
        raise ValueError(f'SaveDataBE.slot size {len(slot):#x}; expected {SLOT_TABLE_SIZE:#x}')
    if len(savslot) != SAVSLOT_SIZE:
        raise ValueError(f'{savslot_path.name} size {len(savslot):#x}; expected {SAVSLOT_SIZE:#x}')
    if savslot[:8] != b'SAVE0001':
        raise ValueError(f'{savslot_path.name}: missing SAVE0001 signature')

    old_md5 = bytes(savslot[0x18:0x28])
    actual_old_md5 = md5(sw_main)
    if old_md5 != actual_old_md5:
        raise ValueError(
            f'{savslot_path.name}: MD5 does not match Switch .sav: '
            f'stored={old_md5.hex()} actual={actual_old_md5.hex()}'
        )

    pc_main = switch_main_to_pc(sw_main)

    # Strong internal proof: padding conversion must be exactly reversible.
    roundtrip = pc_main_to_switch_for_roundtrip(pc_main)
    if roundtrip != sw_main:
        raise AssertionError('Switch -> PC -> Switch round-trip is not byte-identical')

    new_md5 = md5(pc_main)
    savslot[0x18:0x28] = new_md5
    pc_savslot_inner = bytes(savslot)

    outputs = {
        slot_path.name: pc_wrap(slot),
        save_path.name: pc_wrap(pc_main),
        savslot_path.name: pc_wrap(pc_savslot_inner),
    }

    outdir.mkdir(parents=True, exist_ok=True)
    for name, data in outputs.items():
        dst = outdir / name
        if dst.exists() and not overwrite:
            raise FileExistsError(f'{dst} exists; use --overwrite or another output directory')
        dst.write_bytes(data)

    report = {
        'format': 'Mary Skelter 2 Switch -> PC SaveDataBE conversion report',
        'source_directory': str(src.resolve()),
        'source_slot': save_path.stem.replace('SaveDataBE_', ''),
        'layout': {
            'switch_main_inner_size': hex(SW_MAIN_SIZE),
            'pc_main_inner_size': hex(PC_MAIN_SIZE),
            'inserted_padding_total': hex(PC_MAIN_SIZE - SW_MAIN_SIZE),
            'pc_outer_wrapper_size': PC_WRAPPER_SIZE,
        },
        'source': {
            slot_path.name: {'size': len(slot), 'sha256': sha256(slot)},
            save_path.name: {'size': len(sw_main), 'sha256': sha256(sw_main), 'md5': actual_old_md5.hex()},
            savslot_path.name: {'size': len(savslot_path.read_bytes()), 'sha256': sha256(savslot_path.read_bytes())},
        },
        'converted': {},
        'savslot': {
            'preserved_identifier_0x08_0x17': pc_savslot_inner[0x08:0x18].hex(),
            'old_main_md5_0x18_0x27': old_md5.hex(),
            'new_main_md5_0x18_0x27': new_md5.hex(),
        },
        'roundtrip_switch_pc_switch_byte_identical': True,
        'notes': [
            'SystemData_0000.sav is intentionally not generated or replaced.',
            'The 16-byte identifier at SAVSLOT offset 0x08 is preserved verbatim; it is present in the same cross-platform SAVSLOT layout and is not the main-file integrity MD5.',
        ],
    }
    for name, data in outputs.items():
        inner = pc_unwrap(data)
        report['converted'][name] = {
            'pc_file_size': len(data),
            'inner_size': len(inner),
            'wrapper_checksum': f'{struct.unpack_from("<I", data, 8)[0]:08x}',
            'sha256': sha256(data),
        }

    (outdir / 'conversion_report.json').write_text(json.dumps(report, indent=2), encoding='utf-8')
    return report


def verify_pc_dir(src: Path) -> None:
    slot_path, save_path, savslot_path = find_triplet(src)
    slot = pc_unwrap(slot_path.read_bytes())
    main = pc_unwrap(save_path.read_bytes())
    savslot = pc_unwrap(savslot_path.read_bytes())
    if len(slot) != SLOT_TABLE_SIZE:
        raise ValueError('PC SaveDataBE.slot inner size mismatch')
    if len(main) != PC_MAIN_SIZE:
        raise ValueError(f'PC main inner size {len(main):#x}; expected {PC_MAIN_SIZE:#x}')
    if struct.unpack_from('<I', main, 0)[0] != PC_MAIN_SIZE:
        raise ValueError('PC main embedded size mismatch')
    if len(savslot) != SAVSLOT_SIZE or savslot[:8] != b'SAVE0001':
        raise ValueError('PC savslot format mismatch')
    stored = savslot[0x18:0x28]
    actual = md5(main)
    if stored != actual:
        raise ValueError(f'PC savslot MD5 mismatch: {stored.hex()} != {actual.hex()}')
    print('PASS: PC wrappers, sizes, SAVE0001 and main-save MD5 are valid.')
    print(f'  {save_path.name}: inner={len(main):#x}, md5={actual.hex()}')


def main() -> int:
    ap = argparse.ArgumentParser(description='Convert Mary Skelter 2 Switch SaveDataBE save to Ghostlight PC format.')
    sub = ap.add_subparsers(dest='cmd', required=True)
    c = sub.add_parser('convert', help='convert extracted Switch save directory to PC files')
    c.add_argument('input_dir', type=Path)
    c.add_argument('output_dir', type=Path)
    c.add_argument('--overwrite', action='store_true')
    v = sub.add_parser('verify-pc', help='verify an already converted/extracted PC SaveDataBE triplet')
    v.add_argument('input_dir', type=Path)
    args = ap.parse_args()

    try:
        if args.cmd == 'convert':
            report = convert(args.input_dir, args.output_dir, args.overwrite)
            print('PASS: Switch -> PC conversion complete.')
            print(f"  output: {args.output_dir.resolve()}")
            print(f"  PC inner main size: {report['layout']['pc_main_inner_size']}")
            print(f"  new main MD5: {report['savslot']['new_main_md5_0x18_0x27']}")
            print('  NOTE: keep the PC SystemData_0000.* files; do not replace them.')
        elif args.cmd == 'verify-pc':
            verify_pc_dir(args.input_dir)
        return 0
    except Exception as exc:
        print(f'ERROR: {exc}', file=sys.stderr)
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
