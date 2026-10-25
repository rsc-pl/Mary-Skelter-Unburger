#!/usr/bin/env python3
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

# Maintainer-only preflight: validate every graphics recipe even when private,
# legally non-distributable font files are unavailable in a clean FULL.
# Normal execution (no flags) MUST still enforce font file presence.
SKIP_FONT_PRESENCE = (sys.argv[1:] == ["--skip-font-presence"])
if len(sys.argv) > 1 and not SKIP_FONT_PRESENCE:
    raise SystemExit("usage: verify_graphics_config.py [--skip-font-presence]")
ROOT = Path(__file__).resolve().parents[4]
MANIFEST = ROOT / "graphics" / "manifest.json"
CANON = ROOT / "graphics" / "canon_replacements.json"
TYPO = ROOT / "graphics" / "ui_typography.json"

def require(cond: bool, msg: str) -> None:
    if not cond:
        raise SystemExit(f"graphics config FAIL: {msg}")

manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
canon = json.loads(CANON.read_text(encoding="utf-8"))
typ = json.loads(TYPO.read_text(encoding="utf-8"))

require(manifest.get("schema_version") == 2, "manifest schema_version")
require(set(manifest) == {"schema_version", "assets"}, "unexpected manifest top-level metadata")

assets = manifest["assets"]
require(isinstance(assets, list) and assets, "asset list is empty")
ids = [a["id"] for a in assets]
require(len(ids) == len(set(ids)), "duplicate asset id")
require(all(a.get("enabled") is True for a in assets), "public manifest must contain active assets only")
require(not any(re.search(r"_G\d", aid) for aid in ids), "workflow suffix in public asset id")
require(not any(x in aid.upper() for aid in ids for x in ("AUDIT", "TRANSFER")), "internal workflow id in public manifest")

forbidden_asset_keys = {
    "state", "capture", "notes", "runtime_evidence", "runtime_capture_evidence",
    "pristine_source_evidence", "activation_policy", "candidate_visual_status",
    "pending_source_policy",
}
for a in assets:
    bad = forbidden_asset_keys.intersection(a)
    require(not bad, f"{a['id']}: internal metadata present: {sorted(bad)}")
    require("dataset" in a and "renderer" in a, f"{a['id']}: missing build identity")
    require(a["dataset"] in {"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED"}, f"{a['id']}: unsupported dataset")
    require(isinstance(a.get("archive"), int), f"{a['id']}: missing archive")
    variants = a.get("targets") if a.get("grouped") else a.get("source_variants")
    require(variants, f"{a['id']}: source metadata missing")
    for v in variants:
        require(len(v["tid_sha256"]) == 64, f"{a['id']}: invalid TID hash")
        require(v["width"] > 0 and v["height"] > 0, f"{a['id']}: invalid dimensions")
        require(v["format"] in {"BC7", "DXT5"}, f"{a['id']}: unsupported texture format")

expected_ids = {
    "MS2_RESULT_NAMES",
    "MS2_BATTLE_NAMES",
    "MS2_HEROINE_ROOM_LIST_BLUE",
    "MS2_JOB_CHARACTER_NAMES",
    "MS2_MYROOM_TITLE",
    "MS2_AFFECTION_CHARACTER_NAMES",
    "MS2_BONUS_CHARACTER_NAMES",
    "MS2_CAMP_CHARACTER_NAMES",
    "MS2_JOB_CLASS_24PX",
    "MS2_JOB_CLASS_MIDDLE",
    "MS2_DLC_JOB_LABELS",
    "MS2_KESSIKI_SKILL_LABELS",
    "MS2_BONUS_CHARACTER_SELECT_NAMES",
    "MS2_HEROINE_ROOM_BANNERS",
    "MS2_LIVING_QUARTERS_SELECTOR",
    "MS2_CAMP_TOP_LEGACY_NAMES",
    "MS2_CAMP_TOP2_CHARACTER_NAMES",
    "MS2_PARTY_FORMATION_NAMES",
    "MS2_EQUIPMENT_HEADER_NAMES",
    "MSF_EQUIPMENT_HEADER_NAMES",
    "MS1_INCLUDED_RESIDENCE_NAMES",
    "MSF_EN_BATTLE_CHARACTER_NAMES",
    "MSF_EN_BONUS_CHARACTER_SELECT_NAMES",
    "MSF_EN_BONUS_CHARACTER_NAMES",
    "MSF_EN_FINALE_CHARACTER_GRID",
    "MSF_EN_CAMP_CHARACTER_NAMES",
    "MSF_EN_HEROINE_ROOM_NAMES",
    "MSF_EN_REN_BONUS_CHARACTER_NAMES",
    "MS2_TITLE_MOD_FOOTER_MAIN",
    "MS2_TITLE_MOD_FOOTER_BE",
    "MSF_TITLE_MOD_FOOTER",
}
require(set(ids) == expected_ids, f"active asset set drift: {sorted(set(ids) ^ expected_ids)}")
by = {a["id"]: a for a in assets}

