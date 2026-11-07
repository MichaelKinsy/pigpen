# Extension equivalence harness

A Go extension Package that proves a Go port of a Pi extension behaves like the
TypeScript original. It is what the
[`pig-extension-porter`](../../piglets/pig-extension-porter/README.md) Piglet calls,
and it works on its own: `pig install ./components/extension-equivalence`.

It provides four model tools and one command-line program (`pigeq`, the same
code):

| Tool / command | Use |
|---|---|
| `equivalence_gaps` / `pigeq gaps` | Static: list the Pi APIs the TypeScript uses that the Go SDK lacks (`ctx.newSession({ setup })`, `ctx.ui.custom({ onHandle })`, ...). A missing API cannot show up in any scenario, because the Go lane cannot call it, so only a static check sees it. Rows come from PiG's `docs/extension-sdk-surface.md`; a snapshot of its non-implemented rows and its `pi.*` rows (PiG 0.5.0, Pi 1.3.0+1402973a; `eq/surface/go-gaps.md` names the revision and the one-line command that regenerates it) is embedded, and `--surface` reads a fresh copy. A `pi.<member>` the table has no row for is an API that PiG predates (`pi.registerToolRenderer` before PiG 0.4.1) and is reported missing. `--ts` takes a file (read with the local modules it imports or re-exports) or a multi-file extension's directory; on a table where `pi.events` is missing for Go (PiG 0.3.x), the bus reached through an alias, destructuring or an index is reported too; `--accept-gaps` lists owner-approved exclusions. |
| `equivalence_run` mode `run` / `pigeq run` | Live: the original under **Pi**, the original under **PiG's Node runtime**, and the Go port under **PiG**, with the same scripted scenario. The port passes when its trace equals Pi's. The PiG-Node lane proves the trace is host-neutral. |
| mode `record` / `pigeq record` | Write golden traces from Pi (and refuse a scenario on which Pi and PiG's Node lane disagree). Commit them. |
| mode `check` / `pigeq check` | Compare only the Go port with the golden traces. Needs no Pi installation, so it can run wherever `pig` does. |
| `pigeq check --builtin --pig <Binary>` / `equivalence_run` mode `check` with `builtin` | Prove a built Piglet Binary against the goldens: the port is compiled in, so no extension directory is loaded with `-e`. `record --self --builtin` / mode `record` with `self` and `builtin` records its baseline the same way. Not available for `mutate` (mutate the source directory) or `run`. |
| `equivalence_run` mode `mutate` / `pigeq mutate` | Apply deliberate defects to a copy of the port; every one must make a scenario fail (or, with the SDK directory, the port's own Go tests). `--unit` (tool: `unit`) also runs the port's own tests on each mutant first (needs `--sdk-dir`, tool `sdkDir`; the tool, as before `unit` existed, also turns the layer on when `sdkDir` or `$PIG_SDK_DIR` is given and `unit` is not `false`); `--unit-only` (tool: `unitOnly`) judges by the port's own tests alone, for a port with no scenarios; `--jobs N` runs mutants in parallel; each unit run is bounded by `-timeout=180s`; a mutant is judged only after the unmutated port's tests pass, and only a reported test failure is a kill. |
| (in `run`, `record`, `check`) | Two pseudo-scenarios: `sdk-gaps` (above) and `exec-coverage`: every command the original or the port starts by a literal name must be declared in some scenario's `commands` and must be a bare name, because an undeclared command runs unshimmed and unrecorded. A command in code no scenario reaches (a development launcher, a deferred feature, an SQL statement handed to a driver's `exec`) is excused by the owner with an `"exec:<name>": "<reason>"` entry in the `--accept-gaps` file; it stays listed with its reason, and a path is never excused. |
| `equivalence_diff` / `pigeq diff` | First difference between two traces. |
| `equivalence_twins` / `pigeq twins list\|check` | The twin contract: `list` writes the ledger of upstream test titles per file; `check` requires each title to have a Go `tw(...)` twin or a named `tskip(...)` (for a Go original: a test function of the same name, or one that skips first, directly or through a skip helper, as a named skip), rejects unknown titles and one reason shared by many skips, and prints the `N exact twins, M skipped` line for `PORT.md`. Helpers: the Skill's `references/twin_test.go.txt`. |
| `pigeq lane --scenarios DIR --host pi\|pig --bin EXE --ext PATH --out DIR` / `equivalence_run` mode `lane` (`host`, `bin`, `ext`, `out`) | Run one host on the scenarios and write its traces, comparing nothing: for debugging a divergence. A lane that fails is reported as a failed lane, with its trace. |
| `--keep` / `keep` (on `run`, `record`, `check`, `lane`) | Leave each lane's run directory in place (workspace, home, agent directory, host logs). The CLI prints `eq: keeping DIR` on stderr; the tool puts the same line in its report. |
| `pigeq llm --script turns.json [--log F]` | Serves the scenarios' scripted OpenAI-compatible model on a loopback port (prints its base URL; stops when stdin closes) and logs each request as a JSON line. For a port's own end-to-end run, for example an adapter driven by a real client. CLI only, on purpose: it is a long-lived server whose lifetime is its stdin, which a tool call has no way to hold open. |
| `pigeq env`, `pigeq source` | Session setup (CLI only, on purpose: `env` prints a shell script that the calling shell must `source` to get its isolated `HOME`, `PATH` and `go.work`, which a tool call cannot do for a model; `source` exports a PiG revision to a directory for `PIG_SOURCE_ROOT`, a one-time checkout step). `env --root DIR --pig PIG --pi PI [--pi-source DIR] [--sdk-dir DIR] --source DIR --module EXTDIR` prints an isolated shell environment to source (temporary `HOME`, `PIG_HOME`, agent directories; the real `go` and `node` first on `PATH`; `GOCACHE` and `GOMODCACHE` kept; `PIG_SDK_GO_ROOT` unset) and, with `--pig` and `--module`, writes a `go.work` that resolves PiG's SDK. `source --from PIG-REPO --rev COMMIT --out DIR` exports a PiG revision as a one-commit git checkout for `PIG_SOURCE_ROOT`. | A PiG that pins an untagged Pi commit reports `0.5.0+1.3.0.1402973a`; `env` then accepts a Pi that prints `1.3.0` and whose source checkout (the git tree above the `pi` executable, or `--pi-source`) is at that commit, and rejects any other commit.

**Go source** is scanned too: `pigeq gaps --go DIR` (tool: `equivalence_gaps` with `go`) reports
`MISSING` for an import of a PiG package other than the public SDK and for the process-global
calls PiG's fuse check rejects (`os.Exit`, `os.Chdir`, `os.Stdout`, `log.Fatal*`, `fmt.Print*`), and
`PARTIAL` for SDK stand-ins the surface table records. `run` and `check` repeat it on the port
as the `port-gaps` pseudo-scenario. Test files and `package main` files are not scanned.

**Oracles.** `record` takes exactly one of `--ts` (a TypeScript original, run under Pi: the
proof of equivalence), `--go-oracle DIR` (an original that is already a Go extension, run
under PiG) or `--self` (no original exists: the port is recorded as its own regression
baseline). The golden trace's header names the kind and `check` labels a self-recorded trace
as a baseline, not equivalence. `run` needs an original.

**What the header's `extension` hash covers.** For a TypeScript original it is the SHA-256 of the entry file's bytes. An
original split over several files (an `index.ts` that imports `ui.ts` and `src/**`) is not identified by its entry alone:
`record --ts-closure` (tool: `tsClosure`) hashes the entry, every local module it reaches and the `package.json` beside
it, in path order, and the header gains `"extensionFiles": "closure"` (`eq.TSClosureHash`). It is opt-in so that no
golden recorded before it existed changes; the header is not part of any comparison, so recording with it never changes a
trace. `scripts/port-equivalence.test.mjs` finds the recorded entry of such a header from `port/oracle/package.json`
(`pi.extensions`).

## Go originals (relocations)

A Go extension moved from another repository has no Pi oracle. Pass `--go-oracle <dir>` (the
unmodified original, named with its exact package directory when it lives inside a larger
module) instead of `--ts` and `--pi`:

```sh
pigeq record --scenarios port/scenarios --golden port/golden --go-oracle <original-dir> --pig $PIGEQ_PIG
pigeq check  --scenarios port/scenarios --golden port/golden --go extensions/<name> --pig $PIGEQ_PIG
pigeq run    --scenarios port/scenarios --go-oracle <original-dir> --go extensions/<name> --pig $PIGEQ_PIG
```

To get a runnable original from a monorepo whose module replaces the SDK by a relative path,
export it and point that path at the staged SDK:

```sh
git -C <repo> archive <full-commit> piglets/standard | tar -x -C X
mkdir -p X/extensions && ln -s "$(pig reload --sdk-path | tail -1)" X/extensions/sdk
# --go-oracle X/piglets/standard/extensions/<name>
```

The original runs under PiG as the lane `pig-go-upstream` and is what the relocated code must
equal; there is no SDK gap scan and no host check (both lanes are the same host). Mark the
port with `port/relocation.json` so the repository's layout test expects that lane name.
`ui.custom` overlays and other terminal-only effects are still invisible to the trace.

## How a scenario runs

Each lane starts a real host in RPC mode (`pi --mode rpc` or `pig --mode rpc`) in a
scratch workspace with a hermetic environment (no inherited HOME, credentials or
configuration). The scenario is data (see [scenarios](extensions/extension-equivalence/eq/scenario.go)):

- `files`, `setup`: the workspace, including a git repository;
- `commands`: child processes the extension may start, each `real` (run the real tool
  and record the call), `canned` (fixed stdout, stderr, exit) or `missing` (not on PATH);
- `llm`: scripted model turns served by a local OpenAI-compatible server (so tool calls,
  `tool_call`/`tool_result`/`context` events and turn order are exercised without a model);
- `agentFiles`: files written under the lane's agent directory before the host starts (an extension's user-level config; `{{server:NAME}}` expanded).
- `homeFiles`: files written under the lane's host home before the host starts, for a port whose own configuration lives outside the agent directory: `<PIG_HOME>` on a pig lane, `<HOME>/.pi` on a pi lane (`{{server:NAME}}` expanded). Paths are relative and must stay inside that home: an absolute path, `..`, `.` or an empty path is a load error (and `writeFilesUnder` refuses them again at write time). The original under Pi and the port under PiG are each given the file in their own home from one entry.
- `servers`, `env`: fake HTTP upstreams for an extension that calls an API (a search provider, a judge
  model, a remote agent). Each server has routes (method, exact or prefix path, required query
  parameters, status, headers, `body` or `json`, `delayMs`, `times`, `drop`) and gets its own loopback
  port per lane. `{{server:NAME}}` in `env`, `files`, `agentFiles`, `args` and the `llm` script is its base URL, so provider base URLs
  and fake credentials can point at it, and the model's own tool call can name it (see below). Every request it receives enters the trace (`http`: method,
  path, query, body decoded, content type and the headers listed in `recordHeaders`; user agents and
  other runtime-specific headers stay out), and a URL an extension echoes back is normalized to
  `<server:NAME>`. Verified live: a TypeScript extension under Pi and its Go port under PiG send the
  same request and produce identical traces, and a port that sends another header fails at the request.
  Parallel requests arrive in a nondeterministic order. A port whose SSRF guard refuses loopback needs the
  extension's own allow-list for the fake server's range, written with `agentFiles` (pigpen-websearch: `ssrf.allowRanges` in its `web-search.json`).
