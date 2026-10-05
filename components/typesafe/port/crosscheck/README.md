# Cross-check against the official SDK and WorkflowEvals

Two recorded oracles, replayed by `libraries/typesafe/crosscheck*_test.go` with no network beyond
`127.0.0.1` and a dummy API key.

| File | What it is | How it was made |
| --- | --- | --- |
| `scenarios.json` | 45 scripted exchanges: systemOne and models.list calls, success and every error class, retries, retry headers, timeouts, connection resets, header merging, client-side validation | written by hand |
| `golden.json` | what the **official SDK** (`@typesafe-ai/sdk` 0.6.0, built from typesafe-sdk-js `66880cc`) sent (method, path, headers, body) and returned or threw, per scenario | `node record_js.mjs <dist/index.mjs> > golden.json` |
| `workflowevals.json` | 23 real `system_one` calls made by the four WorkflowEvals workflows (`0ac3b8a`, Apache-2.0): real question sets, a synthetic state (dataset content is not stored), and the body the **Python SDK** builds | `python extract_workflowevals.py 6 [full.json] > workflowevals.json` (run in a WorkflowEvals checkout with typesafe-sdk 0.7.2) |
| `golden_workflowevals.json` | hash of the canonical body the official SDK sends for each of those calls, and its key order | `node record_js.mjs <dist> workflowevals.json golden_workflowevals.json` |
| `live_js.mjs` | the official SDK's side of the live check | needs `TYPESAFE_API_KEY` |

`TestCrossCheck_ClientMatchesTheOfficialSDK` compares per scenario: request count, method, path, body (as
JSON, plus the order of state/questions/model), every SDK-set and custom header, and the outcome (result JSON,
or error class, message, status, request ID, body). `TestCrossCheck_WorkflowEvals...` compares the Go body for each
real call with the official SDK's and the Python SDK's. `WORKFLOWEVALS_FULL=full.json` also runs the real dataset
states (extracted locally with the second argument of the extractor) against the Python body.
`TestLive_AgainstTheRealAPI` runs only with `TYPESAFE_API_KEY` (and compares with the official SDK when
`TYPESAFE_LIVE_JS_DIST` names its `dist/index.mjs`); it was **not** run for this port (no key was supplied).

## Known differences (asserted by the test, so a stale entry fails)

- `200-invalid-json`, `200-empty-body`: the TS SDK returns the raw text or `undefined`; the typed Go client reports
  a `*TypeSafeError` (invalid JSON) or returns a zero result (empty body).
- `unknown-answer-type-and-extra-usage`: fields the typed result does not model (extra keys of a known answer,
  extra `usage` keys, extra top-level keys) are dropped; `SystemOneWithResponse` / `SystemOneRaw` keep the body.
- `score-map-criteria-client-side-error`: the wire form of a score map is rejected by `ParseQuestions` (a Go-only
  entry point) with another message; Go's builders cannot express a map.
- Bodies: same JSON, same state/questions/model order; the Python SDK orders state, model, questions. The Go encoder
  escapes U+2028/U+2029 (and the official SDK does not), which changes `content-length` by 3 bytes each.
- `x-typesafe-sdk` is `typesafe-sdk-go/0.6.0` and `x-typesafe-runtime` is `go/<version> (<os>; <arch>)`.
- The text after `Connection error:` comes from each runtime's HTTP stack.
