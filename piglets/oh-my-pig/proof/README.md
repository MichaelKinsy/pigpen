# oh-my-pig vs omp — Parity Proof & Oracle Harness

This directory contains three self-contained, offline, hermetic verification tools comparing **omp** (`v19.1.1`) and **oh-my-pig**:

1. **Headless Tern native-drawing proof** (`run.sh` + `compare.py` + `turns.json` + `llm_server.py`) — runs both programs inside isolated headless Tern instances (`tern serve` + `tern ctl`), captures screenshots, Tern DOM trees, and TSP wire recordings at every step, and compares node kinds, roles, tones, statuses, and child nesting.
2. **Per-tool behavioural oracle** (`oracle.sh` + `oracle.py` + `cases/*.json`) — runs both programs non-interactively (`-p --mode json`) against an in-process loopback OpenAI-compatible server + local HTTP fixture server, capturing and diffing:
   - per-call tool results (supporting both sequential and parallel tool calls),
   - `tool_execution_end.isError` and normalized `result.details` from `--mode json`,
   - follow-up messages added between parent turns (initial `<system-reminder>`, async `<system-notice>`, vision image attachments),
   - advertised tool lists in exact wire order (names, descriptions, order-preserving JSON schemas) and system prompts across every distinct surface (default, skills-enabled, vision) plus the first subagent child's system prompt & tools.
3. **Plain-PTY (no Tern) ANSI proof** (`ansi.sh` + `ansi_capture.py`) — runs both programs on `turns.json` inside a fixed-size pseudo-terminal (`TERM=xterm-256color`, all `TERN_*` / `TERM_PROGRAM*` unset, `PI_TUI_NATIVE=0`, with controlling terminal `TIOCSCTTY`), drives steps via `/health` cursor polling, and saves both final screens, full scrollback transcripts, and full-width side-by-side + unified diffs.

## Files

