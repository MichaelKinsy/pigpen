# acp

PiG as an agent for editors that speak the [Agent Client Protocol](https://agentclientprotocol.com) (ACP), such as [Zed](https://zed.dev). A Go port of [pi-acp](https://github.com/svkozak/pi-acp) by Sergii Kozak (MIT). See the Package [`components/acp`](../../components/acp/README.md) for the protocol coverage, the limits and the evidence.

**Source only, not a published release.** No Piglet Binary is published and no remote install command exists yet.

This Piglet selects the Go extension of the Package: `/acp` prints how to point an editor at the running agent. The ACP entrypoint itself is the companion executable `pig-acp`. ACP is JSON-RPC on stdio, and a fused extension must not write protocol bytes to the host's stdout, so the editor starts `pig-acp`, which starts this agent in RPC mode. There is no one-command Binary yet: see [`RELEASE-BLOCKERS.md`](../../RELEASE-BLOCKERS.md).

```sh
npm ci --ignore-scripts && npm run stage
pig piglet validate dist/staged/piglets/acp/piglet.yaml
# the editor runs (Zed: agent_servers, see components/acp/README.md):
pig-acp --pig pig --piglet dist/staged/piglets/acp/piglet.yaml
```

A Piglet Binary builds from the staged manifest (`pig piglet build dist/staged/piglets/acp/piglet.yaml --format binary --targets <os>/<arch> --out <dir>` with `PIG_SOURCE_ROOT` set to a git checkout of the PiG source) and works as the `--pig` target: `pig-acp --pig <dir>/acp`.

`discovery` keeps the user's and the workspace's extensions, skills and prompt templates, so the editor session behaves like a terminal session.

## Layout

```text
piglet.yaml     authored composition, input to staging
catalog.json    presentation metadata
../../components/acp/   the Package: extensions/acp (Go extension) and extensions/acp/cmd/pig-acp (companion)
```
