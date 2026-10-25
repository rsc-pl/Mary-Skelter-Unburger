#!/usr/bin/env python3
"""Maintainer-only offline vendoring of public CPython, Pillow and NumPy.

Inputs must already be downloaded and their SHA-256 independently pinned.
No network, no pip installation at game build time, no proprietary fonts.
Outputs are public runtime dirs under toolchain/python-runtime/<platform>.
"""
from __future__ import annotations
import argparse, hashlib, io, json, os, pathlib, posixpath, shutil, tarfile, tempfile, zipfile

ALLOWED = {'linux-amd64': 'x86_64', 'windows-amd64': 'win_amd64'}
PYTHON = '3.13.15'
PILLOW = '12.3.0'
NUMPY = '2.3.5'

def sha(path):
    h=hashlib.sha256()
    with open(path,'rb') as f:
        for block in iter(lambda:f.read(1024*1024),b''): h.update(block)
    return h.hexdigest()

def safe_relative(name):
    p=pathlib.PurePosixPath(name)
    if not name or p.is_absolute() or '..' in p.parts or '\\' in name or name.startswith('/') or '\x00' in name:
        raise ValueError(f'unsafe archive member {name!r}')
    return p

def extract_portable_python(archive, target):
    with tarfile.open(archive,'r:*') as tf:
        members=tf.getmembers()
        if not members: raise ValueError('empty Python install_only archive')
        delayed=[]
        for member in members:
            rel=safe_relative(member.name)
            if not rel.parts or rel.parts[0]!='python': raise ValueError('not an install_only Python archive')
            if member.issym() or member.islnk():
                delayed.append(member)
                continue
            if member.isdir():
                (target/rel).mkdir(parents=True,exist_ok=True);continue
            if not member.isfile(): raise ValueError(f'unsupported archive type: {member.name}')
            dest=target/rel;dest.parent.mkdir(parents=True,exist_ok=True)
            if dest.exists():raise ValueError(f'duplicate Python archive path: {member.name}')
            obj=tf.extractfile(member)
            with dest.open('wb') as out: shutil.copyfileobj(obj,out)
            dest.chmod(member.mode & 0o755)
        # Safe, deterministic materialization of upstream internal symbolic/hard links.
        # The public manifest contains ordinary files only (never symlinks).
        for _ in range(len(delayed)+1):
            remaining=[]
            for member in delayed:
                if member.issym():
                    target_rel=posixpath.normpath(posixpath.join(posixpath.dirname(member.name),member.linkname))
                else:
                    target_rel=posixpath.normpath(member.linkname)
                target_parts=safe_relative(target_rel)
                if target_parts.parts[0]!='python':raise ValueError(f'link escapes runtime: {member.name}')
                source=target/target_parts
                if not source.is_file():
                    remaining.append(member)
                    continue
                dest=target/safe_relative(member.name)
                if dest.exists():raise ValueError(f'linked path collision: {member.name}')
                dest.parent.mkdir(parents=True,exist_ok=True)
                shutil.copyfile(source,dest)
                dest.chmod(source.stat().st_mode & 0o755)
            if not remaining:break
            if len(remaining)==len(delayed):raise ValueError('unresolved/cyclic runtime link')
            delayed=remaining

def install_wheel(path, output, package, version, platform):
    fn=path.name.lower()
    if not (fn.startswith(package+'-'+version+'-') and fn.endswith('.whl')): raise ValueError(f'unexpected wheel {path.name}')
    if 'cp313' not in fn or ALLOWED[platform] not in fn: raise ValueError(f'wrong Python ABI/platform: {fn}')
    with zipfile.ZipFile(path) as z:
        for member in z.infolist():
            rel=safe_relative(member.filename)
            if member.is_dir(): continue
            # PEP 427 .data/{purelib,platlib} contents are importable on PYTHONPATH.
            if len(rel.parts)>=3 and rel.parts[0].endswith('.data'):
                if rel.parts[1] not in {'purelib','platlib'}:
                    raise ValueError(f'unreviewed wheel data location: {member.filename}')
                rel=pathlib.PurePosixPath(*rel.parts[2:])
            out=output/rel;out.parent.mkdir(parents=True,exist_ok=True)
            if out.exists():raise ValueError(f'wheel collision: {member.filename}')
            with z.open(member) as src, out.open('wb') as dst:shutil.copyfileobj(src,dst)

def assemble(args):
    platform=args.platform
    out=args.repo/'toolchain'/'python-runtime'/platform
    if out.exists():raise ValueError(f'output exists (no clobber): {out}')
    names={'python':args.python_archive,'pillow':args.pillow_wheel,'numpy':args.numpy_wheel}
    pins=json.loads(args.inputs_lock.read_text(encoding='utf8'))
    if pins.get('schema')!=1 or pins.get('platform')!=platform:
        raise ValueError('input lock schema/platform mismatch')
    for key,src in names.items():
        want=pins.get('sha256',{}).get(key)
        if not want or len(want)!=64 or sha(src)!=want:raise ValueError(f'{key}: missing/incorrect pinned SHA-256')
    with tempfile.TemporaryDirectory(prefix='ms-python-vendor-') as t:
        staging=pathlib.Path(t)
        extract_portable_python(args.python_archive,staging)
        sp=staging/'site-packages';sp.mkdir()
        install_wheel(args.pillow_wheel,sp,'pillow',PILLOW,platform)
        install_wheel(args.numpy_wheel,sp,'numpy',NUMPY,platform)
        exe='python/bin/python3' if platform=='linux-amd64' else 'python/python.exe'
        if not (staging/exe).is_file():raise ValueError(f'embedded interpreter missing: {exe}')
        files={}
        for f in sorted(staging.rglob('*')):
            if f.is_symlink():raise ValueError(f'unexpected symlink: {f}')
            if f.is_file():files[f.relative_to(staging).as_posix()]=sha(f)
        if len(files)<3:raise ValueError('incomplete runtime contents')
        m={'schema':1,'platform':platform,'python':exe,'python_version':PYTHON,
           'pillow_version':PILLOW,'numpy_version':NUMPY,'files':files,'input_sha256':pins['sha256']}
        (staging/'manifest.json').write_text(json.dumps(m,indent=2,sort_keys=True)+'\n',encoding='utf8')
        out.parent.mkdir(parents=True,exist_ok=True)
        shutil.move(str(staging),str(out))
        print(f'Vendored {platform}: {len(files)} SHA-pinned files -> {out}')

if __name__=='__main__':
    ap=argparse.ArgumentParser()
    ap.add_argument('--platform',choices=sorted(ALLOWED),required=True)
    ap.add_argument('--repo',type=pathlib.Path,required=True)
    ap.add_argument('--python-archive',type=pathlib.Path,required=True)
    ap.add_argument('--pillow-wheel',type=pathlib.Path,required=True)
    ap.add_argument('--numpy-wheel',type=pathlib.Path,required=True)
    ap.add_argument('--inputs-lock',type=pathlib.Path,required=True)
    assemble(ap.parse_args())
