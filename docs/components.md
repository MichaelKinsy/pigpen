# Components and Piglets

Pigpen is a monorepo of [PiG](https://github.com/MichaelKinsy/PiG) Piglets and reusable components. Piglets compose named agents. Component Packages can also be installed on their own. Every Package and every Piglet source is published on npm under `@pi-in-go`, and 14 Piglets have signed Binaries on [GitHub Releases](https://github.com/MichaelKinsy/pigpen/releases) (see [Releases](releases.md)). `index.json` lists a Piglet as `planned` until its signed receipt is recorded in `releases/`. That is a catalog status, not a statement that nothing is released.

This page describes what each component does and how it was proven. The directory tree is in [layout.md](layout.md).

## Go only

Every Piglet here is Go only: the extensions use PiG's Go SDK, so they fuse into a Piglet Binary. Each extension we port is its own installable [PiG Package](https://pi-in-go.dev) with the `pig-package` keyword. `npm run check` enforces the rule from the manifests. `go-only-exceptions.json` is empty now that the herdr reporter is a Go extension.

## Target: PiG 0.7.0

The target is PiG 0.7.0 (Pi 1.5.0; `pig --version` prints `0.7.0+1.5.0`). See [Pins](../CONTRIBUTING.md#pins-and-the-pig-target) for how the pin works and how to move it. Where a note names PiG 0.7 behavior, it is the target. A note that names an earlier PiG says so.

## Porting tools

The [shared porting Skill](../components/extension-port/README.md) supports direct Package installation. The focused [`pig-extension-porter`](../piglets/pig-extension-porter/README.md) Piglet selects it together with the [equivalence harness](../components/extension-equivalence/README.md) (a fused Go extension) from a generated local stage, and [`pig-porter`](../piglets/pig-porter/README.md) extends it. `npm run stage` removes the sibling-path blocker without changing PiG's path rules.

The first port made with the workflow is [`dirty-repo-guard`](../components/dirty-repo-guard/README.md), from Pi's examples. Eleven scripted scenarios give identical traces for the original under Pi 1.0.x and the Go port under PiG 0.4, and 26 of 26 mutations are caught. It is published as the Package `dirty-repo-guard` (npm `@pi-in-go/pigpen-dirty-repo-guard`, 0.1.1).

Every port has a row in the [ports list](../ports/README.md). See [The ports list](../CONTRIBUTING.md#the-ports-list) for what a row holds and how to change one.

## Ports and original Packages

[`pig-doctor`](../components/pig-doctor/README.md) is a Go Package: a `/doctor` command, a read-only `pig_doctor` tool and a `pig-doctor` command line. It finds and safely fixes cruft in a PiG setup: duplicate or broken extensions, unpruned caches, orphaned agent directories, legacy files. Fixes are backed up and restorable. `pig-with-batteries` selects it as a command only.

[`warden`](../components/warden/README.md) is a Go port of DevMortimer's pi-warden: guardrails that steer instead of interrupt. Irreversible calls are held before they run, off-task calls are flagged, and stuck loops and unverified done claims are called out. It is opt-in and shows what leaves the machine before anything does. It judges with TypeSafe Jev (through the shared Go client in [`components/typesafe`](../components/typesafe/README.md)), with the session's own model, or offline. The [`pig-warden`](../piglets/pig-warden/README.md) Piglet selects it. 19 live equivalence scenarios prove it against the original under Pi (see [`port/PORT.md`](../components/warden/port/PORT.md)).

[`websearch`](../components/websearch/README.md) is the first slice of a Go port of nicobailon's pi-web-access: the `web_search`, `fetch_content`, `get_search_content`, `source_check` and `web_enable` tools, with an SSRF guard on fetches and retained sources for bounded read-back. The rest of pi-web-access is deferred and named in its [`port/PORT.md`](../components/websearch/port/PORT.md).

[`ahp`](../components/ahp/README.md) is a Go port of Qusic's pi-ahp. It serves the running session to remote clients over Microsoft's Agent Host Protocol (WebSocket host, reconnect with replay, token authentication, origin check). It opens no listener unless asked and gives no remote filesystem or terminal unless you opt in. It is a standalone Package: no Piglet selects it. Its README has a Visual Studio Code section: the listener prints the `ws://<host>:<port>?tkn=<token>` address that VS Code's "Agents: Add Remote Agent Host..." takes. It is not yet verified with a live VS Code.

[`pi-typesafe`](../components/pi-typesafe/README.md) is a Go port of DevMortimer's pi-typesafe: the `typesafe_evaluate` tool (batched typed questions to TypeSafe Jev), a `/typesafe` playground and result renderers. It is off by default and discloses what leaves the machine. Its library half, [`pi-typesafe-api`](../components/pi-typesafe-api/README.md), is the typed Go API for other extension authors, and the [`pig-typesafe`](../piglets/pig-typesafe/README.md) Piglet selects the extension. Both sit on the shared client in [`components/typesafe`](../components/typesafe/README.md).

[`jev`](../components/jev/README.md) is a Go port of y0usaf's pi-jev: a typed judge for tool calls and tool output. It is off until you run `/jev on` (which shows what leaves the machine) and it fails open. It judges with the model PiG is configured with, or with the TypeSafe API through the shared client [`components/typesafe`](../components/typesafe/README.md). The [`jev` Piglet](../piglets/jev/README.md) builds a Piglet Binary. `pig-with-batteries` selects the Package too (off by default). The evidence and the ten corrections to the original are in [its port record](../components/jev/port/PORT.md).

The [`acp` Package](../components/acp/README.md), a Go port of pi-acp, lets an ACP editor such as Zed drive a PiG agent through the companion executable `pig-acp`. It has 124 upstream twins, 11 end-to-end scenarios against a real `pig` and the built [`acp` Piglet Binary](../piglets/acp/README.md), the ACP reference client with a schema-checked wire, and 104 of 105 mutants killed (one documented equivalent). The `acp` Package (npm 0.2.1) and the `acp` Piglet (signed Binary, release `acp/v0.1.1`) are published. The Piglet Binary does not speak ACP by itself.

The [`a2a` Package](../components/a2a/README.md) serves PiG tasks to A2A agents and calls remote ones, on the upstream Go SDK a2a-go (Apache-2.0), pinned to A2A protocol 1.0 and checked against kagent's own A2A server code. It is an original adapter, so there is no Pi oracle. Its proof is a fake-host test suite, an end-to-end run with a real `pig` and a local fake model, 109 mutations, host scenarios recorded from the port itself, and the kagent interop test. The listener stays off until configured and refuses to start without authentication. The [`a2a` Piglet](../piglets/a2a/README.md) selects it on its own, and `pig-with-batteries` selects it too (its tools appear, its listener stays off).

The [herdr reporter](../components/herdr/README.md) is a standalone extension Package that reports PiG's state to herdr. It works on its own with `pig install`. The [`herdr` Piglet](../piglets/herdr/README.md) that selects it is optional. Its herdr plugin and `herdr:blocked` listener are ported from [herdr-pig](https://github.com/Hai-Fai-Solutions/herdr-pig) by Christian Polzer and Hai-Fai Solutions (Apache-2.0; [credits](../piglets/herdr/README.md#credits)).

The full local integration scenario, including explicit empty tool scopes in RPC, print and JSON mode, passes on PiG 0.4.1 (`npm run test:porter`, `npm run test:tool-scope`). PiG 0.3.x still has that defect. See the [verification record](plan/pigpen-roadmap.md#resolved-print-and-json-tool-scoping).

## Packages from the author's own setup

Three Packages come from the author's own PiG configuration rather than from a port. Each is an independent `pig install`:

- [`session-ingest`](../components/session-ingest/README.md): the `read_session` tool, for querying session transcripts.
- [`context-info`](../components/context-info/README.md): `/context`, `/tools`, `/cost`, `/prompts` and an opt-in status footer.
- [`dev-skills`](../components/dev-skills/README.md): eight general development Skills adapted from public sources, with credit.

They are Go on the public SDK, like the ports.

## Games

The pig games are Go Packages moved from PiG Standard (Michael Kinsy, MIT): [`pig-runner`](../components/pig-runner/README.md) (`/runner`), [`angry-pigs`](../components/angry-pigs/README.md) (`/angry-pigs`) and their shared library [`pig-play`](../components/pig-play/README.md) (the pig sprite, pixel rasterizer, arcade scenery, terminal plumbing). Each installs on its own from npm (the tarball carries `pig-play`) or from a Pigpen checkout, where `pig-play` sits beside it. The [`pig-games`](../piglets/pig-games/README.md) Piglet selects both games, and a Binary of it builds locally.

A game starts only when you type its command. The games draw the pig you chose with PiG's built-in `/sprite`. The sprite login is part of PiG itself since 0.4.0, so Pigpen no longer ships its own.

An agent can play them too, opt-in and off by default. `/runner mcp on` or `/angry-pigs mcp on` (or `PIG_GAMES_MCP=1`) serves the game as an MCP server on `127.0.0.1` behind a random token. [`demo/jev-plays.js`](../demo/README.md) is a codemode script in which Jev plays both, about 10 seconds each, then stops them.

## pig-with-batteries

`pig-with-batteries` is published (signed Binaries up to release `pig-with-batteries/v0.1.2`), but the composition is not complete. It selects:

- the [herdr reporter](../components/herdr/README.md), which reports PiG's state to herdr and does nothing outside a herdr pane;
- PiG Runner and Angry Pigs (commands only: bundling is not activating);
- `session-ingest` and `context-info` (explicit: a tool the model calls on demand and slash commands that answer when typed);
- the [Pig Snake game](../components/pig-snake/README.md), a snake whose every apple adds another pig head to the line. It starts only when you type `/pig-snake`;
- the `/doctor` command of [`pig-doctor`](../components/pig-doctor/README.md) (nothing runs automatically);
- [`jev`](../components/jev/README.md) (off until `/jev on`);
- the [`websearch`](../components/websearch/README.md) first slice;
- the [a2a Package](../components/a2a/README.md) (A2A tools; no listener until configured);
- [`pig-music`](../components/pig-music/README.md), the `/music` player (nothing starts until you type it);
- an empty build-pipeline fixture (`seed-check`) that adds no tools, commands, or hooks.

The separate [`herdr` Piglet](../piglets/herdr/README.md) selects the same Package on its own. The herdr reporter is a Go extension, so batteries and the `herdr` Piglet both build a Binary locally with the reviewed PiG (see [RELEASE-BLOCKERS.md](maintainers/RELEASE-BLOCKERS.md)). The owner approved the [roadmap](maintainers/plans/pigpen-roadmap.md), but the capability ports and the rest of the battery composition are not complete.

## Licences and credits

Every Package is MIT, except where its own `package.json` and `LICENSE` say otherwise (the root README lists those). Whose copyright the `LICENSE` names depends on where the code came from.

- **Pigpen code, including the Packages moved from PiG** (the repository `LICENSE`, and the `LICENSE` of `a2a`, `angry-pigs`, `context-info`, `extension-equivalence`, `extension-port`, `herdr`, `pig-doctor`, `pig-play`, `pig-runner`, `pig-snake` and `session-ingest`): Copyright (c) 2026 Michael Kinsy. PiG is credited in each moved Package's `CREDITS.md` and `provenance.json`.
- **Ports of third-party upstreams** (`acp`, `ahp`, `dirty-repo-guard`, `jev`, `typesafe`, `warden`, `websearch`, `pi-typesafe`, `pi-typesafe-api` and `dev-skills`): the `LICENSE` lists the upstream copyright lines verbatim and then "Copyright (c) 2026 Michael Kinsy (the Go port)". The upstream's own `LICENSE` is kept unchanged next to the original source, and the credit is in each Package's `CREDITS.md`.

Apache-2.0 upstreams (a2a-go, the ACP typescript-sdk, agent-stuff and WorkflowEvals) keep their licence files, and none has a NOTICE file at its pin. The upstream, pinned commit, licence and author of every port and move are in [ports/README.md](../ports/README.md), generated from `ports/ports.json`.

Data-flow notes that the Packages state themselves: `pi-typesafe` and `warden` send content to `api.typesafe.ai` unless the own-model backend (the model PiG is configured with) is selected, or for `warden` its offline judge. `jev` defaults to the own-model backend and sends to `api.typesafe.ai` only when its TypeSafe backend is chosen. All three are off until enabled and show what leaves the machine before anything does. `websearch` sends a search to the keyless Exa MCP (`mcp.exa.ai`) when no provider is configured, as the original does, once the model has called `web_enable` and then `web_search`, and `fetch_content` fetches the URL it is given. `a2a` sends to the A2A agents you configure and opens a listener only when configured.
