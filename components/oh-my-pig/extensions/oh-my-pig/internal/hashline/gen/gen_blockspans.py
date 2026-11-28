#!/usr/bin/env python3
"""Generate block-span oracle fixtures by running real omp (v19.1.1) offline
through piglets/oh-my-pig/proof/oracle.sh (omp-only mode, 0 network).

Usage (from extension module root components/oh-my-pig/extensions/oh-my-pig):
    python3 -B internal/hashline/gen/gen_blockspans.py [--sample 1|2|3|all] [--langs ts,go,...]

Writes:
    Sample 1 (curated real + constructed W1-W8/N1-N5 adversarial):
        internal/hashline/testdata/blockspans/files/<lang>/<name>
        internal/hashline/testdata/blockspans/<lang>.json
    Sample 2 (fresh discovery sample, >=400 anchors/lang, disjoint from Sample 1):
        internal/hashline/testdata/blockspans/files/sample2/<lang>/<name>
        internal/hashline/testdata/blockspans/sample2_<lang>.json
    Sample 3 (held-out validation sample, >=400 anchors/lang, disjoint from Samples 1 & 2):
        internal/hashline/testdata/blockspans/files/sample3/<lang>/<name>
        internal/hashline/testdata/blockspans/sample3_<lang>.json

For each anchor line in each file, runs `CUT N*` in omp against an untouched copy
of the file and records omp's exact outcome:
    - `span: [start, end]` when omp resolved `CUT N* → resolved lines S-E (...)`
    - `refused: <omp message>` when omp refused the block op.

Provenance policy: all real-file samples are taken exclusively from omp v19.1.1
(MIT) and PiG/Pigpen-authored files. Vendored and third-party trees (`vendor`,
`third_party`, `node_modules`, `pi-dist`, `shims`, `port`) are excluded.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import random
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

sys.dont_write_bytecode = True
SCRIPT_DIR = Path(__file__).resolve().parent
EXT_DIR = SCRIPT_DIR.parent.parent.parent  # components/oh-my-pig/extensions/oh-my-pig
REPO_ROOT = EXT_DIR.parent.parent.parent.parent  # pigpen-omp
ORACLE_SH = REPO_ROOT / "piglets" / "oh-my-pig" / "proof" / "oracle.sh"
OUT_ROOT = EXT_DIR / "internal" / "hashline" / "testdata" / "blockspans"
FILES_ROOT = OUT_ROOT / "files"

_OMP_SRC_ENV = os.environ.get("OMP_SRC", "").strip()
if not _OMP_SRC_ENV:
    sys.stderr.write("OMP_SRC is not set; set OMP_SRC to omp tag v19.1.1 (commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a)\n")
    raise SystemExit(1)
_PIG_SRC_ENV = os.environ.get("PIG_SRC", "").strip()
if not _PIG_SRC_ENV:
    sys.stderr.write("PIG_SRC is not set; set PIG_SRC to the PiG host source checkout (for omp tag v19.1.1, commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a)\n")
    raise SystemExit(1)
OMP_SRC = Path(_OMP_SRC_ENV)
PIG_SRC = Path(_PIG_SRC_ENV)
RESOLVED_RE = re.compile(r"CUT \d+\* → resolved lines? (\d+)(?:-(\d+))? \(\d+ lines?\)")

PRIME32_1 = 0x9E3779B1
PRIME32_2 = 0x85EBCA77
PRIME32_3 = 0xC2B2AE3D
PRIME32_4 = 0x27D4EB2F
PRIME32_5 = 0x165667B1
M32 = 0xFFFFFFFF


def _rotl32(x: int, r: int) -> int:
    return ((x << r) | (x >> (32 - r))) & M32


def _xxh32_round(acc: int, inp: int) -> int:
    acc = (acc + inp * PRIME32_2) & M32
    acc = _rotl32(acc, 13)
    return (acc * PRIME32_1) & M32


def xxh32_bytes(data: bytes, seed: int = 0) -> int:
    n = len(data)
    p = 0
    if n >= 16:
        v1 = (seed + PRIME32_1 + PRIME32_2) & M32
        v2 = (seed + PRIME32_2) & M32
        v3 = seed & M32
        v4 = (seed - PRIME32_1) & M32
        while p + 16 <= n:
            v1 = _xxh32_round(v1, int.from_bytes(data[p : p + 4], "little"))
            v2 = _xxh32_round(v2, int.from_bytes(data[p + 4 : p + 8], "little"))
            v3 = _xxh32_round(v3, int.from_bytes(data[p + 8 : p + 12], "little"))
            v4 = _xxh32_round(v4, int.from_bytes(data[p + 12 : p + 16], "little"))
            p += 16
        acc = (_rotl32(v1, 1) + _rotl32(v2, 7) + _rotl32(v3, 12) + _rotl32(v4, 18)) & M32
    else:
        acc = (seed + PRIME32_5) & M32
    acc = (acc + n) & M32
    while p + 4 <= n:
        acc = (acc + int.from_bytes(data[p : p + 4], "little") * PRIME32_3) & M32
        acc = (_rotl32(acc, 17) * PRIME32_4) & M32
        p += 4
    while p < n:
        acc = (acc + data[p] * PRIME32_5) & M32
        acc = (_rotl32(acc, 11) * PRIME32_1) & M32
        p += 1
    acc ^= acc >> 15
    acc = (acc * PRIME32_2) & M32
    acc ^= acc >> 13
    acc = (acc * PRIME32_3) & M32
    acc ^= acc >> 16
    return acc


def xxh32_file_hash(text: str) -> str:
    """Compute omp's exact 4-hex file hash (BOM stripped, CRLF->LF, trailing space/tab/CR per line stripped)."""
    if text.startswith("\ufeff"):
        text = text[1:]
    text = text.replace("\r\n", "\n")
    lines = text.split("\n")
    stripped = "\n".join(line.rstrip(" \t\r") for line in lines)
    return f"{xxh32_bytes(stripped.encode('utf-8')) & 0xFFFF:04X}"


