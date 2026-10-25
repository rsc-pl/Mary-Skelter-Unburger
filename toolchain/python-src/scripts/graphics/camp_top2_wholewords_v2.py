#!/usr/bin/env python3
"""MS2 Switch canonical runtime-accepted renderer: eight complete word renders for localized camp_top_2 names.

Source g133 is the geometric/typographic reference; eight Western names are localized.
All words use a single SHA-pinned font, calibrated to six preserved original names.
No alternate translation tree or individual-glyph compositing. Runtime visually accepted by owner on the Western update in Eden.
"""
from __future__ import annotations
import argparse
import hashlib
import importlib.util
import json
import os
import sys
import tempfile
import zipfile
from pathlib import Path
from PIL import Image, ImageChops
import numpy as np

SHA_STOCK_074 = 'c6b1c4508ba5896b60b008d88d9a398b2b01b21d04323406ede63fcaaea21f79'
SHA_TID = '6cac6e1c439db75ac43084e3452fb959c11984030b178f7b6a23c2ba445d8f39'
SHA_CL3 = 'aba38d41a5cb97fe80088332ae3fd11d7385206eb41a1db3b2b236aea1debf20'
IDX = 133
Y0,Y1 = 808,1112
# Historical source words are identity checks, NOT a second editable EN translation tree.
# Actual desired EN labels come exclusively from graphics/manifest.json (PC canonical).
SOURCE = [
 ('OTSUU',1450,1475), ('LITTLE MERMAID',1490,1515), ('HAMELN',1530,1555),
 ('GRETEL',1570,1595), ('RAPUNZEL',1610,1635), ('KAGUYA',1649,1675),
 ('THUMBELINA',1690,1715), ('SNOW WHITE',1730,1755),
 ('SLEEPING BEAUTY',1770,1801), ('RED RIDING HOOD',1810,1835),
 ('CINDERELLA',1850,1875), ('ALICE',1890,1915),
 ('MAMORU & HANSEL',1930,1955), ('OTSUU & JACK',1970,1998),
]


def sha(data): return hashlib.sha256(data).hexdigest()
def file_sha(path):
    h=hashlib.sha256()
    with open(path,'rb') as f:
        for block in iter(lambda:f.read(1<<20),b''): h.update(block)
    return h.hexdigest()


def import_source(name,path):
    s=importlib.util.spec_from_file_location(name,path)
    if s is None or s.loader is None: raise ValueError(f'Cannot import {path}')
    m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m


def _mask_words(font, word, height=25, shear=0.0):
    """Render the whole shaped word at 4x, downsample once, preserve kerning."""
    from PIL import ImageDraw
    scale=4
    highfont=font
    rawbox=highfont.getbbox(word)
    if not rawbox or rawbox[2] <= rawbox[0] or rawbox[3] <= rawbox[1]:
        raise ValueError(f'Empty font bbox for {word!r}')
    pad=8*scale
    im=Image.new('L',(rawbox[2]-rawbox[0]+2*pad,rawbox[3]-rawbox[1]+2*pad),0)
    ImageDraw.Draw(im).text((pad-rawbox[0],pad-rawbox[1]),word,font=highfont,fill=255)
    bb=im.getbbox()
    if not bb: raise ValueError(f'Empty rendered word {word!r}')
    im=im.crop(bb)
    if shear:
        # Skew at supersampled size. Positive shear leans glyph tops right.
        from PIL import Image as _Image
        hh=im.height
        margin=round(abs(shear)*hh)+4*scale
        expanded=_Image.new('L',(im.width+margin*2,hh),0)
        expanded.paste(im,(margin,0))
        im=expanded.transform(expanded.size,_Image.Transform.AFFINE,
             (1.0,shear,-shear*hh,0,1.0,0),resample=_Image.Resampling.BICUBIC)
        bb=im.getbbox()
        if not bb:raise ValueError(f'Empty sheared word {word}')
        im=im.crop(bb)
    neww=max(1,round(im.width*height/im.height))
    return im.resize((neww,height),Image.Resampling.LANCZOS)


