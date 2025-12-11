#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CFG="$ROOT/.local/source_paths.sh"
[[ -f "$CFG" ]] || { echo "Run scripts/configure_sources.sh first." >&2; exit 1; }
# shellcheck disable=SC1090
source "$CFG"
TOOL="$ROOT/toolchain/tools/ms_text_tool"
OUT="$ROOT/.local/fresh_exports"
mkdir -p "$OUT"
python3 "$TOOL" extract --dataset MS2_PC_LOCALIZED --master "$MS2_PC_MASTER" --out "$OUT/MS2_PC_LOCALIZED.jsonl"
python3 "$TOOL" extract --dataset MSF_PC_LOCALIZED --master "$MSF_PC_MASTER" --out "$OUT/MSF_PC_LOCALIZED.jsonl"
python3 "$TOOL" extract --dataset MSF_PC_SYSTEM --master "$MSF_PC_MASTER" --out "$OUT/MSF_PC_SYSTEM.jsonl"
if [[ -n "${MS2_SWITCH_MASTER:-}" ]]; then
  python3 "$TOOL" extract --dataset MS2_SWITCH_JP --master "$MS2_SWITCH_MASTER" --out "$OUT/MS2_SWITCH_SOURCE.jsonl"
fi
echo "Fresh exports written under .local/fresh_exports/."
