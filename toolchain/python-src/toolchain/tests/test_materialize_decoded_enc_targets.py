#!/usr/bin/env python3
"""Regression for exact decoded-ENC source target materialization."""
from __future__ import annotations

import importlib.util
import struct
import sys
import tempfile
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'tools'))

from mskit import enc, gdat  # noqa: E402

spec = importlib.util.spec_from_file_location(
    'materialize_decoded_enc_targets', ROOT / 'toolchain' / 'python-src' / 'scripts' / 'materialize_decoded_enc_targets.py')
mod = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(mod)


def enc_entry(decoded: bytes) -> bytes:
    compressed = enc.lzo1x_compress(decoded)
    row_off = 0x40
    payload = bytearray(struct.pack('<I', 1))
    payload += struct.pack('<III', 0x80000000 | len(compressed), len(decoded), row_off)
    payload += b'\0' * (row_off - len(payload))
    payload += compressed
    header = b'\0cne' + b'\0' * (0x80 - 4)
    body = zlib.compress(bytes(payload), 7)
    return header + b'BILZ' + struct.pack('>III', len(payload), len(body), 7) + body


def test_exact_locator_materializes_scanner_invisible_short_literal():
    decoded = bytearray(b'\x01\x02\x03\x04\0\0\0\0' + b'\0' * 32)
    off = 8
    old = b'A'  # Deliberately below the generic scanner's 3-letter threshold.
    capacity = 7
    decoded[off:off + len(old)] = old
    decoded[off + len(old):off + capacity + 1] = b'\0' * (capacity + 1 - len(old))
    entry = enc_entry(bytes(decoded))
    assert not enc.EncFile(entry).strings(), 'fixture unexpectedly visible to generic scanner'

    with tempfile.TemporaryDirectory() as td:
        master = Path(td)
        (master / 'GxArchivedFile061.dat').write_bytes(gdat.build([(0, entry)]))
        rec = mod.materialize_record(
            dataset='MS2_PC_LOCALIZED', master=master, archive=61, gidx=0,
            row_index=0, offset=off,
            spec={
                'localized': 'A',
                'record_key': 'MS2_PC_LOCALIZED/a061/g00000/top/row0000/off00000008/f0000',
                'field_index': 0,
            },
            capacity=capacity,
        )
    assert rec['text'] == 'A'
    assert rec['record_key'].endswith('/row0000/off00000008/f0000')
    assert rec['repack_metadata']['str_offset'] == off
    assert rec['repack_metadata']['slot_capacity'] == capacity
    assert rec['control_metadata']['decoded_row'] is True
    assert rec['control_metadata']['inner_lzo1x'] is True


def test_pinned_capacity_must_match_pristine_zero_padding():
    decoded = bytearray(b'\x01\x02\x03\x04\0\0\0\0' + b'\0' * 32)
    off = 8
    old = b'ABCD'
    decoded[off:off + len(old)] = old
    decoded[off + len(old)] = 0
    decoded[off + len(old) + 2] = 0x7F
    entry = enc_entry(bytes(decoded))
    with tempfile.TemporaryDirectory() as td:
        master = Path(td)
        (master / 'GxArchivedFile061.dat').write_bytes(gdat.build([(0, entry)]))
        try:
            mod.materialize_record(
                dataset='MS2_PC_LOCALIZED', master=master, archive=61, gidx=0,
                row_index=0, offset=off,
                spec={
                    'localized': 'ABCD',
                    'record_key': 'MS2_PC_LOCALIZED/a061/g00000/top/row0000/off00000008/f0000',
                    'field_index': 0,
                },
                capacity=len(old) + 3,
            )
        except SystemExit as exc:
            assert 'crosses nonzero' in str(exc)
        else:
            raise AssertionError('invalid decoded-ENC slot capacity was accepted')



def test_public_slot_manifest_covers_translation_targets_exactly():
    slots = mod.load_slots(ROOT / 'toolchain' / 'manifests' / 'decoded_enc_slots.tsv')
    targets = mod.load_targets(ROOT / 'translations')
    assert len(targets) == 54263
    assert set(slots) == set(targets)


def main():
    test_exact_locator_materializes_scanner_invisible_short_literal()
    test_pinned_capacity_must_match_pristine_zero_padding()
    test_public_slot_manifest_covers_translation_targets_exactly()
    print('Exact decoded-ENC target materialization regression: PASS')


if __name__ == '__main__':
    main()
