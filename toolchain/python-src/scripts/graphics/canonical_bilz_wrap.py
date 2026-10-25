#!/usr/bin/env python3
from __future__ import annotations
import sys
from pathlib import Path
ROOT=Path(__file__).resolve().parents[4]
sys.path.insert(0,str(ROOT/'toolchain'/'python-src'/'toolchain'/'tools'))
from mskit import bilz

def main():
    if len(sys.argv)!=3:
        raise SystemExit('usage: canonical_bilz_wrap.py ALGO LEVEL')
    algo=int(sys.argv[1]); level=int(sys.argv[2])
    data=sys.stdin.buffer.read()
    sys.stdout.buffer.write(bilz.wrap(data,algo,level))
if __name__=='__main__': main()