- `llm` text and tool-call arguments may contain `{{server:NAME}}`: it is expanded in the text of a turn and in every string value of a tool call's `arguments`, at any depth (objects and arrays included; keys are not expanded), so a scripted `fetch_content` can ask for `{{server:web}}/page`. The harness expands a copy per lane (each lane's fake server has its own port); the scenario file is not changed. A name the scenario does not declare is a load error (`llm turn 2 tool call 1 (fetch_content) arguments refers to server "nope", which the scenario does not declare`), like in `env` and `files`. A URL that later turns carry back (the model request's messages, a tool's echo) is normalized to `<server:NAME>` as before.
- `normalize`: extra normalization rules for values the extension invents on every run (rule N7 below): `"normalize": [{"pattern": "[a-z0-9]{8,12}-[a-z0-9]{6}", "replacement": "<response-id>"}]`. `pattern` is a Go (RE2) regular expression, validated when the scenario loads (an empty or non-compiling pattern is a load error that names the rule); `replacement` may use `$1` and `${name}` for capture groups and `$$` for a dollar sign. Rules run in list order on every string (object keys included) of every event of both lanes, after the built-in N1 to N3. A rule that matches nothing is not an error. The rules belong to the scenario, so the golden trace was normalized by exactly the rules the port is checked with; a scenario edit that changes a rule needs `record` again.
- `env` alone (no servers) switches an opt-in extension on: `"env": {"PIGPEN_WARDEN_ENABLED": "1"}` reaches the host process of every lane, the original under Pi and the port under PiG alike, on top of the hermetic environment (the harness's own variables and the test process's environment do not leak in). `PATH`, `HOME`, `TMPDIR`, `LANG`, `TERM`, `NO_COLOR`, the Go toolchain variables (`GOROOT`, `GOCACHE`, `GOMODCACHE`, `GOPATH`, `GOFLAGS`, `GOPROXY`, `GONOSUMDB`, `GONOSUMCHECK`, `GOSUMDB`, `GOTOOLCHAIN`), `GOWORK`, `GOENV` and every `EQ_*`, `PIG_*`, `PI_*` and `GIT_*` name are the harness's and are rejected. Tested with a fake host that records its environment (`TestScenarioEnvReachesEveryLaneAndKeepsTheHostHermetic`).
- `steps`: RPC commands, and the answers to the dialogs they raise. Session operations
  that only an extension command context can start go through a small driver extension loaded in every lane:
  `/eq-new`, `/eq-fork [ENTRY_ID [at]]` (default: the first user message), `/eq-tree ENTRY_ID` (navigate the session
  tree without a summary) and `/eq-reload`. Every other session operation is a plain RPC command (`compact`,
  `abort`, `steer`, `follow_up`, `new_session`, `switch_session`, `fork`, `clone`, `get_entries`, `get_tree`, ...).

