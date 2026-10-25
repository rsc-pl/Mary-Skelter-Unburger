"""R32C3 full-family typography renderer. No fonts or private game inputs are bundled.

All text is rasterized and transformed at >=4x final resolution, into padded
straight-alpha masks. Layers are composited in deterministic order, then exactly
one final downsample is performed. No historical raster glyphs are spliced.
"""
from __future__ import annotations

import hashlib
import math
from typing import Callable

from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont


def _mask_text(text: str, font: ImageFont.FreeTypeFont, stroke: int, pad: int):
    probe = ImageDraw.Draw(Image.new('L', (1, 1), 0))
    box = probe.textbbox((0, 0), text, font=font, stroke_width=stroke)
    w, h = box[2] - box[0], box[3] - box[1]
    if w < 1 or h < 1:
        raise ValueError(f'R32C3 empty glyph: {text!r}')
    im = Image.new('L', (w + 2 * pad, h + 2 * pad), 0)
    ImageDraw.Draw(im).text((pad - box[0], pad - box[1]), text, font=font,
                            fill=255, stroke_width=stroke, stroke_fill=255)
    return im


def _fit_mask_pair(text, face_name, preset, font_loader, f, rect_size):
    """Find a fitting face and horizontal scale, honoring the declared scale floor.

    First preserve the exact existing R32C3 fit (for already accepted labels).
    Only if that fails, use the preset's pre-existing min_horizontal_scale.
    This mirrors the older PC UI font fitting policy: do not shorten names,
    go below the declared floor, or silently bypass the slot's height limit.
    """
    w,h=rect_size
    px=max(2,int(w*0.025))*f
    py=max(2,int(h*0.05))*f
    sx=float(preset.get('horizontal_scale',1.0))
    min_sx=float(preset.get('min_horizontal_scale',sx))
    sy=float(preset.get('vertical_scale',1.0))
    shear=float(preset.get('shear_x',0.0))
    if not (0 < min_sx <= sx and sy > 0):
        raise ValueError('R32C3 invalid horizontal/vertical scaling limits')
    ring=int(preset.get('r32c3_inner_width',0))+int(preset.get('r32c3_outer_width',preset.get('stroke_width',0)))
    probe=ImageDraw.Draw(Image.new('L',(1,1),0))
    sizes=range(int(preset['nominal_size']),int(preset.get('min_size',16))-1,-1)
    # Preserve previous results bit-for-bit whenever an ordinary fit exists.
    for fallback in (False, True):
        for sz in sizes:
            font=font_loader(sz*f,face_name)
            bb=probe.textbbox((0,0),text,font=font,stroke_width=0)
            tw=bb[2]-bb[0]+2*ring*f
            th=bb[3]-bb[1]+2*ring*f
            if th*sy+4*f > h*f-2*py:
                continue
            available=(w*f-2*px-4*f-abs(shear)*th*sy)
            if tw<=0 or available<=0:
                continue
            fit_sx=sx if not fallback else min(sx,available/tw)
            if fit_sx < min_sx-1e-8 or tw*fit_sx>available+1e-6:
                continue
            pad=max(16*f,(sz//2+ring+10)*f)
            size=(bb[2]-bb[0]+2*pad,bb[3]-bb[1]+2*pad)
            return font,bb,pad,size,fit_sx
    raise ValueError(f'R32C3 glyph cannot fit {w}x{h} within declared minimum scale: {text!r}')


def _draw_mask(text,font,bbox,pad,size,stroke):
    out=Image.new('L',size,0)
    ImageDraw.Draw(out).text((pad-bbox[0],pad-bbox[1]),text,font=font,fill=255,
                             stroke_width=stroke,stroke_fill=255)
    return out


def _warp_mask(im, preset, factor):
    sx=float(preset.get('horizontal_scale', 1.0))
    sy=float(preset.get('vertical_scale', 1.0))
    shear=float(preset.get('shear_x', 0.0))
    bb=im.getbbox()
    if bb is None: return im
    # Include supersampled padding in all transforms; crop AFTER, not before.
    im=im.resize((max(1,round(im.width*sx)),max(1,round(im.height*sy))),Image.Resampling.LANCZOS)
    if abs(shear)>1e-6:
        extra=math.ceil(abs(shear)*im.height)+8*factor
        # Negative shear == right-leaning text in this graphics pipeline.
        im=im.transform((im.width+extra,im.height),Image.Transform.AFFINE,
                        (1,-shear,-extra//2,0,1,0),resample=Image.Resampling.BICUBIC)
    degrees=float(preset.get('rotation_degrees',0.0))
    if abs(degrees)>1e-6:
        im=im.rotate(degrees,expand=True,resample=Image.Resampling.BICUBIC)
    return im


def _tint(size, color, mask):
    rgba=tuple(int(v) for v in color)
    alpha=mask
    if len(rgba)>3 and rgba[3]!=255:
        alpha=mask.point(lambda v: (v*rgba[3]+127)//255)
    out=Image.new('RGBA',size,rgba[:3]+(0,))
    out.putalpha(alpha)
    return out


def _gradient(size, mask, top, bottom):
    # Per-scanline colors with a full-strength straight-alpha mask.
    w,h=size
    strip=Image.new('RGBA',(1,h),(0,0,0,0))
    pix=strip.load()
    for y in range(h):
        k=y/max(1,h-1)
        pix[0,y]=tuple(int(round(top[i]*(1-k)+bottom[i]*k)) for i in range(4))
    full=strip.resize(size,Image.Resampling.NEAREST)
    existing=full.getchannel('A')
    full.putalpha(ImageChops.multiply(mask,existing))
    return full


def _solid_accents(face, text: str, strength: float, factor: int):
    """Long, connected in-glyph cuts, not freestanding/random background dots."""
    if strength<=0: return Image.new('L',face.size,0)
    bbox=face.getbbox()
    if not bbox: return Image.new('L',face.size,0)
    x0,y0,x1,y1=bbox; w=x1-x0; h=y1-y0
    key=hashlib.sha256(text.encode('utf-8')).digest()
    pattern=Image.new('L',face.size,0)
    draw=ImageDraw.Draw(pattern)
    # Only a handful of broad, directional irregular accents.
    count=max(1,min(7,round(float(strength)*16)))
    for j in range(count):
        frac=(j+1)/(count+1)
        cx=x0+int(w*frac)+(key[j]%9-4)*factor
        cy=y0+int(h*(0.2+0.58*(key[8+j]/255)))
        seg=max(11*factor,int(h*(0.24+0.10*max(0.0,strength))))
        thick=max(2*factor,int(h*0.07*max(0.55,strength)))
        draw.polygon([(cx-seg//2,cy+thick),(cx+seg//3,cy-thick),
                      (cx+seg//2,cy-thick//2),(cx-seg//3,cy+thick*2)],fill=255)
    return ImageChops.multiply(pattern,face)


def render_label(size, text, preset: dict, font_loader: Callable, *, factor=4,
                 accents=0.0):
    if factor<4: raise ValueError('R32C3 requires >=4x supersampling')
    w,h=map(int,size)
    if w<10 or h<10: raise ValueError(f'R32C3 impossible text slot {size!r}')
    face_name=preset.get('font_face','neofara')
    font,bb,pad,raw_size,fit_sx=_fit_mask_pair(text,face_name,preset,font_loader,factor,(w,h))
    # Never mutate the accepted family preset: only this one label may need
    # the permitted horizontal squeeze (all glyph layers share one transform).
    render_preset=preset if fit_sx == float(preset.get('horizontal_scale',1.0)) else dict(preset,horizontal_scale=fit_sx)
    inner_width=int(preset.get('r32c3_inner_width',0))*factor
    outer_width=int(preset.get('r32c3_outer_width',preset.get('stroke_width',0)))*factor
    face=_warp_mask(_draw_mask(text,font,bb,pad,raw_size,0),render_preset,factor)
    inner=_warp_mask(_draw_mask(text,font,bb,pad,raw_size,inner_width),render_preset,factor) if inner_width else face
    outer=_warp_mask(_draw_mask(text,font,bb,pad,raw_size,inner_width+outer_width),render_preset,factor) if outer_width else inner
    shadow=outer
    offset=preset.get('shadow_offset',[0,0]); ox,oy=[int(round(v*factor)) for v in offset]
    bb=outer.getbbox()
    if bb is None: raise ValueError(f'R32C3 lost glyph: {text!r}')
    border=max(6*factor,round(max(w,h)*0.03*factor))
    left=max(0, -min(bb[0], bb[0]+ox))+border
    top=max(0, -min(bb[1], bb[1]+oy))+border
    right=max(0,max(bb[2],bb[2]+ox)-outer.width)+border
    bottom=max(0,max(bb[3],bb[3]+oy)-outer.height)+border
    size_hi=(outer.width+left+right,outer.height+top+bottom)
    def padded(m):
        dst=Image.new('L',size_hi,0);dst.paste(m,(left,top));return dst
    face,inner,outer=[padded(m) for m in (face,inner,outer)]
    layers=Image.new('RGBA',size_hi,(0,0,0,0))
    sh_col=preset.get('shadow_rgba',[0,0,0,0])
    if int(sh_col[3])>0 and (ox or oy):
        m=Image.new('L',size_hi,0);m.paste(outer,(ox,oy))
        blur=float(preset.get('shadow_blur_radius',0))*factor
        if blur>0:m=m.filter(ImageFilter.GaussianBlur(blur))
        layers.alpha_composite(_tint(size_hi,sh_col,m))
    if outer_width:
        outer_col=preset.get('r32c3_outer_rgba',preset.get('stroke_rgba',[140,0,40,255]))
        layers.alpha_composite(_tint(size_hi,outer_col,outer))
    if inner_width:
        layers.alpha_composite(_tint(size_hi,preset.get('r32c3_inner_rgba',[0,0,0,255]),inner))
    elif not outer_width and 'stroke_rgba' in preset:
        layers.alpha_composite(_tint(size_hi,preset['stroke_rgba'],outer))
    if 'fill_gradient_top_rgba' in preset:
        layers.alpha_composite(_gradient(size_hi,face,preset['fill_gradient_top_rgba'],preset['fill_gradient_bottom_rgba']))
    else:
        layers.alpha_composite(_tint(size_hi,preset.get('fill_rgba',[255,255,255,255]),face))
    if accents>0:
        layers.alpha_composite(_tint(size_hi,[9,4,9,230],_solid_accents(face,text,accents,factor)))
    bounds=layers.getbbox()
    if bounds is None:raise ValueError(f'R32C3 fully transparent: {text!r}')
    # Oversized (already transformed) canvas: preserve a transparent margin for QA.
    pad=max(2*factor,round(min(w,h)*0.06*factor))
    crop=layers.crop((max(0,bounds[0]-pad),max(0,bounds[1]-pad),
                      min(layers.width,bounds[2]+pad),min(layers.height,bounds[3]+pad)))
    limit=(w*factor-2*pad,h*factor-2*pad)
    scale=min(1.,limit[0]/crop.width,limit[1]/crop.height)
    if scale<1.:
        crop=crop.resize((max(1,round(crop.width*scale)), max(1,round(crop.height*scale))),Image.Resampling.LANCZOS)
    high=Image.new('RGBA',(w*factor,h*factor),(0,0,0,0))
    high.alpha_composite(crop,((high.width-crop.width)//2,(high.height-crop.height)//2))
    result=high.resize((w,h),Image.Resampling.LANCZOS)
    bb=result.getchannel('A').getbbox()
    if not bb or bb[0]<1 or bb[1]<1 or bb[2]>w-1 or bb[3]>h-1:
        raise ValueError(f'R32C3 clipping detected: {text!r}, {size!r}, {bb!r}')
    return result


def _source_title_bounds(src, search_rect):
    """Find original high-opacity white *name* ink in one local atlas slot.

    The low-opacity God Jail Tower tagline is deliberately excluded. The
    detector is evaluated on the pristine source image, separately for PC
    and Switch. Nothing relies on V10/V11 hand-estimated ink positions.
    """
    x0,y0,x1,y1=map(int,search_rect)
    if not (0<=x0<x1<=src.width and 0<=y0<y1<=src.height):
        raise ValueError(f'invalid original text search rectangle {search_rect}')
    patch=src.crop((x0,y0,x1,y1)).convert('RGBA')
    px=patch.load()
    lo_x=patch.width; lo_y=patch.height; hi_x=-1; hi_y=-1
    for y in range(patch.height):
        for x in range(patch.width):
            r,g,b,a=px[x,y]
            # Actual title glyphs are bright/opaque; the unchanged subtitle
            # is near-transparent and must not enter the title bounds.
            if min(r,g,b)>=155 and a>=65:
                lo_x=min(lo_x,x);lo_y=min(lo_y,y)
                hi_x=max(hi_x,x);hi_y=max(hi_y,y)
    if hi_x<0:
        raise ValueError(f'No source title glyphs inside {search_rect}')
    return (x0+lo_x,y0+lo_y,x0+hi_x+1,y0+hi_y+1)


# SOURCE-DERIVED MS2 subtitle geometry. Confirmed on the pristine 2048x2048
# atlas: all 26 lines match the same 194x40 'God Jail Tower' source glyphs.
# No fonts, rasterized character names, or game images are bundled.
_MS2_TAG_COLUMNS = (482,796,1110,1424,1738)
_MS2_TAG_ROWS = (1229,1399,1569,1739,1909)
_MS2_TAG_SIZE = (194,40)


def _ms2_tag_boxes(src, rows):
    if src.size != (2048,2048) or len(rows)!=26:
        raise ValueError('MS2 Bonus Select requires original 2048 atlas / 26 names')
    items=[]
    for i,row in enumerate(rows):
        if i<5: x=_MS2_TAG_COLUMNS[i]; y=_MS2_TAG_ROWS[0]
        elif i==5: x=168; y=1497
        elif i<=10: x=_MS2_TAG_COLUMNS[i-6]; y=_MS2_TAG_ROWS[1]
        else:
            j=i-11; x=_MS2_TAG_COLUMNS[j%5]; y=_MS2_TAG_ROWS[2+j//5]
        items.append((x,y,x+194,y+40))
    return items


def _ms2_title_cleanup(src,spec):
    """Deterministically remove ALL old white title ink; reconstruct caption safely.

    The actual source name is RGBA white, high-opacity (~241), with a broad
    antialias fringe extending into low alpha. Its caption is also white but
    only alpha 1..31. Old approaches thresholded/dilated/pasted over one layer,
    which caused ghosting and chopped captions. We clear the entire white title
    in its measured bounds, independently from the repeated caption artwork,
    which is recovered from the pristine reference (same original font). Black
    panel, colored navigation UI, and unrelated atlas art are never cleared.
    """
    src=src.convert('RGBA')
    rows=spec['rows']; out=src.copy()
    bounds=[_source_title_bounds(src,row['rect']) for row in rows]
    tagboxes=_ms2_tag_boxes(src,rows)
    # The unclipped caption source is the first 'God Jail Tower' (JACK).
    canonical=src.crop(tagboxes[0]); cw,ch=canonical.size
    if canonical.getchannel('A').getextrema()[1]>64:
        raise ValueError('The pristine MS2 caption reference is contaminated')
    cp=canonical.load()
    allowed=[]; targets=[]
    # Clear all original title ink with measured padding. Clear each old title
    # BEFORE rendering any new names; the name's final visual position is kept.
    for row,(x0,y0,x1,y1) in zip(rows,bounds):
        cx0=max(0,x0-12); cy0=max(0,y0-9)
        cx1=min(src.width,x1+12); cy1=min(src.height,y1+9)
        cleanup=(cx0,cy0,cx1,cy1)
        # No blanket black rectangle: erase precisely the white source-ink
        # pixels, regardless of alpha. This includes every antialias fringe.
        source=src.crop(cleanup); patch=out.crop(cleanup)
        sp=source.load(); op=patch.load()
        for yy in range(patch.height):
            for xx in range(patch.width):
                r,g,b,a=sp[xx,yy]
                if a and min(r,g,b)>=235:
                    op[xx,yy]=(0,0,0,0)
        out.paste(patch,(cx0,cy0))
        up=2 if row['text']=='KADOWAKI\nTOWA' else 8
        # Identical positioning and fitting geometry to the accepted V17.
        gx0=max(0,x0-6);gy0=max(0,y0-4)
        gx1=min(src.width,x1+6);gy1=min(src.height,y1)
        render_box=(gx0,max(0,gy0-up),gx1,max(max(0,gy0-up)+1,gy1-up))
        targets.append(render_box)
        allowed.append((min(cx0,render_box[0]),min(cy0,render_box[1]),
                        max(cx1,render_box[2]),max(cy1,render_box[3])))
    # Restore precisely the repeated caption lettering from the pristine
    # source, after any title-ink overlap. The canonical source is used only
    # for *the unchanged caption*, never as a spliced name or a font.
    # This step is repeated after drawing new names, ensuring caption priority.
    return out, targets, allowed, tagboxes, canonical


def _ms2_restore_taglines(out,src,tagboxes,canonical):
    out=out.copy()
    cp=canonical.load()
    for box in tagboxes:
        x0,y0,x1,y1=box
        op=out.load(); original=src.load()
        for dy in range(40):
            for dx in range(194):
                rgba=cp[dx,dy]
                x=x0+dx;y=y0+dy
                if rgba[3]>0:
                    native=original[x,y]
                    # Original tag glyphs vary by only a few alpha values.
                    # If the old high-opacity name overlaps the caption,
                    # use the intact canonical pixel instead.
                    op[x,y]=native if 0<native[3]<=31 else rgba
                else:
                    # The caption art has no pixel here. Nothing to restore;
                    # any old localized title was already removed above.
                    pass
    return out


def _render_ms2_source_select(src,spec,get_preset,font_loader,assert_diff):
    clean,targets,allowed,tagboxes,canonical=_ms2_title_cleanup(src,spec)
    out=clean
    f=max(4,int(spec.get('supersample',4)))
    for row,box in zip(spec['rows'],targets):
        x0,y0,x1,y1=box
        preset=get_preset(row.get('typography_preset',spec['typography_preset']))
        layer=render_label((x1-x0,y1-y0),row['text'],preset,font_loader,
                           factor=f,accents=float(row.get('distress_strength',spec.get('distress_strength',0))))
        out.alpha_composite(layer,(x0,y0))
    out=_ms2_restore_taglines(out,src,tagboxes,canonical)
    allowed+=tagboxes
    assert_diff(src,out,allowed,spec['id'])
    return out


def _render_source_select(original,spec,get_preset,font_loader,assert_diff):
    """Replace localized title ink only; preserve original subtitle verbatim."""
    src=original.convert('RGBA')
    if spec.get('id')=='MS2_BONUS_CHARACTER_SELECT_NAMES':
        return _render_ms2_source_select(src,spec,get_preset,font_loader,assert_diff)
    out=src.copy()
    f=max(4,int(spec.get('supersample',4)))
    bounds=[]
    is_ms2=(spec.get('id')=='MS2_BONUS_CHARACTER_SELECT_NAMES')
    # First read every original name bounding box BEFORE modifying pixels.
    for row in spec['rows']:
        bb=_source_title_bounds(src,row['rect'])
        bounds.append((row,bb))
    allow=[]
    draw_rects=[]
    for row,(x0,y0,x1,y1) in bounds:
        # For the still-open MS2 sheet, keep farther away from the subtitle line:
        # less downward expansion, and later a slight upward render bias.
        if is_ms2:
            gx0=max(0,x0-6); gy0=max(0,y0-4)
            gx1=min(src.width,x1+6); gy1=min(src.height,max(y0+1,y1))
        else:
            gx0=max(0,x0-6); gy0=max(0,y0-4)
            gx1=min(src.width,x1+6); gy1=min(src.height,y1+4)
        target=(gx0,gy0,gx1,gy1)
        # V13 drew each MS2 name higher than its cleanup rectangle, but still
        # declared only the cleanup rectangle to assert_diff. The result was a
        # false out-of-bounds QA failure (58 pixels in the user runtime).
        # Record BOTH the cleanup rectangle and actual positioned render rect.
        up=0
        if is_ms2:
            up=2 if row['text']=='KADOWAKI\nTOWA' else 8
        draw_target=(gx0, max(0,gy0-up), gx1, max(max(0,gy0-up)+1, gy1-up))
        draw_rects.append(draw_target)
        allow.append((min(gx0,draw_target[0]), min(gy0,draw_target[1]),
                      max(gx1,draw_target[2]), max(gy1,draw_target[3])))
        # Remove the old localized title by clearing a tight rectangle around
        # the ORIGINAL source title bounds. The earlier source-pixel mask still
        # left speckled remnants; the V15 broad cleanup created debris. A small
        # grown rectangle is more decisive while still staying safely above the
        # preserved subtitle line.
        patch=out.crop(target)
        w,h=patch.size
        lx0=max(0, (x0-gx0) - 2)
        ly0=max(0, (y0-gy0) - 2)
        lx1=min(w, (x1-gx0) + 2)
        ly1=min(h, (y1-gy0) + 2)
        px=patch.load()
        for yy in range(ly0, ly1):
            for xx in range(lx0, lx1):
                px[xx,yy]=(0,0,0,0)
        out.paste(patch,(gx0,gy0))
    for row,target in zip(spec['rows'],draw_rects):
        x0,y0,x1,y1=target
        preset=get_preset(row.get('typography_preset',spec['typography_preset']))
        layer=render_label((x1-x0,y1-y0),row['text'],preset,font_loader,
                           factor=f,accents=float(row.get('distress_strength',spec.get('distress_strength',0))))
        out.alpha_composite(layer,(x0,y0))
    assert_diff(src,out,allow,spec['id'])
    return out


def render_family(original, spec, get_preset, font_loader, assert_diff, bleed):
    if spec.get('r32c3_preserve_source_subtitles'):
        return _render_source_select(original,spec,get_preset,font_loader,assert_diff)
    src=original.convert('RGBA'); out=src.copy()
    f=max(4,int(spec.get('supersample',4)))
    rows=spec.get('rows',[])
    if not rows:raise ValueError(f'{spec["id"]}: missing full-family rows')
    # Clear all cells BEFORE rendering anything. Overlapping bleed/margins on a
    # later row can otherwise erase the previous row's freshly drawn glyphs.
    allow=[]
    for row in rows:
        x0,y0,x1,y1=map(int,row['rect'])
        if not (0<=x0<x1<=src.width and 0<=y0<y1<=src.height):
            raise ValueError(f'{spec["id"]}: invalid tile {row}')
        cx0,cy0,cx1,cy1=map(int,row.get('clear_rect',row['rect']))
        if not (0<=cx0<cx1<=src.width and 0<=cy0<cy1<=src.height):
            raise ValueError(f'{spec["id"]}: invalid full clear rectangle {row}')
        allow.append((cx0,cy0,cx1,cy1))
        if row.get('clear_rect') and spec['id'] in ('MS2_BONUS_CHARACTER_SELECT_NAMES','MSF_EN_BONUS_CHARACTER_SELECT_NAMES'):
            # Clear baked bright old name/tagline pixels only. Keep dark background
            # artwork that intersects certain source slots (e.g. Towa panel).
            patch=out.crop((cx0,cy0,cx1,cy1))
            px=patch.load()
            for yy in range(patch.height):
                for xx in range(patch.width):
                    rr,gg,bb,aa=px[xx,yy]
                    hi=max(rr,gg,bb); lo=min(rr,gg,bb)
                    if aa and hi>=28 and ((hi-lo)<56 or hi>=140):
                        px[xx,yy]=(0,0,0,0)
            out.paste(patch,(cx0,cy0))
        else:
            out.paste(Image.new('RGBA',(cx1-cx0,cy1-cy0),(0,0,0,0)),(cx0,cy0))
    for row in rows:
        x0,y0,x1,y1=map(int,row['rect'])
        preset=get_preset(row.get('typography_preset',spec['typography_preset']))
        rotation=int(row.get('output_rotation',spec.get('output_rotation',0)))%360
        if rotation not in (0,90,180,270):
            raise ValueError('Non-quadrant rotation must be in the supersampled preset')
        final_size=(x1-x0,y1-y0)
        base_size=final_size[::-1] if rotation in (90,270) else final_size
        layer=render_label(base_size,row['text'],preset,font_loader,factor=f,
                           accents=float(row.get('distress_strength',spec.get('distress_strength',0.0))))
        if rotation:
            layer=layer.rotate(rotation,expand=True)
        if layer.size!=final_size:raise ValueError(f'R32C3 rotation/slot geometry mismatch {row}')
        out.alpha_composite(layer,(x0,y0))
    assert_diff(src,out,allow,spec['id'])
    return out


def render_banners(images, spec, clean_base, get_preset, font_loader, bleed):
    out={}; preset=get_preset(spec['typography_preset']); f=max(4,int(spec.get('supersample',4)))
    keys=set(images)
    expected={i['inner_name'] for i in spec['replacements']}
    if keys!=expected:raise ValueError(f'R32C3 banner family must have all {len(expected)} members; got {len(keys)}')
    for item in spec['replacements']:
        original=images[item['inner_name']]
        canvas=clean_base(original.size)
        # Source stripes must not retain guide strokes beyond their polygon.
        # Mask the whole recreated backdrop to the intended banner silhouette.
        from PIL import ImageDraw, ImageChops
        mask=Image.new('L',original.size,0)
        ImageDraw.Draw(mask).polygon([(5,40),(630,16),(605,146),(5,166)],fill=255)
        canvas.putalpha(ImageChops.multiply(canvas.getchannel('A'),mask))
        for part in item['parts']:
            x0,y0,x1,y1=map(int,part['rect'])
            p=dict(preset)
            p['nominal_size']=max(int(p.get('min_size',24)),int(part.get('height',y1-y0)*1.25))
            if item['inner_name']=='HeroineRoom_title11.tid':
                if part['text']=='TSUU AND': p['nominal_size']=56
                elif part['text']=="NINGYOHIME'S": p['nominal_size']=85
            p['r32c3_inner_width']=int(spec.get('inner_black_outline',5))
            p['r32c3_outer_width']=int(spec.get('outer_pink_outline',5))
            p['r32c3_inner_rgba']=[3,0,3,255]
            p['r32c3_outer_rgba']=[214,12,93,255]
            if part['text'] != 'ROOM':
                p['fill_gradient_top_rgba']=[255,253,255,255]
                p['fill_gradient_bottom_rgba']=[244,120,192,255]
            lab=render_label((x1-x0,y1-y0),part['text'],p,font_loader,factor=f)
            canvas.alpha_composite(lab,(x0,y0))
        out[item['inner_name']]=bleed(canvas,8)
    return out


def render_battle(original,spec,background_fn,get_preset,font_loader,assert_diff):
    src=original.convert('RGBA');out=src.copy()
    bg=background_fn(src,spec)
    x0,y0,x1,y1=map(int,spec.get('text_box',[0,28,400,148]))
    preset=get_preset(spec['typography_preset']);f=max(4,int(spec.get('supersample',4)))
    allow=[]
    for item in spec['replacements']:
        a,b,c,d=map(int,item['rect']);allow.append((a,b,c,d))
        if (c-a,d-b)!=bg.size:raise ValueError(f'{spec["id"]}: battle source slot size mismatch')
        out.paste(bg,(a,b))
        lab=render_label((x1-x0,y1-y0),item['to'],preset,font_loader,factor=f,
                         accents=float(spec.get('distress_strength',0)))
        out.alpha_composite(lab,(a+x0,b+y0))
    assert_diff(src,out,allow,spec['id'])
    return out
