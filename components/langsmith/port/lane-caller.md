# Lane LsCaller: caller.go

Upstream: langsmith 0.10.5 `utils/async_caller.js`, `utils/p-retry/index.js`, `utils/is-network-error/index.js`,
`utils/p-queue.js` (p-queue 6.6.2 `dist/index.js`), `utils/error.js`.
Files: `caller.go`, `caller_test.go`, `caller_golden_test.go`, `testdata/caller_golden.json`,
`port/tools/gen/caller-differential.mjs`, `port/red/caller.log`.

## Contract

| ID | Upstream | Go | Test |
|---|---|---|---|
| C1 | async_caller.js:3-11 STATUS_RETRYABLE | `statusRetryable`, `isStatusRetryable` | TestStatusRetryableTable, golden "retryable/terminal *" |
| C2 | async_caller.js:62-69 constructor defaults (maxRetries 6, concurrency Infinity) | `newAsyncCaller` (`MaxRetries<0` = 6, `MaxConcurrency<=0` = Infinity, `MaxQueueSizeBytes<0` = undefined) | TestNewAsyncCallerDefaults, TestRetryCountsAndDelays, golden "default retries (6)" |
| C3 | async_caller.js:73-79 byte budget error text | `callWithOptions` | TestQueueSizeLimit, TestQueueSizeLimitEdges |
| C4 | async_caller.js:81-83,107-111 size accounting, released on success/failure | `callWithOptions`/`finish` | TestQueueSizeLimit*, TestCancelRaceKeepsSlots, TestPanicInCallReleasesSlot |
| C5 | async_caller.js:88-122 onFailedAttempt (Cancel/TimeoutError/AbortError message prefix, name TimeoutError, code ECONNABORTED, hook, status check) | `rethrow` | TestNoRetryClassification, TestFailedResponseHook, TestResponseCarryingError |
| C6 | p-retry/index.js:46-51 calculateDelay | `retryDelay` | TestRetryDelay, TestCallerGoldenDelay (420 vectors from the real p-retry) |
| C7 | p-retry/index.js:54-95,119-150 onAttemptFailure / retry loop (retries+1 attempts, retriesLeft, non-network TypeError thrown, backoff sleep) | `retry` | TestRetryCountsAndDelays, TestCallerGoldenRetry |
| C8 | is-network-error/index.js | `isNetworkError`, `fetchTypeError` | TestIsNetworkError, TestNoRetryClassification |
| C9 | p-queue.js (PQueue concurrency, FIFO, onIdle) | `callWithOptions` slot queue, `onIdle` | TestConcurrencyBound, TestQueueIsFIFO, TestSlotHeldDuringBackoff, TestOnIdle*, TestNoGoroutineLeak |
| C10 | error.js:86-132 raiseForStatus (403 org_scoped rewrite, 404/409 names, status/statusText/body message) | `raiseForStatus`, `statusError` | TestRaiseForStatus*, TestCallerGoldenRaise (54 vectors) |
| C11 | error.js:72-81 isLangSmithNotFoundError / isLangSmithConflictError | same names | TestStatusErrorNames |
| C12 | `await response.text()` (undici UTF-8 decode) | `utf8DecodeLossy` | TestUTF8DecodeLossy, golden "utf8 *" |

## Go mapping of JS failures

* `*statusError` (raiseForStatus): name/status as JS; retried only for 408 425 429 500 502 503 504.
* `context.DeadlineExceeded` (per-attempt `AbortSignal.timeout` equivalent, `http.Client.Timeout`) = TimeoutError: not retried.
  `context.Canceled` = abort: not retried. Both ahead of the hook, as the JS throws them ahead of the hook.
* Any other transport error (`*url.Error`, `net.Error`, `io.EOF`, `io.ErrUnexpectedEOF`, dial errors, ...) = undici's
  `TypeError: fetch failed`: retried. Go dial / read `i/o timeout` (not a ctx deadline) is retried, as undici's connect/headers timeout is.
* `*fetchTypeError` lets a client represent a JS TypeError explicitly: a non-network one (e.g. "Invalid URL") is thrown, not retried.
* Optional error interfaces mirror JS properties: `ErrorName()` (`name`), `ErrorCode()` (`code`, "ECONNABORTED" is not retried),
  `ErrorResponse()` (`response`, then `response.status` beats `error.status`).
