# Port record: jev

| Input | Identity |
|---|---|
| Original | [y0usaf/pi-jev](https://github.com/y0usaf/pi-jev) 0.2.2, commit `88e5fb3888948e7065110d47cdf6ac57abb71ba4`, MIT (y0usaf). Vendored unmodified in `oracle/` (`src/*.ts`, `package.json`, `README.md`, `LICENSE`; sha256 of the sources in `oracle/SHA256SUMS`) |
| Oracle | Pi 0.87.1 (real, `--mode rpc`), Node 24.19.0; every scenario is also identical under PiG's Node runtime (host check) |
| Target | PiG 0.3.0+0.87.1 (a pre-release build, `63c6ba456`), go1.27.1; the 0.4.0 (Pi 0.99.1) notes are at the end |
| Client | Shared Go Package `components/typesafe` (lane `pigpen-typesafe-client`, CONTRACT `841b25f`): the TypeSafe backend and the own-model backend. This port has no HTTP client of its own |
| Original's tests | none (no test script, no test files upstream). The cases are derived from every branch of the source, so there are no upstream test titles to twin: **0 exact twins, 0 skipped**; the named skipped tests below record the gaps |

## Contract table

Upstream lines are in `oracle/src`. "Scenario" = `port/scenarios/<name>.json` (original under Pi == port under PiG); "Go" = a layer-1 test through the fake host.

| ID | Upstream | Behavior | Go | Scenario / test |
|---|---|---|---|---|
| M1 | `index.ts:130` `session_start` | reload config; warn about config problems; no key: one warning; status `jev: <mode>` | `sessionStart` | every scenario (startup status); `TestKey_MissingKeyWarnsOnceAndStaysInactive`, `TestConfig_*` |
| M2 | `index.ts:152` `tool_call` | judge `gate.tools`; shadow notifies, enforce asks, headless degrades unless `blockWithoutUI` | `toolCall` | shadow-flagged, clear, enforce-accept, enforce-decline; `TestGate_Enforce*` (incl. headless: **not reachable through RPC**, fake host, print/JSON mode) |
| M3 | `index.ts:195` `tool_result` | judge `output.tools`, append `[pi-jev] <notice>`, notify on a leak | `toolResult` | output-leak, output-advice, output-low-confidence; `TestOutput_*` |
| M4 | `index.ts:290-318` cache and in-flight sharing | identical input judged once per `cacheSeconds`; siblings share a request | `memo.go` | cache-identical-calls; `TestGate_IdenticalCallIsJudgedOncePerWindow`, `…SiblingCallsShareOneInFlightRequest`, `…CacheHoldsSixtyFourVerdicts` |
| M5 | `gate.ts:61` `GATE_QUESTIONS` | four questions, exact text, exact order | `gateQuestions` | every gate request in the goldens; `TestGate_RequestShape` |
| M6 | `gate.ts:101-142` `buildGateState`, `summarizeArguments` | state `{cwd, tool, arguments, platform, user_request}`; strings elided at `argumentChars` with `…[N chars elided]`; user request cut at 1200 with `…[truncated]` | `gateStateJSON`, `summarize` | write-elision; `TestGate_LongArgumentsAreElided`, `…ArgumentCharsCountUTF16Units`, `…UserRequestIsTruncatedTo1200Units` |
| M7 | `gate.ts:144-194` `evaluateGate` | thresholds inclusive; impact needs `minConfidence`; reasons text | `evaluateGate` | shadow-flagged; `TestGate_EachThresholdIsInclusive`, `…JustBelowEachThresholdIsClear`, `…ImpactBelowMinConfidenceDoesNotFlag` |
| M8 | `gate.ts:212` `judgmentKey` | stable key over tool and input (property order ignored) | `stableKey` | `TestGate_CacheKeyIgnoresPropertyOrder` |
| M9 | `output.ts:52-136` questions, `CLASS_ADVICE`, `evaluateOutput` | leak wins over class; advice table; class floor | `output.go` | output-leak, output-advice, output-low-confidence; `TestOutput_*` |
| M10 | `output.ts:89` `outputKey` | identical output judged once per 120 s | `extension.go` | cache-identical-calls; `TestOutput_IdenticalOutputIsJudgedOnce` |
| M11 | `index.ts:367-466` `/jev` | `on`, `off`, `mode`, `last`, `output`, `check`, status | `command.go` | commands; `TestCommand_*` |
| M12 | `index.ts:471-560` `jev_ask`, `toQuestion`, `renderAnswers` | typed questions, exact rendering, validation messages | `ask.go` | jev-ask; `TestAsk_*` |
| M13 | `index.ts:624` `lastUserRequest` | latest user text in the branch | `lastUserRequest` (`GetBranch`) | every gate request carries `user_request`; `TestGate_LatestUserMessageWins`, `…UserRequestIsTrimmed` |
| M14 | `client.ts:110-360` HTTP client, retries, timeouts, error texts, `redact` | request, retry on 429/529/5xx, per-attempt timeout | **shared client** `typesafe.Client` | the goldens' `http` events (request shape, headers); retries and error texts are the client Package's own tests |
| M15 | `config.ts` layering, key resolution, validation | defaults ← `<agent dir>/pi-jev.json` ← `<cwd>/.pig/pi-jev.json`; key env > `apiKey` > `apiKeyFile` | `config.go` | `TestKey_*`, `TestConfig_*`, `TestProject_*` |

SDK gap check (`pigeq gaps --ts port/oracle/src`): 0 blocking, 4 PARTIAL, all shown not to matter here: `ctx.model` (`GetModelInfo`, used only to name the judge), `ctx.signal` (`Context.Done`, mapped to a `context.Context`), `ctx.ui.confirm(opts.signal)` (line 188: the dialog closes when the request is cancelled), `tool.promptSnippet` (the scenarios' `system.inserted` part of the model request is identical under PiG 0.3.0: the snippet and the guidelines reach the prompt). `pigeq gaps --go extensions/jev`: clean.

## Corrections to the original (approved: roadmap "Jev identification", owner 2026-09-28, and the lane task)

Each has the upstream reproduction, the corrected expectation and its Go test (red first, in the RED commit). The equivalence scenarios avoid every one of them, so parity is claimed only where the port does what the original does.

| ID | Upstream defect (reproduction) | Correction | Test |
|---|---|---|---|
| C1 | `index.ts:290` cache key ignored the user's request: the same arguments after the request changed reused a verdict for the old intent (review probe 1: one HTTP request, second status `clear (enforce)`) | key binds tool, input, cwd and the user's request; the cache is cleared when the configuration (judge, model) is reloaded; errors are never cached | `TestCorrection_CacheIsBoundToTheUserRequest`, `…CacheIsBoundToTheModelAndEndpoint`, `TestGate_ErrorsAreNotCached` |
| C2 | `config.ts:166-197` a project file chose the endpoint while the environment key travelled to it (review probe 2) | backend, endpoint, model, key, timeout, retries, consent and display come **only** from the user's own config; a project can only lower what leaves the machine; no built-in endpoint or model name; the endpoint must be `https` (or `http` to localhost); `TYPESAFE_*` variables are ignored by the client | `TestCorrection_ProjectFileCannotRedirectTheEndpoint`, `…NoDefaultEndpointForTheHTTPBackend`, `…PlainHTTPToARemoteHostIsRefused`, `…ProjectCanOnlyNarrowWhatLeaves`, `…ProjectCannotRaiseTheStateCapOrOutputLimit`, `…TypeSafeEnvironmentCannotRedirectTheKey` |
| C3 | `config.ts:273` `maxStateChars` parsed, documented, never read (NEXT.md acknowledges it) | applied: long strings shrink first, then arguments, then the request are dropped; the same cap on `jev_ask`'s text | `TestCorrection_MaxStateCharsCapsTheState`, `…KeepsShortenedArguments`, `…AskStateIsCappedAtMaxStateChars` |
| C4 | a key alone switched judging on, in shadow mode, with no word about where content goes | opt-in: nothing is judged until `/jev on` (which shows the disclosure and asks) or `"enabled": true` in the user's own config; a start-up notice says what leaves and where until `"acknowledged": true`; `/jev` names the destination and the fail-open policy | `TestOptIn_*`, `TestDisclosure_*`, `TestCommand_Status*` |
| C5 | `client.ts:249-265` accepted a probability of 1.7, a score of 9, a choice outside its options, a confidence of 4 | strict typed validation; any violation is an unavailable verdict (fails open), never a number compared to a threshold | `TestCorrection_OutOfRangeProbabilityIsAnError`, `…ScoreOutsideTheRubricIsAnError`, `…ConfidenceOutsideZeroToOneIsAnError`, `…ChoiceConfidence…`, `TestErrors_ChoiceOutsideItsOptionsIsAnError`, `TestErrors_EmptyAnswersAreNotAClearVerdict` (twin of the 0.2.2 fix) |
| C6 | on an error `tool_call` returned silently and the footer kept the earlier `jev: clear` | the footer says `jev: unavailable (failing open)` | `TestCorrection_ErrorStatusIsUnavailableNotStaleClear`, `…OutputJudgeErrorAlsoShowsUnavailable` |
| C7 | `gate.ts:120`, `output.ts:143` elision stops below depth 4 / 3, so a deeper string left whole (found while porting; not in the review) | elided at every depth (hard stop at 64 levels) | `TestCorrection_DeepStringsAreElidedToo` |
| C8 | `index.ts:497` a duplicate `jev_ask` id silently replaced the earlier question | refused: `duplicate question id` | `TestCorrection_DuplicateQuestionIdIsRefused` |
| C9 | `/jev` said `apiKey (inline)` for a key from the environment; `lastOutput` was recorded only when a notice existed, so a clean output read as "no tool output judged yet" | the real key source; every judged output is recorded | `TestCommand_StatusIsTruthfulAboutTheKeySource`, `TestCorrection_CleanOutputIsRecordedToo` |
| C10 | `jev_ask` sent the model's text after `/jev off` | it exists only while Jev is on and refuses ("Jev is off") after `/jev off` | `TestAsk_HonoursOff`, `TestAsk_RegisteredOnlyWhenEnabledWithAKey` |

## Review corrections (rev-pigpen-jev)

Found by the adversarial review of this port; each has a test in `extensions/jev/review_test.go` that was red on `477a904`
(R1 to R4) or kills a mutant that survived the lane's tests (R5), and a `review-*` entry in `mutations.json`.

| ID | Defect in the port | Correction | Test |
|---|---|---|---|
| R1 | enforce mode: when the confirmation dialog failed (host UI error), `tool_call` failed open and ran the flagged call. The original's handler throws there and Pi blocks the call (`index.ts:188`; Pi `agent-session.ts` `_installAgentToolHooks`, "Extension failed, blocking execution") | blocked, reason `pi-jev: <verdict> (not confirmed: <error>)`. Fail-open covers an unavailable judge, not a missing approval | `TestReview_EnforceConfirmFailureBlocks` |
| R2 | the default judge was resolved once at `session_start`; after a model switch Jev kept sending content to the previous provider while the disclosure called it "the provider that already receives your conversation" | `model_select` rebuilds the default judge (unless the user's own config names `"model"`), drops cached verdicts, tells the user, and registers `jev_ask` if no judge was available before | `TestReview_DefaultJudgeFollowsTheSelectedModel`, `…ConfiguredJudgeIgnoresModelSwitch`, `…ModelSelectedLaterEnablesTheDefaultJudge`; end to end `e2e/model-switch.py` |
| R3 | the disclosure (`/jev on`, start-up notice) listed what the gate and output judge send, not `jev_ask`'s text (up to `maxStateChars`) or `/jev check`'s | listed; `/jev` "Sends" names `jev_ask` text | `TestReview_DisclosureNamesJevAskAndCheck` |
| R4 | a repository's `.pig/pi-jev.json` could silently turn the user's gate off, `enforce` into `shadow`, `blockWithoutUI` off, raise thresholds or drop judged tools | still applied (it only sends less), but reported at start-up with the settings it relaxed | `TestReview_ProjectThatRelaxesTheGateIsReported`, `…StricterProjectIsNotReported` |
| R5 | test gaps, not code defects: a project-applied `apiKeyFile`, `backend`, `model` or `acknowledged`, `http://` to a remote IP literal, an output cache key without the tool, and the C3 drop order all survived as mutants | guard tests | `TestReview_ProjectCannotChooseTheKeyFile`, `…CannotSwitchTheBackend`, `…CannotChooseTheModel`, `…CannotHideTheDisclosure`, `…PlainHTTPToARemoteIPIsRefused`, `…OutputCacheIsPerTool`, `…StateCapDropsArgumentsBeforeTheRequest` |

The Piglets selecting Jev (`piglets/jev`, `pig-with-batteries`) said `tools: []`, which per PiG's Piglet docs exposes none of
the extension's tools, yet the README promises `jev_ask`. It was exposed only because PiG 0.3.0 does not apply a Piglet's tool
scope to tools registered after start-up. Both now say `tools: [jev_ask]`.

## Other differences (mechanical, not corrections)

- **D1 endpoint.** The original's `endpoint` is the full request URL; the shared client wants the API root. A trailing `/v1/systemone` is removed, so the same setting works in both (the scenarios rely on it).
- **D2 argument key order.** The SDK decodes event data into `map[string]any`, so the arguments are sent with sorted keys. Named skipped test `TestGate_ArgumentsKeepInsertionOrder_GAP`.
- **D3 UTF-16.** Limits count UTF-16 units like JavaScript. A cut through a surrogate pair yields U+FFFD (a Go string cannot hold a lone surrogate); JSON stays valid. `TestGate_CutInsideSurrogatePairStaysValidUTF8`.
- **D4 `cacheSeconds` 0** disables the cache (upstream reused within the same millisecond).
- **D5 answer order.** `jev_ask` and `/jev last` list answers in question order; the original used the server's key order. The SDK result is a map, and the API answers in question order (the scenarios' fake server does).
- **D6 paths.** Agent directory `PIG_CODING_AGENT_DIR` (default `<PIG_HOME>/agent`), project directory `<cwd>/.pig`, file name kept (`pi-jev.json`).
- **D7 `/jev check`** needs Jev on (it sends the user's text): `TestCommand_CheckNeedsJevOn`.
- **D8 default judge.** The original always called the TypeSafe API (`api.typesafe.ai`, model `jev-latest`). Here the default judge is the model PiG is configured with (`"backend": "model"`), and the TypeSafe API is chosen with an explicit endpoint and model. `TestNoHardcodedProviderOrEndpoint` greps the sources.
- **D9 user message text blocks** are joined with no separator (SDK `BranchEntry`), the original joined with `\n`. `TestUserRequestJoinsTextBlocksWithNewline_GAP` (skipped, named).
- **D10 display.** `"display": "rich"` (default) adds glyphs, a verdict card and the destination; `"plain"` is the original's wording character for character (the scenarios run in it).

## Results (this revision)

- **Layer 1** (fake PiG host, real SDK over `net.Pipe`): 135 tests pass (121 from the lane, 14 from the review), 2 skipped with `-race -count=3` against the real shared client (`components/typesafe`). Two named skipped tests: `TestGate_ArgumentsKeepInsertionOrder_GAP` (D2), `TestUserRequestJoinsTextBlocksWithNewline_GAP` (D9). The own-model backend is tested through the template's `ModelStream` (`ownmodel_test.go`, 7 tests).
- **Red first**: `port/red-run.log`: on the registering-nothing stub 81 of 101 tests failed; the 18 that passed are the no-op cases (off by default, disabled, unjudged tool, no hardcoded provider), proved by the mutation check below.
- **`pigeq check`** (build 5, `agentFiles`): 12 of 12 scenarios identical to the traces the original recorded under Pi 0.87.1 (`pig-go == pi-ts`), 28 s. `pigeq record --ts` recorded them; each is also identical under PiG's Node runtime (`host check`). `port-gaps` and `exec-coverage` pass.
- **Mutations**: 81 from the lane, one per contract row and per no-op case, plus 15 `review-*` (`mutations.json`, `mutate-unit.py`). **96 of 96 killed, all by the unit layer** (review run); the lane's run was 81 of 81 (about 5 s each); none INVALID, none survived. The scenarios would also kill the wording and request-shape mutants, but the harness's own `pigeq mutate --unit` cannot run this port (its go.work makes a shared library Package unresolvable: FRICTION note, fix proposed), so `port/mutate-unit.py` builds each mutant with a go.work that works and runs the port's tests. `python3 port/mutate-unit.py` runs the list from any checkout (`PIG_SDK_DIR` or `pig reload --sdk-path`, `go` on PATH). A cache key without the working directory is also equivalent (the working directory is fixed for a session and every `session_start` clears the caches). Two mutants were removed as equivalent, with reasons: the judge id in the cache key (the caches are cleared on every configuration reload, so the judge cannot change under a cache) and the client's `Getenv` override (an explicit `BaseURL`, key, model and log level already take precedence over the environment; the line stays as defence in depth).
- **Binary**: `pig piglet build dist/staged/piglets/jev/piglet.yaml --format binary` builds with "Preparing fused Go members: jev" (pig 0.3.0+0.87.1, go1.27.1). `pig-with-batteries` cannot build a Binary because it selects the Node herdr reporter (`lock extension "extension": selected origin is unavailable`, RELEASE-BLOCKERS item 2, unrelated to Jev); the same manifest without herdr builds (58 MB).
- **End to end on the built Binaries** (`port/e2e/*.py`, real host in RPC mode, a scripted OpenAI-compatible model from `pigeq llm`, isolated dirs): (1) `gate-and-output.py`, the own-model backend, `/jev on` with its confirmation, a `bash` call: 4 model requests (agent, gate, output judge, agent), the shadow card and the leak notice appear, `/jev` shows the status; (2) `jev-ask.py`: `jev_ask` is registered at run time by `/jev on` and answered by the session model (3 requests); (3) `off-by-default.py` on the batteries Binary without herdr: no `/jev on` means 2 model requests (only the agent's) and no Jev UI output; (4) `model-switch.py` (review, R2): two scripted providers, `/jev on` on `eq-a`, RPC `set_model` to `eq-b`, one `bash` call: all 4 requests reach `eq-b` (the lane's Binary sent the gate and output judgments to `eq-a`). These use `--mode rpc`, not a TUI: no terminal screenshot was taken.
- **Cross-platform**: `GOOS=linux|darwin|windows go vet ./...` clean.

## Against PiG 0.4.0 (Pi 0.99.1)

Nothing here is Pi-version specific except the recorded goldens (Pi 0.87.1): re-record them under 0.99.1. Watch: the SDK may deliver event data with key order (D2 would go away); the `prompt snippet` host gap listed by `pigeq gaps` is already satisfied for 0.3.0 by the scenarios; built-in model access (`ModelRegistry`) is the own-model backend's only host dependency; the roadmap's built-in MCP does not touch this extension. `TYPESAFE_*` and the agent directory rules (D6) should be re-checked if 0.4.0 renames `PIG_CODING_AGENT_DIR`.

## Findings about the hosts

1. PiG hands `tool_call`/`tool_result` data over as `map[string]any` (D2).
2. `ModelRegistry.Complete` (used by the own-model backend) reaches the host through the `modelStream` call, which the SDK's fake-host template cannot answer; the own-model backend is proved end to end with a real binary instead (see the results).
3. Real Pi and PiG's Node runtime print a JSON-escaped `>` differently in the recorded model request (`>` vs `\u003e`): the scenarios avoid redirections in scripted commands.

## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the original under Pi 1.0.1 (host check: identical under PiG's Node runtime), with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). The bash tool's results now carry `structuredContent` (6 events), whose `wall_time_seconds` the harness normalizes (N6). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.