# Core canonical replacements.
ms2_chars = canon["character_labels"]["MS2"]
require(ms2_chars == {
    "OTSUU": "TSUU",
    "LITTLE MERMAID": "NINGYOHIME",
    "RED RIDING HOOD": "AKAZUKIN",
    "THUMBELINA": "OYAYUBIHIME",
    "SNOW WHITE": "SHIRAYUKIHIME",
    "SLEEPING BEAUTY": "NEMURIHIME",
    "KAGUYA": "KAGUYAHIME",
}, "MS2 character canon drift")
require(canon["character_labels"]["MSF"]["PYRE"] == "SHIRA", "Finale Shira canon drift")

# Critical font-rendered sources.
r = by["MS2_RESULT_NAMES"]
require((r["archive"], r["gdat_index"], r["cl3_inner_name"]) == (55, 1333, "result_2.tid"), "result source drift")
require(r["renderer"] == "ms2_result_font_v2" and r["typography_preset"] == "result_nameplate", "result renderer drift")
require(len(r["rows"]) == 13, "result family must rebuild all 13 names")
require(r["source_variants"][0]["tid_sha256"] == "3af42766364633da20762a59c3c3054f52179548ae0604a8265d773a0a980a14", "result source hash drift")

q = by["MS2_LIVING_QUARTERS_SELECTOR"]
require((q["archive"], q["gdat_index"], q["cl3_inner_name"]) == (55, 1858, "menu_kyoju.tid"), "Living Quarters source drift")
require(q["renderer"] == "ms2_room_selector_font_v4", "Living Quarters renderer drift")
require(q["selected_background_asset"] == "room_selector/selected_bg.png", "Living Quarters asset path drift")
require(len(q["rows"]) == 13 and len(q["row_rects"]) == 13, "Living Quarters row count drift")
require(q["source_variants"][0]["tid_sha256"] == "7c06055bc8864a2e0dbc10e64af8e63ff8f224d66e0e8ac56479dfa1ac0a9979", "Living Quarters source hash drift")

compact = by["MS2_CAMP_TOP2_CHARACTER_NAMES"]
require((compact["archive"], compact["gdat_index"], compact["cl3_inner_name"]) == (55, 1463, "camp_top_2.tid"), "compact name source drift")
require(compact["renderer"] == "ms2_camp_top2_character_names_font_v3", "compact renderer drift")
require(compact["typography_preset"] == "camp_top2_vertical_neofara" and len(compact["rows"]) == 14, "compact family drift")
require(compact["source_variants"][0]["tid_sha256"] == "6cac6e1c439db75ac43084e3452fb959c11984030b178f7b6a23c2ba445d8f39", "compact source hash drift")

upper = by["MS2_CAMP_TOP_LEGACY_NAMES"]
require((upper["archive"], upper["gdat_index"], upper["cl3_inner_name"]) == (55, 1448, "camp_top.tid"), "upper header source drift")
require(upper["renderer"] == "ms2_camp_top_legacy_names_font_v2" and upper["typography_preset"] == "camp_top_legacy_neofara", "upper header renderer drift")
require(upper["source_variants"][0]["tid_sha256"] == "196169c577b99f73cf621e813566e793ee11576f0ea8a2b42b780439b9a1928a", "upper header source hash drift")

formation = by["MS2_PARTY_FORMATION_NAMES"]
require((formation["archive"], formation["gdat_index"], formation["cl3_inner_name"]) == (55, 1438, "camp_hensei.tid"), "party formation source drift")
require(formation["renderer"] == "ms2_name_family_font_v1" and formation["typography_preset"] == "party_formation_pink", "party formation renderer drift")
require(len(formation["rows"]) == 12, "party formation row count drift")
require(formation["source_variants"][0]["tid_sha256"] == "5eac8fae322a0d260941fb89cf735b4bfa44184c0822eb2d37430c21a2c8599e", "party formation source hash drift")

