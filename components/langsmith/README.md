# langsmith (Go port)

A Go extension for PiG that traces your sessions to [LangSmith](https://smith.langchain.com): one `Pi agent run` per
prompt, a `Pi turn N` run per agent-loop turn, an LLM run per provider request (with the provider's own payload, the
assistant message, stop reason and token/cost usage), a tool run per tool call, `Context Compaction (<reason>)` runs, git
and repository attribution on every run, replicas, sampling and redaction. It sends nothing until you enable it, and it
needs no Node runtime.

This is a port of [`@langchain/langsmith-pi-extension`](https://github.com/langchain-ai/langsmith-pi-extension) 0.2.1 by
**LangChain** (MIT), including the parts of the **LangSmith JavaScript SDK** (`langsmith` 0.10.5, also LangChain, MIT) that
the extension sends through: the run tree, the batching client, multipart ingestion, retries, sampling, redaction and the
CLI profile authentication. See [CREDITS.md](CREDITS.md). The Go code is original work that follows the SDK's behavior;
what differs is listed below and in [port/PORT.md](port/PORT.md).

## Install

```sh
pig install ./components/langsmith                  # the Package, built from Go source on first use
pig --piglet dist/staged/piglets/pig-langsmith/piglet.yaml   # after `npm run stage`: the pig-langsmith Piglet
```

## Use

Tracing is off by default. Enable it with the same variables as the original:

```sh
export TRACE_TO_LANGSMITH=true
export LANGSMITH_PI_API_KEY="<your-langsmith-api-key>"
```

`/langsmith-tracing` shows the state (`LangSmith tracing is enabled (active trace <id>).` or the disabled hint). The footer
status reads `LangSmith: tracing`, `tracing run`, `traced` or `disabled`.

Configuration is merged in this order, later wins: defaults, `~/.pi/langsmith.json`, `<cwd>/.pi/langsmith.json`, environment.

| Variable | Meaning |
|---|---|
| `TRACE_TO_LANGSMITH` | `true`/`1`/`yes`/`on` enables, `false`/`0`/`no`/`off` disables |
| `LANGSMITH_PI_API_KEY` | API key, falls back to `LANGSMITH_API_KEY` |
| `LANGSMITH_PI_ENDPOINT` | API URL, falls back to `LANGSMITH_ENDPOINT` |
| `LANGSMITH_PI_PROJECT` | project, falls back to `LANGSMITH_PROJECT`, default `pi-coding-agent` |
| `LANGSMITH_PI_METADATA` | JSON object merged into the root run metadata, falls back to `LANGSMITH_METADATA` |
| `LANGSMITH_PI_RUNS_ENDPOINTS` | JSON array of replica destinations, falls back to `LANGSMITH_RUNS_ENDPOINTS` |

The config file fields (`enabled`, `api_key`, `api_url`, `project`, `metadata`, `replicas`) are the original's. The LangSmith
SDK's own variables work as they do in the original (`LANGSMITH_TRACING_SAMPLING_RATE`, `LANGSMITH_HIDE_INPUTS`,
`LANGSMITH_HIDE_OUTPUTS`, `LANGSMITH_HIDE_METADATA`, `LANGSMITH_WORKSPACE_ID`, `LANGSMITH_TRACING_BACKGROUND`,
`LANGSMITH_FAILED_TRACES_DIR`, `LANGSMITH_DISABLE_RUN_COMPRESSION`, the LangSmith CLI profile in
`~/.langsmith/config.json`, and the rest listed in [port/PORT.md](port/PORT.md)). An option the port does not implement
**refuses loudly**: OTEL tracing mode (`LANGSMITH_TRACING_MODE=otel`, `LANGSMITH_OTEL_ENABLED`, `OTEL_ENABLED`) stops the
extension from loading with a message that names it.

## Proof that it behaves like the original

`port/` holds the evidence and ships with the Package. The original is run, unmodified, under Pi 1.5.0 and under PiG's Node
runtime, against a local mock LangSmith endpoint (no real service, no credentials), with the same scripted sessions as the Go
port under PiG; the requests, host traces and UI calls are compared.

| Path | What |
|---|---|
| `port/oracle/` | The unmodified original (`src/`, tests, README, changelog) and its published npm build `dist/`. |
| `port/scenarios/`, `port/golden/` | 43 pigeq scenarios and the Pi traces the original recorded for them. |
| `port/scenarios-wire/`, `port/scenarios-fail/` | Wire-only scenarios (providers, errors, compaction, replicas) and configurations every lane must refuse to load. |
| `port/tools/` | The mock LangSmith endpoint (`mockls`), the request-log comparator with its explicit normalisation list (`lsdiff`), the SDK driver and `run-lane.sh`. |
| `port/sdkdiff/` | 30 op scripts driven through the real SDK and through the Go port; byte-identical requests. |
| `port/mutations.json`, `port/mutations.md` | Deliberate defects; every one is caught. |
| `port/perf/` | The perf basket (Go vs the original under PiG and Pi) with the method and the results. |
| [`port/PORT.md`](port/PORT.md), `port/lane-*.md` | Mapping tables, findings, decisions, results. |

## Differences from the original

* **Sender identity (D1).** `extra.runtime.runtime`, `sdk`, `sdk_version` and the `User-Agent` describe this sender
  (`go`, `pigpen-langsmith-go`, the Package version) instead of claiming to be `langsmith-js`; the keys are the SDK's.
* **Embedding options (E1).** `client`, `config` and `getCurrentRunTree` of the original's TypeScript embedding API have no
  Go entry point; every agent run is a new root trace, as in the original with no ambient run.
* **Key order inside host-delivered objects (F1).** Message content, provider payloads and tool arguments reach a Go
  extension as decoded maps, so their keys are written sorted; the objects the extension builds keep the original's order.
* **Batch timing (D3).** The original's 250 ms aggregation timer leaks timers under overlapping operations and sends more,
  smaller batches; the port debounces as the SDK documents. Payloads are identical.
* Other named differences (stack traces on `console.error`, the file-remote URL parser subset, response-body caps, proxy
  handling): [port/PORT.md](port/PORT.md).

Defects of the original and its SDK are ported, not fixed (for example `GET /info` is re-probed for every run operation when
it fails, the SDK's 429 `Retry-After` wait never runs, and a very short secret-named environment variable drops the later
`LANGSMITH_*` variables from run metadata). They are listed for the owner in [port/PORT.md](port/PORT.md).

MIT. Original © LangChain (extension and SDK); port © Michael Kinsy.