* `onFailedResponseHook`: invoked for every failure that gets past the no-retry checks, including the last attempt, with the
  error's response. raiseForStatus errors carry none (error.js:113-120 builds `new Error` + `.status`), so it receives nil,
  and handle429 (client.js:111-122, `response?.status === 429`) never fires in this SDK path. "handled" skips the status check
  only (async_caller.js:107-109): a handled 400 is still retried until the retries run out.
* Cancelling the ctx ends a call that is queued, running (the fn must honour ctx) or backing off, with `ctx.Err()`. In JS the
  `signal` option only races the promise ("AbortError") and the queued work keeps running; the client never passes one.

## Design

No goroutine per call: the call runs on the caller's goroutine, the queue is a FIFO of parked callers (`container/list`,
one `chan` per parked call; the unsaturated path allocates nothing). The slot is held during backoff (pRetry runs inside
`queue.add`). `onIdle(ctx)` waits for `queued + running == 0`. Time is injectable (`asyncCaller.sleep`, `asyncCaller.jitter`).
Allocations (bench): call 0, callWithOptions 0, retry once 5 (104 B, mostly the closure of the bench), retryDelay 0.

## Findings (defects/quirks ported, for the lead)

1. **L1** `handled` from `onFailedResponseHook` does not skip the retry: it returns from `onFailedAttempt`, so pRetry continues
   (retries consume as normal) and only the non-retryable-status rethrow is skipped. Ported as is.
2. **L2** The hook runs on the last attempt too; handle429 would then sleep `retry-after` seconds before the error is thrown.
   In this SDK path it never fires (finding 3).
3. **L3** `onFailedResponseHook` always receives `undefined` here (raiseForStatus errors have no `.response`), so the 429
   Retry-After back-off in client.js `handle429` is dead code in 0.10.5: a 429 just uses the exponential delay.
4. **L4** `startsWith("Cancel"/"TimeoutError"/"AbortError")` on the message is case-sensitive and also catches any error whose
   text begins so (ported with `strings.HasPrefix`).
5. **L5** `debug` in the AsyncCaller params is never read by AsyncCaller; `callerParams.Debug` is accepted and ignored.
6. **L6** Node 24 (v24.22.1, undici) `response.text()` strips up to TWO leading BOMs (observed: BOM BOM x -> x; BOM BOM BOM x -> BOM x;
   BOM a BOM -> a BOM). A raise-message body starting with BOM bytes differs from a single-strip WHATWG decode. Ported as observed;
   depends on the Node version Pi runs.
7. **L7** raiseForStatus on a success body that fails to read rejects (retried as a network failure); on an error body it silently yields "".
8. Status text: the message uses the reason phrase of the status line (`Response.statusText`); over HTTP/2 Go synthesises
   "403 Forbidden" where undici gives "". Not reachable on the HTTP/1.1 loopback/cloud path; noted.

## Differential evidence

`port/tools/gen/caller-differential.mjs` runs the real SDK (`utils/async_caller.js`, `utils/p-retry`, `utils/error.js`) on node 24
against a loopback server: 37 retry scenarios (all retryable/terminal statuses, retries 0/1/4/default, 429 runs, reset, hang
timeout, 403 org-scoped, unicode body), 54 raise scenarios (statuses, reasons, 403 variants, UTF-8 edge bodies, BOMs),
420 backoff vectors (edge + pseudo-random jitters, 0..13 retries consumed). Recorded: attempts, backoff delays (setTimeout calls
from p-retry), outcome, error name/status/message. `caller_golden_test.go` replays each against a raw loopback server that answers the
JS server's bytes. Regenerate: `ls run "node components/langsmith/port/tools/gen/caller-differential.mjs <out>"` and copy to `testdata/caller_golden.json`.

## Checks

`go vet` linux/darwin/windows ok; `go test -race -count=3` ok (32 test functions, 123 `=== RUN` incl. the 91 golden subtests), statement coverage 98.6 %.
Run in an isolated scratch module (caller files only) because other lanes' files did not compile together at the time (duplicate `jsTrim`
in `client_jsutil.go`, `jsutil.go`, `profile_json.go`).

## Named skips

None. Open question for LsClient: transport errors you want treated as a non-retried TypeError must be wrapped in `*fetchTypeError`
(e.g. URL parse failure); everything else is retried like undici's `fetch failed`.