equip = by["MS2_EQUIPMENT_HEADER_NAMES"]
require((equip["archive"], equip["gdat_index"], equip["cl3_inner_name"]) == (55, 1468, "commonBE.tid"), "equipment header source drift")
require(equip["renderer"] == "ms2_name_family_font_v1" and equip["typography_preset"] == "equipment_header_chiaro", "equipment header renderer drift")
require(len(equip["rows"]) == 13, "equipment header row count drift")
require(equip["source_variants"][0]["tid_sha256"] == "d540e4d718490eb990313830782662aed075650eb56c19266d4c8f3965dcd7f3", "equipment header source hash drift")
require(next(r for r in equip["rows"] if r["text"] == "KAGUYAHIME")["rect"] == [3608,362,3833,404], "KAGUYAHIME equipment sizing box drift")

msf_equip = by["MSF_EQUIPMENT_HEADER_NAMES"]
require((msf_equip["archive"], msf_equip["gdat_index"], msf_equip["cl3_inner_name"]) == (74, 8, "commonBE.tid"), "Finale equipment header source drift")
require(msf_equip["renderer"] == "ms2_name_family_font_v1" and msf_equip["typography_preset"] == "equipment_header_chiaro", "Finale equipment header renderer drift")
require(len(msf_equip["rows"]) == 13, "Finale equipment header row count drift")
require(msf_equip["source_variants"][0] == {
    "tid_sha256": "e8ac0ad32138bb600d29bb2f895103c5c17d9fe7f0688af0c879336a7b3fa070",
    "width": 4096, "height": 4096, "format": "DXT5",
}, "Finale equipment header source variant drift")
require(msf_equip["rows"] == equip["rows"], "Finale/MS2 commonBE row-layout parity drift")
require(msf_equip["replacements"] == equip["replacements"], "Finale/MS2 commonBE replacement parity drift")

battle = by["MS2_BATTLE_NAMES"]
require(battle["renderer"] == "ms2_battle_nameplates_font_v3", "MS2 battle renderer drift")
require(battle.get("extra_cleanup_names")==["NINGYOHIME","AKAZUKIN"], "MS2 battle local cleanup target drift")
require(int(battle.get("extra_cleanup_filter_size",0))==21, "MS2 battle local cleanup radius drift")
require(battle["typography_preset"] == "battle_nameplate", "MS2 battle preset drift")
require(battle.get("background_asset") == "battle/ms2_nameplate_bg.png", "MS2 battle legacy background asset path drift")
require(abs(float(battle.get("final_scale", 0)) - 0.94) < 1e-9, "MS2 battle final scale drift")
require(int(battle.get("supersample", 0)) >= 3, "MS2 battle supersampling drift")
require(len(battle["rows"]) == 15, "MS2 battle full-family row count drift")
require(next(r for r in battle["rows"] if r["text"]=="JACK")["rect"] == [780,1784,1123,1965], "MS2 battle JACK reference rect drift")

aff = by["MS2_AFFECTION_CHARACTER_NAMES"]
require(aff["renderer"] == "ms2_affection_font_v2" and aff["typography_preset"] == "affection_cyan", "affection renderer drift")
require(len(aff["rows"]) == 11, "affection row count drift")

# Typography config is deliberately build-only: no provenance/history prose.
require(set(typ) == {"schema_version", "fonts", "presets"}, "unexpected typography metadata")
require(typ["schema_version"] == 1, "typography schema_version")
require(set(typ["fonts"]) == {"avionic", "avionic_regular", "chiaro", "gebryna", "neofara"}, "font set drift")
for name, cfg in typ["fonts"].items():
    require("note" not in cfg, f"{name}: private provenance leaked into public config")
    require(len(cfg["reference_sha256"]) == 64, f"{name}: invalid font hash")
    require(cfg.get("repo_path"), f"{name}: public font path missing")
    if not SKIP_FONT_PRESENCE:
        require((ROOT / cfg["repo_path"]).is_file(), f"{name}: public font file missing")

