# Approval Gate Differential (`proof/diff/approval`)

Reproducible differential harness comparing `internal/approval` and `internal/shell/approval.go` (through the joined `ohmypig.Extension()` and real `settings.StoreFor` layers) against **omp `v19.1.1`** (commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`)'s real approval resolver (`tools/approval.ts`, `tools/index.ts`, `tools/resolve.ts`, `mcp/tool-bridge.ts`, `task/executor.ts`, `config/settings.ts`) across 32,599 generated cases in 10 sections, plus an end-to-end 17-shape `write [proc://<job>/kill#…]` unwrap-route check.

No corpus (`cases.jsonl`, `corpus.jsonl`), symlink shim (`nm/`), or measurement output (`results/`) is checked in; all generated artifacts are written under `$DIFF_OUT`.

## Prerequisites

- **`OMP_SRC`** (default: `$HOME/work/omp-v19.1.1`): read-only omp source tree at **tag `v19.1.1`** (commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`). `make-corpus.sh` and `mknm.py` fail loudly naming the tag and commit if absent, wrong version, or wrong git HEAD.
- **`PIG_SRC`** (default: `$HOME/work/pig-080-link13`): PiG checkout providing `extensions/sdk`.
- **`bun`** on `PATH` (tested with **`bun 1.4.2`**) plus third-party npm packages imported by omp's `coding-agent` sources (resolved via `OMP_DIFF_NODE_MODULES`, default `$HOME/.bun/install/global/node_modules`: `@babel/parser`, `@opentelemetry/*`, `axe-core`, `puppeteer-core` — provided by `bun install -g @oh-my-pi/pi-coding-agent`; all `@oh-my-pi/*` workspace packages resolve from `$OMP_SRC` through `$DIFF_OUT/nm/`, never from global `node_modules`).
- **`python3`** (stdlib only) and **`go`** (tested with Go 1.27).
- **`DIFF_OUT`** (default: `/tmp/omp-approval-diff`): scratch directory for `nm/`, `cases.jsonl`, `corpus.jsonl`, `meta.json`, and `results/`.

## Files

| File | Purpose |
|---|---|
| `mknm.py` | Builds `$DIFF_OUT/nm/@oh-my-pi/*` symlinking workspace packages to `$OMP_SRC/packages/*` and stubbing `@oh-my-pi/pi-natives`. |
| `gen.py` | Deterministic 32,599-case generator across 10 sections (`base`, `paths`, `unwrap`, `restricted`, `cached`, `interceptor`, `project`, `glob-path`, `nil-setting`, `malformed`). |
| `oracle.ts` | Bun driver importing omp's real approval resolver and tool instances from `$OMP_SRC`, computing both `omp` verdicts and the two-view `expectPort` targets (`port-stricter`, `port-ignores-untrusted-project`, `fail-closed`). Note: mirrors the two headless refusal format strings from `packages/coding-agent/src/extensibility/extensions/wrapper.ts:476-490` (MIT, Can Bölük / Stencil Labs) because that wrapper class cannot be instantiated standalone. |
| `meta.json` | Reference metadata for the omp `v19.1.1` corpus (`mounted`, `active`, decision counts). |
| `make-corpus.sh` | Runs `mknm.py` → `gen.py` → `oracle.ts` into `$DIFF_OUT`. Reproduces `corpus.jsonl` with sha256 `b64b61eb9a439973cea276bef7142d7953234de7fc6daddf5c849d13c978d494` (~3 s). |
| `go/zz_approval_ompdiff_test.go` | Go differential + unwrap-route tests (`package ohmypig`, skipped unless `OMP_APPROVAL_DIFF_CORPUS` is set). |
| `run-port.sh` | Runs `go test -overlay` on the extension module root with a temporary `go.mod` (no edits to the repo). |
| `measure.sh` | Fingerprints `internal/approval/*.go` and `internal/shell/approval.go`, runs `run-port.sh`, and writes `$DIFF_OUT/results/{report.txt,report.txt.route,port.jsonl,summary.txt,tree-fingerprint.txt}`. |
| `select-reduced.py` | Deterministic selector reproducing `testdata/approval_diff_reduced.jsonl` byte for byte. |

## Usage

```sh
# 1. Build the 32,599-case omp v19.1.1 corpus under $DIFF_OUT
OMP_SRC=/path/to/omp-v19.1.1 DIFF_OUT=/tmp/omp-approval-diff \
  piglets/oh-my-pig/proof/diff/approval/make-corpus.sh

# 2. Measure the joined extension against all 32,599 cases + 17 unwrap-route shapes
PIG_SRC=/path/to/pig-080-link13 DIFF_OUT=/tmp/omp-approval-diff \
  piglets/oh-my-pig/proof/diff/approval/measure.sh

# Optional: run selected sections or fail on any unexpected diff
OMP_APPROVAL_DIFF_SECTIONS=project,interceptor OMP_APPROVAL_DIFF_FAIL=1 \
PIG_SRC=/path/to/pig-080-link13 DIFF_OUT=/tmp/omp-approval-diff \
  piglets/oh-my-pig/proof/diff/approval/run-port.sh
```

## Reduced Corpus (`testdata/approval_diff_reduced.jsonl`)

The checked-in reduced corpus (`components/oh-my-pig/extensions/oh-my-pig/testdata/approval_diff_reduced.jsonl`, 3,873 cases, sha256 `00bb1a189d9c960ccc13afa26642789d30f62f988c075940368fcbd18903059c`, exercised by `approval_ompdiff_test.go`) is selected deterministically from `corpus.jsonl` in original `id` order:

1. Keep 100% of compared cases in the 8 focused sections: `paths` (624), `unwrap` (693), `cached` (34), `interceptor` (300), `project` (360), `glob-path` (120), `nil-setting` (48), `malformed` (48).
2. In `base` (1,338 cases): keep all `(tool, args)` variants for no-policy `interactive`/`top` under `always-ask` & `always-ask-cached`; first case per `(mode, tool, tier, decision, source)` for `<unset>`/`yolo`/`write`; first case per `(mode, ui, session, tier, decision, source)` for non-`interactive`/`top` no-policy calls; first case per `(mode, userPolicy, ui, session, tier, decision, source)` for policy calls (excluding `interactive`/`subagent`), plus one per tool for `(always-ask, deny)`, `(always-ask, allow)`, `(yolo, prompt)` on `interactive`/`top`; and for `bash.patterns`, all commands for no-policy `interactive`/`top` under `<unset>`/`always-ask` plus first case per `(mode, userPolicy, ui, session, decision, source, reason)` (excluding `interactive`/`subagent`).
3. In `restricted` (308 cases): keep all 39 calls × 3 `tools.restrictedFolders` sets under `<unset>`/no-policy/`interactive`/`top`, plus first case per `(restrictedFolders, mode, userPolicy, ui, session, decision, source)`.

Command that reproduces the checked-in reduced corpus **byte for byte**:

```sh
python3 piglets/oh-my-pig/proof/diff/approval/select-reduced.py \
  "$DIFF_OUT/corpus.jsonl" \
  "$DIFF_OUT/approval_diff_reduced.jsonl"
cmp -s "$DIFF_OUT/approval_diff_reduced.jsonl" \
  components/oh-my-pig/extensions/oh-my-pig/testdata/approval_diff_reduced.jsonl
```

## Reference Counts (`corpus.jsonl` sha256 `b64b61eb9a439973cea276bef7142d7953234de7fc6daddf5c849d13c978d494`)

- **`29,992` equal**
- **`175` as expected**: `48` `fail-closed` + `80` `port-ignores-untrusted-project` + `47` `port-stricter`
- **`2,432` not compared**: `160` legacy `glob.pattern`, `1,040` `edit` (native addon stubbed), `1,232` tools not registered by omp in that session
- **`0` cases not equal and not as expected**
- **Unwrap route:** `17` shapes, **`0` violations**