| File | Role |
|---|---|
| `harness_isolation.py` | Shared isolation utilities: refusing HTTP/CONNECT proxy listener (`RefusingProxyServer`), macOS `sandbox-exec` loopback-only profile, `ORACLE_RUN_TAG` process-tree reaper, prefix env stripping, offline omp `config.yml` generator, and 2 GiB free-space guard. |
| `oracle.sh` / `oracle.py` | Per-tool oracle runner (`-p --mode json`), loopback LLM + fixture HTTP server, narrow named normalizations, surface + details + text + recorded allowlists, and Markdown/JSON diff reporter. Exits non-zero on any case failure, network attempt, leaked process, or (in `both` mode unless `ORACLE_ALLOW_DIFFS=1`) unexplained call diff, details-only diff, recorded-call diff, surface/child-surface/follow-up diff, or `timing` call (unless `ORACLE_ALLOW_TIMING=1`). |
| `cases/*.json` | 13 case files (52 cases, 309 calls) covering `read` (ranges, all selectors, truncation, directories, binary, structured JSON/TAR/ipynb, vision PNG, local HTTP/HTML, `:conflicts`), `write` (including read-only URL refusals), `edit` (all hashline ops + failure paths), `bash` (exit/state/cwd/spill/chunked column cap/timeout/async/services/notify/non-empty proc streams/running job cancel), `grep`, `glob`, `find` (with `find.enabled: "on"` + deterministic judge), `todo` (all ops + phases + parallel + mid-run reminder), `ask` (print-mode refusal), `web_search`, `eval` (py/js state/timeout/reset/sh), `task`+`wait` (batch/errors/in-flight cancel/history/structured output + `yield` + child surface), `lsp` (status + real `gopls` symbols/hover/definition/diagnostics), `ast_grep`, `ast_edit` + `resolve`/`reject` success, `debug` (sessions + deterministic stdio DAP adapter), `checkpoint`/`rewind`, `tools.intentTracing` on/off (schema + recorded `tool_execution_start` args/intent + job intent), approvals (`approvalMode: always-ask` and `write`, `tools.approval`, `bash.patterns`, mounted tools via bash & direct), parallel multi-tool turns, argument validation + unknown tool suggestions, and internal URLs (`local://`, `skill://`, `proc://`, `agent://`, `history://`, `help://`, `cfg://`, `conflict://`, `out://`, `rule://`). |
| `allowlist_refs.py` + `*_allowlist.json` (`details`, `text`, `recorded`, `surface`, `drawing`, `terminal`) | Line-independent reference resolver (`ref_file` + `ref_term` + optional `ref_subject` resolved uniquely at load time to `file:line`, failing on 0 matches, >1 matches, or a resolved line that does not name the differing field/subject; skips external `PiG ...` host refs with a visible note when `PIG_SRC` is unset or absent) backing reviewable allowlists for documented (`LISTED`, `PORT_EXTRA`, `NO_CONSUMER_AT_TAG`, `HOST`, `ON_PURPOSE`) diffs. Any unexplained sub-difference, details diff, surface diff, or line flips the call/surface/part/hunk to `differs`. Details entries marked `"real_model_only": true` (AI-authored `live`/`authoring` job widgets that cannot be triggered under the offline loopback LLM) are reported as `real-model-only` and excluded from `details_allowlist_stale`. |
| `test_oracle_harness.py` | Unit tests for the oracle and drawing harnesses (child surface selection, Details verdict classes, unexplained-details propagation, side-aware `/private` normalization, narrow `agent_loop_error` & `todo` matchers, type-aware details masks, bounded `timing` band, positional diff headers, anchored regexes, `surface_allowlist.json` classification, `allowlist_refs.py` subject checks, `ORACLE_TOOL_DIRS` / `PIG_SRC` hygiene, recursive subtree/prop diffing, drawing & terminal allowlist subtraction, judge isolation, EAW char width, and SGR summaries). |
| `ansi.sh` / `ansi_capture.py` | Plain-PTY (no Tern) runner using Python 3 stdlib `pty` + `VTScreen` (with scrollback, per-cell SGR `(fg, bg)` tracking, `?7h`/`?7l`, EAW-accurate cell width, separate `ESC 7/8` and `CSI s/u` slots, full `ESC c` reset, `TIOCSCTTY`, `--tui-mode regular` for the port, `/health` completed+in_flight cursor + PTY quiet sync, width-preserving unit-preserving duration & `/private`-preserving path normalization, side-specific anchored regex + structural text/blank-row/per-cell-colour/terminal-mode classification via `terminal_allowlist.json` with `ref_subject` enforcement, and non-zero exit on any unexplained text, blank-row, colour, or terminal-mode diff). |
| `run.sh` / `t.sh` / `compare.py` / `llm_server.py` / `turns.json` | Headless Tern structural, prop, and frame-level parity runner (`omp` vs `oh-my-pig`): walks every part's subtree (covering all 23 steps including `17b-slash-closed` and `18-narrow-80`) including shared props and declared projections below `$kind` differences, checks `count` and `took`/`age`/`stopped` presence & type, counts no-op defaults explicitly, LCS-aligns ordered children of `main`, `dock`, and `layer` at every step, classifies every unmatched child and matched-subtree visible-node-count delta via `drawing_allowlist.json` (with pinned values, `ref_subject` enforcement, and `class_in_doubt`), and exits non-zero on any unexplained part diff, unmatched child, or count delta. |
| `diff/intent/` | Reproducible 20,065-case argument-validation differential against omp `v19.1.1`'s real `validateAgentToolArguments` (`make-corpus.sh`, `run-port.sh`, `measure.sh`, `select-reduced.py`, `oracle.ts`, `gen.py`, `mknm.py`, `go/zz_ompdiff*_test.go`). |
| `diff/approval/` | Reproducible 32,599-case approval-gate + 17-shape unwrap-route differential against omp `v19.1.1`'s real `tools/approval.ts` (`make-corpus.sh`, `run-port.sh`, `measure.sh`, `select-reduced.py`, `oracle.ts`, `gen.py`, `mknm.py`, `go/zz_approval_ompdiff_test.go`). |

## Prerequisites

- `python3` (standard library only; no third-party packages; run with `-B` so no `__pycache__` is written)
- `omp` binary (`v19.1.1`, default: `omp` on `PATH`, override with `OMP_BIN=...`)
- `oh-my-pig` Piglet binary (default: `/tmp/omp-bin/oh-my-pig`, override with `OH_MY_PIG_BIN=...`)
- `tern` on `PATH` (only required for `run.sh`; not needed for `oracle.sh` or `ansi.sh`)

## How to run

### 1. Per-tool oracle

