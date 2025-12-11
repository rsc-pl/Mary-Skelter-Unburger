#!/usr/bin/env python3
import json,sys
r=json.load(open(sys.argv[1]))
for a in r.get('archives',[]):
    print(a['archive_number'])
