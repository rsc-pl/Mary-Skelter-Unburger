#!/usr/bin/env python3
"""Stage evidence-backed PC translation transfers to native Western Switch records.

Do not confuse this *bounded candidate matching* tool with a complete Switch text
port. Any source mismatch or ambiguous duplicate is skipped. The source JSONL
is never modified and no release ZIP is produced by this module.
"""
from __future__ import annotations

import argparse
import collections
import hashlib
import json
from pathlib import Path

TRANSLATIONS = {
    "ms2": ("MS2_STORY.jsonl", "MS2_SYSTEM_BIN_055.jsonl",
            "MS2_SYSTEM_ENC_052.jsonl", "MS2_SYSTEM_ENC_061.jsonl"),
    "finale": ("MSF_STORY.jsonl", "MSF_ARCHIVE_088.jsonl",
               "MSF_SYSTEM_ENC_055.jsonl", "MSF_SYSTEM_ENC_072.jsonl",
               "MSF_SYSTEM_ENC_088.jsonl", "MSF_SYSTEM_GDAT0.jsonl"),
}
TARGETS = {"ms2": "MS2_SWITCH_WESTERN", "finale": "MSF_SWITCH_WESTERN"}

def read_jsonl(path):
    with Path(path).open(encoding="utf-8") as f:
        for lineno, line in enumerate(f, 1):
            if not line.strip():
                continue
            try:
                yield json.loads(line)
            except (json.JSONDecodeError, UnicodeDecodeError) as e:
                raise ValueError(f"{path}:{lineno}: {e}") from e

