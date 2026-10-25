#!/usr/bin/env python3
import json,sys
x=json.load(open(sys.argv[1]))
for k,v in x.items():
    if v.get('changed_fields',0)>0: print(k)
