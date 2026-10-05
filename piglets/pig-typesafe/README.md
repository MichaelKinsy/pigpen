# pig-typesafe

Batched TypeSafe (Jev) judgments for the agent, as a Go Piglet: the `typesafe_evaluate` tool, the `/typesafe` command (login,
consent, status, sample test, playground) and the result renderers, from the Package
[`pi-typesafe`](../../components/pi-typesafe/README.md) (a port of [pi-typesafe](https://github.com/DevMortimer/pi-typesafe) by Ryan Gapac).

## Off by default, and where content goes

Selecting this Piglet does not turn anything on. The tool refuses every call until the operator runs `/typesafe enable` and
confirms the notice (or sets `PI_TYPESAFE_ENABLED=1` for a headless run). **Submitted state and questions go to `api.typesafe.ai`
and may incur charges**, unless the **own-model backend** is selected (`PI_TYPESAFE_BACKEND=ownmodel`, or `/typesafe backend
ownmodel`): then they go to the provider of the model PiG is configured with, and nothing goes to TypeSafe. Do not submit secrets.

## What it selects

| Resource | From | Role |
|---|---|---|
| Extension `pi-typesafe` (Go, fused) | Package `pi-typesafe` | The tool, the command, the renderers. |
| (libraries) | Packages `pi-typesafe-api`, `typesafe` | Imported by the extension; they carry no extension of their own. |

Built-in tools stay at PiG's defaults; discovery of ambient extensions and Skills is off.

## Run from a checkout

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-typesafe/piglet.yaml
PIG_BIN=pig PIG_SOURCE_ROOT=<git checkout of the PiG source> npm run build:piglet -- pig-typesafe --out dist/bin/pig-typesafe
```

Isolate the run (temporary `HOME`, `PIG_HOME`, `PIG_CODING_AGENT_DIR`) unless you want your own PiG state.
