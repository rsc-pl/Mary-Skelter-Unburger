#!/usr/bin/env python3
"""Entry point for the Mary Skelter text toolchain.

    ms_text_tool list-datasets
    ms_text_tool extract --dataset MSF_PC_LOCALIZED --out out.jsonl
    ms_text_tool rebuild --dataset MSF_PC_LOCALIZED --records edited.jsonl --out rebuilt/
    ms_text_tool verify  --dataset MSF_PC_LOCALIZED --rebuilt rebuilt/masterWindows \
                         --expected-records edited.jsonl

Run `ms_text_tool <command> --help` for the options of each command.
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from mskit.cli import main  # noqa: E402

if __name__ == "__main__":
    raise SystemExit(main())
