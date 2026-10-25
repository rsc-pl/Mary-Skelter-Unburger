#!/usr/bin/env python3
from pathlib import Path
import json,sys,re
root=Path(__file__).resolve().parents[3]
errors=[]; stats={}
PUBLIC_ROW_KEYS={'id','game','scope','source','fields'}
PUBLIC_FIELD_KEYS={'kind','JP','LOCALIZED','EN','field_ordinal','target_dataset','localized_record_key','jp_record_key','mapping_status'}
FORBIDDEN_WORKFLOW_KEYS={'review_status','row_review_status','status','notes','review','decision','batch','checkpoint','review_batch','review_notes'}
WORKFLOW_MARKER_RE=re.compile(r'(?:^|_)(?:REVIEW|REVIEWED|REUSE|BATCH|CHECKPOINT|OPEN_HOLD|KEEP|APPLY|CHANGED)(?:$|_)', re.I)

def scan_workflow_metadata(obj,path=''):
    if isinstance(obj,dict):
        for k,v in obj.items():
            kp=f'{path}.{k}' if path else k
            if k.lower() in FORBIDDEN_WORKFLOW_KEYS:
                yield f'forbidden workflow key {kp!r}'
            if isinstance(v,str) and k not in {'JP','LOCALIZED','EN'} and WORKFLOW_MARKER_RE.search(v):
                yield f'workflow marker in metadata {kp}: {v!r}'
            yield from scan_workflow_metadata(v,kp)
    elif isinstance(obj,list):
        for i,v in enumerate(obj):
            yield from scan_workflow_metadata(v,f'{path}[{i}]')
for path in sorted((root/'translations').glob('*.jsonl')):
    n=flds=exact=pending=source_absent=changed=0; keys=set()
    with path.open(encoding='utf-8') as f:
        for ln,line in enumerate(f,1):
            if not line.strip(): continue
            n+=1; o=json.loads(line)
            # Public translation corpora are release data, not review ledgers.
            extra_row=set(o)-PUBLIC_ROW_KEYS
            if extra_row:
                errors.append(f'{path.name}:{ln}: unexpected public row keys: {sorted(extra_row)}')
            if 'DLC' in path.name.upper() or 'DLC' in str(o.get('id','')).upper() or 'DLC' in str(o.get('scope','')).upper():
                errors.append(f'{path.name}:{ln}: public corpus identity must describe the physical archive/surface, not workflow/DLC grouping')
            for msg in scan_workflow_metadata(o):
                errors.append(f'{path.name}:{ln}: {msg}')
            for fld in o['fields']:
                flds+=1; k=fld['localized_record_key']
                extra_field=set(fld)-PUBLIC_FIELD_KEYS
                if extra_field:
                    errors.append(f'{path.name}:{ln}: unexpected public field keys: {sorted(extra_field)}')
                if k in keys: errors.append(f'{path.name}:{ln}: duplicate target key {k}')
                keys.add(k)
                if fld['mapping_status'].startswith('EXACT'):
                    exact+=1
                elif fld['mapping_status'] == 'SOURCE_ABSENT_IN_PC_JP_RESOURCE':
                    source_absent+=1
                else:
                    pending+=1
                if fld['EN'] != fld['LOCALIZED']: changed+=1
                en=fld.get('EN') or ''
                if re.search(r"\b[A-Za-z][A-Za-z'’.-]*[A-Za-z]-(?:onee|onii|nee|nii)-(?:san|chan|sama)\b", en, re.I):
                    errors.append(f'{path.name}:{ln}: named kinship suffix must use one separator only (e.g. Name-neesama): {en!r}')
                if re.search(r'\b(?:otou|okaa|imouto|otouto|obaa|ojii|oji|oba)-(?:san|chan|sama)\b', en, re.I):
                    errors.append(f'{path.name}:{ln}: ordinary family term must be translated naturally, not romanized: {en!r}')
                if fld['JP'] is None and fld['mapping_status'].startswith('EXACT'):
                    errors.append(f'{path.name}:{ln}: exact mapping with JP=null')
                if fld['JP'] is not None and fld['mapping_status'] == 'SOURCE_ABSENT_IN_PC_JP_RESOURCE':
                    errors.append(f'{path.name}:{ln}: source-absent mapping with non-null JP')
    stats[path.name]={'records':n,'fields':flds,'exact_jp_fields':exact,'source_absent_jp_fields':source_absent,'pending_jp_fields':pending,'changed_EN_fields':changed}
print(json.dumps(stats,ensure_ascii=False,indent=2))
if errors:
    print('\n'.join(errors[:100]),file=sys.stderr); print(f'errors={len(errors)}',file=sys.stderr); sys.exit(1)
