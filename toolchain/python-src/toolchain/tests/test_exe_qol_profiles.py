#!/usr/bin/env python3
"""Regression gates for the shared MS2/Finale executable QoL architecture."""
from __future__ import annotations

import importlib.util
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[4]
TOOLCHAIN = ROOT / "toolchain" / "python-src" / "toolchain"
sys.path.insert(0, str(TOOLCHAIN))

from exe_qol.common import destub  # noqa: E402
from exe_qol.profiles import FINALE_PROFILE, MS2_PROFILE  # noqa: E402


def load_module(path: Path, name: str):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


assert MS2_PROFILE.original_sha256 == "07b97dd3b4aa9e50249a2fcfb619b31822eb7219ebeae150b9b9c421a5b5834c"
assert MS2_PROFILE.destub_sha256 == "c24df22f43524e236925bf38ab8cf9d6171ba3518e6f1b5828bdf740d95cffbc"
assert MS2_PROFILE.final_sha256 == "81b6641fadff2767bb23492b1c6b0a9cac645a7496c65ed9d8eef2144ca94fa8"
assert [(p.rva, p.expected.hex(), p.replacement.hex()) for p in MS2_PROFILE.patches] == [
    (0x25B2BB, "8b4140", "6a1b58"),
    (0x2AA27A, "488d0daf1b7100", "488d0def1a7100"),
]
assert [(p.rva, p.expected.hex(), p.replacement.hex()) for p in MS2_PROFILE.late_patches] == [
    (0xBFE0E, "488d155b696400488d0d0c5b2a01e8bf7c2800", "90" * 19),
]

assert FINALE_PROFILE.original_sha256 == "5c16abfdc3f355404c2c7faa6b277e5023812d2292cb8c5d4ad5177ba6c05a54"
assert FINALE_PROFILE.destub_sha256 == "900a2cf0485fe882285e2409b2105eda52d44063c378d4ea9e9e1db052f2fa25"
assert FINALE_PROFILE.final_sha256 == "cc0faf4df3c94e64a0084092e6afdb8c34dee9abe9f00a9f9c429cc5957c570c"
assert {(p.rva, p.expected.hex(), p.replacement.hex()) for p in FINALE_PROFILE.patches} == {
    (0x11FC81, "0c", "ff"),
    (0x29B248, "01", "1a"),
}
assert [(p.rva, p.expected.hex(), p.replacement.hex()) for p in FINALE_PROFILE.late_patches] == [
    (0x91F10, "488d15d9eb8a00488d0dc2d00f01e84d162f00", "90" * 19),
]

# Backward-compatible MS2 imports point at the one shared SteamStub implementation.
ms2 = load_module(TOOLCHAIN / "ms2_qol" / "patch_ms2_qol.py", "ms2_qol_regression")
assert ms2.destub is destub

# Public source must contain exactly one SteamStub destub implementation.
destub_defs = []
for path in TOOLCHAIN.rglob("*.py"):
    if "__pycache__" in path.parts:
        continue
    for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        if line.startswith("def destub("):
            destub_defs.append((path.relative_to(ROOT).as_posix(), lineno))
assert [path for path, _line in destub_defs] == ["toolchain/python-src/toolchain/exe_qol/common.py"], destub_defs

# Shell layer must not duplicate game identity hashes; profiles.py is the one data source.
shell_files = [ROOT / "toolchain/python-src/scripts/lib/qol_exe.sh", ROOT / "toolchain/python-src/scripts/build_ms2_qol.sh", ROOT / "toolchain/python-src/scripts/build_finale_qol.sh"]
for shell in shell_files:
    text = shell.read_text(encoding="utf-8")
    for profile in (MS2_PROFILE, FINALE_PROFILE):
        assert profile.original_sha256 not in text
        assert profile.destub_sha256 not in text
        assert profile.final_sha256 not in text

import subprocess
probe = subprocess.run(
    ["bash", "-c", "source toolchain/python-src/scripts/lib/qol_exe.sh; set +e; build_qol_exe test /definitely/missing /tmp/out /tmp/no-patcher 1; rc=$?; [[ $rc -eq 10 ]]"],
    cwd=ROOT,
    text=True,
    capture_output=True,
)
assert probe.returncode == 0, (probe.stdout, probe.stderr)
assert probe.stdout.startswith("SKIP: test executable not found:")

rebuild = (ROOT / "toolchain/python-src/scripts/rebuild_and_verify.sh").read_text(encoding="utf-8")
assert rebuild.index('build_ms2_qol.sh') < rebuild.index('build_finale_qol.sh')
verify = (ROOT / "toolchain/python-src/scripts/verify_project.sh").read_text(encoding="utf-8")
assert "toolchain/finale_qol" in verify
assert "toolchain/python-src/scripts/build_finale_qol.sh" in verify
configure = (ROOT / "toolchain/python-src/scripts/configure_sources.sh").read_text(encoding="utf-8")
assert "FINALE_PC_GAME" in configure and "FINALE_PC_EXE" in configure

print("EXE QoL shared-profile regressions: PASS")
