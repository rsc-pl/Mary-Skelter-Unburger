#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import io
import json
import os
import struct
import sys
import tempfile
import zipfile
import zlib
from collections import defaultdict
from pathlib import Path

HERE = Path(__file__).resolve().parent
MAPPING = HERE / "switch_graphics_mapping_closed.json"
MS2_IPS = HERE / "accepted_ms2_latest_update.ips"
MSF_IPS = HERE / "accepted_finale_latest_update.ips"

FIXED_DT = (2026, 10, 7, 0, 0, 0)
BILZ = b"BILZ"

GAMES = {
    "ms2": {
        "title_id": "01003DE00C95E000",
        "build_id": "C16E154E683D7EB814B0AB840CBFBE36",
        "ips": MS2_IPS,
        "ips_sha256": "4459e23948249541fad2abff9ac0cda37dcebd8120ce40d73e14aa373fd017a1",
        "dataset": "MS2_PC_LOCALIZED",
        "mod_name": "UNBURGER_MS2_R32C3_FULL_GRAPHICS_PORT",
    },
    "finale": {
        "title_id": "0100530014438000",
        "build_id": "B1AFBB02475AD7E35618B4C0DAEEA1B1027AF62F",
        "ips": MSF_IPS,
        "ips_sha256": "d4aede5992744de8c97c2a570a7d3577974b7a551ac29cb09a818356907c76e5",
        "dataset": "MSF_PC_LOCALIZED",
        "mod_name": "UNBURGER_MSF_R32C3_FULL_GRAPHICS_PORT",
    },
}

SUPPORTED_RENDERERS = {
    "raster_atlas_v1",
    "font_atlas_v2",
    "mixedcase_raster_atlas_v1",
    "ms2_result_font_v2",
    "ms2_battle_nameplates_font_v3",
    "ms2_heroine_room_blue_font_v6",
    "ms2_myroom_title_font_v3",
    "ms2_affection_font_v2",
    "msf_battle_nameplates_v1",
    "msf_battle_nameplates_font_v2",
    "ms2_heroine_room_banners_raster_v3",
    "ms2_heroine_room_banners_font_v2",
    "ms1_residence_names_raster_v2",
    "ms2_room_selector_font_v4",
    "ms2_camp_top_legacy_names_font_v2",
    "ms2_camp_top2_character_names_font_v3",
    "ms2_name_family_font_v1",
    "title_copyright_footer_chiaro_v2",
}


def die(msg: str) -> None:
    raise SystemExit("ERROR: " + msg)