```sh
# Single tool file (both sides if OH_MY_PIG_BIN exists, else omp-only)
piglets/oh-my-pig/proof/oracle.sh piglets/oh-my-pig/proof/cases/read.json

# Entire case library against omp alone (reference capture)
ORACLE_MODE=omp-only ORACLE_OUT=/tmp/omp-proof2/reference/oracle \
  piglets/oh-my-pig/proof/oracle.sh piglets/oh-my-pig/proof/cases

# Both sides explicitly (add ORACLE_ALLOW_DIFFS=1 to inspect diffs without non-zero exit;
# set ORACLE_TOOL_DIRS for extra external tools such as ast-grep/gopls and PIG_SRC for PiG host refs)
ORACLE_MODE=both OH_MY_PIG_BIN=/tmp/omp-bin/oh-my-pig \
  ORACLE_TOOL_DIRS=/path/to/tools/bin PIG_SRC=/path/to/pig \
  piglets/oh-my-pig/proof/oracle.sh piglets/oh-my-pig/proof/cases
```

Overrides: `OMP_BIN`, `OH_MY_PIG_BIN`, `ORACLE_MODE` (`both` | `omp-only` | `pig-only`), `ORACLE_OUT` (default `/tmp/omp-proof2/oracle`), `ORACLE_TIMEOUT` (default `45`), `ORACLE_TOOL_DIRS` (optional `PATH`-separated extra tool directories prepended to `PATH` for both programs; empty by default), `PIG_SRC` (optional PiG host checkout path for verifying `PiG ...` allowlist refs; skips with a visible note when unset), `ORACLE_KEEP=1` (keep per-case scratch for failing cases only), `ORACLE_ALLOW_DIFFS=1`, `ORACLE_ALLOW_TIMING=1` (allow narrow-band `timing` crossings to exit 0), `ORACLE_NO_SANDBOX=1`.
### 2. Plain-PTY ANSI proof (no Tern)

```sh
piglets/oh-my-pig/proof/ansi.sh

ANSI_MODE=omp-only ANSI_OUT=/tmp/omp-proof2/reference/ansi-omp \
  piglets/oh-my-pig/proof/ansi.sh
```

Overrides: `OMP_BIN`, `OH_MY_PIG_BIN`, `ANSI_MODE`, `ANSI_OUT` (default `/tmp/omp-proof2/ansi`; must be under `/tmp` or `/var/tmp`), `ANSI_ROWS` (default `40`), `ANSI_COLS` (default `120`), `ORACLE_TOOL_DIRS` (optional `PATH`-style list of extra tool directories prepended to `PATH` for both programs, e.g. `ORACLE_TOOL_DIRS=/path/to/tools/bin`; empty by default), `ORACLE_ALLOW_DIFFS=1` (exit 0 even when unexplained text diffs exist), `ORACLE_KEEP=1`.

