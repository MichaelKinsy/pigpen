# pig-rea: the REA harness

[REA (Reverse Engineer Anything)](https://github.com/morluto/rea) as one executable. "See a feature you like. Understand how it
works, down to the binary level." `pig-rea` is a PiG Binary whose agent is set up for that work:

- **REA's 229 MCP tools and 6 prompts**, from the published `rea-agents` 6.8.0 running unmodified on its own Node 24.22.2,
  both embedded in the Binary. No Node, npm, npx or network is needed at runtime. The first start of a REA version unpacks
  it into `~/.pig/agent/rea/` (about 2 s); later starts reuse it.
- **REA's skill** (`pigpen-reverse-engineer-anything`, REA's own text) and **`pigpen-derive-a-harness`**, which turns REA
  evidence about an agent harness, CLI or terminal app into an inventory of its surface, a behavior spec and a new
  piglet, measures the piglet against the target, and re-checks the inventory before anything is called done.
- **`pigpen-audit-app-traffic`**, which reads a capture of an app's traffic (HAR, Fiddler, or one made with a private
  mitmproxy for that one process) and reports what the app sent, where, which requests carry your data and what likely
  sent them, with what was observed kept apart from what was inferred. mitmproxy is not bundled.
- **A REA dashboard**: a header with REA's version and providers, a widget with the REA calls in flight (including those
  made inside codemode scripts), the `/rea` overlay (investigation timeline, targets, providers, about) and compact cards
  for direct REA calls.
- **Nothing ambient.** No extensions or skills from `~/.pig` or the project load by default.

It is REA as `npx rea-agents` gives it: REA's providers (Hopper, Ghidra, IDA, JADX, Rizin and others) are found the way REA
finds them, and Frida is absent, as for npx users. REA's tools reach the model through codemode, REA's default exposure.

## Run it

```sh
npm run payload:rea                    # REA + Node for this machine, from components/rea/rea.lock.json
npm run build:piglet -- pig-rea        # dist/bin/pig-rea (PIG_BIN and PIG_SOURCE_ROOT as for any Piglet)
dist/bin/pig-rea
```

Use your own provider and login as with `pig`. `/rea` opens the dashboard; `/rea-runtime status|verify|gc` reports, checks
or cleans the unpacked REA and skills. `-p` and `--mode rpc` give the scriptable harness, with the dashboard silent.

## Change it at launch, without rebuilding

The Binary is open: it accepts an overlay Piglet. This one brings your own extensions and skills back:

```yaml
# ~/rea-mine.yaml            pig-rea --piglet ~/rea-mine.yaml   (or PIG_PIGLET_PATH=~/rea-mine.yaml)
name: rea-mine
extends:
  allowWiden: true
discovery:
  extensions: [user, workspace]
  skills: [user, workspace]
```

An overlay can also remove the dashboard (`extends: {remove: {extensions: [rea-dashboard]}}`), or add skills, prompts or
extensions by `origins`. It cannot bring back what the Binary left out: the Rust, Python and Lisp extension SDKs and
`self-update` (a new harness version is a new Piglet release, not `pig update`).

`-ns` keeps the harness's three skills. `-ne` and `--no-mcp` also turn off PiG's MCP support, so REA's tools disappear; the
`rea` member says which switch did it. `-ne -e builtin:mcp -e builtin:codemode -e builtin:tool-search` keeps REA.

For a Binary that accepts none of this, use [`pig-rea-sealed`](../pig-rea-sealed/README.md).

## Known issue in the PiG 0.7.0 release

When the project directory has its own PiG config that needs a trust decision (for example `.pig/mcp.json`), the PiG 0.7.0
release (`v0.7.0`) runs the user's `~/.pig/agent/extensions` in its pre-trust pass even though this Piglet loads no ambient
extensions. They do not join the session, but their factories run. PiG fixed this after 0.7.0 (MichaelKinsy/PiG#453); the
PiG commit Pigpen pins has the fix, and `npm run test:rea-harness` checks it on both Binaries.

## Platforms

The manifest lists linux/amd64, linux/arm64, darwin/arm64, darwin/amd64 and windows/amd64. Only darwin/arm64 has been
built and run (108 MB Binary, 41.9 MB of it the payload); CI builds the payload and the Binary for each target.

## Credits

REA by morluto (MIT) and Node.js (MIT), embedded unmodified; see [components/rea/CREDITS.md](../../components/rea/CREDITS.md).
The harness members, `pigpen-derive-a-harness` and `pigpen-audit-app-traffic` are original work by Michael Kinsy (MIT).
