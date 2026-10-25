"""Raw technical text records and the JSONL export format.

A record is deliberately *technical*: it carries the text, the exact original
encoded bytes and enough provenance to rebuild the owning archive byte-for-byte.
Nothing here is a translation artefact.
"""
from __future__ import annotations

import hashlib
import json
from pathlib import Path

SCHEMA_VERSION = 1


def decode_text(raw: bytes, encoding: str) -> tuple[str, int]:
    """Lossless text decode. Returns (text, error_count).

    ``surrogateescape`` keeps undecodable bytes recoverable, so
    ``text.encode(encoding, 'surrogateescape')`` reproduces the input exactly.
    """
    if encoding == "utf-8":
        try:
            return raw.decode("utf-8"), 0
        except UnicodeDecodeError:
            pass
    elif encoding == "cp932":
        try:
            return raw.decode("cp932"), 0
        except UnicodeDecodeError:
            pass
    return raw.decode(encoding, "surrogateescape"), 1


def encode_text(text: str, encoding: str) -> bytes:
    return text.encode(encoding, "surrogateescape")


def detect_language(text: str) -> str | None:
    """Mechanical script detection for a single string (evidence, not a guess).

    Returns 'ja' (kana present), 'ko' (Hangul present), 'zh' (Han only) or 'en'
    (Latin letters), or None when the string carries no diagnostic characters.
    """
    kana = hangul = han = latin = 0
    for ch in text:
        o = ord(ch)
        if 0x3040 <= o <= 0x30FF:
            kana += 1
        elif 0xAC00 <= o <= 0xD7A3 or 0x1100 <= o <= 0x11FF:
            hangul += 1
        elif 0x4E00 <= o <= 0x9FFF or 0x3400 <= o <= 0x4DBF:
            han += 1
        elif ch.isascii() and ch.isalpha():
            latin += 1
    if kana:
        return "ja"
    if hangul:
        return "ko"
    if han:
        return "zh"
    if latin:
        return "en"
    return None


def make_record(*, dataset: str, game: str, platform: str, language,
                source_file: str, source_file_sha256: str, archive_number: int,
                gdat_index: int, format_chain: list[str], cl3_inner_index=None,
                cl3_inner_name=None, container_key: str, table_key: str,
                record_index: int, field_index: int, field_role: str,
                encoding: str, text: str, raw: bytes, control: dict, repack: dict,
                decode_errors: int = 0) -> dict:
    key = (f"{dataset}/a{archive_number:03d}/g{gdat_index:05d}/{container_key}"
           f"/{table_key}/r{record_index:05d}/f{field_index:03d}")
    return {
        "record_key": key,
        "schema_version": SCHEMA_VERSION,
        "source_dataset": dataset,
        "game": game,
        "platform": platform,
        "language": language,
        "source_file": source_file,
        "source_file_sha256": source_file_sha256,
        "archive_number": archive_number,
        "gdat_index": gdat_index,
        "format_chain": format_chain,
        "cl3_inner_index": cl3_inner_index,
        "cl3_inner_name": cl3_inner_name,
        "container_key": container_key,
        "table_key": table_key,
        "record_index": record_index,
        "field_index": field_index,
        "field_role": field_role,
        "encoding": encoding,
        "text": text,
        "raw_hex": raw.hex(),
        "control_metadata": control,
        "repack_metadata": repack,
        "decode_errors": decode_errors,
    }


def write_jsonl(path: Path, records) -> int:
    path.parent.mkdir(parents=True, exist_ok=True)
    n = 0
    with path.open("w", encoding="utf-8") as fp:
        for rec in records:
            fp.write(json.dumps(rec, ensure_ascii=False, sort_keys=True))
            fp.write("\n")
            n += 1
    return n


def read_jsonl(path: Path):
    with Path(path).open("r", encoding="utf-8") as fp:
        for line in fp:
            line = line.strip()
            if not line:
                continue
            yield json.loads(line)


def sha256_file(path: Path, chunk: int = 1 << 20) -> str:
    h = hashlib.sha256()
    with Path(path).open("rb") as fp:
        while True:
            b = fp.read(chunk)
            if not b:
                break
            h.update(b)
    return h.hexdigest()
