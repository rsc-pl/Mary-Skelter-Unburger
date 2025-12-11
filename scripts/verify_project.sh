#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "[1/8] Python compile"
python3 -m compileall -q toolchain/tools/mskit toolchain/ms2_qol toolchain/ms2_save scripts

echo "[2/8] Tool starts"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
python3 toolchain/tools/ms_text_tool list-datasets > "$tmp"
if grep -q 'MSF_PC_KO' "$tmp"; then
  echo "ERROR: unwanted Korean dataset is exposed" >&2
  exit 1
fi
rm -f "$tmp"
trap - EXIT

echo "[3/8] Packer shared-string regression"
python3 toolchain/tests/test_edit_shared_strings.py

echo "[4/8] STCM relayout + logical verifier regressions"
python3 toolchain/tests/test_stcm_relayout.py
python3 toolchain/tests/test_verify_logical_identity.py

echo "[5/8] Translation corpus validation"
python3 scripts/validate_translations.py

echo "[6/8] Translation -> technical-record bridge"
rm -rf .local/build/records .local/build/change_manifest.json
mkdir -p .local/build
python3 scripts/build_records.py
python3 - <<'PY'
from pathlib import Path
import json
root=Path('.')
want=json.loads((root/'.local/manifests/project_counts.json').read_text())
m=json.loads((root/'.local/build/change_manifest.json').read_text())
expected={
 'MS2_PC_LOCALIZED': want['semantic_passes']['MS2_changed_EN_fields'],
 'MSF_PC_LOCALIZED': want['semantic_passes']['MSF_changed_EN_fields'],
 'MSF_PC_SYSTEM': 0,
}
for ds,n in expected.items():
    got=m[ds]['changed_fields']
    if got!=n: raise SystemExit(f'{ds}: changed_fields {got} != expected {n}')
    src=root/'.local/rebuild/raw_targets'/f'{ds}.jsonl'
    dst=root/'.local/build/records'/f'{ds}.jsonl'
    diffs=0
    with src.open(encoding='utf-8') as a, dst.open(encoding='utf-8') as b:
        for la,lb in zip(a,b):
            if la==lb: continue
            oa=json.loads(la); ob=json.loads(lb)
            if oa['record_key']!=ob['record_key']: raise SystemExit(f'{ds}: record order/key drift')
            xa=dict(oa); xb=dict(ob); xa.pop('text',None); xb.pop('text',None)
            if xa!=xb: raise SystemExit(f'{ds}: non-text raw drift at {oa["record_key"]}')
            diffs+=1
    if diffs!=n: raise SystemExit(f'{ds}: raw text diffs {diffs} != expected {n}')
print('translation -> technical-record bridge: PASS')
PY

echo "[7/8] Canonical counts"
python3 - <<'PY'
from pathlib import Path
import json
r=Path('.')
want=json.loads((r/'.local/manifests/project_counts.json').read_text())
def cwork(p):
 rows=fields=exact=source_absent=pending=changed=0
 for line in p.open(encoding='utf-8'):
  if not line.strip(): continue
  rows+=1; o=json.loads(line)
  for f in o['fields']:
   fields+=1; st=f['mapping_status']
   if st.startswith('EXACT'): exact+=1
   elif st=='SOURCE_ABSENT_IN_PC_JP_RESOURCE': source_absent+=1
   else: pending+=1
   changed+=f['EN']!=f['LOCALIZED']
 return rows,fields,exact,source_absent,pending,changed
m2=cwork(r/'translations/MS2_STORY.jsonl')
mf=cwork(r/'translations/MSF_STORY.jsonl')
msys=cwork(r/'translations/MSF_SYSTEM_GDAT0.jsonl')
assert m2==(want['ms2_working_records'],want['ms2_working_fields'],want['ms2_working_fields'],0,0,want['semantic_passes']['MS2_changed_EN_fields']),m2
assert mf==(want['msf_working_records'],want['msf_localized_fields'],want['msf_exact_jp_fields'],want['msf_source_absent_jp_fields'],want['msf_pending_localized_fields'],want['semantic_passes']['MSF_story_changed_EN_fields']),mf
assert msys==(want['msf_system_gdat0_records'],want['msf_system_gdat0_fields'],want['msf_system_gdat0_exact_jp_fields'],0,0,want['semantic_passes']['MSF_system_gdat0_changed_EN_fields']),msys
print('counts: PASS')
PY

echo "[8/8] Public build script syntax"
bash -n build.sh scripts/rebuild_and_verify.sh scripts/build_ms2_qol.sh scripts/configure_sources.sh scripts/refresh_exports.sh

rm -rf .local/build/records .local/build/change_manifest.json
echo "VERIFY_PROJECT: ALL CHECKS PASSED"
