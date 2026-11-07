# LangSmith port tools

Evidence tools for the Go port of `@langchain/langsmith-pi-extension` 0.2.1 (and the LangSmith JS SDK 0.10.5 it sends
through). One Go module (`go.mod`, standard library only, no PiG imports) and some node scripts.

| Tool | What it is |
|---|---|
| `cmd/mockls` | A loopback LangSmith endpoint. Answers `GET /info`, `POST /runs/multipart`, `/runs/batch`, `/runs`, `PATCH /runs/<id>`, the OAuth discovery and token endpoints; injects failures; records **every request** as JSONL. |
| `cmd/lsdiff` | Compares two request logs (the JS original, the Go port) after a documented list of normalisations and reports the first difference. |
| `sdkdrive.mjs` | Drives the **real vendored SDK** (`/home/kinsy/ls-work/oracle-run/node_modules/langsmith`) from a JSON op script against mockls. |
| `sdkdiff-regen.sh` | Regenerates the golden request logs of `../sdkdiff` with `sdkdrive.mjs`. `../../extensions/langsmith/sdkdiff_test.go` replays the same scripts through the Go port and runs `lsdiff` against them. |
| `run-lane.sh` | Runs the `pigeq` scenarios on the original under Pi, the original under PiG's Node runtime and the Go port under PiG, each against its own mockls; then `lsdiff` on the wire and `pigeq diff` on the host traces. |
| `lane/materialise.mjs` | Turns a pigeq scenario that talks to its `ls` fake server into one that talks to mockls (and the `ls` routes into a mockls config). |
| `gen/scenarios.mjs`, `gen/sdkdiff-ops.mjs` | Generate `../scenarios`, `../scenarios-wire` and `../sdkdiff/ops` (the committed JSON is their output). |

Everything heavy runs on xd6702 through the dev loop (`ls run "<cmd>"`, `LS_CPUS<=3`); never `go test` on the Mac.

## mockls

```sh
mockls -log requests.jsonl [-addr 127.0.0.1:0] [-config cfg.json] [-gzip] [-multipart=false] [-port-file F] [-stdin-lifetime]
```

Prints `mockls listening http://127.0.0.1:PORT`, stops on SIGINT/SIGTERM (or stdin EOF with `-stdin-lifetime`). Loopback
addresses only. `GET /__mockls/health` and `/__mockls/stats` are admin routes and are not logged.

Config (JSON, unknown fields rejected):

```json
{
  "info": { "batch_ingest_config": { "use_multipart_endpoint": true, "size_limit": 100, "size_limit_bytes": 20971520 },
            "instance_flags": { "gzip_body_enabled": false } },
  "oauthMetadata": { "issuer": "{{base}}", "token_endpoint": "{{base}}/oauth/token", "device_authorization_endpoint": "{{base}}/d" },
  "token": { "access_token": "A", "refresh_token": "R", "expires_in": 3600 },
  "faults": [
    { "route": "multipart", "calls": [1], "status": 404 },
    { "route": "multipart", "from": 3, "to": 4, "status": 429, "headers": { "Retry-After": "1" } },
    { "route": "info", "delayMs": 300 },
    { "route": "runs", "calls": [2], "drop": true }
  ]
}
```

