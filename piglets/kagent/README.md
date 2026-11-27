# kagent

PiG for the [kagent](https://github.com/kagent-dev/kagent) harness: the [`kagent-policy`](../../components/kagent/README.md)
extension fused into one Piglet Binary, so a harness adapter can run `pig-kagent --mode rpc` with no Go toolchain and no
extension build at run time. Everything is in the standalone Package [`components/kagent`](../../components/kagent/README.md);
this Piglet only selects it and fuses it.

**Inert until configured.** Without `KAGENT_PIG_POLICY` the Binary behaves as the Stock PiG it is built from. With it set,
the extension registers the policy's MCP servers (every tool direct, or only the allowlist), asks for approval before a
call of a tool that needs it, and offers `ask_user`. A policy that cannot be used stops PiG from starting. The policy
file, the confirm messages and the failure rules are in the [Package README](../../components/kagent/README.md).

**Source only, not a published release.** kagent is a trademark of its owners; this Piglet is not endorsed by the kagent
project. It was verified against a hermetic mock model and a local MCP server only.

## Build the Binary

Needs a Go toolchain, a git checkout of PiG at the tag the `pig` you build with was released from (`PIG_SOURCE_ROOT`),
and the Pigpen checkout. The build is cold-cache slow (tens of seconds to minutes) and network is needed once for modules.

```sh
npm run stage                         # a local Package reference becomes Piglet-local in dist/staged/
PIG_SOURCE_ROOT=<PiG checkout at v0.7.0> pig piglet build dist/staged/piglets/kagent/piglet.yaml \
  --format binary --targets linux/amd64 --out ./pig-kagent
```

The path convention for an image is `/usr/local/bin/pig-kagent` (one static executable, no other file). Build once per
target architecture; `--targets linux/arm64` builds the other listed target.

Validate the manifest without building: `pig piglet validate dist/staged/piglets/kagent/piglet.yaml`.

## Run it

```sh
KAGENT_PIG_POLICY=/run/pig/policy.json KAGENT_PIG_HDR_1='Bearer ...' pig-kagent --mode rpc --session-dir ... --session-id ...
```

The closed extension surface (`discovery.extensions: []`) means an extension planted in the working directory is never
loaded, and the Binary cannot update itself. The Piglet does not stop PiG's file tools or `bash`: a tool allowlist is a
model-facing filter, not a network control (see the Package README).
