# Upstream Reference: `autoresearch`

Ported from omp (`oh-my-pi`, tag `v19.1.1`, commit `31ffee513f`, MIT).

## Upstream files followed

- `packages/coding-agent/src/autoresearch/types.ts` — `PendingRunSummary`, `RunningExperiment`, `AutoresearchRuntime` (`Runtime`), `AutoresearchControlEntryData` (`ControlEntryData`), `ReconstructedControlState`.
- `packages/coding-agent/src/autoresearch/helpers.ts` — `ExperimentOutputScanner`, `parseMetricLines`, `parseAsiLines`, `parseAsiValue`, `mergeAsi`, `inferMetricUnitFromName`, `normalizePathSpec`, `pathMatchesSpec`, `dedupeStrings`, `ensureNumericMetricMap`, `sanitizeAsi`.
- `packages/coding-agent/src/autoresearch/git.ts` — `getCurrentAutoresearchBranch`, `ensureAutoresearchBranch` (including pure-jj rejection, colocated jj+git, nested git under jj, dirty-tree check, slugified `autoresearch/<slug>-YYYYMMDD[-N]` allocation), `parseWorkDirDirtyPaths`, `relativizeGitPathToWorkDir`, `parseDirtyPaths`, `normalizeStatusPath`, `DirtyPathEntry`, `parseDirtyPathsWithStatus`, `parseWorkDirDirtyPathsWithStatus`, `computeRunModifiedPaths`. Executed via `exec.CommandContext(ctx, "git", ...)` stripping only the ambient `GIT_DIR` family (`GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`, `GIT_ALTERNATE_OBJECT_DIRECTORIES`) while preserving `GIT_AUTHOR_*`, `GIT_COMMITTER_*`, and `GIT_CONFIG_*`, with `--` before pathspecs.
- `packages/coding-agent/src/autoresearch/state.ts` — `createExperimentState`, `createSessionRuntime`, `findBestKeptMetric`, `sortedMedian`, `computeConfidence` (MAD-based noise floor), `buildExperimentState`, `reconstructControlState`, `createRuntimeStore`.
- `packages/coding-agent/src/autoresearch/storage.ts` — `encodeProjectKey`, `SessionRow`, `RunRow`, `openSession`, `updateSession`, `bumpSegment`, `closeSession`, `insertRun`, `updateRunLogPath`, `updateRunConfidence`, `markRunCompleted`, `markRunLogged`, `flagRun`, `abandonPendingRuns`, `getPendingRun`, `getRunById`, `listRuns`, `listLoggedRuns`, `openAutoresearchStorage`, `openAutoresearchStorageIfExists`, `closeAllAutoresearchStorages`.
- `packages/coding-agent/src/autoresearch/tools/init-experiment.ts` — `init_experiment` tool (Phase 1 → Phase 2 transition, `./autoresearch.sh` presence check, auto-commit of harness setup on `autoresearch/*` branches, segment bumping, pending run abandonment).
- `packages/coding-agent/src/autoresearch/tools/run-experiment.ts` — `run_experiment` tool (fixed `bash autoresearch.sh` entrypoint, process-group timeout & user abort via `ctx.Done()`, live 1-second progress streaming via `ctx.OnUpdate`, streaming `METRIC`/`ASI` parsing, `runs/NNNN/benchmark.log` persistence, full-stream line/byte totals and tail truncation).
- `packages/coding-agent/src/autoresearch/tools/log-experiment.ts` — `log_experiment` tool (`keep`/`discard`/`crash`/`checks_failed`, on-branch commit & `git reset --hard HEAD` + `git clean -fd`, off-branch selective restore & untracked cleanup, scope deviation accounting, `justification`, `flag_runs`, MAD confidence recomputation, `max_iterations` auto-off).
- `packages/coding-agent/src/autoresearch/tools/update-notes.ts` — `update_notes` tool (`body` replacement and `append_idea` under `## Ideas`).
- `packages/coding-agent/src/autoresearch/index.ts` — `/autoresearch [off|clear|status|<goal>]` command, `session_start` / `session_tree` / `session_shutdown` rehydration & cleanup, `before_agent_start` Phase 1 / Phase 2 prompt injection, `agent_end` auto-resume loop, legacy artifact cleanup on `clear`.
- `packages/coding-agent/src/autoresearch/{prompt,prompt-setup,command-resume,resume-message}.md` — embedded verbatim via `//go:embed` and rendered through `renderHandlebars` + post-render blank-line formatting (`template.go`).
- `packages/tui/src/tools/autoresearch.ts` & `packages/tui/src/apps/autoresearch-data.ts` — `MetricDirection`, `ExperimentStatus`, `ExperimentResult`, `ExperimentState`, `commas`, `fmtNum`, `formatNum`, `formatElapsed`, `isBetter`, `currentResults`, `findBaselineResult`, `findBaselineMetric`, `findBaselineRunNumber`, `findBaselineSecondary`.

