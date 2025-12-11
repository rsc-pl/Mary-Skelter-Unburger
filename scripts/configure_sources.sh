#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if (( $# < 2 )); then
  echo "Usage: bash scripts/configure_sources.sh <MS2 game directory> <MSF game directory> [MS2 Switch masterNX64]" >&2
  exit 2
fi
MS2_GAME="$(realpath "$1")"
MSF_GAME="$(realpath "$2")"
[[ -d "$MS2_GAME/masterWindows" ]] || { echo "ERROR: missing $MS2_GAME/masterWindows" >&2; exit 1; }
[[ -d "$MSF_GAME/masterWindows" ]] || { echo "ERROR: missing $MSF_GAME/masterWindows" >&2; exit 1; }
[[ -f "$MS2_GAME/MarySkelter2.exe" ]] || { echo "ERROR: missing $MS2_GAME/MarySkelter2.exe" >&2; exit 1; }
mkdir -p "$ROOT/.local"
{
  printf 'export MS2_PC_GAME=%q\n' "$MS2_GAME"
  printf 'export MSF_PC_GAME=%q\n' "$MSF_GAME"
  printf 'export MS2_PC_MASTER=%q\n' "$MS2_GAME/masterWindows"
  printf 'export MSF_PC_MASTER=%q\n' "$MSF_GAME/masterWindows"
  printf 'export MS2_PC_EXE=%q\n' "$MS2_GAME/MarySkelter2.exe"
  if [[ -f "$MSF_GAME/MarySkelterFinale.exe" ]]; then
    printf 'export MSF_PC_EXE=%q\n' "$MSF_GAME/MarySkelterFinale.exe"
  fi
  if (( $# >= 3 )); then
    [[ -d "$3" ]] || { echo "ERROR: Switch source directory not found: $3" >&2; exit 1; }
    printf 'export MS2_SWITCH_MASTER=%q\n' "$(realpath "$3")"
  fi
} > "$ROOT/.local/source_paths.sh"
echo "Wrote .local/source_paths.sh"
