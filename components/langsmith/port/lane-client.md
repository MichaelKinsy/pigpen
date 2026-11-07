# Lane LsClient: client*.go

Upstream: langsmith 0.10.5 `client.js` (constructor, getDefaultClientConfig, headers/auth, createRun, updateRun,
processRunOperation, drainAutoBatchQueue, _processBatch, _ensureServerInfo, batchIngestRuns, multipartIngestRuns,
_sendMultipartRequest, awaitPendingTraceBatches, prepareRunCreateOrUpdateInputs, mergeRuntimeEnvIntoRun, failed-trace
fallback dir, AutoBatchQueue), `utils/fast-safe-stringify` (serialize, estimateSerializedSize), `singletons/fetch.js`
(debug logging), `utils/env.js` resolveTracingMode (LsWire's `resolveTracingMode` is used).
Files: `client.go` (stderr helpers, options, constructor, headers/auth, createRun/updateRun), `client_prepare.go`
(prepare/merge/estimate), `client_queue.go` (queue, timer, drain, workers, /info, awaitPending, close),
`client_ingest.go` (processBatch, multipart assembly/send, /runs/batch, fallback dir), `client_jsutil.go` (JS value
helpers), tests `client_test.go client_units_test.go client_golden_test.go client_bench_test.go`, vectors
`testdata/client_{scenarios,golden,units}.json`, generators `port/tools/gen/client-differential.mjs`,
`client-units.mjs`, `client-bench.mjs`, red log `port/red/client.log`.

## Contract

| ID | Upstream | Go | Test |
|---|---|---|---|
| K1 | client.js:519-912 constructor, 913-947 getDefaultClientConfig (every env var, profile, trimQuotes, trailing slash, concurrency 5, timeout 90 s, hide flags, TRACING_BACKGROUND, DISABLE_*, FAILED_TRACES_*, DEBUG) | `newClient`, `getDefaultClientConfig` | TestNewClientConfig, TestTrimQuotesVectors, golden quoted-values / langchain-fallback-env / profile-* / env-metadata |
| K2 | client.js:77-88 getTracingSamplingRate (parseFloat, NaN passes, range error) | `getTracingSamplingRate`, `jsParseFloat` | TestJSParseVectors (60 engine vectors), TestNewClientConfig, golden sampling-* |
| K3 | env.js:224-246 resolveTracingMode; OTEL refused | `newClient` returns an error naming the option | TestNewClientConfig (otel cases) |
| K4 | client.js:997-1045 `_mergedHeaders`, 332-466 `_fetch` / auth precedence (x-api-key, x-tenant-id, profile Authorization, explicit headers) | `mergedHeaders`, `fetch`, `applyCurrentAuthHeaders` | golden profile-*, apikey-replica-direct, profile-oauth-replica-key-* |
| K5 | client.js:1179-1273 processInputs/Outputs/Metadata/Error, `_filterNewTokenEvents`, prepareRunCreateOrUpdateInputs; 1581-1599 mask | `prepareRunInPlace`, `filterNewTokenEvents`, `maskRunMetadata` | TestPrepareRunInPlace*, TestFilterNewTokenEvents, golden hide-*, multipart-update-only-and-events |
| K6 | client.js:47-76 mergeRuntimeEnvIntoRun | `mergeRuntimeEnvIntoRun` | TestMergeRuntimeEnvIntoRunVectors (14 real-SDK vectors), golden env-metadata |
| K7 | client.js:1360-1368 `_shouldSample` / `_filterForSampling` | `shouldSample`, `samplingIdentifier` | TestSamplingIdentifier, golden sampling-half |
| K8 | client.js:1727-1775 createRun, 2137-2218 updateRun (direct and batched, blockOnRootRunFinalization) | `createRun`, `updateRun`, `postJSON` | golden direct-*, block-on-root*, TestCreateRunDirect*, TestUpdateRunDirectBodyHasNoID |
| K9 | client.js:228-327 AutoBatchQueue; fast-safe-stringify:60-261 estimateSerializedSize | `autoBatchQueue`, `estimateSerializedSize` | TestAutoBatchQueueVectors (6 real-class sequences), TestEstimateSerializedSizeVectors (36), golden queue-size-drop (exact byte counts) |
| K10 | client.js:1600-1628 processRunOperation (250 ms timer reset by every op, limits from /info) | `processRunOperation`, `afterLimits`, `timerFired` | golden timer-reset-single-batch, timer-two-batches, limit-* |
| K11 | client.js:1388-1432 drainAutoBatchQueue (destination key `apiUrl|apiKey|workspace`) | `drainLocked`, `worker`, `jobDone` | golden replica-destinations, no-api-key-quirk, TestBatchWorkersAreBounded |
| K12 | client.js:1629-1680 `_getServerInfo` / `_ensureServerInfo` (10 s, retries, warn text, re-probe) | `withServerInfo`, `probe`, `getServerInfo` | golden info-500-reprobe (32 requests), info-404, info-bad-json, info-recovers, TestSingleFlightServerInfoAndOneBatch |
| K13 | client.js:1490-1552 `_processBatch` (multipart vs batch, gzip flag, 404 fallback and multipart disabled) | `processBatch`, `exportBatch` | golden multipart-404-fallback, batch-endpoint-from-info, gzip* |
| K14 | client.js:1866-1994 multipartIngestRuns (part names, fields split, merge by id, validation, attachments) | `buildMultipart`, `mergeCreatesAndUpdates`, `addAttachments` | golden multipart-*, TestMergeCreatesAndUpdates, TestAttachments, TestMultipartRejectsRunsWithoutTraceFields |
| K15 | client.js:1995-2136 body assembly, streamed vs buffered, gzip, retry, 404 rethrow, warn text | `multipartBody`, `bodyReader`, `sendMultipartRequest` | golden gzip*, streaming-disabled-buffered, multipart-4xx/5xx, TestBodyReader*, TestLargeBodyStreamsAndGzipsIntact |
| K16 | client.js:1780-1861 batchIngestRuns / `_postBatchIngestRuns` | `batchIngestRuns` | golden batch-endpoint-from-info, batch-endpoint-400 |
| K17 | client.js:1445-1489 `_writeTraceToFallbackDir` | `writeTraceToFallbackDir` | golden failed-traces-dir, failed-traces-over-budget, TestFailedTrace* |
| K18 | client.js:5634-5674 awaitPendingTraceBatches | `awaitPendingTraceBatches`, `waitUntil` | all golden scenarios, TestAwaitPendingHonoursContext, TestManualFlushMode |
| K19 | client.js:1708-1715 flush; manualFlushMode | `flush` | TestManualFlushMode |
| K20 | fetch.js debug logging, the constructor and `_getServerInfo` debug output | `fetch`, `getServerInfo` | golden debug-logs |
| K21 | fast-safe-stringify:263-306 serialize | `serializeForTracing` | TestUnserializableValueIsReplaced |
| K22 | (Go) bounded goroutines, close | `close`, workers, timers | TestCloseStopsTimersAndGoroutines, TestCloseCancelsInFlightRequests, TestBatchWorkersAreBounded |
| K23 | client.js:2063, async_caller.js:73-79 ingest budget (`maxIngestMemoryBytes`) per batch | `processBatch`, `goSend` | golden ingest-budget-refused-* |
| K24 | pooled body ownership (Go), HTTP/1.1 (Node fetch) | `newRawBody`, `multipartBody.release`, transport setup in `newClient` | TestEarlyResponseDoesNotRaceThePooledBody, TestRawBodyReleasedOnLastClose, TestTransportSpeaksHTTP11OverTLS |

Differential evidence: `client-differential.mjs` drives the real `Client` against a loopback server with 55 scenarios
(~165 requests) and records requests (method, path, selected headers, framing, parsed multipart parts, gzip decoded), console
output and the failed-trace directory; `TestClientDifferential` replays them on the Go client and compares. Canonicalised only:
User-Agent, `runtime`/`sdk`/`sdk_version` values (D1, and the `length=N` of the parts that contain them, after verifying the
declared length on both sides), multipart boundary, temp paths and timestamps in file names, the JSON parse-error text of
/info, the order of concurrent batches in the scenarios marked `unordered`.
Run: `-race -count=3` ok; vet linux/darwin/windows ok.

## Design

* One mutex `client.mu` replaces the event loop. `createRun` runs the synchronous half of `processRunOperation` on the
  caller's goroutine (clear timer, merge env, estimate, push). The async half (two `await _ensureServerInfo()`, thresholds,
  timer) is a callback: inline when `/info` is cached, otherwise run by the probe goroutine in registration order. A failed
  probe resets the flight before the callbacks run, as the promise handlers of the original do, so the re-probe counts match
  the original (golden info-500-reprobe: 32 requests).
