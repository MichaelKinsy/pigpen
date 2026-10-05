# Port record: typesafe

Status: complete. Every twin passes, every mutant is killed, both recorded oracles (official TS SDK,
Python adapter) match, and the WorkflowEvals cross-check matches. No live check was possible (no
`TYPESAFE_API_KEY` was supplied). No Piglet was touched, so no Binary build applies (this is a library
Package, like `pig-play`).

## Pinned upstreams

| Upstream | License | Commit | Version |
|---|---|---|---|
| https://github.com/typesafe-ai/typesafe-sdk-js | MIT | `66880ccded6cb642dc1809620c2b108c33730214` | `@typesafe-ai/sdk` 0.6.0 |
| https://github.com/typesafe-ai/system-one-adapter-python | MIT | `e1d4cc938204b22fc5a3c3aca7044072fe3f712d` | `system-one-adapter` 0.2.1 |
| https://github.com/typesafe-ai/WorkflowEvals (cross-check only) | Apache-2.0 | `0ac3b8ad845429f0d8e064ecfb2430a47c5a25cb` | none (uses `typesafe-sdk` 0.7.2 and `system-one-adapter` 0.2.1) |

The sources, tests and licenses of the first two are vendored byte for byte under `port/oracle/` (the oracle
the twins and goldens were taken from). Of WorkflowEvals only its license is kept: the cross-check stores its
questions (from its code), never its dataset content.

## File mapping: typesafe-sdk-js to `libraries/typesafe`

| Upstream | Go |
|---|---|
| `src/client.ts` (`TypeSafeClient`, transport, retry loop, header merge, `systemOne`) | `client.go` (`Client`, `Config`, `RequestOptions`, `request`/`attempt`), `answers.go` (`SystemOneRequest`) |
| `src/api-promise.ts` (`APIPromise`, `withResponse`, `asResponse`) | `client.go` (`Response[T]`, `RawResponse`, `*WithResponse`, `*Raw`, `MapResponse`) |
| `src/resources/models.ts` | `client.go` (`Models`, `ModelCard` in `answers.go`) |
| `src/questions.ts` (`noul`, `score`, `choice`, `validateQuestions`) | `questions.go` (+ `ParseQuestions`, Go-only) |
| `src/types.ts` (question, answer, result, options types) | `questions.go`, `answers.go`, `entry.go` (`Entry`), `client.go`, `retry.go` |
| `src/errors.ts` | `errors.go` |
| `src/retry.ts` | `retry.go` (`RetryPolicy`, `Retry`, `ParseRetryAfter`) |
| `src/logging.ts` | `logging.go` |
| `src/env.ts`, `src/version.ts`, `src/runtime.ts` | `env.go`, `version.go`, `retry.go` (`describeRuntime`) |
| `src/index.ts` (exports) | the exported identifiers of the package, listed in `CONTRACT.md` |
| none (the SDK has no batch call) | `batch.go` (`EvaluateBatch`), requested for the batched evaluate tools |

## File mapping: system-one-adapter-python to `libraries/ownmodel`, `libraries/pigmodel`

| Upstream | Go |
|---|---|
| `_client.py` (client, prompts, corrective and transient retries, usage totals, attempts) | `run.go`, `ownmodel.go` (`Backend`, `Options`, `Evaluation`, `DebugError`) |
| `_schema.py`/`_response.py` (answer schema, descriptions, strict validation, conversion) | `plan.go` (schema, validation), `convert.go` (conversion), `canon.go` (pydantic-compatible JSON) |
| `_utils/confidence_metrics.py`, `_utils/probability_normalization.py` | `convert.go` |
| `_utils/error_handling.py`, `providers/*` (OpenAI, Anthropic, Gemini) | **not ported**: the model comes from PiG. `libraries/pigmodel` (`pigmodel.go`) replaces the provider layer with `ModelRegistry`; no provider or model name appears in it (a test scans the source literals) |
| `_version.py`, `__init__.py` | `doc.go`, exported identifiers |

## Tests: twins

Every upstream test case is listed in `port/twins/*.txt` (189 TS cases from `vitest run --reporter=json`, 424
Python cases from `pytest --collect-only`), and a test in each package checks by AST that every case is claimed
by a `twin(...)` (ported) or `skipTwin(...)` (named skip with a reason) call, or matches a per-file skip rule
with a file-specific reason. The tests are red-first: commits `a47c0f1` (core, 158 fail) and `14d26d7`
(ownmodel and pigmodel, 44 fail) precede the implementations `28b3ff2` and `fa9a322`.

| Upstream test file | Cases | Go twin file |
|---|---|---|
| `api-promise.test.ts` | 10 | `api_response_test.go` |
| `client.test.ts` | 42 | `client_test.go`, `questions_test.go`, `types_test.go` |
| `errors.test.ts` | 19 | `errors_test.go` |
| `logging.test.ts` | 15 | `logging_test.go` |
| `native-transport.test.ts` | 8 | `transport_test.go` |
| `release-regressions.test.ts` | 9 | `regressions_test.go` |
| `reliability.test.ts` | 37 | `reliability_test.go` |
| `retry.test.ts` | 31 | `retry_test.go` |
| `runtime.test.ts` | 5 | `runtime_test.go` |
| `types.test-d.ts` | 13 | `types_test.go` |
| `tests/utils/*.py` | ownmodel | `utils_test.go` |
| `tests/test_client_with_fake_model.py`, `tests/test_schema.py` | ownmodel | `backend_test.go`, `schema_test.go`, `twins_test.go` |