def sha256(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()

def is_valid(s):
    return isinstance(s, str) and bool(s) and "\x00" not in s and not any(0xD800 <= ord(c) <= 0xDFFF for c in s)

def build_candidates(root, game):
    # Identical localized words can have *different* accepted translations.
    # Even identical replacements are ambiguous unless the occurrence itself
    # is unique. We use original localized text as exact primary evidence;
    # never assume JP-PC and Western Switch physical indices coincide.
    by_text = collections.defaultdict(list)
    all_original_counts = collections.Counter()
    counts = collections.Counter()
    for name in TRANSLATIONS[game]:
        p = root / "translations" / name
        if not p.is_file():
            raise FileNotFoundError(p)
        for r in read_jsonl(p):
            if r.get("game") != ("MS2" if game == "ms2" else "MSF"):
                raise ValueError(f"{p}: unexpected game {r.get('game')}")
            for field in r.get("fields", ()):
                counts["translation_fields"] += 1
                local, new = field.get("LOCALIZED"), field.get("EN")
                if not is_valid(local) or not is_valid(new):
                    counts["invalid_or_empty_translation"] += 1
                    continue
                all_original_counts[local] += 1
                if local == new:
                    counts["unchanged_translation"] += 1
                    continue
                status = field.get("mapping_status")
                if not isinstance(status, str) or not status.startswith("EXACT_"):
                    counts["unproven_pc_alignment"] += 1
                    continue
                # A candidate may be unique in the PC corpus but still not in
                # Western source; source-side uniqueness is checked separately.
                by_text[local].append((new, name, r.get("id"), field.get("kind")))
                counts["changed_translation_fields"] += 1
    return by_text, all_original_counts, counts

def plan(root, game, west_records, output, report_out):
    root = Path(root)
    west_records = Path(west_records)
    output, report_out = Path(output), Path(report_out)
    if not west_records.is_file():
        raise FileNotFoundError(west_records)
    if west_records.resolve() in (output.resolve(), report_out.resolve()):
        raise ValueError("cannot overwrite the original Western records")
    by_text, all_pc_counts, counts = build_candidates(root, game)
    west_hits = collections.Counter()
    western_keys = set()
    western_count = 0
    # First pass deliberately counts all Western occurrences (not just the
    # translated records). A common term appearing elsewhere is *not* unique.
    for r in read_jsonl(west_records):
        if r.get("source_dataset") != TARGETS[game]:
            raise ValueError(f"non-Western-Switch dataset at {r.get('record_key')}")
        if r.get("game") != ("MS2" if game == "ms2" else "MSF") or r.get("platform") != "Switch":
            raise ValueError(f"incorrect game/platform at {r.get('record_key')}")
        key = r.get("record_key")
        if not isinstance(key, str) or not key.startswith(TARGETS[game] + "/") or key in western_keys:
            raise ValueError(f"missing/duplicate/bad Western record key: {key}")
        western_keys.add(key)
        western_count += 1
        local = r.get("text")
        if local in by_text:
            west_hits[local] += 1
    counts["western_fields"] = western_count
    counts["western_matching_occurrences"] = sum(west_hits.values())
    counts["western_unique_localized_values"] = len(west_hits)

    # Second pass: only one-to-one UNIQUE exact localized text values, valid
    # encoding, strict raw-byte parity, and re-encode safety are eligible.
    edits = []
    ambiguous = []
    for r in read_jsonl(west_records):
        old = r.get("text")
        if old not in by_text:
            continue
        sources = by_text[old]
        if len(sources) != 1 or all_pc_counts[old] != 1 or west_hits[old] != 1:
            counts["ambiguous_western_occurrences"] += 1
            if len(ambiguous) < 12:
                ambiguous.append({"record_key": r["record_key"],
                                  "pc_occurrences": all_pc_counts[old], "western_occurrences": west_hits[old]})
            continue
        new, filename, record_id, kind = sources[0]
        enc = r.get("encoding")
        try:
            original_raw = bytes.fromhex(r["raw_hex"])
            if old.encode(enc, "strict") != original_raw:
                counts["original_raw_mismatch"] += 1
                continue
            encoded = new.encode(enc, "strict")
            if not encoded or b"\0" in encoded:
                counts["new_text_bad_encoding"] += 1
                continue
        except (LookupError, UnicodeError, ValueError, KeyError, TypeError):
            counts["encoding_or_metadata_error"] += 1
            continue
        if r.get("decode_errors", 0):
            counts["decode_error"] += 1
            continue
        if not (r.get("repack_metadata") or {}).get("blob_type") in {"gnl", "enc_row", "bin_string"}:
            counts["unsupported_repack_format"] += 1
            continue
        staged = dict(r)
        staged["text"] = new
        # Provenance lives in the private report, never in engine record keys.
        edits.append(staged)
        counts["safe_exact_unique_fields"] += 1
    counts["skipped_changed_pc_fields"] = max(0, counts["changed_translation_fields"] - counts["safe_exact_unique_fields"])
    report = {
        "schema_version": 1,
        "status": "PARTIAL_CANDIDATE_ONLY_NOT_RELEASE_READY",
        "game": game,
        "source_records_sha256": sha256(west_records),
        "method": "unique-exact-Western-localized-string-with-verified-original-raw-and-PC-exact-alignment",
        "counts": dict(counts),
        "first_ambiguous": ambiguous,
        "full_text_port_complete": False,
        "warning": "These are uniquely matched preliminary edits, NOT a full verified Western Switch semantic map. Do not distribute as a finished text patch.",
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    report_out.parent.mkdir(parents=True, exist_ok=True)
    with output.open("w", encoding="utf-8") as f:
        for row in edits:
            f.write(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n")
    report_out.write_text(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return report

def main(argv=None):
    p = argparse.ArgumentParser(description="Evidence-only native Western Switch text-port candidate planner")
    p.add_argument("--project-root", type=Path, required=True)
    p.add_argument("--game", choices=tuple(TRANSLATIONS), required=True)
    p.add_argument("--western-records", type=Path, required=True)
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--report", type=Path, required=True)
    a = p.parse_args(argv)
    rep = plan(a.project_root, a.game, a.western_records, a.out, a.report)
    print(json.dumps(rep, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