expected_presets = {
    "living_quarters_normal", "living_quarters_selected",
    "camp_top2_vertical", "affection_cyan", "affection_pink",
    "camp_top_legacy_name", "result_nameplate",
    "equipment_header_chiaro", "party_formation_pink",
    "heroine_room_banner_gebryna", "ms2_room_entry_gebryna",
    "battle_nameplate", "first_game_residence_normal", "first_game_residence_selected",
    "title_footer_chiaro",
    "pink_block_large_neofara", "pink_block_small_neofara",
    "white_name_neofara", "white_name_small_neofara",
    "camp_gold_neofara", "finale_ren_gray_neofara",
    "finale_camp_vertical_neofara", "finale_room_pink_neofara",
    "finale_battle_pink_neofara", "heroine_room_banner_neofara",
    "camp_top2_vertical_neofara", "camp_top_legacy_neofara",
    # R32C3 V4 full-family typography candidates (V4 visual gate remains open).
    "r32c3_camp_gold", "r32c3_finale_battle", "r32c3_finale_battle_v2",
    "r32c3_finale_bonus_name", "r32c3_finale_bonus_select",
    "r32c3_finale_camp", "r32c3_finale_camp_v2", "r32c3_ren_gray",
    "r32c3_room_pink", "r32c3_room_pink_fsf",
    "r32c3_white_oblique", "r32c3_white_oblique_small",
    "r32c3_select_subtitle",  # V18 source-first God Jail Tower preservation.
}
require(set(typ["presets"]) == expected_presets, "typography preset set drift")
require(all("note" not in p for p in typ["presets"].values()), "preset history leaked into public config")
require(typ["presets"]["living_quarters_normal"]["font_face"] == "avionic_regular", "Living Quarters font drift")
require(typ["presets"]["camp_top2_vertical"]["font_face"] == "avionic", "compact font drift")
require(typ["presets"]["camp_top_legacy_name"]["font_face"] == "chiaro", "upper header font drift")
require(typ["presets"]["result_nameplate"]["font_face"] == "avionic", "result font drift")
require(float(typ["presets"]["result_nameplate"].get("shadow_blur_radius",0)) > 0, "result shadow blur missing")
require(typ["presets"]["equipment_header_chiaro"]["font_face"] == "chiaro", "equipment header font drift")
require(typ["presets"]["party_formation_pink"]["font_face"] == "avionic", "party formation font drift")

label = ROOT / "graphics" / "assets" / "room_selector" / "selected_bg.png"
require(label.is_file(), "Living Quarters selected-row asset missing")


main_room = by["MS2_HEROINE_ROOM_LIST_BLUE"]
require((main_room["archive"],main_room["gdat_index"],main_room["cl3_inner_name"]) == (55,1853,"menu_heroineroom.tid"), "main MS2 room-title source drift")
require(main_room["renderer"] == "ms2_heroine_room_blue_font_v6", "main MS2 room-title renderer drift")
require(main_room["typography_preset"] == "ms2_room_entry_gebryna", "main MS2 room-title preset drift")
require(len(main_room["rows"]) == 11, "main MS2 room-title family row count drift")
require([r["name"] for r in main_room["rows"]] == [
    "NINGYOHIME","HAMELN","GRETEL","RAPUNZEL","KAGUYAHIME","OYAYUBIHIME",
    "SHIRAYUKIHIME","NEMURIHIME","CINDERELLA","AKAZUKIN","ALICE"
], "main MS2 room-title order drift")
require([r.get("name_target_width") for r in main_room["rows"]] == [
    581,395,382,489,399,574,501,591,555,565,308
], "main MS2 room-title shipped-width targets drift")
require(abs(float(main_room.get("room_vertical_scale",1.0))-0.82) < 1e-9, "main MS2 room-title ROOM height drift")
require(main_room.get("room_vertical_anchor")=="bottom", "main MS2 room-title ROOM anchor drift")
require(typ["presets"]["ms2_room_entry_gebryna"]["font_face"] == "gebryna", "main MS2 room-title font drift")
require(typ["presets"]["ms2_room_entry_gebryna"]["face_dark_rgba"][:3] == [0,4,16], "main MS2 room-title dark face drift")
require(typ["presets"]["ms2_room_entry_gebryna"]["inner_stroke_rgba"][:3] == [189,247,255], "main MS2 room-title light edge drift")

myroom = by["MS2_MYROOM_TITLE"]
require((myroom["archive"],myroom["gdat_index"],myroom["cl3_inner_name"]) == (55,1860,"menu_kyoju_myroom.tid"), "MS2 own-room source drift")
require(myroom["renderer"] == "ms2_myroom_title_font_v3" and myroom["typography_preset"] == "ms2_room_entry_gebryna", "MS2 own-room renderer drift")
require(myroom["name"] == "TSUU", "MS2 own-room canonical name drift")
require(myroom.get("name_target_width") == 342 and myroom.get("name_offset_y") == 22, "MS2 own-room scale/offset drift")
require(abs(float(myroom.get("room_vertical_scale",1.0))-0.92) < 1e-9, "MS2 own-room ROOM height drift")
require(myroom.get("room_vertical_anchor","top")=="top", "MS2 own-room ROOM anchor drift")

