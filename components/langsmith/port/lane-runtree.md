# Lane notes: LsRunTree (runtree.go)

Port of `run_trees.js` `RunTree` of langsmith 0.10.5 for what the extension reaches, plus everything those paths read
from the environment (`LANGSMITH_RUNS_ENDPOINTS`, `LANGSMITH_/LANGCHAIN_EXCLUDE_INPUTS_ON_PATCH`, the project
variables, `LANGSMITH_ENDPOINT` for the conflict check).

Files: `runtree.go`, `runtree_test.go`, `runtree_golden_test.go`, `testdata/runtree_cases.json` (103 scenarios),
`testdata/runtree_golden.json` (what the real JS produced), generator `port/tools/gen/runtree-golden.mjs`,
`port/red/runtree.log`.

Regenerate (the container does not write back into the synced tree, so the output goes to scratch):

    ls run "RUNTREE_OUT=/home/kinsy/ls-work/scratch/runtree node components/langsmith/port/tools/gen/runtree-golden.mjs"
    scp xd6702:ls-work/scratch/runtree/runtree_\*.json components/langsmith/extensions/langsmith/testdata/

## API (changes against the contract stub, all announced to LsExt/LsHarness)

| Go | Meaning |
|---|---|
| `newRunTree(cfg) (*runTree, error)` | `new RunTree(config)`; errors: `Only one replica can be marked as primary.`, `conflictingEndpointsError` |
| `(*runTree).createChild(cfg) (*runTree, error)` | error only where the constructor throws (two primaries in `cfg.Replicas`) |
| `end(outputs, errMsg, metadata)`, `endAt(outputs, errMsg, endTimeMs *int64, metadata)` | `RunTree.end`; first value wins |
| `addEvent(name, kwargs)`, `addEventMessage(msg)` | `addEvent({name, kwargs})`, `addEvent("msg")` |
| `postRun(ctx)`, `postRunWith(ctx, excludeChildRuns)` | never returns an error (logged) |
| `patchRun(ctx) error`, `patchRunWith(ctx, excludeInputs *bool) error` | error only from the replica branch (finding F1) |
| `toJSON()` | `_convertToCreate(this, undefined, false)` |
| `runTreeConfig` | + `ID`, `StartTimeMs *int64`, `TraceID`, `DottedOrder`, `ParentRunID`, `Extra`, `DefaultProject`; `Client` is the interface `runClient` (`*client` satisfies it) |
| `replicaConfig` | + `WorkspaceID`, `Primary *bool`, `Reroot *bool`, `Client runClient`. The tuple form `[name, updates]` is `replicaConfig{ProjectName, Updates}` |
| `runTreeConfig.ProjectName` | used verbatim, `""` included (`LANGSMITH_PI_PROJECT=""` reaches the SDK as an empty project); `DefaultProject: true` is the SDK default (`getDefaultProjectName`) |
| `runTreeConfig.Name` | used verbatim (an absent `name` is not expressible; the extension always names) |
| `runTreeConfig.Inputs` | `nil` absent (`{}`), `undef` explicit undefined, `inputsNull` JSON null |

