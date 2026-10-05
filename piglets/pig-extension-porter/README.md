# pig-extension-porter

The focused porter for Pi TypeScript extensions. **Go only**: it ports to a Go extension on
PiG's public SDK, and everything it selects is a Go SDK extension or a Skill, so the Piglet
builds a Binary with its extension fused.

Most people who port an extension want this Piglet. [`pig-porter`](../pig-porter/README.md)
extends it and is the umbrella for future porting work.

## What it selects

| Resource | From | Role |
|---|---|---|
| Skill `pigpen-pi-extension-port` | Package [`extension-port`](../../components/extension-port/README.md) | The strict workflow: SDK gap check, tests first (fake host and scenarios), Go implementation, differential equivalence against the original, mutation check, binary build, Package with provenance. Includes the TypeScript-to-Go pattern catalog. |
| Extension `extension-equivalence` (Go, fused) | Package [`extension-equivalence`](../../components/extension-equivalence/README.md) | Tools `equivalence_gaps` (TypeScript or Go source), `equivalence_run`, `equivalence_twins`, `equivalence_diff`: the Skill's proof steps. The `pigeq` command line (session setup: `pigeq env`, `pigeq source`; `pigeq llm`) is built from a checkout with `PIG_BIN=... npm run build:pigeq`. |

The built-in tools stay at PiG's defaults because porting reads and writes files and runs
`go`, `pig` and `pi`. Discovery of ambient extensions and Skills is off. Model choice is
left to the user.

## Run from a checkout

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-extension-porter/piglet.yaml
pig --piglet dist/staged/piglets/pig-extension-porter/piglet.yaml
```

Set up an isolated session first (`pigeq env`, see the Skill's "Set up the session"), then, in the session:

```text
/skill:pigpen-pi-extension-port port <path-to-original.ts> to components/<name>
```

Build a Piglet Binary with one command from a checkout:

```sh
npm ci --ignore-scripts
PIG_BIN=/path/to/pig PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  npm run build:piglet -- pig-extension-porter --out ./pig-extension-porter
```

`--out` defaults to `dist/bin/pig-extension-porter`. `PIG_SOURCE_ROOT` is a git checkout of
the PiG source that the `pig` was built from (PiG's native Binary builder compiles the Piglet
against it). The command runs the quality gates, stages the Piglet, then runs
`pig piglet build dist/staged/piglets/pig-extension-porter/piglet.yaml --format binary --targets <os>/<arch>`.

Do **not** point `pig piglet build` at `piglets/pig-extension-porter/piglet.yaml`. That authored
manifest selects Packages in `../../components/`, and PiG only accepts local Packages inside
the Piglet's own directory (`... escapes the Piglet anchor`). `npm run stage` (called by the
command above) copies the shared Packages next to the manifest and rewrites them to
`local:./packages/<alias>`; the staged copy is what PiG builds. If you build by hand, run
`npm run stage` first and use the path under `dist/staged/`.

`build.extensionRealization: fused` makes the build fail if the extension would be a
subprocess, which keeps the Go-only rule enforced by PiG itself.

## What it does not port

Packages whose function PiG has built in are skipped, not ported. The first example is
MCP: it is built into PiG from 0.4.0, so `pi-mcp-adapter` is not ported and no Piglet here
selects an MCP adapter. The Skill's section 0 carries the rule.

## Worked example

[`components/dirty-repo-guard`](../../components/dirty-repo-guard/README.md) was ported
with this workflow from Pi's `dirty-repo-guard.ts` example. Its `port/PORT.md` shows the
mapping table, and its scenarios, Pi-recorded traces and mutation list are the evidence.

## Requirements

`go` (source builds and the harness), `node` (the TypeScript lanes), a `pi` and a `pig`
executable to compare (`PIGEQ_PI`, `PIGEQ_PIG`). The traces compare a real Pi with a real
PiG; nothing is mocked.

## Status

Source only. No Piglet Binary release and no supported platform. The manifest lists
`linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64` and `windows/amd64`, but only
`linux/amd64` has been built and run; the other four are compile-checked only. The version
is a development default. Original composition:
[MIT](../../LICENSE); the Skill's provenance is in its
[Package](../../components/extension-port/CREDITS.md).
