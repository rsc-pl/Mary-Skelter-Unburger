#!/usr/bin/env python3
from pathlib import Path
import json,sys,re
root=Path(__file__).resolve().parents[1]
errors=[]; stats={}
for path in sorted((root/'translations').glob('*.jsonl')):
    n=flds=exact=pending=source_absent=changed=0; keys=set()
    with path.open(encoding='utf-8') as f:
        for ln,line in enumerate(f,1):
            if not line.strip(): continue
            n+=1; o=json.loads(line)
            for fld in o['fields']:
                flds+=1; k=fld['localized_record_key']
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
                if re.search(r'-(?:onee|onii|nee|nii)-(?:san|chan|sama)\b', en, re.I):
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