room = by["MS2_HEROINE_ROOM_BANNERS"]
require(room["renderer"] == "ms2_heroine_room_banners_font_v2", "included-first-game room banner renderer drift")
require(room["typography_preset"] == "heroine_room_banner_neofara", "included-first-game room banner preset drift")
require(len(room["replacements"]) == 11, "included-first-game room banner source-family row count drift")
require(room.get("changed_gdat_indices") == [int(t["gdat_index"]) for t in room["targets"]], "R32C3 entire 11-banner family changed-set drift")
require(typ["presets"]["heroine_room_banner_neofara"]["font_face"] == "neofara", "included-first-game room banner font drift")
require(typ["presets"]["heroine_room_banner_neofara"]["shadow_rgba"][:3] == [5,0,5] and int(typ["presets"]["heroine_room_banner_neofara"]["shadow_stroke_width"]) >= 7, "included-first-game magenta treatment drift")

res = by["MS1_INCLUDED_RESIDENCE_NAMES"]
require((res["archive"],res["gdat_index"],res["cl3_inner_name"]) == (55,1883,"menu_kyojuu.tid"), "included-first-game Residence source drift")
require(res["renderer"] == "ms1_residence_names_raster_v2", "included-first-game Residence renderer drift")
require(len(res["rows"]) == len(res["source_rows"]) == len(res["normal_rects"]) == len(res["selected_rects"]) == 14, "included-first-game Residence row count drift")
require(res.get("expected_changed_rows") == 6, "included-first-game Residence changed-row count drift")
require(res["rows"][11]["text"] == "TSUU AND NINGYOHIME'S ROOM", "included-first-game Residence TSUU/NINGYOHIME label drift")
require(res["source_variants"][0]["tid_sha256"] == "e5a0c241a1d515745377473f1e3bea3a583d5756673f829d215eaa1c9f394eab", "included-first-game Residence source hash drift")

battle = by["MS2_BATTLE_NAMES"]
require(float(typ["presets"]["battle_nameplate"].get("rotation_degrees",0)) > 0, "battle shipped tilt missing")
require(typ["presets"]["battle_nameplate"]["font_face"] == "neofara", "battle font drift")

# Already-established class/skill baked labels must stay active; this pass must not lose them.
require(by["MS2_JOB_CLASS_24PX"]["renderer"] == "font_atlas_v2", "24px class-label renderer drift")
require(by["MS2_JOB_CLASS_MIDDLE"]["renderer"] == "font_atlas_v2", "middle class-label renderer drift")
require(by["MS2_KESSIKI_SKILL_LABELS"]["renderer"] == "font_atlas_v2", "Kesshiki skill-label renderer drift")


# Title footer targets are source-pinned to literal copyright sprites from the
# shipped title atlases.  title_moji remains rejected as a logo-local texture.
expected_title_footer = {
    "MS2_TITLE_MOD_FOOTER_MAIN": ("MS2_PC_LOCALIZED", 55, 1821, "titlemenu.tid", [50,2544,900,2634], "101c81d0e9953dfea166ed01852e3f4f50c20f575f59d1f990be41f40c9e2311"),
    "MS2_TITLE_MOD_FOOTER_BE": ("MS2_PC_LOCALIZED", 55, 1822, "TitleBE.tid", [1312,1660,2040,1732], "44cd6d66ec412412b650b431120c4c04390cb8574d357905bd39b5ac54d66462"),
    "MSF_TITLE_MOD_FOOTER": ("MSF_PC_LOCALIZED", 73, 220, "title_f.tid", [374,1796,1230,1886], "4cfbfb005ba44299615259ece0485ffa56c4a427c06b82917ed21e12b441d7fd"),
}
require({aid for aid in ids if "TITLE_MOD_FOOTER" in aid} == set(expected_title_footer), "title footer target set drift")
for aid, (dataset, archive, gidx, inner, rect, source_sha) in expected_title_footer.items():
    a = by[aid]
    require(a.get("enabled") is True, f"{aid}: disabled")
    require((a["dataset"], a["archive"], a["gdat_index"], a["cl3_inner_name"]) == (dataset, archive, gidx, inner), f"{aid}: source locator drift")
    require(a["renderer"] == "title_copyright_footer_chiaro_v2", f"{aid}: renderer drift")
    require(a["typography_preset"] == "title_footer_chiaro", f"{aid}: typography drift")
    require(a["copyright_rect"] == rect, f"{aid}: copyright rect drift")
    require(a["source_variants"][0]["tid_sha256"] == source_sha, f"{aid}: source hash drift")
require(not any(a.get("cl3_inner_name") == "title_moji.tid" and "TITLE_MOD_FOOTER" in a.get("id","") for a in assets), "rejected title_moji footer target returned")

print("graphics config: PASS" + (" (font file presence deliberately not tested)" if SKIP_FONT_PRESENCE else ""))