REAL_FILES: dict[str, list[tuple[str, Path]]] = {
    "ts": [
        ("store.ts", OMP_SRC / "packages/coding-agent/src/edit/store.ts"),
        ("schemas.ts", OMP_SRC / "packages/coding-agent/src/edit/schemas.ts"),
        ("remote-mirror.ts", OMP_SRC / "packages/coding-agent/src/edit/remote-mirror.ts"),
        ("settings.ts", OMP_SRC / "packages/coding-agent/src/edit/settings.ts"),
        ("normalize.ts", OMP_SRC / "packages/coding-agent/src/edit/normalize.ts"),
        ("pipeline.ts", OMP_SRC / "packages/coding-agent/src/commit/pipeline.ts"),
        ("model-selection.ts", OMP_SRC / "packages/coding-agent/src/commit/model-selection.ts"),
        ("validation.ts", OMP_SRC / "packages/coding-agent/src/commit/agentic/validation.ts"),
    ],
    "tsx": [
        ("App.tsx", OMP_SRC / "python/robomp/web/src/App.tsx"),
        ("IssueCard.tsx", OMP_SRC / "python/robomp/web/src/components/IssueCard.tsx"),
        ("Shell.tsx", OMP_SRC / "python/robomp/web/src/components/shell/Shell.tsx"),
        ("TopBar.tsx", OMP_SRC / "python/robomp/web/src/components/shell/TopBar.tsx"),
        ("Vitals.tsx", OMP_SRC / "python/robomp/web/src/components/shell/Vitals.tsx"),
        ("Events.tsx", OMP_SRC / "python/robomp/web/src/components/Events.tsx"),
    ],
    "js": [
        ("commit-prelude.js", OMP_SRC / "packages/coding-agent/src/commit/selective/prelude.js"),
        ("browser-prelude.js", OMP_SRC / "packages/coding-agent/src/tools/browser/prelude.js"),
        ("computer-prelude.js", OMP_SRC / "packages/coding-agent/src/tools/computer/prelude.js"),
        ("archive-prelude.js", OMP_SRC / "packages/coding-agent/src/archive/prelude.js"),
        ("disasm-prelude.js", OMP_SRC / "packages/coding-agent/src/disasm/prelude.js"),
        ("legacy-pi-grep-tool.test.js", OMP_SRC / "packages/coding-agent/test/extensibility/legacy-pi-grep-tool.test.js"),
        ("print-mode-signal.js", OMP_SRC / "packages/coding-agent/test/fixtures/print-mode-signal.js"),
    ],
    "json": [
        ("coding-agent-pkg.json", OMP_SRC / "packages/coding-agent/package.json"),
        ("tsconfig.json", OMP_SRC / "packages/coding-agent/tsconfig.json"),
        ("edit-cases.json", REPO_ROOT / "piglets/oh-my-pig/proof/cases/edit.json"),
        ("bash-cases.json", REPO_ROOT / "piglets/oh-my-pig/proof/cases/bash.json"),
    ],
    "py": [
        ("test_retry.py", OMP_SRC / "python/robomp/tests/test_retry.py"),
        ("test_git_ops.py", OMP_SRC / "python/robomp/tests/test_git_ops.py"),
        ("test_pragmas.py", OMP_SRC / "python/robomp/tests/test_pragmas.py"),
        ("test_persona.py", OMP_SRC / "python/robomp/tests/test_persona.py"),
        ("regen.py", OMP_SRC / "crates/shell/src/bash/syntax/suites/data/regen.py"),
        ("check-glm-rankmerge.py", OMP_SRC / "crates/natives/tools/check-glm-rankmerge.py"),
    ],
    "go": [
        ("tool_definition.go", PIG_SRC / "extensions/sdk/tool_definition.go"),
        ("provider.go", PIG_SRC / "extensions/sdk/provider.go"),
        ("footer_data.go", PIG_SRC / "extensions/sdk/footer_data.go"),
        ("compaction_cutpoint.go", PIG_SRC / "extensions/sdk/compaction_cutpoint.go"),
        ("user_bash.go", PIG_SRC / "extensions/sdk/user_bash.go"),
        ("test_faux_excerpt.go", PIG_SRC / "ai/test_faux.go"),
    ],
    "rs": [
        ("pretoken.rs", OMP_SRC / "crates/natives/src/utok/pretoken.rs"),
        ("format.rs", OMP_SRC / "crates/edit/src/modes/hashline/format.rs"),
        ("syntax.rs", OMP_SRC / "crates/edit/src/modes/hashline/syntax.rs"),
        ("clipboard.rs", OMP_SRC / "crates/edit/src/modes/hashline/clipboard.rs"),
        ("mismatch.rs", OMP_SRC / "crates/edit/src/modes/hashline/mismatch.rs"),
        ("preview.rs", OMP_SRC / "crates/edit/src/modes/hashline/preview.rs"),
        # Pass 3 W2 (bare `{` on line 512) and W3 (macro invocation body items on lines 32, 63, 87).
        ("recovery.rs", OMP_SRC / "crates/edit/src/modes/hashline/recovery.rs"),
        ("code.rs", OMP_SRC / "crates/shell/src/bash/syntax/tree/code.rs"),
    ],
    "yaml": [
        ("bun-cache-warm.yml", OMP_SRC / ".github/workflows/bun-cache-warm.yml"),
        ("nix.yml", OMP_SRC / ".github/workflows/nix.yml"),
        ("docker-compose.yml", OMP_SRC / "python/robomp/docker-compose.yml"),
        ("bug_report.yml", OMP_SRC / ".github/ISSUE_TEMPLATE/bug_report.yml"),
        ("feature_request.yml", OMP_SRC / ".github/ISSUE_TEMPLATE/feature_request.yml"),
        ("native-artifacts.yml", OMP_SRC / ".github/actions/native-artifacts/action.yml"),
        ("bun-install.yml", OMP_SRC / ".github/actions/bun-install/action.yml"),
        ("piglet.yaml", REPO_ROOT / "piglets/oh-my-pig/piglet.yaml"),
        # Pass 3 W6 (flow-mapping item at line 786) and W8 (Go-templated YAML).
        ("ci.yml", OMP_SRC / ".github/workflows/ci.yml"),
        ("job.yaml", PIG_SRC / "bench/k8s/cmd/k8sbench/templates/job.yaml"),
    ],
    "md": [
        ("recall.md", OMP_SRC / "docs/tools/recall.md"),
        ("manage_skill.md", OMP_SRC / "docs/tools/manage_skill.md"),
        ("find.md", OMP_SRC / "docs/tools/find.md"),
        ("learn.md", OMP_SRC / "docs/tools/learn.md"),
        ("rewind.md", OMP_SRC / "docs/tools/rewind.md"),
        ("memory_edit.md", OMP_SRC / "docs/tools/memory_edit.md"),
        ("reflect.md", OMP_SRC / "docs/tools/reflect.md"),
        ("omptype-guide.md", OMP_SRC / "docs/omptype-guide.md"),
        ("advise-tool.md", OMP_SRC / "packages/coding-agent/src/prompts/advisor/advise-tool.md"),
        ("boundary-guidance.md", OMP_SRC / "packages/coding-agent/src/prompts/advisor/boundary-guidance.md"),
        ("system.md", OMP_SRC / "packages/coding-agent/src/prompts/advisor/system.md"),
    ],
    # CSS/SCSS was removed from the resolvable set (pass 4 C1); keep only constructed refusal cases.
    "css": [],
    # C/C++ was removed from the resolvable set (pass 3 W8); keep only PiG-authored run.c + constructed
    # refusal fixtures (five vendored Pi files removed per P1).
    "c": [
        ("run.c", PIG_SRC / "test/term-engines/libvterm/run.c"),
    ],
}

