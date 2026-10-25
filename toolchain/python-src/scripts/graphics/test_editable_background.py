"""Minimal regression: edits in public nameplate PNG must change rendered pixels.

Uses an artificial atlas and synthetic PNG, no private game data or fonts.
"""
import importlib.util
import tempfile
from pathlib import Path

from PIL import Image

source = Path(__file__).with_name('build_graphics.py')
spec = importlib.util.spec_from_file_location('ms_build_graphics', source)
bg = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bg)

with tempfile.TemporaryDirectory() as temp:
    root = Path(temp)
    filename = root / 'battle' / 'ms2_nameplate_bg.png'
    filename.parent.mkdir()
    bg.LABEL_ASSET_ROOT = root
    bg._ui_preset = lambda _: {}
    bg._make_layered_ui_label_supersampled = lambda *a, **kw: Image.new('RGBA', (2, 2), (255, 10, 10, 255))
    original = Image.new('RGBA', (18, 10), (0, 0, 0, 255))
    asset = dict(id='MS2_BATTLE_NAMES', typography_preset='battle_nameplate',
                 background_asset='battle/ms2_nameplate_bg.png',
                 rows=[dict(text='TEST', rect=[2, 1, 14, 7])],
                 text_box=[0, 0, 2, 2], final_scale=1, supersample=1)
    Image.new('RGBA', (12, 6), (10, 20, 30, 255)).save(filename)
    first = bg.render_ms2_battle_nameplates_font_v3(original, asset)
    Image.new('RGBA', (12, 6), (80, 90, 100, 255)).save(filename)
    second = bg.render_ms2_battle_nameplates_font_v3(original, asset)
    assert first.getpixel((10, 5)) == (10, 20, 30, 255)
    assert second.getpixel((10, 5)) == (80, 90, 100, 255)
    assert second.getpixel((2, 1)) == (255, 10, 10, 255)
    assert second.getpixel((0, 0)) == original.getpixel((0, 0))
print('EDITABLE PNG → RENDERER: PASS')