### Reproducing a session suite (real-Pi test files)

The fields below exist so a scenario can drive the same host behavior a `createAgentSession` + `fauxProvider` test
drives: model limits and usage, overflow errors, queued input, abort, resume and compaction, real session files, and the
files an extension leaves behind. Every one is opt-in; a scenario without them runs and records exactly as before. They
are covered by `eq/features_test.go` and `eq/features_host_test.go` (a fake host that speaks just enough RPC).

- `model`: `{"id", "contextWindow", "maxTokens", "reasoning", "input": ["text","image"]}` for the scripted model
  (default `eq-1`, 100000, 4096, text). `models`: further models of the same provider (`set_model`, per-model
  settings); the llm trace then records each request's model id. Needs an `llm` script.
- `llm` turn fields: `usage` (`input`, `output`, `cacheRead`, `cacheWrite`, optional `totalTokens`, which must equal their
  sum because the host derives it) is what the OpenAI-compatible usage chunk reports (`prompt_tokens` = input + cacheRead
  + cacheWrite, `completion_tokens` = output, `prompt_tokens_details.cached_tokens` / `cache_write_tokens`); a turn with
  `"usage": {}` reports zero (Pi then treats usage after a compaction as unknown) and a turn without the field reports
  1 and 1 as before. `finishReason` (`length`, ...) replaces the last chunk's `finish_reason`; `thinking` streams
  `reasoning_content`; `error` answers with an HTTP error instead of a stream: `{"status": 400, "message": "prompt is too
  long: 300000 tokens > 100000 maximum"}` (body `{"error":{"message":...,"type":"invalid_request_error"}}`, which Pi
  classifies as a context overflow and words as `400: {...}`) or `{"status": 529, "body": "<raw>", "headers": {...}}`;
  `delayMs` waits before answering; `hold` keeps the turn unanswered until a trigger releases that name or the host
  cancels the request (an abort). `{{var:NAME}}` in a turn's text or tool-call arguments is expanded when the turn is
  served. Images in a tool result appear in the llm trace as `images: [{mimeType, bytes, sha256}]` on the message (only
  when a request has any), with the first 16 hex digits of the SHA-256 of the decoded bytes.
