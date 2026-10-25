#!/usr/bin/env python3
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[4]


def install_block(path: Path) -> str:
    text = path.read_text(encoding='utf-8')
    m = re.search(r"cat > \"\$INSTALL\" <<'EOF'\n(.*?)\nEOF\n", text, re.S)
    if not m:
        raise SystemExit(f'missing final INSTALL.txt heredoc in {path.relative_to(ROOT)}')
    return m.group(1)


def require(block: str, needle: str, label: str) -> None:
    if needle not in block:
        raise SystemExit(f'{label}: INSTALL.txt contract missing {needle!r}')

ms2 = install_block(ROOT / 'toolchain/python-src/scripts/build_ms2_qol.sh')
msf = install_block(ROOT / 'toolchain/python-src/scripts/build_finale_qol.sh')

for label, block, exe in (
    ('MS2', ms2, 'MarySkelter2.exe'),
    ('Finale', msf, 'MarySkelterFinale.exe'),
):
    require(block, 'Back up the original game files first.', label)
    require(block, exe, label)
    require(block, 'masterWindows/', label)
    require(block, "game's\n   masterWindows directory", label)
    require(block, 'Only DATs changed by the verified rebuild are included.', label)

require(msf, 'PATCH_MANIFEST.json', 'Finale')
print('patch bundle INSTALL contract: PASS')
print('  MS2: EXE + masterWindows DATs')
print('  Finale: EXE + masterWindows DATs')
