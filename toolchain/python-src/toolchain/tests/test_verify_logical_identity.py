#!/usr/bin/env python3
"""Regression tests for verification across legacy keys and STCM relocation."""
from __future__ import annotations

import sys
from pathlib import Path
import json
import struct
import tempfile
import zlib

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / 'toolchain' / 'python-src' / 'toolchain' / 'tools'))

from mskit.verify import _logical_index, verify_dataset  # noqa: E402
from mskit import enc, gdat  # noqa: E402


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



def test_partial_expected_subset_uses_complete_source_ranks():
    source = [
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=100,
            text='source-only-a', key='src/a'),
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=200,
            text='localized', key='src/target'),
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=300,
            text='source-only-b', key='src/b'),
    ]
    # Public/raw target contains only the one player-facing field.
    expected_subset = [
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=200,
            text='edited', key='public/target'),
    ]
    # A legal rebuilt row may move physical offsets while preserving string order.
    rebuilt = [
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=100,
            text='source-only-a', key='new/a'),
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=205,
            text='edited', key='new/target'),
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=305,
            text='source-only-b', key='new/b'),
    ]
    want, wc = _logical_index(expected_subset, rank_rows=source)
    got, gc = _logical_index(rebuilt)
    assert not wc and not gc
    assert len(want) == 1
    assert set(want) <= set(got)
    key = next(iter(want))
    assert got[key]['text'] == 'edited'


def test_partial_expected_subset_without_complete_ranks_would_renumber():
    source = [
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=100,
            text='source-only-a', key='src/a'),
        rec(role='enc_string', archive=52, gdat=1, cl3=None, row=10, soff=200,
            text='localized', key='src/target'),
    ]
    subset = [source[1]]
    full, _ = _logical_index(source)
    wrong, _ = _logical_index(subset)
    fixed, _ = _logical_index(subset, rank_rows=source)
    assert set(wrong) != {k for k, v in full.items() if v['record_key'] == 'src/target'}
    assert set(fixed) == {k for k, v in full.items() if v['record_key'] == 'src/target'}



def _enc_entry_with_row(row: bytes) -> bytes:
    count = 1
    table_end = 4 + 12
    row_off = (table_end + 0x3F) & ~0x3F
    payload = bytearray(struct.pack('<I', count))
    payload += struct.pack('<III', len(row), 0, row_off)
    payload += b'\0' * (row_off - len(payload))
    payload += row
    header = b'\0cne' + b'\0' * (0x80 - 4)
    body = zlib.compress(bytes(payload), 7)
    return header + b'BILZ' + struct.pack('>III', len(payload), len(body), 7) + body


def test_enc_verifier_uses_decoded_row_not_content_scanner():
    # One-kanji source has only one alphabetic Unicode character, so the
    # conservative generic ENC scanner does not export it (min_letters=3).
    # Replacing it with longer English makes it visible after rebuild.  This
    # must not look like a new structural record.
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        source = root / 'source'; rebuilt = root / 'rebuilt'
        source.mkdir(); rebuilt.mkdir()
        off = 8
        old = '愛'.encode('utf-8')
        capacity = 16
        row = bytearray(b'\x01\x02\x03\x04\0\0\0\0' + b'\0' * 64)
        row[off:off+len(old)] = old
        row[off+len(old):off+capacity+1] = b'\0' * (capacity + 1 - len(old))
        src_entry = _enc_entry_with_row(bytes(row))
        src_archive = gdat.build([(0, src_entry)])
        (source / 'GxArchivedFile052.dat').write_bytes(src_archive)

        sf = enc.EncFile(src_entry)
        new = b'Love Style'
        new_entry = sf.rebuild({(0, off): {
            'old': old, 'new': new, 'slot_capacity': capacity, 'record_key': 'short-jp'
        }})
        (rebuilt / 'GxArchivedFile052.dat').write_bytes(gdat.build([(0, new_entry)]))

        expected = root / 'expected.jsonl'
        rec = {
            'record_key': 'MS2_PC_LOCALIZED/a052/g00000/top/row0000/off00000008/f0000',
            'archive_number': 52, 'gdat_index': 0, 'cl3_inner_index': None,
            'field_role': 'enc_string', 'record_index': 0, 'field_index': 0,
            'container_key': 'top', 'encoding': 'utf-8', 'text': 'Love Style',
            'raw_hex': old.hex(),
            'repack_metadata': {'blob_type': 'enc_row', 'row': 0,
                                'str_offset': off, 'str_len': len(old),
                                'slot_capacity': capacity},
        }
        expected.write_text(json.dumps(rec, ensure_ascii=False) + '\n', encoding='utf-8')
        rep = verify_dataset('MS2_PC_LOCALIZED', rebuilt, expected,
                             archives=[52], source_master=source)
        assert rep['ok'], rep
        assert rep['enc_audit']['edits_checked'] == 1
        assert rep['source_extract_stats']['by_role'].get('enc_string', 0) == 0
        assert rep['extract_stats']['by_role'].get('enc_string', 0) == 1


def main():
    test_gnl_physical_offset_can_move()
    test_legacy_enc_f000_collisions_are_split_by_string_order()
    test_partial_expected_subset_uses_complete_source_ranks()
    test_partial_expected_subset_without_complete_ranks_would_renumber()
    test_enc_verifier_uses_decoded_row_not_content_scanner()
    print('Logical verifier regression: PASS')


if __name__ == '__main__':
    main()
