# Extension equivalence harness

A Go extension Package that proves a Go port of a Pi extension behaves like the
TypeScript original. It is what the
[`pig-extension-porter`](../../piglets/pig-extension-porter/README.md) Piglet calls,
and it works on its own: `pig install ./components/extension-equivalence`.

It provides four model tools and one command-line program (`pigeq`, the same
code):

| Tool / command | Use |
|---|---|
| `equivalence_gaps` / `pigeq gaps` | Static: list the Pi APIs the TypeScript uses that the Go SDK lacks (`ctx.newSession({ setup })`, `pi.on("cache_warming_decision")`, ...). A missing API cannot show up in any scenario, because the Go lane cannot call it, so only a static check sees it. Rows come from PiG's `docs/extension-sdk-surface.md`; a snapshot of its non-implemented rows and its `pi.*` rows (PiG 0.4.1, Pi 1.0.1) is embedded, and `--surface` reads a fresh copy. A `pi.<member>` the table has no row for is an API that PiG predates (`pi.registerToolRenderer` before PiG 0.4.1) and is reported missing. `--ts` takes a file (read with the local modules it imports or re-exports) or a multi-file extension's directory; on a table where `pi.events` is missing for Go (PiG 0.3.x), the bus reached through an alias, destructuring or an index is reported too; `--accept-gaps` lists owner-approved exclusions. |
| `equivalence_run` mode `run` / `pigeq run` | Live: the original under **Pi**, the original under **PiG's Node runtime**, and the Go port under **PiG**, with the same scripted scenario. The port passes when its trace equals Pi's. The PiG-Node lane proves the trace is host-neutral. |
| mode `record` / `pigeq record` | Write golden traces from Pi (and refuse a scenario on which Pi and PiG's Node lane disagree). Commit them. |
| mode `check` / `pigeq check` | Compare only the Go port with the golden traces. Needs no Pi installation, so it can run wherever `pig` does. |
| `pigeq check --builtin --pig <Binary>` | Prove a built Piglet Binary against the goldens: the port is compiled in, so no extension directory is loaded with `-e`. Not available for `mutate`. |
| `equivalence_run` mode `mutate` / `pigeq mutate` | Apply deliberate defects to a copy of the port; every one must make a scenario fail (or, with the SDK directory, the port's own Go tests). `--unit-only` (tool: `unitOnly`) judges by the port's own tests alone, for a port with no scenarios; `--jobs N` runs mutants in parallel; each unit run is bounded by `-timeout=180s`; a mutant is judged only after the unmutated port's tests pass, and only a reported test failure is a kill. |
| (in `run`, `record`, `check`) | Two pseudo-scenarios: `sdk-gaps` (above) and `exec-coverage`: every command the original or the port starts by a literal name must be declared in some scenario's `commands` and must be a bare name, because an undeclared command runs unshimmed and unrecorded. A command in code no scenario reaches (a development launcher, a deferred feature, an SQL statement handed to a driver's `exec`) is excused by the owner with an `"exec:<name>": "<reason>"` entry in the `--accept-gaps` file; it stays listed with its reason, and a path is never excused. |
| `equivalence_diff` / `pigeq diff` | First difference between two traces. |
| `equivalence_twins` / `pigeq twins list\|check` | The twin contract: `list` writes the ledger of upstream test titles per file; `check` requires each title to have a Go `tw(...)` twin or a named `tskip(...)` (for a Go original: a test function of the same name, or one that skips first, directly or through a skip helper, as a named skip), rejects unknown titles and one reason shared by many skips, and prints the `N exact twins, M skipped` line for `PORT.md`. Helpers: the Skill's `references/twin_test.go.txt`. |
| `pigeq llm --script turns.json [--log F]` | Serves the scenarios' scripted OpenAI-compatible model on a loopback port (prints its base URL; stops when stdin closes) and logs each request as a JSON line. For a port's own end-to-end run, for example an adapter driven by a real client. |
| `pigeq env`, `pigeq source` | Session setup (CLI only). `env --root DIR --pig PIG --pi PI --source DIR --module EXTDIR` prints an isolated shell environment to source (temporary `HOME`, `PIG_HOME`, agent directories; the real `go` and `node` first on `PATH`; `GOCACHE` and `GOMODCACHE` kept; `PIG_SDK_GO_ROOT` unset) and, with `--pig` and `--module`, writes a `go.work` that resolves PiG's SDK. `source --from PIG-REPO --rev COMMIT --out DIR` exports a PiG revision as a one-commit git checkout for `PIG_SOURCE_ROOT`. |

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
- `servers`, `env`: fake HTTP upstreams for an extension that calls an API (a search provider, a judge
  model, a remote agent). Each server has routes (method, exact or prefix path, required query
  parameters, status, headers, `body` or `json`, `delayMs`, `times`, `drop`) and gets its own loopback
  port per lane. `{{server:NAME}}` in `env`, `files` and `args` is its base URL, so provider base URLs
  and fake credentials can point at it. Every request it receives enters the trace (`http`: method,
  path, query, body decoded, content type and the headers listed in `recordHeaders`; user agents and
  other runtime-specific headers stay out), and a URL an extension echoes back is normalized to
  `<server:NAME>`. Verified live: a TypeScript extension under Pi and its Go port under PiG send the
  same request and produce identical traces, and a port that sends another header fails at the request.
  Parallel requests arrive in a nondeterministic order. A port whose SSRF guard refuses loopback needs the
  extension's own allow-list for the fake server's range, written with `agentFiles` (pigpen-websearch: `ssrf.allowRanges` in its `web-search.json`).
- `env` alone (no servers) switches an opt-in extension on: `"env": {"PIGPEN_WARDEN_ENABLED": "1"}` reaches the host process of every lane, the original under Pi and the port under PiG alike, on top of the hermetic environment (the harness's own variables and the test process's environment do not leak in). `PATH`, `HOME`, `TMPDIR`, `LANG`, `TERM`, `NO_COLOR`, the Go toolchain variables (`GOROOT`, `GOCACHE`, `GOMODCACHE`, `GOPATH`, `GOFLAGS`, `GOPROXY`, `GONOSUMDB`, `GONOSUMCHECK`, `GOSUMDB`, `GOTOOLCHAIN`), `GOWORK`, `GOENV` and every `EQ_*`, `PIG_*`, `PI_*` and `GIT_*` name are the harness's and are rejected. Tested with a fake host that records its environment (`TestScenarioEnvReachesEveryLaneAndKeepsTheHostHermetic`).
- `steps`: RPC commands, and the answers to the dialogs they raise. Session operations
  that only an extension command context can start (`new session`, `fork`) go through a
  small driver extension loaded in every lane (`/eq-new`, `/eq-fork`).

Recorded per step, in arrival order: UI requests (dialogs, notifications, status,
widgets), exec calls and exits (via PATH shims), requests that reached the model (the
messages, every tool's full definition, and the extension's part of the system prompt:
its difference from the same host's baseline prompt without the extension), tool
executions and results, agent and turn lifecycle, `extension_error`, command responses,
and the host exit. After the last step a final quiet period (`tailMs`, default 1000 ms)
records late effects instead of losing them at shutdown. The normalizer removes only values that differ on every run
(paths, UUIDs, timestamps, host session ids, the bash tool's measured wall time); its rules N1 to N6 are listed in
[normalize.go](extensions/extension-equivalence/eq/normalize.go) and versioned, and a
golden trace recorded under another version is refused.

## Limits

- The trace holds what an extension can influence through RPC. Terminal cells and
  TUI-only behavior (custom components, overlays) are not compared.
- The `hasUI == false` branch cannot be reached through RPC. Test it with the fake host
  described in the Skill (`references/fakehost_test.go.txt`).
- Effects the extension starts without awaiting are collected during a quiet period
  (`settleMs`, default 150 ms, plus `tailMs` after the last step). An effect later than
  that is not seen. Parallel exec calls arrive in a nondeterministic order.
- Only declared commands are recorded. `exec-coverage` catches literal command names;
  a computed name (`pi.exec(tool, ...)`) must be declared by hand and reviewed.
- The host's own system prompt wording is not compared, only the extension's difference
  from it. An extension that removes host text shows the removed text, which may differ
  between Pi and PiG (a host-check failure to record, not to normalize).
- The extension's stderr and log output are not in the trace.
- Linux and macOS. The shims are POSIX symlinks; Windows is covered by the Skill's
  Windows notes and `go vet`, not by this harness.
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
