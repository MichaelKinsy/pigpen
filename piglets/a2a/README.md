# a2a

PiG that speaks [A2A](https://a2a-protocol.org) (Agent2Agent): three tools call remote A2A agents, and PiG can serve its own
tasks to A2A agents such as [kagent](https://github.com/kagent-dev/kagent). Everything is in the standalone Package
[`components/a2a`](../../components/a2a/README.md); this Piglet only selects it as a named agent and fuses it into a Binary.

**The listener is off.** Nothing opens a port until `a2a.json`, `PIG_A2A_LISTEN` or `--a2a-listen` configures one, and the server
refuses to start without authentication tokens (or `insecureNoAuth` on a loopback address). Configuration, authentication, tenant
boundaries and cancellation are documented in the [Package README](../../components/a2a/README.md).

**Source only, not a published release.** Try it from the checkout:

```sh
npm run stage
pig piglet validate dist/staged/piglets/a2a/piglet.yaml
pig --piglet dist/staged/piglets/a2a/piglet.yaml
```

Build a Binary (needs a git checkout of PiG as `PIG_SOURCE_ROOT`, a Go toolchain, and network or a warm Go module cache for
`github.com/a2aproject/a2a-go/v2`):

```sh
PIG_SOURCE_ROOT=<pig git checkout> pig piglet build dist/staged/piglets/a2a/piglet.yaml --format binary --targets <os>/<arch> --out ./pig-a2a
```

`pig-with-batteries` selects the same Package (its tools appear, its listener stays off until configured). To have the Binary serve tasks, set
`worker.command` to the Binary itself in `a2a.json`, so each task's worker is the same composition without its listener
(`PIG_A2A_WORKER=1` keeps children from listening).
