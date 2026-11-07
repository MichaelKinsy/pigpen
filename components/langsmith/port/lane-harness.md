# Lane LsHarness: mock endpoint, comparator, SDK driver, scenarios

Owns `port/tools/**`, `port/scenarios/`, `port/scenarios-wire/`, `port/golden/`, `port/sdkdiff/`,
`extensions/langsmith/sdkdiff_test.go`. Usage is in `port/tools/README.md`; this file is the lane's contract table, findings and
what is deliberately not done.

## Contract table

| ID | What | Where | Test |
|---|---|---|---|
| H-M1 | Loopback LangSmith endpoint: `/info` (configurable, incl. batch limits, gzip flag, multipart flag), `/runs/multipart` (chunked or content-length, gzip, parts recorded with name, content type, raw header, bytes), `/runs/batch`, `/runs`, `PATCH /runs/<id>`, OAuth discovery and token endpoints; every request logged as JSONL | `tools/internal/mockls`, `tools/cmd/mockls` | `internal/mockls/server_test.go` (11 tests), `cmd/mockls/main_test.go` (2) |
| H-M2 | Failures per route and per call index: status (404, 422, 429 + Retry-After, 5xx), headers, body/json, delay, connection drop | `mockls.Fault` | `TestFaultsPerRouteAndCallIndex`, `TestDelayAndDrop` |
| H-D1 | Request-log comparator with the explicit normalisation list N1-N9 / T1-T2 and a report of which fired | `tools/internal/lsdiff`, `tools/cmd/lsdiff` | `internal/lsdiff/compare_test.go` (incl. `TestMutationsAreReported`: one changed field, one moved key in an extension-built object, a runtime key, a number-vs-string time, a header, framing, part drop/reorder, request count), `cmd/lsdiff/main_test.go` |
| H-S1 | Real vendored SDK driven from op scripts against mockls, request log + console + thrown errors + rewritten files recorded | `tools/sdkdrive.mjs`, `tools/gen/sdkdiff-ops.mjs`, `tools/sdkdiff-regen.sh`, `port/sdkdiff/{ops,golden}` (30 scripts) | `extensions/langsmith/sdkdiff_test.go` `TestSDKDiffGolden` |
| H-L1 | `run-lane.sh`: mockls per scenario and lane, `pigeq lane` for pi / pig-node / pig-go, `lsdiff` on the wire and `pigeq diff` on the host traces | `tools/run-lane.sh`, `tools/lane/materialise.mjs` | run on the oracle lanes (below) |
| H-C1 | Scenarios for `pigeq run|record|check` (43), mockls-only wire scenarios (24) and scenarios every lane must refuse to load (3, with `.expect`) | `port/scenarios`, `port/scenarios-wire`, `port/scenarios-fail`, `tools/gen/scenarios.mjs` | `pigeq lane` of every one under Pi and PiG-Node |
| H-G1 | Pi golden traces of `port/scenarios` (`pigeq record --ts <original>`, host check Pi = PiG-Node passed on all 43, three records byte-identical) | `port/golden/*.jsonl` | `pigeq check` once the port exists |

## Scenario coverage added on the lead's request (2026-11-06)

