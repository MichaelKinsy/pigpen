# Lane LsExt: extension, config, metadata, conversion

Files: `extension.go config.go metadata.go convert.go urlparse.go jsutil.go`; tests `fakehost_test.go` (the Skill's
template, `langsmith_test`), `twin_test.go` (twin template), `twin_*_test.go`, `extension_test.go`, `factory_test.go`,
`lsrig_test.go`, `export_test.go` (bridge to the external test package), `config_test.go convert_test.go
metadata_test.go`; vectors `testdata/ext_*.json` from `port/tools/gen/ext-vectors.mjs` (real `dist/` 0.2.1 run under node,
zod 4.6.5); ledger `port/upstream-tests.json`.

Evidence: `pigeq twins check`: 18 exact twins, 3 skipped (the three `host-options` cases, E1 below). `pigeq gaps --go`:
one non-blocking PARTIAL (`Context.GetModelInfo`, see Open questions). `GOOS=linux|darwin|windows go vet ./...` clean.
`go test -race -count=3` of this lane's tests green. Benchmarks (xd6702, Xeon 8468, ordered input / host map input):
`ConvertMessages` 5.7 µs, 4.2 KB, 49 allocs; `ConvertProviderPayload` anthropic 3.6 µs 33 allocs, responses 3.0 µs 30
allocs, gemini 6.2 µs 64 allocs; anthropic from a host `map[string]any` 11 µs, 104 allocs (keys are sorted on every read).

## Named exclusions

* **E1: the embedding options.** `LangSmithExtensionOptions.client`, `.config`, `.getCurrentRunTree` (index.ts:383-418,
  471-476) and the langsmith JS SDK `traceable`/`withRunTree` ambient run: a Go Piglet is built by the host and has no
  call site that can pass them. `Extension()` takes no arguments; with no ambient run `getCurrentRunTree(true)` is
  `undefined` upstream too, so every agent run is a new root trace (`TestRootRunsAreNewTraces`). Twins skipped, each
  with its reason: the three `host-options` cases.
* **`src/sdk.ts`** is the README's embedding example (`createAgentSession` + `langsmith(pi, { config, getCurrentRunTree })`),
  not extension code: not ported.
* Test seams (`newExtension(cfg, client, piVersion)`, `export_test.go`) are unexported / test-only, not an entry point.

## Contract table