# Mandatory anchor lines per real file (guaranteed included even when stride-sampling).
MANDATORY_ANCHORS: dict[str, list[int]] = {
    "recovery.rs": [509, 512, 514],
    "code.rs": [22, 32, 44, 62, 63, 64, 85, 86, 87],
    "ci.yml": [768, 774, 780, 786, 794],
    "job.yaml": [8, 12, 33, 34, 52, 58, 77, 80, 89, 96, 99, 105, 110],
}
# Constructed adversarial cases: every case from findings N1-N5 + pass-1 findings 1-3 + 71 adversarial anchors.
CONSTRUCTED: dict[str, list[dict[str, Any]]] = {
    "ts": [
        {"name": "valid_fn", "ext": ".ts", "text": "function foo(x: number): number {\n  return x + 1;\n}\nconst y = 2;\n", "anchors": [1, 2, 3, 4]},
        {"name": "chained_then_catch", "ext": ".ts", "text": "fetch(url).then((r) => {\n  return r.json();\n})\n  .catch((e) => {\n    log(e);\n  });\n", "anchors": [1, 4]},
        {"name": "multiline_sig", "ext": ".ts", "text": "function foo(\n  a: number,\n  b: number,\n) {\n  return a + b;\n}\n", "anchors": [1, 4]},
        {"name": "continuation_dot", "ext": ".ts", "text": "items\n  .map((x) => {\n    return x;\n  });\n", "anchors": [1, 2]},
        {"name": "template_literal_braces", "ext": ".ts", "text": "function f() {\n  const s = `a } b { c`;\n  return s;\n}\n", "anchors": [1, 2]},
        {"name": "regex_with_brace", "ext": ".ts", "text": "function f(s: string) {\n  return /foo}bar/.test(s);\n}\n", "anchors": [1]},
        {"name": "allman_brace", "ext": ".ts", "text": "if (cond)\n{\n  work();\n}\n", "anchors": [1, 2]},
        {"name": "decorator_above_class", "ext": ".ts", "text": "@Injectable()\nclass Svc {\n  run() {}\n}\n", "anchors": [1, 2]},
        {"name": "allman_else_next_line", "ext": ".ts", "text": "if (a) {\n  x();\n}\nelse {\n  y();\n}\n", "anchors": [1, 4]},
        {"name": "trailing_member_after_brace", "ext": ".ts", "text": "const x = {\n  a: 1,\n}.a;\n", "anchors": [1]},
        {"name": "two_stmts_on_one_line", "ext": ".ts", "text": "foo(); if (x) {\n  bar();\n}\n", "anchors": [1]},
        {"name": "regex_then_obj_same_line", "ext": ".ts", "text": "const re = /[{]/; const obj = {\n  a: 1,\n};\n", "anchors": [1]},
        {"name": "switch_case_brace_then_break", "ext": ".ts", "text": "switch (x) {\n  case 1: {\n    doA();\n  }\n  break;\n  default: {\n    doB();\n  }\n  break;\n}\n", "anchors": [1, 2, 6]},
        {"name": "multiline_arrow_extends_return", "ext": ".ts", "text": "const f = (\n  a,\n): T =>\n  g(a);\n\nclass A extends B<{\n  x: number\n}>\n  implements C {\n}\n\nreturn f(\n  x\n) +\n  \"tail\";\n", "anchors": [1, 6, 9, 12]},
        {"name": "prev_line_operator_cont", "ext": ".ts", "text": "if (a &&\n  b) {\n  work();\n}\n", "anchors": [1, 2]},
        # Pass 3 W1: JS/TS statement ending in `}` without `;` followed by `;`, `(`, `[`, or backtick.
        {"name": "w1_asi_next_line_cont", "ext": ".ts", "text": "const v = {\n  a: 1,\n}\n;[1].forEach(f)\n\nconst w = {\n  a: 1,\n}\n(function () {})()\n\nconst t2 = {\n  a: 1,\n}\n`tpl`\n\nconst z2 = {\n  a: 1,\n}\n[1, 2].map(f)\n", "anchors": [1, 6, 11, 16]},
    ],
    "tsx": [
        {"name": "valid_component_fn", "ext": ".tsx", "text": "export function App() {\n  return <div>hi</div>;\n}\n", "anchors": [1]},
        {"name": "opening_tag", "ext": ".tsx", "text": "function App() {\n  return (\n    <section>\n      <p>hi</p>\n    </section>\n  );\n}\n", "anchors": [1, 2, 3]},
        {"name": "multiline_props_fn", "ext": ".tsx", "text": "function App({\n  title,\n}: Props) {\n  return <div />;\n}\n", "anchors": [1, 3]},
        {"name": "continuation_ternary", "ext": ".tsx", "text": "const x = ok\n  ? (() => {\n    return 1;\n  })()\n  : 0;\n", "anchors": [1, 2]},
        {"name": "template_closer_in_fn", "ext": ".tsx", "text": "function C() {\n  const x = `}`;\n  return x;\n}\n", "anchors": [1]},
        {"name": "decorator_class", "ext": ".tsx", "text": "@observer\nclass View {\n  render() { return null; }\n}\n", "anchors": [1, 2]},
        {"name": "inline_else_chain", "ext": ".tsx", "text": "if (a) {\n  x();\n} else {\n  y();\n}\n", "anchors": [1, 3]},
    ],
    "js": [
        {"name": "backslash_string", "ext": ".js", "text": "function f() {\n  const s = \"hello \\\n  }\";\n  return s;\n}\n", "anchors": [1]},
        {"name": "two_stmts_same_line", "ext": ".js", "text": "foo(); if (x) {\n  bar();\n}\n", "anchors": [1]},
        {"name": "case_brace_break", "ext": ".js", "text": "switch (x) {\n  case 1: {\n    work();\n  }\n  break;\n}\n", "anchors": [1, 2]},
        {"name": "template_and_regex", "ext": ".js", "text": "const s = `hello { world`;\nconst re = /foo{1/g;\nfunction ok() {\n  return s + re;\n}\n", "anchors": [1, 2, 3]},
    ],
    "json": [
        {"name": "nested_objects", "ext": ".json", "text": "{\n  \"a\": {\n    \"x\": 1,\n    \"y\": 2\n  },\n  \"b\": [\n    {\n      \"z\": 3\n    }\n  ]\n}\n", "anchors": [1, 2, 6, 7]},
        {"name": "two_members_same_line", "ext": ".json", "text": "{\n  \"g\": 1, \"h\": {\n    \"k\": 2\n  }\n}\n", "anchors": [1, 2]},
    ],
    "go": [
        {"name": "n1_cont_if_and", "ext": ".go", "text": "package p\n\nfunc f(a, b bool) {\n\tif a &&\n\t\tb {\n\t\twork()\n\t}\n}\n", "anchors": [3, 4, 5]},
        {"name": "n1_cont_for_or", "ext": ".go", "text": "package p\n\nfunc f(a, b bool) {\n\tfor a ||\n\t\tb {\n\t\twork()\n\t}\n}\n", "anchors": [4, 5]},
        {"name": "n1_cont_switch_type", "ext": ".go", "text": "package p\n\nfunc f(h func() any) {\n\tswitch v :=\n\t\th().(type) {\n\tcase int:\n\t\t_ = v\n\t}\n}\n", "anchors": [4, 5, 6]},
        {"name": "n1_two_stmts_same_line", "ext": ".go", "text": "package p\n\nfunc f(c bool) {\n\tg(); if c {\n\t\twork()\n\t}\n}\n", "anchors": [3, 4]},
        {"name": "multiline_if_cond", "ext": ".go", "text": "package p\n\nfunc f() {\n\tif foo(a,\n\t\tb) &&\n\t\tc {\n\t\tx()\n\t}\n}\n", "anchors": [3, 4, 6]},
        {"name": "multiline_sig_chained_lit", "ext": ".go", "text": "package p\n\nfunc foo(\n\ta int,\n\tb int,\n) int {\n\treturn a + b\n}\n\nfunc bar() {\n\tBuilder{\n\t\tx: 1,\n\t}\n\t\t.Build()\n}\n", "anchors": [3, 6, 11]},
        {"name": "valid_func_raw_if_else", "ext": ".go", "text": "package p\n\nfunc f() string {\n\ts := `}`\n\tif ok {\n\t\ta()\n\t} else {\n\t\tb()\n\t}\n\treturn s\n}\n", "anchors": [3, 5, 7]},
        # Pass 3 W5: closed block then `; for {` on same anchor line (preceded by a statement so it's not first in statement_list).
        {"name": "w5_closed_block_then_for", "ext": ".go", "text": "package p\n\nfunc f() {\n\tx := 1\n\tfor i := 0; i < 3; i++ { b() }; for {\n\t\tbreak\n\t}\n\t_ = x\n}\n", "anchors": [3, 5]},
    ],
    "rs": [
        {"name": "n1_cont_if_and_where", "ext": ".rs", "text": "fn f(a: bool, b: bool) {\n    if a &&\n        b {\n        work();\n    }\n}\n\nfn g<T>(x: T) -> T\nwhere\n    T: Clone {\n    x\n}\n", "anchors": [1, 2, 3, 8, 9, 10]},
        {"name": "n1_two_stmts_same_line", "ext": ".rs", "text": "fn f() {\n    let t = S { a: 1 }; let u = S {\n        a: 2,\n    };\n}\n", "anchors": [1, 2]},
        {"name": "n1_match_arm_under_attr", "ext": ".rs", "text": "fn f(x: u32) {\n    match x {\n        0 => {},\n        #[cfg(test)]\n        1 => {\n            work();\n        },\n        _ => {},\n    }\n}\n", "anchors": [1, 2, 4, 5]},
        {"name": "finding2_attrs_and_where", "ext": ".rs", "text": "#[inline]\nfn foo<T>(x: T) -> T\nwhere\n    T: Clone,\n{\n    x\n}\n\n/// Doc.\npub fn bar(\n    a: u32,\n) -> u32\nwhere\n    u32: Copy,\n{\n    a\n}\n\n#[allow(dead_code)]\nconst X: &[u8] =\n    &[1, 2, 3];\n\n#[allow(dead_code)]\nconst Y: u32 = 1;\n", "anchors": [1, 2, 9, 10, 19, 23]},
        {"name": "valid_lifetimes_nested_comments_attrs", "ext": ".rs", "text": "#[derive(Debug)]\nstruct S {\n    x: u32,\n}\n\nfn foo<'a>(s: &'a str) -> &'a str {\n    /* outer /* } */ }\n    still comment */\n    let x = r#\"}\"#;\n    let c = '}';\n    s\n}\n", "anchors": [1, 2, 6]},
        {"name": "chained_match", "ext": ".rs", "text": "fn f(x: Option<i32>) -> String {\n    match x {\n        Some(v) => v,\n        None => 0,\n    }\n        .to_string()\n}\n", "anchors": [1, 2]},
    ],
    "py": [
        {"name": "n4_trailing_indented_comments", "ext": ".py", "text": "class A:\n    def m(self):\n        return 1\n    # trailing comment\n\ndef h():\n    x = 1\n    return x\n\n    # dangling indented comment\n\ndef k():\n    return 2\n        # deeper comment\n    # body-level comment\n\nx = 1\n", "anchors": [1, 2, 6, 12]},
        {"name": "decorators_multiline_docstring", "ext": ".py", "text": "@first\n@second\ndef foo(\n    a: int,\n    b: int,\n) -> int:\n    \"\"\"doc\ncol0\n\"\"\"\n# dedented comment\n    return a + b\n\nif a:\n    x()\nelif b:\n    y()\nelse:\n    z()\n\nif a and \\\n   b:\n    w()\n", "anchors": [1, 2, 3, 13, 20]},
        {"name": "multiline_call_and_single_line", "ext": ".py", "text": "res = foo(\n    a,\n).bar()\n\ndef f(): pass\n\nx = 1\n", "anchors": [1, 5, 7]},
        # Pass 3 W4: later decorator after multi-line decorator whose `])` ends at same indent.
        {"name": "w4_later_decorator_after_multiline", "ext": ".py", "text": "@a.b(c=[\n    1,\n])\n@d\nclass K(B,\n        metaclass=M):\n    pass\n", "anchors": [1, 4, 5]},
    ],
    "yaml": [
        {"name": "n3_trailing_comments", "ext": ".yaml", "text": "discovery:\n  a: 1\n  b: 2\n\n# comment after discovery\n# second comment\nnext:\n  c: 3\n", "anchors": [1, 7]},
        {"name": "n3_list_trailing_comment", "ext": ".yaml", "text": "rules:\n  - linters: [gosec]\n    text: foo\n  # comment after item\n  - linters: [errcheck]\n    text: bar\n", "anchors": [1, 2, 5]},
        {"name": "n3_inside_block_scalar", "ext": ".yaml", "text": "config: |\n  paths:\n    - /a\n    - /b\nother:\n  x: 1\n", "anchors": [1, 2, 3, 5]},
        {"name": "n3_same_indent_seq_and_scalars", "ext": ".yaml", "text": "steps:\n- run: a\n- run: b\nnext: 1\ndesc: first line\n  second line\nitems: [\n  # ]\n  1,\n]\n", "anchors": [1, 2, 4, 5, 7]},
        # Pass 4 Y1: constructed shapes matching the three reviewer EOF-blank-line YAML files (LF and CRLF) + doc markers.
        {"name": "y1_workflow_lint_eof_blank_lf", "ext": ".yml", "text": "name: lint\n\njobs:\n  linter:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - name: run-linter\n        uses: example/lint-action@v1\n\n", "anchors": [3, 4, 6, 8]},
        {"name": "y1_workflow_lint_eof_blank_crlf", "ext": ".yml", "text": "name: lint\r\n\r\njobs:\r\n  linter:\r\n    runs-on: ubuntu-latest\r\n    steps:\r\n      - uses: actions/checkout@v4\r\n      - name: run-linter\r\n        uses: example/lint-action@v1\r\n\r\n", "anchors": [3, 4, 6, 8]},
        {"name": "y1_workflow_test_eof_blank_lf", "ext": ".yml", "text": "name: test\n\njobs:\n  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - name: test\n        run: go test ./...\n\n", "anchors": [3, 4, 6, 8]},
        {"name": "y1_workflow_test_eof_blank_crlf", "ext": ".yml", "text": "name: test\r\n\r\njobs:\r\n  unit:\r\n    runs-on: ubuntu-latest\r\n    steps:\r\n      - uses: actions/checkout@v4\r\n      - name: test\r\n        run: go test ./...\r\n\r\n", "anchors": [3, 4, 6, 8]},
        {"name": "y1_service_rules_eof_blank_lf", "ext": ".yaml", "text": "type: example.api.Service\n\nhttp:\n  rules:\n    - selector: example.Echo\n      post: /v1/echo\n    - selector: example.EchoDelete\n      delete: /v1/echo\n\n", "anchors": [3, 4, 7]},
        {"name": "y1_service_rules_eof_blank_crlf", "ext": ".yaml", "text": "type: example.api.Service\r\n\r\nhttp:\r\n  rules:\r\n    - selector: example.Echo\r\n      post: /v1/echo\r\n    - selector: example.EchoDelete\r\n      delete: /v1/echo\r\n\r\n", "anchors": [3, 4, 7]},
        {"name": "y1_doc_markers_refuse", "ext": ".yaml", "text": "first:\n  a: 1\n\n---\nsecond:\n  b: 2\n...\n", "anchors": [1, 5]},
    ],
    "md": [
        {"name": "n2_trailing_blanks_and_eof", "ext": ".md", "text": "# A\nfirst\n\n## B\nbody\n\n\n# H\nend\n\n\n", "anchors": [1, 4, 8]},
        {"name": "n2_setext_inside_section", "ext": ".md", "text": "# A\ntext\n\nTitle\n=====\nbody\n\n# C\nend\n", "anchors": [1, 4, 8]},
        {"name": "n2_html_and_fences", "ext": ".md", "text": "# Real\n```md\n# NotAHeading\n```\nAfter\n\n# Next\n<!--\n# hidden\n-->\ntext\n# End\n", "anchors": [1, 3, 7, 12]},
        {"name": "n2_indented_heading_under_list", "ext": ".md", "text": "## Top\n- item\n  ## Nested\n  more\n\n## Sibling\n", "anchors": [1, 3, 6]},
        {"name": "n2_front_matter", "ext": ".md", "text": "---\ntitle: hi\n---\n# A\nbody\n\n# B\ntail\n", "anchors": [1, 4, 7]},
    ],
    "css": [
        {"name": "n5_selector_list", "ext": ".css", "text": "a,\nb {\n  color: red;\n}\n\n@keyframes spin {\n  0%,\n  50% {\n    opacity: 0;\n  }\n  100% {\n    opacity: 1;\n  }\n}\n\n.solo {\n  margin: 0;\n}\n", "anchors": [1, 2, 6, 7, 8, 11, 16]},
        {"name": "w7_leading_comma_selector", "ext": ".css", "text": "g\n,h {\n  i: j;\n}\n\nbody {\n  margin: 0;\n}\n\n.a\n> .b {\n  color: red;\n}\n", "anchors": [1, 2, 6, 10, 11]},
        {"name": "w8_custom_prop_block", "ext": ".css", "text": ":root {\n  --a: { b };\n}\n\nhtml {\n  color: red;\n}\n", "anchors": [1, 5]},
        # Pass 4 C1: CSS/SCSS constructs omp rejects (content "{", @container, @page, &-suffix, SCSS @mixin).
        {"name": "c1_css_and_scss_refused", "ext": ".css", "text": ".q::before {\n  content: \"{\";\n}\n\n.after {\n  color: red;\n}\n\n@container sidebar (min-width: 400px) {\n  .card { padding: 8px; }\n}\n\n@page :first {\n  margin: 1cm;\n}\n", "anchors": [1, 5, 9, 13]},
        {"name": "c1_scss_mixin_refused", "ext": ".scss", "text": "@mixin box($pad) {\n  padding: $pad;\n}\n\n.btn {\n  @include box(4px);\n  &-primary {\n    color: blue;\n  }\n}\n", "anchors": [1, 5, 7]},
    ],
    "c": [
        {"name": "n1_ifdef_split_body", "ext": ".c", "text": "void f(int a) {\n#ifdef X\n    if (a) {\n#else\n    if (!a) {\n#endif\n        work();\n    }\n}\n\nvoid g(void) {\n    done();\n}\n", "anchors": [1, 3, 5, 11]},
        {"name": "n1_extern_c", "ext": ".h", "text": "#ifdef __cplusplus\nextern \"C\" {\n#endif\nint foo(void);\n#ifdef __cplusplus\n}\n#endif\n", "anchors": [2]},
        {"name": "n1_case_brace_break", "ext": ".c", "text": "void f(int x) {\n    switch (x) {\n    case 1: {\n        work();\n    }\n    break;\n    }\n}\n", "anchors": [1, 2, 3]},
        {"name": "finding1_multiline_allman_typedef", "ext": ".c", "text": "static int foo(int a,\n    int b)\n{\n    return a + b;\n}\n\ntypedef struct {\n    int x;\n} Point;\n\nint ok(int a, int b) {\n    /* } */\n    return a + b;\n}\n", "anchors": [1, 3, 7, 11]},
    ],
    "refused_ext": [
        {"name": "jsonc", "ext": ".jsonc", "text": "{\n  // comment\n  \"a\": {\n    \"b\": 1\n  }\n}\n", "anchors": [1, 3]},
        {"name": "hxx", "ext": ".hxx", "text": "struct S {\n    int x;\n};\n", "anchors": [1]},
        {"name": "pyw", "ext": ".pyw", "text": "def f():\n    return 1\n", "anchors": [1]},
        {"name": "proto", "ext": ".proto", "text": "syntax = \"proto3\";\nmessage A {\n  string id = 1;\n}\n", "anchors": [2]},
        {"name": "sol", "ext": ".sol", "text": "contract C {\n    uint x;\n}\n", "anchors": [1]},
        {"name": "zig", "ext": ".zig", "text": "fn main() void {\n    const x = 1;\n}\n", "anchors": [1]},
        {"name": "star", "ext": ".star", "text": "def f():\n    return 1\n", "anchors": [1]},
        {"name": "bzl", "ext": ".bzl", "text": "def f():\n    return 1\n", "anchors": [1]},
    ],
}
SAMPLE23_LANGS = ["ts", "tsx", "js", "json", "go", "py", "md", "rs", "yaml"]

