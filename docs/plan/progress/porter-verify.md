# porter-verify: the extension porter re-verified on PiG 0.4.1 (Pi 1.0.1)

Status: READY. Base: the reviewed games-play-well work (`f0847d0`). Dogfood plan, Phase C.

## What was run against

- **pig**: built from PiG `port/0.4.1` (`5f948f86a5130641387b0df8469cc1c982c6706b`, "Follow Pi 1.0.1 (0.4.1
  content)"), which is PiG's development main (`c8596596d`) plus the 0.4.1 content. That main alone has no D89
  (`registerToolRenderer`), so the checks use the 0.4.1 build. Its release identity is not bumped on that branch:
  `pig --version` prints `0.3.1+1.0.1` (it satisfies `scripts/pig-requirement.json`, 0.3.1).
- **Pi**: `@earendil-works/pi-coding-agent@1.0.1` (what that pig matches; `pigeq env` refuses Pi 0.87.1 with it).
- Node 24.19.0, Go 1.27.1, linux/amd64. Every `pig`/`pi` run in an isolated HOME, PIG_HOME and agent directory.

## What broke, and why

1. **Every golden trace failed `pigeq check`** (all 9 ports with scenarios). Root cause: Pi 0.99.0 added the
   input disposition to RPC `prompt`, `steer` and `follow_up` responses (`{"data":{"disposition":"started"|"handled"}}`,
   pi-coding-agent 1.0.1 `dist/modes/rpc/rpc-mode.js:307-325`, CHANGELOG 0.99.0 #9098/#9803), and PiG 0.4 follows it.
   The goldens were recorded under Pi 0.87.1, so the first `prompt` response of every scenario differed. In jev and
   warden Pi's bash tool results also gained `structuredContent` (`output`, `exit_code`, `truncated`,
   `wall_time_seconds`), `isError: true` on a failure and no empty `details` (`dist/core/tools/bash.js:284-300`).
2. **The bash tool's wall time is a clock in the trace.** `wall_time_seconds` is the command's duration rounded to
   0.1 s (`bash.js:284`): 0 in every recording here, 0.1 the first time a command takes 50 ms under load. New
   normalizer rule N6 replaces only that value in a `tool_execution_end` result's `structuredContent` (every other
   structured key is compared); normalizer v3.
3. **pigpen-warden's record failed `sdk-gaps` with "NOT AN EXTENSION".** Its original is `port/eq-entry.ts`, a one-line
   re-export of the vendored `port/oracle/src/extension.ts`; the gap scan, the extension test and the exec coverage read
   only the named file. They now read a file original with the local modules it imports or re-exports (relative
   specifiers, `.js` written for `.ts`, directory index; never `node_modules`). The warden record now lists the
   original's real PARTIAL gaps and `git` in the exec coverage.
4. **The embedded gap table was PiG 0.3.0's.** It still called `pi.events.on/emit` and the `withSession` callbacks
   missing for Go (PiG 0.4 implements them) and had no idea of 0.4's new rows. Refreshed from PiG 0.4.1's
   `docs/extension-sdk-surface.md` (41 non-implemented rows; `pi.registerToolRenderer` is `implemented
   Extension.ToolRenderer`). The 0.3.0 snapshot stays as a test fixture for the rules that only fire when the bus is
   missing (indirect `pi.events` use).
5. **The fake host had no D89.** A port of an extension that calls `pi.registerToolRenderer` could not test its
   resolvers: the template ignored the register frame's `tool_renderers` count and every notify, and could not send
   `resolve_tool_renderers` or `render_tool`. Added `ToolRenderers()`, `ResolveToolRenderers(tool, next)`,
   `RenderTool(tool, ToolRender{...})`, `Invalidated()` and `ReleaseToolCard(card)`, with the wire shapes of PiG's
   `coding/extension/host/subprocess/protocol.go` (`RequestResolveToolRenderers`, `RequestRenderTool`,
   `NotifyToolRenderers`, `NotifyToolRenderInvalidate`, `NotifyToolRenderRelease`). The template's 13 copies are
   updated with it (the template test requires equal copies).