def sha_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def import_file(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if not spec or not spec.loader:
        die(f"cannot import {path}")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def load_project(project: Path):
    bgp = project / "toolchain/python-src/scripts/graphics/build_graphics.py"
    clp = project / "toolchain/python-src/toolchain/tools/mskit/cl3.py"
    mp = project / "graphics/manifest.json"
    for p in (bgp, clp, mp, project / "version.txt"):
        if not p.is_file():
            die(f"project root missing required file: {p}")
    bg = import_file("r32_build_graphics", bgp)
    clmod = import_file("r32_cl3", clp)
    manifest = json.loads(mp.read_text(encoding="utf-8"))
    return bg, clmod, manifest


def parse_gdat(data: bytes):
    if len(data) < 8 or data[:4] != b"GDAT":
        die("not GDAT")
    n = struct.unpack_from("<I", data, 4)[0]
    if not (0 < n < 5_000_000) or 8 + 8 * n > len(data):
        die(f"bad GDAT count {n}")
    out = []
    for i in range(n):
        off, size = struct.unpack_from("<II", data, 8 + 8 * i)
        if off + size > len(data):
            die(f"g{i} outside archive")
        out.append((off, size))
    return out


def find_bilz(entry: bytes):
    pos = 0
    stop = min(len(entry), 0x10000)
    while True:
        p = entry.find(BILZ, pos, stop)
        if p < 0:
            return None
        if p + 16 <= len(entry):
            usz, csz, algo = struct.unpack_from(">III", entry, p + 4)
            if 0 < usz < 2**31 and 0 < csz < 2**31 and algo in (0, 7) and p + 16 + csz <= len(entry):
                return p, usz, csz, algo
        pos = p + 1


def decode_entry(entry: bytes):
    bh = find_bilz(entry)
    if bh is None:
        return entry, None
    p, usz, csz, algo = bh
    body = entry[p + 16 : p + 16 + csz]
    out = body if algo == 0 else zlib.decompress(body)
    if len(out) != usz:
        die(f"BILZ decoded size {len(out)} != {usz}")
    return out, bh


def wrap_reflow(stock_entry: bytes, new_payload: bytes):
    _, bh = decode_entry(stock_entry)
    if bh is None:
        return new_payload, {
            "wrapper": "plain",
            "entry_size_before": len(stock_entry),
            "entry_size_after": len(new_payload),
            "entry_size_delta": len(new_payload) - len(stock_entry),
        }
    p, old_usz, old_csz, algo = bh
    suffix = stock_entry[p + 16 + old_csz :]
    body = new_payload if algo == 0 else zlib.compress(new_payload, 9)
    out = stock_entry[:p] + BILZ + struct.pack(">III", len(new_payload), len(body), algo) + body + suffix
    chk, _ = decode_entry(out)
    if chk != new_payload:
        die("BILZ reflow roundtrip failed")
    return out, {
        "wrapper": "BILZ",
        "bilz_offset": p,
        "algorithm": algo,
        "uncompressed_size_before": old_usz,
        "uncompressed_size_after": len(new_payload),
        "compressed_size_before": old_csz,
        "compressed_size_after": len(body),
        "preserved_suffix_bytes": len(suffix),
        "entry_size_before": len(stock_entry),
        "entry_size_after": len(out),
        "entry_size_delta": len(out) - len(stock_entry),
    }


def rebuild_gdat(src: bytes, records, replacements: dict[int, bytes]) -> bytes:
    entries = [replacements.get(i, src[o : o + s]) for i, (o, s) in enumerate(records)]
    table = bytearray()
    body = bytearray()
    off = 8 + 8 * len(entries)
    for e in entries:
        table += struct.pack("<II", off, len(e))
        body += e
        off += len(e)
    return b"GDAT" + struct.pack("<I", len(entries)) + bytes(table) + bytes(body)


def verify_non_targets(src: bytes, srec, out: bytes, orec, targets: set[int]):
    if len(srec) != len(orec):
        die("GDAT entry count changed")
    for i, ((so, ss), (oo, osz)) in enumerate(zip(srec, orec)):
        if i in targets:
            continue
        if ss != osz or src[so : so + ss] != out[oo : oo + osz]:
            die(f"non-target GDAT payload changed at g{i}")


def find_member(container, name: str):
    hits = [f for f in container.files if f.name.casefold() == name.casefold()]
    if len(hits) != 1:
        die(f"expected exactly one CL3 member {name!r}, found {len(hits)}")
    return hits[0]


def mapping_rows(mapping: dict, game: str):
    return mapping["games"][game]["targets"]


def manifest_asset_map(manifest: dict):
    return {a["id"]: a for a in manifest.get("assets", []) if a.get("enabled")}


def validate_static_contract(mapping: dict, manifest: dict):
    if mapping.get("phase") != "switch_graphics_mapping_v1":
        die("wrong frozen mapping phase")
    if mapping.get("applicable_source_target_count") != 40 or mapping.get("not_applicable_target_count") != 6:
        die("frozen mapping count drift; expected 40 applicable + 6 non-applicable")
    amap = manifest_asset_map(manifest)
    # RUNTIME-APPROVED SWITCH EXCEPTION (native): eight localized source labels
    # in g133 must be replaced with canonical whole-word V2. The other six
    # already-canonical original words remain pixel-identical. PC unchanged.
    special = {aid: spec.get("switch_native_policy") for aid, spec in amap.items()
               if spec.get("switch_native_policy")}
    if special != {"MS2_CAMP_TOP2_CHARACTER_NAMES": "canonical_whole_words_v2"}:
        die(f"Switch canonical V2 policy drift: {special}")
    seen_physical = set()
    applicable_assets = set()
    for game in ("ms2", "finale"):
        expected_dataset = GAMES[game]["dataset"]
        for row in mapping_rows(mapping, game):
            if row.get("format") != "DXT5":
                die(f"{row['logical_id']}: R32C3 frozen mapping expects native DXT5")
            aid = row["asset_id"]
            applicable_assets.add(aid)
            if aid not in amap:
                die(f"mapping asset missing from graphics manifest: {aid}")
            if amap[aid].get("dataset") != expected_dataset:
                die(f"{aid}: dataset mismatch for {game}")
            key = (game, row["archive"], int(row["gdat_index"]))
            if key in seen_physical:
                die(f"duplicate physical mapping {key}")
            seen_physical.add(key)
    na = mapping.get("not_applicable", [])
    if {x.get("asset_id") for x in na} != {"MS2_DLC_JOB_LABELS"} or len(na) != 6:
        die("non-applicable set drift")
    expected_assets = {
        a["id"] for a in manifest.get("assets", [])
        if a.get("enabled") and a.get("dataset") in {"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED"} and a["id"] != "MS2_DLC_JOB_LABELS"
    }
    if applicable_assets != expected_assets:
        missing = sorted(expected_assets - applicable_assets)
        extra = sorted(applicable_assets - expected_assets)
        die(f"applicable logical asset set drift: missing={missing}, extra={extra}")
    bad_renderers = sorted({amap[a]["renderer"] for a in applicable_assets} - SUPPORTED_RENDERERS)
    if bad_renderers:
        die(f"unsupported renderers in R32C3: {bad_renderers}")
    return {
        "applicable_source_targets": len(seen_physical),
        "applicable_logical_assets": len(applicable_assets),
        "not_applicable_targets": len(na),
        "renderers": sorted({amap[a]["renderer"] for a in applicable_assets}),
    }


def render_one(bg, original, spec: dict):
    r = spec["renderer"]
    if r == "raster_atlas_v1":
        return bg.render_raster_atlas_v1(original, spec)
    if r == "font_atlas_v2":
        return bg.render_font_atlas_v2(original, spec)
    if r == "mixedcase_raster_atlas_v1":
        return bg.render_mixedcase_raster_atlas_v1(original, spec)
    if r == "ms2_result_font_v2":
        return bg.render_ms2_result_font_v2(original, spec)
    if r == "ms2_battle_nameplates_font_v3":
        return bg.render_ms2_battle_nameplates_font_v3(original, spec)
    if r == "ms2_heroine_room_blue_font_v6":
        return bg.render_ms2_heroine_room_blue_font_v6(original, spec)
    if r == "ms2_myroom_title_font_v3":
        return bg.render_ms2_myroom_title_font_v3(original, spec)
    if r == "ms2_affection_font_v2":
        return bg.render_ms2_affection_font_v2(original, spec)
    if r == "msf_battle_nameplates_v1":
        return bg.render_msf_battle_nameplates_v1(original, spec)
    if r == "msf_battle_nameplates_font_v2":
        return bg.render_msf_battle_nameplates_font_v2(original, spec)
    if r == "ms1_residence_names_raster_v2":
        return bg.render_ms1_residence_names_raster_v2(original, spec)
    if r == "ms2_room_selector_font_v4":
        return bg.render_ms2_room_selector_font_v4(original, spec)
    if r == "ms2_camp_top_legacy_names_font_v2":
        return bg.render_ms2_camp_top_legacy_names_font_v2(original, spec)
    if r == "ms2_camp_top2_character_names_font_v3":
        return bg.render_ms2_camp_top2_character_names_font_v3(original, spec)
    if r == "ms2_name_family_font_v1":
        return bg.render_ms2_name_family_font_v1(original, spec)
    if r == "title_copyright_footer_chiaro_v2":
        return bg.render_title_copyright_footer_chiaro_v2(original, spec)
    die(f"non-grouped renderer dispatch missing: {r}")


def render_group(bg, images: dict[str, object], spec: dict):
    r = spec["renderer"]
    if r == "ms2_heroine_room_banners_raster_v3":
        return bg.render_ms2_heroine_room_banners_raster_v3(images, spec)
    if r == "ms2_heroine_room_banners_font_v2":
        return bg.render_ms2_heroine_room_banners_font_v2(images, spec)
    die(f"grouped renderer dispatch missing: {r}")


def changed_block_count(before: bytes, after: bytes) -> int:
    if len(before) != len(after) or len(before) % 16:
        die("DXT5 block payload size drift")
    return sum(before[i:i+16] != after[i:i+16] for i in range(0, len(before), 16))


def diff_preview(original, rendered, label: str, outdir: Path):
    from PIL import Image, ImageChops, ImageDraw
    o = original.convert("RGBA")
    r = rendered.convert("RGBA")
    diff = ImageChops.difference(o, r)
    bbox = diff.getbbox()
    if bbox is None:
        die(f"{label}: renderer produced no pixel changes")
    pad = 8
    x0 = max(0, bbox[0]-pad); y0 = max(0, bbox[1]-pad)
    x1 = min(o.width, bbox[2]+pad); y1 = min(o.height, bbox[3]+pad)
    oc = o.crop((x0,y0,x1,y1)); rc = r.crop((x0,y0,x1,y1))
    w = oc.width + rc.width + 12
    h = max(oc.height, rc.height) + 28
    sheet = Image.new("RGBA", (w,h), (24,24,24,255))
    sheet.alpha_composite(oc, (0,28)); sheet.alpha_composite(rc, (oc.width+12,28))
    d = ImageDraw.Draw(sheet)
    d.text((2,5), "ORIGINAL", fill=(255,255,255,255))
    d.text((oc.width+14,5), "RENDERED", fill=(255,255,255,255))
    safe = "".join(c if c.isalnum() or c in "._-" else "_" for c in label)
    path = outdir / f"{safe}.png"
    sheet.save(path)
    return list(bbox), path.name


def load_archive_sources(mapping: dict, game: str, master: Path):
    if not master.is_dir():
        die(f"{game}: missing masterNX64 directory: {master}")
    out = {}
    for name, meta in mapping["games"][game]["archives"].items():
        p = master / name
        if not p.is_file():
            die(f"{game}: required source archive missing: {p}")
        got = sha_file(p)
        if got != meta["sha256"]:
            die(f"{game}: {name} SHA-256 {got} != frozen Western/latest source {meta['sha256']}")
        raw = p.read_bytes()
        out[name] = {"path": p, "raw": raw, "records": parse_gdat(raw)}
    return out


def extract_target(archives: dict, row: dict, clmod, bg):
    ar = archives[row["archive"]]
    idx = int(row["gdat_index"])
    rec = ar["records"]
    if idx < 0 or idx >= len(rec):
        die(f"{row['logical_id']}: g{idx} outside {row['archive']}")
    off, size = rec[idx]
    stock_entry = ar["raw"][off:off+size]
    decoded, _ = decode_entry(stock_entry)
    if sha_bytes(decoded) != row["decoded_cl3_sha256"]:
        die(f"{row['logical_id']}: decoded CL3 SHA drift")
    c = clmod.Cl3(decoded)
    f = find_member(c, row["inner_name"])
    if f.index != int(row["member_index"]):
        die(f"{row['logical_id']}: CL3 member index drift {f.index} != {row['member_index']}")
    tid = c.file_data(f.index)
    if sha_bytes(tid) != row["tid_sha256"]:
        die(f"{row['logical_id']}: TID SHA drift")
    meta = bg.tid_meta(tid)
    expected = (int(row["width"]), int(row["height"]), row["format"])
    if meta != expected:
        die(f"{row['logical_id']}: TID metadata {meta} != {expected}")
    return {
        "archive": ar,
        "row": row,
        "stock_entry": stock_entry,
        "decoded": decoded,
        "container": c,
        "member": f,
        "tid": tid,
        "original": bg.decode_tid(tid),
    }


def rebuild_target_entry(source: dict, rendered, bg, clmod, preview_dir: Path, label: str):
    tid = source["tid"]
    original = source["original"]
    if bg.tid_meta(tid)[2] != "DXT5":
        die(f"{label}: R32C3 only accepts frozen DXT5 mapping")
    payload = bg.encode_dxt5(rendered, tid, original)
    new_tid = bg.make_tid(tid, payload)
    if new_tid[:0x80] != tid[:0x80]:
        die(f"{label}: TID header drift")
    roundtrip = bg.decode_tid(new_tid)
    if roundtrip.size != original.size:
        die(f"{label}: DXT5 roundtrip dimension drift")
    bbox, preview = diff_preview(original, rendered, label, preview_dir)

    c = source["container"]
    f = source["member"]
    new_cl3 = c.rebuild({f.index: new_tid})
    cc = clmod.Cl3(new_cl3)
    if len(c.files) != len(cc.files):
        die(f"{label}: CL3 file count changed")
    for bf, af in zip(c.files, cc.files):
        if bf.name != af.name:
            die(f"{label}: CL3 file order/name drift")
        if bf.index == f.index:
            if cc.file_data(af.index) != new_tid:
                die(f"{label}: target TID CL3 roundtrip mismatch")
        elif c.file_data(bf.index) != cc.file_data(af.index):
            die(f"{label}: non-target CL3 member payload changed: {bf.name}")
    wrapped, wrapinfo = wrap_reflow(source["stock_entry"], new_cl3)
    return wrapped, {
        "source_tid_sha256": sha_bytes(tid),
        "patched_tid_sha256": sha_bytes(new_tid),
        "source_cl3_sha256": sha_bytes(source["decoded"]),
        "patched_cl3_sha256": sha_bytes(new_cl3),
        "changed_4x4_blocks": changed_block_count(tid[0x80:], new_tid[0x80:]),
        "decoded_diff_bbox": bbox,
        "preview": preview,
        "wrapper": wrapinfo,
    }


def deterministic_zip(root: Path, out: Path):
    if out.exists():
        out.unlink()
    with zipfile.ZipFile(out, "w", compression=zipfile.ZIP_STORED, allowZip64=True) as z:
        for p in sorted(x for x in root.rglob("*") if x.is_file()):
            info = zipfile.ZipInfo(p.relative_to(root).as_posix(), FIXED_DT)
            info.external_attr = 0o644 << 16
            z.writestr(info, p.read_bytes(), compress_type=zipfile.ZIP_STORED)


def build(project: Path, ms2_master: Path, finale_master: Path, outzip: Path):
    mapping = json.loads(MAPPING.read_text(encoding="utf-8"))
    bg, clmod, manifest = load_project(project)
    static = validate_static_contract(mapping, manifest)
    amap = manifest_asset_map(manifest)
    version = (project / "version.txt").read_text(encoding="utf-8").strip()

    for game, g in GAMES.items():
        if sha_file(g["ips"]) != g["ips_sha256"]:
            die(f"{game}: bundled accepted IPS hash mismatch")

    masters = {"ms2": ms2_master.resolve(), "finale": finale_master.resolve()}
    archives = {game: load_archive_sources(mapping, game, masters[game]) for game in ("ms2", "finale")}
    rows_by_asset = {game: defaultdict(list) for game in ("ms2", "finale")}
    for game in ("ms2", "finale"):
        for row in mapping_rows(mapping, game):
            rows_by_asset[game][row["asset_id"]].append(row)

    replacements = {game: defaultdict(dict) for game in ("ms2", "finale")}
    target_reports = []
    preserved_target_reports = []
    canonical_target_reports = []
    source_target_count = 0
    modified_target_count = 0

    with tempfile.TemporaryDirectory(prefix="ms-r32c3-previews-") as ptd:
        preview_dir = Path(ptd)
        for game in ("ms2", "finale"):
            for aid in sorted(rows_by_asset[game]):
                spec = amap[aid]
                rows = rows_by_asset[game][aid]
                source_target_count += len(rows)
                if spec.get("grouped"):
                    by_name = {}
                    srcs = {}
                    for row in rows:
                        src = extract_target(archives[game], row, clmod, bg)
                        by_name[row["inner_name"]] = src["original"]
                        srcs[row["inner_name"]] = src
                    expected_names = {t["inner_name"] for t in spec.get("targets", [])}
                    if set(by_name) != expected_names:
                        die(f"{aid}: grouped Switch mapping name set drift")
                    rendered = render_group(bg, by_name, spec)
                    if not rendered:
                        die(f"{aid}: grouped renderer returned no changed outputs")
                    for name, out_im in rendered.items():
                        if name not in srcs:
                            die(f"{aid}: renderer returned unmapped member {name}")
                        src = srcs[name]
                        row = src["row"]
                        new_entry, rep = rebuild_target_entry(src, out_im, bg, clmod, preview_dir, f"{game}__{aid}__{name}")
                        idx = int(row["gdat_index"])
                        if idx in replacements[game][row["archive"]]:
                            die(f"duplicate replacement for {game}/{row['archive']}/g{idx}")
                        replacements[game][row["archive"]][idx] = new_entry
                        rep.update({
                            "game": game, "asset_id": aid, "logical_id": row["logical_id"],
                            "archive": row["archive"], "gdat_index": idx, "inner_name": name,
                            "renderer": spec["renderer"], "format": row["format"],
                        })
                        target_reports.append(rep); modified_target_count += 1
                else:
                    if len(rows) != 1:
                        die(f"{aid}: non-grouped asset resolved to {len(rows)} Switch rows")
                    row = rows[0]
                    src = extract_target(archives[game], row, clmod, bg)
                    if spec.get("cl3_inner_name", "").casefold() != row["inner_name"].casefold():
                        die(f"{aid}: manifest/mapping inner-name drift")
                    if spec.get("switch_native_policy") == "canonical_whole_words_v2":
                        if (game, row["archive"], int(row["gdat_index"]),
                            row["inner_name"].casefold()) != (
                                "ms2", "GxArchivedFile074.dat", 133, "camp_top_2.tid"):
                            die(f"{aid}: unexpected canonical whole-word source target")
                        helper = import_file("camp_top2_wholewords_v2",
                            project / "toolchain/python-src/scripts/graphics/camp_top2_wholewords_v2.py")
                        rendered, changes, pixels, style = helper.render_accepted_switch_name_atlas(
                            src["original"], manifest, bg)
                        if len(changes) != 8 or style["canonical_cap_height"] != 25:
                            die("g133 whole-word V2 QA failure: expected exactly eight canonical edits at 25 px")
                        canonical_target_reports.append({
                            "game": game, "asset_id": aid, "archive": row["archive"],
                            "gdat_index": int(row["gdat_index"]), "inner_name": row["inner_name"],
                            "policy": "canonical_whole_words_v2", "changed_names": 8,
                            "untouched_names": 6, "canonical_cap_height": 25,
                            "original_source_tid_sha256": row["tid_sha256"],
                            "font_face": style["font_face"], "font_sha256": style["font_sha256"],
                            "replaced_labels": [[old, new] for old,new,*_ in changes],
                            "changed_rgba_pixels": pixels,
                        })
                    else:
                        rendered = render_one(bg, src["original"], spec)
                    new_entry, rep = rebuild_target_entry(src, rendered, bg, clmod, preview_dir, f"{game}__{aid}")
                    idx = int(row["gdat_index"])
                    if idx in replacements[game][row["archive"]]:
                        die(f"duplicate replacement for {game}/{row['archive']}/g{idx}")
                    replacements[game][row["archive"]][idx] = new_entry
                    rep.update({
                        "game": game, "asset_id": aid, "logical_id": row["logical_id"],
                        "archive": row["archive"], "gdat_index": idx, "inner_name": row["inner_name"],
                        "renderer": spec["renderer"], "format": row["format"],
                    })
                    target_reports.append(rep); modified_target_count += 1

        if source_target_count != 40:
            die(f"source target accounting drift: {source_target_count} != 40")
        # All 40 source textures remain mapped and are rebuilt. g133 receives
        # eight approved canonical names, not a pristine-texture exception.
        if (modified_target_count != 40 or len(preserved_target_reports) != 0
                or len(canonical_target_reports) != 1):
            die(f"target accounting drift: {modified_target_count} modified, "
                f"{len(preserved_target_reports)} preserved, "
                f"{len(canonical_target_reports)} canonical; expected 40/0/1")

        rebuilt_archives = {"ms2": {}, "finale": {}}
        for game in ("ms2", "finale"):
            for name, ar in archives[game].items():
                repl = replacements[game].get(name, {})
                if not repl:
                    die(f"{game}/{name}: required archive has no replacements")
                rebuilt = rebuild_gdat(ar["raw"], ar["records"], repl)
                orec = parse_gdat(rebuilt)
                verify_non_targets(ar["raw"], ar["records"], rebuilt, orec, set(repl))
                if game == "ms2" and name == "GxArchivedFile074.dat":
                    old_off, old_size = ar["records"][133]
                    new_off, new_size = orec[133]
                    if (133 not in repl or
                        ar["raw"][old_off:old_off+old_size] == rebuilt[new_off:new_off+new_size]):
                        die("runtime-approved MS2 074/g133 canonical V2 replacement missing")
                cursor = 8 + 8 * len(orec)
                for i, (off, size) in enumerate(orec):
                    if off != cursor:
                        die(f"{game}/{name}: non-canonical output offset at g{i}")
                    cursor += size
                if cursor != len(rebuilt):
                    die(f"{game}/{name}: rebuilt GDAT total-size mismatch")
                rebuilt_archives[game][name] = rebuilt

        with tempfile.TemporaryDirectory(prefix="ms-r32c3-pkg-") as td:
            root = Path(td)
            for game, g in GAMES.items():
                mod = root / "2_EMULATOR_EDEN" / "load" / g["title_id"] / g["mod_name"]
                (mod / "exefs").mkdir(parents=True, exist_ok=True)
                (mod / "romfs/masterNX64").mkdir(parents=True, exist_ok=True)
                (mod / "exefs" / f"{g['build_id']}.ips").write_bytes(g["ips"].read_bytes())
                for name, data in sorted(rebuilt_archives[game].items()):
                    (mod / "romfs/masterNX64" / name).write_bytes(data)

            pdir = root / "R32C3_PREVIEWS"
            pdir.mkdir()
            for p in sorted(preview_dir.iterdir()):
                if p.is_file():
                    (pdir / p.name).write_bytes(p.read_bytes())

            archive_report = []
            for game in ("ms2", "finale"):
                for name, data in sorted(rebuilt_archives[game].items()):
                    src = archives[game][name]["raw"]
                    archive_report.append({
                        "game": game,
                        "archive": name,
                        "source_sha256": sha_bytes(src),
                        "output_sha256": sha_bytes(data),
                        "source_size": len(src),
                        "output_size": len(data),
                        "modified_gdat_indices": sorted(replacements[game][name]),
                    })

            report = {
                "schema_version": 1,
                "phase": "switch_graphics_build_v1",
                "project_version": version,
                "footer_text": f"UNBURGER MOD V{version}",
                "source_mapping_sha256": sha_file(MAPPING),
                "static_contract": static,
                "source_target_count": source_target_count,
                "modified_target_count": modified_target_count,
                "preserved_target_count": len(preserved_target_reports),
                "preserved_targets": preserved_target_reports,
                "canonical_target_count": len(canonical_target_reports),
                "canonical_targets": canonical_target_reports,
                "not_applicable_target_count": 6,
                "not_applicable_asset": "MS2_DLC_JOB_LABELS",
                "native_texture_format": "DXT5",
                "non_target_gdat_payloads_byte_identical": True,
                "non_target_cl3_members_byte_identical": True,
                "full_gdat_reflow": True,
                "games": {
                    game: {
                        "title_id": GAMES[game]["title_id"],
                        "build_id": GAMES[game]["build_id"],
                        "accepted_ips_sha256": GAMES[game]["ips_sha256"],
                        "source_master": str(masters[game]),
                    } for game in ("ms2", "finale")
                },
                "archives": archive_report,
                "targets": sorted(target_reports, key=lambda x: (x["game"], x["archive"], x["gdat_index"])),
                "runtime_gate": [
                    "MS2: title/footer, battle/result names, camp/job UI, Heroine Room list/banners, included-MS1 residence labels, equipment headers",
                    "Finale: title/footer, battle names, Bonus character labels/grid, camp/Heroine Room/ren-bonus labels, equipment headers",
                    "accepted JP-voice/logo-skip behavior remains healthy in both latest-update builds",
                    "save/DLC/menu behavior does not regress",
                ],
            }
            (root / "R32C3_BUILD_REPORT.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
            (root / "README_TEST.txt").write_text(
                "R32C3 full Switch graphics-port candidate with typography/compositing repairs for Mary Skelter 2 + Finale.\n\n"
                "The six PC MS2_DLC_JOB_LABELS targets are intentionally NOT part of this standalone-Western-Switch build: R31C1 found none in the user's complete standalone MS2 Switch content set, and the Western standalone Switch DLC catalogue contains only Jailbreak Support Goods, Mary Skelter Game Item, Blood Maidens' Swimsuits, and Death end re;Quest Job Attire 1/2. The six store-exclusive MS2 Job Set I classes are a later Finale Grand Finale Bundle payload, not standalone Western MS2 Switch DLC.\n\n"
                "R32C3 maps and modifies 40 applicable Switch source textures. MS2 camp_top_2.tid at 074/g133 uses owner-runtime-approved eight whole-word canonical English replacements (six source names pixel-identical). The complete Heroine Room banner family is rerendered consistently. Every mapped Switch texture is native DXT5. All untouched GDAT entries and CL3 members are byte-identical.\n\n"
                "Use ONLY the 2_EMULATOR_EDEN directory for the runtime smoke test.\n",
                encoding="utf-8",
            )
            checks = []
            for p in sorted(x for x in root.rglob("*") if x.is_file()):
                checks.append(f"{sha_file(p)}  {p.relative_to(root).as_posix()}")
            (root / "CHECKSUMS.sha256").write_text("\n".join(checks) + "\n", encoding="utf-8")
            deterministic_zip(root, outzip)

    print("R32C3 READY")
    print("SOURCE_TARGETS", source_target_count)
    print("MODIFIED_TARGETS", modified_target_count)
    print("CANONICAL_WHOLE_WORD_V2_TARGETS", len(canonical_target_reports))
    print("OUTPUT", outzip)
    print("SHA256", sha_file(outzip))


def self_test(project: Path):
    mapping = json.loads(MAPPING.read_text(encoding="utf-8"))
    _, _, manifest = load_project(project)
    static = validate_static_contract(mapping, manifest)
    if sha_file(MS2_IPS) != GAMES["ms2"]["ips_sha256"]:
        die("MS2 accepted IPS self-test hash mismatch")
    if sha_file(MSF_IPS) != GAMES["finale"]["ips_sha256"]:
        die("Finale accepted IPS self-test hash mismatch")
    print("R32C3 SELF_TEST PASS")
    print(json.dumps(static, sort_keys=True))


def main():
    ap = argparse.ArgumentParser(description="Build the full applicable Western/latest Switch graphics port for Mary Skelter 2 + Finale.")
    ap.add_argument("--project-root", type=Path, required=True)
    ap.add_argument("--ms2-master", type=Path)
    ap.add_argument("--finale-master", type=Path)
    ap.add_argument("--out", type=Path)
    ap.add_argument("--self-test", action="store_true")
    args = ap.parse_args()
    project = args.project_root.resolve()
    if args.self_test:
        self_test(project)
        return 0
    if not args.ms2_master or not args.finale_master or not args.out:
        die("--ms2-master, --finale-master and --out are required unless --self-test is used")
    build(project, args.ms2_master, args.finale_master, args.out.resolve())
    return 0


if __name__ == "__main__":
    sys.exit(main())
