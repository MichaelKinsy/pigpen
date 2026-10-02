# Pigpen

[Pigpen](https://github.com/MichaelKinsy/pigpen) is a monorepo of
[PiG](https://github.com/MichaelKinsy/PiG) Piglets and reusable components maintained
by Michael Kinsy. Piglets compose named agents. Component Packages can also be
installed independently. Planned catalog entries are not published releases.
The [herdr reporter](components/herdr/README.md) is a standalone extension Package
that reports PiG's state to herdr. It works on its own with `pig install`; the
[`herdr` Piglet](piglets/herdr/README.md) that selects it is optional.

## Current status

**Every Piglet here is Go only** (extensions on PiG's Go SDK, so they fuse into a
Piglet Binary), and each extension we port is its own installable
[PiG Package](https://pi-in-go.dev) with the `pig-package` keyword. `npm run check` enforces the rule from
the manifests; `go-only-exceptions.json` is empty now that the herdr reporter is a Go extension.

The [shared porting Skill](components/extension-port/README.md) supports direct
Package installation. The focused
[`pig-extension-porter`](piglets/pig-extension-porter/README.md) Piglet selects it together
with the [equivalence harness](components/extension-equivalence/README.md) (a fused Go
extension) from a generated local stage, and [`pig-porter`](piglets/pig-porter/README.md)
extends it. `npm run stage` removes the sibling-path blocker without changing PiG's path
rules. The first port made with the workflow is
[`dirty-repo-guard`](components/dirty-repo-guard/README.md), from Pi's examples: 11
scripted scenarios give identical traces for the original under Pi 0.87.1 and the Go port
under PiG 0.3.0, and 26 of 26 mutations are caught. Nothing here is a published release.

**Every port has a row in the [ports list](ports/README.md)** (`ports/ports.json`, schema
`ports/ports.schema.json`): upstream repository, the commit it is pinned to, license, upstream
author, target Package, port lane, status (`queued`, `porting`, `review`, `done`) and credit text.
`npm run check` validates it, including that an existing Package's `provenance.json` carries the
same pin and license (and, for a port or move, records the row's upstream); `npm run generate` rewrites `ports/README.md`. The porting Skill takes one
row as its input (`port row <id>`; see [CONTRIBUTING.md](CONTRIBUTING.md#the-ports-list)).

[`pig-doctor`](components/pig-doctor/README.md) is a Go Package (a `/doctor` command, a read-only
`pig_doctor` tool and a `pig-doctor` command line) that finds and safely fixes cruft in a PiG setup:
duplicate or broken extensions, unpruned caches, orphaned agent directories, legacy files. Fixes
are backed up and restorable; `pig-with-batteries` selects it as a command only.

[`warden`](components/warden/README.md) is a Go port of DevMortimer's pi-warden: guardrails that steer instead
of interrupt (irreversible calls held before they run, off-task calls flagged, stuck loops and unverified done
claims called out). It is opt-in, shows what leaves the machine before anything does, and judges with TypeSafe Jev
(through the shared Go client in [`components/typesafe`](components/typesafe/README.md)), with the session's own
model, or offline. The [`pig-warden`](piglets/pig-warden/README.md) Piglet selects it. It is proven against the original
under Pi by 19 live equivalence scenarios (see [`port/PORT.md`](components/warden/port/PORT.md)).

[`websearch`](components/websearch/README.md) is the first slice of a Go port of nicobailon's pi-web-access: the
`web_search`, `fetch_content`, `get_search_content`, `source_check` and `web_enable` tools, with an SSRF guard on fetches and
retained sources for bounded read-back. The rest of pi-web-access is deferred and named in its
[`port/PORT.md`](components/websearch/port/PORT.md).

[`ahp`](components/ahp/README.md) is a Go port of Qusic's pi-ahp: it serves the running session to remote clients over
Microsoft's Agent Host Protocol (WebSocket host, reconnect with replay, token authentication, origin check). It opens no
listener unless asked and gives no remote filesystem or terminal unless you opt in. It is a standalone Package: no Piglet
selects it.

[`pi-typesafe`](components/pi-typesafe/README.md) is a Go port of DevMortimer's pi-typesafe: the
`typesafe_evaluate` tool (batched typed questions to TypeSafe Jev), a `/typesafe` playground and result
renderers. It is off by default and discloses what leaves the machine. Its library half,
[`pi-typesafe-api`](components/pi-typesafe-api/README.md), is the typed Go API for other extension authors, and the
[`pig-typesafe`](piglets/pig-typesafe/README.md) Piglet selects the extension. Both sit on the shared client in
[`components/typesafe`](components/typesafe/README.md).

The full local integration scenario passes with PiG commit `22755205a` (the same
fix is `04eab6dd2` in the release-all-next lineage, verified at `63c6ba456`), including
explicit empty tool scopes. Older builds still have that defect. See the
[verification record](docs/plan/pigpen-roadmap.md#resolved-print-and-json-tool-scoping)
for the tested revision and pending CI pin update.

Three Packages come from the author's own PiG configuration rather than from a port,
each an independent `pig install`:
[`session-ingest`](components/session-ingest/README.md) (the `read_session` tool for
querying session transcripts), [`context-info`](components/context-info/README.md)
(`/context`, `/tools`, `/cost`, `/prompts`, and an opt-in status footer) and
[`dev-skills`](components/dev-skills/README.md) (eight general development Skills
adapted from public sources, with credit). They are Go on the public SDK, like the ports.

The pig games are Go Packages moved from PiG Standard (Michael Kinsy, MIT):
[`pig-runner`](components/pig-runner/README.md) (`/runner`),
[`angry-pigs`](components/angry-pigs/README.md) (`/angry-pigs`) and their shared library
[`pig-play`](components/pig-play/README.md) (the pig sprite, pixel rasterizer, arcade scenery,
terminal plumbing). Each installs on its own from a Pigpen checkout, where `pig-play` sits beside
it (a copy of a game Package without `../pig-play` does not build); the
[`pig-games`](piglets/pig-games/README.md) Piglet selects both games, and a Binary of it builds
locally. A game starts only when you type its command. The games draw the pig you chose with PiG's
built-in `/sprite` (the sprite login is part of PiG itself since 0.4.0, so Pigpen no longer ships its own).
An agent can play them too, opt-in and off by default: `/runner mcp on` or `/angry-pigs mcp on` (or `PIG_GAMES_MCP=1`) serves
the game as an MCP server on `127.0.0.1` behind a random token, and [`demo/jev-plays.js`](demo/README.md) is a codemode
script in which Jev plays both, about 10 seconds each, then stops them.

The [`acp` Package](components/acp/README.md), a Go port of pi-acp, lets an ACP editor such as Zed drive a PiG agent
through the companion executable `pig-acp` (124 upstream twins, 11 end-to-end scenarios against a real `pig` and the built
[`acp` Piglet Binary](piglets/acp/README.md), the ACP reference client with a schema-checked wire, 104 of 105 mutants killed and
one documented equivalent). Nothing here is a published release, and the Piglet Binary does not speak ACP by itself.

**[`jev`](components/jev/README.md)** is a Go port of y0usaf's pi-jev: a typed judge for tool calls
and tool output that is off until you run `/jev on` (which shows what leaves the machine), fails open, and
judges with the model PiG is configured with or the TypeSafe API through the shared client
[`components/typesafe`](components/typesafe/README.md). The [`jev` Piglet](piglets/jev/README.md) builds a Piglet
Binary; `pig-with-batteries` selects the Package too (off by default). Evidence and the ten corrections to the
original are in [its port record](components/jev/port/PORT.md).

The [`a2a` Package](components/a2a/README.md) serves PiG tasks to A2A agents and calls remote ones, on the upstream Go SDK
a2a-go (Apache-2.0), pinned to A2A protocol 1.0 and checked against kagent's own A2A server code. It is an original
adapter, so there is no Pi oracle: its proof is a fake-host test suite, an end-to-end run with a real `pig` and a local fake
model, 109 mutations, host scenarios recorded from the port itself, and the kagent interop test. The listener stays off until
configured and refuses to start without authentication. The [`a2a` Piglet](piglets/a2a/README.md) selects it on its own, and `pig-with-batteries` selects it too
(its tools appear, its listener stays off).

**`pig-with-batteries` remains source only, not an installable release.** It
selects the [herdr reporter](components/herdr/README.md), which reports PiG's state to
herdr and does nothing outside a herdr pane, PiG Runner and Angry Pigs
(commands only: bundling is not activating), `session-ingest` and `context-info`
(explicit: a tool the model calls on demand and slash commands that answer when typed), the [Pig Snake game](components/pig-snake/README.md)
(a snake whose every apple adds another pig head to the line; it starts only when you type
`/pig-snake`), the `/doctor` command of [`pig-doctor`](components/pig-doctor/README.md) (nothing runs
automatically), [`jev`](components/jev/README.md) (off until `/jev on`), the [`websearch`](components/websearch/README.md) first slice, the [a2a Package](components/a2a/README.md) (A2A tools; no listener until
configured), plus an empty build-pipeline fixture
(`seed-check`) that adds no tools, commands, or hooks. The separate
[`herdr` Piglet](piglets/herdr/README.md) selects the same Package on its own.
The herdr reporter is a Go extension, so batteries and the `herdr` Piglet both build a
Binary locally with the reviewed PiG (see [RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md)).
The owner approved the [roadmap](docs/plan/pigpen-roadmap.md), but the capability
ports and the rest of the battery composition are not complete.

No Piglet Binary, release signing key, supported binary platform, or completed
batteries-included composition is published here. Development versions and build
targets are not release claims. The newer PiG docs describe monorepo distribution,
but Pigpen's release guard remains closed pending its own release verification.
See [RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md).

## Pull or add a Piglet — after support and releases are ready

These are future instructions, **not commands to install this scaffold today**.
Choose a published Piglet/version, review its source and requirements, and use
only the commands recorded in its generated catalog entry and release notes.

- **Signed binary:** `pig piglet pull <signed-release-index-url>` verifies the
  signed index, checksums and Binary before installation. Direct HTTPS index
  URLs already work in PiG; Pigpen has no published index to pull yet. A future
  release's URL will use its own tag namespace, for example
  `pig-with-batteries/v0.1.0`, rather than a repository-wide `v0.1.0`.
  Confirm the public signing key with the publisher; a signature alone does not
  prove who holds the key.
- **Editable source:** `pig piglet add <version-pinned-source-reference>` adds a
  named Piglet; select it with `pig --piglet pig-with-batteries`. The reference
  must select `piglets/pig-with-batteries/` in this repository, not an arbitrary
  root manifest. Published compositions must select approved immutable Package
  references. Local staging alone does not verify remote source installation.
- Do **not** use `pig piglet pull github:MichaelKinsy/pigpen@0.1.0` for a named
  Piglet. Use the reviewed namespace-specific command recorded with its release.
  No such Pigpen release is available yet.

## Run locally

From the checkout root:

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-porter/piglet.yaml
pig --piglet dist/staged/piglets/pig-porter/piglet.yaml
```

Each authored Resource has one owner under `components/` or a Piglet's private
Resource directories. An authored composition may reference a shared Package:

```yaml
packages:
  extension-port: local:../../components/extension-port
skills:
  - name: pigpen-pi-extension-port
    origins: [package:extension-port]
```

PiG does not accept that sibling path directly. Staging materializes the selected
Package inside `dist/staged/piglets/<name>/packages/<alias>/` and rewrites only its
Package source. The output is an ordinary PiG manifest with no sibling paths.
The original manifest and Package remain unchanged. Multiple Piglets can select
the same authored Package without maintaining copied source.

Staging copies local source, licenses, and executable permissions. It excludes
`.git`, `node_modules`, and `dist` trees. Shared references must name a direct
`components/<name>/` directory. Staging rejects escaping references, symlinks,
missing local Packages, invalid YAML, and generated-destination collisions.
PiG still validates the resulting manifests and selected members.

Every Piglet manifest lists five build targets: `linux/amd64`, `linux/arm64`,
`darwin/arm64`, `darwin/amd64` and `windows/amd64`. `pig piglet build` builds only the
machine it runs on, so a local Binary build names that machine:
`npm run build:piglet -- <piglet-name>` does this for you, and a direct
`pig piglet build ... --format binary` needs `--targets <os>/<arch>`. The release build
runs one native runner per target (`npm run build-matrix`). Only `linux/amd64` Binaries
have been built and run so far; see `RELEASE-NOTES.md` for what is and is not verified.

Edit source, then stage again and restart the selected Piglet. Restaging replaces
`dist/staged/`, removing stale files. A failed preflight leaves the previous stage
intact, so never treat an old stage as verification of a failed edit. Do not edit
or commit generated copies. This is a local development process, not a publisher.

## Layout

```text
catalog.config.json                 MichaelKinsy/pigpen, branch main
components/extension-port/          directly installable shared Skill Package (the porting
                                    workflow, TS-to-Go pattern catalog, fake-host template)
components/extension-equivalence/   directly installable Go extension Package: the
                                    differential harness (pigeq) and SDK gap scanner
demo/                               codemode demo scripts (jev-plays.js: Jev plays both pig games over MCP)
ports/                              the ports list: ports.json (the dispatch list), ports.schema.json,
                                    README.md (GENERATED table; npm run generate)
components/dirty-repo-guard/        a Go port (Pi's dirty-repo-guard example) as a Package,
                                    with its evidence in port/
components/typesafe/                shared Go client for the TypeSafe API, plus an own-model
                                    backend (libraries only, no extension)
components/warden/                  a Go port of pi-warden as a Package, with its evidence in port/
components/websearch/                a Go port (first slice) of pi-web-access as a Package, with its evidence in port/
components/ahp/                     a Go port of pi-ahp as a Package (Agent Host Protocol host), with its evidence in proof/
components/pi-typesafe/             a Go port of pi-typesafe as a Package (typesafe_evaluate, /typesafe), with its evidence in port/
components/pi-typesafe-api/         the typed Go API half of the pi-typesafe port (library, no extension)
components/pig-doctor/              /doctor, the read-only pig_doctor tool and the pig-doctor command line: find and fix cruft
components/herdr/                   directly installable herdr reporter Package
                                    (Go extension and its tests)
components/pig-play/                Go library Package: pig sprite, pixel, arcade, termgame, gamemcp (no extension)
components/pig-runner/              PiG Runner, /runner (Go, from PiG Standard)
components/angry-pigs/              Angry Pigs, /angry-pigs (Go, from PiG Standard)
components/pig-snake/               directly installable Go game Package: a snake whose head is
                                    the PiG pig and whose every apple adds another pig to the
                                    herd (explicit /pig-snake command; evidence in port/)
components/acp/                     Agent Client Protocol for PiG: the pig-acp companion and
                                    the /acp extension, a Go port of pi-acp with its evidence in port/
components/jev/                     a Go port of pi-jev as a Package (typed judge, off until /jev on),
                                    with its evidence in port/
components/session-ingest/          directly installable Go extension Package: read_session
components/context-info/            directly installable Go extension Package: context and
                                    cost inspection commands, opt-in footer
components/dev-skills/              directly installable Skill Package (adapted, credited)
components/a2a/                     directly installable Go Package: A2A (Agent2Agent) server and client on
                                    a2a-go v2.6.0, listener off unless configured (evidence in port/)
piglets/
  a2a/                              Go Piglet selecting components/a2a (also selected by pig-with-batteries)
  pig-extension-porter/             source-only, Go-only focused porter Piglet
  pig-porter/                       source-only umbrella; extends pig-extension-porter
  pig-games/                        source-only: PiG Runner and Angry Pigs
  jev/                              Go Piglet selecting components/jev (also selected by pig-with-batteries)
  pig-warden/                       source-only, Go-only: PiG with the opt-in warden guardrails
  pig-typesafe/                     source-only, Go-only: PiG with the opt-in typesafe_evaluate tool and /typesafe
  pig-with-batteries/
    piglet.yaml                     authored composition, input to staging
    catalog.json                    presentation metadata, not a PiG manifest
                                    (selects herdr, the games, Pig Snake, session-ingest, context-info, pig-doctor, jev, a2a, websearch, plus the fixture)
    extensions/                     empty build fixture, not curated capability
    skills/                         owned source, initially empty
    prompts/                        owned source, initially empty
    README.md
  acp/                              PiG for ACP editors (selects components/acp; source only)
  herdr/                            PiG plus the herdr agent-state reporter
    piglet.yaml, catalog.json, README.md, tests/   (tests/ runs real pig: npm run test:pig)
scripts/stage-piglets.mjs            materializes local shared Packages
dist/staged/                        GENERATED, gitignored local Piglet trees
scripts/generate-index.mjs           derives catalog data from authored compositions
scripts/validate-manifests.mjs       validates Packages and staged manifests with PiG
scripts/build-matrix.mjs             target metadata and closed release guard
scripts/ports.mjs                    validates ports/ports.json, renders ports/README.md, and reads and
                                    updates rows (show, list, set-status, provenance, verify-pins)
index.json                          GENERATED; never hand-edit
index.schema.json                   machine-readable catalog format
.github/workflows/ci.yml             validation only; no publishing or signing
RELEASE-BLOCKERS.md                  exact core PiG work needed for releases
CONTRIBUTING.md                     changes, checks, sign-off and issue routing
SECURITY.md                         private security reporting guidance
```

## Maintain and validate

Use Node.js 24 and the reviewed PiG build-tool commit pinned in CI. PiG is the
authority for the closed Piglet manifest and resource contract.

```sh
npm ci --ignore-scripts
npm test
npm run generate
npm run check
PIG_BIN=pig npm run validate
PIG_BIN=pig npm run test:porter
PIG_BIN=pig npm run test:go-ports -- -race     # go vet and go test for every Go extension module
PIG_BIN=pig npm run test:port                  # ports vs recorded Pi traces (PI_BIN=pi re-runs Pi)
KAGENT_GO_DIR=<kagent>/go PIG_BIN=pig node scripts/interop-kagent.mjs   # a2a against kagent's A2A server package
```

`pig install --validate-only` takes an extension directory (`components/<name>/extensions/<dir>`); a Package root is checked by `pig package validate`.

`PIG_BIN` is required by every script and test that runs a real pig, and it must be the pig Pigpen targets
(`scripts/pig-requirement.json`: the same `major.minor`, at least that patch). Pigpen never falls back to whatever `pig`
is on `PATH`; an older or newer pig stops with a message that names it and says how to build the right one
(`go build -o /path/to/pig ./cmd/pig` in the PiG checkout). `PIG_BIN=pig` therefore works only when the `pig` on `PATH` is that build.

The generator checks duplicate YAML keys and presentation metadata. Names and
descriptions come from the manifests, and the index records their SHA-256
identities. `index.json` is committed alongside source changes for predictable
static-site consumption. A manifest's intended targets never become binary
availability claims. The generator permits only planned entries until verified
publisher receipts can supply real release metadata.

`npm test` runs the quality-gate cases, the Go-only gate, the ports-list cases, and the fake-host template and
port-layout checks. `npm run check` also runs the Go-only gate and the ports-list check. `PIG_BIN=pig npm run test:go` runs the herdr
reporter's Go tests, and `PIG_BIN=pig npm run test:go-ports` runs `go vet` and `go test` for every
Go extension module (both need a Go toolchain and PiG's SDK; not run in CI until the pin moves).
`PIG_BIN=pig npm run test:pig` additionally drives a real `pig` in tmux against a fake herdr, once for the `herdr` Piglet and once for `pig-with-batteries`;
with `PIG_SOURCE_ROOT` set it also builds each into a Binary and runs that
(needs tmux and Go; stages first; not run in CI).
`PIG_BIN=pig npm run test:pig-snake` (or `PIG_SNAKE_BINARY=<a Piglet Binary>`) plays Pig Snake in a real terminal the same way: title, play, pause, crash, resize, quit, an agent stream under the open game (needs tmux; not run in CI).
`PIG_BIN=pig npm run test:tool-scope` checks that a Piglet entry's `tools: []` keeps pig-doctor's `pig_doctor` tool from the model (RPC passes; print and JSON mode are known PiG 0.3.0 failures, marked todo; needs Go; not run in CI).

CI checks source quality, generated catalog data, component Packages, and staged
manifests. `npm run validate` stages before invoking PiG. The porter scenario
exercises independent Package installation and two staged Piglets against a local
fixture provider. It checks exact Skill delivery, selection, source preservation,
restaging, and path safety. It does not measure an LLM's ability to follow the Skill.

The scenario also checks that an explicit `tools: []` reaches the provider with
no tools. It passes on core commit `22755205a` (`04eab6dd2` in release-all-next), and
the assertion stays in the scenario as a regression guard. Select a build containing that fix with `PIG_BIN` for local checks. CI does
not run it: the pinned validator fails it at direct Package Skill expansion, so it
waits for a reviewed pin containing the fixes. No hosted CI pass is claimed. CI does not build or sign Piglet Binaries, upload
artifacts, or publish releases. The release guard remains closed.

## Where to report issues

- Piglet manifests, selected resources, defaults, Pigpen documentation or index:
  [Pigpen issues](https://github.com/MichaelKinsy/pigpen/issues).
- Core PiG runtime, CLI, extension host, or monorepo publish/pull/update support:
  [PiG issues](https://github.com/MichaelKinsy/PiG/issues).
- Suspected vulnerabilities: follow [SECURITY.md](SECURITY.md), not a public bug
  report. Never post credentials, private keys or unredacted session files.

See [CONTRIBUTING.md](CONTRIBUTING.md) before proposing a change.

## Site consumption

The site at [pi-in-go.dev](https://pi-in-go.dev/packages) reads this repository's
generated index over HTTPS at build time, validates it, and retains a checked-in
fallback for outages. It never writes back into Pigpen. Verified upstream Pi
packages remain platform-owned data; community Piglet discovery is deferred.

## License

Original code and documentation: [MIT](LICENSE), Copyright (c) 2026 Michael Kinsy.
Every Package has its own `LICENSE`: ports of third-party projects list the upstream
copyright first and keep the upstream's license file. Curated third-party resources
must retain their own licenses and provenance; inclusion does not grant new
redistribution rights.
See [RELEASE-NOTES.md](RELEASE-NOTES.md) for the split.
