#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
# Rebuilds the argument-validation differential corpus from omp v19.1.1 ($OMP_SRC):
#   shim (mknm.py) -> real wire schemas -> cases (gen.py) -> omp verdicts (oracle.ts).
# Writes ONLY under $DIFF_OUT (default: /tmp/omp-intent-diff). About 20 seconds.
set -euo pipefail

EXPECTED_TAG="v19.1.1"
EXPECTED_VERSION="19.1.1"
EXPECTED_COMMIT="31ffee513f27a01bba7d4dfbe12bae239cdc646a"

here="$(cd "$(dirname "$0")" && pwd)"
OMP_SRC="${OMP_SRC:-$HOME/work/omp-v19.1.1}"
DIFF_OUT="${DIFF_OUT:-/tmp/omp-intent-diff}"
# Third-party packages the coding-agent sources import (loaded, not exercised by validation).
# Workspace packages (@oh-my-pi/*) always resolve to $OMP_SRC through $DIFF_OUT/nm.
THIRD_NM="${OMP_DIFF_NODE_MODULES:-$HOME/.bun/install/global/node_modules}"

verify_omp_src() {
  if [ ! -d "$OMP_SRC" ] || [ ! -f "$OMP_SRC/packages/coding-agent/package.json" ]; then
    echo "error: OMP_SRC=$OMP_SRC does not exist or is not an omp source checkout." >&2
    echo "       Set OMP_SRC to omp tag $EXPECTED_TAG (commit $EXPECTED_COMMIT)." >&2
    exit 2
  fi
  local ver
  ver="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("version",""))' "$OMP_SRC/packages/coding-agent/package.json")"
  if [ "$ver" != "$EXPECTED_VERSION" ]; then
    echo "error: OMP_SRC=$OMP_SRC is version '$ver', expected '$EXPECTED_VERSION' (tag $EXPECTED_TAG, commit $EXPECTED_COMMIT)." >&2
    exit 2
  fi
  if [ -e "$OMP_SRC/.git" ]; then
    local rev
    rev="$(git -C "$OMP_SRC" rev-parse HEAD 2>/dev/null || true)"
    if [ -n "$rev" ] && [ "$rev" != "$EXPECTED_COMMIT" ]; then
      echo "error: OMP_SRC=$OMP_SRC is at commit $rev, expected tag $EXPECTED_TAG (commit $EXPECTED_COMMIT)." >&2
      exit 2
    fi
  fi
}

verify_omp_src
command -v bun >/dev/null 2>&1 || { echo "error: bun not found on PATH (tested with bun 1.4.2)" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "error: python3 not found on PATH" >&2; exit 2; }
if [ ! -d "$THIRD_NM" ]; then
  echo "error: third-party node_modules not found at OMP_DIFF_NODE_MODULES=$THIRD_NM (install global bun deps; see README.md)" >&2
  exit 2
fi

mkdir -p "$DIFF_OUT"
export OMP_SRC DIFF_OUT

python3 "$here/mknm.py" "$DIFF_OUT/nm"
(
  cd "$OMP_SRC"
  NODE_PATH="$DIFF_OUT/nm:$THIRD_NM" bun --no-install "$here/oracle.ts" schemas "$DIFF_OUT/real_wire.json"
)
python3 "$here/gen.py" "$DIFF_OUT/real_wire.json" "$DIFF_OUT/cases.jsonl"
(
  cd "$OMP_SRC"
  NODE_PATH="$DIFF_OUT/nm:$THIRD_NM" bun --no-install "$here/oracle.ts" run "$DIFF_OUT/cases.jsonl" "$DIFF_OUT/corpus.jsonl"
)
wc -l "$DIFF_OUT/corpus.jsonl"
shasum -a 256 "$DIFF_OUT/corpus.jsonl"