| ID | Upstream | Go | Test |
|---|---|---|---|
| E-01 | index.ts:14 STATUS_KEY, 434-440 session_start status `LangSmith: tracing\|disabled` | `extension.go` `newExtension` | `TestSessionStartStatusAndCommand`, `TestDisabledRegistersOnlyStatusAndCommand` |
| E-02 | index.ts:442-450 `/langsmith-tracing`, both messages and levels | `newExtension` | same two tests |
| E-03 | index.ts:452 `if (!enabled \|\| !client) return` | `newExtension` | `TestDisabledRegistersOnlyStatusAndCommand`, `TestEnabledRegistersEveryHook` |
| E-04 | index.ts:453-495 before_agent_start (previous run ended with loopResult / interrupted, staleSettles, thread id, user turn counter, root run, status `tracing run`) | `beforeAgentStart` | `TestInterruptedByNextRun`, `TestStaleSettle...`, `TestRootRunPayload`, `TestImageCountIsTheJSLength`, twin `increments turn_number...` |
| E-05 | index.ts:497-512 session_compact | `sessionCompact` | twin `overflow compaction...`, `TestCompactionRun` |
| E-06 | index.ts:514-538 turn_start (turn run, pendingLlm start) | `turnStart` | `TestTurnStartMetadata`, `TestDeferredLlmStartsInTheNextTurn` |
| E-07 | index.ts:540-558 before_provider_request (name fallback, defer to next turn) | `beforeProviderRequest` | `TestProviderRequestNaming`, `TestDeferredLlm...` |
| E-08 | index.ts:560-577 message_end | `messageEnd` | `TestMessageEndBranches` |
| E-09 | index.ts:579-609 tool_execution_start/update/end | `toolExecutionStart/Update/End` | `TestToolRunLifecycle`, `TestToolRunFallsBackToTheRootWithoutATurn` |
| E-10 | index.ts:611-625 turn_end | `turnEnd` | `TestTurnEndFindsTheTurnByIndexOrCurrent`, the replay twins |
| E-11 | index.ts:627-672 agent_end (tools, pending llm, llm, turns, loopResult, `PI_EMITS_AGENT_SETTLED` branch, status `traced`) | `agentEnd` | `TestOutstandingTools...`, `TestAgentEndErrorBranches`, `TestAgentEnd...EndsTheRunAtOnce`, `TestPendingLlm...` |
| E-12 | index.ts:674-686 agent_settled | `agentSettled` | `TestStaleSettle...`, `TestSettleWithoutAgentEndIsIncomplete`, twin `ignores a late agent_settled...` |
| E-13 | index.ts:688-711 session_shutdown (end runs, reset staleSettles, await pending batches, clear status) | `sessionShutdown` | `TestShutdownEndsTheRunAndClearsStatus`, `TestShutdownAfterAgentEndKeepsTheLoopResult`, `TestPendingLlm...` |
| E-14 | index.ts:16-22 `PI_EMITS_AGENT_SETTLED` from `VERSION` | `piEmitsAgentSettled`, `Extension()` reads `sdk.Version()` | `TestPiEmitsAgentSettled`, `TestFactoryReadsThePiVersionFromTheHost`, twins `still ends the trace at agent_end...`, `ignores a late...` |
| E-15 | index.ts:58-105 `safeAdd`, `extractUsageMetadata` | `convert.go` | `TestConvertVectors` (real JS vectors), `TestExtractUsageMetadataOmits...`, twin `omits cost fields...` |
| E-16 | index.ts:118-181 `normalizeContentPart`, `convertContentPart`, `convertMessages`, `convertToolOutputs` | `convert.go` | `TestConvertVectors`, normalize twins |
| E-17 | index.ts:183-315 `convertProviderPayload` (Responses, Gemini, Anthropic messages/system, `_raw`) | `convertProviderPayload` | `TestConvertVectors` (274 vectors incl. the four recordings, byte-exact key order), replay twins |
| E-18 | index.ts:350-381 `createRootRun` | `createRootRun` | `TestRootRunPayload`, `TestReplicasReceiveTheRuns` |
| E-19 | index.ts:406-418 factory: config, client | `Extension()` | `TestFactoryFailsOnAZodError`, `TestFactoryRefusesOTelTracingLoudly`, `TestFactoryDisabledByDefault` |
| C-01 | config.ts:7-27 ReplicaSchema (aliases, `??`, key stripping) | `validateReplica` | `TestConfigVectors`, `TestReplicaValidationBranches` |
| C-02 | config.ts:31-53 ConfigSchema, PartialConfigSchema | `validatePartial`, `zodError`, `parsedType` (`Infinity`, `-Infinity`, `nan` as zod 4 names them) | `TestConfigVectors` (every ZodError text from real zod, incl. `1e400`), `TestZodErrorShape` |
| C-03 | config.ts:55-63 `parseBoolean` | `parseBoolean` | `TestParseBoolean`, vectors |
| C-04 | config.ts:65-73 `parseJson` | `parseJSON` (ordered `JSON.parse`) | `TestParseJSON`, vectors |
| C-05 | config.ts:81-88 `readConfigFile` (any failure is undefined) | `readConfigFile` | vectors (bad JSON, arrays, BOM, null fields, wrong types) |
| C-06 | config.ts:90-107 `getVar`, `readConfigEnv` (ZodError not caught) | `getVar`, `readConfigEnv` | `TestGetVarFallback`, vectors |
| C-07 | config.ts:109-133 `getHomeDir`, `getConfig` merge order | `getHomeDir`, `getConfig` | `TestGetHomeDir`, `TestGetConfigOptionsNil`, vectors |
| M-01 | metadata.ts:25-36 `runGit` | `runGit` (1 MiB cap, ctx, trimmed) | `TestRunGitOutputCap`, `TestRunGitStuckProcessEndsWithContext` |
| M-02 | metadata.ts:39-70 `parseRemote` (scp regexp, `new URL`) | `parseRemote`, `matchScp`, `urlparse.go` | `TestParseRemoteVectors` (real `new URL` on ~125 remotes), `TestMatchScp` |
| M-03 | metadata.ts:72-94 `getGitInfo` cache per cwd | `getGitInfo` | `TestGetGitInfo*` |
| M-04 | metadata.ts:96-102 `localUsername` | `localUsername` | `TestLocalUsername` |
| M-05 | metadata.ts:104-152 `compact`, `codingAgentMetadata` | `codingAgentMetadata` | `TestCodingAgentMetadataKeyOrderAndOptionalKeys`, contract twin |
| M-06 | `toLocaleLowerCase` of the model name | `jsToLowerCase` (jstext.go: U+0130, Final_Sigma) | `TestLowerCaseVectors` |

## Decisions and findings (for PORT.md)