Test seams (package vars): `rtNow` (`Date.now`), `rtRuntimeEnv` (`getRuntimeEnvironment`). Console text goes through
`warnf`/`errorf`/`warnOnce` of client.go (`swapStderr` in tests). `runTree` is not safe for concurrent use (the JS is
single-threaded; the extension's handlers serialise).

## Contract table

| ID | Upstream | Go | Test |
|---|---|---|---|
| RT1 | run_trees.js:324-370 constructor (defaults, deduped metadata, id from start time, trace_id, dotted_order, execution orders) | `buildRunTree` | `TestRunTreeGolden` (root basic/defaults/extra/dedupe/dotted order given/trace id given), `TestRunTreeGeneratedIDs` |
| RT2 | utils/project.js getDefaultProjectName | `getDefaultProjectName` | golden "default project ..." (6 cases) |
| RT3 | run_trees.js:404-468 createChild (metadata inheritance, replicas inheritance minus reroot, child_execution_order propagation, child_runs, context vars) | `createChild`, `stripReroot` | golden "children tree", "metadata inheritance*", "execution order *", "child inherits replicas strips reroot", `TestStripReroot` |
| RT4 | run_trees.js:469-478 end | `endAt` | golden "end *" (6 cases) |
| RT5 | run_trees.js:838-855 addEvent | `addEvent`, `addEventMessage` | golden "events*" |
| RT6 | run_trees.js:479-523 `_convertToCreate` (key order, runtime merge, child_runs) | `convertToCreate` | every createRun payload; "extra runtime *" (5 cases), "toJSON", "postRun excludeChildRuns false" |
| RT7 | run_trees.js:524-570, 571-693 `_sliceParentId`, `_setReplicaTraceRoot`, `_remapForProject`, `getReplicaKey` | `sliceParentID`, `setReplicaTraceRoot`, `remapForProject`, `getReplicaKey`, `uuidV5` | golden "replica *", "reroot *" (6 cases), `TestUUIDV5` |
| RT8 | run_trees.js:694-737 postRun | `postRunWith`, `tryPostRun` | golden "postRun failure*", replica cases |
| RT9 | run_trees.js:137-140, 750-830 patchRun, getExcludeInputsOnPatch | `patchRunWith`, `nonReplicaUpdate`, `getExcludeInputsOnPatch` | golden "patch excludeInputs env *" (13 cases), "patchRun failure*", "patch without post*" |
| RT10 | run_trees.js:990-1068 `_getWriteReplicasFromEnv`, 1086-1091 `_checkEndpointEnvUnset`, utils/error.js ConflictingEndpointsError | `getWriteReplicasFromEnv`, `conflictingEndpointsError`, `rtParseJSON` (ordered `JSON.parse`) | golden "env replicas *" (19 cases), `TestRtParseJSON` |
| RT11 | run_trees.js:1069-1085 `_ensureWriteReplicas` | `ensureWriteReplicas` | golden "two primaries *", "one primary one false", `TestEnsureWriteReplicas` |

The golden test replays one scenario file through both implementations and compares byte for byte: every payload given
to `createRun`/`updateRun` (`JSON.stringify` vs `stringify`), the options object, every `console.warn/error` line,
every thrown error (name and message) and the final state of every run (ids, orders, extra, events, replicas,
child ids). 103 scenarios, 313 client calls. Run ids the JS generated (random low bits) are normalised on both sides;
declared ids are compared exactly.

## Findings (defects/quirks of the original, ported)

* **F1** `patchRun` with replicas has no try/catch (run_trees.js:752-797): a failing `updateRun` rejects, `child_runs`
  is not cleared, and `safeEnd` throws out of the extension handler. Without replicas the failure is only logged
  (`Error in patchRun for run <id> <error>`, no colon). `postRun` always catches. Port: `patchRun` returns the error
  only from the replica branch. LsExt decides what the handler does with it.
* **F2** `_convertToCreate` mutates `run.extra` in place (adds `runtime`), and the payload's `extra`, `events`,
  `serialized` are the run's own objects. With replicas, a `patchRun` of a run that was never posted adds
  `"runtime":{}` to the payload (golden "patch without post replicas"); without replicas it does not.
* **F3** `execution_order` is rendered with `toFixed(0).slice(0, 3)`: from the 1000th run of a trace on, the dotted-order
  suffix is truncated (`1001` -> `100`), so sibling ordering ties (golden "execution order three digits").
* **F4** `_checkEndpointEnvUnset` runs only for the object form of `LANGSMITH_RUNS_ENDPOINTS`; the array form coexists
  with `LANGSMITH_ENDPOINT`. `LANGSMITH_RUNS_ENDPOINTS` has no `LANGCHAIN_` fallback. An empty object never conflicts.
* **F5** An array item that is `null` warns `expected object, got object`; `primary: null` warns
  `expected boolean, got object`; an array item that is an array warns about `api_url`.
* **F6** Children never re-read the environment: they inherit the parent's list, which is `[]` when the parent had none.
  `replicas: []` on a root suppresses the environment.
* **F7** `getExcludeInputsOnPatch` and `getDefaultProjectName` go through `getLangSmithEnvironmentVariable`
  (`LANGSMITH_X || LANGCHAIN_X`), so `LANGSMITH_X=""` with `LANGCHAIN_X` unset is undefined (default applies) but
  `LANGCHAIN_X=""` alone is `""` (inputs ARE sent on patch; `LANGCHAIN_PROJECT=""` is the project `""`). Covered by
  golden env cases; LsWire's `getLangSmithEnvironmentVariable` matches them.
* **F8** `reroot` context variables (`_REPLICA_TRACE_ROOTS_KEY`) live in an object a child shares with its parent once the
  parent has one (`child[KEY] = this[KEY]`): ported as a shared `*rtContextVars`.
* **F9** The replica `updates` spread after the base keys can overwrite any PATCH key (id, name, ...) and is applied to
  PATCH only; `inputs` is added after it when not excluded.
* **F10** `child_runs` keeps every child alive until the parent's `postRun`/`patchRun` clears it (memory, as in JS).

## Divergences and non-ports (named)

* `console.error(msg, error)` prints the error with its stack in node; Go prints `Name: message` (`errString`, client.go).
* The runtime values (`runtime`, `sdk`, `sdk_version`) are the sender's (brief D1): the golden test injects the JS
  values through the `rtRuntimeEnv` seam.
* Not ported, not reachable from the extension or its zod config: `lc:child_config` callback glue of `createChild`,
  `_awaitInputsOnPost`, `fromRunnableConfig`, `fromHeaders`/`toHeaders`/Baggage, the constructor options
  `serialized`, `reference_example_id`, `attachments`, `end_time`, `caller_options`, `api_url`, `api_key`,
  `tracingEnabled`.
* Dotted-order segments are sliced by byte (they are ASCII for every id the SDK makes); a non-ASCII user dotted order
  would differ from JS UTF-16 slicing. A truthy non-object `extra.runtime` is left alone (JS: a string throws a
  TypeError inside postRun, an array gets a property JSON cannot show).
* `ParentRunID`/`DottedOrder`/`TraceID` config (fromHeaders-style) are supported; `distributedParentID` is set by tests.

## Open questions for the lead

None blocking. LsExt: a typed-nil `*client` assigned to `runTreeConfig.Client` is a non-nil interface (and would be
called); pass an untyped nil to get the shared client.

## Numbers (xd6702, 3 CPUs, Xeon 8468)

`BenchmarkRunTreeLifecycle` (root + child, post/end/patch both): about 15 us, 10 KB, 105 allocs per op.
`BenchmarkRunTreeReplicaPayload` (post + patch of a rerouted replica run): about 7.7 us, 6 KB, 70 allocs per op.
