# Port record: obra/superpowers (Skills)

| Input | Identity |
|---|---|
| Original | `obra/superpowers` v6.4.2 by Jesse Vincent, https://github.com/obra/superpowers, commit `8ca22dba9a94f28898bbce59f2537ff4d87c747d`, MIT |
| Kind | a Skills Package: Markdown Skills, plus one Go extension (the bootstrap, ported from the original's Pi extension: see [the extension](#the-pi-extension-extensionssuperpowers)) |
| Original kept at | `port/oracle` (the unmodified `skills/` tree and `LICENSE`) |
| Target | PiG 0.4.1 |

## What ships

The fifteen Skills of the original, built from `port/oracle/skills` by `port/adapt.py`. The only changes are the ones PiG's
Skill format and Pigpen's Skill gate require, listed in [ADAPTATIONS.md](ADAPTATIONS.md): the `pigpen-superpowers-` prefix
on each Skill's `name` and directory, and the 39 references of the form `superpowers:<skill>` rewritten to the new names.
Every other byte is the original's.

## What does not ship

- The original's hooks (`hooks/`) and the other harness manifests. The original's Pi extension ships as a Go port.

## What ships but needs Node

Two Node helper scripts ship as the original has them, inside their Skills, and run only when the model starts them:
the visual companion server of `brainstorming` (`scripts/server.cjs`, with its browser-side `helper.js`) and the
flowchart renderer of `writing-skills` (`render-graphs.js`). On a machine without Node they cannot run.

## Checks

The Skills have no behavior to run against the original: the check is that the shipped tree
equals the original except for the recorded changes (`python3 port/adapt.py` regenerates `skills/` and ADAPTATIONS.md; a
diff of the result is empty), `npm run check` (the Skill gate: names, directories, licenses, provenance) and
`pig package validate components/superpowers`.

## The Pi extension (`extensions/superpowers`)

| Input | Identity |
|---|---|
| Original | `.pi/extensions/superpowers.ts` of `obra/superpowers` v6.4.2 by Jesse Vincent, https://github.com/obra/superpowers, commit `8ca22dba9a94f28898bbce59f2537ff4d87c747d`, MIT. Identical at v7.0.0 (`bb92a77741419a4ab5f06e711a283343f1ada0c3`): `git diff v6.4.2 v7.0.0 -- .pi tests/pi` is empty. |
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
- `pigeq mutate --unit` (`port/mutations.json`): **34 of 34 killed**: 26 by the layer-1 tests (the flag and its events, the marker guard, the
  placement, the message shape, the trim set, the host-derived directory), 8 by the scenarios (the text of the model request, the
  summary placement, the reset on a new session and on compaction). The first run left 3 alive (`skill-paths-key`,
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
5. **F5: the `pig-essentials` Piglet does not select the extension.** It selects the Package's Skills only, so its behavior is
   unchanged. Adding the extension (about 7 KB in the first request of every session) is an owner decision.

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
