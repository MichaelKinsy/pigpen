#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
"""Deterministically select the reduced approval differential corpus from corpus.jsonl.

Reproduces `components/oh-my-pig/extensions/oh-my-pig/testdata/approval_diff_reduced.jsonl`
byte for byte (3,873 cases, sha256 `00bb1a189d9c960ccc13afa26642789d30f62f988c075940368fcbd18903059c`)
when fed the omp v19.1.1 full corpus (`b64b61eb9a439973cea276bef7142d7953234de7fc6daddf5c849d13c978d494`).

Usage:
    python3 select-reduced.py <full_corpus.jsonl> <reduced_out.jsonl>
"""
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path


def select_reduced(full_path: Path, out_path: Path) -> tuple[int, str]:
    raw_lines = [l for l in full_path.read_text(encoding="utf-8").split("\n") if l]
    selected_lines: list[str] = []

    seen_base_u: set[tuple] = set()
    seen_base_sub: set[tuple] = set()
    seen_base_pol: set[tuple] = set()
    seen_base_pol_tool: set[tuple] = set()
    seen_base_pat: set[tuple] = set()
    seen_rest: set[tuple] = set()

    for raw in raw_lines:
        c = json.loads(raw)
        if c.get("compare") is False:
            continue
        sec = c["section"]
        if sec in ("paths", "unwrap", "cached", "interceptor", "project", "glob-path", "nil-setting", "malformed"):
            selected_lines.append(raw)
            continue

        op = c["settings"].get("operator") or {}
        mode = op.get("tools.approvalMode") or "<unset>"
        pol_map = op.get("tools.approval") or {}
        user_pol = pol_map.get(c["tool"]) or "<none>"
        has_pat = "bash.patterns" in op
        ui = c["ui"]
        sess = c["session"]
        omp = c["omp"]
        dec, src, tier, reason = omp.get("decision"), omp.get("source"), omp.get("tier"), omp.get("reason")

        if sec == "base":
            keep = False
            if not has_pat:
                if user_pol == "<none>" and ui == "interactive" and sess == "top":
                    if mode in ("always-ask", "always-ask-cached"):
                        keep = True
                    else:
                        k = (mode, c["tool"], tier, dec, src)
                        if k not in seen_base_u:
                            seen_base_u.add(k)
                            keep = True
                elif user_pol == "<none>":
                    k = (mode, ui, sess, tier, dec, src)
                    if k not in seen_base_sub:
                        seen_base_sub.add(k)
                        keep = True
                else:
                    if (ui, sess) != ("interactive", "subagent"):
                        k = (mode, user_pol, ui, sess, tier, dec, src)
                        if k not in seen_base_pol:
                            seen_base_pol.add(k)
                            keep = True
                    if ui == "interactive" and sess == "top" and (mode, user_pol) in (
                        ("always-ask", "deny"),
                        ("always-ask", "allow"),
                        ("yolo", "prompt"),
                    ):
                        k2 = (mode, user_pol, c["tool"])
                        if k2 not in seen_base_pol_tool:
                            seen_base_pol_tool.add(k2)
                            keep = True
            else:
                if user_pol == "<none>" and ui == "interactive" and sess == "top" and mode in ("<unset>", "always-ask"):
                    keep = True
                elif (ui, sess) != ("interactive", "subagent"):
                    k = (mode, user_pol, ui, sess, dec, src, reason)
                    if k not in seen_base_pat:
                        seen_base_pat.add(k)
                        keep = True
            if keep:
                selected_lines.append(raw)
        elif sec == "restricted":
            folders = tuple(op.get("tools.restrictedFolders") or [])
            keep = False
            if mode == "<unset>" and user_pol == "<none>" and ui == "interactive" and sess == "top":
                keep = True
            k = (folders, mode, user_pol, ui, sess, dec, src)
            if k not in seen_rest:
                seen_rest.add(k)
                keep = True
            if keep:
                selected_lines.append(raw)

    payload = ("\n".join(selected_lines) + "\n").encode("utf-8")
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_bytes(payload)
    return len(selected_lines), hashlib.sha256(payload).hexdigest()


def main() -> int:
    if len(sys.argv) != 3:
        sys.stderr.write("usage: select-reduced.py <full_corpus.jsonl> <reduced_out.jsonl>\n")
        return 2
    n, digest = select_reduced(Path(sys.argv[1]), Path(sys.argv[2]))
    print(f"selected {n} cases -> {sys.argv[2]} (sha256 {digest})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
