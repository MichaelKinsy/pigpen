# Argument Validation Differential (`proof/diff/intent`)

Reproducible differential harness comparing `internal/intent/validate.go` and `internal/intent/intent.go` against **omp `v19.1.1`** (commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`)'s real `validateAgentToolArguments` (`packages/agent/src/tool-arguments.ts:23` → `packages/ai/src/utils/validation.ts:2052`) across 20,065 generated cases over 24 real tool schemas.

No corpus (`corpus.jsonl`, `cases.jsonl`), symlink shim (`nm/`), or measurement output (`results/`) is checked in; all generated artifacts are written under `$DIFF_OUT`.

## Prerequisites

- **`OMP_SRC`** (default: `$HOME/work/omp-v19.1.1`): read-only omp source tree at **tag `v19.1.1`** (commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`). `make-corpus.sh` and `mknm.py` fail loudly with the expected tag and commit if absent, wrong version, or wrong git HEAD.
- **`PIG_SRC`** (default: `$HOME/work/pig-080-link13`): PiG checkout providing `extensions/sdk` (always) and the full `github.com/MichaelKinsy/PiG` host module (for `run-port.sh --host` and `measure.sh`).
- **`bun`** on `PATH` (tested with **`bun 1.4.2`**) plus third-party npm packages that omp's `coding-agent` sources import at module load time (resolved via `OMP_DIFF_NODE_MODULES`, default `$HOME/.bun/install/global/node_modules`). Specifically, omp's `coding-agent` imports `@babel/parser`, `@opentelemetry/{api,api-logs,context-async-hooks,exporter-logs-otlp-proto,exporter-metrics-otlp-proto,exporter-trace-otlp-proto,resources,sdk-logs,sdk-metrics,sdk-trace-base,sdk-trace-node}`, `axe-core`, and `puppeteer-core` (an existing global install of `@oh-my-pi/pi-coding-agent` via `bun install -g @oh-my-pi/pi-coding-agent` provides all of them; the `@oh-my-pi/*` workspace packages themselves always resolve to `$OMP_SRC` through `$DIFF_OUT/nm/`, never from global `node_modules`).
- **`python3`** (stdlib only) and **`go`** (tested with Go 1.27).
- **`DIFF_OUT`** (default: `/tmp/omp-intent-diff`): scratch directory for `nm/`, `real_wire.json`, `cases.jsonl`, `corpus.jsonl`, and `results/`.

## Files

| File | Purpose |
|---|---|
| `mknm.py` | Builds `$DIFF_OUT/nm/@oh-my-pi/*` symlinking workspace packages to `$OMP_SRC/packages/*` by each `package.json` `exports` map, plus a proxy stub for `@oh-my-pi/pi-natives`. |
| `oracle.ts` | Bun driver importing omp's real `BUILTIN_TOOLS`, `HIDDEN_TOOLS`, `specialToolCommands`, `toolWireSchema`, `validateAgentToolArguments`, and `validateToolArguments`. Emits `real_wire.json` (`schemas`) and `corpus.jsonl` (`run`). Embeds no omp source code. |
| `gen.py` | Deterministic 20,065-case generator from `real_wire.json` (base ids `0..15921` + 4,143 extra `x:*` edge-case ids). |
| `real_wire.json` | Captured omp `v19.1.1` wire schemas + `lenientArgValidation` flags for reference. |
| `make-corpus.sh` | Runs `mknm.py` → `oracle.ts schemas` → `gen.py` → `oracle.ts run` into `$DIFF_OUT`. Reproduces `corpus.jsonl` with sha256 `d757247bd6793cc3414e52851fcfea069692a620303708ad3925788942638a86` (~4 s). |
| `go/zz_ompdiff_test.go` | Go differential test speaking the SDK wire protocol (`tool_prepare_arguments`) to `ohmypig.Extension()`. Skipped unless `OMP_INTENT_DIFF_CORPUS` is set. |
| `go/zz_ompdiff_host_test.go` | `//go:build pighost` hook wiring PiG's real `ai.ValidateToolArgumentsJSON` after `PrepareArguments`. |
| `run-port.sh` | Runs `go test -overlay` with a temporary `go.mod` (no edits to the repo). Pass `--host` to include PiG's host validator. |
| `measure.sh` | Fingerprints `internal/intent/{intent,schema,validate}.go`, runs both `PrepareArguments`-only and `--host` stages into `$DIFF_OUT/results/`, and writes `summary.txt` + `tree-fingerprint.txt`. |
| `select-reduced.py` | Deterministic reducer keeping up to 2 cases per `(tool, tag_family, result_class)` bucket. |

