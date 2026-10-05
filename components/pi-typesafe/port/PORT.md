# Port record: pi-typesafe

| Input | Identity |
|---|---|
| Original | https://github.com/DevMortimer/pi-typesafe, commit `ed439f834665ad6fc652787f5ba7c23852566d49` (0.8.0), MIT, Ryan Gapac; vendored unmodified in `port/oracle/` (no `.github`, `package-lock.json` or preview image). Baseline: `npm test` 134/134 in that directory. |
| Oracle | Pi 0.87.1 (the npm release), Node 24.19.0; the extension loaded from `port/oracle/src/extension.ts` with `npm install` dependencies (`@typesafe-ai/sdk` 0.6.0, `typebox`) |
| Target | PiG 0.3.0+0.87.1 (source commit `63c6ba456`), go1.27.1, shared client `components/typesafe` (port of `@typesafe-ai/sdk` 0.6.0 and system-one-adapter) |
| Original's tests | 134 cases in 12 files; the ledger is `port/upstream-tests.json` |

Two Packages carry the port: `components/pi-typesafe` (extension, this record) and `components/pi-typesafe-api` (the typed API).

## File mapping

| Original (`port/oracle/`) | Go |
|---|---|
| `src/index.ts` (exports) | the exported API of `pi-typesafe-api` (package `pitypesafe`) |
| `src/client.ts` (`createTypeSafe`, budget, ledger, model mapping, backend transport) | `client.go` (`New`, `Evaluate`, `EvaluateRaw`, `ListModels`, `backendDoer`); the SDK calls are the shared client's |
| `src/backends.ts` | `backends.go` (+ the own-model backend, `hostmodel/`) |
| `src/schema.ts` (typebox schema, admission) | `schema.go`, `evaluation_schema.json` (the schema TypeBox serializes to, generated from the original), `json.go` (ordered JSON) |
| `src/errors.ts` | `errors.go` |
| `src/credentials.ts`, `src/auth.ts`, `src/usage.ts` | `credentials.go`, `auth.go`, `usage.go` |
| `src/batch.ts`, `src/ask.ts`, `src/calibrate.ts` | `batch.go`, `ask.go`, `calibrate.go` |
| `src/login.ts`, `src/key-prompt.ts`, `src/ui.ts` | `ui/login.go`, `ui/keyprompt.go` |
| `src/extension.ts` | `extensions/pi-typesafe/{extension,tool,command,format}.go` |
| `extensions/index.js`, `examples/decision-extension.ts` | the `pi.extensions` entry in `package.json`; the example is not ported (its content is in the README) |
| `tests/*.test.ts` | Go twins: `pi-typesafe-api/*_test.go`, `pi-typesafe-api/ui/ui_test.go`, `extensions/pi-typesafe/extension_test.go` |

## Twins and scenarios

- **134 exact twins, 0 skipped** (`pigeq twins check`: 111 in `pi-typesafe-api`, 7 in `pi-typesafe-api/ui`, 16 in the extension).
  Sub-cases with no Go counterpart are named inside their twin: JavaScript getters, `Date`, `BigInt`, `NaN` options, `null` (Go nil is
  the default), a value of the wrong type in a struct field (covered through map-form endpoints).
- **Layer 2: 12 scenarios** (`port/scenarios`), golden traces recorded from the original under Pi 0.87.1 and each also identical under
  PiG's Node runtime; the Go port reproduces every one (`pigeq check`: 14/14 with the gap and exec pseudo-scenarios). The scenarios
  cover what needs no network: status, setup, login refusal, logout, consent accepted and declined, the disabled tool called by a
  model, the playground's three no-send paths, and actions followed by extra words (`trailing-words`, added by the review). The harness reserves `PI_*` variables, so `PI_TYPESAFE_ENABLED` scenarios
  cannot be scripted; those (headless opt-in, callouts) are layer-1 cases with the fake host. **Network paths** (a successful
  evaluation, HTTP errors, model lists) run against a fake TypeSafe HTTP server in Go; the original cannot be pointed at one (it
  deliberately ignores `TYPESAFE_BASE_URL`), so those are proven by the twins, not by traces.
- **Mutations** (`port/mutations.json`, `pigeq mutate --unit`): see Results.

## Deliberate differences (each named, none silent)

1. **PiG's directories.** State lives in `<PiG agent dir>/pi-typesafe/` (`PIG_CODING_AGENT_DIR`, else `$PIG_HOME/agent`, else `~/.pig/agent`;
   Pi's `PI_CODING_AGENT_DIR`/`~/.pi/agent` with `PIG_USE_PI_DIRS=1`) instead of Pi's fixed directory (PiG divergence D2). Twin
   "credentials live under Pi's agent directory" asserts PiG's directory.