LANG_EXTS: dict[str, set[str]] = {
    "ts": {".ts", ".mts", ".cts"},
    "tsx": {".tsx", ".jsx"},
    "js": {".js", ".mjs", ".cjs"},
    "json": {".json"},
    "go": {".go"},
    "py": {".py", ".pyi"},
    "md": {".md", ".markdown", ".mdx"},
    "rs": {".rs"},
    "yaml": {".yml", ".yaml"},
}

# Exclude vendored/third-party trees, upstream copies, fixtures, and generated/test trees (P1, P2).
EXCLUDE_DIRS = {
    "vendor",
    "third_party",
    "node_modules",
    "pi-dist",
    "shims",
    "port",
    "upstream",
    "fixtures",
    ".git",
    "dist",
    "target",
    "testdata",
    "__pycache__",
    ".venv",
    "venv",
}

AUTOGEN_MARKERS = (
    "code generated",
    "do not edit",
    "@generated",
    "automatically generated",
    "auto-generated",
    "generated by",
)

THIRD_PARTY_NOTICE_RE = re.compile(r"@license|copyright|\(c\)\s*\d{4}", re.I)
OWN_COPYRIGHT_MARKERS = (
    "can bölük",
    "can boluk",
    "stencil labs",
    "oh-my-pi",
    "michael kinsy",
    "michaelkinsy",
    "pigpen",
)


