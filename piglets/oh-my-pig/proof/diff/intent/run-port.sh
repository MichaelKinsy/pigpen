#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
# Usage: run-port.sh [--host] [corpus.jsonl] [results.jsonl]
# Runs the port over the corpus through the extension's real registered tools, WITHOUT writing in the tree:
# go/zz_ompdiff*_test.go are overlaid onto internal/intent (`go test -overlay`) using a temporary go.mod.
#   (no flag)  what PrepareArguments returns (extension SDK only)
#   --host     also PiG's real host validator (`ai.ValidateToolArgumentsJSON`, -tags pighost)
set -euo pipefail
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE PIG_SOURCE_ROOT

here="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$here/../../../../.." && pwd)"
ext_dir="${EXT_DIR:-$repo_root/components/oh-my-pig/extensions/oh-my-pig}"
PIG_SRC="${PIG_SRC:-$HOME/work/pig-080-link13}"
DIFF_OUT="${DIFF_OUT:-/tmp/omp-intent-diff}"

host=0
if [ "${1:-}" = "--host" ]; then
  host=1
  shift
fi

abspath() {
  case "$1" in
    /*) printf '%s' "$1" ;;
    "") ;;
    *)  printf '%s/%s' "$PWD" "$1" ;;
  esac
}

corpus="$(abspath "${1:-$DIFF_OUT/corpus.jsonl}")"
results="$(abspath "${2:-}")"

if [ ! -f "$corpus" ]; then
  echo "error: corpus not found: $corpus (run $here/make-corpus.sh first)" >&2
  exit 2
fi
if [ ! -d "$ext_dir" ]; then
  echo "error: extension module not found: $ext_dir" >&2
  exit 2
fi
if [ ! -d "$PIG_SRC/extensions/sdk" ]; then
  echo "error: PIG_SRC=$PIG_SRC does not contain extensions/sdk (set PIG_SRC to your PiG checkout)" >&2
  exit 2
fi

MODDIR="$(mktemp -d)"
trap 'rm -rf "$MODDIR"' EXIT

cd "$ext_dir"
mod="$MODDIR/go.mod"
cp go.mod "$mod"
[ -f go.sum ] && cp go.sum "${mod%.mod}.sum"
printf '\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "$PIG_SRC/extensions/sdk" >> "$mod"

tags=()
files=("zz_ompdiff_test.go")
if [ "$host" -eq 1 ]; then
  if [ ! -f "$PIG_SRC/go.mod" ]; then
    echo "error: --host requires a full PiG host checkout at PIG_SRC=$PIG_SRC" >&2
    exit 2
  fi
  cat "$PIG_SRC/go.sum" "$PIG_SRC/extensions/sdk/go.sum" >> "${mod%.mod}.sum" 2>/dev/null || true
  sort -u -o "${mod%.mod}.sum" "${mod%.mod}.sum"
  printf 'require github.com/MichaelKinsy/PiG v0.0.0\nreplace github.com/MichaelKinsy/PiG => %s\n' "$PIG_SRC" >> "$mod"
  tags=(-tags pighost)
  files+=("zz_ompdiff_host_test.go")
fi

ov="$MODDIR/overlay.json"
{
  printf '{"Replace":{'
  sep=""
  for f in "${files[@]}"; do
    sed -e 's/ompDiff/zzScratchDiff/g' -e 's/TestZZOmpDifferential/TestZZScratchOmpDifferential/g' "$here/go/$f" > "$MODDIR/$f"
    printf '%s"%s/internal/intent/zz_scratch_%s":"%s/%s"' "$sep" "$ext_dir" "${f#zz_}" "$MODDIR" "$f"
    sep=","
  done
  printf '}}\n'
} > "$ov"

GOMAXPROCS="${OMP_DIFF_GOMAXPROCS:-4}" \
OMP_INTENT_DIFF_CORPUS="$corpus" \
OMP_INTENT_DIFF_OUT="$results" \
OMP_INTENT_DIFF_REPORT="${OMP_INTENT_DIFF_REPORT:-}" \
OMP_INTENT_DIFF_EXAMPLES="${OMP_INTENT_DIFF_EXAMPLES:-2}" \
OMP_INTENT_DIFF_FAIL="${OMP_INTENT_DIFF_FAIL:-}" \
GOWORK=off GOFLAGS=-mod=mod \
  nice -n 10 go test -modfile="$mod" -overlay="$ov" "${tags[@]}" -count=1 -run 'TestZZScratchOmpDifferential$' -v ./internal/intent/