* Workers: at most `traceBatchConcurrency` (5) goroutines drain a FIFO of destination groups (assembly only; sends run
  on the budget-bounded goroutines described below) and exit when it is empty;
  the probe is one goroutine at a time; each live timer is one `time.AfterFunc` (stopped by `close`). `close()` stops
  timers, cancels the client context (in-flight requests), waits for every goroutine, closes idle connections.
  Queued, unsent items are abandoned: call `awaitPendingTraceBatches` first. Background work uses the client's own
  context, never an `sdk.Context`.
* Allocation: payloads are `*omap` serialised with `appendValue` into pooled buffers. The multipart body is written once,
  in wire form, into one pooled buffer; the chunked stream, the buffered body, the gzip stream (pull style, 32 KiB steps,
  pooled `gzip.Writer`) and the failed-trace file all read it through a segment cursor. A part's header is appended after its
  payload (it carries the payload length) and the segment list restores the wire order. The buffer goes back to the pool when
  the last body reader is closed (the transport may close it after `RoundTrip` returned). `prepareRunInPlace` and the
  create/update merge work in place on the items the batch owns (idempotent, nested values are replaced, never mutated).
* Request bodies: every pooled body (multipart, `/runs/batch`, direct `/runs` and PATCH) is a refcounted `multipartBody`
  (`newRawBody` for the JSON ones); the buffer returns to the pool only after the owner released it AND every reader handed
  to the transport was closed (net/http may read or close a body after `RoundTrip` returned when the server answers before
  reading it: review finding, `TestEarlyResponseDoesNotRaceThePooledBody` with an 8 KiB socket buffer and a 3 MiB body,
  effective under `-race`).