## Usage

```sh
# 1. Build the 20,065-case omp v19.1.1 corpus under $DIFF_OUT
OMP_SRC=/path/to/omp-v19.1.1 DIFF_OUT=/tmp/omp-intent-diff \
  piglets/oh-my-pig/proof/diff/intent/make-corpus.sh

# 2. Measure the extension (both PrepareArguments-only and --host stages)
PIG_SRC=/path/to/pig-080-link13 DIFF_OUT=/tmp/omp-intent-diff \
  piglets/oh-my-pig/proof/diff/intent/measure.sh

# Or run a single stage directly
PIG_SRC=/path/to/pig-080-link13 DIFF_OUT=/tmp/omp-intent-diff \
  piglets/oh-my-pig/proof/diff/intent/run-port.sh --host
```

## Reduced Corpus (`internal/intent/testdata/omp_diff_reduced.jsonl`)

The checked-in reduced fixture (`components/oh-my-pig/extensions/oh-my-pig/internal/intent/testdata/omp_diff_reduced.jsonl`, 1,199 cases, sha256 `5924b27c88fb5853cf016a3d02750e68bfae3ab2eb71a8cb3280fcdf4bc29e69`, exercised by `internal/intent/ompdiff_test.go`) is selected deterministically by taking the first 2 cases per `(tool, tag_family, result_class)` bucket in ascending `id` order, where `tag_family` is the first two `:`-delimited segments of `tag`.

Command that reproduces the checked-in reduced corpus **byte for byte**:

```sh
python3 piglets/oh-my-pig/proof/diff/intent/select-reduced.py \
  "$DIFF_OUT/corpus.jsonl" \
  "$DIFF_OUT/omp_diff_reduced.jsonl" \
  "$DIFF_OUT/results/port-prepare-only.jsonl"
cmp -s "$DIFF_OUT/omp_diff_reduced.jsonl" \
  components/oh-my-pig/extensions/oh-my-pig/internal/intent/testdata/omp_diff_reduced.jsonl
```

## Reference Counts (`corpus.jsonl` sha256 `d757247bd6793cc3414e52851fcfea069692a620303708ad3925788942638a86`)

- **Strict tools (17,479 cases = 13,816 base + 3,663 extra), `--host`:**
  - `11,798` equal accepts + `5,286` equal rejections (`5,228` byte-identical, `58` differing only in `Received arguments:` key order)
  - **`0` wrong rejections** (`PORT REJECTS / omp accepts: 0`), **`0` wrong accepts**, **`0` text diffs**
  - `29` `normalized differ (both accept)` (array-for-object `{}` normalization required for PiG host), `366` `normalized differ only by undeclared keys the port drops`
- **Lenient tools (2,586 cases = 2,106 base + 480 extra), `--host`:**
  - **`8` host-answered lenient raw fallbacks** (`4` base + `4` extra) where PiG's host validator runs after `PrepareArguments` without a lenient opt-out
  - `1,235` equal accepts, `26` `glob` unknown-key refusals (matching `glob.ts:241`), `813` tool `PrepareArguments` refusals, `7` port-changed raw fallback, `497` undeclared-keys-only drops