Accounting: TS 189 = **171 ported + 18 named skips** (JavaScript Promise and thenable mechanics, the browser guard and
runtime detection, TypeScript type-level inference and compile-time rejections that Go's typed builders make
inexpressible; each reason is in its `skipTwin` call). Python 424 = **68 ported + 24 named skips + 332 covered by per-file rules** (the OpenAI,
Anthropic and Gemini provider transports, their cassettes, lifecycle and error translators, which do not exist
here; the rules give the reason per file). The prompts, schema and answers that the skipped live-API cases
exercised are covered by the differential goldens below.

## Equivalence proof

The Skill's `pigeq` harness drives Pi extensions (events, tools, commands) and does not apply to a library, so
the proof is differential against recorded oracles, replayed offline:

1. **Python adapter.** `port/equivalence/record_python.py` runs the unmodified adapter on 19 scenarios (both
   modes, native and prompted, fences, malformed then valid, malformed exhausted, normalization on/off, ties,
   escapes, rich descriptions) and records the messages, schema, answers, usage and diagnostics.
   `TestEquivalence_GoBackendMatchesThePythonOracle` replays each through `ownmodel` and compares: prompts and
   schema byte for byte, answers and diagnostics equal.
2. **Official TS SDK.** `port/crosscheck/` (see its README): 45 scripted exchanges recorded from the SDK built at
   the pinned commit and replayed through the Go client: requests (method, path, headers, body) and outcomes
   (result, or error class, message, status, request ID, body) match, except the four listed differences.
3. **WorkflowEvals.** 23 real `system_one` calls made by its four workflows (invoice processing, customer
   service, agent trace observability, security incidents) carry 205 Noul, 105 Choice and 6 Score questions with
   rich instructions and criteria; the Go body equals the official SDK's and the Python SDK's canonical body.
   With `WORKFLOWEVALS_FULL` the same holds on real dataset states (run locally, all equal).
4. **Live.** `TestLive_AgainstTheRealAPI` (and `live_js.mjs`) run only with `TYPESAFE_API_KEY`; not run here.
5. **PiG SDK.** `port/sdkcheck/run.sh` proves at compile time that `sdk.ModelRegistry` (PiG checkout
   `63c6ba456`) satisfies `pigmodel.Registry`.

Reproduce: `go test -race ./...` in this directory; `python3 port/mutate.py`; re-record with the commands in
`port/crosscheck/README.md` and `port/equivalence/record_python.py`.

## Mutation check

`port/mutations.json` holds 55 deliberate defects (retry arithmetic, status classes, header protection, URL
handling, error mapping, question validation, batching, prompt text, escaping, schema requirements, bounds,
tie-breaking, normalization, tolerance, stop-reason mapping, credential forwarding). `port/mutate.py` applies each
to a copy of the module and runs the package tests: **55/55 killed** (`port/mutation-results.txt`). Every kill is
by a Go test (layer 1); the differential goldens kill the prompt, schema and conversion mutants. The first run
found three survivors, closed with new tests (`libraries/*/mutation_test.go`): default batch concurrency, default
transient retries of the backend, and the 1e-6 tolerance. The review added a 55th (`retry-after-wraps-negative`,
see below).

## Findings from the cross-check

- The client escaped `<`, `>` and `&` in request bodies (`encoding/json`); the official SDK does not. Fixed
  (`marshalPlain`); `U+2028`/`U+2029` are always escaped (`marshalPlain` makes Go 1.26, which passes them raw through a Marshaler, match Go 1.27; same JSON, +3 bytes each).
- Python SDK 0.7.x rejects `null` score criteria; the TS SDK 0.6.0 accepts them. The Go client follows TS (the
  contract), and `ownmodel` sends whatever it is given.
- (Review) A `Retry-After` beyond `time.Duration`'s range (for example `1e12` seconds) wrapped to a negative
  delay, passed the `MaxRetryAfter` ceiling and retried at once, where JavaScript falls back to backoff.
  Fixed by saturating the conversion (`msToDuration`); twin-free Go test
  `TestRetryDelayMs_HugeRetryAfterFallsBackToBackoffNotANegativeDelay`.
- The Python SDK orders the body `state, model, questions`; the TS SDK `state, questions, model`; Go follows TS.

## Deliberate differences from the originals

See `CONTRACT.md` (TypeScript SDK) and the list in `port/crosscheck/README.md`. In `ownmodel`: no provider
clients (PiG model access instead); prompted mode only through `pigmodel`; the `Model` interface returns
`typesafe` errors directly; the default number of transient retries is 0 like the oracle; `Evaluate` adds
`Debug` and `Usage` as return values where the oracle attaches them to the response object.