2. **Own-model backend** (owner requirement): `ownmodel` in the registry, `PI_TYPESAFE_BACKEND`, and `/typesafe backend [typesafe|ownmodel]`
   (completed, not in the usage text). Unknown-backend text therefore lists `ownmodel`. Disclosure and consent follow the destination.
3. **Zero values mean "default"** in `Options` (timeout, model, maxRequests, ...); negatives are rejected. The original's `timeoutMs: 0`, `NaN`
   and `model: ""` rejections have no Go counterpart.
4. **Key order in tool arguments.** PiG's Go SDK decodes tool arguments into maps, so question ids and Choice options arrive sorted, where JavaScript
   keeps insertion order. Typed (`typesafe.SystemOneRequest`) and JSON-text (`ParseJSON`, the playground) paths keep order end to end.
   The tool's `details` carry an extra `order` array so the renderer shows answers in question order. Reported as an SDK gap.
5. **Validation wording.** Request-level failures (missing properties, counts, non-objects) use TypeBox's words (`testdata/typebox-messages.json`
   compares them with the original); deep union-branch failures name the offending path with different words. The host validates tool
   arguments against the same schema first, so the difference is reachable only from the playground and the library.
6. **Model list** entries are read leniently as in the original; the Go client's typed decoder is bypassed (`ListRaw`).
7. **Answer order** in `Evaluation` comes from the request's questions (`Order`), since answers are a map.
8. **Scheduling.** Two requests started together race for the last allowed request; JavaScript ran the first to its cap check first. The batch twin "a daily cap stops the batch with the cap named" therefore asserts that exactly one of the two is refused, naming the cap.
9. **Consent across a concurrent backend switch** (review). The original runs on one thread, so its consent check and its send cannot be
   separated; here a `/typesafe backend` command can run while a tool call or the `/typesafe test` dialog is in progress. A switch
   counts as a new destination: a call or dialog admitted before it stops with "The judgment backend changed after this request was
   admitted; nothing was sent." (`clientFor`, `review_test.go`). A call already sending is not cancelled, as with `/typesafe disable`.
10. **Loopback http: hosts** (review). The 127.0.0.0/8 rule also requires the host to parse as an IP address. WHATWG `URL` rejects
   `127.999.0.1`, and Go's `url.Parse` accepts it as a name that DNS would resolve. The Go rule is stricter than the original's for forms
   WHATWG normalizes (`127.1`, `0x7f.0.0.1`, `127.0.0.01`): they are refused, not rewritten.
11. **Own-model errors** (review, PiG-only backend). Setup failures (no model selected, no Context) are configuration errors, so their
   reason reaches the operator, and the callout names the own-model backend instead of a key.

SDK gap check (`pigeq gaps`): no blocking gaps. PARTIAL stand-ins: entry renderer, tool call/result renderers and prompt snippet render
lines instead of pi-tui Components (host limit shared by every Go extension).

## Findings about the hosts and the harness

1. A Package with an extension nested under a library root (`go.mod` at the Package root) breaks PiG's packed runner (it derives the
   import path from the parent module) and `pigeq mutate` (the parent is copied as a sibling). Split the library into its own Package.
2. `pigeq env`, `pigeq mutate --unit` and `scripts/go-modules.mjs` wrote a go.work in which the SDK is a `replace`; a library module that
   requires another workspace module at `v0.0.0` then fails with "unknown revision". Fixed here (versioned replace for every used module,
   test first) and reported to the porter driver.
3. The Go SDK has no raw access to tool arguments (see difference 4).

## Results (this revision)

- `go test -race` in `pi-typesafe-api` (root, `ui`) and the extension: pass. `GOOS=windows|darwin|linux go vet`: see the report.
- `pigeq check` against the Pi-recorded goldens: 12 of 12 scenarios plus `port-gaps` and `exec-coverage`.
- **Mutations (extension)**: `pigeq mutate --unit`, 50 mutants (4 added by the review), **50 killed** (33 by the fake-host tests alone; the scenarios kill the rest or share the kill).
  The first run left 13 unproven; the added `branches_test.go` cases (callout channel and level, 403, logout's auth record, unusable stored
  key, consent titles, entries, renderers, tool content) killed them, and three mutants that did not compile were rewritten.
- **Mutations (library)**: `port/library-mutations.py`, 22 applied mutants over client, backends, credentials, auth, usage, schema, errors, batch,
  calibrate and ask: all killed after `gaps_test.go` was added, except mutant 5, which is equivalent (a model containing `/` cannot match either
  OpenRouter rewrite rule) and was dropped.

## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the original under Pi 1.0.1 (host check: identical under PiG's Node runtime), with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.
