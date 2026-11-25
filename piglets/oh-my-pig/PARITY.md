# Parity with omp

oh-my-pig follows omp at release tag **`v19.1.1`** (commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`, 2026-11-23, omp 19.1.1).
This file is the only claim of parity this Piglet makes.

States used in this document:

- **same**: real `omp` (`v19.1.1`) and oh-my-pig were run on the same input and matched with zero unexplained differences in that channel.
- **expected**: compared against real `omp`; every difference matched a narrow, cited allowlist entry (`ON_PURPOSE`, `HOST`, `LISTED`, `PORT_EXTRA`, `NO_CONSUMER_AT_TAG`, or `OMP_ARTIFACT`).
- **tests**: the port has its own Go tests, not compared against live `omp` output.
- **listed**: a documented omission or difference in an area `UPSTREAM.md` or `piglets/pig-tern/frontend/OMP-UPSTREAM.md`.
- **host**: requires a change in PiG's host or SDK.
- **absent**: not ported.

---

## Final Gate Proof Runs (Single Binary `r25`, PiG `v0.8.0`)

Earlier "0 unexplained" claims were withdrawn after an audit of the proof harness's allowlists and normalisers (the audit and the review notes cited below are working notes of the port and are not in this repository): the oracle hid details differences under listed text, path masks hid unstandardised macOS `/private` paths, the Tern comparer checked only one node per part without frame alignment, and the terminal comparer ignored per-cell colours and SGR attributes. All three harnesses were corrected, the port was fixed against them, and all three proof harnesses below ran alone in sequence on **one final Binary**: **`r25`** (`75,176,658` bytes, sha256 `e12096dc49b5b08da302adb17c09a591863577b2cd14604fa4201d4b12e92819`), built from the source of the PiG release `v0.8.0` (commit `73585394f40c`, Pi 1.6.0).

The reference is the omp `19.1.1` binary. omp updates itself: on the day of these runs `omp` on `PATH` became `19.2.1`, and a first run compared against it reported omp's own changes as 2 call and 16 surface differences. The harnesses now refuse any omp but `19.1.1` (`proof/omp_version.sh`); the runs below name the `19.1.1` binary with `OMP_BIN`.

### Block 1 — Per-Tool Oracle (`proof/oracle.sh`)

```text
Command: ORACLE_MODE=both OH_MY_PIG_BIN=<bin> ORACLE_TOOL_DIRS=<tools> PIG_SRC=$PIG_SRC \
           ORACLE_OUT=<out>/oracle piglets/oh-my-pig/proof/oracle.sh piglets/oh-my-pig/proof/cases
Run:     Binary r25 (sha256 e12096dc49b5b08da302adb17c09a591863577b2cd14604fa4201d4b12e92819), omp 19.1.1,
         measured 2026-11-25 21:29Z (load ~22-28 from other work), alone, no allowances, exit 0
Totals:  60 cases (0 failed), 359 calls:
         269 identical, 82 details-listed, 8 differs-listed, 0 details-only, 0 timing, 0 differs, 0 missing
Recorded tool_execution_start: 356 identical, 3 recorded-listed (HOST), 0 recorded-differs
Surfaces: 8 parent snapshots listed (0 differs),
          6 child surfaces across 5 cases paired by agent id — 6 listed (0 differs, 0 missing),
          54 follow-ups identical + 6 listed (0 differs)
Allowlists: 0 stale across details (46; 3 real_model_only), text (10), recorded (1), surface (26)
Hygiene: network_attempts=0, leaked_pids=0
```

### Block 2 — Headless Tern Drawing (`proof/run.sh`)

```text
Command: OH_MY_PIG_BIN=<bin> ORACLE_TOOL_DIRS=<tools> PIG_SRC=$PIG_SRC \
           OMP_PROOF_ROOT=<out>/tern piglets/oh-my-pig/proof/run.sh
Run:     Binary r25 (sha256 e12096dc49b5b08da302adb17c09a591863577b2cd14604fa4201d4b12e92819), omp 19.1.1,
         measured 2026-11-25 21:32Z (load ~22-25 from other work), alone, no allowances, exit 0
Capture: capture_valid=true (33/33 turns consumed per side, in_flight=0)
Parts:   30 parts across 23/23 steps (0 not-compared steps):
         13 same, 17 expected, 0 timing, 0 differs, 0 missing
Sub-diffs: 0 unexpected, 0 timing, 28 allowlisted (ON_PURPOSE=5, HOST=19, LISTED=4; 3 class_in_doubt),
           6 noop-default (counted separately)
Frame alignment (all 23 steps):
         347 expected / 0 unexplained unmatched region children (all 347 HOST)
         413 expected / 0 unexplained matched-subtree visible-node-count deltas (unaccounted=0)
Final step (18-narrow-80): 357 omp vs 199 pig visible TSP nodes (main 70/56, dock 13/11, layer 0/0)
Allowlist: 39 entries in drawing_allowlist.json, 0 stale, 0 zero-hit
```

### Block 3 — Plain-PTY Terminal Drawing (`proof/ansi.sh`)

```text
Command: OH_MY_PIG_BIN=<bin> ORACLE_TOOL_DIRS=<tools> PIG_SRC=$PIG_SRC \
           ANSI_OUT=<out>/ansi piglets/oh-my-pig/proof/ansi.sh
Run:     Binary r25 (sha256 e12096dc49b5b08da302adb17c09a591863577b2cd14604fa4201d4b12e92819), omp 19.1.1,
         measured 2026-11-25 21:34Z (load ~22-25 from other work), alone, no allowances, exit 0
Capture: capture_valid=true (33/33 turns, 15 prompts, 5 snapshots, 0 leaked PIDs per side;
         scrollback 232 omp / 237 pig lines)
Final screen (80x24, --screen-regions):
         verdict=expected, total_unexplained_all=0 (text=0, blank=0, colour=0, mode=0), timing_pairs=0
         text: 2/2 hunks expected (11 HOST lines); blank: 1 expected;
         colour+attrs: 8/8 runs expected across 12 text-matching lines (8 HOST);
         mode: 1 expected (OMP_ARTIFACT alt-screen borrow inside resize only);
         dock height: omp 15 vs pig 15 (delta 0); full-screen clears: omp 2 vs pig 6
Full scrollback (120x40 -> 80x24):
         verdict=expected, total_unexplained_all=0 (text=0, blank=0, colour=0, mode=0), timing_pairs=0
         text: 7/7 hunks expected (33 lines: ON_PURPOSE=5, HOST=22, LISTED=6); blank: 9 expected;
         colour+attrs: 71/71 runs expected across text-matching lines (66 HOST, 5 LISTED);
         mode: 1 expected (OMP_ARTIFACT)
Allowlist: 19 entries in terminal_allowlist.json (9 text, 5 colour/attrs, 4 blank_row, 1 terminal_mode)
```

### Block 4 — Gate Suites, Differentials, Vet & Repository Checks

- **Full extension suites (PiG `v0.8.0` SDK, measured `2026-11-25 21:26Z`, Tern env vars unset, load ~23-28 from other work)**: **31 packages `ok`, 0 `FAIL`, 3 packages without test files; 1,169 top-level tests: 1,169 `PASS` / 0 `FAIL` / 0 `SKIP`** (with real tools including `gopls` and `ast-grep`, `OMP_REAL_BROWSER=1` with the two click-loop tests at `OMP_LOOP_ITERS=200`, `OMP_TERN_DAEMON_TEST=1`, `OMP_REGEN_JS_REWRITER=1`, and the full approval and intent differential corpora with `FAIL=1`).
- **Full 2,000-iteration real-Chrome click loops (run separately on the release candidate's SDK, not repeated on `v0.8.0`; `-timeout 60m`, `OMP_REAL_BROWSER=1`, display locked, load 23 → 9)**: `TestRealPass4_F1_NavigatingClickLoop2000Iterations` **PASS** (`60.07s`) and `TestRealPass5_F1_NoURLTabNavigatingClickLoop2000Iterations` **PASS** (`559.56s`) — 2,000 iterations each with sibling tab open and verified, `wedges=0`.
- **Frontend, vet, harness & repository checks**:
  - `piglets/pig-tern/frontend` Go suite: **138 `Test*` functions, `ok`** (with `-race`); `gofmt` clean and `go vet` exit 0 on `darwin`, `linux`, and `windows` for the extension module, `pig-tern/frontend`, and `piglets/oh-my-pig/frontend`.
  - Proof harness unit tests (`proof/test_oracle_harness.py`): **43 tests OK**; hygiene grep for developer paths: 0 hits.
  - Repository checks (PiG pinned to `v0.8.0`): `npm run check` **exit 0**; `node scripts/quality-gates.mjs` **exit 0**; `npm test` **492 tests — 489 pass, 2 skipped, 1 fail** (`scripts/npm-packages.test.mjs:112` for the `ahp` package, pre-existing before this work).
- **Argument validation differential** (`proof/diff/intent/`, 20,065 cases, run on the PiG `v0.8.0` SDK with `FAIL=1`; checked-in reduced corpus `internal/intent/testdata/omp_diff_reduced.jsonl`, 1,201 cases, `go test ./internal/intent -run TestOmpDifferentialReduced`):
  - **Strict tools (17,479 cases = 13,816 base + 3,663 extra)** with PiG host validator: **17,084 equal** (11,798 accepts + 5,286 rejections, 5,228 byte-identical, 58 differ only in `Received arguments:` key order), **366** undeclared keys dropped so PiG's host accepts them, **29** array-for-object coercions normalized to `{}`, **0** wrong rejections, **0** wrong accepts, **0** host refusals.
  - **Lenient tools (`glob`, `todo`, `ask`, `task`: 2,586 cases = 2,106 base + 480 extra)** with PiG host validator: **1,235 equal**, **26** `glob` unknown-key refusals (matching `glob.execute`), **813** raw fallback refused by the tool in `PrepareArguments`, **8** raw fallback refused by PiG's host validator (host gap), **7** raw fallback altered by port, **497** undeclared-keys-only (`task` `tasks[].tools`), **0** other normalized differences.
- **Approval gate differential** (`proof/diff/approval/`, 32,599 cases + 17 unwrap-route checks, run on the PiG `v0.8.0` SDK with `FAIL=1`; checked-in reduced corpus `testdata/approval_diff_reduced.jsonl`, 3,873 cases, `go test . -run 'TestOmpApprovalDifferentialReduced|TestOmpApprovalUnwrapRoute'`):
  - **29,992 equal**, **47** expected `port-stricter` (two-view operator+session rule), **80** expected `port-ignores-untrusted-project` (PiG trust gate), **48** expected `fail-closed` (malformed global config falls back to `always-ask`), **2,432** not compared (1,232 absent in omp for session, 1,040 `edit` native-addon stub, 160 legacy `glob` `pattern`), **0** unexpected mismatches; **17/17** bracketed `proc://<job>/kill` unwrap-route checks clean (**0** violations).
- **Bash interceptor differential** (`internal/shell/testdata/gen_interceptor_diff.ts` + checked-in `interceptor_diff.txt.gz`, **200,000** recorded omp verdicts, `go test ./internal/shell -run TestInterceptorDifferentialAgainstOmp`): **0** differences across all 4 lookaround rules ported to Go.
- **Hashline block-op oracle** (`internal/hashline/testdata/blockspans/*.json`, **10,573** anchors across 30 files from real `omp` 19.1.1, `go test ./internal/hashline -run TestBlockSpansAgainstRealOmpOracle`): **0 WRONG** spans; on held-out Sample 3 (9 resolvable languages, 4,088 anchors, 1,604 omp-resolved): **1,063** exact matches, **541** conservative refusals (**33.7%**), **0 WRONG**; plus third-party YAML hold-out (1,666 anchors): 644 equal, 33 refused, 989 refused by both, **0 WRONG**.
- **JS cell rewriter** (`internal/eval`, `go test ./internal/eval`): runs omp `v19.1.1`'s bundled rewriter + `@babel/parser@7.29.9`; verified on 190 JS/TS matrix and adversarial cases under `bun` and `node` 22+, and deterministic regeneration verified (`OMP_REGEN_JS_REWRITER=1`).
- **Unit & integration test counts**: **1,169** executable top-level Go `Test*` tests in the extension module (`1,170` `func Test*` declarations including `TestMain` in `internal/ask/main_test.go`; `1,155` in 30 `internal/` packages + `15` in module root), **138** in `piglets/pig-tern/frontend`, and **43** Python `test_*` methods in `proof/test_oracle_harness.py`.
---

## What Each Comparison Covers — and Does Not Cover

Do not read "0 unexplained" as "identical to omp". Each harness checks a bounded slice under strict conditions:

1. **Per-tool oracle (`proof/oracle.sh`)**:
   - **Covers**: one local deterministic scripted model over **60 cases / 359 calls** in offline print mode (`-p --mode json`), comparing result text, `isError`, normalized `details`, `tool_execution_start` args/intent, parent/child system prompts and advertised tools/schemas, and follow-up messages.
   - **Tools with NO oracle case**:
     - Ported tools with zero oracle calls: **`github`**, **`propose`**, **`goal`**, and the **`browser`** eval prelude.
     - Ported tools exercised only on a refusal or narrow path: **`ask`** (print-mode refusal only — omp omits `ask` in `-p`), **`web_search`** (offline no-provider refusal only), **`debug`** (deterministic stdio DAP adapter only, not real `dlv`/`lldb-dap` because macOS sandbox blocks `task_for_pid`), **`find`** (`find.enabled: "on"` with deterministic chat judge, not native classifier).
     - Absent omp tools (24): `security_scan`, `context_notes`, `new_context`, `memory_edit`, `retain`, `recall`, `reflect`, `learn`, `manage_skill`, `think`, `report_issue`, `vibe_spawn`, `vibe_send`, `vibe_wait`, `vibe_kill`, `vibe_list`, `advise`, `init_experiment`, `run_experiment`, `log_experiment`, `update_notes`, `tts`, `generate_image` (and preludes `archive`, `commit`, `dsm`, `ratchet`, `computer`).
   - **Behaviours with NO oracle case**: interactive TUI mode, RPC mode, `/resume`, `/compact`, `/tree`, `/reload`, all 21 ported slash commands, plan/goal/loop/`/btw`/magic-keyword workflows, interactive approval prompts and `cfg://` interactive writes, stop-time `todo` reminders (disabled by omp in `-p`), `issue://`/`pr://`/`mr://`/`attachment://`/`mcp://`, real network providers, and real LLM responses.
   - **Real-model-only allowlist entries (3)**: `read proc.jobs[].widget`, `write proc.jobs[].widget`, and `wait jobs[].widget` for AI-authored `state: "live"` / `"authoring"` specs can only fire with a real model; the offline scripted oracle always observes omp's deterministic `state: "fallback"` view (which is ported and compared).
2. **Drawing proofs (`proof/run.sh` and `proof/ansi.sh`)**:
   - **Covers**: ONE scripted conversation of **23 steps** (`proof/turns.json`, 33 turns per side) at one width (**120×40**) plus one resize (**80×24**) in dark theme (`titanium`).
   - **Evidence-driven capture & validity gate**: both drivers wait on live evidence (prompt/editor readiness, PTY echo, scripted-server completed-turn count with `in_flight == 0`, and visible step postconditions), and both comparers verify capture completeness before diffing (33/33 turns consumed per side, all prompts and snapshots present; an incomplete capture exits `2` with `capture_valid: false` and emits no verdict). Results no longer depend on the output directory's path length (wrapped `raw-http-request=` paths are matched as one logical line and the dock is located structurally).
   - **Welcome tips**: tips are chosen randomly per launch; an exhaustive harness unit test validates every tip in omp's `packages/tui/src/prompt/tips.txt` (**35** tips, plus omp's hard-coded unicode-preset `Please use nerdfont 😭.` tip) and every tip in the port's two lists — **16** tips in `internal/ansi/chrome.go` (`welcomeTips`, 10 drawn from omp's `tips.txt` + 6 port-adapted lines for ported commands/keywords) and **14** tips in `piglets/pig-tern/frontend/omp_transcript.go` (`ompWelcomeTips`, 6 drawn from omp's `tips.txt` + 8 port-adapted command lines), filtered to features registered in oh-my-pig (omitting omp-only features and `{tap:...}` key tokens).
   - **Tools drawn (14)**: `read`, `bash`, `edit`, `todo`, `ask`, `eval`, `task`, `wait`, `web_search`, `write`, `grep`, `glob`, `lsp`, `ast_edit` (plus `reject` via `bash`).
   - **Cards, states, widths, and themes NEVER drawn**:
     - Never drawn: `find`, `ast_grep`, `debug`, `github`, `checkpoint`, `rewind`, `propose`, `goal`, `yield`, `browser`, `proc://` and `cfg://` cards, multi-file edits, long-running streaming/async job tiles, nested subagent trees, interactive approval dialogs, model/session/tree/settings pickers, `/btw` panel, goal status bar, inline image/media blocks.
     - Never compared: pixels, fonts, kerning, layout geometry, light theme, widths other than 120 and 80, non-macOS terminals.
   - **Terminal per-cell colour & attribute rules**: colours and SGR attributes (`bold`, `faint`, `italic`, `underline`, `inverse`, `strike`) are compared per cell **only on text-matching lines** (lines whose text differs follow their text allowlist entry); animated regions (`welcome` gradient sweep, working shimmer) are checked by membership in omp's exact gradient/shimmer stop sets (`@omp_welcome_gradient`, `@omp_spinner_tween`), not frame-by-frame equality.
3. **Platforms, load, display state, and models**:
   - Nothing was executed on **Linux** or **Windows** (`GOOS=linux` and `GOOS=windows` `go vet` only); a **real model** was never used in any proof run.
   - **Load sensitivity**: omp's `Wall time: <N> seconds` notice triggers at `1000 ms`; on a busy machine fast commands cross `1 s` on one side only and the oracle exits non-zero (clean gate runs were at load average under ~10 on 14 cores: ~1 for oracle, ~3 for Tern/terminal).
   - **Locked macOS display**: the host display was **locked** (`CGSSessionScreenIsLocked=Yes`) during every browser and drawing run.

---

## Differences Rebuilt From the Allowlist Files

Every item below comes directly from the six checked-in allowlist files (`piglets/oh-my-pig/proof/{surface,text,recorded,details,drawing,terminal}_allowlist.json`) and the `UPSTREAM.md` / `OMP-UPSTREAM.md` rows they cite, ordered **model-facing first**, then **user-facing (Tern & terminal)**, then **details-only**.

### 1. Model-Facing Differences (`surface_allowlist.json` [26], `text_allowlist.json` [10], `recorded_allowlist.json` [1])

**`ON_PURPOSE`** (2 entries):
- **Role line (parent & child system prompts)**: the model reads `You are oh-my-pig's trusted coding assistant.` instead of `You are omp's trusted coding assistant.` (`internal/prompt/UPSTREAM.md:92`).

**`HOST`** (6 entries: 5 in `surface_allowlist.json` + 1 in `recorded_allowlist.json`):
- **`mcp://` omitted from parent & child system prompts**: PiG SDK lacks MCP client `resources/list` and `resources/read`, so `mcp://` is hidden from `# Internal URLs` (`internal/urls/UPSTREAM.md:68`).
- **Depth-1 subagents cannot delegate (`child_system_prompt`, `child_tool_membership`, `child_tool_description`)**: PiG does not bind `createChildSession` on child sessions (`coding/create_agent_session.go:149`), so depth-1 children omit `task` from their tool list, `# Tool Inventory`, `# Delegation`, and `eval`'s `Session tool names:` line (`internal/agents/UPSTREAM.md:111`).
- **`tool_execution_start` keeps `i` inside `args` (`recorded_allowlist.json`)**: PiG emits raw `args` before `PrepareArguments` with no top-level `intent` field (`$PIG_SRC/agent/tool_execution.go:640`, `coding/cli/rpc_events.go:146-147`).

**`LISTED` — System prompt & tool descriptions/schemas** (19 entries in `surface_allowlist.json`):
- **System prompt lines omitted (parent & child)**: `proc://<id>/streams` clause (`internal/shell/UPSTREAM.md:116`); `ssh://` (`internal/urls/UPSTREAM.md:75`); `omp://` (`internal/urls/UPSTREAM.md:74`); `report_issue` Automated QA `<critical>` block (`internal/prompt/UPSTREAM.md:101`); `commit` and `dsm` eval prelude guidance paragraphs (`internal/prompt/UPSTREAM.md:102`).
- **Same-batch sibling status in child `# Peers` (`peers_sibling_idle_vs_running`)**: in a multi-child batch under an instant scripted model, a sibling listed in `# Peers` appears as `running` in oh-my-pig where omp shows `idle` because omp speculatively launches the first batch item while arguments are still streaming (`internal/agents/UPSTREAM.md:119`).
- **Tool descriptions & schema narrowed to what is ported**: `read` omits AST folding, PDF/DOCX/SQLite/7z/RAR/jq/video/SVG promises (`internal/files/UPSTREAM.md:29`); `bash` omits "5.3", `pty` = interactive, internal-URI VFS, and the ~95 in-process builtins list (`internal/shell/UPSTREAM.md:114`); `eval` omits `@tool`/`tool(fn)` (`internal/eval/UPSTREAM.md:98`) and `archive`/`commit`/`dsm` namespaces (`internal/eval/UPSTREAM.md:107`); `task` description and JSON schema omit `tasks[].tools` (`internal/agents/UPSTREAM.md:107`); `write` advertises only stdlib-writable archive formats (ZIP, TAR, TAR.GZ/TGZ) and omits SQLite/non-stdlib `.tar.zst`/`.asar` writes (`internal/files/UPSTREAM.md:38`).
- **Vision follow-up attachment (`read-image-vision`)**: the model receives the original 92-byte PNG instead of omp's auto-resized/WebP-transcoded 184-byte attachment (`internal/files/UPSTREAM.md:84`).

**`LISTED` — Tool result text** (10 entries in `text_allowlist.json`):
- **`proc://<id>/streams` and `/streams/<n>` (`bash-notify-streams-cancel` #5, #6)**: always returns the empty-streams notice or `Stream not found` (`isError=true`) because system `bash` has no pipe/redirection taps (`internal/shell/UPSTREAM.md:116`).
- **`help://read/urls` (`internal-urls-read-write` #5)**: omits `ssh://` (`internal/urls/UPSTREAM.md:75`) and marks `proc://<id>/streams` unavailable and service mode session-only (`internal/shell/UPSTREAM.md:116,119`).
- **`read` image metadata line (`read-image-vision` #0)**: omits omp's auto-resize/WebP transcoding note (`internal/files/UPSTREAM.md:84`).
- **Named services in print mode (`bash-async-and-service` #3, #4)**: omits omp's ` — persistent` suffix because in-process `ServiceManager` is session-only (`internal/shell/UPSTREAM.md:119`).
- **Unknown-agent `Searched:` list (`task-errors-and-agent-urls` #0)**: prints `~/.pig/agent/agents, ~/.omp/agent/agents` where omp prints `~/.omp/agent/agents` (`internal/agents/UPSTREAM.md:118`).
- **Non-stdlib archive write refusal (`write-archive-errors-and-edge-cases` #5)**: `write` to `zstd.tar.zst:a.txt` returns a stdlib-only refusal (`isError=true`) where omp writes `tar.zst` (`internal/files/UPSTREAM.md:91`).
*(Note: model-facing `Did you mean <tool>?` suggestions for unknown tool names are now ported via `sdk.EventContext` rewrite, so `argument-validation` #6 and #7 match omp's text; only their agent-loop `{isError, error}` details and stored transcript text differ — `internal/toolcmd/UPSTREAM.md:83`.)*

### 2. User-Facing Differences — Headless Tern (`drawing_allowlist.json` [39])

**`ON_PURPOSE`** (5 entries):
- **Surface root & welcome branding**: surface role is `pig.session` instead of `omp.session` (`OMP-UPSTREAM.md:93`); welcome logo alt and wordmark say `oh-my-pig`, version shows the Piglet version (`v0.1.0`), and the welcome tip is chosen randomly from tips filtered to registered commands (`OMP-UPSTREAM.md:18`).

**`LISTED`** (4 entries):
- **Bash command block (`tool card: bash`, `long bash` folded & expanded)**: drawn as `code[omp.tool.bash.command]` (`lang:"bash"`) showing the command as typed rather than omp's `shell` AST visualizer (`OMP-UPSTREAM.md:97-99`). Shared props and command text still match below `$kind`.
- **Subagent `auto` thinking badge (`tool card: task`)**: shows `thinkingHigh` (provisional fallback) where omp's LLM `auto` judge selects `thinkingLow` (`internal/agents/UPSTREAM.md:114`).

**`HOST` — confirmed** (27 entries across parts and frame children):
- **Assistant per-turn usage & thinking nesting**: `row[omp.turn.usage]` is absent (`OMP-UPSTREAM.md:95`), and `section[omp.thinking]` mounts as a top-level sibling in `main` rather than inside `col[omp.assistant]` (`OMP-UPSTREAM.md:40`).
- **Working row Stop control**: `row[omp.working.stop]` (`escape` / `Stop`) is omitted (`OMP-UPSTREAM.md:96`).
- **Live `ask` dialog**: PiG swaps `dock.editor` for a `picker` in `layer` instead of mounting inline `col[omp.ask.card]` in `main` alongside a live `col[omp.editor]` (`OMP-UPSTREAM.md:44,100`).
- **Provider error card & banner**: `card[omp.error]` omits the `Retry` (`F5`) action and omp's trailing `raw-http-request=<log>` path; because the formatted error is 1 line without that dump path, `row[omp.errorBanner]` omits the 2-line `Details` button (`OMP-UPSTREAM.md:51`).
- **Slash autocomplete**: PiG dumps an `ansi` list into `dock.below` instead of mounting `overlay[omp.autocomplete](col[omp.select](list))` in `layer` (`OMP-UPSTREAM.md:52`).
- **Composer context meter (`composer`, `slash closed`, `narrow 80`)**: omits compaction threshold `marks`, tooltip reads `Auto-compact enabled` instead of `Auto-compact at 85%`, and `value` uses total tokens (`155`, `1404`) rather than prompt-only tokens (`120`, `1366`) (`OMP-UPSTREAM.md:43`).
- **Dock HUD pills & progress**: `row[omp.hud]` (agent-count pills) and the dock `judge`/`downloads`/`compaction` container are absent (`OMP-UPSTREAM.md:41`).
- **Empty `col[omp.assistant]` before tool turns**: PiG emits zero ops for tool-call-only turns where omp mounts an empty 0×0 `col[omp.assistant]` (`OMP-UPSTREAM.md:45`).
- **Status toasts & host transcript lines in `main`**: `"Tool output: expanded"`, `"Plan mode enabled..."`, `[Extensions]`, `Piglet oh-my-pig hid 2 tools`, and the `/bug` hint arrive as plain `ansi` transcript lines instead of `toast[omp.toast.status]` or silent chrome (`OMP-UPSTREAM.md:42,46`).

**`HOST` tagged `class_in_doubt`** (3 entries — flagged by the audit because the Piglet may be able to reach part of the behaviour):
- **`card[omp.user]` `actions.hold`, `row[omp.user.tools]`, and `col[omp.assistant]` `actions.hold`** (3 entries): timestamps and per-message rewind targets are HOST, but OSC 52 copy and `/rewind` via `Env.Send` may be reachable (`OMP-UPSTREAM.md:94-95`). *(The earlier 4th entry for the composer model chip display name was removed once `Options.ModelDisplayName` was wired; `drawing_allowlist.json` has 39 entries and 0 stale.)*

### 3. User-Facing Differences — Plain-PTY Terminal (`terminal_allowlist.json` [19])

**`ON_PURPOSE`** (2 text entries):
- **Welcome version & random tip**: displays Piglet version (`v0.1.0`) and a random filtered tip validated against source tip lists and 72-col wrap (`internal/ansi/UPSTREAM.md:89`).

**`LISTED`** (1 text + 1 colour entry):
- **Bash command text**: renders `$ <command>` instead of omp's Rust shell visualizer pseudo-code (`internal/ansi/UPSTREAM.md:95`).
- **Syntax highlighting in `eval` code bodies**: plain `toolOutput` fg instead of tree-sitter/syntect syntax colours (`internal/ansi/UPSTREAM.md:86`).

**`HOST`** (5 text + 4 colour + 3 blank-row entries):
- **Stock editor frame**: PiG's stock editor draws top/bottom `───` rules and a blank input row instead of omp's frameless `╰─ ` gutter (integrator decision: delegated `SetEditorComponent` drops `/` and `@` completion and deadlocks exit; `internal/ansi/UPSTREAM.md:110`).
- **Host startup notices & blank separators**: `[Extensions]` and `Piglet oh-my-pig hid 2 tools from the registry: powershell, ls` print because a Piglet cannot set `quietStartup` (`internal/ansi/UPSTREAM.md:107`).
- **Assistant markdown fenced-code indent & blank line**: 3-column indent (`markdown.codeBlockIndent` default `"  "` + 1 host margin) because a Piglet cannot override host markdown settings (`internal/ansi/UPSTREAM.md:108`).
- **Ctrl+O status text**: host prints `Tool output: expanded` instead of `Tool output expansion: enabled` (`internal/ansi/UPSTREAM.md:106`).
- **Host transcript provider error & `/bug` hint + omp `raw-http-request=`**: host prints raw JSON on the transcript `Error:` line and `/bug` hint (the port's pinned `✘ 400 ...` banner now formats identically to omp, omitting only omp's `raw-http-request=` log path; `internal/ansi/UPSTREAM.md:109`).
- **Host-owned colours**: user prompt bubble lacks `#0f1216` bg / `#e5e5e7` fg; assistant markdown & thinking use PiG's default theme tokens (`38;5;1,3,4,5,6`, `faint+italic`) instead of `titanium`; `ctx.Notify` status lines (`Plan mode enabled...`) use SGR faint + default fg instead of dim truecolor; welcome header is static 3-stop gradient instead of animated sweep (validated inside `@omp_welcome_gradient`) (`internal/ansi/UPSTREAM.md:89-90`).

**`OMP_ARTIFACT`** (1 text + 1 blank-row + 1 terminal-mode entry):
- **omp resize alt-screen borrow & stale pre-resize dock copy**: on width resize, omp briefly enters `CSI ?1049h`…`?1049l` (verified strictly inside the resize byte range on omp, never on port) and can leave a stale 120-col pre-resize dock block in scrollback while PiG clears cleanly via `CSI 2J + CSI 3J` (`internal/ansi/UPSTREAM.md:105`).

### 4. Details-Only Differences (`details_allowlist.json` [46])

- **`HOST`** (3 entries): agent-loop prepare-phase failures return `{}` instead of omp's `{isError: true, error}` (`$PIG_SRC/agent/tool_execution.go:323-327`, 8 calls); failing `todo` `PrepareArguments` returns `{}` instead of `{phases, storage}` (`internal/todo/UPSTREAM.md:46`, 1 call).
- **`LISTED`** (26 active entries): `bash` omits `trace` (36 calls, `internal/shell/UPSTREAM.md:117`) and `toolCommands[].tier` (9 calls, `internal/toolcmd/UPSTREAM.md:87`); `proc://` read/write daemon snapshots use service name as `id`, `policy.hold: "leases"`, and omit 9 out-of-process broker fields (`command`, `cwd`, `generation`, `leases`, `outputBytes`, `owner`, `readyMatch`, `restartCount`, `scope`, 1 call each, `internal/shell/UPSTREAM.md:120`); piped `bash` job fallback widget counts `outputTap` lines (`2`) without pipe-tap lines (`4`, 3 calls across `read`/`wait`, `internal/shell/UPSTREAM.md:116`).
- **`LISTED [real_model_only]`** (3 entries, 0 hits in offline oracle): `read proc.jobs[].widget`, `write proc.jobs[].widget`, and `wait jobs[].widget` when omp's background cheap-model spec author produces `state: "live"` / `"authoring"` (`internal/shell/UPSTREAM.md:121`, `internal/agents/UPSTREAM.md:94`).
- **`PORT_EXTRA`** (14 entries — extra fields emitted by the port for PiG's frontend/renderers, visible in JSON/RPC): `bash` `toolCommands[].callId` (9) and `toolCommandsSole` (4, `internal/toolcmd/UPSTREAM.md:87`); `edit` `patch` (9) and `perFileResults[].patch` (1, `internal/files/UPSTREAM.md:43`); `write` `diff` (18) and `patch` (18, `internal/files/UPSTREAM.md:42`); `read` `isDirectory: true` on `proc://` directory reads (6, `internal/shell/UPSTREAM.md:120`); `proc://` daemon convenience fields `name`, `pid` (read), `persist`, `detached` (`internal/shell/UPSTREAM.md:120`).

---

## Needs a Change in PiG (Deduplicated)

One deduplicated list across oracle, Tern, terminal, and extension areas, naming what omp does that oh-my-pig cannot do today and the verified location in `$PIG_SRC`:

1. **No notification / toast marker in `frontend.Frame`**: omp shows ephemeral `toast[omp.toast.status]` banners (`Tool output expansion: enabled`, `Plan mode enabled...`); PiG's `showStatus` and `ctx.Notify` append unmarked `frontend.Lines` to `RegionMain` (and draw `ctx.Notify` in SGR faint instead of dim truecolor) — `$PIG_SRC/extensions/sdk/frontend/frontend.go:450-825`, `internal/codingagent/interactive_chat.go:583-604`, `internal/codingagent/ext_ui_context.go:457-473`, `tui/tui_surface.go:840-841` (`OMP-UPSTREAM.md:42`, `internal/ansi/UPSTREAM.md:90`).
2. **No assistant-turn boundary op for tool-call-only turns**: omp mounts an empty `col[omp.assistant]` before tool cards; PiG emits zero ops when an assistant message has no text/thinking/error — `$PIG_SRC/tui/tui_surface.go:914-944`, `extensions/sdk/frontend/frontend.go:439-453` (`OMP-UPSTREAM.md:46`).
3. **Thinking blocks cannot nest inside `col[omp.assistant]`**: PiG emits `Thinking` as a flat sibling op in `RegionMain` — `$PIG_SRC/extensions/sdk/frontend/frontend.go:439-453` (`OMP-UPSTREAM.md:40`).
4. **Child sessions cannot create child sessions**: PiG binds `createChildSession` only on top-level sessions, so depth-1 subagents cannot delegate (`task` omitted from child tools, prompt, and `eval`'s tool list) — `$PIG_SRC/coding/create_agent_session.go:149` (`internal/agents/UPSTREAM.md:111`).
5. **Context usage counts total tokens and lacks compaction thresholds**: omp counts prompt tokens and renders auto-compact threshold marks (`Auto-compact at 85%`); PiG sums `TotalTokens` and exposes only a boolean `AutoCompact` — `$PIG_SRC/agent/context_tokens.go:32-37`, `extensions/sdk/frontend/frontend.go:809-818` (`OMP-UPSTREAM.md:44`).
6. **Session-level host settings a Piglet cannot set**: a Piglet manifest cannot set `tuiMode: "regular"` (PiG defaults to fullscreen alt-screen with mouse capture), `quietStartup` (so `[Extensions]` and `Piglet oh-my-pig hid 2 tools` print in both Tern and terminal), or `markdown.codeBlockIndent` (3-column fenced-code indent) — `$PIG_SRC/coding/cli/main.go:1423`, `coding/piglet/scope_summary.go:123-151`, `internal/codingagent/configschema/settings.go:42,106`, `internal/codingagent/settings.go:336` (`internal/ansi/UPSTREAM.md:107-108`, `OMP-UPSTREAM.md:47`).
7. **Live `ask` form dialog primitive**: omp renders an inline `col[omp.ask.card]` in `main` while keeping the composer mounted; PiG swaps the editor for a `picker` in `layer` with no multi-field form callback — `$PIG_SRC/extensions/sdk/frontend/frontend.go:38-126,473-547`, `tui/tui_surface.go:578-585,626-638` (`OMP-UPSTREAM.md:45,101`, `internal/ask/UPSTREAM.md:35`).
8. **Dock status pills, judge/downloads/compaction indicators, and queue region**: `frontend.Frame` has no node kinds for `omp.hud` agent pills, `omp.hud.judge`/`downloads`/`compaction`, or `col[omp.queue]` — `$PIG_SRC/extensions/sdk/frontend/frontend.go:450-483,623-825` (`OMP-UPSTREAM.md:41`).
9. **Frontend interrupt/Stop, retry, user-tools/timestamp, per-turn usage, and slash-autocomplete overlay**: `frontend.Env` / `frontend.MarkdownText` / `frontend.Editor` lack interrupt (`row[omp.working.stop]`), retry (`Retry` on `card[omp.error]`, `/retry`), message timestamps/IDs (`row[omp.user.tools]`), per-turn token/cost metrics (`row[omp.turn.usage]`), and structured slash-completion items (`overlay[omp.autocomplete]` vs ANSI lines in `dock.below`) — `$PIG_SRC/extensions/sdk/frontend/frontend.go:38-126,484-498,627-642,740-753,768,801-806`, `tui/tui_surface.go:748-751` (`OMP-UPSTREAM.md:37-39,52-53`).
10. **Delegated terminal editor defects (3)**: `SetEditorComponent` drops `/` completion, never repaints async `@` popups, deadlocks interactive shutdown on exit keys, and lacks an exported `RefreshEditor()` — `$PIG_SRC/tui/editor_autocomplete_request.go:54`, `tui/editor_async_provider.go:303,345-368`, `extensions/sdk/editor_component.go:280-394`, `internal/codingagent/remote_editor.go:532`, `internal/codingagent/interactive_input.go:391-412` (`internal/ansi/UPSTREAM.md:110`).
11. **No custom theme registration or host-text overrides**: extensions cannot register omp's `titanium` palette for user bubbles / assistant markdown (`internal/ansi/UPSTREAM.md:90`), override `Tool output: expanded` (`$PIG_SRC/internal/codingagent/interactive_editor.go:120`), suppress host-drawn transcript `Error:` / `/bug` lines (`internal/codingagent/interactive_chat.go:524-531`, `ai/errors.go`), or stop host `CSI 2J + CSI 3J` full-screen clears on modal/expand/resize (`tui/tui.go:2634-2674,2970`, 6 clears vs omp's 2).
12. **No global extension keybinding hook (`todo.toggle`)**: extensions cannot bind omp's `todo.toggle` shortcut (`internal/todo/UPSTREAM.md:40`).
13. **Agent-loop & tool-execution host gaps (8)**:
    - `tool_execution_start` keeps raw `i` in `args` with no `intent` field (`$PIG_SRC/agent/tool_execution.go:85,127,640-646`, `coding/cli/rpc_events.go:146-147`);
    - `errorToolResult` drops `{isError, error}` on prepare/loop errors and drops `PrepareArguments` error details like `todo`'s `{phases, storage}` (`agent/tool_execution.go:323-327,378-381`);
    - unknown-tool persisted transcript / `tool_execution_end` text stays bare `"Tool <name> not found"` even though model-facing text is rewritten via `EventContext` (`agent/tool_execution.go:366-369`, `internal/toolcmd/UPSTREAM.md:83`);
    - `tool_prepare_arguments` decodes into `map[string]any`, losing call key order in `Received arguments:` (`extensions/sdk/extension.go:1662-1670`, 58 strict validation cases);
    - no host-validation opt-out for lenient tools (`agent/tool_execution.go:399-402`, 8 lenient raw-fallback cases);
    - no streaming partial-tool-argument delivery before execution, so omp's speculative batch launch (`task/speculative-launch.ts`, starting batch item 0 while arguments still stream) and speculative eval/edit previews cannot be ported (`internal/agents/UPSTREAM.md:89,119`, `internal/eval/UPSTREAM.md:96`);
    - `SetActiveTools` is a set with no ordered mid-session insert (`coding/session_loadout.go:178-189`, `internal/toolcmd/UPSTREAM.md:86`);
    - no MCP client `resources/list` or `resources/read` (`mcp://`, `internal/urls/UPSTREAM.md:68`), no forced wire `tool_choice`, and `NewSession` is restricted to command contexts.
*(Separate from PiG host limits, live pipe tapping — `proc://<id>/streams` and piped bash fallback widget `lines` counts — requires an embedded shell VM rather than a PiG host change; see `Changed on purpose`.)*

---

## Summary Surface Table

| Area | omp has (`v19.1.1`) | Here | Notes |
|---|---|---|---|
| Built-in tools on by default | 15 (12 top-level + 3 mounted in `bash`) | 15 (12 top-level + 3 mounted in `bash`) | Top-level: `read`, `edit`, `write`, `bash`, `grep`, `glob`, `eval`, `task`, `wait`, `todo`, `ask`, `web_search`. Mounted: `lsp`, `ast_edit`, `debug`. (`ask` omitted in `-p` on both sides.) |
| All model-facing tools | 49 (29 built-in + 3 hidden + 4 special + 13 mode/extra) | 25 (20 built-in + 2 hidden + 3 special) | 24 absent (see coverage list above). |
| Eval preludes (optional namespaces) | 6 (`browser`, `archive`, `commit`, `dsm`, `ratchet`, `computer`) | 1 (`browser`, accepted in 8th review) + 2 host bridges (`agent`, `workpool`) | `archive`, `commit`, `dsm`, `ratchet`, `computer` absent. |
| Internal URL schemes | 20 | 15 registered (14 working, 1 `mcp://` SDK-gap stub) | Absent: `omp://`, `ssh://`, `vault://`, `memory://`, `security://`. |
| System prompt | 1 | 1, with listed sections omitted | 49-fixture bun oracle test (`TestOracleFixtureParity`) byte-identical for ported sections; 8 parent + 6 child snapshots classified `listed` (0 unexplained, 0 missing) on `r25`. |
| Headless Tern drawing | 30 parts / 23 steps | 13 same, 17 expected, 0 differs (Block 2) | 347 expected unmatched children + 413 expected count deltas, 0 unexplained, `capture_valid: true` (`r25`). |
| Plain-PTY terminal drawing | 120×40 → 80×24 | 0 unexplained across text, blank, colour+attrs, mode (Block 3) | Dock height 15 vs 15; clears 2 vs 6; `capture_valid: true` (`r25`). |
| Slash commands | 89 built-in (+ `/skill:<name>`) | 21 of omp's own (+ 2 aliases `/rewind`, `/q`) + 17 PiG same-name (+ `/skill:<name>`) | 51 omp commands absent; Go tests only (`internal/modes`: 21, `internal/todo`: 20). |
| Modes and directors | 16 (15 modes + 1 magic-keyword family of 5 words) | 5 (`plan`, `goal`, `loop`, `btw`, magic keywords ×5) | 11 absent (`plan-yolo`, `vibe`, `advisor`, `autoresearch`, `prewalk`, `ratchet`, `live`, `collab`, `cleanse`, `tan`, plus `rpc-ui`/`acp`). Go tests only. |
| Shipped agents | 5 (`scout`, `reviewer`, `security-reviewer`, `task`, `sonic`) | 5 (`scout`, `reviewer`, `security-reviewer`, `task`, `sonic`) | `task.maxEffort` honoured; `auto` thinking falls back to `"high"` (no judge classifier). |
| Settings | 644 keys across 42 domain modules | 644 in schema (`cfg://`), **101** consumed by areas | **543** keys are accepted/validated by `cfg://` and silently ignored at runtime (see [Settings](#settings)). |
| Key bindings / CLI / Providers | 79 keybindings, 52 CLI subcommands, 72 launch flags, 98 providers | PiG's | Not compared. |

---

## Per-Tool Table

| Tool | State | Evidence (`r25` / tests) | Main things left out |
|---|---|---|---|
| `read` | **same** (text, dirs, HTTP/HTML, `?q=` jq, `rule://`, conflict scan, URLs) / **listed** (image meta, streams) | Oracle: 112 calls / 39 cases (102 identical, 5 details-listed, 5 differs-listed). 50+ Go tests. | PDF/DOCX/PPTX/XLSX/EPUB; SQLite; 7z/RAR/ISO/CAB/DEB/RPM/CPIO/AR/ASAR; video; SVG `:img` / image resize / `?q=` vision; binary `dsm`; tree-sitter declaration-only summary; workspace suffix resolution; multi-range streaming >4 MiB. |
| `write` | **same** (files, stdlib archives ZIP/TAR/TAR.GZ, `conflict://` prefix recovery) / **listed** (service stop text, `.tar.zst` refusal, `diff`/`patch` extras) | Oracle: 55 calls / 19 cases (33 identical, 20 details-listed, 2 differs-listed). 61 Go tests in `internal/files`. | Non-stdlib archive writes (`.tar.zst`/`.tzst`, `.asar`); SQLite row writes; `lsp.formatOnWrite` structured detail; SQLite held-open guard. Writing one member rewrites the whole archive, as in omp: every member's mode and time are normalized (`0644`, a fixed time), directory records and empty directories are dropped, links to files become files, links that cannot be read are refused, and the archive file itself gets the default mode under the umask. |
| `edit` | **same** / **listed** (lexical block ops, `patch` extra) | Oracle: 22 calls / 6 cases (13 identical, 9 details-listed). Block-op oracle: 10,573 anchors, 0 WRONG (Sample 3: 33.7% refused). 187 Go tests. | Tree-sitter block resolution (lexical only for ts/tsx/js/json/go/py/md/rs/yaml); syntax-probed warnings/blackbox/auto-repair; non-hashline dialects; streaming previews; SSH mirror writes. |
| `bash` | **same** / **listed** (`trace`, `toolCommands` fields) | Oracle: 53 calls / 19 cases (12 identical, 41 details-listed). Interceptor diff: 200,000 cases (0 diffs). Approval diff: 32,599 cases (0 unexpected). 79 Go tests + 52 (`toolcmd`). | Embedded bash 5.3 VM & ~95 builtins; `proc://<id>/streams`; `details.trace`/AST visualizer; finite-command PTY overlay; persistent out-of-process daemon; shell minimizer/Spotlight; shell URL VFS; `direnv`; `bash.gitGuard`; `bash.allowCompoundCommands`; Kitty/Sixel; user rc snapshot; Windows `SIGTERM` trap grace. |
| `grep` | **same** | Oracle: 14 calls / 3 cases (14 identical). 37 Go tests. | PCRE2 look-around/backreferences (RE2 only); archive/HTTP/ipynb grep; `grep.contextBefore`/`After`; comma/whitespace path lists; parallel directory walk. |
| `glob` | **same** | Oracle: 11 calls / 3 cases (11 identical). 37 Go tests. | Streaming `onUpdate` progress; `eagerDelegation` description branch. |
| `eval` | **same** | Oracle: 17 calls / 5 cases (16 identical, 1 details-listed agent-loop timeout). JS rewriter: 190 cases under `bun` & `node` 22+. 53 Go tests. | In-process Bun worker; `%bun add`; persistent shell/builtins/VFS in `sh()`; `completion`/`judge`/`judge_batch`; `@tool`/`tool(fn)`; `eval.autoBackground`; speculation; live token `budget`; `archive`/`commit`/`dsm`/`ratchet`/`computer` preludes; image resize; artifact spill. |
| `task` | **same** / **listed** (`tasks[].tools`, depth cap 1, `Searched:`, `auto` thinking) | Oracle: 9 calls / 7 cases (8 identical, 1 differs-listed `Searched:`). 50 Go tests. | Depth > 1 delegation (HOST); `tasks[].tools`; `auto` thinking LLM judge (falls back to `"high"`, capped by `task.maxEffort`); worktree isolation; speculative launch; cold revival; completion probe & tiny-model labels; `before_subagent_spawn`; `@role` expansion; cross-instance mesh; idle forced `yield` ladder. |
| `wait` | **same** / **listed** (piped widget line count) | Oracle: 13 calls / 10 cases (12 identical, 1 details-listed). 50 Go tests (`agents`) + 14 (`jobs`). | AI-authored `live`/`authoring` job widgets (`real_model_only`); piped bash widget pipe-tap lines; owned-service wake leg; `Finished earlier:` evicted list; `useless` flag; `AgentAwaits`. |
| `todo` | **same** / **host** (`PrepareArguments` error details) | Oracle: 20 calls / 3 cases (19 identical, 1 details-listed). 20 Go tests (`tasks.todoClearDelay` + styled HUD ported). | Stop-time reminders not in oracle (disabled in `-p`); `/todo edit` `$EDITOR`; `todo.toggle` keybinding (HOST); eager forced `tool_choice`; subagent-matched HUD auto-completion; `todo.backend`. |
| `ask` | **same** (print-mode refusal) / **tests** | Oracle: 1 call (`ask-headless`, details-listed HOST). 14 Go tests. | Interactive answering not in `-p` oracle; native whole-form `ui.formDialog` (HOST); `steeringSignal`; `/tree` re-answer; desktop notifications & TTS. |
| `web_search` | **same** (offline refusal) / **tests** | Oracle: 1 call (`web-search-no-provider`, identical). 49 Go tests (18 providers, 8 scrapers via `httptest`). | Live search untested; grounded LLM providers; `zai`, keyless Exa MCP, Perplexity OAuth/cookies; browser-scraping engines; `AuthStorage`; `modelRoles.web`; ~70 long-tail scrapers & external fetchers. |
| `lsp` (mounted) | **same** (`gopls` actions) | Oracle: 7 calls / 3 cases + mounted calls (7 identical). 33 Go tests. | `lspmux` daemon; OS file watcher; deferred diagnostics & batch coalescing; format-on-write detail; foreign checkout folders; `rust-analyzer/analyzerStatus`; Claude plugin `.lsp.json`. |
| `ast_edit` (mounted) | **same** (preview + `resolve`/`reject`) | Oracle: 4 calls / 3 cases + mounted calls (4 identical). 42 Go tests. | External `ast-grep >= 0.40` (26 langs vs 57 / WASM); silent syntax-broken file skip; JSON member auto-wrap; wire `<examples>`; forced `tool_choice` escalation. |
| `debug` (mounted) | **same** (stdio DAP adapter) | Oracle: 10 calls / 2 cases + mounted calls (10 identical). 46 Go tests. | Live `dlv`/`lldb-dap` not run in oracle (macOS sandbox); Claude plugin `.dap.json`; `/debug` TUI menu; YAML anchors/block scalars. |
| `find` (`detected`) | **same** (`find.enabled: "on"`) | Oracle: 1 call (`find-semantic`, identical). 37 Go tests. | Spotlight ranker; `find.verifyModel`; System One cache & ledger; chat HTTP retry. |
| `ast_grep` (`off`, mounted) | **same** | Oracle: 2 calls (`ast-grep-and-edit-resolve-reject`, identical). 42 Go tests. | Same `ast-grep >= 0.40` CLI limits as `ast_edit`. |
| `checkpoint`, `rewind` (`off`, mounted) | **same** | Oracle: 4 calls (`checkpoint-and-rewind`, identical). 10 Go tests. | `turn_end` `context_edit` omissions instead of `branchWithSummary` tree cut; sibling batch calls omitted together. |
| `resolve`, `reject` (special) | **same** (mounted in `bash`) | Oracle: exercised via `bash` in 2 cases (all matched). 42 Go tests. | Forced `tool_choice` escalation; persistence across reload/resume/fork. |
| `yield` (hidden) | **same** (child surface & invocation) | Oracle: `task-structured-yield`. 50 Go tests. | Canonical JSON Schema key order; idle 3-step forced `yield` ladder. |
| `github` (`off`, mounted) | **tests** | 15 Go tests; **no oracle case** (needs `gh`/network). | JSON cache vs SQLite; `run_watch` artifact spill; `useless`; image resize; bash cache invalidation; CoW worktrees; host-trust guard tightened. |
| `propose`, `goal` (special/hidden) | **tests** | 21 Go tests (`internal/modes`); **no oracle case** (interactive modes). | Direct `agent_end` approve-and-execute (HOST); goal `todo_context` wiring. |
| 24 other omp tools | **absent** | — | `security_scan`, `context_notes`, `new_context`, `memory_edit`, `retain`, `recall`, `reflect`, `learn`, `manage_skill`, `think`, `report_issue`, `vibe_*` (5), `advise`, autoresearch (4), `tts`, `generate_image`. |

---

## Browser (`eval` Prelude) — Accepted (8th Review)

<!-- BROWSER-STATUS-BLOCK -->
**Status: accepted.** The eighth review pass, by a fresh reviewer with its own probes on real Chrome, returned **accept** with zero blocking findings, and all ten of its follow-up findings landed: **72** Go tests in `internal/browser` (all 72 PASS / 0 FAIL / 0 SKIP unloaded, under 24 CPU hogs, and `-race` with `OMP_LOOP_ITERS=200`, plus both click-loop tests passing at the full **2,000 iterations** each), **37** in `internal/ternbrowser`, and **15** in `internal/ternwire`.

- **Measured `allowed_domains` coverage (real Chrome 150.0.7871.24 vs omp `v19.1.1` / Puppeteer `25.3.0`, `internal/browser/UPSTREAM.md:159-168`)**:
  - **Enforced (0 hits on disallowed host)**: main-frame navigation & redirects, main-frame `pagehide` `fetch(keepalive)` / `sendBeacon`, all same-process subresources, cross-site iframes (OOPIFs) and nested OOPIFs (via recursive `Target.setAutoAttach` + per-child `Fetch.enable` before `runIfWaitingForDebugger`, fail-closed), dedicated `Worker`s (from main frame and OOPIFs), and continuously across whole-call-timeout reattaches (0 leaked requests in a 2 ms fetch storm across 3 reattaches via overlapping new-session `Fetch.enable` before old-session detach; omp leaks ~1 per reattach).
  - **NOT enforced (same as omp `v19.1.1` — measured identically on Chrome 150)**: `WebSocket` (`ws://`/`wss://`) upgrade handshakes, `ServiceWorker` fetches, `SharedWorker` fetches, `<a target="_blank">` / `window.open(...)` popups, `<script type="speculationrules">` prefetch requests, and unload-time `fetch(keepalive)` / `sendBeacon` from a cross-site iframe's `pagehide` handler (Chrome detaches the OOPIF before emitting `Fetch.requestPaused`).
- **Three port additions beyond omp for a locked macOS display (`internal/browser/UPSTREAM.md:100-120`)**:
  1. **`SystemInfo.getInfo` handshake once after `DialCDP` (`opts.Headless` only, capped `min(5s, remaining/2)`, floor `500ms`)**: blocks until Chrome's GPU/Viz service finishes initializing before creating a target (prevents the startup race where tabs get no frame sink and stall `captureScreenshot` — omp itself failed 5/5 under 24 hogs on the same probe).
  2. **`Page.bringToFront` before screenshot capture and standalone `mouseMoved`/`hover` on owned headless tabs only (`KindHeadless && headless && ownsPage`)**: restores BeginFrame ticks on older tabs when a second tab is open on a locked macOS display without touching connected, adopted, or headed windows.
  3. **`--run-all-compositor-stages-before-draw` on headless `darwin` only**: keeps headless BeginFrame pumping after macOS pauses `CVDisplayLink` ~0.5–1 s after idle without disabling GPU/WebGL.
- **What is NOT proven**:
  - The original Chrome 150 navigation wedge (~1 in 2,000–3,000 clicks) was **never reproduced** (0 wedges in ~5,700 clicks during reviews + 2 × 2,000 loop iterations at the gate), so real-Chrome wedge recovery is exercised only by fake-CDP unit tests.
  - Nothing was run on **Linux** or **Windows** (`go vet` only).
  - The performance cost of `--run-all-compositor-stages-before-draw` on an **unlocked** macOS display is unmeasured (the display was locked for every run).
  - Real-Chrome tests are opt-in (`OMP_REAL_BROWSER=1`), and running both 2,000-iteration click loops on a locked display requires a 60-minute Go test timeout (`-timeout 60m`; default 10 m fits `OMP_LOOP_ITERS=200`).
- **Not ported (`internal/browser/UPSTREAM.md:170-196`)**: `relay`, `webkit`, and `cmux` backends; managed Chromium auto-download; project-shared Chromium daemon; stealth JS patches & UA stripping; `tab.run(fn)` (Puppeteer guest runtime); post-action DOM settle wait; cross-origin OOPIF DOM/frame helpers & child-frame `observe()` merging (`allowed_domains` IS enforced in OOPIFs); background idle-close timer (runs on `turn_end` and `open`); per-tab download isolation; `trace*`/`profile*`/`record*`/`a11y`/`diffScreenshot`/`clipboard*`/`saveState`/`loadState`/`vitals`/`react*`/`webmcp*`; `@mozilla/readability` scoring; synthetic picker-input probe in `fill`; auto-reload of crashed tabs (crashed tab is closed with a reminder to re-pass `allowed_domains`).
<!-- /BROWSER-STATUS-BLOCK -->
---

## Settings

- **Schema vs runtime**: omp `v19.1.1` defines **644** keys across 42 domain modules; `internal/settings/schema.json` knows all **644** keys (`cfg://`), **101** are consumed by extension areas (counted in the appendix), and **every other key (543 of 644) is accepted, validated, and saved by `cfg://` and `config.yml`, and silently ignored at runtime.**
- **High-surprise ignored keys (by omp UI tab)**:
  - *Appearance*: `theme.dark`, `theme.light`, `colorBlindMode`, `composer.shape`, `composer.tokenRate`, `statusLine.preset`, `statusLine.separator`, `statusLine.sessionAccent`, `tui.hyperlinks`.
  - *Model & thinking*: `defaultThinkingLevel` (including `"auto"`), `providers.autoThinkingMaxEffort`, `hideThinkingBlock`, `expandThinkingBlocks`, `prewalk.*`, `modelRoles`.
  - *Context*: `compaction.thresholdPercent`, `compaction.thresholdTokens`, `compaction.midTurnEnabled`, `extendedContext`, `workspace.additionalDirectories`, `contextFiles.extra`.
  - *Files & search*: `edit.mode`, `edit.blockAutoGenerated`, `edit.enforceSeenLines`, `read.defaultLimit`, `read.lineNumbers`, `read.renderMarkdown`, `read.summarize.enabled`, `grep.enabled`, `glob.enabled`, `grep.contextBefore`/`After`, `find.verifyModel`.
  - *Shell & eval*: `shellPath`, `bash.allowCompoundCommands`, `bash.gitGuard`, `bash.direnv`, `shellMinimizer.enabled`, `eval.autoProvision`, `eval.tools.enabled`, `eval.autoBackground.enabled`.
  - *Interaction & modes*: `steeringMode`, `followUpMode`, `interruptMode`, `tui.vimMode`, `plan.*`, `goal.*`, `loop.*`, `btw.inject`, `magicKeywords.*`, `todo.eager`, `todo.backend`.
  - *Tasks & memory*: `task.speculativeLaunch`, `task.completionProbe`, `worktree.clone`, `isolation.backend`, `memory.backend`, `autolearn.enabled`, `providers.fetch`.
- **101 consumed keys by area**: `agents` (18, incl. `task.maxEffort`), `ansi` (4), `approval` (3), `ask` (1), `ast` (3), `browser` (13), `checkpoint` (1), `debug` (1), `eval` (5), `github` (4), `intent`/`wiring` (1), `lsp` (10), `search` (2), `shell` (15), `tern` (6), `todo` (5, incl. `tasks.todoClearDelay`), `toolcmd`/`wiring` (3), `urls`/`wiring` (1), `web` (12); union = **101** unique keys.

---

## Changed on Purpose

1. **Branding**: system prompt role line, welcome logo/wordmark/version, and Tern surface root (`pig.session`) name `oh-my-pig` rather than `omp`.
2. **Stock PiG terminal editor**: keeps `/` and `@` completion and clean exit working instead of using PiG's broken delegated `SetEditorComponent` (`internal/ansi/UPSTREAM.md:110`).
3. **Config isolation & project trust**: reads/writes only `<agentDir>/config.yml`, trusted `<cwd>/.pig/config.yml`, and `PIG_CONFIG_FILES`; never reads `.omp/config.yml` or PiG's `settings.json`. `lsp` and `debug` gate project configs/binaries on `ctx.IsProjectTrusted()`.
4. **`/reload` is a clean slate**: runtime setting overrides and `cfg://` grants are cleared on `session_shutdown` including `/reload` and `/resume` (omp keeps them).
5. **Two-view approval rule (never looser than omp)**: evaluates operator view and session view and takes the stricter decision (`deny > prompt > allow`); 47 `port-stricter` cases measured in the 32,599-case differential.
6. **Malformed operator config fails closed to `always-ask`**: never quarantines or overwrites the file; notifies once per session, refuses `/save`, and falls back to `always-ask` (48/48 `fail-closed` cases measured).
7. **Lexical-only `edit` block ops (9 languages)**: resolves only ts/tsx/js/json/go/py/md/rs/yaml and refuses all others conservatively (0 WRONG across 10,573 anchors).
8. **System `bash`, no pipe-stream capture or persistent daemon**: `proc://<id>/streams` is always empty, piped fallback widget counts `outputTap` lines only, and `proc://<id>/mode` rejects `persist`/`detached`.
9. **Browser over raw CDP (no Puppeteer)**: `tab.run(fn)` refuses and points to `tab.*` helpers / `tab.evaluate(...)`.
10. **No Tern split-pane `/fork` or `omp_restore` OSC 1337** (would launch real `omp` on a PiG session file) and **host-trust guard on `github` (`gh`) targets**.
11. **omp's sessions resume here (own addition)**: `--resume`, `--continue`, `--session` and `--fork` find this project's omp sessions (`~/.omp/agent/sessions`), translated into PiG's session directory before PiG chooses one; `/import-omp` does the same inside a session. A translation starts at the entries its latest compaction kept, and a snapcompact archive is carried as text, not images. omp's files are only read (`internal/ompsession/UPSTREAM.md`).

---

## Not Proven / Untested

- **Windows process-tree handling (`internal/shell/proc_windows.go`)**: each shell/service uses a Windows Job Object (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) via stdlib syscall, with immediate `TerminateJobObject` (no `SIGTERM` trap grace); **compiled (`GOOS=windows go vet`) only — never executed on Windows**.
- **Linux**: `GOOS=linux go vet` only; zero runtime tests or proof runs on Linux.
- **Real model**: never run against any real LLM provider or live model in oracle, Tern, or terminal proofs.
- **Machine load & wall-time sensitivity**: omp prints `Wall time: <N> seconds` when a bash call crosses `1000 ms`; under heavy host load (`load ~30–70` on 14 cores), fast commands intermittently cross 1 s on one side only and fail the oracle (clean gate runs were at load average under ~10 on 14 cores). `proof/oracle.py` treats narrow crossings (`1.0–1.3 s`) as `timing` (which still exits 1 unless `ORACLE_ALLOW_TIMING=1`) and larger crossings as `differs`.
- **Locked display**: every browser and drawing run occurred with the macOS display locked (`CGSSessionScreenIsLocked=Yes`); unlocked-display behaviour (including the cost of `--run-all-compositor-stages-before-draw`) is unmeasured.
- **Chrome page-wedge recovery in real Chrome**: the ~1-in-2,000–3,000 Chrome 150 navigation wedge was **never reproduced** (0 wedges in ~5,700 review clicks + 2 × 2,000 gate-loop clicks), so real-Chrome wedge recovery remains unexercised beyond fake-CDP unit tests.
- **Full differential tests requiring `$OMP_SRC` / corpus**: `TestZZOmpApprovalDifferential` (all 32,599 cases across 10 sections) and `TestZZOmpApprovalUnwrapRoute` (17 bracketed `proc://<job>/kill` shapes) in `approval_ompdiff_test.go` are **skipped** in default `go test` unless `OMP_APPROVAL_DIFF_CORPUS` is generated from `$OMP_SRC` via `proof/diff/approval/make-corpus.sh` and set (they ran with `FAIL=1` on the PiG `v0.8.0` SDK, Block 4); default `go test` runs the checked-in reduced subset (`TestOmpApprovalDifferentialReduced`, 3,873 cases, and `TestOmpApprovalUnwrapRoute`, 17 shapes). Likewise `TestOmpDifferentialFull` (20,065 cases) requires generating `corpus.jsonl` from `$OMP_SRC` via `proof/diff/intent/make-corpus.sh`, while `TestOmpDifferentialReduced` (1,201 cases) runs by default.
- **Live external services & interactive flows**: `web_search` against live engines, `github`/`issue://`/`pr://`/`mr://`, `mcp://`, interactive `ask`, interactive approvals / `cfg://` saves, RPC mode, `/resume`, `/compact`, stop-time `todo` reminders, and slash commands / modes beyond Go unit tests.

---

## Known Debt

- **Per-package fake-host test harnesses**: the port's `internal/*` packages each carry their own fake-host test harnesses rather than the porting Skill's shared template (the module-root harness was renamed `hostharness_test.go` to keep the template filename reserved for unchanged copies).
- **Accepted-and-ignored settings**: **543** of omp's **644** settings keys are accepted, validated, and saved by `cfg://` and `config.yml`, and silently ignored at runtime (only **101** are consumed by areas).
- **Filtered welcome tip lists**: `internal/ansi/chrome.go` (`welcomeTips`, 16 tips) and `piglets/pig-tern/frontend/omp_transcript.go` (`ompWelcomeTips`, 14 tips) carry hand-filtered/adapted subsets instead of loading omp's full 35-tip `tips.txt` (and do not expand omp's `{tap:...}` tokens).
---

## Appendix: How the Numbers Were Counted & Reproducibility

Set `$OMP_SRC` to the read-only export of omp tag `v19.1.1` (`31ffee513f27a01bba7d4dfbe12bae239cdc646a`) and `$EXT` to `components/oh-my-pig/extensions/oh-my-pig`.

```sh
# omp v19.1.1 built-in tools (29), default-on (15), hidden (3), special (4), mode/extra (13 = 5 vibe + 1 advise + 4 autoresearch + tts + generate_image; total 49):
python3 -B -c '
import os, pathlib, re
OMP = pathlib.Path(os.environ["OMP_SRC"])
bn = (OMP / "packages/coding-agent/src/tools/builtin-names.ts").read_text()
for name in ("BUILTIN_TOOL_NAMES", "HIDDEN_TOOL_NAMES"):
    m = re.search(rf"export const {name}\s*=\s*\[([\s\S]*?)\]", bn)
    print(name, len(re.findall(r"\"([a-z0-9_]+)\"", m.group(1))))
m = re.search(r"export const BUILTIN_TOOL_HELP[\s\S]*?\n\};", bn)
entries = re.findall(r"^\s*([a-z0-9_]+)\s*:\s*\{[^}]*defaultState:\s*\"([^\"]+)\"", m.group(0), re.M)
print("defaultState==on", len([k for k, s in entries if s == "on"]))
'

# omp v19.1.1 internal URL schemes (20):
grep -hoE 'readonly scheme = "[^"]+"' "$OMP_SRC"/packages/coding-agent/src/internal-urls/*-protocol.ts | sort -u | wc -l

# omp v19.1.1 built-in slash commands (89):
python3 -B -c '
import os, pathlib, re
sc = pathlib.Path(os.environ["OMP_SRC"]) / "packages/coding-agent/src/slash-commands"
cmds = [c for p in sorted(sc.glob("builtin-*.ts"))
        if p.name not in ("builtin-registry.ts", "builtin-completions.ts")
        for c in re.findall(r"^\t\{\n\t\tname:\s*\"([^\"]+)\"", p.read_text(), re.M)]
print(len(cmds), len(set(cmds)))
'

# omp v19.1.1 shipped agents (5): EMBEDDED_AGENT_DEFS in $OMP_SRC/packages/coding-agent/src/task/agents.ts
# -> scout, reviewer, security-reviewer, task, sonic

# omp v19.1.1 settings in schema (644) and consumed by port areas (101 unique, excluding API param "temperature"):
python3 -B -c '
import pathlib, json
EXT = pathlib.Path("components/oh-my-pig/extensions/oh-my-pig")
keys = {e["id"] for e in json.loads((EXT / "internal/settings/schema.json").read_text())}
used = set()
for p in EXT.rglob("*.go"):
    if p.name.endswith("_test.go") or str(p.relative_to(EXT)).startswith("internal/settings/"):
        continue
    txt = p.read_text()
    for k in keys:
        if f"\"{k}\"" in txt:
            used.add(k)
used.discard("temperature")
print("schema:", len(keys), "consumed:", len(used), "ignored:", len(keys) - len(used))
'

# omp v19.1.1 keybindings (79), CLI subcommands (52), CLI launch flags (72), catalog providers (98):
python3 -B -c '
import os, pathlib, re
OMP = pathlib.Path(os.environ["OMP_SRC"])
kb = set(re.findall(r"^\s*\"([a-z]+\.[a-zA-Z0-9_.]+)\"\s*:", (OMP / "packages/tui/src/keybindings.ts").read_text(), re.M))
kb |= set(re.findall(r"^\s*\"([a-z]+\.[a-zA-Z0-9_.]+)\"\s*:", (OMP / "packages/tui/src/app-keybindings.ts").read_text(), re.M))
subs = re.findall(r"^export const (\w+Help)\s*=", (OMP / "packages/coding-agent/src/cli/command-help.ts").read_text(), re.M)
ft = set(re.findall(r"^\s*\"(--[a-z0-9-]+)\"\s*:", (OMP / "packages/coding-agent/src/cli/flag-tables.ts").read_text(), re.M))
af = set(re.findall(r"arg === \"(--[a-z0-9-]+)\"", (OMP / "packages/coding-agent/src/cli/args.ts").read_text()))
provs = set(re.findall(r"\"([a-z0-9-]+)\"", (OMP / "packages/catalog/src/compat/provider-ids.ts").read_text()))
print("keybindings:", len(kb), "subcommands:", len(subs), "flags:", len(ft | af), "providers:", len(provs))
'

# Port test counts: extension Go func Test* (1,170 declarations = 1,169 tests + TestMain),
# pig-tern/frontend Go Test* (138), proof Python test_* (43):
grep -rn '^func Test' components/oh-my-pig/extensions/oh-my-pig --include='*_test.go' | wc -l
grep -rn '^func Test' piglets/pig-tern/frontend --include='*_test.go' | wc -l
grep -rnE '^\s+def test_' piglets/oh-my-pig/proof/test_oracle_harness.py | wc -l
# Hashline blockspans fixture anchors (30 files, 10,573 anchors) and interceptor differential (200,000 verdicts):
python3 -B -c '
import gzip, json, pathlib
d = pathlib.Path("components/oh-my-pig/extensions/oh-my-pig/internal/hashline/testdata/blockspans")
fs = sorted(d.glob("*.json"))
print("blockspans:", len(fs), sum(json.loads(f.read_text())["total"] for f in fs))
with gzip.open("components/oh-my-pig/extensions/oh-my-pig/internal/shell/testdata/interceptor_diff.txt.gz", "rt") as f:
    print("interceptor:", sum(1 for _ in f))
'

# Regenerate full differentials from $OMP_SRC (checked into piglets/oh-my-pig/proof/diff/{intent,approval}/):
OMP_SRC=$OMP_SRC DIFF_OUT=<out>/intent   piglets/oh-my-pig/proof/diff/intent/make-corpus.sh
PIG_SRC=$PIG_SRC DIFF_OUT=<out>/intent   piglets/oh-my-pig/proof/diff/intent/measure.sh
OMP_SRC=$OMP_SRC DIFF_OUT=<out>/approval piglets/oh-my-pig/proof/diff/approval/make-corpus.sh
PIG_SRC=$PIG_SRC DIFF_OUT=<out>/approval piglets/oh-my-pig/proof/diff/approval/measure.sh
```

### Disagreements Between Sources

- **Blockspans anchor total**: `internal/hashline/UPSTREAM.md` prose says `10,568` (Sample 3 `4,083`), while summing `total` across all 30 JSON files in `internal/hashline/testdata/blockspans/` gives **10,573** (Sample 3 `4,088`). The JSON sum (`10,573`) is used here.
- **Extension `func Test*` vs executed top-level tests**: `grep -rn '^func Test'` counts **1,170** declarations, while `go test` executes **1,169** top-level `Test*` functions; the difference of 1 is `TestMain` in `internal/ask/main_test.go`.
- **Settings consumed**: scanning non-test Go files outside `internal/settings/` for all 644 schema keys matches 102 literals; `"temperature"` is an LLM/web API field rather than a setting read and is excluded, giving **101**.
- **Welcome tip lists vs omp's `tips.txt`**: omp's `packages/tui/src/prompt/tips.txt` has **35** tips (plus 1 hard-coded unicode-preset tip); `welcomeTips` (16) has 10 lines matching omp's `tips.txt` ignoring backticks + 6 port-adapted lines, and `ompWelcomeTips` (14) has 6 lines matching omp's `tips.txt` ignoring backticks + 8 port-adapted command lines.
- **Full differential READMEs vs latest gate runs**: `proof/diff/intent/README.md` and `proof/diff/approval/README.md` embed earlier snapshots (233 host refusals; 279 approval mismatches); the run on the PiG `v0.8.0` SDK ran both full corpora with `FAIL=1` and 0 unexpected mismatches (8 lenient host refusals in `intent-diff`, 0 unexpected in `approval-diff`).
- **Pre-existing repo test failure**: `npm test` reports 489 pass, 2 skipped, 1 fail (`scripts/npm-packages.test.mjs:112` for the `ahp` package), present before `oh-my-pig` work began.