* Ingest budget: `processBatch` runs on a worker (server-info wait + body assembly); the send, i.e. the
  `batchCaller.callWithOptions({sizeBytes})` call that reserves the batch's bytes against `maxIngestMemoryBytes`, runs on a
  goroutine of its own (`goSend`). Every assembled batch reaches the budget check at once, exactly as `Promise.all` of
  `_processBatch` does in the original, and is refused with `Queue size limit (N bytes) exceeded. Current queue size: X bytes,
  attempted addition: Y bytes.` when it does not fit. The goroutines that exist are the ones the budget admitted plus refused
  calls that return at once. Golden: `ingest-budget-refused-multipart`, `ingest-budget-refused-batch-endpoint` (real JS vs Go,
  same requests and refusals).
* Transport: HTTP/1.1 only (`ForceAttemptHTTP2=false`, empty `TLSNextProto`, `Protocols` = HTTP/1 only), like Node 24 fetch
  (chunked multipart over TLS too): `TestTransportSpeaksHTTP11OverTLS` (with a control that the server would negotiate h2).
* `createRun`/`updateRun` take ownership of the top-level `*omap` (the original mutates it too).
* Size estimate: exact port of `estimateSerializedSize`; a value it cannot classify is measured by serialising it (the
  original's catch branch).

## Benchmarks (xd6702, tern-ci, 3 CPUs, `-benchmem`)

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| CreateRunEnqueue (prepare+merge+estimate+push, sends included) | 26,258 | 26,791 | 28 |
| MultipartAssemble (100 runs, create+update merged, 335 KB body) | 1,270,868 | 973,528 | 42 |
| EndToEnd (1000 runs create+update to a loopback server) | 42,155,294 | 20,624,119 | 155,363 |
| EstimateSerializedSize (one typical run) | 3,776 | 0 | 0 |

Real JS client, same workload (`client-bench.mjs`, node 24): 261-296 ms per 1000 runs, RSS 76 MB -> 339-349 MB peak:
the Go client is about 6x faster for the same work.

## Findings (defects and quirks of the original, ported)

* C1 `_sendMultipartRequest` (client.js:2110-2120) retries with a buffered body when `res.status === 422`, but `res` only
  exists after `raiseForStatus` accepted it, so the branch can never run: a 422 is a failure (warn text, failed-trace file).
  Not ported (dead code); golden multipart-422-warn shows the real behaviour. The brief's "422 buffered retry" does not exist.
* C2 In a batch, `isDefault` (client.js:1403-1405) compares `item.apiUrl === this.apiUrl`, which is never true for an item
  without replica options, so every batch takes the `${apiUrl}|${apiKey}|${workspaceId ?? ""}` key and `options.apiKey`
  is the string `"undefined"` when the client has no API key of its own: the multipart request carries
  **`x-api-key: undefined`** (batched ingestion with a CLI-profile API key or OAuth profile sends that header, plus the
  profile `Authorization` for OAuth; the /info probe is right). A `|` inside the key, URL or workspace splits the key.
  Golden profile-api-key, profile-oauth-batched, no-api-key-quirk.
* C3 A failed `/info` probe is not cached: every `processRunOperation` (twice) and every `_processBatch` probes again,
  each with 4 retries (1+2+4+8 s) and its own warning (golden info-500-reprobe: 32 requests for 2 runs).
* C4 `size_limit_bytes < 1` makes `pop` throw ("Number of bytes to pop off may not be less than 1."): in the timer path that is
  an uncaught exception in Node (process crash); the port prints the error and carries on.
* C5 With both creates and updates in a batch, a create without a truthy `id` is dropped by the `createById` reduce
  (client.js:1788, 1903), silently.
* C6 `prepareRunCreateOrUpdateInputs` runs twice for every run (in createRun/updateRun and again in the batch): the
  hide flags are idempotent, an anonymizer function would run twice. `processRunOperation` merges the runtime and env
  metadata after createRun masked metadata; the batch masks again, so hideMetadata still ends as `{}`.
* C7 Gzip applies only to the streamed body (client.js:2077-2082): with DISABLE_MULTIPART_STREAMING no compression is sent
  even when the server enables it. Attachments of an update are ignored; a string attachment sends `length=undefined`.
* C8 `LANGSMITH_TRACING_SAMPLING_RATE=abc` parses to NaN, passes the range check, samples nothing and writes
  `ls_tracing_sample_rate: null`.
* C9 `AutoBatchQueue.push` drops the run but processRunOperation carries on; the item resolves at once.
* C10 handle429 never fires (raiseForStatus errors carry no response; see lane-caller.md).

## Decisions and divergences

* D2 `console.log` (LANGSMITH_DEBUG lines: API URL, `→ METHOD url`, `← status text url`, server configuration) goes to
  stderr: stdout is the host channel.
* `console.error(err)` prints `Name: message` without a stack (and without the inspected properties) in the Go port.
* The JSON parse error text of a bad `/info` body is the Go decoder's, not V8's.
* The HTTP client is a dedicated transport cloned from `http.DefaultTransport`: it honours HTTP(S)_PROXY/NO_PROXY
  (`ProxyFromEnvironment`); Node 24's global fetch ignores them unless NODE_USE_ENV_PROXY is set. Lead's decision if exact
  parity is wanted (one line: `tr.Proxy = nil`).
* Request header names are Go-canonical (`X-Api-Key`); HTTP header names are case-insensitive.
* Client options not reachable from the extension are not ported: custom `headers`, `fetchOptions`, `anonymizer` / function
  `hideInputs`, prompt cache, every API method other than createRun/updateRun/awaitPendingTraceBatches/flush. `clientOptions`
  exposes the constructor options of the ported part (sampling rate, timeout, concurrency, limits, hide flags, manual flush,
  tracing mode, aggregation delay) for tests and embedders.
* A boolean `hideInputs`/`hideOutputs`/`hideMetadata` only (env `true`); the extension never sets a function.
* The extension's recommended shutdown order: `awaitPendingTraceBatches(ctx)` then `close()`.