def has_third_party_notice(text: str) -> bool:
    """Skip any file whose first 30 lines carry a third-party @license or Copyright notice."""
    for line in text.splitlines()[:30]:
        if THIRD_PARTY_NOTICE_RE.search(line):
            low = line.lower()
            if not any(m in low for m in OWN_COPYRIGHT_MARKERS):
                return True
    return False


def refusal_kind(msg: str) -> str:
    """Classify an omp block-op refusal line into a short stable kind."""
    if "resolved a single-line block" in msg:
        return "single_line"
    if "is blank; no syntactic block" in msg:
        return "blank"
    if "block locators need a tree-sitter grammar" in msg:
        return "no_grammar"
    if "could not resolve a syntactic block" in msg:
        return "unresolved"
    return "refused"

def sample_anchors(lang: str, text: str, max_anchors: int = 80, mandatory: list[int] | None = None, mix_uniform: bool = False) -> list[int]:
    """Select candidate anchor lines from a real file, always preserving mandatory anchors."""
    lines = text.split("\n")
    if text.endswith("\n") and lines:
        lines = lines[:-1]
    candidates: list[int] = []
    nonblank: list[int] = []
    for idx, raw in enumerate(lines):
        ln = idx + 1
        t = raw.strip()
        if not t:
            continue
        nonblank.append(ln)
        if lang == "md":
            if t.startswith("#") or t.startswith("```") or (mix_uniform and (t.startswith("- ") or t.startswith("1."))) or (idx + 1 < len(lines) and lines[idx + 1].strip() in ("===", "---", "=====", "-----")):
                candidates.append(ln)
        elif lang == "py":
            if t.startswith(("def ", "async def ", "class ", "if ", "for ", "async for ", "while ", "with ", "async with ", "try:", "match ", "@")) or t.endswith(":"):
                candidates.append(ln)
        elif lang == "yaml":
            if not t.startswith("#") and (":" in t or t.startswith("- ") or t == "-"):
                candidates.append(ln)
        elif lang == "json":
            if "{" in t or "[" in t:
                candidates.append(ln)
        else:
            if "{" in t or t.startswith(("#[", "///", "/**", "fn ", "pub ", "func ", "function ", "export ", "class ", "if ", "for ", "while ", "switch ", "case ", "match ", "struct ", "enum ", "impl ", "type ", "void ", "int ", "static ", "extern ", "@media", "@keyframes")):
                candidates.append(ln)

    picked: list[int] = []
    seen: set[int] = set()
    for m in mandatory or []:
        if 1 <= m <= len(lines) and m not in seen:
            seen.add(m)
            picked.append(m)

    if mix_uniform:
        # 70% structural candidates + 30% uniformly strided non-blank lines (broadens coverage).
        c_quota = max(1, int(max_anchors * 0.7)) - len(picked)
        c_pool = [x for x in candidates if x not in seen]
        if c_quota > 0:
            if len(c_pool) <= c_quota:
                for v in c_pool:
                    seen.add(v)
                    picked.append(v)
            else:
                step = len(c_pool) / c_quota
                for i in range(c_quota):
                    v = c_pool[int(i * step)]
                    if v not in seen:
                        seen.add(v)
                        picked.append(v)
        rem = max_anchors - len(picked)
        if rem > 0:
            u_pool = [x for x in nonblank if x not in seen]
            if len(u_pool) <= rem:
                for v in u_pool:
                    seen.add(v)
                    picked.append(v)
            else:
                step = len(u_pool) / rem
                for i in range(rem):
                    v = u_pool[int(i * step)]
                    if v not in seen:
                        seen.add(v)
                        picked.append(v)
        return sorted(picked)

    rem = max_anchors - len(picked)
    c_pool = [x for x in candidates if x not in seen]
    if rem > 0:
        if len(c_pool) <= rem:
            for v in c_pool:
                seen.add(v)
                picked.append(v)
        else:
            step = len(c_pool) / rem
            for i in range(rem):
                v = c_pool[int(i * step)]
                if v not in seen:
                    seen.add(v)
                    picked.append(v)
    return sorted(picked)


