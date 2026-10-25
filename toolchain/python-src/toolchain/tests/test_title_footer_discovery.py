#!/usr/bin/env python3
from __future__ import annotations
import importlib.util
import io
import struct
import tempfile
from pathlib import Path
from PIL import Image

ROOT = Path(__file__).resolve().parents[4]
spec = importlib.util.spec_from_file_location('footer_discovery', ROOT/'toolchain'/'python-src'/'scripts'/'discover_title_footer_source.py')
mod = importlib.util.module_from_spec(spec); assert spec.loader; spec.loader.exec_module(mod)
from mskit.gdat import build as build_gdat


def make_cl3(files):
    sec_off=0x18; sec_count=2; coll_off=sec_off+sec_count*0x50
    table_size=len(files)*0x230; payloads=b''.join(data for _,data in files); coll_size=table_size+len(payloads)
    out=bytearray(coll_off+coll_size); out[:4]=b'CL3L'
    struct.pack_into('<IIIII',out,4,0,3,sec_count,sec_off,0)
    def sec(i,name,count,size,off):
        base=sec_off+i*0x50; nb=name.encode()+b'\0'; out[base:base+0x20]=nb+b'\0'*(0x20-len(nb)); struct.pack_into('<12I',out,base+0x20,count,size,off,*([0]*9))
    sec(0,'FILE_COLLECTION',len(files),coll_size,coll_off); sec(1,'FILE_LINK',0,0,coll_off+coll_size)
    cursor=table_size; body=bytearray()
    for i,(name,data) in enumerate(files):
        base=coll_off+i*0x230; nb=name.encode()+b'\0'; out[base:base+0x200]=nb+b'\0'*(0x200-len(nb)); struct.pack_into('<5I',out,base+0x200,0,cursor,len(data),0,0); body+=data; cursor+=len(data)
    out[coll_off+table_size:coll_off+table_size+len(body)]=body
    return bytes(out)



def make_fake_pe_with_xref(path_bytes: bytes, png: bytes) -> bytes:
    # Minimal PE32+ image for the discovery parser (not intended to execute).
    image_base=0x140000000
    pe_off=0x80; opt_size=0xF0; nsec=3
    sec_table=pe_off+4+20+opt_size
    data=bytearray(0xC00)
    data[:2]=b'MZ'; struct.pack_into('<I',data,0x3C,pe_off); data[pe_off:pe_off+4]=b'PE\0\0'
    coff=pe_off+4
    struct.pack_into('<HHIIIHH',data,coff,0x8664,nsec,0,0,0,opt_size,0x22)
    opt=coff+20; struct.pack_into('<H',data,opt,0x20B); struct.pack_into('<Q',data,opt+24,image_base)
    def sec(i,name,vsize,va,raw_size,raw_ptr,chars):
        rec=sec_table+i*40; nb=name.encode()[:8]; data[rec:rec+8]=nb+b'\0'*(8-len(nb))
        struct.pack_into('<IIIIIIHHI',data,rec+8,vsize,va,raw_size,raw_ptr,0,0,0,0,chars)
    sec(0,'.text',0x200,0x1000,0x200,0x400,0x60000020)
    sec(1,'.rdata',0x400,0x2000,0x400,0x600,0x40000040)
    sec(2,'.rsrc',0x200,0x3000,0x200,0xA00,0x40000040)
    str_off=0x620; data[str_off:str_off+len(path_bytes)]=path_bytes
    str_va=image_base+0x2000+(str_off-0x600)
    ins_off=0x420; ins_va=image_base+0x1000+(ins_off-0x400)
    disp=str_va-(ins_va+7)
    data[ins_off:ins_off+7]=b'\x48\x8d\x0d'+struct.pack('<i',disp)
    # A self-relative data-table slot resolving to the exact path.
    slot_off=0x680; slot_va=image_base+0x2000+(slot_off-0x600)
    struct.pack_into('<i', data, slot_off, str_va-(slot_va+4))
    # A separate code reference into the same nearby rdata neighborhood.
    anchor_off=0x6A0; data[anchor_off:anchor_off+12]=b'anchor_text\0'
    anchor_va=image_base+0x2000+(anchor_off-0x600)
    ins2_off=0x440; ins2_va=image_base+0x1000+(ins2_off-0x400)
    disp2=anchor_va-(ins2_va+7)
    data[ins2_off:ins2_off+7]=b'\x48\x8d\x15'+struct.pack('<i',disp2)
    png_off=0xA20; data[png_off:png_off+len(png)]=png
    return bytes(data)

with tempfile.TemporaryDirectory() as td:
    td=Path(td); master=td/'masterWindows'; master.mkdir()
    bio=io.BytesIO(); Image.new('RGBA',(320,64),(0,0,0,0)).save(bio,format='PNG'); png=bio.getvalue()
    cl3=make_cl3([('OVERLAY_EN/copyright.png',png),('unrelated.bin',b'x')])
    (master/'GxArchivedFile051.dat').write_bytes(build_gdat([(0,cl3)]))
    rep=mod.archive_overlay_scan('MSF',master,td/'out',broad=False)
    assert rep['exact_copyright_found'],rep
    hit=next(x for x in rep['matches'] if Path(x['name']).name.lower()=='copyright.png')
    assert hit['png']=={'width':320,'height':64},hit
    assert Path(hit['output']).read_bytes()==png

    exe=td/'test.exe'; exe.write_bytes(b'prefix '+b'/hostapp/OVERLAY/copyright.png'+b' xx '+png+b' suffix')
    er=mod.scan_embedded_pngs('MS2',exe,td/'exout')
    assert any('copyright.png' in x['needle'] for x in er['needle_hits'])
    assert len(er['embedded_pngs'])==1 and er['embedded_pngs'][0]['width']==320

    fake=td/'fake.exe'; fake.write_bytes(make_fake_pe_with_xref(b'/hostapp/OVERLAY/copyright.png', png))
    fr=mod.scan_embedded_pngs('MS2',fake,td/'fakeout',deep_trace=True)
    assert fr['pe']['image_base']==0x140000000,fr
    assert fr['embedded_pngs'][0]['section']=='.rsrc',fr['embedded_pngs'][0]
    assert fr['copyright_path_traces'],fr
    tr=fr['copyright_path_traces'][0]
    assert tr['section']=='.rdata' and tr['direct_xrefs'],tr
    assert tr['direct_xrefs'][0]['kind']=='lea-rip',tr
    assert any(x['encoding']=='rel32-next' for x in tr['relative_slots']),tr
    assert any(x['target_delta'] != 0 for x in tr['nearby_rip_refs']),tr
    assert any(x['text']=='anchor_text' for x in tr['nearby_strings']),tr
    assert Path(tr['deep_trace_report']).is_file(),tr

script=(ROOT/'toolchain'/'python-src'/'scripts'/'discover_title_footer_source.py').read_text(encoding='utf-8')
assert '--broad' in script
assert 'title_moji is a logo-local texture' in script
print('title footer overlay discovery tests: PASS')