1. **Factory errors are panics.** `sdk.Factory` returns no error; a rejected upstream factory (ZodError from the environment,
   `new Client` throwing, OTEL mode) panics in `Extension()`, which the host reports as a failed load (the process exits before
   it connects). The ZodError text equals zod 4's (`JSON.stringify(issues, null, 2)`), verified against the real library.
   The host shows only the first line of a panic, so the panic text starts with a one-line summary of the first issue
   (`langsmith: invalid configuration: replicas: Invalid input: expected array, received object`: path joined with `.`, then the
   message), then a newline and the full ZodError (`factoryError`, `TestFactoryPanicFirstLineSummarisesTheZodError`). Other refusals
   (sampling rate, OTEL) are one line already (`langsmith: <message>`).
2. **Session id.** `ctx.sessionManager?.getSessionId?.()` is `ctx.GetSessionID()`; a host failure fails the hook (upstream's
   optional chain only yields `undefined` for a ctx without a session manager, which Pi never has).
3. **Model.** `ctx.model?.provider` / `.name` come from one `GetModelInfo()` call per `turn_start` and `before_provider_request`.
   A model without a name (empty `Name`) is `undefined`. `toLocaleLowerCase()` is ported as `toLowerCase()`; they differ only
   under a tr/az/lt default locale (the vectors assert the node default equals `toLowerCase` for every sample).
4. **Serialised hooks.** One mutex serialises all hooks (upstream interleaves only at `await`s). A hook holds it while it posts,
   so a slow LangSmith endpoint delays the next event by the client's own timeout (the SDK posts under `await` upstream too).
5. **`convertMessages([event.message])` is computed once in `turn_end`** (upstream computes it twice, identical, even when
   there is no run to end; the TypeError for a missing message is kept).
6. **patchRun failures reject.** `safeEnd` returns the error `runTree.patchRun` returns (replicas configured), as upstream's
   un-caught `await run.patchRun()` rejects the hook. `postRun` logs and continues.
7. **ctx.getContextUsage() is called at `agent_end`** and its host error fails the hook; `undefined` (nil) drops
   `context_usage`. The fake host cannot answer JSON `null`, so no test covers the nil branch through the host.
8. **Defects ported:** `{user:"user",model:"assistant"}[role] ?? role` finds `Object.prototype` members: a Gemini role of
   `constructor`, `toString`, ... yields no `role` key and `__proto__` yields `{}` (`geminiRole`); `messages.some(msg => msg.role...)`
   throws on a `null` entry (`errNullRole`); the `anthropic` branch drops an empty `tool_result`-only user message because
   `[].every()` is true; `flatMap` splices a top-level array element into the message list.
9. **zod record drops `__proto__`** (`zodRecord`), as the real output does.
10. **Config metadata order** is the JSON text's order (ordered parser), not sorted: the one place Go can keep upstream's
    order for user-supplied objects. Objects from the host stay F1 (sorted).
11. **Non-numbers in Pi usage** count as 0 in `safeAdd` / the cost test; upstream would concatenate strings. Pi types them as numbers.
12. **`errorMessage` that is not a string** is stringified (upstream passes it through typed as string).
13. **`parseRemote`'s `new URL`** is `urlparse.go`, a WHATWG subset (special and opaque-host schemes, userinfo, ports,
    IPv4/IPv6, dot segments, percent-encoding, and the `file:` host rules: no userinfo or port, a Windows drive letter such as `file://C:/x` or `file://D:\\x` is path so the host is empty, `localhost` is empty, `file://server:1/x` and `file://u@server/x` fail; ~35 `file:` vectors from the real `new URL`). Divergence: non-ASCII host labels are lower-cased and punycode-encoded without
    the full UTS #46 mapping.
14. **git runs sequentially in upstream's order** (`config --get remote.origin.url`, `rev-parse --abbrev-ref HEAD`, `rev-parse HEAD`,
    as metadata.ts:76-80) under the request context with a 1 s WaitDelay; upstream has no timeout. A result gathered under an ended context is not cached.
15. **The client is left open after `session_shutdown`** (upstream never closes it either); pending batches are awaited with
    the request context, so a cancelled shutdown returns that error after clearing the status.
16. `process.cwd()` for `<cwd>/.pi/langsmith.json` is `os.Getwd()` at factory time (the host starts the extension in the session
    cwd; `ctx.Cwd()` is used for the run metadata as upstream's `ctx.cwd`).

## Open questions for the lead

* `pigeq gaps --go` reports `PARTIAL Context.GetModelInfo` (non-blocking): the model's name/provider come from the host's model
  registry rather than `ctx.model`. The replay twins feed `getModelInfo` from the recorded `ctx.model`; a differential scenario
  under a real host (anthropic model name casing) is the evidence that closes it. Not recorded in `accepted-gaps.json` by me.
* Layer-2 (`pigeq run` scenarios, mutations) belongs to LsHarness / the lead; mutation checks of this lane were not run.