def _calibrate_face(original, bg):
    """Score installed SHA-pinned faces against six untouched original words.

    We compare complete word shapes, not harvested individual glyphs. Width
    calibration from the six retained words preserves normal kerning across all
    eight replacements and avoids any manually assembled type.
    """
    from PIL import ImageFont
    from PIL import ImageOps
    import statistics
    kept=[(text,x0,x1) for text,x0,x1 in SOURCE if text in
          {'HAMELN','GRETEL','RAPUNZEL','CINDERELLA','ALICE','MAMORU & HANSEL'}]
    faces=['neofara','avionic','avionic_regular','chiaro','gebryna']
    results=[]
    for face in faces:
        try:
            path=bg._resolve_ui_font_path(face)
            font=ImageFont.truetype(str(path), 37*4)
        except (SystemExit,OSError,ValueError,RuntimeError):
            continue
        for shear in (0.0,0.10,0.18,0.26,-0.10,-0.18):
            originals=[]; predictions=[]
            for text,x0,x1 in kept:
                old=original.crop((x0,Y0,x1,Y1)).transpose(Image.Transpose.ROTATE_270).getchannel('A')
                bb=old.getbbox()
                if bb is None: raise ValueError(f'Empty source donor {text}')
                old=old.crop(bb)
                synth=_mask_words(font,text,height=old.height,shear=shear)
                originals.append(old); predictions.append(synth)
            ratios=[old.width/max(1,pred.width) for old,pred in zip(originals,predictions)]
            ws=float(statistics.median(ratios))
            errs=[]
            for old,pred in zip(originals,predictions):
                scaled=pred.resize(old.size,Image.Resampling.LANCZOS)
                oa=np.asarray(old,dtype=np.float32)/255
                sa=np.asarray(scaled,dtype=np.float32)/255
                errs.append(float(np.abs(oa-sa).mean()))
            results.append((sum(errs)/len(errs),face,str(path),font,ws,ratios,shear))
    if not results:
        raise ValueError('No available SHA-verified pinned project font for CampTop2')
    results.sort(key=lambda r:r[0])
    return results[0], [{'face':f,'shape_error':round(e,6),
                         'width_ratio_median':round(w,4),'shear':q}
                         for e,f,_,_,w,_,q in results[:12]]


