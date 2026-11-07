# Deploying the PiG harness on kagent

This page installs the PiG harness (`kagent-pig`) as a kagent `Harness` of kind `byo`, with an
`AgentTemplate` and an `Agent` that pairs them. The `pig-kagent` Helm chart and the `pig-kagent.sh`
installer in this directory do the work; they create only kagent `api.kagent.dev/v1alpha3` objects
(Harness, AgentTemplate, Agent, and optionally EgressPolicy, ModelConfig and RemoteMCPServers). They
contain no CRDs, no Secrets and no Substrate objects.

[kagent](https://github.com/kagent-dev/kagent) and Agent Substrate are separate projects with their own
Apache-2.0 licences. This harness uses kagent's public harness runtime packages and is not endorsed by
either project. PiG is a Go implementation of [Pi](https://github.com/earendil-works/pi), the terminal coding agent.

For a local cluster from scratch, read [kind.md](kind.md) first. Day-two topics are in
[operations.md](operations.md).

## Compatibility

| Component | Version this chart targets | Notes |
| --- | --- | --- |
| kagent | `1.0.0-rc3` (`api.kagent.dev/v1alpha3`) | The API is alpha. The installer warns when the controller version differs from the tested one. |
| Agent Substrate | `v1.0.0` (`ate.dev/v1`) | Preview gates `PerTemplateSnapshotLocation` and `GoldenEgressPolicy` must be enabled. |
| Kubernetes | 1.30 or newer for the chart; Substrate needs `ClusterTrustBundle`, `ClusterTrustBundleProjection` and `PodCertificateRequest` | kagent's own Kind setup enables them, see [kind.md](kind.md). |
| Helm, kubectl | Helm 3.13 or newer (`--dry-run=server`), kubectl | The installer needs nothing else. |

The versions are recorded in `chart/pig-kagent/Chart.yaml` annotations (`pig.dev/tested-kagent`,
`pig.dev/tested-substrate`).

## Prerequisites

1. kagent installed with Substrate. `kagent install` does both (see [kind.md](kind.md)); a direct Helm
   install of the kagent chart does not install Substrate, and you then apply
   [kagent's own instructions](https://github.com/kagent-dev/kagent/blob/main/helm/README.md).
2. The kagent controller namespace carries the label `ate.dev/api-access=true`. `kagent install` sets it;
   a direct Helm install must do it:
   `kubectl label namespace kagent ate.dev/api-access=true --overwrite`.
3. A `WorkerPool` in the namespace of the Agent. The kagent chart creates `kagent-default` when
   `substrateWorkerPool.create=true`, which is also what `kagent install` sets up, and that is the chart's
   default `substrate.workerPoolRef`.
4. A snapshot location Substrate serves. The default, `gs://ate-snapshots/kagent/`, is the one kagent's own
   harness documentation uses; set `substrate.snapshotLocation` to match your Substrate object store.
5. A model: an existing `ModelConfig` (the chart default is `default-model-config`, which `kagent install`
   creates), or one created by this chart.
6. The image reference of the PiG harness, pinned by digest (`registry/name@sha256:<64 hex>`). The Harness
   CRD and Substrate reject tags, and the chart's schema rejects them too before any request is sent.
   Release builds publish the digest; for a local build see [kind.md](kind.md).

## Install

The installer checks the cluster read-only, installs with Helm, and waits for the Agent's `Ready` condition.

```sh
cd components/kagent/harness      # or the exported harnesses/pig directory in byo-ext

./pig-kagent.sh check   -n kagent --set image=ghcr.io/OWNER/pig-kagent@sha256:<64 hex>
./pig-kagent.sh install -n kagent --set image=ghcr.io/OWNER/pig-kagent@sha256:<64 hex>
```

Options: `-n/--namespace`, `-r/--release` (default `pig`), `-f/--values FILE` (repeatable),
`--set KEY=VALUE` (repeatable), `--chart` (a directory or an OCI reference such as
`oci://REGISTRY/PATH/pig-kagent@sha256:<digest>`), `--context`, `--substrate-namespace`, `--timeout`
seconds, `--dry-run` (server-side dry run, changes nothing), `--skip-preflight`.

Subcommands:

| Command | Does |
| --- | --- |
| `check` | Read-only preflight. Prints `ok`, `note`, `WARN` or `FAIL` lines and exits 1 on any `FAIL`. |
| `render` | `helm template` with the same values. Needs no cluster. |
| `install` | Preflight, `helm upgrade --install`, then waits for Agent `Ready`. On timeout it prints each condition and the Agent warnings. Safe to run again. |
| `uninstall` | `helm uninstall`. A release that is not installed is reported and is not an error. |

What `check` verifies:

- the six kagent CRDs serve `v1alpha3` and the Harness CRD has `spec.byo`;
- the kagent controller exists, and its version is compared with the tested one (a difference is a warning);
- the controller namespace is labeled `ate.dev/api-access=true`;
- Substrate is installed (its namespace is discovered from the `app=ate-api-server` pod, override with
  `--substrate-namespace`) and has the two preview gates;
- the WorkerPool named by the release exists (the failure lists the pools that do exist);
- the snapshot location is compared with the locations of existing Harnesses (a difference is a warning);
- the ModelConfig exists, and its endpoint is a DNS name and not an IP address or `localhost`;
- every Secret the release refers to exists and has the named key. Only key names are read; a Secret value is
  never read into the output;
- the Substrate credential provider's `namespacePolicies` lets the atespace (the namespace) read Secrets in
  that namespace;
- no Harness, AgentTemplate or Agent of the same name exists that another release or a hand-made manifest owns;
- with `harness.create: false`, the Harness named by `harness.name` exists, is a `byo` Harness, and runs the `image` of the release (a different image is a warning).

Using Helm directly is equivalent and is what GitOps tools do. The installer's checks are then yours:

```sh
helm upgrade --install pig oci://REGISTRY/PATH/pig-kagent --version 0.1.0 -n kagent \
  --set image=ghcr.io/OWNER/pig-kagent@sha256:<64 hex>
kubectl get agents.api.kagent.dev pig -n kagent \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
```

Readiness is on the Agent. A Harness has no status.

## Chart values

`values.schema.json` is strict: an unknown key, a tag instead of a digest, an IP address as an endpoint, or an
environment variable kagent owns is an error at `helm template` time.

| Value | Default | Meaning |
| --- | --- | --- |
| `image` | required | Digest-pinned harness image. |
| `command` | `[/usr/local/bin/kagent-pig]` | Entrypoint (kagent requires `workload.command` for `byo`). The adapter takes no arguments. |
| `args` | `[]` | Passed through to `workload.args`. |
| `runtime.logLevel` | empty | `debug`, `info`, `warn` or `error`; becomes `KAGENT_PIG_LOG_LEVEL`. Empty keeps the level of the compiled configuration. |
| `runtime.tools` | empty | PiG tools the model may use, as a list; becomes `KAGENT_PIG_TOOLS` (comma list). Empty uses `read,bash,edit,write,grep,find,ls`; `ask_user` is always added. |
| `runtime.bin` | empty | Path or `PATH` name of the PiG binary (the fused Piglet Binary); becomes `KAGENT_PIG_BIN`. Empty uses `pig` from `PATH`. |
| `runtime.contextWindow` | `0` | Model context window in tokens; becomes `KAGENT_PIG_CONTEXT_WINDOW`. kagent compiles no model limits, so `0` leaves the adapter default (128000). PiG compacts its session by this window. A positive integer. |
| `runtime.maxTokens` | `0` | Maximum output tokens; becomes `KAGENT_PIG_MAX_TOKENS`. `0` leaves the adapter default (8192). A positive integer. |
| `runtime.passEnv` | `[]` | Names of extra environment variables that `pig` and its tools may inherit; becomes `KAGENT_PIG_PASS_ENV` (comma list). Names must not start with `KAGENT_`. The values come from `env`. `pig` otherwise gets an allowlist of the environment only. |
| `env` | `{}` | More literal environment for the Harness, at most 16. See [Telemetry](#telemetry). |
| `substrate.workerPoolRef` | `kagent-default` | WorkerPool in the release namespace. |
| `substrate.snapshotLocation` | `gs://ate-snapshots/kagent/` | Per-Harness snapshot location. |
| `agentTemplate.description` | `PiG coding agent running on Agent Substrate` | Also the description in the Agent Card kagent publishes for the Agent. |
| `agentTemplate.systemPrompt` | empty | System prompt of the template. |
| `agentTemplate.modelConfig` | `default-model-config` | Existing ModelConfig. Ignored when `modelConfig.create` is true. |
| `agent.create` | `true` | Create the Agent. With `false`, your Agent must reference template and harness `<name>` and, when egress is set, `egressPolicyRefs: [{name: <name>-egress}]`. |
| `egress.destinations` | `[]` | Extra egress. See [Egress](#egress). |
| `modelConfig.*` | create `false` | See [Model credentials](#model-credentials). |
| `mcpServers` | `[]` | See [MCP servers](#mcp-servers-and-approval). |
| `harness.create`, `harness.name` | `true`, empty | `true` creates the Harness (named `harness.name`, default the release name). `false` uses the existing Harness `harness.name`. See [Several Agents on one Harness](#several-agents-on-one-harness). |
| `fullnameOverride` | empty (`pig`) | Name of the AgentTemplate and Agent, and of the Harness unless `harness.name` is set; the EgressPolicy is `<name>-egress`. |

A second PiG Agent in the same namespace needs its own release and name (`-r other --set fullnameOverride=other`), and
either its own Harness or one shared as described below.

## Several Agents on one Harness

A Harness is reusable platform policy (image, command, environment, worker pool, snapshot location); Agents select it
with `harnessRef`. To run several PiG Agents, for example one that may only read and one that needs approvals, create
the Harness once and give each Agent its own release:

```sh
# 1. The Harness, and no Agent. Keep this release as long as any Agent uses the Harness.
./pig-kagent.sh install -r pig-harness -n kagent --set image="$IMAGE" \
  --set fullnameOverride=pig-harness --set agent.create=false

# 2. Agents on it: one release each, with their own prompt, MCP servers and egress.
./pig-kagent.sh install -r pig-review -n kagent --set image="$IMAGE" \
  --set fullnameOverride=pig-review --set harness.create=false --set harness.name=pig-harness \
  -f review-values.yaml
```

With `harness.create: false` the release renders no Harness; its Agent's `harnessRef` is `harness.name` (required
then). `env`, `args` and `runtime.*` configure the Harness and are refused in such a release, and `command` and
`substrate.*` are ignored. `image` is still required: `check` reads that Harness in the namespace and fails when it is
missing or not a `byo` Harness, and warns when its image is not the `image` of the release. The Agent runs what the
Harness names. Uninstalling the release that created the Harness removes it, so uninstall the Agents' releases first.
Each Agent has its own AgentTemplate and EgressPolicy (`<name>-egress`), so egress grants differ per Agent.

## Model credentials

Credentials are never values of this chart and never enter a manifest. kagent and Substrate keep them out
of the Actor: the compiled configuration carries a secret reference, the Substrate credential provider
fetches the value, and the Substrate egress gateway replaces the request header. PiG only sees an inert
placeholder.

1. Create the Secret in the namespace of the Agent, from your shell without echoing the value:

   ```sh
   kubectl create secret generic pig-model-key -n kagent --from-literal=api-key="$MODEL_API_KEY"
   ```

2. Let the credential provider read it. In the Substrate release values (the kagent chart embeds them under
   `substrate:`):

   ```yaml
   credentialProvider:
     namespacePolicies:
       - atespace: kagent
         allowedNamespaces: [kagent]
   ```

   `kagent install` configures this for the `kagent` namespace. For any other namespace the grant is yours to
   add; empty grants deny all credential fetches. `pig-kagent.sh check` reports a missing grant.

3. Either point the template at an existing ModelConfig (`agentTemplate.modelConfig`), or let the chart create one:

   ```yaml
   modelConfig:
     create: true
     name: pig-model
     provider: OpenAI            # OpenAI, Anthropic or Ollama
     model: gpt-example
     baseUrl: https://models.example.com/v1
     apiFormat: responses        # OpenAI only; chatCompletions is the default
     apiKeySecret: pig-model-key
     apiKeySecretKey: api-key
   ```

The endpoint must be a DNS name, because Substrate's egress allows no IP addresses. kagent infers the egress
rule and the credential binding for the model from the ModelConfig, so no EgressPolicy is needed for it.
Other providers (Azure OpenAI, Gemini, Mistral, Bedrock with a bearer token) are used through a ModelConfig you
create yourself and name in `agentTemplate.modelConfig`. Providers kagent supports but this harness does not
(Vertex AI, SAP AI Core, Foundry's Anthropic format, token passthrough, TLS options, `n > 1`) stop the harness at startup, with every
unsupported field named in the error. The same holds for AgentTemplate features it does not implement: sub-agents, skills, plugins,
stdio and SSE tools, remote agents, memory, output schemas. Fields that are accepted without effect (for example
`context_config.compaction`, unsupported sampling parameters, timeouts) are logged once at startup as
`configuration accepted but not honoured`.

## MCP servers and approval

Each entry of `mcpServers` creates a `RemoteMCPServer` and binds it into the AgentTemplate:

```yaml
mcpServers:
  - name: github
    url: https://api.githubcopilot.com/mcp/
    description: GitHub MCP
    protocol: STREAMABLE_HTTP
    requireApproval: true
    tools: [get_issue, list_issues]
    headersFrom:
      - name: Authorization
        valueFrom: {type: Secret, name: github-mcp, key: Authorization}
```

- `requireApproval: true` pauses before every call of a tool of that binding. The Agent's A2A task goes to
  `input-required`; the person approves or rejects through the kagent UI or the gateway ([kind.md](kind.md)
  shows the round trip). Nothing of the pending call has run until it is approved.
- `tools` is an allowlist for what the model sees. Empty exposes every tool of the server. It is a filter, not a
  network control: the server's origin is reachable from the Actor, and an approval does not make a different
  route to the same origin harmless.
- `headersFrom` with `type: Secret` holds the full header value in the Secret; the gateway injects it, the
  Actor does not hold it. The Secret needs the same `namespacePolicies` grant as a model key. Literal and
  ConfigMap headers are not injected by the gateway.
- The server URL must be a DNS name. kagent infers its egress from the URL.
- Not supported by this harness and rejected at startup with a named error: `sse_tools`, `token_passthrough`
  and per-server TLS options.

## Egress

An Agent with no EgressPolicy reaches only what kagent infers: its model, its MCP servers, the kagent control
plane and any OpenTelemetry endpoint set through `env`. The PiG harness itself needs nothing else, so the chart
grants nothing by default.

Tools reach further. The image contains `git`, `gh`, `rg`, `fd`, `jq`, `curl`, `python3` and `go`; a coding task
that clones a repository or installs a package needs explicit origins:

```yaml
egress:
  destinations:
    - origin: https://github.com
    - origin: https://*.githubusercontent.com
    - origin: https://proxy.golang.org
    - origin: https://pypi.org
    - origin: https://files.pythonhosted.org
    - origin: https://api.github.com
      authentication:
        header: Authorization
        prefix: "Bearer "
        secretKeyRef: {name: github-token, key: token}
```

Rules, from kagent's egress documentation:

- An origin is a scheme, a DNS name and an optional port (443 for HTTPS, 80 for HTTP). Every path of that origin
  is allowed. IP addresses, paths and non-HTTP protocols are rejected.
- A wildcard replaces exactly one leftmost label: `https://*.example.com` allows `api.example.com`, not
  `example.com` and not `a.b.example.com`.
- Redirects and downloads from other origins need their own grants. A registry grant does not cover every
  dependency an install script fetches.
- An origin with `authentication` is an exact origin and gets the header from the Secret at the gateway. The
  client has to send that header with an inert placeholder, for example
  `curl -H 'Authorization: Bearer kagent-credential-injected' https://api.github.com/user`; the gateway replaces
  the value and does not add a missing header. The Secret needs the `namespacePolicies` grant described above.
- kagent sets the CA bundle variables (`SSL_CERT_FILE`, `SSL_CERT_DIR`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`,
  `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `AWS_CA_BUNDLE`) for the gateway's interception; they cannot be set
  through `env`.
- A policy holds 1 to 64 destinations; an Agent selects at most 16 policies.
- Changes to a policy apply to running and suspended Actors without a new revision.

An Agent that you author yourself (`agent.create: false`) must list the policy: `egressPolicyRefs: [{name: <name>-egress}]`.

## Telemetry

OpenTelemetry settings go in `env`; the Harness passes them through to the Actor. For a collector in the cluster:

```yaml
env:
  OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector.observability.svc.cluster.local:4318
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
```

kagent adds the endpoint's origin to the Actor's egress by itself (it reads the final environment), so it does not
need an EgressPolicy entry. Values you set win over the controller's runtime telemetry defaults. The endpoint
must be a DNS name. `OTEL_RESOURCE_ATTRIBUTES` is merged with a `service.version` kagent adds. Substrate allows
at most 32 environment variables per Actor in total, and kagent adds about nine of its own, which is why the chart
caps `env` at 16.

## Upgrade

- **New harness image**: run `install` again with the new digest. The Agent compiles a new revision. New Sessions
  use the latest successful revision; existing Sessions keep the revision, and the image, they started with.
  `install` waits until the Agent is `Ready` for the new revision.
- **New chart version**: run `install --chart oci://.../pig-kagent --version ...` or point `--chart` at the new
  directory. Helm removes objects that the new values no longer produce, for example the EgressPolicy when
  `egress.destinations` is emptied.
- Same values again change nothing but the Helm revision number.
- After a kagent upgrade, run `check` (it compares the tested kagent version) and re-validate the chart against the new
  CRDs.

## Uninstall

```sh
./pig-kagent.sh uninstall -n kagent
```

Removes exactly what the release created. It never touches Secrets, WorkerPools, Substrate, or ModelConfigs and
Agents that you created yourself. Existing Sessions of the deleted Agent follow kagent's session lifecycle.

## Troubleshooting by condition

```sh
kubectl get agents.api.kagent.dev pig -n kagent \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
kubectl get agents.api.kagent.dev pig -n kagent -o jsonpath='{.status.warnings}'
```

| Condition | Meaning when not `True` | What to do |
| --- | --- | --- |
| `Accepted` | The Agent or one of its references is not accepted. | Read the message; check names of `harnessRef` and `template.ref`. |
| `ResolvedRefs` | A referenced ModelConfig, RemoteMCPServer, AgentTemplate, Harness or WorkerPool is missing, or not allowed. | `pig-kagent.sh check`; create the missing object or fix the name. |
| `Compatible` (`RevisionInvalid`, `ActorTemplateInvalid`) | The compiled configuration is invalid for the Harness: a field this harness rejects, an environment variable kagent owns, more than 32 environment variables, a conflicting credential binding. | The message names the field. The harness itself rejects unsupported ModelConfig and MCP fields at startup with the field named, see the logs in [operations.md](operations.md). |
| `EgressPoliciesApplied` | `EgressPolicyNotFound`, `InvalidEgressPolicy` or `EgressGrantConflict`: a policy is missing or invalid, or two grants bind different Secrets to the same origin and header. It never blocks `Ready`. | Fix the policy named in the message; `kubectl get egresspolicy -n kagent`. |
| `Ready` = `ActorTemplatePending` | Substrate is building the golden snapshot. | Wait; if it persists, read the atelet and ate-api-server logs in the Substrate namespace. |
| `Ready` = `ActorTemplateRetrying` | The golden build failed and kagent retries; the message carries the reason Substrate reported. The template waits for `/readyz` on port 8081 (30 s timeout), so an image that cannot be pulled, or an entrypoint that exits or never serves it, ends here. | Check the digest exists for the node architecture, the `command`, and the image logs; check the atelet logs (`kubectl -n <substrate ns> logs ds/atelet -c atelet`). |
| `Ready` = `ActorTemplateRejected` or `ActorTemplateConflict` | Substrate did not accept the template. | Read the message. |

`pig-kagent.sh check` repeats the cluster-level causes (missing CRDs, label, WorkerPool, Secret, credential grant,
gate) before you install.
