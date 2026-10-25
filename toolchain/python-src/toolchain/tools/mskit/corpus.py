"""Dataset definitions and source-tree discovery.

Language assignments are *mechanical*: they come from the script layout inside
the archives themselves (see docs/RESOURCE_LANGUAGE_MAP.md for the evidence
table produced by `ms_text_tool inventory`).
"""
from __future__ import annotations

from pathlib import Path

GAMES = ("MS2", "MSF")

#: Finale stores one full language copy per archive triple; the remaining
#: archives are shared engine/system data that every language build uses.
MSF_LANGUAGE_ARCHIVES = {
    "MSF_PC_JP": (64, 65, 66),
    "MSF_PC_LOCALIZED": (70, 71, 72),
}

DATASETS = {
    "MS2_PC_LOCALIZED": {
        "game": "MS2", "platform": "PC", "language": "en", "path": "MS2_PC",
        "archives": None, "note": "Steam PC English target; archives 051-062",
    },
    "MS2_SWITCH_JP": {
        "game": "MS2", "platform": "Switch", "language": "ja", "path": "MS2_SWITCH_JP",
        "archives": None, "note": "Japanese source corpus (read-only)",
    },
    "MSF_PC_JP": {
        "game": "MSF", "platform": "PC", "language": "ja", "path": "MSF_PC",
        "archives": (64, 65, 66), "note": "Finale Japanese script set",
    },
    "MSF_PC_LOCALIZED": {
        "game": "MSF", "platform": "PC", "language": "en", "path": "MSF_PC",
        "archives": (70, 71, 72), "note": "Finale official English script set",
    },
    "MS2_SWITCH_WESTERN": {
        "game": "MS2", "platform": "Switch", "language": "en",
        "path": "MS2_SWITCH_WESTERN", "archives": None,
        "note": "User-owned Western latest-update RomFS; native text-port candidate only",
    },
    "MSF_SWITCH_WESTERN": {
        "game": "MSF", "platform": "Switch", "language": "en",
        "path": "MSF_SWITCH_WESTERN", "archives": None,
        "note": "User-owned Western latest-update RomFS; native text-port candidate only",
    },
    "MSF_PC_SYSTEM": {
        "game": "MSF", "platform": "PC", "language": None, "path": "MSF_PC",
        "archives": (51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 67, 68, 69,
                     73, 74, 75, 76, 77, 78, 82, 83, 84, 85, 86, 88),
        "note": "Finale shared/system archives (all languages)",
    },
}


class CorpusError(Exception):
    pass


def project_root() -> Path:
    return Path(__file__).resolve().parents[5]


def sources_root(root: Path | None = None) -> Path:
    return (root or project_root()) / "sources"


def dataset_dir(dataset: str, root: Path | None = None) -> Path:
    spec = DATASETS[dataset]
    base = sources_root(root) / spec["path"]
    if not base.exists():
        raise CorpusError(f"missing source directory for {dataset}: {base}")
    return base


def master_dir(dataset: str, root: Path | None = None) -> Path:
    """Directory that holds the GxArchivedFile*.dat archives of a dataset."""
    base = dataset_dir(dataset, root)
    for name in ("masterWindows", "masterNX64"):
        p = base / name
        if p.is_dir():
            return p
    found = sorted(base.rglob("masterNX64")) or sorted(base.rglob("masterWindows"))
    if not found:
        raise CorpusError(f"no masterWindows/masterNX64 below {base}")
    return found[0]


def archives_of(dataset: str, root: Path | None = None) -> list[Path]:
    """Sorted GDAT archives belonging to `dataset` (filtered by its spec)."""
    spec = DATASETS[dataset]
    master = master_dir(dataset, root)
    out = []
    for p in sorted(master.glob("GxArchivedFile*.dat")):
        num = int(p.name[len("GxArchivedFile"):-len(".dat")])
        if spec["archives"] is not None and num not in spec["archives"]:
            continue
        out.append(p)
    return out


def archive_number(path: Path) -> int:
    return int(path.name[len("GxArchivedFile"):-len(".dat")])


def csh_path(dataset: str, number: int, root: Path | None = None) -> Path | None:
    master = master_dir(dataset, root)
    p = master / f"GxArchivedTable{number:03d}.csh"
    return p if p.exists() else None