6. **`test:port` skipped the live re-run of three Pi ports** (pi-typesafe, warden, websearch: "build the original
   first"). It re-ran the package directory, which needs a `dist` build, while their traces were recorded from a source
   file Pi compiles itself (`oracle/src/extension.ts`, `eq-entry.ts`, `oracle/index.ts`). The live step now re-runs the
   file under `port/` whose sha256 is the golden header's `extension` hash, and only asks for the package's
   dependencies (`npm ci --ignore-scripts` in `port/oracle`).
7. **tool-scope: PiG core item 9 is fixed.** The two `todo` cases (a per-extension `tools: []` in print and JSON
   mode) pass on 0.4.1; a control run without the scope offers `pig_doctor` in both modes, so the pass is real. The
   todo is dropped and RELEASE-BLOCKERS item 9 marked resolved.

## Re-recorded traces (diff, not overwrite)

Each port was recorded into a scratch directory first (`record` with the golden's own oracle: `--ts` the file whose
hash the old header names, `--go-oracle` a `git archive` of PiG `d86eb93f217e` `piglets/standard` with the one
documented `pig-default` rename, or `--self`), then compared event by event with the committed traces. Only after the
diff showed nothing but Pi's format change were they copied over. Recording twice gives identical bytes (warden).

| Port | Oracle | Scenarios | Differences (all events compared) |
|---|---|---|---|
| a2a | self | 6 | 6 `disposition` |
| angry-pigs | upstream Go | 1 | 1 `disposition` |
| dirty-repo-guard | Pi | 11 | 13 `disposition` |
| jev | Pi | 12 | 19 `disposition`, 6 bash `structuredContent` results |
| pi-typesafe | Pi | 12 | 22 `disposition` |
| pig-runner | upstream Go | 1 | 2 `disposition` |
| pig-snake | self | 3 | 3 `disposition` |
| warden | Pi | 19 | 20 `disposition`, 15 bash `structuredContent` results |
| websearch | Pi | 5 | 5 `disposition` |

No other difference in any file. Every Pi recording passed its host check (Pi 1.0.1 == PiG's Node runtime), and
`pigeq check` of each Go port against the new traces passes (all scenarios plus `port-gaps` and `exec-coverage`).

## Results

- `npm test`: 117/117. `npm run test:porter`: 2/2. `npm run test:tool-scope`: 3/3, no todo. `npm run test:moved`: 1/1.
  `npm run test:go-ports`: every module vetted and tested, exit 0.
- Harness Go tests (`extension-equivalence` and `cmd/pigeq`): pass; `go test -race -count=30` of the fake-host
  tests; `go vet` for linux, darwin and windows.
- Red first: the D89 fake-host tests failed on signature stubs (count 0, empty answers, no lines); the
  module-following test failed with the old single-file read (not an extension, no gaps, no command); the N6 test
  failed with the wall time compared raw; the current-surface gap assertions failed on the 0.3.0 snapshot. Three
  mutants of the fake host (drop the register count, the notify count, the invalidation) each fail its tests.
- `pigeq gaps`: every original and port clean of blockers. The D89 fixture (`eq/testdata/gaps/tool-renderer.ts`)
  reports only the `tool.renderCall` stand-in, on the embedded snapshot and on the full PiG 0.4.1 table; on a table
  whose Go cell is missing the same call blocks.
- `--self` baselines: a2a and pig-snake (the originals with scenarios) re-recorded and checked. herdr, session-ingest
  and context-info have no scenarios; their Go tests pass (`test:go-ports`, `test:moved`).
- Rows without scenarios (acp, ahp, typesafe-client, skills, pig-play, extension-porter): proven by their module
  tests in `test:go-ports` and the repository tests above.
- `mutate`: dirty-repo-guard 26/26, angry-pigs 17/17, pig-runner 17/17 killed (`--unit`); dirty-repo-guard with
  scenarios only, 24/26: the two `hasUI` mutants survive by construction and are killed by layer 1 (PORT.md M6).
- `npm run test:port` with `PI_BIN` (Pi 1.0.1): 34 pass, 0 fail, 11 skipped (before the fix above). Every port's
  gaps, `check` and full mutation list (a2a, angry-pigs, dirty-repo-guard, jev, pi-typesafe, pig-runner, pig-snake,
  warden, websearch: all killed). The live re-runs after the fix, with `PIG_UPSTREAM` an export of PiG `d86eb93f217e`:
  7 of 7 pass (dirty-repo-guard, jev, pi-typesafe, warden, websearch under Pi 1.0.1; angry-pigs, pig-runner against
  the upstream Go extensions); a2a and pig-snake are self baselines with no original.
- Piglet Binaries: all 10 build for linux/amd64 with fused members, and each starts in RPC mode in an isolated home
  and lists its extension commands. The other 4 targets need native runners (pig: "native builder supports only
  host target"; the container builder needs a tagged release): every module `go vet`s (build and test compile) for
  linux/arm64, darwin/arm64, darwin/amd64 and windows/amd64.
- `pigeq check --builtin` on the Binaries: warden (pig-warden) 19/19, angry-pigs and pig-runner (pig-games) pass. jev,
  pi-typesafe and websearch differ at the first scripted model request only because a Piglet Binary offers more
  built-in tools (`read, bash, powershell, edit, write, grep, find, ls`) than plain pig (`read, bash, edit, write`),
  which changes the request's tools and the system prompt's rules; warden passes because its scenarios pin `--tools`.
  a2a and pig-snake goldens are `--go` self baselines (the harness says to record `--self --builtin` for a Binary).

## Findings for PiG (not fixed here)

- A Piglet (Binary or `--piglet`) in RPC mode activates every built-in tool, and with discovery off also `codemode`
  and `tool_search`; print mode activates Pi's default four. Seen in the tool-scope control run and the `--builtin`
  checks above.
- `port/0.4.1` does not bump the release identity, so its builds print `0.3.1+1.0.1` and golden headers say
  `pig 0.3.1+1.0.1`.

## Follow-ups

- herdr: PiG 0.4's Go SDK has `pi.events` (`Extension.Events()`); RELEASE-BLOCKERS says to listen for `herdr:blocked`
  and un-skip its two tests when it ships.
- Pigpen's pending SDK bump (`scripts/pig-requirement.json` to 0.4.x, pigpen-040-fixes) waits on a pig that reports
  0.4.x and has D89; the D89 fake-host self-test needs an SDK with `Extension.ToolRenderer`.
