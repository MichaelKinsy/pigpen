# oh-my-pig vs omp — Headless Tern Drawing Proof

This directory contains the self-contained harness that feeds **omp** and **oh-my-pig** the exact same scripted conversation inside isolated headless Tern instances (`tern serve` + `tern ctl`), captures screenshots, Tern DOM trees, and Tern Surface Protocol (TSP) wire recordings at every step, and produces a parity report comparing what each program draws.

## Files

| File | Role |
|---|---|
| `run.sh` | Single-command runner. Starts the scripted LLM server, runs `omp` and `oh-my-pig` in separate isolated headless Tern instances, captures per-step screenshots/trees/TSP snapshots, and writes `/tmp/omp-proof/report.md` and `/tmp/omp-proof/report.json`. |
| `t.sh` | Hermetic `tern` wrapper: unsets every `TERN_*`, `TERM_PROGRAM*`, and multiplexer variable from the calling shell and pins `TERN_CONFIG_DIR` and `TERN_DAEMON_SOCKET` under `/tmp/omp-proof/`. |
| `llm_server.py` | Loopback OpenAI-compatible SSE server (Python 3 stdlib) serving `turns.json`. Adapts the canonical `edit` turn to whichever `edit` parameter schema the client advertises (hashline `input`, `old_string`/`new_string`, or PiG's `edits[].oldText`/`newText`), and answers auxiliary `<composer-prediction>` calls without advancing the turn cursor. |
| `turns.json` | Fixed conversation script covering plain Markdown, delayed working state + thinking + text, `read`, `bash`, `edit`, and a failing `read` call. |
| `compare.py` | Python 3 (stdlib) comparer. Replays each lane's TSP wire log (`PI_TUI_TSP_RECORD` / `TERN_TSP_RECORD`) up to each step's line snapshot to recover exact `data-role`, `kind`, `tone`, `status`, and child nesting, cross-checks every node kind and the three surface regions (`sf-main`, `sf-dock`, `sf-layer`) against `tern ctl tree`, and emits Markdown + JSON reports. |

## Prerequisites

- `tern` on `PATH` (with headless `tern serve` and `tern ctl`)
- `python3` (standard library only; no third-party packages)
- `omp` binary (default: `omp` on `PATH`, override with `OMP_BIN=...`)
- `oh-my-pig` Piglet binary (default: `/tmp/omp-bin/oh-my-pig`, override with `OH_MY_PIG_BIN=...`)

## How to run

```sh
piglets/oh-my-pig/proof/run.sh
```

Environment overrides:

- `OMP_BIN`: path to `omp` (default: `omp` on `PATH`)
- `OH_MY_PIG_BIN`: path to `oh-my-pig` (default `/tmp/omp-bin/oh-my-pig`)
- `OMP_PROOF_ROOT`: scratch/output directory (default `/tmp/omp-proof`)

Outputs written under `/tmp/omp-proof/`:

- `report.md` — human-readable Markdown parity table and native-rendering proof
- `report.json` — machine-readable per-part and per-step comparison
- `omp/out/serve/*.png` and `pig/out/serve/*.png` — per-step headless Tern screenshots
- `omp/out/tree-*.json` and `pig/out/tree-*.json` — raw `tern ctl tree` dumps
- `omp/out/tsp.jsonl` and `pig/out/tsp.jsonl` — complete TSP wire recordings

## Isolation guarantees

- **Tern**: `t.sh` unsets `TERN_ACP`, `TERN_BLOB_DIR`, `TERN_COMPLETE`, `TERN_IDENTITY`, `TERN_LENSES`, `TERN_PANE`, `TERN_PANE_SOCKET`, `TERN_WINDOW_KEY`, `TERN_WINDOW_SOCKET`, `TERM_PROGRAM`, `TERM_PROGRAM_VERSION`, `TMUX`, `TMUX_PANE`, `STY`, and `ZELLIJ`, and points `TERN_CONFIG_DIR` and `TERN_DAEMON_SOCKET` under `/tmp/omp-proof/`. Real Tern windows are never touched.
- **omp**: runs with throwaway `HOME`, `PI_CODING_AGENT_DIR`, `PI_CODING_AGENT_SESSION_DIR`, `PI_CONFIG_DIR`, `PI_CONFIG_FILES=""`, `OMP_PROFILE=""`, `PI_PROFILE=""`, `OMP_SKIP_SETUP=1`, `OMP_AUTORESEARCH_DB_DIR`, `OMP_WORKTREE_DIR`, `OMP_GITHUB_CACHE_DB`, `CLAUDE_CONFIG_DIR`, `COPILOT_HOME`, `XDG_*_HOME`, `TMPDIR`, `--no-session`, `--offline`, and real provider/broker env vars unset.
- **oh-my-pig**: runs with throwaway `HOME`, `USERPROFILE`, `PIG_HOME`, `PI_HOME`, `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR`, `PIG_CODING_AGENT_SESSION_DIR`, `PI_CODING_AGENT_SESSION_DIR`, `PIG_OFFLINE=1`, `XDG_*_HOME`, `TMPDIR`, `--no-session`, `--approve`, and `PIG_USE_PI_DIRS` / real provider env vars unset.
- **Processes**: `run.sh` tracks only the PIDs it spawns (`LLM_PID`, `SERVE_PID`) and cleans them up on exit; it never uses `pkill` or `killall`.

## Honesty rule & what this does not compare

A row is reported as **`same`** only when both programs drew natively, both DOM trees contain the matching `sf-<kind>` surface node, and `data-role`, TSP kind, `tone`, `status`, and child nesting all match. Matching outer role + kind with different child nesting is reported as **`differs`** (with that exact note).

Because `tern ctl tree` omits `data-role` attributes on returned nodes, `compare.py` recovers `data-role` and exact TSP child trees from each program's own TSP wire recording (`PI_TUI_TSP_RECORD` for omp, `TERN_TSP_RECORD` for oh-my-pig) and verifies the rendered surface regions and `sf-<kind>` nodes against `tern ctl tree`.

**What this harness does not compare:**

- **Pixel appearance, colors, typography, spacing, and layout geometry**: screenshots (`*.png`) and layout JSONs (`*.layout.json`) are saved for visual inspection, but `compare.py` does not diff pixels or bounding rectangles.
- **CSS theme rules and custom stylesheets**: TSP `s` (stylesheet) frames and computed CSS properties are not compared.
- **Interactive overlays, pickers, dialogs, and keybindings**: only the inline chat transcript, working row, composer, and status/footer are exercised (no `/model`, `/settings`, `/session`, `ask`, or approval popups).
- **Non-tool / wave-2 features**: streaming incremental token cadence, image/blob uploads, multi-line/attachment composer chips, subagent cards, and non-`read`/`bash`/`edit` tools (`grep`, `glob`, `write`, `todo`, `eval`, etc.) are not covered by `turns.json`.
- **Wall-clock timing & exact text copy**: timestamps, durations, token counters, and wording differences in tool output or welcome banners are not compared (only structural kinds, roles, tones, statuses, and nesting).