def run_omp_batch(work_items: list[tuple[str, str, list[int]]]) -> dict[tuple[str, int], dict[str, Any]]:
    """Run real omp (via oracle.sh, omp-only) on a batch of (rel_path, content, anchors).

    Copies each file in `setup` to a private probe filename per anchor, computes its exact
    4-hex content hash, and issues `CUT N*` in parallel turns (up to 16 parallel calls per
    turn, 80 calls per omp process) so hundreds of anchors run in seconds.
    Raises if any probe fails with a non-block error (timeout, missing file, etc.).
    """
    results: dict[tuple[str, int], dict[str, Any]] = {}
    with tempfile.TemporaryDirectory(prefix="omp-blockgen-") as tmp:
        tmp_path = Path(tmp)
        cases_dir = tmp_path / "cases"
        cases_dir.mkdir()
        out_dir = tmp_path / "out"

        flat_probes: list[tuple[str, str, int, bool]] = []
        for rel, content, anchors in work_items:
            has_wide = any(len(line.encode("utf-8")) > 512 for line in content.splitlines())
            for a in anchors:
                flat_probes.append((rel, content, a, has_wide))

        cases: list[dict[str, Any]] = []
        probe_meta: dict[str, list[tuple[str, int, int]]] = {}

        idx = 0
        case_num = 0
        while idx < len(flat_probes):
            case_name = f"batch-{case_num:04d}"
            case_num += 1
            files_dir = tmp_path / "src" / case_name
            files_dir.mkdir(parents=True, exist_ok=True)

            setup_lines: list[str] = []
            turns: list[dict[str, Any]] = []
            written_src: dict[str, Path] = {}
            meta_list: list[tuple[str, int, int]] = []
            probe_i = 0
            flat_call_idx = 0

            # Pack up to 80 probes per case (5 parallel turns of 16) for fast non-wide probes,
            # or 24 sequential calls when wide-line pre-reads are needed.
            batch_items: list[tuple[str, str, int, bool]] = []
            batch_has_wide = False
            while idx < len(flat_probes):
                item = flat_probes[idx]
                if batch_items and (batch_has_wide or item[3]):
                    if len(batch_items) >= 12:
                        break
                elif len(batch_items) >= 80:
                    break
                batch_items.append(item)
                if item[3]:
                    batch_has_wide = True
                idx += 1

            if batch_has_wide:
                # Sequential read + edit turns when any probe needs wide-line read first.
                for rel, content, anchor, has_wide in batch_items:
                    vrel = rel[:-4] if rel.endswith(".src") else rel
                    ext = Path(vrel).suffix
                    probe_name = f"p{probe_i:03d}{ext}"
                    probe_i += 1
                    tag = xxh32_file_hash(content)
                    if rel not in written_src:
                        src_host = files_dir / f"s{len(written_src):03d}{ext}"
                        src_host.write_bytes(content.encode("utf-8"))
                        written_src[rel] = src_host
                    setup_lines.append(f"cp {sh_quote(str(written_src[rel]))} {sh_quote(probe_name)}")
                    if has_wide:
                        line_count = max(1, len(content.splitlines()))
                        turns.append({
                            "tool": "read",
                            "arguments": {"path": f"{probe_name}:1-{line_count}:raw"},
                        })
                        flat_call_idx += 1
                    edit_call_idx = flat_call_idx
                    turns.append({
                        "tool": "edit",
                        "arguments": {"input": f"[{probe_name}#{tag}]\nCUT {anchor}*"},
                    })
                    flat_call_idx += 1
                    meta_list.append((rel, anchor, edit_call_idx))
            else:
                for t_start in range(0, len(batch_items), 16):
                    chunk = batch_items[t_start : t_start + 16]
                    pcalls: list[dict[str, Any]] = []
                    for rel, content, anchor, _ in chunk:
                        vrel = rel[:-4] if rel.endswith(".src") else rel
                        ext = Path(vrel).suffix
                        probe_name = f"p{probe_i:03d}{ext}"
                        probe_i += 1
                        tag = xxh32_file_hash(content)
                        if rel not in written_src:
                            src_host = files_dir / f"s{len(written_src):03d}{ext}"
                            src_host.write_bytes(content.encode("utf-8"))
                            written_src[rel] = src_host
                        setup_lines.append(f"cp {sh_quote(str(written_src[rel]))} {sh_quote(probe_name)}")
                        edit_call_idx = flat_call_idx
                        pcalls.append({
                            "tool": "edit",
                            "arguments": {"input": f"[{probe_name}#{tag}]\nCUT {anchor}*"},
                        })
                        flat_call_idx += 1
                        meta_list.append((rel, anchor, edit_call_idx))
                    turns.append({"parallel": pcalls})

            case_obj: dict[str, Any] = {
                "name": case_name,
                "description": f"Block-span oracle batch {case_name}",
                "calls": turns,
            }
            if setup_lines:
                case_obj["setup"] = " && ".join(setup_lines)
            cases.append(case_obj)
            probe_meta[case_name] = meta_list

        case_file = cases_dir / "blockspans.json"
        case_file.write_text(json.dumps(cases), encoding="utf-8")

        env = os.environ.copy()
        env["ORACLE_MODE"] = "omp-only"
        env["ORACLE_OUT"] = str(out_dir)
        env["ORACLE_TIMEOUT"] = "120"
        proc = subprocess.run(
            ["bash", str(ORACLE_SH), str(case_file)],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        report_path = out_dir / "report.json"
        if not report_path.exists():
            raise RuntimeError(f"oracle.sh failed (rc={proc.returncode}):\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}")
        report = json.loads(report_path.read_text(encoding="utf-8"))
        for c_rep in report.get("cases", []):
            cname = c_rep["name"]
            c_calls = c_rep.get("calls", [])
            for rel, anchor, edit_idx in probe_meta.get(cname, []):
                if edit_idx >= len(c_calls):
                    raise RuntimeError(f"{cname} missing call {edit_idx} for {rel}:{anchor}")
                call = c_calls[edit_idx]
                txt = call.get("omp_normalized") or ""
                is_err = bool(call.get("omp_is_error"))
                m = RESOLVED_RE.search(txt) if not is_err else None
                if m:
                    s = int(m.group(1))
                    e = int(m.group(2)) if m.group(2) else s
                    results[(rel, anchor)] = {"span": [s, e]}
                else:
                    first_line = txt.splitlines()[0] if txt else "(empty)"
                    if not (
                        "could not resolve a syntactic block" in first_line
                        or "resolved a single-line block" in first_line
                        or "is blank; no syntactic block" in first_line
                    ):
                        raise RuntimeError(
                            f"Unexpected non-block outcome for {rel}:{anchor} (is_err={is_err}): {txt!r}"
                        )
                    results[(rel, anchor)] = {"refused": first_line}

    return results


def sh_quote(s: str) -> str:
    return "'" + s.replace("'", "'\"'\"'") + "'"


def collect_sample1_hashes() -> set[str]:
    """Return SHA-256 hashes of all Sample 1 files (checked-in and configured REAL_FILES)."""
    hashes: set[str] = set()
    for lang_items in REAL_FILES.values():
        for _, p in lang_items:
            if p.exists():
                hashes.add(hashlib.sha256(p.read_bytes()).hexdigest())
    if FILES_ROOT.exists():
        for lang_dir in FILES_ROOT.iterdir():
            if not lang_dir.is_dir() or lang_dir.name in ("sample2", "sample3"):
                continue
            for f in lang_dir.rglob("*"):
                if f.is_file():
                    hashes.add(hashlib.sha256(f.read_bytes()).hexdigest())
    return hashes


def discover_disjoint_samples(lang: str, exclude_hashes: set[str], target_anchors: int = 440) -> tuple[list[tuple[str, Path, list[int]]], list[tuple[str, Path, list[int]]]]:
    """Deterministically select two disjoint fresh file sets (sample2, sample3) for `lang`.

    Only searches omp v19.1.1 (MIT) and PiG/Pigpen-authored trees, excluding all vendored
    or third-party paths (`EXCLUDE_DIRS`) and deduplicating by SHA-256 against `exclude_hashes`.
    Returns (sample2_items, sample3_items) where each item is (fixture_filename, src_path, anchors).
    """
    exts = LANG_EXTS[lang]
    seen = set(exclude_hashes)
    candidates: list[tuple[str, Path, str]] = []
    for root_name, root in [("omp", OMP_SRC), ("pig", PIG_SRC), ("pigpen", REPO_ROOT)]:
        for dirpath, dirnames, filenames in os.walk(root):
            parts = Path(dirpath).parts
            if any(ex in parts for ex in EXCLUDE_DIRS):
                dirnames[:] = []
                continue
            dirnames[:] = sorted(d for d in dirnames if d not in EXCLUDE_DIRS and d != ".git")
            for fn in sorted(filenames):
                if fn.endswith(("lock.yaml", "lock.json", "-lock.json", "package-lock.json")):
                    continue
                p = Path(dirpath) / fn
                if p.suffix.lower() not in exts:
                    continue
                try:
                    b = p.read_bytes()
                except OSError:
                    continue
                if len(b) < 120 or len(b) > 150000:
                    continue
                h = hashlib.sha256(b).hexdigest()
                if h in seen:
                    continue
                seen.add(h)
                txt = b.decode("utf-8", errors="replace")
                if "\x00" in txt:
                    continue
                head = "\n".join(txt.splitlines()[:10]).lower()
                if any(m in head for m in AUTOGEN_MARKERS):
                    continue
                if has_third_party_notice(txt):
                    continue
                if lang != "yaml" and any(len(l.encode("utf-8")) > 500 for l in txt.splitlines()):
                    continue
                if len(txt.splitlines()) < 15:
                    continue
                rel_slug = f"{root_name}_{str(p.relative_to(root)).replace('/', '__')}"
                candidates.append((rel_slug, p, txt))

    rng = random.Random(f"sample23-{lang}-v1")
    rng.shuffle(candidates)
    cap = 60 if lang == "yaml" else 35
    s2: list[tuple[str, Path, list[int]]] = []
    s3: list[tuple[str, Path, list[int]]] = []
    s2_cnt = 0
    s3_cnt = 0
    for slug, p, txt in candidates:
        anchors = sample_anchors(lang, txt, max_anchors=cap, mix_uniform=True)
        if not anchors:
            continue
        if s2_cnt < target_anchors:
            s2.append((slug, p, anchors))
            s2_cnt += len(anchors)
        elif s3_cnt < target_anchors:
            s3.append((slug, p, anchors))
            s3_cnt += len(anchors)
        else:
            break
    return s2, s3


def write_fixture_doc(out_json: Path, lang: str, sample_label: str, fixture_files: list[dict[str, Any]], outcomes: dict[tuple[str, int], dict[str, Any]]) -> None:
    cases_out: list[dict[str, Any]] = []
    resolved_cnt = 0
    refused_cnt = 0
    refusal_samples: dict[str, str] = {}
    for meta in fixture_files:
        rel = meta["file"]
        for anchor in meta["anchors"]:
            out = outcomes[(rel, anchor)]
            entry: dict[str, Any] = {
                "file": rel,
                "anchor": anchor,
            }
            if meta["virtual_path"] != Path(rel).name:
                entry["path"] = meta["virtual_path"]
            if meta.get("constructed"):
                entry["constructed"] = True
                entry["name"] = meta["name"]
            if "span" in out:
                entry["span"] = out["span"]
                resolved_cnt += 1
            else:
                msg = out["refused"]
                kind = refusal_kind(msg)
                if kind not in refusal_samples:
                    refusal_samples[kind] = msg
                    entry["refused"] = msg
                else:
                    entry["refused_kind"] = kind
                refused_cnt += 1
            cases_out.append(entry)

    doc = {
        "lang": lang,
        "sample": sample_label,
        "omp_tag": "v19.1.1",
        "generator": f"python3 -B internal/hashline/gen/gen_blockspans.py --sample {sample_label}",
        "total": len(cases_out),
        "omp_resolved": resolved_cnt,
        "omp_refused": refused_cnt,
        "refusal_samples": refusal_samples,
        "cases": cases_out,
    }
    out_json.write_text(json.dumps(doc, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"  Wrote {out_json.name}: total={len(cases_out)} omp_resolved={resolved_cnt} omp_refused={refused_cnt}", flush=True)

def generate_sample1(wanted: set[str] | None, max_anchors_per_file: int) -> None:
    all_langs = list(dict.fromkeys(list(REAL_FILES.keys()) + list(CONSTRUCTED.keys())))
    for lang in all_langs:
        if wanted and lang not in wanted:
            continue
        print(f"=== Sample 1: {lang} ===", flush=True)
        lang_dir = FILES_ROOT / lang
        lang_dir.mkdir(parents=True, exist_ok=True)

        work_items: list[tuple[str, str, list[int]]] = []
        fixture_files: list[dict[str, Any]] = []

        for name, src_path in REAL_FILES.get(lang, []):
            if not src_path.exists():
                print(f"  WARN: missing {src_path}", file=sys.stderr)
                continue
            raw = src_path.read_text(encoding="utf-8", errors="replace")
            if name == "test_faux_excerpt.go":
                lines = raw.splitlines(keepends=True)
                content = "package ai\n\nfunc excerpt(lastText string, messages any, builder any, turn any, ctx any) {\n" + "".join(lines[201:228]) + "}\n"
            else:
                content = raw
            fname = name + ".src" if name.endswith(".go") else name
            dst = lang_dir / fname
            dst.write_text(content, encoding="utf-8")
            rel = f"files/{lang}/{fname}"
            anchors = sample_anchors(lang, content, max_anchors=max_anchors_per_file, mandatory=MANDATORY_ANCHORS.get(name))
            if anchors:
                work_items.append((rel, content, anchors))
                fixture_files.append({
                    "name": name,
                    "virtual_path": name,
                    "source": str(src_path.name),
                    "file": rel,
                    "anchors": anchors,
                })

        for item in CONSTRUCTED.get(lang, []):
            vname = f"constructed_{item['name']}{item['ext']}"
            fname = vname + ".src" if vname.endswith(".go") else vname
            dst = lang_dir / fname
            dst.write_bytes(item["text"].encode("utf-8"))
            rel = f"files/{lang}/{fname}"
            anchors = list(item["anchors"])
            work_items.append((rel, item["text"], anchors))
            fixture_files.append({
                "name": item["name"],
                "virtual_path": vname,
                "constructed": True,
                "file": rel,
                "anchors": anchors,
            })

        total_anchors = sum(len(a) for _, _, a in work_items)
        print(f"  Running omp on {len(work_items)} files / {total_anchors} anchors...", flush=True)
        outcomes = run_omp_batch(work_items)
        write_fixture_doc(OUT_ROOT / f"{lang}.json", lang, "sample1", fixture_files, outcomes)


def generate_fresh_sample(sample_num: int, wanted: set[str] | None, target_anchors: int) -> None:
    sample_label = f"sample{sample_num}"
    s1_hashes = collect_sample1_hashes()
    for lang in SAMPLE23_LANGS:
        if wanted and lang not in wanted:
            continue
        s2_items, s3_items = discover_disjoint_samples(lang, s1_hashes, target_anchors=target_anchors)
        chosen = s2_items if sample_num == 2 else s3_items
        print(f"=== {sample_label}: {lang} ({len(chosen)} files) ===", flush=True)
        lang_dir = FILES_ROOT / sample_label / lang
        if lang_dir.exists():
            shutil.rmtree(lang_dir)
        lang_dir.mkdir(parents=True, exist_ok=True)

        work_items: list[tuple[str, str, list[int]]] = []
        fixture_files: list[dict[str, Any]] = []
        for slug, src_path, anchors in chosen:
            content = src_path.read_text(encoding="utf-8", errors="replace")
            fname = slug + ".src" if slug.endswith(".go") else slug
            dst = lang_dir / fname
            dst.write_text(content, encoding="utf-8")
            rel = f"files/{sample_label}/{lang}/{fname}"
            work_items.append((rel, content, anchors))
            fixture_files.append({
                "name": slug,
                "virtual_path": slug,
                "source": str(src_path),
                "file": rel,
                "anchors": anchors,
            })

        total_anchors = sum(len(a) for _, _, a in work_items)
        print(f"  Running omp on {len(work_items)} files / {total_anchors} anchors...", flush=True)
        outcomes = run_omp_batch(work_items)
        write_fixture_doc(OUT_ROOT / f"{sample_label}_{lang}.json", lang, sample_label, fixture_files, outcomes)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--sample", default="1", choices=("1", "2", "3", "all"), help="Which sample to generate")
    parser.add_argument("--langs", default="", help="Comma-separated subset of langs to regenerate")
    parser.add_argument("--max-anchors-per-file", type=int, default=60)
    parser.add_argument("--target-anchors", type=int, default=440, help="Target anchors per language for samples 2 and 3")
    args = parser.parse_args()

    wanted = set(x.strip() for x in args.langs.split(",") if x.strip()) if args.langs else None
    OUT_ROOT.mkdir(parents=True, exist_ok=True)
    FILES_ROOT.mkdir(parents=True, exist_ok=True)

    if args.sample in ("1", "all"):
        generate_sample1(wanted, args.max_anchors_per_file)
    if args.sample in ("2", "all"):
        generate_fresh_sample(2, wanted, args.target_anchors)
    if args.sample in ("3", "all"):
        generate_fresh_sample(3, wanted, args.target_anchors)


if __name__ == "__main__":
    main()
