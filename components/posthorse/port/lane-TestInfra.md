# Lane T (TestInfra): test fixtures

Files (test code only, package `posthorse`): `extensions/posthorse/fixture_test.go` (fixture, host answers, views, events,
tool execution, results/display checks, schema check, `fakePolicy`), `fixture_session_test.go` (fake session manager, entry
builders, session-file writers), `fixture_smoke_test.go` (smoke tests of the fixture against a probe extension; run with
`go test -run Fixture`). Everything is `fx*` / `fix*` / `fake*` / `with*` so it cannot clash with production names.
The full API list is the header comment of `fixture_test.go`; this page adds the porting recipes and the differences.

## Mapping from the TS harness

| TS (test/posthorse.test.ts, harness.ts, results.ts, host-helpers.ts) | Go |
|---|---|
| `setup()` -> `{handlers, tools, toolDefinitions, messages, context}` | `f := newFixture(t, opts...)`; `f.ToolNames()`, `f.Messages()`, the context = fixture state (`f.Set`, `f.With`) |
| `loadFixture(createPosthorse(fixturePolicy))` | `newFixture` calls `newPosthorse(reader)`; default reader = `fixturePolicy` over the fixture's compaction settings (drops keepRecentTokens) |
| `indexedBranch(branch)` | `fxIndexedSession(entries...)` (+ `push`, `pop`, `setEntry`); the default fixture session is one with an empty projection |
| `SessionManager.inMemory(cwd)` | `fxMemorySession(cwd)`; real files: `fxPersistedSession`, `fxOpenSession`, `fxForkSession` |
| `emptyProjection` / `buildSessionProjection: () => ({entries})` | `withEmptyProjection()` / `withProjection(entries...)` / `withRealProjection()` |
| `usageContext(base, window, tokens, reserve, enabled)` | `f.With(withUsage(window, tokens), withReserve(r), withEnabled(e))` |
| `{...context, getContextUsage: () => undefined}` | `f.With(withNoUsage())`; unknown tokens: `withUsageUnknown(window)` |
| `let tokens` mutated between turns | `f.Set(withUsage(window, tokens))` |
| `pageContext(handlers, context)` | `f.PageContext()` |
| `guidanceFor(handlers, context)` | `f.GuidanceFor()` |
| `turnEnd(handlers, context, toolResults)` | `f.TurnEnd(toolResults...)` (returns the BoundaryResult or nil) |
| `automaticResult` / `automaticHandoff` | `f.AutomaticResult(branchEntries, projected...)` / `f.AutomaticHandoff(branchEntries)` |
| `run(tools, name, params, ctx)` | `f.Run(name, params)` / `f.RunErr` (thrown error text) / `f.StartRun().Cancel().Wait()` |
| `notesIn(tools, context)(cwd, params)` | `f.NotesIn(cwd, params)` |
| `toolText(result)` | `res.Text()`; `res.Details` (PosthorseDisplay rec), `res.Images()` |
| `fixtureResult` / `fixtureDisplay` (results.ts) | run through `fxCheckResult` / `fxCheckDisplay` on every `Run` (the test fails like `assert.ok(Check(...))`) |
| `Check(registration.definition.parameters, params)` | `fxSchemaCheck` against the registered schema on every `Run` |
| `handlers.get(name)(event, ctx)` | `f.Fire(name, event)` (FIRST registered handler, like `callbacks[0]`); `f.FireAll` runs every handler |
| `handlers.get("context")(...)` | `f.ContextEvent(messages...)` (nil = undefined) |
| `before_agent_start` with shared options | `f.BeforeAgentStart(prompt, options)` -> `.Result`, `.Sections`, `.Options` |
| `loaded.runtime.sendMessage` capture | `f.Messages()` (`CustomType`, `Content` string, `Display`, `Details`, `Options`, `WindowID()`) |
| `appendEntry` stub / real `appendCustomEntry` | `f.Appended()`; `withAppendToSession()` for the real-session wiring |
| `writeArchive(file, content)` / `archivedId(id, source, legacy)` | `fxWriteArchive` / `fxArchivedID` |
| entry literals | `fxMessageEntry`, `fxCustomMessage`, `fxCustom`, `fxCompaction`, `fxBoundary`, `fxContextWindow`, `fxUser`, `fxAssistant`, `fxToolResult`, `fxToolCall`, `fxText`, `fxImage` |
| `{getCompactionSettings, getContextUsage, getSystemPrompt}` literal (`budgetFor`, history archive tests) | `fakePolicy{Settings, Usage, Prompt}` |
| `SessionManager.getEntry` spy (`reads++`) | `f.Reads("sessionRead.getEntry")`; `getBranch` that throws: `withHostError("sessionRead.getBranch", "...")` |