* `info` replaces the whole `/info` document (default: multipart on, no gzip, the SDK's limits). `{{base}}` expands to the
  server URL.
* Routes for faults: `info multipart batch runs patch token oauth-metadata other`, or `*`. `calls` are 1-based **per-route**
  call indexes; `from`/`to` give a range; none means every call. The first matching fault decides the answer. A fault has
  `status`, `headers`, `body` or `json`, `delayMs` (the request is logged before the delay), `drop` (close the connection
  after the body was read).
* Without a fault every ingest route answers `200 {}`. Unknown routes answer 404 and are logged as route `other`.

Log (`internal/reqlog`): one JSON object per line: `seq`, `time`, `route`, `call`, `method`, `path`, `query`, `headers`
(all, lower-cased, `host` included), `transfer` (`chunked`, `content-length`, `none`), `contentLength`, `encoding`, `body`
(raw bytes as received, base64), `decoded` (after gunzip), `boundary` and `parts` (each: `name`, `contentType`, `filename`,
`header` (raw header block), `bytes` (base64), `text` when UTF-8) for `multipart/form-data`, `form` for a form body,
`parseError` when the body claimed a format it did not have (a framing the SDK never produces is reported, not repaired),
and `reply` (`status`, `fault` index or -1, `delayMs`, `dropped`).

## lsdiff

```sh
lsdiff [-max N] [-scenario scenario.json] [-rule P=>R]... [-path-prefix P]... [-host-prompt] [-unordered-drain] A.jsonl B.jsonl    # A: JS original, B: Go port
lsdiff -rules                                                                                    # print the rule list
```

Exit 0 identical after normalisation, 1 different, 2 error, 3 no difference but an `ANOMALY` in a log itself (a body that did not parse, a wrong `length=`: two logs can agree on something broken). Requests are compared one by one, in log order: route, method,
path, query, every header, framing mode, content-encoding, then the body: for multipart part by part (name, raw header
block, bytes), for JSON bodies (`/runs/batch`, `/runs`, `PATCH`) as text. **The first difference** prints the request, what
differs, both values around the first differing byte, then the normalisations that fired (counts per log; `(uneven)` marks a
rule that fired a different number of times in the two logs). The comparison is on the raw bytes of the sender: a document
is parsed only to find the values a rule edits, and re-emitted exactly as written apart from those edits.

Normalisations (anything else compares byte for byte; `lsdiff -rules` prints the same list):

* **N1 ids**: every UUID in any string (run/trace/parent ids, `dotted_order` ids, part names, URL paths, session ids) is
  numbered `<id1>`, `<id2>`... by order of first appearance in the log. Same ids in the same places in the same order.
* **N2 times**: the `YYYYMMDDTHHMMSSffffffZ` prefixes of `dotted_order`; ISO-8601 timestamps inside strings (`<ts>`);
  numeric `start_time`/`end_time` of a run, an event's `time`, `metadata.timestamp`, and `timestamp`/`durationMs` numbers
  inside host-delivered objects (`<ts:num>`: a number stays distinguishable from a string).
* **N3 boundary**: the multipart boundary of each request (Content-Type and framing) becomes `<boundary>`; its shape must be
  `----LangSmithFormBoundary` + base36 in both logs.
* **N4 divergence D1**: `extra.runtime.{runtime,sdk,sdk_version}` values and the `User-Agent` header (presence still
  compared). The `length=` parameter of a part whose text such an edit changed is **checked** against the part's byte
  length instead of compared (every part's `length=` is checked this way in both logs: it is the UTF-8 byte length of the part).
* **N5 host key order**: inside objects the host delivers (provider payload of an llm run's `inputs`, tool `args` and tool
  `output`, message objects under `outputs.messages`, `toolResults`, `context_usage`, `tool_update.partialResult`) keys are
  sorted recursively and the object is re-emitted compactly. The keys the extension itself puts first (`messages` in an llm
  payload, `role`/`content` of a message, `role` of a tool result) must still lead (a lead key that moved shows as a
  difference). Objects the extension builds (`{prompt,imageCount}`, run payloads, metadata, `usage_metadata`) keep their
  order and their whitespace.
* **N6 paths**: `<anything>/eqXXXXXXXX` (pigeq's lane scratch directory) and each `-path-prefix` become `<lane>`.
* **N7 scenario rules**: the `normalize` list of the pigeq scenario (`-scenario`) or `-rule PATTERN=>REPLACEMENT` (repeatable), applied after N1-N6 like pigeq
  does. run-lane.sh passes only what `lane/lsdiff-flags.txt` lists for a scenario (today one `-rule`: Pi's Gemini provider names tool calls `read_<Date.now()>_<n>`).
* **N8 (opt-in, `-host-prompt`)**: for a Pi-versus-PiG pair only: the `content`/`sections` of a system message inside
  host-delivered messages are the host's own prompt text and are not compared, and `outputs.context_usage` / a compaction run's `outputs.tokensBefore`, which the host estimates from
  that text, are not compared. `pig-node` versus `pig-go` never uses it.
* **N9 (opt-in, `-unordered-drain`)**: consecutive ingest requests (`/runs/multipart`, `/runs/batch`) are compared as a multiset: the batches of one queue drain are sent
  concurrently and which reaches the wire first is scheduler order (microtask depth in the JS SDK, goroutines in Go). They are sorted by a key free of ids,
  times and boundary before N1 numbers the ids. A request's content is still compared in full. The op script says where it applies (`"unorderedDrain": true`).
* **T1** transport headers: `host keep-alive accept-encoding content-length transfer-encoding` are not compared (the framing mode
  `chunked`/`content-length` is compared instead); `accept accept-language sec-fetch-mode connection` are not compared when they carry
  the HTTP client's default (`*/*`, `*`, `cors`, `keep-alive`), so a value the SDK sets itself (`Accept: application/json` on `/info`) is.
  **T2** a gzip body is compared decompressed (the compressor's bytes belong to the HTTP client); whether the request was
  gzip-encoded is compared.

## sdkdrive.mjs and sdkdiff

```sh
node sdkdrive.mjs --ops ../sdkdiff/ops/basic-post-patch.json --mockls $BIN/mockls --log out.jsonl --console out.result.json
```

An op script (`../sdkdiff/ops/*.json`, generated by `gen/sdkdiff-ops.mjs`) has `env`, `files` (written under an empty HOME that
is also the working directory), `readFiles`, a `mockls` config and `ops`: `client`, `runTree`, `createChild`, `end`,
`addEvent`, `postRun`, `patchRun`, `createRun`, `updateRun`, `awaitPendingTraceBatches`, `sleep`. Ids and start/end times are
fixed in the script; `{{endpoint}}` is the mockls URL. The result JSON holds what the SDK wrote to `console.warn/error`, the ops
that threw and the files asked for afterwards.

`sdkdiff-regen.sh` runs every script and stores `../sdkdiff/golden/<name>.jsonl` (the request log of the real SDK) and
`<name>.result.json`. `sdkdiff_test.go` (module `langsmith`) builds mockls and lsdiff, replays each script through the Go
port, then compares wire (lsdiff), stderr text, thrown op errors and rewritten files with the golden.

## Scenarios and lanes

`../scenarios/*.json` are pigeq scenarios that run unchanged under `pigeq run|record|check` (the LangSmith endpoint is a pigeq
`servers.ls` entry whose recorded requests enter the trace; the scenario `normalize` rules (N7) cover boundary, time prefixes,
numeric times, `length=`, D1 and the host's system prompt text; `capture` leaves the agent/turn/message lifecycle out because where the
extension's own requests fall among those events is host output pacing, which differs between Pi and PiG; `commands` declares `git`). `../scenarios-fail/*.json` + `.expect` must fail to load in every lane (run-lane.sh checks the refusal, an extended regexp in the `.expect` file, in the lane's output). `../scenarios-wire/*.json` need mockls (gzip bodies and framing are not comparable through a pigeq
server). `../golden/*.jsonl` are the Pi traces `pigeq record` wrote for `../scenarios`.

```sh
ls run "cd components/langsmith/port/tools && ./run-lane.sh -o /home/kinsy/ls-work/scratch/LsHarness/lanes"          # all scenarios, 3 lanes
ls run "cd components/langsmith/port/tools && ./run-lane.sh -s ../scenarios-wire -n wire-gzip -l pi,pig-node"
```

`lane/lsdiff-flags.txt` (generated) gives a scenario extra lsdiff options (`-unordered-drain` for the two replica destinations, `-rule ...`). Options: `-s DIR` scenarios, `-o DIR` output, `-n a,b` only these, `-l pi,pig-node,pig-go` lanes, `-p PORT` base mockls port (default
39631; scenario N gets base+N, the same port in every lane of that scenario so the endpoint in `extra.metadata` is equal, and a host that
outlives its lane cannot post into the next scenario's log), `-k` keep pigeq run directories. Per scenario and lane
it starts a mockls (config derived from the scenario's `ls` routes), runs `pigeq lane`, stops mockls; then compares `pi`/`pig-node`,
`pi`/`pig-go` (`-host-prompt` on for pairs with Pi) and `pig-node`/`pig-go` (strictest: one host, no host rule). lsdiff is run without the
scenario's `normalize` list on purpose: those rules (N7) blank `length=` and the host prompt, which lsdiff checks or treats separately. `report.txt` has one line per comparison; exit 0 only when all are identical.
`LS_ORACLE` (default `/home/kinsy/ls-work/oracle-run/dist/index.js`) and `LS_GOEXT` (default the Go module) override the extensions.

Regenerate scenarios: `node gen/scenarios.mjs`. Record the Pi goldens: see `../lane-harness.md`.
