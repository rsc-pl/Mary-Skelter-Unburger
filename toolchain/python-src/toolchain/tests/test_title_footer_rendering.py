#!/usr/bin/env python3
from __future__ import annotations
import importlib.util
from pathlib import Path
from PIL import Image, ImageDraw, ImageChops

ROOT=Path(__file__).resolve().parents[4]
spec=importlib.util.spec_from_file_location('bg',ROOT/'toolchain'/'python-src'/'scripts'/'graphics'/'build_graphics.py')
bg=importlib.util.module_from_spec(spec); assert spec.loader; spec.loader.exec_module(bg)
assert bg._title_footer_text()==f'UNBURGER MOD V{(ROOT/"version.txt").read_text().strip()}'

im=Image.new('RGBA',(1024,512),(0,0,0,0))
rect=(64,380,960,470)
# Synthetic two-line shipped legal artwork inside the exact UV cell.
legal=Image.new('RGBA',(rect[2]-rect[0],rect[3]-rect[1]),(0,0,0,0))
d=ImageDraw.Draw(legal)
d.rectangle((5,5,890,32),fill=(255,255,255,255))
d.rectangle((120,40,775,66),fill=(220,220,220,255))
im.paste(legal,(rect[0],rect[1]))
a={
    'id':'TEST_TITLE',
    'copyright_rect':list(rect),
    'typography_preset':'title_footer_chiaro',
    'legal_scale':0.70,
    'footer_nominal_size':24,
    'footer_min_size':18,
}
out=bg.render_title_copyright_footer_chiaro_v2(im,a)
diff=ImageChops.difference(im,out); bbox=diff.getbbox(); assert bbox
assert bbox[0]>=rect[0] and bbox[1]>=rect[1] and bbox[2]<=rect[2] and bbox[3]<=rect[3],bbox
crop=out.crop(rect)
assert crop.getchannel('A').getbbox() is not None
# The lower footer band must become non-empty, while legal art survives above it.
assert crop.crop((0,0,crop.width,55)).getchannel('A').getbbox() is not None
assert crop.crop((0,60,crop.width,crop.height)).getchannel('A').getbbox() is not None

bad=dict(a); bad['legal_scale']=0.95
try:
    bg.render_title_copyright_footer_chiaro_v2(im,bad)
except SystemExit:
    pass
else:
    raise AssertionError('unsafe legal scale did not fail closed')
print('title footer rendering regression: PASS')