* `replicas-env`, `replicas-same-project`: two replica destinations drain concurrently, so the wire order of their requests is scheduler order. They
  run with `-unordered-drain` (N9, `lane/lsdiff-flags.txt`); the content of each destination's request, headers (`x-api-key`) included, is still
  compared after sorting, so a wrong key/project/id pairing is not hidden (pi, pig-node and the lead's pig-go lane: identical).
* `config-home-file` now writes `$HOME/.pi/langsmith.json` with a `setup` command (HOME is the lane's home on every lane), so Pi, PiG-Node and the
  port all see the same global file.
* Fallbacks: `config-pi-prefix-wins`, `config-metadata-fallback`, `config-langchain-endpoint-key` (SDK's LANGCHAIN_* names), `config-empty-project`
  (`??` keeps an empty `LANGSMITH_PI_PROJECT`), `replicas-sdk-env-array` (earlier).
* Must refuse to load (`scenarios-fail`, `.expect` regexp checked in every lane's output by run-lane.sh): `config-runs-endpoints-object-form`,
  `config-runs-endpoints-fallback-object-form` (message must name `replicas`), `config-sampling-rate-out-of-range` (must name the sampling variable).
* Provider payloads (a pigeq `model` server serving the provider's own stream format; wire-only because the server's recorded request body holds
  the host's system prompt): `payload-gemini-tool-call` (contents/parts, functionCall, functionResponse), `payload-openai-responses-tool-call` (`input`
  items, function_call), `payload-anthropic-tool-call` (system blocks, tool_use, relabelled tool_result).
* Errors: `error-provider-rejects` (provider 400: stopReason error + errorMessage on llm run, turn and root), `error-aborted-stream` (abort while the model
  streams: stopReason aborted), `abort-running-tool`, `shutdown-while-running` (session_shutdown ends open runs: 'Pi session shut down before run
  completed'), `compaction-overflow` (provider overflow error; see below), `compaction-manual`.
* `trace-tool-update`: a streaming bash tool, tool_execution_update -> `tool_update` events (the same number of events in Pi and PiG on every run).
* Host differences handled by N8 on the Pi pair and found with these scenarios: Gemini `config.abortSignal` (Pi serialises `{}`, PiG omits it), `systemInstruction` / Anthropic `_raw`
  system block, `tokensBefore` and `context_usage` estimates. Pi's Gemini tool-call ids embed `Date.now()` (`read_<ms>_<n>`): a per-scenario `-rule`.
* **Not reachable with pigeq**: `interrupted-by-next-run` ('Trace replaced by a new Pi run') needs a `before_agent_start` while the previous run has not
  settled, i.e. a second extension that prompts from its own `agent_settled` (Pi < 0.87 order) or a host that starts the next prompt before
  `agent_settled`; Pi 1.5.0 and PiG settle first and a follow-up prompt joins the running loop. Covered by upstream's `late-settle` twin in LsExt.
  Context-overflow compaction depends on the provider error text matching the host's overflow patterns (see the scenario description).

## Findings (about the original and its SDK, found while building the evidence)

* **F-H1, `LANGSMITH_RUNS_ENDPOINTS` in the SDK's object form breaks the extension.** `getVar("RUNS_ENDPOINTS")` falls back to the
  unprefixed `LANGSMITH_RUNS_ENDPOINTS`; the SDK documents `{ "<url>": "<key>" }` for it, but the extension's schema wants an array:
  `Failed to load extension ... expected array` under both Pi and PiG-Node (verified with pigeq). The array form works (scenario
  `replicas-sdk-env-array`; `project_name` is not a replica field of the extension and is ignored). Not a pigeq scenario because the host
  fails to start; the Go port must refuse the same way (config error from the factory).
* **F-H2, the 422 "buffered retry" of `_sendMultipartRequest` is unreachable.** `raiseForStatus` throws on 422, so the
  `res.status === 422` branch never runs: one request, a `console.warn` (`Failed to Failed to send multipart request. Received status
  [422]: ...` followed by `Context: trace=...,id=...`), the batch is lost and streaming stays on. Golden `multipart-422-buffered`, scenario
  `wire-422-buffered-retry`. Port the defect.
* **F-H3, a failing `GET /info` is retried on every run operation and shutdown can take over a minute.** Golden `info-unavailable`: 15 info
  requests for one run; under pigeq the host did not exit within 30 s of stdin close (`awaitPendingTraceBatches` waits for the retries). No
  pigeq scenario (the lane fails); the sdkdiff golden covers it.
* **F-H4, `length=` of a multipart part is the UTF-8 byte length** of the serialised part (`_serializeBody` returns bytes), not
  `String.length`. lsdiff checks it on every part (`body problems`).
* **F-H5, host differences visible in the traces**: Pi 1.5.0 and PiG put different text into the system message (name, doc paths); the
  assistant message carries a measured `durationMs`. lsdiff has N2 for `durationMs`/`timestamp` and the opt-in N8 for the system prompt of a
  Pi-versus-PiG pair; `pig-node` versus `pig-go` (same host) needs neither.
* **F-H6, `~/.pi/langsmith.json` is read from `process.env.HOME`.** pigeq's `homeFiles` write to `<PIG_HOME>` on a pig lane, so the original
  under PiG-Node would not see the user-level file. Scenario `config-home-file` writes it with a `setup` command under the lane's HOME instead
  (all lanes, same path); the Go port must read `$HOME/.pi/langsmith.json` like the original to be identical there.
* **F-H7, behaviours that are not pigeq-native scenarios** (they are `scenarios-wire`, compared by lsdiff, because the pigeq trace cannot
  hold them): numbers inside a decoded JSON body (`/runs/batch`: `end_time`) are outside pigeq's string normalisation; two upstream
  requests race (the OAuth refresh against the model request, one request per replica); the end of an aborted run differs between Pi and
  PiG in the trace body (lsdiff: identical). Excluded entirely: parallel tool calls in one turn (end order differs between hosts) and a
  `size_limit` flush inside a pigeq run (batch composition depends on host timing; the JS-SDK-level script `size-limit-flush` replays the
  same ops through the Go port deterministically).
* **F-H8, the host check needs the agent/turn/message lifecycle out of `capture`**: where the extension's own requests (`GET /info`, the
  multipart POST) fall among those events is host output pacing and differs between Pi and PiG. Scenarios capture `response`,
  `extension_ui_request`, `extension_error`, `tool_execution_*` only; the extension's UI calls (status/notify), its tool-visible effects and
  its requests are in the trace.
* **F-H9, `pigeq gaps --ts` on the original**: `PARTIAL ctx.model` (stand-in `Context.GetModelInfo`) and `metadata.js` runs `git`
  (declared in every scenario's `commands`).

## Wire facts the other lanes can use (all from the goldens)

* Multipart request: `Content-Type: multipart/form-data; boundary=----LangSmithFormBoundary<base36>`, `Transfer-Encoding: chunked`
  (content-length when `LANGSMITH_DISABLE_MULTIPART_STREAMING=true`), parts `post.<id>`, `post.<id>.inputs|outputs|extra|serialized`, each
  `Content-Type: application/json; length=<bytes>`; a create and an update of the same run in one batch merge into one `post.<id>` set.
* `GET /info` is the first request; `/runs/batch` after a 404 on `/runs/multipart` (and stays off); autoBatchTracing off posts `POST /runs`
  and `PATCH /runs/<id>`.
* `extra.runtime`: `library`, `runtime`, `sdk`, `sdk_version` (+ release shas when present).

## Evidence (oracle only; the Go lanes were not available to this lane)

* `go vet` + `go test -race` in `port/tools`: 4 packages, 28 tests (mockls 12 + 2, lsdiff 12 + 2) pass.
* `pigeq lane` of the original under Pi and under PiG-Node against mockls, all 43 + 24 scenarios: wire **identical after normalisation**
  (strict lsdiff; N8 on for the Pi pair, N9 on the replica scenarios, one `-rule` on the Gemini one) on all of them; `pigeq diff` of the host traces
  identical except `shutdown-while-running` (PiG reports an `extension_error` in its own turn_end handling at shutdown) and the scenarios with a model server
  (skipped for the Pi pair: the model server's recorded requests hold the host prompt). The 3 `scenarios-fail` are refused in both lanes.
* `pigeq record --ts` (Pi) on `port/scenarios`: 38/38 pass the host check (pig-ts == pi-ts); recorded twice, goldens byte-identical.
* sdkdrive: 30 op scripts against the vendored SDK 0.10.5; rerun JS vs JS through lsdiff: 30/30 identical.
* sdkdiff replay through the Go port (`TestSDKDiffGolden`, tree of 2026-11-06): 30 of 30 pass. `size-limit-flush` first differed only in the
  wire order of the three concurrent batches of one drain (scheduler order: microtask depth in the SDK, goroutines in Go; agreed with LsClient
  that it is no contract): lsdiff rule N9 (`-unordered-drain`, per-script `unorderedDrain`) compares such a drain as a multiset.
* Upstream vitest suite on the oracle (`@langchain/langsmith-pi-extension` 0.2.1 at b0d83c3, pnpm 10.33.0 frozen lockfile, vitest 5.0.2,
  Pi 0.87.1 packages): **25 passed, 1 skipped (`replay.test.ts`: needs a recorded model), 0 failed** in 8 files (needs a git repository:
  `contract.test.ts` asserts `git_branch`).
