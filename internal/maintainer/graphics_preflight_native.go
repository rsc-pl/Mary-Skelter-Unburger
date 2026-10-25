package maintainer

// Native Go source-first graphics recipe validation. This is the production
// counterpart to the retained independent Python oracle in toolchain/python-src.
// It validates configuration and provenance, NEVER renders or alters an atlas.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

type graphicsConfigCheck struct{ err error }

func (c *graphicsConfigCheck) need(ok bool, reason string) {
	if !ok && c.err == nil {
		c.err = fmt.Errorf("native graphics config FAIL: %s", reason)
	}
}
func gcObj(x any) map[string]any {
	if m, ok := x.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
func gcArr(x any) []any {
	if a, ok := x.([]any); ok {
		return a
	}
	return nil
}
func gcGet(x any, key string) any { return gcObj(x)[key] }
func gcAt(x any, i int) any {
	a := gcArr(x)
	if i >= 0 && i < len(a) {
		return a[i]
	}
	return nil
}
func gcStr(x any) string { s, _ := x.(string); return s }
func gcInt(x any) int {
	v, ok := x.(float64)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v {
		return -1
	}
	return int(v)
}
func gcFloat(x any) float64 {
	v, ok := x.(float64)
	if ok {
		return v
	}
	return 0
}
func gcKeys(x any) []string {
	var out []string
	for k := range gcObj(x) {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func gcStrings(a ...string) []any {
	out := make([]any, len(a))
	for i, v := range a {
		out[i] = v
	}
	return out
}
func gcJSONEqual(a, b any) bool { // Normalize Go literals to JSON-unmarshaled number types.
	data, err := json.Marshal(b)
	if err != nil {
		return false
	}
	var norm any
	if json.Unmarshal(data, &norm) != nil {
		return false
	}
	return reflect.DeepEqual(a, norm)
}
func gcOneOf(s string, items ...string) bool {
	for _, v := range items {
		if s == v {
			return true
		}
	}
	return false
}
func gcRead(root, name string) (any, error) {
	raw, err := os.ReadFile(filepath.Join(root, "graphics", name))
	if err != nil {
		return nil, fmt.Errorf("read graphics/%s: %w", name, err)
	}
	var obj any
	if err = json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("parse graphics/%s: %w", name, err)
	}
	return obj, nil
}
func gcRowText(a any, text string) any {
	for _, r := range gcArr(gcGet(a, "rows")) {
		if gcStr(gcGet(r, "text")) == text {
			return r
		}
	}
	return nil
}
func gcAssetLocator(a any, dataset string, archive, index int, inner, renderer, preset, sha string) bool {
	if gcStr(gcGet(a, "dataset")) != dataset || gcInt(gcGet(a, "archive")) != archive || gcInt(gcGet(a, "gdat_index")) != index || gcStr(gcGet(a, "cl3_inner_name")) != inner || gcStr(gcGet(a, "renderer")) != renderer || gcStr(gcGet(a, "typography_preset")) != preset {
		return false
	}
	return gcStr(gcGet(gcAt(gcGet(a, "source_variants"), 0), "tid_sha256")) == sha
}

// VerifyGraphicsConfigNative replaces the production Python graphics recipe
// preflight. The independent Python checker remains available solely as an
// oracle for parity tests while the other image/texture backends are ported.
// skipFontPresence is allowed for distribution/CI verification only.
func VerifyGraphicsConfigNative(root string, skipFontPresence bool) error {
	manifest, err := gcRead(root, "manifest.json")
	if err != nil {
		return err
	}
	canon, err := gcRead(root, "canon_replacements.json")
	if err != nil {
		return err
	}
	typ, err := gcRead(root, "ui_typography.json")
	if err != nil {
		return err
	}
	c := &graphicsConfigCheck{}
	c.need(gcInt(gcGet(manifest, "schema_version")) == 2 && reflect.DeepEqual(gcKeys(manifest), []string{"assets", "schema_version"}), "manifest schema and top-level keys")
	assets := gcArr(gcGet(manifest, "assets"))
	c.need(len(assets) > 0, "no graphics assets")
	expected := strings.Fields(`MS2_RESULT_NAMES MS2_BATTLE_NAMES MS2_HEROINE_ROOM_LIST_BLUE MS2_JOB_CHARACTER_NAMES MS2_MYROOM_TITLE MS2_AFFECTION_CHARACTER_NAMES MS2_BONUS_CHARACTER_NAMES MS2_CAMP_CHARACTER_NAMES MS2_JOB_CLASS_24PX MS2_JOB_CLASS_MIDDLE MS2_DLC_JOB_LABELS MS2_KESSIKI_SKILL_LABELS MS2_BONUS_CHARACTER_SELECT_NAMES MS2_HEROINE_ROOM_BANNERS MS2_LIVING_QUARTERS_SELECTOR MS2_CAMP_TOP_LEGACY_NAMES MS2_CAMP_TOP2_CHARACTER_NAMES MS2_PARTY_FORMATION_NAMES MS2_EQUIPMENT_HEADER_NAMES MSF_EQUIPMENT_HEADER_NAMES MS1_INCLUDED_RESIDENCE_NAMES MSF_EN_BATTLE_CHARACTER_NAMES MSF_EN_BONUS_CHARACTER_SELECT_NAMES MSF_EN_BONUS_CHARACTER_NAMES MSF_EN_FINALE_CHARACTER_GRID MSF_EN_CAMP_CHARACTER_NAMES MSF_EN_HEROINE_ROOM_NAMES MSF_EN_REN_BONUS_CHARACTER_NAMES MS2_TITLE_MOD_FOOTER_MAIN MS2_TITLE_MOD_FOOTER_BE MSF_TITLE_MOD_FOOTER`)
	by := map[string]any{}
	seen := map[string]bool{}
	shaPattern := regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	suffix := regexp.MustCompile(`_G\d`)
	forbidden := strings.Fields("state capture notes runtime_evidence runtime_capture_evidence pristine_source_evidence activation_policy candidate_visual_status pending_source_policy")
	for _, a := range assets {
		id := gcStr(gcGet(a, "id"))
		c.need(id != "" && !seen[id], "missing or duplicate asset ID: "+id)
		seen[id] = true
		by[id] = a
		c.need(!suffix.MatchString(id) && !strings.Contains(strings.ToUpper(id), "AUDIT") && !strings.Contains(strings.ToUpper(id), "TRANSFER"), id+": forbidden workflow identifier")
		c.need(gcGet(a, "enabled") == true, id+": disabled asset")
		for _, k := range forbidden {
			_, has := gcObj(a)[k]
			c.need(!has, id+": internal metadata "+k)
		}
		c.need(gcOneOf(gcStr(gcGet(a, "dataset")), "MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED"), id+": dataset")
		c.need(gcInt(gcGet(a, "archive")) >= 0, id+": archive index")
		c.need(gcStr(gcGet(a, "renderer")) != "", id+": renderer")
		variants := gcGet(a, "source_variants")
		if gcGet(a, "grouped") == true {
			variants = gcGet(a, "targets")
		}
		c.need(len(gcArr(variants)) > 0, id+": missing source identity")
		for _, v := range gcArr(variants) {
			c.need(shaPattern.MatchString(gcStr(gcGet(v, "tid_sha256"))), id+": bad source SHA")
			c.need(gcInt(gcGet(v, "width")) > 0 && gcInt(gcGet(v, "height")) > 0, id+": source dimensions")
			c.need(gcOneOf(gcStr(gcGet(v, "format")), "BC7", "DXT5"), id+": texture format")
		}
	}
	c.need(len(by) == len(expected), "asset count drift")
	for _, id := range expected {
		_, ok := by[id]
		c.need(ok, "missing mandatory asset "+id)
	}
	return verifyGraphicsCanonicalInvariants(root, c, by, canon, typ, skipFontPresence)
}

func verifyGraphicsCanonicalInvariants(root string, c *graphicsConfigCheck, by map[string]any, canon, typ any, skipFontPresence bool) error {
	replacements := map[string]string{"OTSUU": "TSUU", "LITTLE MERMAID": "NINGYOHIME", "RED RIDING HOOD": "AKAZUKIN", "THUMBELINA": "OYAYUBIHIME", "SNOW WHITE": "SHIRAYUKIHIME", "SLEEPING BEAUTY": "NEMURIHIME", "KAGUYA": "KAGUYAHIME"}
	c.need(gcJSONEqual(gcGet(gcGet(canon, "character_labels"), "MS2"), replacements), "MS2 character canon drift")
	c.need(gcStr(gcGet(gcGet(gcGet(canon, "character_labels"), "MSF"), "PYRE")) == "SHIRA", "Finale Shira canon drift")
	a := by["MS2_RESULT_NAMES"]
	c.need(gcAssetLocator(a, "MS2_PC_LOCALIZED", 55, 1333, "result_2.tid", "ms2_result_font_v2", "result_nameplate", "3af42766364633da20762a59c3c3054f52179548ae0604a8265d773a0a980a14") && len(gcArr(gcGet(a, "rows"))) == 13, "MS2 result source/renderer/family drift")
	a = by["MS2_LIVING_QUARTERS_SELECTOR"]
	c.need(gcAssetLocator(a, "MS2_PC_LOCALIZED", 55, 1858, "menu_kyoju.tid", "ms2_room_selector_font_v4", gcStr(gcGet(a, "typography_preset")), "7c06055bc8864a2e0dbc10e64af8e63ff8f224d66e0e8ac56479dfa1ac0a9979") && gcStr(gcGet(a, "selected_background_asset")) == "room_selector/selected_bg.png" && len(gcArr(gcGet(a, "rows"))) == 13 && len(gcArr(gcGet(a, "row_rects"))) == 13, "living quarters source/layout drift")
	a = by["MS2_CAMP_TOP2_CHARACTER_NAMES"]
	c.need(gcAssetLocator(a, "MS2_PC_LOCALIZED", 55, 1463, "camp_top_2.tid", "ms2_camp_top2_character_names_font_v3", "camp_top2_vertical_neofara", "6cac6e1c439db75ac43084e3452fb959c11984030b178f7b6a23c2ba445d8f39") && len(gcArr(gcGet(a, "rows"))) == 14, "camp top2 source/preset/rows drift")
	a = by["MS2_CAMP_TOP_LEGACY_NAMES"]
	c.need(gcAssetLocator(a, "MS2_PC_LOCALIZED", 55, 1448, "camp_top.tid", "ms2_camp_top_legacy_names_font_v2", "camp_top_legacy_neofara", "196169c577b99f73cf621e813566e793ee11576f0ea8a2b42b780439b9a1928a"), "camp top legacy source drift")
	a = by["MS2_PARTY_FORMATION_NAMES"]
	c.need(gcAssetLocator(a, "MS2_PC_LOCALIZED", 55, 1438, "camp_hensei.tid", "ms2_name_family_font_v1", "party_formation_pink", "5eac8fae322a0d260941fb89cf735b4bfa44184c0822eb2d37430c21a2c8599e") && len(gcArr(gcGet(a, "rows"))) == 12, "party formation drift")
	a = by["MS2_EQUIPMENT_HEADER_NAMES"]
	c.need(gcAssetLocator(a, "MS2_PC_LOCALIZED", 55, 1468, "commonBE.tid", "ms2_name_family_font_v1", "equipment_header_chiaro", "d540e4d718490eb990313830782662aed075650eb56c19266d4c8f3965dcd7f3") && len(gcArr(gcGet(a, "rows"))) == 13 && gcJSONEqual(gcGet(gcRowText(a, "KAGUYAHIME"), "rect"), []int{3608, 362, 3833, 404}), "MS2 equipment labels/fit drift")
	ms2Equipment := a
	a = by["MSF_EQUIPMENT_HEADER_NAMES"]
	c.need(gcAssetLocator(a, "MSF_PC_LOCALIZED", 74, 8, "commonBE.tid", "ms2_name_family_font_v1", "equipment_header_chiaro", "e8ac0ad32138bb600d29bb2f895103c5c17d9fe7f0688af0c879336a7b3fa070") && len(gcArr(gcGet(a, "rows"))) == 13, "Finale equipment source drift")
	c.need(gcJSONEqual(gcGet(a, "rows"), gcGet(ms2Equipment, "rows")) && gcJSONEqual(gcGet(a, "replacements"), gcGet(ms2Equipment, "replacements")), "shared equipment row/translation drift")
	a = by["MS2_BATTLE_NAMES"]
	c.need(gcStr(gcGet(a, "renderer")) == "ms2_battle_nameplates_font_v3" && gcStr(gcGet(a, "typography_preset")) == "battle_nameplate" && gcJSONEqual(gcGet(a, "extra_cleanup_names"), []string{"NINGYOHIME", "AKAZUKIN"}) && gcInt(gcGet(a, "extra_cleanup_filter_size")) == 21 && gcStr(gcGet(a, "background_asset")) == "battle/ms2_nameplate_bg.png" && math.Abs(gcFloat(gcGet(a, "final_scale"))-0.94) < 1e-9 && gcInt(gcGet(a, "supersample")) >= 3 && len(gcArr(gcGet(a, "rows"))) == 15 && gcJSONEqual(gcGet(gcRowText(a, "JACK"), "rect"), []int{780, 1784, 1123, 1965}), "battle type/rows/source geometry drift")
	a = by["MS2_AFFECTION_CHARACTER_NAMES"]
	c.need(gcStr(gcGet(a, "renderer")) == "ms2_affection_font_v2" && gcStr(gcGet(a, "typography_preset")) == "affection_cyan" && len(gcArr(gcGet(a, "rows"))) == 11, "affection renderer drift")
	c.need(reflect.DeepEqual(gcKeys(typ), []string{"fonts", "presets", "schema_version"}) && gcInt(gcGet(typ, "schema_version")) == 1, "typography schema")
	fontNames := []string{"avionic", "avionic_regular", "chiaro", "gebryna", "neofara"}
	sort.Strings(fontNames)
	c.need(reflect.DeepEqual(gcKeys(gcGet(typ, "fonts")), fontNames), "font set drift")
	shaPattern := regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	for name, conf := range gcObj(gcGet(typ, "fonts")) {
		_, has := gcObj(conf)["note"]
		c.need(!has, name+": private provenance leaked")
		c.need(shaPattern.MatchString(gcStr(gcGet(conf, "reference_sha256"))), name+": invalid reference SHA")
		repo := gcStr(gcGet(conf, "repo_path"))
		c.need(repo != "" && !filepath.IsAbs(repo), name+": font repo path")
		if !skipFontPresence && repo != "" {
			info, err := os.Stat(filepath.Join(root, repo))
			c.need(err == nil && info.Mode().IsRegular(), name+": font file missing")
		}
	}
	presetNames := strings.Fields(`living_quarters_normal living_quarters_selected camp_top2_vertical affection_cyan affection_pink camp_top_legacy_name result_nameplate equipment_header_chiaro party_formation_pink heroine_room_banner_gebryna ms2_room_entry_gebryna battle_nameplate first_game_residence_normal first_game_residence_selected title_footer_chiaro pink_block_large_neofara pink_block_small_neofara white_name_neofara white_name_small_neofara camp_gold_neofara finale_ren_gray_neofara finale_camp_vertical_neofara finale_room_pink_neofara finale_battle_pink_neofara heroine_room_banner_neofara camp_top2_vertical_neofara camp_top_legacy_neofara r32c3_camp_gold r32c3_finale_battle r32c3_finale_battle_v2 r32c3_finale_bonus_name r32c3_finale_bonus_select r32c3_finale_camp r32c3_finale_camp_v2 r32c3_ren_gray r32c3_room_pink r32c3_room_pink_fsf r32c3_white_oblique r32c3_white_oblique_small r32c3_select_subtitle`)
	sort.Strings(presetNames)
	c.need(reflect.DeepEqual(gcKeys(gcGet(typ, "presets")), presetNames), "typography preset set drift")
	for name, p := range gcObj(gcGet(typ, "presets")) {
		_, has := gcObj(p)["note"]
		c.need(!has, name+": preset provenance leaked")
	}
	p := func(name string) any { return gcGet(gcGet(typ, "presets"), name) }
	font := func(name string) string { return gcStr(gcGet(p(name), "font_face")) }
	c.need(font("living_quarters_normal") == "avionic_regular" && font("camp_top2_vertical") == "avionic" && font("camp_top_legacy_name") == "chiaro" && font("result_nameplate") == "avionic" && gcFloat(gcGet(p("result_nameplate"), "shadow_blur_radius")) > 0, "core font/preset drift")
	c.need(font("equipment_header_chiaro") == "chiaro" && font("party_formation_pink") == "avionic" && font("ms2_room_entry_gebryna") == "gebryna", "equipment/room font drift")
	bgPath := filepath.Join(root, "graphics", "assets", "room_selector", "selected_bg.png")
	info, err := os.Stat(bgPath)
	c.need(err == nil && info.Mode().IsRegular(), "living quarters source background missing")
	a = by["MS2_HEROINE_ROOM_LIST_BLUE"]
	c.need(gcAssetLocator(a, "MS2_PC_LOCALIZED", 55, 1853, "menu_heroineroom.tid", "ms2_heroine_room_blue_font_v6", "ms2_room_entry_gebryna", gcStr(gcGet(gcAt(gcGet(a, "source_variants"), 0), "tid_sha256"))) && len(gcArr(gcGet(a, "rows"))) == 11, "main heroine room locator/family")
	names := []string{"NINGYOHIME", "HAMELN", "GRETEL", "RAPUNZEL", "KAGUYAHIME", "OYAYUBIHIME", "SHIRAYUKIHIME", "NEMURIHIME", "CINDERELLA", "AKAZUKIN", "ALICE"}
	widths := []int{581, 395, 382, 489, 399, 574, 501, 591, 555, 565, 308}
	for i, r := range gcArr(gcGet(a, "rows")) {
		if i >= len(names) {
			break
		}
		c.need(gcStr(gcGet(r, "name")) == names[i] && gcInt(gcGet(r, "name_target_width")) == widths[i], "main heroine room order/width drift")
	}
	c.need(math.Abs(gcFloat(gcGet(a, "room_vertical_scale"))-0.82) < 1e-9 && gcStr(gcGet(a, "room_vertical_anchor")) == "bottom", "main room ROOM scale/anchor drift")
	c.need(gcJSONEqual(gcArr(gcGet(p("ms2_room_entry_gebryna"), "face_dark_rgba"))[:3], []int{0, 4, 16}) && gcJSONEqual(gcArr(gcGet(p("ms2_room_entry_gebryna"), "inner_stroke_rgba"))[:3], []int{189, 247, 255}), "main room font edge treatment drift")
	a = by["MS2_MYROOM_TITLE"]
	c.need(gcInt(gcGet(a, "archive")) == 55 && gcInt(gcGet(a, "gdat_index")) == 1860 && gcStr(gcGet(a, "cl3_inner_name")) == "menu_kyoju_myroom.tid" && gcStr(gcGet(a, "renderer")) == "ms2_myroom_title_font_v3" && gcStr(gcGet(a, "typography_preset")) == "ms2_room_entry_gebryna" && gcStr(gcGet(a, "name")) == "TSUU" && gcInt(gcGet(a, "name_target_width")) == 342 && gcInt(gcGet(a, "name_offset_y")) == 22 && math.Abs(gcFloat(gcGet(a, "room_vertical_scale"))-0.92) < 1e-9 && gcStr(gcGet(a, "room_vertical_anchor")) == "top", "MS2 My Room source/geometry drift")
	a = by["MS2_HEROINE_ROOM_BANNERS"]
	c.need(gcStr(gcGet(a, "renderer")) == "ms2_heroine_room_banners_font_v2" && gcStr(gcGet(a, "typography_preset")) == "heroine_room_banner_neofara" && len(gcArr(gcGet(a, "replacements"))) == 11, "included first game banner renderer/family drift")
	c.need(len(gcArr(gcGet(a, "changed_gdat_indices"))) == len(gcArr(gcGet(a, "targets"))), "banner grouped target drift")
	for i, t := range gcArr(gcGet(a, "targets")) {
		c.need(gcInt(gcAt(gcGet(a, "changed_gdat_indices"), i)) == gcInt(gcGet(t, "gdat_index")), "banner grouped target order drift")
	}
	c.need(font("heroine_room_banner_neofara") == "neofara" && gcJSONEqual(gcArr(gcGet(p("heroine_room_banner_neofara"), "shadow_rgba"))[:3], []int{5, 0, 5}) && gcInt(gcGet(p("heroine_room_banner_neofara"), "shadow_stroke_width")) >= 7, "magenta heroine room treatment drift")
	a = by["MS1_INCLUDED_RESIDENCE_NAMES"]
	c.need(gcInt(gcGet(a, "archive")) == 55 && gcInt(gcGet(a, "gdat_index")) == 1883 && gcStr(gcGet(a, "cl3_inner_name")) == "menu_kyojuu.tid" && gcStr(gcGet(a, "renderer")) == "ms1_residence_names_raster_v2", "first-game residence source drift")
	c.need(len(gcArr(gcGet(a, "rows"))) == 14 && len(gcArr(gcGet(a, "source_rows"))) == 14 && len(gcArr(gcGet(a, "normal_rects"))) == 14 && len(gcArr(gcGet(a, "selected_rects"))) == 14 && gcInt(gcGet(a, "expected_changed_rows")) == 6, "first-game residence rows drift")
	c.need(gcStr(gcGet(gcAt(gcGet(a, "rows"), 11), "text")) == "TSUU AND NINGYOHIME'S ROOM" && gcStr(gcGet(gcAt(gcGet(a, "source_variants"), 0), "tid_sha256")) == "e5a0c241a1d515745377473f1e3bea3a583d5756673f829d215eaa1c9f394eab", "first-game residence source/name drift")
	c.need(gcFloat(gcGet(p("battle_nameplate"), "rotation_degrees")) > 0 && font("battle_nameplate") == "neofara", "battle tilt/font drift")
	for _, id := range []string{"MS2_JOB_CLASS_24PX", "MS2_JOB_CLASS_MIDDLE", "MS2_KESSIKI_SKILL_LABELS"} {
		c.need(gcStr(gcGet(by[id], "renderer")) == "font_atlas_v2", id+": font atlas renderer drift")
	}
	footer := map[string]struct {
		dataset        string
		archive, index int
		inner          string
		rect           []int
		sha            string
	}{
		"MS2_TITLE_MOD_FOOTER_MAIN": {"MS2_PC_LOCALIZED", 55, 1821, "titlemenu.tid", []int{50, 2544, 900, 2634}, "101c81d0e9953dfea166ed01852e3f4f50c20f575f59d1f990be41f40c9e2311"},
		"MS2_TITLE_MOD_FOOTER_BE":   {"MS2_PC_LOCALIZED", 55, 1822, "TitleBE.tid", []int{1312, 1660, 2040, 1732}, "44cd6d66ec412412b650b431120c4c04390cb8574d357905bd39b5ac54d66462"},
		"MSF_TITLE_MOD_FOOTER":      {"MSF_PC_LOCALIZED", 73, 220, "title_f.tid", []int{374, 1796, 1230, 1886}, "4cfbfb005ba44299615259ece0485ffa56c4a427c06b82917ed21e12b441d7fd"},
	}
	for id, s := range footer {
		a = by[id]
		c.need(gcAssetLocator(a, s.dataset, s.archive, s.index, s.inner, "title_copyright_footer_chiaro_v2", "title_footer_chiaro", s.sha) && gcJSONEqual(gcGet(a, "copyright_rect"), s.rect), id+": title-footer source/geometry drift")
	}
	for id, asset := range by {
		if strings.Contains(id, "TITLE_MOD_FOOTER") {
			_, ok := footer[id]
			c.need(ok, "unapproved title footer: "+id)
			c.need(gcStr(gcGet(asset, "cl3_inner_name")) != "title_moji.tid", "rejected title_moji footer returned")
		}
	}
	return c.err
}