## Where the Go wire differs from the TS harness (decisions the fixture took)

1. **Context is host state.** `ctx.*` reads are host calls answered from `fixState`. A view's options apply for the whole
   call made through it; two views cannot be active at once (the call is refused); `Set` changes persistent state.
2. **`ctx.cwd` is fixed per host** (the `ready` frame has it once). A view with another cwd starts a second host and
   extension instance for that cwd (shared recorders/tools/state), so extension-internal state is per cwd. `NotesIn` uses
   this. If a twin needs ONE extension instance across cwds, say so: the fixture cannot change `ctx.Cwd()` of a live host.
3. **Two ways to read the branch.** `ctx.SessionManager().GetBranch()` is the wire `sessionRead`; `ctx.GetBranch()` /
   `GetEntries()` read the SDK's replicated log (`watchSessionLog` once, then `state_update` pushes). The fixture answers
   both from one `fakeSession` and pushes a full `state_update` before a call when the session changed. A "must not read
   history" assertion must count BOTH `sessionRead.getBranch`/`getEntries` and `watchSessionLog`.
4. **Event replies are wrapped by the SDK** (`_pigBoundaryEntries/_pigBoundaryResult` for `turn_end`/`agent_before_settle`,
   `_pigPrompt*` for `before_agent_start`, `_pigToolCallResult` for `tool_call`, `_pigContextUnchanged` for `context*`).
   `Fire` unwraps them; `FireWire` returns the raw reply. A `context*` handler that edits messages in place and returns
   nothing yields `{messages: [...]}` (marker `_pigContextUnchanged:true` but the content differs): `ContextEvent`/
   `GuidanceFor` compare with what was sent.
5. **Abort.** `ctx.signal.aborted` is `ctx.Done()`. `StartRun(...).Cancel(reason)` sends the real cancel frame while the
   tool runs. "Signal already aborted when the handler starts" cannot be staged deterministically; use a gated host answer
   (`withHostAnswer`) plus `Cancel`. Events have no handle API yet (ask me; `fxStartRequest`/`cancel` exist for it).
6. **Argument validation.** The fake host does not validate tool arguments; `Run` does it with `fxSchemaCheck` on the schema the
   extension registered (captured from a throw-away registration), at the place `setup()` asserts `Check(...)`.
7. **Tool tokens.** `getAllTools` answers name/description/`parameters` bytes exactly as registered (the register frame),
   `getActiveTools` all registered tools unless `withActiveTools(...)`.
8. **`sdk.WithFileMutationQueue`** is answered (`fileMutation.enter/exit`, FIFO per path); `f.MutationLog()` shows it. The SDK
   helper rides the process-wide "first running extension" connection: never `t.Parallel()` with a fixture.
9. **Host facts.** `newFixture` sets `PIG_EXT_HOST_INFO` (`agentDir` = a temp dir, `configDirName` ".pi") with `t.Setenv`, for
   `sdk.AgentDir()` / `notesDir` (a project containing `.git`). Override with `withAgentDir`.
10. **Unknown host calls** (not `ui.*`, not in the answer table) are counted and fail the test at cleanup with the method name:
    add `withHostAnswer(method, fn)`.
11. Not modelled: `session_before_compact.signal` (use `ctx.Done()`), the `systemMessage` Pi stores on a compaction entry,
    `SessionManager.setSessionFile`/`createBranchedSession`, SettingsManager graph objects.

## Findings for the lead (SDK/host)

- None blocking. Documented difference: the TS fixtures hand each handler a fresh context object; Go has one host state per
  host, so per-call context differences are emulated by views (decision 1) and per-cwd hosts (decision 2).
- The fake host's `Fire` reaches the LAST handler of an event; the fixture reaches the FIRST (TS parity) or ALL.

## Verification

`ph run "cd components/posthorse/extensions/posthorse && go test -count=1 -run Fixture ./..."` (smoke tests: host command and
tool first, execution events, views and `Set`, policy reader, context answers, session reads and counters, replicated log,
first-handler/FireAll, automatic boundary, recorded messages, cancel, per-cwd hosts, file-mutation queue, schema check,
display/result checks, fake session tree/projection/edits, persisted/forked session files, archive helpers).
Also verified in a private module copy with `-race` (the shared tree did not compile while other lanes were mid-edit).
