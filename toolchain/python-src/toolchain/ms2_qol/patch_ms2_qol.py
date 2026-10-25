#!/usr/bin/env python3
"""Mary Skelter 2 PC QoL patcher using the shared executable patch engine."""
from __future__ import annotations

import sys
from pathlib import Path

TOOLCHAIN = Path(__file__).resolve().parents[1]
if str(TOOLCHAIN) not in sys.path:
    sys.path.insert(0, str(TOOLCHAIN))

from exe_qol.cli import run_profile_cli  # noqa: E402
from exe_qol.common import destub, parse_pe, patch_rva, sha256  # noqa: E402,F401
from exe_qol.profiles import MS2_PROFILE  # noqa: E402

TARGET_SHA256 = MS2_PROFILE.original_sha256
DESTUB_SHA256 = MS2_PROFILE.destub_sha256
FINAL_SHA256 = MS2_PROFILE.final_sha256
LOGO_RVA = MS2_PROFILE.patches[0].rva
LOGO_EXPECTED = MS2_PROFILE.patches[0].expected
LOGO_PATCH = MS2_PROFILE.patches[0].replacement
VOICE_BOOT_RVA = MS2_PROFILE.patches[1].rva
VOICE_EXPECTED = MS2_PROFILE.patches[1].expected
VOICE_PATCH = MS2_PROFILE.patches[1].replacement


def main() -> int:
    return run_profile_cli(MS2_PROFILE)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
