"""Shared command-line frontend for game-specific executable QoL profiles."""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

from .common import ExeProfile, classify, destub, patch_executable, patch_rva, parse_pe, sha256


def run_profile_cli(profile: ExeProfile, argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=f"Patch {profile.display_name} PC QoL executable.")
    ap.add_argument("exe", type=Path, help=f"input {profile.input_filename}")
    ap.add_argument("-o", "--output", type=Path,
                    help=f"output path (default: {profile.default_output_filename} beside input)")
    checks = ap.add_mutually_exclusive_group()
    checks.add_argument("--check", action="store_true",
                        help="identify a supported executable state; do not write")
    checks.add_argument("--check-final", action="store_true",
                        help="require the exact final QoL executable; do not write")
    ap.add_argument("--force-unknown", action="store_true",
                    help="allow processing an unknown hash (all restored-image byte/RVA guards still apply)")
    args = ap.parse_args(argv)

    data = args.exe.read_bytes()
    state = classify(data, profile)
    print(f"input: {args.exe}")
    print(f"size: {len(data)}")
    print(f"sha256: {sha256(data)}")
    print(f"state: {state}")

    if args.check:
        return 0 if state != "unknown" else 2
    if args.check_final:
        return 0 if state == "qol-final" else 2
    if state == "qol-final":
        print("already final; nothing to do")
        if args.output:
            args.output.write_bytes(data)
        return 0
    if state == "unknown" and not args.force_unknown:
        print("REFUSING: unsupported/unknown EXE hash. Use the exact target build or inspect manually.",
              file=sys.stderr)
        return 2

    if state == "original-steamstub":
        print("[1/3] removing SteamStub 3.1 x64 wrapper...")
    elif state == "destubbed-unpatched":
        print("[1/3] input is already the verified destubbed build")
    elif state == "qol-base-final":
        print("[1/3] verified prior QoL final; applying late runtime fixes")
    else:
        print("[1/3] forced unknown restored image; validating every patch-site guard")

    out_data, original_state = patch_executable(data, profile, force_unknown=args.force_unknown)
    if original_state == "original-steamstub":
        # patch_executable already enforced this exact intermediate identity.
        print(f"      destub SHA-256 OK: {profile.destub_sha256}")

    applied = []
    if original_state in ("original-steamstub", "destubbed-unpatched", "unknown"):
        applied.extend(profile.patches)
    applied.extend(profile.late_patches)
    for index, patch in enumerate(applied, start=2):
        print(f"[{index}/{len(applied)+1}] {patch.message}...")

    out = args.output or args.exe.with_name(profile.default_output_filename)
    out.write_bytes(out_data)
    out_hash = sha256(out_data)
    print(f"output: {out}")
    print(f"sha256: {out_hash}")
    if original_state in ("original-steamstub", "destubbed-unpatched", "qol-base-final"):
        print("FINAL STATE VERIFIED")
    else:
        print("FINAL STATE VERIFIED AFTER GUARDED FORCE")
    return 0


__all__ = [
    "run_profile_cli", "destub", "parse_pe", "patch_rva", "sha256",
]