Modes & diff channels: `ansi.sh` saves both `diff.json`/`diff.txt` (final screen via `--screen-regions`, separating bottom-aligned `transcript` and `dock` and isolating omp-side-only `OMP_ARTIFACT` stale pre-resize copies) and `scrollback-diff.json`/`scrollback-diff.txt` (full scrollback), plus per-cell styled captures (`*.sgr.json` with recorded resize byte offsets). Every channel feeds the verdict and exit code: (1) non-blank text hunks, (2) changed blank rows (`blank_row` entries only; extra blank rows elsewhere fail), (3) per-cell SGR `(fg, bg)` runs on text-matching lines (`colour` entries with pinned colours or validated animated/theme manifolds such as `@omp_welcome_gradient` derived from `welcome.ts` `GRADIENT_STOPS`), and (4) terminal modes (`verify_terminal_modes` verifies from raw stream byte offsets that omp's `?1049h`/`?1049l` occurs strictly inside the resize window and nowhere else). Raw palette census is kept as summary only (`sgr_palette_census`).

### 3. Headless Tern native-drawing proof

```sh
piglets/oh-my-pig/proof/run.sh
```

Overrides: `OMP_BIN`, `OH_MY_PIG_BIN`, `OMP_PROOF_ROOT` (default `/tmp/omp-proof`; must be under `/tmp` or `/var/tmp`), `ORACLE_TOOL_DIRS` (optional `PATH`-style list of extra tool directories prepended to `PATH` for both programs; empty by default), `ORACLE_ALLOW_DIFFS=1` (exit 0 even when unexplained diffs exist), `ORACLE_KEEP=1`.

Coverage & normalization: `compare.py` drops only wire-internal keys (`key`, `head`), masks numeric `took`/`age`/`stopped` values to `<MS>` while checking presence and type, counts known no-op defaults (`md.stream=false`, `editor/text.text=""`, `tool.meta=[]`) separately under `noop_default_diffs`, preserves `/private<WORK>` vs `<WORK>` path differences, compares shared props and declared projections below `$kind` differences, checks per-part `count`, and covers all 23 steps (including `17b-slash-closed` and `18-narrow-80`). At every step, `frame_comparison` LCS-aligns the ordered visible children of `main`, `dock`, and `layer`, reports every unmatched child (step, region, index, kind, role, visible node count, ≤80 chars of text, and allowlist classification), and audits every matched-subtree visible-node-count delta (`delta = wrapper_delta + allowlisted_node_delta + unexplained_node_delta + unaccounted_delta`). Exits non-zero when any part diff, unmatched region child, or matched-subtree count delta is unexplained unless `--allow-diffs` / `ORACLE_ALLOW_DIFFS=1` is set.
### 4. Source-level differentials (`diff/intent/` and `diff/approval/`)

These two harnesses drive omp `v19.1.1`'s real TypeScript source (pointed to by `OMP_SRC`, commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`) via `bun` (tested with `bun 1.4.2`) to build deterministic JSONL corpora under `DIFF_OUT`, then overlay Go tests onto the extension module without editing any tracked file:

```sh
# Argument validation (20,065 cases, corpus sha256 d757247bd6793cc3...)
OMP_SRC=/path/to/omp-v19.1.1 DIFF_OUT=/tmp/omp-intent-diff \
  piglets/oh-my-pig/proof/diff/intent/make-corpus.sh
PIG_SRC=/path/to/pig-080-link13 DIFF_OUT=/tmp/omp-intent-diff \
  piglets/oh-my-pig/proof/diff/intent/measure.sh

# Approval gate + 17-shape unwrap route (32,599 cases, corpus sha256 b64b61eb9a439973...)
OMP_SRC=/path/to/omp-v19.1.1 DIFF_OUT=/tmp/omp-approval-diff \
  piglets/oh-my-pig/proof/diff/approval/make-corpus.sh
PIG_SRC=/path/to/pig-080-link13 DIFF_OUT=/tmp/omp-approval-diff \
  piglets/oh-my-pig/proof/diff/approval/measure.sh
```

See `diff/intent/README.md` and `diff/approval/README.md` for global `bun` third-party package requirements (`OMP_DIFF_NODE_MODULES`), `OMP_SRC`/`PIG_SRC`/`DIFF_OUT` defaults, and reduced-corpus selection (`select-reduced.py`).

## Isolation & cleanup guarantees (by platform)

- **Network isolation**:
  - **All platforms (macOS, Linux, Windows/WSL)**: both programs receive `HTTP_PROXY`, `http_proxy`, `HTTPS_PROXY`, `https_proxy`, `ALL_PROXY`, `all_proxy` pointed at an in-harness refusing proxy listener (`403 Forbidden`) with `NO_PROXY=127.0.0.1,localhost,::1` and `no_proxy` matching. Any attempt recorded by the listener fails the run (non-zero exit) and is printed in the report. On both sides, `config.yml` sets `providers.webKeyless: false` and `providers.fetch: native`; omp's `config.yml` additionally sets `startup.checkUpdate: false`, `update.auto: false`, and disables all built-in catalog providers (`disabledProviders`), plus `PI_AI_CODEX_CLIENT_VERSION=0.1.0` so neither background model discovery, npm version checks, `trafilatura` auto-install, nor `r.jina.ai` runs.
  - **macOS (Darwin)**: in addition to the refusing proxy, every child process is wrapped in `sandbox-exec` with a Seatbelt profile that denies all non-loopback `network-outbound` IP traffic at the kernel level without requiring privileges.
  - **Linux**: the refusing proxy enforces and detects proxy-respecting HTTP/HTTPS traffic; kernel-level network namespaces require privileges (`CLONE_NEWNET`) and are not assumed.
- **No real home / config & bounded disk use**:
  - `HOME`, `XDG_*_HOME`, `PI_CODING_AGENT_DIR`, `PIG_HOME`, `PI_HOME`, `TMPDIR`, and Tern config/socket paths are pinned to per-run/per-case temp directories (resolved via `realpath` once). External provider, cloud, search, VCS (`GH_*`, `GITHUB_*`, `GLAB_*`), SSH, proxy, and multiplexer env vars are stripped by prefix.
  - All cases in one run share a single `PI_NATIVES_DIR` (`$OUT/.natives-cache`) so `omp` unpacks its ~115 MiB native addon once instead of once per case; `.natives-cache` is always removed on exit.
  - Per-case scratch directories are deleted immediately after capture (on both success and failure, unless `ORACLE_KEEP=1` for a failing case). Every runner refuses to start under 2 GiB free disk space and prints the final output size.
- **Process hygiene**:
  - Every spawned child carries a unique `ORACLE_RUN_TAG=<id>` in its environment. After every side, on every exit path, and in `SIGTERM`/`SIGINT`/`SIGHUP` handlers, the harness locates processes carrying that exact tag (via `/proc/*/environ` on Linux or `ps eww` + descendant walk on macOS — never by process name) and stops them by PID (`SIGTERM` then `SIGKILL`). Each script verifies zero tagged processes remain before exiting and fails non-zero if any survive.

## Oracle normalizations

Every listed rule is implemented in `oracle.py` and intentionally narrow so real behavioural divergences are not masked:

### Text normalizations (`normalize_text`)

- `temp_paths` — side-aware macOS `/private` handling: replaces each side's `/private`-stripped alias with `<WORKSPACE>`, `<HOME>`, `<TMPDIR>` and canonical `/private/...` paths with `/private<WORKSPACE>`, `/private<HOME>`, `/private<TMPDIR>` so a side that fails to strip `/private` where omp strips it visibly differs
- `local_fixture_port` — ephemeral local fixture port on `127.0.0.1`/`localhost` → `<FIXTURE_PORT>`
- `wall_time` — replace the numeric duration in `Wall time: <N> seconds` with `Wall time: <DURATION> seconds` (never silently strip one-sided wall-time notices; one-sided threshold crossings report as `timing` only when the printing side is within `1.0–1.3s`, the other side's `wallTimeMs < 1000`, and no details other than `wallTimeNoticeEmitted` differ; `timing` exits non-zero unless `--allow-timing` / `ORACLE_ALLOW_TIMING=1`)
- `duration_attr` — XML `duration="<N>(ms|s|m|us|µs)"` → `duration="<DURATION <unit>>"` (preserves unit)
- `proc_duration` — `completed in <N>(ms|s)`, `failed in <N>(ms|s)`, `cancelled in <N>(ms|s)`, `running up <N>(ms|s)`, `— up <N>(ms|s)` → `<DURATION <unit>>` (preserves unit)
- `service_pid` — `pid=<N>` and `— pid <N>` → `<PID>`
- `find_timing` — `find` footer `<N>(ms|s|m) wall / <N>(ms|s|m) api` → `<DURATION <u1>> wall / <DURATION <u2>> api` (preserves units)
- `today_date` — today's calendar date (local and UTC only) → `<TODAY>`
- `iso_timestamps` — ISO-8601 timestamps → `<TIMESTAMP>`
- `task_dir_hex` — `omp-task-<16-hex>` → `omp-task-<TASK_HEX>`
- `daemon_service_hex` — `daemon/services/<16-hex>/` → `daemon/services/<SVC_HEX>/`
- `session_uuids` — non-nil RFC-4122 UUIDs → `<UUID>` (nil UUID `00000000-0000-0000-0000-000000000000` is preserved)

### Type-aware details masks (`normalize_obj`)

- **Numeric-only timing/PID keys** (masked to `<KEY>` ONLY when the JSON value is `int`/`float` and NOT `bool`; `null`, strings, booleans, and objects are never masked): `durationMs`, `totalDurationMs`, `duration`, `elapsedMs`, `elapsed`, `wallTimeMs`, `apiMs`, `startedAt`, `finishedAt`, `updatedAt`, `createdAt`, `exitedAt`, `readyAt`, `settledAtMs`, `timestamp`, `pid`.
- **ISO-8601 or numeric timestamp keys** (masked to `<TIMESTAMP>` when a full ISO-8601 string, or `<KEY>` when numeric; arbitrary strings/`null`/`bool` are never masked): `startedAt`, `finishedAt`, `updatedAt`, `createdAt`, `exitedAt`, `readyAt`, `timestamp`.
- **PID lists**: `pids` is masked to `<PIDS>` ONLY when the value is a JSON list of integers (excluding booleans).
- **Trace-only keys** (masked ONLY inside a `trace` subtree, numeric-only): `ns`, `t_ns`, `dur_ns`, `end_ns`, `at`, `seq`, `end_seq`, `event_seq`, `run`. Outside `trace` they are compared verbatim.
- **Behavioural booleans are never masked**: `wallTimeNoticeEmitted` is compared verbatim.
- **Widget throughput rates**: `{label: "lines/s"|"bytes/s", text}` masks `text` to `<RATE>` ONLY when `text` matches a finite numeric rate (`^\d+(?:\.\d+)?[kMG]?$`; never `NaN` or arbitrary strings).
