#!/usr/bin/env python3
from __future__ import annotations
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
manifest = json.loads((ROOT / "graphics" / "manifest.json").read_text(encoding="utf-8"))
by = {a["id"]: a for a in manifest.get("assets", [])}
expected = {
    "MS2_TITLE_MOD_FOOTER_MAIN": ("MS2_PC_LOCALIZED", 55, 1821, "titlemenu.tid", [50,2544,900,2634], "101c81d0e9953dfea166ed01852e3f4f50c20f575f59d1f990be41f40c9e2311"),
    "MS2_TITLE_MOD_FOOTER_BE": ("MS2_PC_LOCALIZED", 55, 1822, "TitleBE.tid", [1312,1660,2040,1732], "44cd6d66ec412412b650b431120c4c04390cb8574d357905bd39b5ac54d66462"),
    "MSF_TITLE_MOD_FOOTER": ("MSF_PC_LOCALIZED", 73, 220, "title_f.tid", [374,1796,1230,1886], "4cfbfb005ba44299615259ece0485ffa56c4a427c06b82917ed21e12b441d7fd"),
}
version = (ROOT / "version.txt").read_text(encoding="utf-8").strip()
if not version or any(ch in version for ch in "\r\n"):
    raise SystemExit("TITLE FOOTER PREFLIGHT: FAIL — malformed version.txt")
for aid, (dataset, archive, gidx, inner, rect, sha) in expected.items():
    a = by.get(aid)
    if not a or a.get("enabled") is not True:
        raise SystemExit(f"TITLE FOOTER PREFLIGHT: FAIL — missing/disabled {aid}")
    if (a.get("dataset"), a.get("archive"), a.get("gdat_index"), a.get("cl3_inner_name")) != (dataset, archive, gidx, inner):
        raise SystemExit(f"TITLE FOOTER PREFLIGHT: FAIL — source locator drift for {aid}")
    if a.get("renderer") != "title_copyright_footer_chiaro_v2" or a.get("copyright_rect") != rect:
        raise SystemExit(f"TITLE FOOTER PREFLIGHT: FAIL — renderer/rect drift for {aid}")
    variants = a.get("source_variants") or []
    if len(variants) != 1 or variants[0].get("tid_sha256") != sha:
        raise SystemExit(f"TITLE FOOTER PREFLIGHT: FAIL — source pin drift for {aid}")
if any(a.get("id", "").startswith(("MS2_TITLE_MOD_FOOTER", "MSF_TITLE_MOD_FOOTER")) and a.get("cl3_inner_name") == "title_moji.tid" for a in manifest.get("assets", [])):
    raise SystemExit("TITLE FOOTER PREFLIGHT: FAIL — rejected title_moji target is active")
print(f"TITLE FOOTER PREFLIGHT: PASS — source-pinned copyright sprites; label=UNBURGER MOD V{version}")
