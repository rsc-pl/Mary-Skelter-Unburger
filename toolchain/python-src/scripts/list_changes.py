#!/usr/bin/env python3
from pathlib import Path
import json
root=Path(__file__).resolve().parents[3]
for path in sorted((root/'translations').glob('*.jsonl')):
    print(f'## {path.name}'); n=0
    with path.open(encoding='utf-8') as f:
        for line in f:
            if not line.strip(): continue
            o=json.loads(line)
            for fld in o['fields']:
                if fld['EN'] != fld['LOCALIZED']:
                    n+=1
                    print(json.dumps({'id':o['id'],'kind':fld['kind'],'JP':fld['JP'],'LOCALIZED':fld['LOCALIZED'],'EN':fld['EN'],'record_key':fld['localized_record_key']},ensure_ascii=False))
    print(f'# changes={n}\n')