## Storage difference (JSON + JSONL vs SQLite)

Upstream `packages/coding-agent/src/autoresearch/storage.ts` persists sessions and runs in a `bun:sqlite` WAL database at `~/.omp/autoresearch/--<repo>--/autoresearch.db` (or `$OMP_AUTORESEARCH_DB_DIR/--<repo>--.db`). Because `oh-my-pig` is Go standard library only (zero cgo, zero third-party Go modules), `internal/autoresearch/storage.go` implements the exact same logical schema and API using a crash-safe JSON + JSONL store under `<agentDir>/autoresearch/--<repo>--/` (honouring `OMP_AUTORESEARCH_DB_DIR`, `PIG_CODING_AGENT_DIR`, `sdk.AgentDir()`, and `~/.pig/agent`):

- `state.json` — atomically written via temp file + `Sync` + `os.Rename`, storing schema version, ID counters, and `sessions`.
- `runs.jsonl` — append-only, `Sync`-ed JSONL journal of `RunRow` mutations read with unbounded `bufio.Reader.ReadBytes('\n')` (replayed in order by run ID on load; any torn trailing line is truncated back to the last valid record offset during crash recovery, and appends guarantee a leading newline boundary).
- `runs/NNNN/benchmark.log` — identical per-run captured benchmark log path.

## Not Ported

| Upstream File:Lines | Feature | Reason |
|---|---|---|
| `packages/coding-agent/src/autoresearch/index.ts:227-245` | `ctrl+x` and `ctrl+shift+x` dashboard toggle/overlay shortcuts | Intentionally omitted along with the fullscreen interactive TUI dashboard overlay (while PiG SDK does expose `Extension.Shortcut`, the shortcuts only drive the omitted dashboard widget/overlay). Use `/autoresearch status` instead. |
| `packages/tui/src/apps/autoresearch-dashboard.ts:1-1282` | Fullscreen interactive TUI dashboard overlay & live sparkline widget (`createDashboardController`) | Intentionally omitted from this headless/stdlib Go port (PiG SDK supports `SetWidget` / `Custom` overlays, but porting the 1,282L custom TUI sparkline/table overlay is out of scope). `/autoresearch status` provides a formatted plain-text status and results table instead. |
| `packages/tui/src/tools/autoresearch.ts:165-435` | Custom TUI tool call/result renderers (`initExperimentToolRenderer`, `runExperimentToolRenderer`, `logExperimentToolRenderer`, `updateNotesToolRenderer`) | PiG renders tool text output directly; custom per-tool TUI component renderers belong to the frontend lane. |
| `packages/coding-agent/src/autoresearch/index.ts:248-249` | `session_switch` and `session_branch` event hooks | PiG v0.8.0 SDK exposes `session_start`, `session_tree`, and `session_shutdown` (no separate `session_switch` / `session_branch` post-events). `rehydrate` runs on `session_start` and `session_tree`. |
| `packages/coding-agent/src/autoresearch/index.ts:378-415` | Multi-block `systemPrompt: string[]` return in `before_agent_start` | PiG's `before_agent_start` hook accepts a single `systemPrompt` string, not `[]string`. Base prompt and autoresearch prompt are joined with `\n\n`. |
