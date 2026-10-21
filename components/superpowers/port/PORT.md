# Port record: obra/superpowers (Skills)

| Input | Identity |
|---|---|
| Original | `obra/superpowers` v7.0.0 by Jesse Vincent, https://github.com/obra/superpowers, commit `bb92a77741419a4ab5f06e711a283343f1ada0c3`, MIT |
| Kind | a Skills Package: Markdown Skills, plus one Go extension (the bootstrap, ported from the original's Pi extension: see [the extension](#the-pi-extension-extensionssuperpowers)) |
| Original kept at | `port/oracle` (the unmodified `skills/` tree and `LICENSE`) |
| Target | PiG 0.4.1 |

## What ships

The fifteen Skills of the original, built from `port/oracle/skills` by `port/adapt.py`. The only changes are the ones PiG's
Skill format and Pigpen's Skill gate require, listed in [ADAPTATIONS.md](ADAPTATIONS.md): the `pigpen-superpowers-` prefix
on each Skill's `name` and directory, and the 41 references of the form `superpowers:<skill>` rewritten to the new names.
Every other byte is the original's.

## What does not ship

- The original's hooks (`hooks/`) and the other harness manifests. The original's Pi extension ships as a Go port.

## What ships but needs Node

Two Node helper scripts ship as the original has them, inside their Skills, and run only when the model starts them:
the visual companion server of `brainstorming` (`scripts/server.cjs`, with its browser-side `helper.js`) and the
flowchart renderer of `writing-skills` (`render-graphs.js`). On a machine without Node they cannot run.

## Checks

There is no behavior to run against the original, so there are no scenarios or mutations: the check is that the shipped tree
equals the original except for the recorded changes. `node scripts/skills-drift.mjs` (part of `npm run quality`) rebuilds
every file of `skills/` from `port/oracle/skills` with the rules of `port/adapt.py` and reports a missing, extra or differing
file, a changed executable bit, and a stale `port/ADAPTATIONS.md` or `CREDITS.md`; `python3 port/adapt.py` regenerates the
tree and the table. `node scripts/skills-drift.mjs --upstream superpowers=<git checkout of obra/superpowers>` also compares
`port/oracle` byte for byte with the pinned commit. `npm run check` (the Skill gate: names, directories, licenses,
provenance) and `pig package validate components/superpowers` complete the set.

## The Pi extension (`extensions/superpowers`)

| Input | Identity |
|---|---|
| Original | `.pi/extensions/superpowers.ts` of `obra/superpowers` v7.0.0 by Jesse Vincent, https://github.com/obra/superpowers, commit `bb92a77741419a4ab5f06e711a283343f1ada0c3`, MIT; byte for byte the file of v6.4.2 (`8ca22dba`), which is where the vendored copy was taken from (`git diff v6.4.2 v7.0.0 -- .pi tests/pi` is empty) |
| Kind | 1: a Pi TypeScript extension (one file, no dependency beyond `node:` modules) |
| Oracle | the original, byte for byte in `port/oracle/.pi/extensions/superpowers.ts`, run under Pi 1.3.0 (`pi 1.3.0.1402973a`, the Pi PiG 0.5.0 follows), Node 24.19.0, over the tree `port/oracle-layout.sh` builds (below) |
| Target | PiG 0.5.0 (`0.5.0+1.3.0.1402973a`), go1.27.2, SDK `github.com/MichaelKinsy/PiG/extensions/sdk` (the version-matched staged SDK) |
| Original's tests | `tests/pi/test-pi-extension.mjs` (vendored in `port/oracle/tests/pi/`): 6 cases, **4 exact twins, 2 named skips** |

### What the extension does

It registers five handlers and nothing else (no tool, command, flag or UI):

| Event | Original | Port |
|---|---|---|
| `resources_discover` | returns `{ skillPaths: [<package root>/skills] }` | returns `{ skillPaths: [<dir>] }`, `<dir>` being the directory the host loaded the Package's `using-superpowers` Skill from (F1) |
| `session_start`, `session_compact` | `injectBootstrap = true` | the same flag, an `atomic.Bool` per loaded extension |
| `agent_end` | `injectBootstrap = false` | the same |
| `context` | while the flag is set and no message already holds the marker `superpowers:using-superpowers bootstrap for pi`, returns the messages with one user message (the bootstrap) inserted after the leading `compactionSummary` messages | the same |

The bootstrap is `<EXTREMELY_IMPORTANT>`, the marker line, a fixed introduction, the Skill's body without its frontmatter
(`trim`med), the `## Pi tool mapping` text and `</EXTREMELY_IMPORTANT>`. The message is `{role: "user", content: [{type: "text",
text}], timestamp: Date.now()}` and is never saved in the session: `context` only shapes one provider request.

### Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `pi.on("resources_discover")` | `OnEvent(EventResourcesDiscover)`, `skillsDir(ctx)` through `ctx.GetCommands()` | `TestSkillsDir`, twin "resources_discover contributes the bundled skills directory"; a real install, below |
| M2 | `pi.on("session_start")`, `pi.on("session_compact")`, `pi.on("agent_end")` and the flag | three `OnEvent` handlers over an `atomic.Bool` | `bootstrap-once`, `bootstrap-new-session`, `bootstrap-after-compact`; `TestInjectionCycle` |
| M3 | `pi.on("context")`: flag, dedupe by marker, build, insert | `OnEvent(EventContext)` returning `{messages: [...]}` (a handler that returns nothing leaves the list as the host sent it) | every scenario; twins "startup context injects ..." and "session_compact injects ..."; `TestInjectionPlacement` |
| M4 | `getBootstrapContent`, `stripFrontmatter` | `bootstrapContent` (cached), `stripFrontmatter`, `jsTrim` | every scenario (the text of the model request); `TestBootstrapText`, `TestStripFrontmatter` |
| M5 | `piToolMapping()` | `pi_tool_mapping.md`, extracted from the vendored original by `port/adapt.py` and embedded | `TestBootstrapText`; every scenario; mutation `mapping-text-changed` |
| M6 | `messageContainsBootstrap`, `firstNonCompactionSummaryIndex` | the same names | `TestMessageContainsBootstrap`, `TestFirstNonCompactionSummaryIndex`, `bootstrap-after-compact` |

### SDK gap check

`pigeq gaps --ts <oracle>/.pi/extensions/superpowers.ts --surface <PiG 0.5.0>/docs/extension-sdk-surface.md`: **no gaps**. The
`context` event that returns modified messages exists in the Go SDK (`EventContext`; a handler returns `{messages: [...]}`, and the
SDK reports whether the list changed), so nothing is `needs-SDK` for the extension itself. `pigeq gaps --go extensions/superpowers`: clean.

One thing the original has that the SDK does not give a Go extension is a location of its own (the original reads
`import.meta.url`): see F1. It is recorded as an SDK request in `status/superpowers.md` and `core-items.md` (C3+).

### Oracle layout (`port/oracle-layout.sh`)

The original finds the Skills at `<package root>/skills` and reads `skills/using-superpowers/SKILL.md`. This Package ships that
Skill as `pigpen-superpowers-using-superpowers`, so the proof runs the unmodified original over a tree built from the shipped one:
`skills/using-superpowers/SKILL.md` is the shipped Skill with its `description:` line removed. The extension reads only the body,
so the injected text is the shipped text; a Skill without a description is not loaded, so the original contributes no Skill to
the system prompt and the two lanes (which share no skills) compare equal. `cmp` checks the copied extension is the vendored one.
The unmodified Skills tree stays at `port/oracle/skills`; `port/adapt.py` regenerates `skills/` from it, and a diff of the result is empty.

### Results (this revision)

- `pigeq twins check --ledger port/upstream-tests.json --go extensions/superpowers`: **4 exact twins, 2 skipped**. The ledger is
  `pigeq twins list` over a copy of `tests/pi/test-pi-extension.mjs` named `test-pi-extension.test.mjs` (the lister reads
  `*.test.*` files). Skipped, each with its own reason: "package.json declares a pi package with skills and extension resources"
  (asserts upstream's own manifest; this Package's manifest is checked by `npm run check` and `pig package validate`) and "pi
  tools reference documents pi-specific mappings" (reads a Skill file outside the module; it is the shipped copy of the
  original's, checked by `port/adapt.py`).
- Red: `port/red-run.log` (commit `698c1819`): the twins and layer-1 tests against signature stubs, failing on behavior.
  Cases that describe a no-op pass on a stub by design and are proven by mutation: "nothing when the host lists no commands",
  "nothing when the host does not list the Skill", and the "unchanged" half of the injection cycle.
- `pigeq run` (original under Pi, original under PiG's Node runtime, port under PiG): **5 of 5 scenarios identical**, host check
  and port check, plus `sdk-gaps`, `port-gaps` and `exec-coverage`. Scenarios: `bootstrap-first-request`, `bootstrap-once`,
  `bootstrap-tool-loop` (two model requests in one run, both carry the bootstrap), `bootstrap-new-session`,
  `bootstrap-after-compact` (the summarising request has no bootstrap; the next request has it after the compaction summary).
- `pigeq record` then `pigeq check`: 5 of 5 against goldens recorded from the original under Pi 1.3.0.
- `pigeq mutate --unit` (`port/mutations.json`): **34 of 34 killed**: with `--unit` the layer-1 tests judge first, so 32 are killed by them (the flag and its events, the marker guard, the placement, the message shape, the trim set, the host-derived directory) and the 2 mutants of the embedded data files (`mapping-text-changed`, `skill-text-changed`) by the scenarios, which see the text of the model request. The first run left 3 alive (`skill-paths-key`,
  `frontmatter-needs-no-newline`, `trim-range-short`); each got a test and is killed now. The harness's mutation kinds that need
  a UI call or an `exec` (reordered or extra dialogs, a started command) do not apply: the extension makes neither.
- `go test -race -count=24`, `go vet` and `gofmt`: clean; `GOOS=windows|darwin|linux go vet`: clean.
- `pig package validate components/superpowers` and `pig install components/superpowers/extensions/superpowers --validate-only --json`
  (isolated HOME): `form: factory`, `language: go`.
- A real install (`pig install components/superpowers`, isolated HOME): `get_commands` lists the 15 Skills once each, no duplicate name.

### Differences and findings

1. **F1: the Skills directory comes from the host, not from a path.** The original returns `<package root>/skills`, derived from
   `import.meta.url`. A Go extension has no such path: PiG builds it with `-trimpath` in a cache, and the SDK has no accessor for the
   extension's own directory. The first version of this port derived the directory from `runtime.Caller`; `pigeq run` showed
   it returns nothing under `-trimpath`, so the red tests that asserted it were changed (this is the one case where tests changed after
   the red commit). The port asks the host (`ctx.GetCommands()`) where the shipped `using-superpowers` Skill was loaded from and
   returns its parent directory. Consequences: installed through its Package (the only way a Pigpen extension is delivered), the
   Skills are loaded by the manifest's `pi.skills` and the directory it returns is the same one, so nothing changes; loaded alone with
   `-e` and no Skills, it contributes none, where the original would add its own tree. SDK request (Pi has no public API either; the
   original uses a Node module path): an accessor for the extension's own directory or package root. Nothing in the scenarios sees it
   (the oracle layout has no Skill with a description), so it is proven by the layer-1 twin and by the real install.
2. **F2: the Skill is embedded, not read at run time.** The original reads `skills/using-superpowers/SKILL.md` and caches `null`
   when it cannot, so a missing file means no injection. The port embeds the shipped Skill (`bootstrap_skill.md`, written by
   `port/adapt.py` from `skills/pigpen-superpowers-using-superpowers/SKILL.md`), which cannot be missing: that branch does not exist.
   The embedded text is the Package's renamed Skill, so the bootstrap names the renamed Skills (the original's text has the 2
   `superpowers:<skill>` references of that file).
3. **F3: handlers cannot return `undefined`.** The Go SDK reports a handler that returns nothing as "unchanged list"; the twins
   assert that (`Unchanged`) where upstream asserts `undefined`.
4. **F4: the message objects are copies.** In JS the returned list holds the very objects the host sent; in Go they are decoded
   copies. Not observable through a provider request, and the twins compare by value where upstream compares by identity.
5. **F5: the `pig-essentials` Piglet offers the extensions as an optional selection, off by default.** A default stage or build selects the Skills only, so its behavior is unchanged; `--with pig-essentials:superpowers-extensions` (or `PIGPEN_WITH`) selects both extensions (see the Piglet README and `piglets/pig-essentials/optional.json`). The bootstrap adds about 7 KB to the first request of every session, which is why it is not on by default.

### Performance (imladris, CPUs 96-151, PiG 0.5.0, go1.27.2, Node 24.19.0)

There is no previous release of this extension; the comparison is the original under PiG's Node runtime.

| What | Go port | Original (Node runtime) |
|---|---|---|
| extension on disk | 5.76 MB runner binary (PiG's cache) | 4.3 KB source, plus the shared Node runtime files PiG caches (1.5 and 1.9 MB) |
| cold start: `pig --mode rpc` to the first response, median of 15 interleaved rounds | 79.0 ms (46.9 ms with no extension: +32.1 ms) | 386.3 ms (+339.4 ms) |
| `context` handler work on a 4-message request that gets the bootstrap, median of 10 runs of 200000 | 988 ns, 816 B, 9 allocs | 335 ns (in process, no allocation count) |
| `context` handler on a request that already holds the bootstrap | 66 ns, 0 allocs | not measured |

The handler is called once per model request of the first agent run (and the first after a compaction). Its Go time is about 0.65
microseconds more than the original's in-process handler and does not include the wire transfer, which is the same JSON
for both; the registration cost, paid once, is a tenth of the original's. The per-event difference exceeds the 10% rule in
relative terms and is under a microsecond in absolute terms: recorded for the owner (`status/superpowers.md`), no profile taken.
Reproduce: `go test -run xxx -bench . -benchtime 200000x -count 10` in `extensions/superpowers`; the Node and cold-start scripts are in
`handoffs/pigpen-parity/perf-superpowers-1/`.

## The Node helpers (`extensions/superpowers-tools`)

| Input | Identity |
|---|---|
| Original | `skills/brainstorming/scripts/server.cjs` (723 lines), `start-server.sh`, `stop-server.sh`, `helper.js`, `frame-template.html` and `skills/writing-skills/render-graphs.js` of `obra/superpowers` v7.0.0, commit `bb92a77741419a4ab5f06e711a283343f1ada0c3`, MIT, as shipped (byte for byte) in `port/oracle/skills` |
| Kind | 3 for the proof: Node programs, not Pi extensions, so there is no Pi oracle. The oracle is the unmodified Node original, run on the same fixtures by `TestAgainstNodeOriginal` (needs `node` and `SUPERPOWERS_NODE_ORIGINAL=<port/oracle/skills>`, which a Package build does not have, so the test is a named skip there). It is a separate extension from the bootstrap on purpose: the bootstrap's trace proof compares the tool list of the model request, and a tool would break it |
| Tools | `brainstorm_server` (`start`, `stop`) and `render_skill_graphs` |
| Original's tests | `tests/brainstorm-server/*.test.js` (7 files), `tests/writing-skills/test-render-graphs.sh`: **not vendored and not twinned case by case**; the Go tests below cover their subjects (auth, headers, WebSocket framing and handshake, `/files/` rules, lifecycle, port and key reuse, browser opener, render output) and `TestAgainstNodeOriginal` compares with the real thing |

Mapping: `computeAcceptKey`, `encodeFrame`, `decodeFrame`, the HTTP handler, `handleUpgrade`, `handleMessage`, the content watcher
(polling every 100 ms, new file versus update, new screen removes `state/events`), `maybeOpenBrowser` and `browserLauncherForPlatform`,
the idle watchdog, port and key persistence with the `EADDRINUSE` fallback, and `shutdown` are `companion.go`; `start-server.sh` and
`stop-server.sh` are `StartCompanion` and `StopCompanion`; `render-graphs.js` is `render.go`. `helper.js` and `frame-template.html` are
embedded unchanged (written by `port/adapt.py`).

Differences, recorded and deliberate:

1. The server is a goroutine of the extension process, not a detached `node`: no `nohup`, PID file, `server-instance-id`,
   `--foreground`/`--background`, Codex/Windows auto-foreground, or owner-PID check. `stop` stops the server of this process; a
   `server-info` left by an earlier process is reported `stale_pid` (the original's answer for a PID it cannot prove).
2. `fs.watch` is a 100 ms poll of the content directory (modification time and size), with the original's new-versus-updated rule.
3. `SUPERPOWERS_VERSION` is the constant `7.0.0` (the original reads the package manifest, which a Go extension cannot find; see F1).
4. The brand image is still loaded by the user's browser from primeradiant.com unless `SUPERPOWERS_DISABLE_TELEMETRY`,
   `DISABLE_TELEMETRY` or `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` is set, exactly as in the original.
5. The Skills' text still names the scripts; the tools' prompt guidelines redirect the model. The Skills tree is not edited because it is
   the original's byte for byte (the drift check enforces it).

Results (imladris, PiG 0.5.0, go1.27.2, Node 24.19.0):

- `go test -race -count=10` in `extensions/superpowers-tools` with `SUPERPOWERS_NODE_ORIGINAL=port/oracle/skills`: pass, including
  `TestAgainstNodeOriginal`: `render-graphs.js` and the port produce the same stdout and byte-identical files (`diagrams/*.svg`,
  `*_combined.dot`) with and without `--combine`, and `server.cjs` and the port answer 9 requests (no key, wrong key, key, cookie,
  `/files/` with a PNG, an unknown type, a dotfile, the bare directory, an unknown path) with the same status, body (port and key
  normalised) and the same `Content-Type`, `Referrer-Policy`, `Cache-Control`, `X-Frame-Options`, `Content-Security-Policy`,
  `Cross-Origin-Resource-Policy` and `Set-Cookie`. The comparison found three differences, fixed: the version the original reads from
  the manifest, the `Content-Type` that net/http adds to a 404 (the original sends none), and path cleaning of `/files/../x` by the
  default mux (a 307 where the original answers 404).
- Without the original (no `SUPERPOWERS_NODE_ORIGINAL`) that test is the one named skip; the other 11 cases pass.
- `pigeq mutate --unit-only --go extensions/superpowers-tools --mutations port/tools-mutations.json --sdk-dir $PIG_SDK_DIR --jobs 4`: 28
  mutations, **all killed** (the first run: 5 not killed; three did not build and were rewritten, one survived because it needed a test
  of a symlink inside the content directory, one because the stop reason was not asserted). One mutant, dropping the
  explicit symlink check of the content file test, is removed as equivalent: `os.FileInfo.Mode().IsRegular()` already rejects a symlink.
- `gofmt`, `go vet` with GOOS linux, windows and darwin: clean. `pig install extensions/superpowers-tools --validate-only --json`:
  `form: factory`, `language: go`, tools `brainstorm_server` and `render_skill_graphs`. `pig package validate components/superpowers`:
  2 extensions, 15 skills.
- Merged head, re-run: `go test -race -count=24` in both modules (with the Node original available): pass; `pigeq mutate` bootstrap
  34 of 34 and tools 28 of 28 killed.

Performance of the tools (imladris, CPUs 96-151, PiG 0.5.0, go1.27.2, Node 24.19.0; scripts in `handoffs/pigpen-parity/perf-superpowers-3/`):

| What | Go port | Node original |
|---|---|---|
| runner binary in PiG's cache | 7.95 MB (`superpowers-tools`), 5.76 MB (bootstrap) | `server.cjs` 723 lines, `render-graphs.js` 169 lines, run by the shared Node runtime |
| cold start, `pig --mode rpc` to the first response, median of 15 interleaved rounds | no extension 46.5 ms, bootstrap 79.4 ms (+32.9), tools 65.0 ms (+18.5) | the original bootstrap under PiG's Node runtime 389.3 ms (+342.8) |
| `GET /` of a framed screen, keep-alive, median of 10 runs of 3000 (Go) or 2000 (Node) | 274 us, 70 KB, 180 allocs (client and server in one process) | 327 us (Node client, server in its own process) |
| server start to `server-info` | in process, within the tool call | 53.8 ms (spawn of `node server.cjs`) |
| `render_skill_graphs`, three diagrams, a stand-in `dot`, median of 10 (Go) or 15 (Node) | 7.3 ms, 33 KB, 284 allocs (4 `dot` spawns included) | 59.2 ms for the whole `node render-graphs.js` process |

The Go server answers a request 16% faster than `server.cjs` and the renderer call is 8 times faster than starting the Node script, so
no tool regresses against its original. The comparison of the request path uses each side's own client (the Go benchmark cannot drive
the Node server and vice versa without a second process), so it bounds the difference rather than isolating the server.
For the bootstrap extension the in-process `context` handler stays slower than the original's (988 ns against 335 ns, 9 allocs): the
allocations are the message and content maps and the output slice that the SDK's `map[string]any` message type requires; no profile was
taken, and the absolute cost, once per model request of the first run, is under a microsecond.
