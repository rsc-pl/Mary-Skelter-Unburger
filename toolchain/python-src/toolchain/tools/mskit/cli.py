"""Command line interface: `ms_text_tool <command> [options]`."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from . import corpus, extract, rebuild, records, verify


def _print(obj) -> None:
    print(json.dumps(obj, ensure_ascii=False, indent=2, sort_keys=True))


def cmd_list_datasets(args) -> int:
    for name, spec in sorted(corpus.DATASETS.items()):
        print(f"{name}\t{spec['game']}\t{spec['platform']}\t{spec['language']}\t"
              f"{spec['archives'] if spec['archives'] else 'all'}\t{spec['note']}")
    return 0


def cmd_archives(args) -> int:
    for p in extract.archive_paths(args.dataset, master=args.master):
        print(f"{corpus.archive_number(p)}\t{p.stat().st_size}\t{p}")
    return 0


def cmd_inventory(args) -> int:
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    summary = {}
    datasets = args.datasets or sorted(corpus.DATASETS)
    for ds in datasets:
        try:
            st = extract.extract_dataset(
                ds, out / "records" / f"{ds}.jsonl",
                master=args.master if args.master and len(datasets) == 1 else None,
                inventory_path=out / f"{ds}.inventory.tsv",
                archives=args.archives)
        except corpus.CorpusError as exc:
            summary[ds] = {"error": str(exc)}
            continue
        summary[ds] = st.as_dict()
    (out / "inventory_summary.json").write_text(
        json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True), encoding="utf-8")
    _print(summary)
    return 0


def cmd_extract(args) -> int:
    st = extract.extract_dataset(args.dataset, Path(args.out), master=args.master,
                                 inventory_path=Path(args.inventory) if args.inventory else None,
                                 archives=args.archives,
                                 include_fixstr=args.include_fixstr)
    _print(st.as_dict())
    return 0


def cmd_rebuild(args) -> int:
    rep = rebuild.rebuild_dataset(args.dataset, Path(args.records) if args.records else None,
                                  Path(args.out), master=args.master,
                                  archives=args.archives, level=args.level,
                                  force_rewrite=args.force_rewrite, mode=args.mode)
    _print(rep)
    if args.report:
        Path(args.report).write_text(json.dumps(rep, ensure_ascii=False, indent=2),
                                     encoding="utf-8")
    return 0


def cmd_verify(args) -> int:
    rep = verify.verify_dataset(args.dataset, Path(args.rebuilt), Path(args.expected_records),
                                out_report=Path(args.report) if args.report else None,
                                archives=args.archives,
                                source_master=Path(args.source_master) if args.source_master else None)
    print(json.dumps({k: v for k, v in rep.items() if k not in ("changed",)},
                     ensure_ascii=False, indent=2, sort_keys=True))
    if rep["changed"]:
        print("first changed records:")
        _print(rep["changed"][:5])
    return 0 if rep["ok"] else 1


def cmd_diff(args) -> int:
    rep = verify.diff_against_source(args.dataset, Path(args.rebuilt), Path(args.source_master),
                                     archives=args.archives,
                                     out_report=Path(args.report) if args.report else None)
    print(json.dumps({k: v for k, v in rep.items() if k != "changed"},
                     ensure_ascii=False, indent=2, sort_keys=True))
    return 0


def cmd_stats(args) -> int:
    counts = {}
    for path in args.records:
        n = 0
        keys = set()
        for rec in records.read_jsonl(path):
            n += 1
            keys.add(rec["record_key"])
        counts[str(path)] = {"records": n, "unique_keys": len(keys)}
    _print(counts)
    return 0


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="ms_text_tool",
        description="Mary Skelter 2 / Mary Skelter Finale reversible text toolchain "
                    "(extract, rebuild, verify). Source trees are never modified.")
    sub = p.add_subparsers(dest="command", required=True)

    s = sub.add_parser("list-datasets", help="show the built-in dataset definitions")
    s.set_defaults(func=cmd_list_datasets)

    s = sub.add_parser("archives", help="list the GDAT archives of a dataset")
    s.add_argument("--dataset", required=True)
    s.add_argument("--master", type=Path, help="override the archive directory")
    s.set_defaults(func=cmd_archives)

    s = sub.add_parser("inventory", help="census every dataset (writes per-entry inventory TSVs)")
    s.add_argument("--datasets", nargs="*", help="datasets to scan (default: all)")
    s.add_argument("--archives", nargs="*", type=int)
    s.add_argument("--master", type=Path, help="use this archive directory instead of sources/")
    s.add_argument("--out", required=True, help="output directory")
    s.set_defaults(func=cmd_inventory)

    s = sub.add_parser("extract", help="export all text records of a dataset to JSONL")
    s.add_argument("--dataset", required=True)
    s.add_argument("--master", type=Path, help="use this archive directory instead of sources/")
    s.add_argument("--archives", nargs="*", type=int)
    s.add_argument("--out", required=True, help="output JSONL file")
    s.add_argument("--inventory", help="also write a per-entry inventory TSV here")
    s.add_argument("--include-fixstr", action="store_true",
                   help="also export fixed-length string fields (usually empty)")
    s.set_defaults(func=cmd_extract)

    s = sub.add_parser("rebuild", help="rebuild archives from an edited JSONL export")
    s.add_argument("--dataset", required=True)
    s.add_argument("--records", help="edited JSONL (default: no edits -> copy-through only)")
    s.add_argument("--master", type=Path, help="use this archive directory instead of sources/")
    s.add_argument("--archives", nargs="*", type=int)
    s.add_argument("--out", required=True, help="output directory (created; never the source)")
    s.add_argument("--level", type=int, default=9, help="zlib level for recompressed entries")
    s.add_argument("--force-rewrite", action="store_true",
                   help="rewrite every archive even without edits (byte-identity check)")
    s.add_argument("--mode", choices=("relayout", "append"), default="relayout",
                   help="relayout = grow items in place and fix every offset (compact); "
                        "append = copy the grown item to EOF (no existing byte moves)")
    s.add_argument("--report", help="write the rebuild report JSON here")
    s.set_defaults(func=cmd_rebuild)

    s = sub.add_parser("verify", help="re-extract from a rebuilt tree and compare")
    s.add_argument("--dataset", required=True)
    s.add_argument("--rebuilt", required=True, help="rebuilt master dir (…/masterWindows)")
    s.add_argument("--expected-records", required=True)
    s.add_argument("--source-master", help="overlay unchanged archives from this source master")
    s.add_argument("--archives", nargs="*", type=int)
    s.add_argument("--report", help="write the verification report JSON here")
    s.set_defaults(func=cmd_verify)

    s = sub.add_parser("diff", help="compare a rebuilt tree with the original source tree")
    s.add_argument("--dataset", required=True)
    s.add_argument("--rebuilt", required=True)
    s.add_argument("--source-master", required=True)
    s.add_argument("--archives", nargs="*", type=int)
    s.add_argument("--report")
    s.set_defaults(func=cmd_diff)

    s = sub.add_parser("stats", help="record/unique-key counts for JSONL exports")
    s.add_argument("records", nargs="+", type=Path)
    s.set_defaults(func=cmd_stats)
    return p


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except (corpus.CorpusError, rebuild.RebuildError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())