- `settings`: the lane's agent-directory `settings.json` (`{"compaction": {"enabled": true, "reserveTokens": 16384,
  "keepRecentTokens": 20000}, "retry": {"enabled": false}}`); do not also write it through `agentFiles`.
- `session`: `{}` runs the host with `--session-dir <run>/sessions` instead of `--no-session`; `session.files` seeds
  session files there. `{{sessions}}`, `{{cwd}}` and `{{agent}}` in a step's rpc strings are the lane's directories.
  Rule N9 (only with `session`): a session file name's timestamp (`2026-11-06T10-20-30-123Z`) becomes `<time>`.
- `extensions`: helper extensions written into the run and loaded with `-e` in every lane, next to the extension under
  test (a tool that returns a huge result, a `turn_end` hook): `[{"name": "dump.ts", "source": "export default
  function (pi) {...}", "position": "before"}]` (`before` or the default `after` the extension under test).
- `steps[].save`: `{"owner": "data.entries.[message.role=user].id"}` stores values of the command's response for
  later steps (`{{var:owner}}` in any rpc string, and in llm turns). A path is dotted keys; an integer indexes an array
  (negative from the end); `[key=value]` selects the first array element whose (dotted) key equals the value,
  `[key=value]!` the last. `steps[].quiet` keeps that response out of the trace (use it for `get_entries`, whose entries
  carry the host's system messages); a failed response still fails the step.
- `steps[].triggers`: act while the step runs, once each, and must fire by the end of the step:
  `{"when": {"event": "agent_start", "match": {"toolName": "dump"}, "nth": 2}` or `{"when": {"llmRequest": 2}}`,
  `"rpc": {"type": "steer", "message": "..."}`, `"release": ["name"]}`. The command's response is awaited before
  the held turns are released, so queued input reaches the host before the model's answer does.
  `llmRequest` fires after the nth model request of the run has been recorded.
  A trigger's command can `save` values of its response like a step (`"save": {"toolId": "data.entries.[message.toolName=dump].id"}`,
  `"quiet": true` keeps the response out of the trace): the saved value is set before the `release`, and a turn is
  expanded (`{{var:toolId}}` in its text or tool-call arguments) only after its hold ends, so a held turn can quote the
  id of an entry the run itself has just created.
- Host events: any name in `capture` is recorded. The session events (`compaction_start`, `compaction_end`,
  `auto_retry_start`, `auto_retry_end`, `queue_update`, `session_info_changed`, `agent_settled`, `entry_appended`,
  `thinking_level_changed`, `summarization_retry_*`) are recorded whole (all keys but `timestamp`, `sessionId`,
  `sessionFile`, `sessionPath`; session entry ids become `<id:N>` in order of first appearance); `project.full` adds
  event types to record whole. `project.messageFields` keeps further keys of a `message_end`/`message_start` message
  (`errorMessage`, `summary`, `usage`, ...); `project.images` records an image block's `mimeType`, `bytes` and
  `sha256`; `project.drop` removes key paths from whole events (`**.estimatedTokensAfter`: a host-computed value); `project.skipSystem` leaves out the `message_start`/`message_end` events of system messages (the host decides when it records its own prompt snapshot: Pi appends one when a tree navigation lands on a branch that has none, PiG does not; the extension's part of the prompt is still compared in each request's system delta).
  The responses of `get_entries`, `get_tree` and `get_fork_messages` have their entry ids relabeled the same way.
  Compaction summaries, custom messages and the retained-tail sentinel are visible to the model as `user` messages in the
  llm trace (Pi converts them), so they are compared as text.
- `systemDiff`: `"hunks"` reports the extension's part of the system prompt as each changed run of lines (an extension
  that lists its tools in the host's tools section and appends guidance at the end otherwise drags the host's own wording
  between the two changes into the comparison, which differs between Pi and PiG by design); the default `"span"` is the
  original single delta; `"inserted"` is `"hunks"` reduced to the inserted lines (a request whose base text is host-owned and
  worded differently by Pi and PiG, such as the host's own summarization request, differs only in the removed host wording).
- `captureFiles` (scenario: after the host has exited, recorded under step `99-files`; step: after that step has
  settled): `[".pi/notes/**", "agent:settings.json", {"root": "sessions", "glob": "**/*.jsonl", "modes": true,
  "exclude": ["**/skip.jsonl"], "drop": ["**.sections.docs"]}]`. A string is `[root:]glob`, root one of `workspace`
  (default), `agent`, `sessions`, `home` (the host's own home); `**` crosses directories; `.git` and `node_modules`
  are skipped unless the glob names them. One `files` event per entry lists the matching regular files and symlinks
  in path order as `{"path", "text"}`, `{"path", "jsonl": [...]}` for `*.jsonl` (one decoded object per line, sorted
  keys), `{"path", "symlink"}`, or `{"path", "size", "sha256"}` for binary and over-4-MiB files; `modes` adds
  `"mode": "0600"`. Rule N8: paths and strings get N1 to N3 and N7 plus the session-name timestamp; in a `*.jsonl`
  file a numeric `timestamp` or `durationMs` key is dropped, `drop` removes more key paths (dotted names from the
  line's root, `*`, leading `**`; for host wording that differs by design, never for what the extension writes), and
  session entry ids become `<id:N>`.
- Large inputs: `{{repeat:C:N}}` anywhere in a scenario file (expanded when the file is loaded, before JSON decoding; C is one
  letter, digit, space, `_`, `.`, `-` or `#`, N at most 16,000,000) writes C N times, so a 1.2 MB seed or prompt does not
  live in the repository. `"collapseRuns": 512` (at least 64) records every run of that many identical characters in a trace
  string (llm messages, events, captured files) as `<run C xN>`, keeping the character and the exact length, so goldens stay
  small without hiding a single character of extension text.
- `{{cwd}}`, `{{agent}}` and `{{sessions}}` are also expanded in `session.files`, workspace `files` and `setup` arguments (a seeded
  session file's header names the lane's workspace).
- `commands`: `rg` and `git` need nothing special: declare `{"rg": {"mode": "real"}}` and the shim records each call
  (arguments with the run's paths normalized) while running the real binary, which must be on the harness's PATH.

Recorded per step, in arrival order (a request to the model or to a fake upstream server is recorded after the host
output that precedes it: the harness holds the request and round-trips `get_state` on the host's stdout first, so a fast
host cannot make the order depend on which harness goroutine ran first): UI requests (dialogs, notifications, status,
widgets), exec calls and exits (via PATH shims), requests that reached the model (the
messages, every tool's full definition, and the extension's part of the system prompt:
its difference from the same host's baseline prompt without the extension), tool
executions and results, agent and turn lifecycle, `extension_error`, command responses,
and the host exit. After the last step a final quiet period (`tailMs`, default 1000 ms)
records late effects instead of losing them at shutdown. Every lane runs in a scratch directory of one fixed length (`<tmp>/eq` and eight hex digits), so an extension that
quotes a path and counts its characters gets the same number in every lane. The loopback address of each fake server and of
the scripted model server (a `set_model` response echoes the model's base URL) is normalized to `<server:NAME>` and `<llm>`.
The normalizer removes only values that differ on every run
(paths, UUIDs, timestamps, host session ids, the bash tool's measured wall time); its rules N1 to N6 are listed in
[normalize.go](extensions/extension-equivalence/eq/normalize.go) and versioned, and a
golden trace recorded under another version is refused. Rule N7 is the scenario's own `normalize` list
(above): the one place where a scenario author, not the harness, decides that a value differs on every run.
It is a reviewed hole in the comparison, so write the narrowest pattern that matches only the invented
value (an id's exact shape, not `.*`), keep the extension's own text out of it, and read the recorded
trace once to see the placeholder where the value was. N7 changes no trace of a scenario without the
field, so it does not change the normalizer version.

Two more rules apply only to the scenario fields that need them, so no trace recorded before they existed changes
and the normalizer version stays `v3`: N8 (captured files, above) and N9 (a scenario with `session`: the timestamp in
session file names, wherever it appears in a trace, becomes `<time>`). The session entry ids that a trace carries
(`<id:N>`) are the one run-dependent value of the session channels that the harness relabels itself, in the keys
`id`, `parentId`, `firstKeptEntryId`, `targetId`, `leafId`, `entryId`, `fromId` and `since` when the value has the shape of
an entry id (eight lowercase hex digits). An id an extension quotes inside its own text (a recovery record that
names an entry) is the scenario's N7 rule to normalize.

## Limits

- The trace holds what an extension can influence through RPC. Terminal cells and
  TUI-only behavior (custom components, overlays) are not compared.
- The `hasUI == false` branch cannot be reached through RPC. Test it with the fake host
  described in the Skill (`references/fakehost_test.go.txt`).
- Effects the extension starts without awaiting are collected during a quiet period
  (`settleMs`, default 150 ms, plus `tailMs` after the last step). An effect later than
  that is not seen. Parallel exec calls arrive in a nondeterministic order. An exec call is
  not ordered against the host's output either: its shim cannot hold the call for a round trip
  (a host blocked in a synchronous spawn could not answer it).
- Only declared commands are recorded. `exec-coverage` catches literal command names;
  a computed name (`pi.exec(tool, ...)`) must be declared by hand and reviewed.
- The host's own system prompt wording is not compared, only the extension's difference
  from it. An extension that removes host text shows the removed text, which may differ
  between Pi and PiG (a host-check failure to record, not to normalize).
- The extension's stderr and log output are not in the trace.
- Linux and macOS. The shims are POSIX symlinks and the tests' fake hosts are shell scripts, so
  a lane does not run on Windows: the extension and `pigeq` build for it (`GOOS=windows go vet`)
  and the tests that need a POSIX shell skip there by name, but no run on Windows has been done.
  Port authors on Windows run the lanes under WSL or Linux CI and cover Windows with the Skill's
  Windows notes. Whether to add Windows lanes is an owner decision (recommendation: no, the
  recorded traces check on any host that can run `pig`, so only recording needs Linux or macOS).
- The harness needs `go` (it builds its shim and PiG builds Go extensions on first use)
  and `node` (TypeScript lanes).

## Use from a checkout

```sh
PIG_BIN=/path/to/pig npm run build:pigeq              # dist/bin/pigeq (a go.work resolves PiG's SDK)
dist/bin/pigeq env --root /tmp/porting --pig /path/to/pig --pi /path/to/pi > /tmp/porting/env.sh && . /tmp/porting/env.sh
pigeq run --scenarios <port>/port/scenarios --ts <original>.ts --go <port-dir>
```

MIT. The harness is original work; PiG's SDK surface table is quoted as data (rows
listed with their revision in `eq/surface/go-gaps.md`).
