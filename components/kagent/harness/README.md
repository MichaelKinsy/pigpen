# PiG as a kagent native Harness

An experimental community integration that runs [PiG](https://github.com/MichaelKinsy/PiG), a Go implementation of
[Pi](https://github.com/earendil-works/pi) (the terminal coding agent by Mario Zechner and contributors), as a
[kagent](https://github.com/kagent-dev/kagent) `Harness` of kind `byo` on Agent Substrate. The adapter (`kagent-pig`) serves
kagent's runtime contract and drives one `pig --mode rpc` process per turn; the Piglet it drives is Stock PiG with a policy extension fused
in (MCP servers from the kagent Agent, an approval gate, an `ask_user` tool).

kagent and Agent Substrate are built by their own authors and are Apache-2.0 projects. The adapter imports kagent's published Go packages
(`go/adk/pkg/app`, `go/harness/runtime/...`). This directory is independent work: it follows the published harness contract and is not
affiliated with or endorsed by the kagent or Substrate projects, and it copies none of their code.

This directory is exported from `components/kagent/harness` of [MichaelKinsy/pigpen](https://github.com/MichaelKinsy/pigpen) by
`scripts/export-byo-ext.sh`; the Pigpen tree is the source of truth.

## Maintainers

- [@MichaelKinsy](https://github.com/MichaelKinsy)

## Compatibility

| Component | Version | Status |
| --- | --- | --- |
| kagent | `v1.0.0-rc3` (`api.kagent.dev/v1alpha3`) | the version the adapter's kagent packages and the chart are written against (main `ccb2dd350e06` has the same harness runtime packages) |
| Agent Substrate | `v1.0.0` (`ate.dev/v1`) | the chart's tested version; preview gates `PerTemplateSnapshotLocation` and `GoldenEgressPolicy` are required |
| PiG | `v0.7.0` (`pig version`: 0.7.0, upstream Pi 1.5.0), commit `beb56447c476da70d05d8e1e1ca9e993b367cb8b` | built from source at that commit; the build fails if the tag moves |
| Go | 1.27.3 | adapter and Piglet builder |
| Platforms | `linux/amd64`, `linux/arm64` | Verified: the amd64 image builds from this directory with plain `docker build` and passes the tool smoke below. Not verified: arm64 was not built or run; the release workflow builds it on a native arm64 runner |

Verified, with PiG v0.7.0 release binaries on a Linux amd64 host (no real model provider was used):
- closing PiG's stdin while an approval request is outstanding leaves a valid session JSONL with nothing of the pending batch executed;
- a `toolResult` line appended to that session is accepted and the next request is valid;
- a fused Piglet Binary starts in about 45 ms and idles at about 54 MB with the approval gate loaded.

The kagent contract this follows is the one in kagent's `go/harness/README.md`; the [BYO guide](https://github.com/kagent-dev/website/blob/main/docs-site/content/kagent/1.x/agents/bring-your-own-agent.md)
describes the user side. kagent's API is alpha and may change.

## Prerequisites and configuration

To build: Docker with BuildKit on Linux (amd64 or arm64), and network access to `github.com`, `go.dev`, `proxy.golang.org`, `snapshot.debian.org` and Docker Hub or a
mirror of the pinned base images. No credentials, no secrets, no paid API calls.

To run: a cluster with kagent and Agent Substrate, the controller namespace labelled `ate.dev/api-access=true`, a `WorkerPool`, a snapshot location, a `ModelConfig`
and a digest-pinned image reference. [docs/deploy.md](docs/deploy.md) lists each prerequisite and what the installer checks; [docs/kind.md](docs/kind.md) builds a
local Kind cluster from nothing.

What the image holds (everything is baked: the Substrate sandbox runs the Actor as UID 65532 with no capabilities and cannot install packages):

| Layer, bottom to top | Contents |
| --- | --- |
| base | `debian:trixie-slim` pinned by digest |
| OS | `bash ca-certificates curl git python3 python3-pip python3-venv` from a snapshot.debian.org timestamp; user and group 65532 |
| Go toolchain | Go 1.27.3 in `/usr/local/go` |
| tools | `gh` 2.103.3, `rg` (ripgrep) 15.2.0, `fd` 10.5.1, `jq` 1.8.2 |
| PiG | `/usr/local/bin/pig`: PiG v0.7.0 with the `kagent` Piglet fused in |
| adapter | `/usr/local/bin/kagent-pig` |

Every archive is pinned by sha256 (`ADD --checksum`), the base images by digest, Go modules by `go.sum`. The image has no `ENTRYPOINT`, no `CMD` and no health check:
the Harness supplies `workload.command: ["/usr/local/bin/kagent-pig"]` and kagent probes `/readyz` on port 8081. `HOME` and every cache are under `/data`, the
durable volume kagent mounts. kagent sets the TLS trust variables itself and rejects an Agent that sets them, so the image sets none; the adapter combines the
system roots in `/etc/ssl/certs/ca-certificates.crt` with the egress trust bundle for `pig` and its tools.

Runtime configuration is the chart values ([docs/deploy.md](docs/deploy.md#chart-values)); the adapter reads these optional variables, which the chart sets from `runtime.*`:
`KAGENT_PIG_BIN`, `KAGENT_PIG_TOOLS`, `KAGENT_PIG_LOG_LEVEL`, `KAGENT_PIG_CONTEXT_WINDOW`, `KAGENT_PIG_MAX_TOKENS`, `KAGENT_PIG_PASS_ENV`. Model credentials are never chart values
and never enter the image or a manifest: the compiled kagent configuration carries a secret reference and the Substrate egress gateway replaces the request header
([docs/deploy.md](docs/deploy.md#model-credentials)). The external cost is the model provider's.

## Build

This directory is the whole build context. From here:

```sh
docker build --tag byo-ext/pig:local .
```

CI runs exactly that on Linux amd64 with no build arguments and no credentials. The build compiles the adapter (cross-compiled on the build platform), builds `pig` from PiG source at the
pinned commit, uses it to build the Piglet in `piglet/piglet.yaml` with the extension in `policy/` fused into one binary, and assembles the layers above.
The Piglet stage runs `go build` for the machine it runs on, so build arm64 on an arm64 host or runner. A cold build (no image layer cache) took about 1 min 35 s on a 192-core
amd64 host with warm BuildKit module caches and the base images present; the image is about 208 MB compressed and 620 MB uncompressed on amd64 (measured).

Regenerate this directory from the Pigpen tree, or check that it has not drifted (in a Pigpen checkout):

```sh
sh scripts/export-byo-ext.sh /path/to/harnesses/pig            # the target must be new or empty
sh scripts/export-byo-ext.sh --check /path/to/harnesses/pig    # exits 1 and shows a diff on drift
```

Smoke test of the built image (all tools are baked; this runs as UID 65532):

```sh
docker run --rm --entrypoint sh byo-ext/pig:local -c 'id; git --version; rg --version; fd --version; gh --version; jq --version; curl --version; python3 --version; go version; pig version'
```

Verified on the image built from this tree: `uid=65532(kagent)`, git 2.47.3, ripgrep 15.2.0, fd 10.5.1, gh 2.103.3, jq 1.8.2, curl 8.14.1, Python 3.13.5 with pip,
go1.27.3 linux/amd64, `pig: 0.7.0`.

Reproducibility, measured: with `SOURCE_DATE_EPOCH` and `rewrite-timestamp` the OS, Go, tool and adapter layers were identical across two cold builds. The fused
`pig` layer differs by 123 bytes because PiG v0.7.0 records the build time in the Piglet's embedded resolution record; the digest a release publishes is what a Harness pins.

## Run with kagent

The `pig-kagent` Helm chart in `chart/pig-kagent` and the installer `pig-kagent.sh` create only kagent `v1alpha3` objects (Harness `byo`, AgentTemplate, Agent, and
optionally EgressPolicy, ModelConfig and RemoteMCPServers). They contain no CRDs and no Secrets. Replace the image reference with your own digest:

```sh
./pig-kagent.sh check   -n kagent --set image=REGISTRY/NAME@sha256:<64 hex>
./pig-kagent.sh install -n kagent --set image=REGISTRY/NAME@sha256:<64 hex>
```

The Harness CRD and Substrate reject tags, so the image must be referenced by digest (the chart schema rejects a tag before any request is sent). The image reaches the
cluster through a registry the Substrate node agent can pull from; for a local registry on Kind see [docs/kind.md](docs/kind.md#2-a-local-registry-for-your-own-image).
Day-two topics (logs, what is persisted, session restart) are in [docs/operations.md](docs/operations.md).

## Try it

After the install, the walk-through in [docs/kind.md](docs/kind.md) shows how to read the Agent Card through the kagent gateway
(`/agents/<namespace>/pig/.well-known/agent-card.json`), send an A2A `SendMessage`, and run an approval round trip against an MCP server bound with `requireApproval`.

### Verified on a cluster

Run on a single-node Kind cluster (`kindest/node` v1.35.0) with kagent `v1.0.0-rc3` and Agent Substrate `v1.0.0` (gVisor Actors), image built from this
directory, in November 2026. **No real model provider was used**: every agent in every row below talked to the same hermetic, deterministic mock model server
(OpenAI Chat Completions, OpenAI Responses and Anthropic Messages, plus a small MCP server), so model quality and model latency are not measured.

| Check | Result |
| --- | --- |
| Task through the kagent A2A gateway: a shell tool call, an MCP tool call, then the answer | completed, 10 of 10 sessions |
| Approval round trip: `input-required` with a kagent HITL tool approval request, approve, task completes | 10 of 10 sessions |
| Rejection with a reason: the model sees `Tool call was rejected by user.` and the reason | observed |
| Session survives restart: kagent suspends the Actor after every turn and restores it into a new process; a later turn recalls the first message | 10 of 10 sessions, and after deleting the worker pod |
| A parked approval resumed after the Actor was suspended and restored (the `RestorePending` path), also with the worker pod deleted while parked | completed |
| MCP wiring: a `RemoteMCPServer` bound to the Agent, called by name | 10 of 10 sessions |
| `requireApproval` on that binding becomes the approval gate | 10 of 10 sessions |

Per-turn cost, same task and same mock model on the same cluster, medians of 10 sessions (the Claude Code and Codex rows are kagent's own harness images from the rc3
catalog; pi-go is the community harness [dimetron/pi-go](https://github.com/dimetron/pi-go) v0.3.10, rebuilt for amd64 because its published amd64 manifest holds an arm64 binary):

| | PiG | Claude Code | Codex | pi-go |
| --- | ---: | ---: | ---: | ---: |
| Actor memory per turn, working set max (MiB) | 51 | 142 | 58 | 31 |
| CPU time per turn (ms) | 528 | 2413 | 1414 | 357 |
| `[basket]` send to done through the gateway (ms) | 1031 | 2708 | 1863 | 798 |
| MCP tool called / approval round trip | 10/10, 10/10 | 10/10, 10/10 | 10/10, 10/10 | 0/10 (no MCP tools reach it on rc3), none |

Read the table with its limits: memory is the working set of the Actor's gVisor sandbox cgroup, so it includes the sandbox's own overhead; every turn includes starting the
harness process because kagent restores the Actor into a new process; pi-go does less work per turn on rc3; differences under about 15 percent are within run-to-run spread; and
with a real model the model's latency would dominate every turn. The measurements used a mock model for every harness. Method, raw data, caveats and the scripts to repeat them are in the
[proof directory of the Pigpen repository](https://github.com/MichaelKinsy/pigpen/tree/main/components/kagent/proof).

CI checks only that the image builds.

## Limitations

- One pending input (an approval or an `ask_user` question) at a time.
- Approvals are decided on the adapter side: when a call needs approval the adapter stops PiG, records the pending call durably, and on approval runs the MCP call itself, appends its
  result to the PiG session, and starts PiG again with one short continuation prompt. Other calls of the same assistant message are reported as not executed and may be re-issued.
- If the process dies after a decision was recorded but before the continuation finished, the turn ends as `ErrSessionAbandoned`; an approved call is never run twice automatically.
- A tool allowlist and `requireApproval` are filters for the model, not network controls: an approval-gated MCP origin is still reachable through `bash` with `curl` when the Actor's
  egress allows it. What the Actor can reach is decided by egress rules.
- Rejected at startup with the field named: `sse_tools`, `token_passthrough`, per-server TLS options, `gemini_vertex_ai`, `gemini_anthropic`, `sap_ai_core`, the Foundry Anthropic format,
  `token_exchange`, TLS skip or CA overrides, `api_key_passthrough`, and `n > 1`. Providers mapped: openai (chat completions and responses), anthropic, azure_openai (responses), mistral,
  gemini (default endpoint), bedrock (bearer only), ollama.
- PiG's session file format (version 3) is a compatibility surface: the PiG version is pinned and the adapter's session handling is tested against recorded v0.7.0 sessions.
- The image is amd64-verified only (see Compatibility). The kagent API is alpha; the chart is tested against the versions above.
- kagent (Apache-2.0), Agent Substrate (Apache-2.0) and Pi are the work of their own authors; nothing here implies their endorsement.

## Licences

| Path | Licence |
| --- | --- |
| this directory (`adapter/`, `chart/`, `docs/`, `Dockerfile`, `pig-kagent.sh`) | Apache-2.0, [LICENSE](LICENSE), [NOTICE](NOTICE) |
| `policy/` (the kagent-policy extension) | MIT, [policy/LICENSE](policy/LICENSE) |
| PiG, built into the image | MIT, licence texts in `/usr/share/licenses/pig` of the image |

## Cleanup

```sh
./pig-kagent.sh uninstall -n kagent
kubectl delete secret pig-model-key -n kagent      # only if you created it as in docs/deploy.md
docker image rm byo-ext/pig:local
```

For the Kind walk-through: `kind delete cluster --name pig` and `docker rm -f kind-registry` ([docs/kind.md](docs/kind.md#8-clean-up)).
