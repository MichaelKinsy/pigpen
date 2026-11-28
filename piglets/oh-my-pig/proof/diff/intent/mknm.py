#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
# Drives omp v19.1.1 (MIT, Can Bölük / Stencil Labs) from $OMP_SRC; embeds no omp source code.
"""Build <nm_dir>/@oh-my-pi/*: a NODE_PATH directory that resolves omp's workspace packages
to the v19.1.1 source tree (read-only, no node_modules of its own) by symlink, honouring
each package's `exports` map, and stubs @oh-my-pi/pi-natives (the Rust addon is not built
in the source export; argument validation never calls into it).

Usage:
    OMP_SRC=/path/to/omp-v19.1.1 python3 mknm.py [nm_dir]
"""
import glob
import json
import os
import re
import shutil
import sys

EXPECTED_TAG = "v19.1.1"
EXPECTED_VERSION = "19.1.1"
EXPECTED_COMMIT = "31ffee513f27a01bba7d4dfbe12bae239cdc646a"


def verify_omp_src(omp_src: str) -> str:
    if not omp_src or not os.path.isdir(omp_src):
        sys.exit(
            f"error: OMP_SRC={omp_src!r} not found. Point OMP_SRC at an omp {EXPECTED_TAG} "
            f"(commit {EXPECTED_COMMIT}) source tree."
        )
    pkg_json = os.path.join(omp_src, "packages", "coding-agent", "package.json")
    if not os.path.isfile(pkg_json):
        sys.exit(
            f"error: {pkg_json} not found. OMP_SRC must point to an omp {EXPECTED_TAG} "
            f"(commit {EXPECTED_COMMIT}) source tree."
        )
    ver = json.load(open(pkg_json, encoding="utf-8")).get("version")
    if ver != EXPECTED_VERSION:
        sys.exit(
            f"error: OMP_SRC={omp_src!r} has @oh-my-pi/pi-coding-agent version {ver!r}, "
            f"expected {EXPECTED_VERSION!r} (tag {EXPECTED_TAG}, commit {EXPECTED_COMMIT})."
        )
    git_head = os.path.join(omp_src, ".git")
    if os.path.exists(git_head):
        import subprocess
        try:
            rev = subprocess.check_output(
                ["git", "-C", omp_src, "rev-parse", "HEAD"], stderr=subprocess.DEVNULL, text=True
            ).strip()
        except Exception:
            rev = ""
        if rev and rev != EXPECTED_COMMIT:
            sys.exit(
                f"error: OMP_SRC={omp_src!r} is at git commit {rev}, expected tag {EXPECTED_TAG} "
                f"(commit {EXPECTED_COMMIT})."
            )
    return os.path.join(omp_src, "packages")


def main() -> None:
    omp_src = os.environ.get("OMP_SRC", "")
    packages_root = verify_omp_src(omp_src)
    nm_base = (
        os.path.abspath(sys.argv[1])
        if len(sys.argv) > 1
        else os.path.join(os.environ.get("DIFF_OUT", "/tmp/omp-intent-diff"), "nm")
    )
    nm_scope = os.path.join(nm_base, "@oh-my-pi")
    shutil.rmtree(nm_base, ignore_errors=True)
    os.makedirs(nm_scope)

    for d in sorted(glob.glob(packages_root + "/*/")):
        d = d.rstrip("/")
        pj = d + "/package.json"
        if not os.path.exists(pj):
            continue
        j = json.load(open(pj, encoding="utf-8"))
        name = j["name"]
        if not name.startswith("@oh-my-pi/") or name == "@oh-my-pi/pi-natives":
            continue
        t = nm_scope + "/" + name.split("/")[1]
        os.makedirs(t)
        ex = j.get("exports") or {}

        def tgt(v):
            return v if isinstance(v, str) else (v.get("import") or v.get("default") or v.get("types"))

        main_entry = tgt(ex["."]) if "." in ex else j.get("main", "./src/index.ts")
        json.dump({"name": name, "type": "module", "main": "./__main.ts"}, open(t + "/package.json", "w", encoding="utf-8"))
        os.symlink(os.path.normpath(os.path.join(d, main_entry)), t + "/__main.ts")

        def link(key, target):
            dst = os.path.join(t, key)
            ext = os.path.splitext(target)[1]
            if not os.path.splitext(key)[1] or not dst.endswith(ext):
                dst += ext
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            if not os.path.lexists(dst):
                os.symlink(target, dst)

        for k, v in ex.items():
            if k == ".":
                continue
            target = tgt(v)
            if target is None:
                continue
            if "*" in k:
                if k.endswith(".js"):
                    continue
                kpre, ksuf = k[2:].split("*")
                tpre, tsuf = target[2:].split("*")
                base = os.path.join(d, tpre)
                bdir = base if base.endswith("/") else os.path.dirname(base)
                for root, _dirs, files in os.walk(bdir):
                    for f in files:
                        full = os.path.join(root, f)
                        if not full.startswith(base) or not full.endswith(tsuf):
                            continue
                        mid = full[len(base) : len(full) - len(tsuf)] if tsuf else full[len(base) :]
                        link(kpre + mid + ksuf, full)
            else:
                link(k[2:], os.path.normpath(os.path.join(d, target)))

    # pi-natives stub: every export is one permissive proxy. Argument validation never calls into it.
    t = nm_scope + "/pi-natives"
    os.makedirs(t)
    json.dump({"name": "@oh-my-pi/pi-natives", "type": "module", "main": "./index.js"}, open(t + "/package.json", "w", encoding="utf-8"))
    n_dir = os.path.join(packages_root, "natives", "native")
    u_stub = (
        "const U = new Proxy(function(){}, { get(t,p){ if (p===Symbol.iterator) return function*(){}; "
        "if (p===Symbol.toPrimitive) return ()=>''; if (p==='then') return undefined; if (p==='length') return 0; "
        "if (p==='prototype') return t.prototype; return U; }, apply(){ return U; }, construct(){ return U; } });\n"
    )
    for f in ["index", "path", "sqlite-locks", "vcs", "desktop", "clipboard"]:
        src = open(f"{n_dir}/{f}.d.ts", encoding="utf-8").read()
        names = sorted(
            set(re.findall(r"^export (?:declare )?(?:async )?(?:function|class|const|enum|let|var) +([A-Za-z_0-9]+)", src, re.M))
        )
        open(f"{t}/{f}.js", "w", encoding="utf-8").write(u_stub + "".join(f"export const {n} = U;\n" for n in names))
    print("nm built at", nm_base)


if __name__ == "__main__":
    main()
