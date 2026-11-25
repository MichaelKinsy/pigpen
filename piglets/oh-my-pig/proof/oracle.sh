#!/usr/bin/env bash
# Per-tool oracle runner: compares omp (v19.1.1) and oh-my-pig on scripted
# tool-call cases via a loopback LLM server.
#
# Usage:
#   oracle.sh cases/read.json [more.json ...]
#   ORACLE_MODE=omp-only oracle.sh cases/read.json
#   OH_MY_PIG_BIN=/tmp/omp-bin/oh-my-pig oracle.sh cases/read.json
#
# Environment:
#   OMP_BIN        Path to omp binary (default: omp on PATH)
#   OH_MY_PIG_BIN  Path to oh-my-pig binary (default: /tmp/omp-bin/oh-my-pig;
#                  if absent and ORACLE_MODE is unset, runs in omp-only mode)
#   ORACLE_MODE    both | omp-only | pig-only (auto-detected if unset)
#   ORACLE_OUT          Output directory (default: /tmp/omp-proof2/oracle)
#   ORACLE_TIMEOUT      Per-case timeout in seconds (default: 45)
#   ORACLE_KEEP         Set to 1 to keep per-case scratch for failing cases only
#   ORACLE_ALLOW_DIFFS  Set to 1 to exit 0 even on output/surface diffs in both-mode
#   ORACLE_ALLOW_TIMING Set to 1 to exit 0 on narrow wall-time threshold `timing` calls
#   ORACLE_TOOL_DIRS    Optional PATH-separated extra tool dirs (e.g. for gopls/ast-grep)
#   PIG_SRC             Optional path to PiG source checkout for verifying PiG host refs

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OMP_BIN="${OMP_BIN:-$(command -v omp || true)}"
OH_MY_PIG_BIN="${OH_MY_PIG_BIN:-/tmp/omp-bin/oh-my-pig}"
ORACLE_OUT="${ORACLE_OUT:-/tmp/omp-proof2/oracle}"
ORACLE_TIMEOUT="${ORACLE_TIMEOUT:-45}"

if [ "$#" -lt 1 ]; then
  echo "usage: $0 <case.json|cases-dir> [more.json ...]" >&2
  exit 2
fi

MODE="${ORACLE_MODE:-}"
if [ -z "$MODE" ]; then
  if [ -n "$OH_MY_PIG_BIN" ] && [ -x "$OH_MY_PIG_BIN" ]; then
    MODE="both"
  else
    MODE="omp-only"
  fi
fi
if [ "$MODE" != "pig-only" ]; then
  [ -n "$OMP_BIN" ] && [ -x "$OMP_BIN" ] || { echo "error: omp binary not found" >&2; exit 1; }
  . "$SCRIPT_DIR/omp_version.sh"
  require_omp_reference "$OMP_BIN"
fi

ARGS=(
  --mode "$MODE"
  --out-dir "$ORACLE_OUT"
  --timeout "$ORACLE_TIMEOUT"
)
if [ "${ORACLE_KEEP:-}" = "1" ]; then
  ARGS+=(--keep-failing)
fi
if [ "${ORACLE_ALLOW_DIFFS:-}" = "1" ]; then
  ARGS+=(--allow-diffs)
fi
if [ "${ORACLE_ALLOW_TIMING:-}" = "1" ]; then
  ARGS+=(--allow-timing)
fi
if [ -n "$OMP_BIN" ]; then
  ARGS+=(--omp-bin "$OMP_BIN")
fi
if [ -n "$OH_MY_PIG_BIN" ]; then
  ARGS+=(--pig-bin "$OH_MY_PIG_BIN")
fi

exec python3 -B "$SCRIPT_DIR/oracle.py" "${ARGS[@]}" "$@"
