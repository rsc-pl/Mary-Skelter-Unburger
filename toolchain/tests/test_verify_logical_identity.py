#!/usr/bin/env python3
"""Regression tests for verification across legacy keys and STCM relocation."""
from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'toolchain' / 'tools'))

from mskit.verify import _logical_index  # noqa: E402


def rec(*, role, archive=55, gdat=1, cl3=0, blob=None, row=None, soff=None,
        ridx=0, fidx=0, text='x', key='legacy'):
    meta = {'blob_type': 'gnl'}
    if role == 'gnl_str':
        meta.update(blob_offset=blob)
    elif role == 'enc_string':
        meta = {'blob_type': 'enc_row', 'row': row, 'str_offset': soff}
    elif role == 'bin_string':
        meta = {'blob_type': 'bin_string', 'string_index': ridx}
    return {
        'record_key': key,
        'archive_number': archive,
        'gdat_index': gdat,
        'cl3_inner_index': cl3,
        'field_role': role,
        'record_index': ridx,
        'field_index': fidx,
        'container_key': 'c0',
        'repack_metadata': meta,
        'text': text,
        'encoding': 'utf-8',
        'raw_hex': text.encode().hex(),
    }


def test_gnl_physical_offset_can_move():
    src = [
        rec(role='gnl_str', blob=0x100, ridx=0, fidx=3, text='a', key='src1'),
        rec(role='gnl_str', blob=0x200, ridx=0, fidx=3, text='b', key='src2'),
    ]
    rebuilt = [
        rec(role='gnl_str', blob=0x100, ridx=0, fidx=3, text='a', key='new1'),
        rec(role='gnl_str', blob=0x248, ridx=0, fidx=3, text='b', key='new2'),
    ]
    a, ac = _logical_index(src)
    b, bc = _logical_index(rebuilt)
    assert not ac and not bc
    assert set(a) == set(b)


def test_legacy_enc_f000_collisions_are_split_by_string_order():
    src = [
        rec(role='enc_string', archive=61, gdat=1032, cl3=None, row=0, soff=175,
            text='one', key='same/f000'),
        rec(role='enc_string', archive=61, gdat=1032, cl3=None, row=0, soff=320,
            text='two', key='same/f000'),
    ]
    rebuilt = [
        rec(role='enc_string', archive=61, gdat=1032, cl3=None, row=0, soff=175,
            text='one', key='new/f000'),
        rec(role='enc_string', archive=61, gdat=1032, cl3=None, row=0, soff=325,
            text='two', key='new/f001'),
    ]
    a, ac = _logical_index(src)
    b, bc = _logical_index(rebuilt)
    assert not ac and not bc
    assert len(a) == len(b) == 2
    assert set(a) == set(b)


def main():
    test_gnl_physical_offset_can_move()
    test_legacy_enc_f000_collisions_are_split_by_string_order()
    print('Logical verifier regression: PASS')


if __name__ == '__main__':
    main()
