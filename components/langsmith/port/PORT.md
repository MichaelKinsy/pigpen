# Port record: langsmith (@langchain/langsmith-pi-extension 0.2.1)

| Input | Identity |
|---|---|
| Kind | 1 (Pi TypeScript extension), plus a re-implementation of the LangSmith JS SDK paths it sends through |
| Original | `@langchain/langsmith-pi-extension` 0.2.1, https://github.com/langchain-ai/langsmith-pi-extension, tag `@langchain/langsmith-pi-extension@0.2.1`, commit `b0d83c3c3bc994fc7570f2bfc7b12561b8282bef`, MIT (LangChain). `port/oracle/` = `src/ test/ scripts/ README CHANGELOG package.json LICENSE` of that commit, unmodified, plus `dist/` = the npm 0.2.1 build (tarball sha1 `7ef996ee6eabeba41a52b2110378e0a1d0a4e321`), which is what Pi users run and what the harness executes (`src/version.ts` in git says 0.0.2: the build injects 0.2.1) |
| SDK it sends through | `langsmith` 0.10.5 (the extension's lockfile pin, `^0.10.5`), js/ of https://github.com/langchain-ai/langsmith-sdk commit `ad399f1da7a775119eda41e2644be1c050153445`, MIT (LangChain); installed from npm for the oracle at `/home/kinsy/ls-work/oracle-run` |
| Oracle hosts | Pi 1.5.0 and PiG's Node runtime (both run the unmodified `dist/index.js`) |
| Target | PiG 0.7.0+1.5.0 (`pig --version`), Go 1.27.3 |
| Original's tests | 8 vitest files, 26 cases (25 pass, 1 skipped on the oracle: `replay.test.ts` needs `REPLAY=1`) |
| Wire endpoint | a local mock (`port/tools/cmd/mockls`), never a real service or credential |

The Go code is in `extensions/langsmith`: `extension.go config.go metadata.go convert.go` port `src/*.ts`; `runtree.go client*.go caller.go
profile*.go timefmt.go uuid.go xxh3.go sampling.go runtime_env.go` port the SDK files named in each file's header. `ordered.go` carries JavaScript
object semantics (insertion order, `undefined`, `JSON.stringify`). The mapping tables are in `port/lane-*.md` (one contract table per lane, with
the upstream file:line, the Go symbol and the test) and are the contract of this port.

## Result summary (see each section for the evidence)

* Extension behaviour: 129 of 129 compared wire pairs identical (pi = pig-node = pig-go, 43 scenarios) plus 72 of 72 for the 24 wire-only scenarios,
  after the documented normalisations; three configurations that must refuse to load are refused by all three lanes.
* SDK behaviour: 30 SDK op scripts, 55 client scenarios, 103 run-tree scenarios, 158 profile scenarios and the helper vectors are
  byte-identical to the real JS SDK, which ran on the same inputs.
* Twins of the upstream tests: 18 exact, 3 named skips (E1). `pigeq gaps --go`: one non-blocking PARTIAL (`GetModelInfo`).
* Mutations: `port/mutations.md`. Perf: `port/perf/README.md`.

## Evidence

1. **Unit and vector layer (`go test -race -count=3`, vet on linux/darwin/windows).** Every helper that has a JS counterpart has golden vectors produced
   by the real vendored JS: XXH3-128 for every length 0..700 and 3000 random inputs, uuid v7 and the deterministic replica ids, sampling, dotted order,
   the env metadata, the retry/backoff policy (420 backoff vectors, 37 retry and 54 raise scenarios), the run tree (103 scenarios, every createRun and
   updateRun payload), the client (53+2 scenarios, 159+ requests: parsed multipart parts, console output, fallback files), the profile/OAuth flow (158
   scenarios) and the conversion code (274 vectors including the four upstream recordings, 31,000 random vectors in review).
2. **SDK differential (`TestSDKDiffGolden`).** 30 op scripts driven through the real SDK (`port/tools/sdkdrive.mjs`) and through the Go run tree and
   client; request logs compared by `lsdiff` after the explicit normalisation list below.
3. **Lane differential (`port/tools/run-lane.sh`).** The same scenario under the original on Pi, the original on PiG's Node runtime and the Go port on
   PiG, each against its own `mockls`; `lsdiff` on the wire and `pigeq diff` on the host traces. Scenarios: `scenarios` (43), `scenarios-wire` (24),
   `scenarios-fail` (3). Results at the end of the lane work: wire identical on every pair except as listed under "Known differences in the lane run".
4. **Upstream vitest on the oracle** (pnpm frozen lockfile): 25 passed, 1 skipped, 0 failed.
5. **Mutation check:** `port/mutations.md`.

### Normalisations (everything else compares byte for byte; `lsdiff -rules` prints the same list)

N1 ids numbered by first appearance; N2 timestamps, `dotted_order` time prefixes, numeric start/end times, event times, measured durations; N3 multipart
boundary; N4 divergence D1 (runtime, sdk, sdk_version values and the User-Agent; `length=` is verified, not compared); N5 key order inside host-delivered
objects only (finding F1); N6 lane scratch paths; N7 the scenario's own list; N8 (Pi versus PiG pairs only) the host's own system prompt text, Gemini
`abortSignal`, host token estimates; N9 (opt-in, per scenario) concurrent batches of one drain compared as a multiset (each destination's content is
still compared); T1/T2 transport headers and gzip decoded.

## Named exclusions and divergences (owner decisions, none silent)

* **E1 embedding options.** `client`, `config`, `getCurrentRunTree` of `LangSmithExtensionOptions` and `src/sdk.ts` (the README's embedding example): a Go
  Piglet has no embedding entry point. Every agent run is a new root trace, as in the original with no ambient traceable run. Three twin cases skipped.
* **Refused loudly:** OTEL tracing mode (`LANGSMITH_TRACING_MODE=otel`, `LANGSMITH_OTEL_ENABLED`, `OTEL_ENABLED`), a configuration the original's zod schema rejects
  (the factory panics with a one-line summary, then the ZodError text), and the profile values a Go type cannot carry (non-string `api_url`/`workspace_id`).
* **D1 sender identity.** `extra.runtime.{runtime,sdk,sdk_version}` and `User-Agent` say `go`, `pigpen-langsmith-go`, `0.1.0` (keys are the SDK's). Flipping them is two constants in `runtime_env.go`.
* **F1 key order in host-delivered objects.** The SDK hands event data to Go as `map[string]any`, so message, payload and tool-argument keys are written sorted.
  Objects the extension builds keep the original's order. Recommendation: the SDK exposes the ordered raw JSON of an event payload (a lane, `SdkKeyOrder`, is looking at it).
* **D3 batch timing.** The SDK's 250 ms timer is cleared before two awaits (`client.js:1601`), so under overlapping operations timers leak and the original sends
  more, smaller batches (33 vs 15 requests in one workload, same bytes). The port debounces as documented. Payloads identical.
* **D2/D4 smaller:** `console.log/warn/error` go to stderr (and `console.error(err)` prints `Name: message`, no stack); the HTTP client honours `HTTP(S)_PROXY`
  (Pi installs undici's EnvHttpProxyAgent too) and speaks HTTP/1.1; response bodies of the OAuth flow are capped at 8 MiB; the `file:` and UTS #46 limits of the URL parser
  (`profile_url.go`, `urlparse.go`); `jsISOString` panics beyond +-8.64e15 ms; one mutex serialises the hooks (the original interleaves at awaits only); `GetModelInfo`
  is read per `turn_start` and `before_provider_request` (host round trip, see "Host findings").

## Defects of the original and the SDK, ported (for the owner)

LsWire W1-W6: XXH3 1-3 byte path sign-extends, 9-16 byte path ORs a carry; a secret-named env var shorter than 4 units (even an empty `LANGSMITH_API_KEY=`) aborts the
metadata scan and drops every later variable; `getShas`/runtime cached per process; uuid7 with seq 0 has only 42 random bits. LsCaller L1-L7: the 429 `Retry-After` wait
(`handle429`) never fires; `handled` does not skip the retry; BOM stripping depends on the Node version. LsClient C1-C10: the 422 buffered retry is unreachable; batched ingestion
sends `x-api-key: undefined` for profile auth; a failed `GET /info` is re-probed for every operation (15 requests for one run, shutdown can take over a minute: F-H3);
creates without a truthy id are dropped from mixed batches; gzip only for the streamed body; `LANGSMITH_TRACING_SAMPLING_RATE=abc` samples nothing. LsRunTree F1-F10: a replica
`patchRun` rejects the handler; execution order truncated to 3 digits; env replicas conflict check only for the object form; with replicas only the replicas receive runs; replica
`updates` apply to PATCH only. LsProfile P1-P10: non-string credentials throw; OAuth rewrite normalises the user's file; stale-lock race. LsExt: Gemini role lookup finds
`Object.prototype` members; `null` message entry throws; an empty `tool_result`-only user message is dropped; `LANGSMITH_RUNS_ENDPOINTS` in the SDK's documented object form
makes the extension fail to load (F-H1). Details with line references: `port/lane-*.md`.

## Known differences in the lane run

* `pigeq check` against the Pi goldens passes only where the trace carries no host-delivered object: its byte comparison cannot normalise F1 or the host's system prompt, so
  the wire comparison is `lsdiff` (pi = pig-node = pig-go). The goldens stay as the host-trace record of the original.
* `tracing-background-false` cannot complete under pigeq's request-holding fake server on the Go lane: the Go handler for `agent_settled` awaits the PATCH and PiG's RPC loop does not
  answer `get_state` meanwhile (host finding below); with `mockls` the three lanes are identical.
* `shutdown-while-running` and `trace-bash-tool` host traces differ between Pi and PiG (host behaviour: an `extension_error` of PiG's own `turn_end`; a harness pipe flake), wire identical.
* `interrupted-by-next-run` (`Trace replaced by a new Pi run`) is not reachable on Pi 1.5.0/PiG; covered by the upstream `late-settle` twin and Go tests.

## Host and SDK findings made while porting (reported to Main; the port stays faithful)

1. SDK frame decode: `UnmarshalFrame` validates then decodes (two scans) and copies each frame three times: 44-55 % of the Go extension's CPU on large payloads; Go loses to TS on d1000 (1.53x wall) and e (1.46x) until fixed (lane SdkFrameDecode).
2. Packed Go runner has no per-member recover: a factory panic kills sibling extensions (PackedRecover).
3. SDK replicates only model id and provider: `name`, session id and context usage cost host round trips (SdkReplica).
4. A Go handler awaiting in `agent_settled` blocks the host's `get_state` (SettleBlock).
5. Event data key order is lost (SdkKeyOrder). 6. `pig` does not surface the Go extension's stderr to the harness in the f500 workload (0 bytes against 1138 for TS): to be checked.
