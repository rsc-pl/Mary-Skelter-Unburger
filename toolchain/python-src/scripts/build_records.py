#!/usr/bin/env python3
from pathlib import Path
import json
root=Path(__file__).resolve().parents[3]
rawdir=root/'.local'/'build'/'source_records'; outdir=root/'.local'/'build'/'records'; outdir.mkdir(parents=True,exist_ok=True)
if not rawdir.is_dir(): raise SystemExit('missing local source records; run scripts/prepare_source_records.sh')
edits={}
for path in sorted((root/'translations').glob('*.jsonl')):
    with path.open(encoding='utf-8') as f:
        for line in f:
            if not line.strip(): continue
            o=json.loads(line)
            for fld in o.get('fields',[]):
                ds=fld['target_dataset']; k=fld['localized_record_key']
                if k in edits: raise SystemExit(f'duplicate working target key: {k}')
                edits[k]={'dataset':ds,'localized':fld['LOCALIZED'],'en':fld['EN'],'working_id':o['id'],'kind':fld['kind']}
summary={}
for raw in sorted(rawdir.glob('*.jsonl')):
    ds=raw.stem; out=outdir/raw.name; found=set(); changed=[]; total=0
    with raw.open('r',encoding='utf-8') as fi, out.open('w',encoding='utf-8') as fo:
        for line in fi:
            if not line.strip(): continue
            total+=1; obj=json.loads(line); k=obj['record_key']; e=edits.get(k)
            if e and e['dataset']==ds:
                found.add(k)
                if obj['text'] != e['localized']:
                    raise SystemExit(f'LOCALIZED drift for {k}: raw={obj["text"]!r} working={e["localized"]!r}')
                if e['en'] != e['localized']:
                    obj['text']=e['en']
                    fo.write(json.dumps(obj,ensure_ascii=False,sort_keys=True)+'\n')
                    changed.append({'record_key':k,'working_id':e['working_id'],'kind':e['kind'],'from':e['localized'],'to':e['en']})
                    continue
            fo.write(line)
    expected={k for k,v in edits.items() if v['dataset']==ds}
    missing=expected-found
    if missing: raise SystemExit(f'{ds}: {len(missing)} working keys absent from raw target; first={next(iter(missing))}')
    summary[ds]={'raw_records':total,'mapped_working_fields':len(found),'changed_fields':len(changed),'changes':changed}
(root/'.local'/'build'/'change_manifest.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
for ds,v in summary.items(): print(f'{ds}: mapped={v["mapped_working_fields"]} changed={v["changed_fields"]}')
