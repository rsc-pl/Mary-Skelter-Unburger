#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CFG="$ROOT/.local/source_paths.sh"
if [[ ! -f "$CFG" ]]; then
  echo "Missing .local/source_paths.sh." >&2
  echo "Run: bash scripts/configure_sources.sh <MS2 game directory> <MSF game directory>" >&2
  exit 1
fi
# shellcheck disable=SC1090
source "$CFG"
: "${MS2_PC_GAME:?MS2_PC_GAME is not configured}"
: "${MSF_PC_GAME:?MSF_PC_GAME is not configured}"
: "${MS2_PC_MASTER:?MS2_PC_MASTER is not configured}"
: "${MSF_PC_MASTER:?MSF_PC_MASTER is not configured}"

python3 "$ROOT/scripts/validate_translations.py"
# Build intermediates are disposable.  The last successful .local/dist is NOT:
# keep it intact until the new rebuild + verification + bundle staging all pass.
rm -rf "$ROOT/.local/build/records" "$ROOT/.local/build/rebuilt" \
       "$ROOT/.local/build/reports" "$ROOT/.local/build/dist.staging"
mkdir -p "$ROOT/.local/build/rebuilt" "$ROOT/.local/build/reports"
python3 "$ROOT/scripts/build_records.py"
MANIFEST="$ROOT/.local/build/change_manifest.json"
mapfile -t CHANGED < <(python3 "$ROOT/scripts/changed_datasets.py" "$MANIFEST")
if (( ${#CHANGED[@]} > 0 )); then
  python3 "$ROOT/scripts/check_sources.py" \
    --manifest "$ROOT/.local/manifests/expected_pc_archive_sha256.tsv" \
    --ms2 "$MS2_PC_MASTER" --msf "$MSF_PC_MASTER" \
    --change-manifest "$MANIFEST"
fi

TOOL="$ROOT/toolchain/tools/ms_text_tool"
for ds in "${CHANGED[@]}"; do
  case "$ds" in
    MS2_PC_LOCALIZED) MASTER="$MS2_PC_MASTER" ;;
    MSF_PC_LOCALIZED|MSF_PC_SYSTEM) MASTER="$MSF_PC_MASTER" ;;
    *) echo "Unsupported target dataset $ds" >&2; exit 1 ;;
  esac
  echo "=== REBUILD $ds ==="
  python3 "$TOOL" rebuild --dataset "$ds" \
    --records "$ROOT/.local/build/records/$ds.jsonl" --master "$MASTER" \
    --out "$ROOT/.local/build/rebuilt/$ds" --report "$ROOT/.local/build/reports/$ds.rebuild.json"

  mapfile -t ARCHIVES < <(python3 "$ROOT/scripts/report_archives.py" "$ROOT/.local/build/reports/$ds.rebuild.json")
  if (( ${#ARCHIVES[@]} == 0 )); then
    echo "ERROR: rebuild of $ds reported no output archives" >&2
    exit 1
  fi

  REBUILT="$ROOT/.local/build/rebuilt/$ds/masterWindows"
  # Verify only the archives that were rebuilt.  Historical raw targets for
  # other archives were extracted before ENC field ordinals became unique and
  # cannot be compared by their legacy record_key.  mskit.verify itself uses a
  # stable logical locator so legal STCM relocations also do not look like
  # missing/extra records.
  python3 "$TOOL" verify --dataset "$ds" --rebuilt "$REBUILT" \
    --source-master "$MASTER" --expected-records "$ROOT/.local/build/records/$ds.jsonl" \
    --archives "${ARCHIVES[@]}" \
    --report "$ROOT/.local/build/reports/$ds.verify.json"

  python3 "$TOOL" diff --dataset "$ds" --rebuilt "$REBUILT" \
    --source-master "$MASTER" --archives "${ARCHIVES[@]}" \
    --report "$ROOT/.local/build/reports/$ds.diff.json"
done

# Build complete deliverables in staging.  A failed rebuild/verify/QoL step no
# longer wipes the last known-good patch under .local/dist.
DIST_STAGE="$ROOT/.local/build/dist.staging"
rm -rf "$DIST_STAGE"
mkdir -p "$DIST_STAGE"
export MARY_SKELTER_DIST="$DIST_STAGE"
python3 "$ROOT/scripts/make_patch_bundle.py"
bash "$ROOT/scripts/build_ms2_qol.sh"
unset MARY_SKELTER_DIST

DIST="$ROOT/.local/dist"
DIST_OLD="$ROOT/.local/build/dist.previous"
rm -rf "$DIST_OLD"
if [[ -d "$DIST" ]]; then
  mv "$DIST" "$DIST_OLD"
fi
if mv "$DIST_STAGE" "$DIST"; then
  rm -rf "$DIST_OLD"
else
  rc=$?
  rm -rf "$DIST"
  if [[ -d "$DIST_OLD" ]]; then mv "$DIST_OLD" "$DIST"; fi
  exit "$rc"
fi

printf 'DONE. Verified patch bundles:\n  %s\n  %s\n' \
  "$DIST/MS2_EN_FIDELITY_PATCH" \
  "$DIST/MSF_EN_FIDELITY_PATCH"
