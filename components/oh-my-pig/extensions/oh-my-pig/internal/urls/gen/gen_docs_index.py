#!/usr/bin/env python3
"""Deterministically build internal/urls/docs_index.generated.txt and
internal/urls/docs_index.manifest.json from $OMP_SRC/docs/**/*.md.

Usage:
    OMP_SRC=~/work/omp-v19.1.1 python3 ./internal/urls/gen/gen_docs_index.py
"""

from __future__ import annotations

import argparse
import base64
import gzip
import hashlib
import json
import os
import sys
from pathlib import Path

PINNED_OMP_TAG = "v19.1.1"
PINNED_OMP_COMMIT = "31ffee513f27a01bba7d4dfbe12bae239cdc646a"
GENERATOR_REL = "internal/urls/gen/gen_docs_index.py"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--omp-src",
        default=os.environ.get("OMP_SRC", ""),
        help="path to omp v19.1.1 checkout",
    )
    parser.add_argument(
        "--out-dir",
        default="internal/urls",
        help="output directory for docs_index.generated.txt and manifest",
    )
    args = parser.parse_args()

    omp_src = args.omp_src.strip()
    if not omp_src:
        sys.stderr.write("OMP_SRC is required (or pass --omp-src)\n")
        return 1

    docs_dir = Path(omp_src) / "docs"
    if not docs_dir.is_dir():
        sys.stderr.write(f"docs directory not found: {docs_dir}\n")
        return 1

    files = sorted(
        p.relative_to(docs_dir).as_posix()
        for p in docs_dir.rglob("*.md")
        if p.is_file()
    )
    bodies = [
        (docs_dir / rel).read_text(encoding="utf-8")
        for rel in files
    ]

    files_line = json.dumps(files, ensure_ascii=False, separators=(",", ":"))
    bodies_raw = json.dumps(bodies, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    gz_bytes = gzip.compress(bodies_raw, compresslevel=9, mtime=0)
    payload = (files_line + "\n" + base64.b64encode(gz_bytes).decode("ascii")).encode("utf-8")

    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    out_file = out_dir / "docs_index.generated.txt"
    out_file.write_bytes(payload)

    manifest = {
        "generator": GENERATOR_REL,
        "ompTag": PINNED_OMP_TAG,
        "ompCommit": PINNED_OMP_COMMIT,
        "sourceDir": "docs/**/*.md",
        "fileCount": len(files),
        "output": {
            "file": "docs_index.generated.txt",
            "bytes": len(payload),
            "sha256": hashlib.sha256(payload).hexdigest(),
            "gzipBytes": len(gz_bytes),
            "gzipSha256": hashlib.sha256(gz_bytes).hexdigest(),
        },
    }
    manifest_file = out_dir / "docs_index.manifest.json"
    manifest_file.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(f"Generated {out_file} ({len(files)} docs, {len(payload)} bytes) and {manifest_file}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
