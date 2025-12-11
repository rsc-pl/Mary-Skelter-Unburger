#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CFG="$ROOT/.local/source_paths.sh"
[[ -f "$CFG" ]] || { echo "Missing .local/source_paths.sh" >&2; exit 1; }
# shellcheck disable=SC1090
source "$CFG"
: "${MS2_PC_GAME:?MS2_PC_GAME is not configured}"
EXE="${MS2_PC_EXE:-$MS2_PC_GAME/MarySkelter2.exe}"
[[ -f "$EXE" ]] || { echo "ERROR: MarySkelter2.exe not found: $EXE" >&2; exit 1; }
DIST_ROOT="${MARY_SKELTER_DIST:-$ROOT/.local/dist}"
OUTDIR="$DIST_ROOT/MS2_EN_FIDELITY_PATCH"
OUT="$OUTDIR/MarySkelter2.exe"
mkdir -p "$OUTDIR"
ORIGINAL='07b97dd3b4aa9e50249a2fcfb619b31822eb7219ebeae150b9b9c421a5b5834c'
DESTUB='c24df22f43524e236925bf38ab8cf9d6171ba3518e6f1b5828bdf740d95cffbc'
FINAL='81b6641fadff2767bb23492b1c6b0a9cac645a7496c65ed9d8eef2144ca94fa8'
GOT="$(sha256sum "$EXE" | awk '{print $1}')"
case "$GOT" in
  "$FINAL")
    cp -f -- "$EXE" "$OUT"
    ;;
  "$ORIGINAL"|"$DESTUB")
    python3 "$ROOT/toolchain/ms2_qol/patch_ms2_qol.py" "$EXE" -o "$OUT"
    ;;
  *)
    echo "ERROR: unsupported MarySkelter2.exe SHA-256: $GOT" >&2
    exit 1
    ;;
esac
OUT_SHA="$(sha256sum "$OUT" | awk '{print $1}')"
[[ "$OUT_SHA" == "$FINAL" ]] || { echo "ERROR: QoL EXE verification failed: $OUT_SHA" >&2; exit 1; }

TOOLS_DIR="$OUTDIR/tools"
mkdir -p "$TOOLS_DIR"
cp -f -- "$ROOT/toolchain/ms2_save/convert_ms2_switch_to_pc.py" "$TOOLS_DIR/convert_ms2_switch_to_pc.py"

INSTALL="$OUTDIR/INSTALL.txt"
cat > "$INSTALL" <<'EOF'
Back up the original game files first.

1. Copy MarySkelter2.exe to the game root, replacing the original executable.
   This verified executable skips the startup logo movies and bypasses the
   voice-language prompt while using Japanese voices.
2. Copy files from masterWindows/ over the same names in the game's
   masterWindows directory. Only DATs changed by the verified rebuild are included.
3. tools/convert_ms2_switch_to_pc.py is an optional Switch-to-PC save converter.
   It is not run automatically. Never replace the PC SystemData_0000 files.
EOF

printf '%s\n' "MS2 QoL EXE: $OUT" "SHA-256: $OUT_SHA"