def wholeword_patch(original, manifest, bg):
    spec=next((a for a in manifest['assets'] if a['id']=='MS2_CAMP_TOP2_CHARACTER_NAMES'),None)
    if not spec or spec.get('switch_native_policy')!='canonical_whole_words_v2' or len(spec['rows'])!=14:
        raise ValueError('native V2 canonical manifest not present or row count differs')
    wanted=[r['text'] for r in spec['rows']]
    if wanted!=['TSUU','NINGYOHIME','HAMELN','GRETEL','RAPUNZEL','KAGUYAHIME',
               'OYAYUBIHIME','SHIRAYUKIHIME','NEMURIHIME','AKAZUKIN',
               'CINDERELLA','ALICE','MAMORU & HANSEL','TSUU & JACK']:
        raise ValueError('Current canonical labels drifted; source review required')
    chosen,scores=_calibrate_face(original,bg)
    score,face,fontpath,font,widthscale,ratios,shear=chosen
    # Six already-canonical original labels are the baseline for actual glyph
    # cap height. This is NOT the width/length of each full word: longer names
    # properly extend farther down each vertical slot, but all must have the
    # same letter height/weight in the perpendicular direction.
    preserved_caps=[]
    for (source_name,x0,x1),canonical in zip(SOURCE,wanted):
        if source_name != canonical:
            continue
        donor=original.crop((x0,Y0,x1,Y1)).transpose(Image.Transpose.ROTATE_270).getchannel('A')
        bbox=donor.getbbox()
        if bbox is None: raise ValueError(f'Missing preserved reference: {source_name}')
        preserved_caps.append(bbox[3]-bbox[1])
    if len(preserved_caps)!=6 or len(set(preserved_caps))!=1:
        raise ValueError(f'Preserved canonical glyph cap heights disagree: {preserved_caps}')
    cap_height=preserved_caps[0]
    if cap_height!=25:
        raise ValueError(f'Unexpected source cap height: {cap_height} (expected pinned 25)')
    out=original.copy()
    original_alpha=np.asarray(original.getchannel('A'))
    changes=[]; data=[]
    for (old,x0,x1),new in zip(SOURCE,wanted):
        if old==new:continue
        orig=original.crop((x0,Y0,x1,Y1)).transpose(Image.Transpose.ROTATE_270).getchannel('A')
        bb=orig.getbbox()
        if not bb:raise ValueError(f'Missing source name {old}')
        # Align to actual source-native top after reversing the 270-degree
        # horizontal word transform; do NOT normalize every name's length.
        source_cap_height=bb[3]-bb[1]
        label=_mask_words(font,new,height=cap_height,shear=shear)
        # Calibrate a single global typographic WIDTH scale from the six
        # retained names. Every glyph cap HEIGHT stays at their common 25 px.
        neww=max(1,round(label.width*widthscale))
        if neww > (Y1-Y0-8):
            # Do not clip or shrink glyph cap height. Only compress word length.
            neww=Y1-Y0-8
        label=label.resize((neww,cap_height),Image.Resampling.LANCZOS)
        if neww<10 or neww>Y1-Y0-8 or cap_height>x1-x0:
            raise ValueError(f'Word fit failed for {new}: {neww}x{cap_height}')
        # ROTATE_270 reverses the horizontal axis of the word view.
        # Native vertical top is `304 - source_270_bbox_right`, not bb[1]
        # (the latter measures lane thickness). Preserve the source 2-4 px top.
        cap_top=(Y1-Y0)-bb[2]
        cx=(x1-x0-cap_height)//2
        canvas=Image.new('L',(Y1-Y0,x1-x0),0)
        canvas.paste(label,(Y1-Y0-cap_top-neww,cx))
        rotated=canvas.transpose(Image.Transpose.ROTATE_90)
        # Original white glyph antialiasing extends exactly one pixel beyond
        # several historical strip bounds. Removing only x0..x1 left thin
        # vertical strokes in the candidate. Clear the pinned source fringe,
        # but explicitly fail if the next pixel is occupied (might be artwork).
        if np.any(original_alpha[Y0:Y1,x0-2]) or np.any(original_alpha[Y0:Y1,x1+1]):
            raise ValueError(f'{new}: source ink extends beyond safe 1px fringe')
        clear_x0=x0-1; clear_x1=x1+1
        clear=Image.new('RGBA',(clear_x1-clear_x0,Y1-Y0),(0,0,0,0))
        out.paste(clear,(clear_x0,Y0))
        white=Image.new('RGBA',rotated.size,(255,255,255,0));white.putalpha(rotated)
        out.alpha_composite(white,(x0,Y0))
        changes.append((old,new,x0,x1))
        name_bbox=out.crop((x0,Y0,x1,Y1)).getchannel('A').getbbox()
        if name_bbox is None:
            raise ValueError(f'New vertical name empty: {new}')
        if name_bbox[2]-name_bbox[0]!=cap_height or name_bbox[1]!=cap_top:
            raise ValueError(f'{new}: in-atlas cap-height/top placement changed: {name_bbox}; '
                             f'expected cap height {cap_height} top {cap_top}')
        if name_bbox[3]>Y1-Y0:
            raise ValueError(f'{new}: name extends beyond native atlas lane')
        data.append({'source':old,'canonical':new,'source_clear_x':[clear_x0,clear_x1],'source_bbox':list(bb),
                     'source_cap_height':source_cap_height,'new_word_width':neww,
                     'new_word_height':cap_height,'native_atlas_bbox':list(name_bbox),
                     'source_lane':[x0,x1]})
    before=np.asarray(original);after=np.asarray(out)
    diff=np.any(before!=after,axis=2)
    permitted=np.zeros(diff.shape,dtype=bool)
    for _,_,x0,x1 in changes: permitted[Y0:Y1,x0-1:x1+1]=True
    extraneous=int(np.count_nonzero(diff&~permitted))
    if extraneous:raise ValueError(f'Changed {extraneous} pixels outside eight allowed strips')
    for (old,x0,x1),new in zip(SOURCE,wanted):
        if old==new and not np.array_equal(before[Y0:Y1,x0-2:x1+2],after[Y0:Y1,x0-2:x1+2]):
            raise ValueError(f'Preserved name was modified: {old}')
    if len(changes)!=8:raise ValueError(f'Expected 8 changes, got {len(changes)}')
    details={'native_slot_height':Y1-Y0,'canonical_cap_height':cap_height,'reference_caps':preserved_caps,
             'font_face':face,'font_sha256':file_sha(Path(fontpath)),
             'reference_shape_scores':scores,'scale_x':round(widthscale,5),'shear':shear,
             'per_name':data, 'changed_rgba_pixels':int(diff.sum())}
    return out,changes,int(diff.sum()),details




def render_accepted_switch_name_atlas(original, manifest, bg):
    """V2 exact whole-word recipe; in-game confirmed 2026-10-10.

    Fails closed on source-label drift, font absence, cap-height/slot geometry,
    overlong words or non-target pixel changes. Never modifies input.
    """
    return wholeword_patch(original,manifest,bg)
