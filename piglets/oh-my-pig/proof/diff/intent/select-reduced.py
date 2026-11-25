#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
"""Deterministically select a reduced argument-validation corpus from corpus.jsonl + port-prepare-only.jsonl.

Selection rule:
  Walks `corpus.jsonl` in ascending `id` order and keeps up to 2 cases per
  `(tool, tag_family, result_class)` bucket, where `tag_family` is the first two
  `:`-separated segments of `tag` (e.g. `set:full`, `trail:req`, `x:dquote2`,
  `x:ident-trim`, or the full tag if it has no colon) and `result_class` is the
  port's classification from `results/port-prepare-only.jsonl` (or omp's
  `(ok, schemaOk)` outcome when no port results JSONL is supplied).

Reproduces `components/oh-my-pig/extensions/oh-my-pig/internal/intent/testdata/omp_diff_reduced.jsonl`
(1,199 cases, sha256 `5924b27c88fb5853cf016a3d02750e68bfae3ab2eb71a8cb3280fcdf4bc29e69`)
byte for byte from `corpus.jsonl` and `results/port-prepare-only.jsonl`.

Usage:
    python3 select-reduced.py <full_corpus.jsonl> <reduced_out.jsonl> [port-prepare-only.jsonl]
"""
from __future__ import annotations

import hashlib
import json
import sys
from collections import defaultdict
from pathlib import Path


def tag_family(tag: str) -> str:
    parts = tag.split(":")
    return ":".join(parts[:2]) if len(parts) >= 2 else tag


def select_reduced(full_path: Path, out_path: Path, port_jsonl: Path | None = None, limit_per_bucket: int = 2) -> tuple[int, str]:
    raw_lines = [l for l in full_path.read_text(encoding="utf-8").split("\n") if l]
    port_classes: dict[int, str] = {}
    if port_jsonl is not None:
        for l in port_jsonl.read_text(encoding="utf-8").split("\n"):
            if not l:
                continue
            row = json.loads(l)
            port_classes[int(row["id"])] = str(row["class"])

    counts: dict[tuple, int] = defaultdict(int)
    selected_lines: list[str] = []
    for raw in raw_lines:
        c = json.loads(raw)
        cid = int(c["id"])
        cls = port_classes.get(cid) if port_classes else f"omp:{c['omp'].get('ok')}:{c['omp'].get('schemaOk')}"
        key = (c["tool"], tag_family(c["tag"]), cls)
        if counts[key] < limit_per_bucket:
            counts[key] += 1
            selected_lines.append(raw)

    payload = ("\n".join(selected_lines) + "\n").encode("utf-8")
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_bytes(payload)
    return len(selected_lines), hashlib.sha256(payload).hexdigest()


def main() -> int:
    if len(sys.argv) not in (3, 4):
        sys.stderr.write("usage: select-reduced.py <full_corpus.jsonl> <reduced_out.jsonl> [port-prepare-only.jsonl]\n")
        return 2
    port_p = Path(sys.argv[3]) if len(sys.argv) == 4 else None
    n, digest = select_reduced(Path(sys.argv[1]), Path(sys.argv[2]), port_p)
    print(f"selected {n} cases -> {sys.argv[2]} (sha256 {digest})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
