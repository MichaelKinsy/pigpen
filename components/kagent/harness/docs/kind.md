# Running the PiG harness on a local Kind cluster

A reproducible path from nothing to an approval round trip on one machine. It follows kagent's own Kind
setup (`scripts/kind/` and the `Makefile` of the kagent repository, tag `v1.0.0-rc3`); only the parts that
differ for a harness you build yourself are added here. Nothing in the chart is specific to Kind: the same
chart and installer work on any cluster that meets the prerequisites in [deploy.md](deploy.md).

kagent and Agent Substrate are separate Apache-2.0 projects and are not affiliated with this one.

## What you need

- Docker (or Podman), [kind](https://kind.sigs.k8s.io/) v0.33.0 (the version kagent's CI pins), `kubectl`, Helm 3.13 or newer.
- The kagent CLI for the release you target. For rc3:
  `https://github.com/kagent-dev/kagent/releases/download/v1.0.0-rc3/kagent-<os>-<arch>` (for example
  `kagent-linux-amd64`); verify it against the checksum published with the release.
- A model: an OpenAI-compatible endpoint, or an API key for a supported provider. Substrate's egress allows DNS
  names only, so the endpoint of an in-cluster model is a Service DNS name, never an IP address.

Substrate's Actors run in gVisor. Substrate's worker pods run as UID 0 without `privileged` and keep the `SYS_ADMIN`
and `NET_ADMIN` capabilities (for mounts and networking), so the nodes have to be trusted (Substrate v1.0.0,
`docs/reference/api-guide.md`, section "Worker security"). The node also
needs outbound access to `storage.googleapis.com` the first time, where Substrate downloads the gVisor runtime. A
Linux host is the tested place; Docker Desktop is not covered here.

## 1. Create the cluster

Substrate needs three Kubernetes feature gates that Kind does not enable by default. `kind-config.yaml`:

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: pig
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
featureGates:
  ClusterTrustBundle: true
  ClusterTrustBundleProjection: true
  PodCertificateRequest: true
runtimeConfig:
  "api/alpha": "false"
  "certificates.k8s.io/v1beta1": "true"
nodes:
  - role: control-plane
```

```sh
kind create cluster --config kind-config.yaml --image kindest/node:v1.35.0 --wait 60s
kubectl api-resources | grep -E 'clustertrustbundles|podcertificaterequests'   # both must be listed
```

## 2. A local registry for your own image

Substrate's node agent, not containerd, pulls Actor images. kagent's Kind setup starts a registry on the Kind
network and tells the node agent to rewrite `localhost:<port>` references to it; that is how an image built on
your machine reaches the cluster without a public registry.

```sh
docker run -d --restart=always -p 127.0.0.1:5001:5000 --network bridge --name kind-registry registry:2
docker network connect kind kind-registry
```

The registry is plain HTTP. Give the node's containerd the same alias for ordinary pods (the Actor image does not
need it; a model server image you push there does):

```sh
node=pig-control-plane
docker exec "$node" mkdir -p /etc/containerd/certs.d/localhost:5001
printf '[host."http://kind-registry:5000"]\n  skip_verify = true\n' |
  docker exec -i "$node" cp /dev/stdin /etc/containerd/certs.d/localhost:5001/hosts.toml
```

## 3. Install kagent and Substrate

`kagent install` installs the kagent CRDs, Substrate (CRDs, pod-certificate controller, node agent, API, credential
provider and egress gateway), a development PostgreSQL, and kagent. The rc3 CLI is configured through a JSON config
file (`kagent --config FILE`) and ignores the `KAGENT_SUBSTRATE_HELM_EXTRA_ARGS` and `KAGENT_DEFAULT_MODEL_PROVIDER`
variables that appear in kagent's Makefile for the previous release's CLI. Pass the registry rewrite as a Substrate values
file; the node agent inherits the Substrate values files:

```sh
cat > substrate-values.yaml <<'EOF'
atelet:
  extraArgs:
    - --localhost-registry-replacement=kind-registry:5000
ateApi:
  extraArgs:
    - --template-resync-interval=250ms
EOF
cat > kagent-config.json <<EOF
{"install":{"substrate":{"valuesFiles":["$PWD/substrate-values.yaml"]}}}
EOF
export OPENAI_API_KEY=...            # still read from the environment; never written to a manifest
kagent --config kagent-config.json install --timeout 15m
```

The `--template-resync-interval` argument only speeds up template pickup for local testing. Without the registry
rewrite Substrate cannot pull `localhost:5001/...` images. The default model provider is OpenAI; another provider is set
with `{"install":{"defaultModelProvider":"Anthropic", ...}}` in the same config file, together with that provider's
`*_API_KEY` variable.

The CLI also labels the `kagent` namespace `ate.dev/api-access=true`, creates the WorkerPool `kagent-default`,
creates the ModelConfig `default-model-config` for the default provider, and grants the `kagent` atespace access
to Secrets in the `kagent` namespace. It installs Substrate in the namespace `ate-system`; the installer script
discovers whichever namespace Substrate is in.

Check what the PiG chart needs is there:

```sh
kubectl -n kagent get workerpool kagent-default
kubectl get ns kagent --show-labels | grep ate.dev/api-access
kubectl -n kagent get modelconfig default-model-config
```

## 4. Build the image and pin its digest

The harness directory is the build context and needs no build arguments or secrets. Build for the architecture of
the Kind node (the host's, for Kind):

```sh
cd components/kagent/harness
docker build -t localhost:5001/pig/pig-kagent:dev .
docker push localhost:5001/pig/pig-kagent:dev
```

`docker push` prints the digest (`digest: sha256:<64 hex>`). Reference the image by that digest, never by the tag,
because the Harness CRD and Substrate accept only digest-pinned images:

```sh
IMAGE=localhost:5001/pig/pig-kagent@sha256:<64 hex from the push>
```

When the digest is not at hand: `docker buildx imagetools inspect localhost:5001/pig/pig-kagent:dev`, or ask the
registry. Current Docker pushes an OCI index, so accept both index and manifest types:

```sh
curl -sI -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' \
  http://localhost:5001/v2/pig/pig-kagent/manifests/dev | grep -i docker-content-digest
```

## 5. Install the harness

```sh
cd components/kagent/harness
./pig-kagent.sh check   -n kagent --set image="$IMAGE"
./pig-kagent.sh install -n kagent --set image="$IMAGE"
```

The Agent is `Ready` once Substrate has built the golden snapshot of the harness image. A failure prints the
Agent's conditions; [deploy.md](deploy.md#troubleshooting-by-condition) explains each.

The Harness the chart creates sets `substrate.snapshotPolicy.location` to the chart default `gs://ate-snapshots/kagent/`.
kagent hands that value to Substrate as the base location for the snapshots of this Harness's Actors (the
ActorTemplate's `snapshotConfig.storageLocation`, which `kagent install` allows by enabling Substrate's
`PerTemplateSnapshotLocation` gate); Substrate writes below it, under `atespaces/<atespace>/actors/<uid>/...`. It is a
base prefix, not a separate store: Substrate's chart keeps snapshots in an in-cluster RustFS bucket called `ate-snapshots`
by default and addresses it with `gs://` URIs, which is why the default works unchanged on a Kind cluster set up with
`kagent install`.
Change `substrate.snapshotLocation` only when your Substrate uses another object store; the value has to name a location
that store serves.

To use a model of your own instead of `default-model-config`, create the key Secret and let the chart create the
ModelConfig (the endpoint is a DNS name; here a Service in the cluster):

```sh
kubectl create secret generic pig-model-key -n kagent --from-literal=api-key="$MODEL_API_KEY"
cat > my-values.yaml <<'EOF'
modelConfig:
  create: true
  provider: OpenAI
  model: my-model
  baseUrl: http://my-model.models.svc.cluster.local:8080/v1
  apiKeySecret: pig-model-key
  apiKeySecretKey: api-key
EOF
./pig-kagent.sh install -n kagent -f my-values.yaml --set image="$IMAGE"
```

## 6. Send a first message

The kagent gateway serves each Agent at `/agents/{namespace}/{name}` on the controller's API listener (port 8083).
Forward it (and the UI) to your machine:

```sh
kubectl -n kagent port-forward svc/kagent-controller 8083:8083 &
kubectl -n kagent port-forward svc/kagent-ui 8080:8080 &
```

With kagent's default (insecure) authentication mode the user is a header. Read the Agent Card, then send a message:

```sh
curl -s -H 'X-User-Id: admin@kagent.dev' \
  http://localhost:8083/agents/kagent/pig/.well-known/agent-card.json

curl -s -H 'X-User-Id: admin@kagent.dev' -H 'A2A-Version: 1.0' -H 'Content-Type: application/json' \
  http://localhost:8083/agents/kagent/pig -d '{
    "jsonrpc": "2.0", "id": "r1", "method": "SendMessage",
    "params": {"message": {"messageId": "m1", "role": "ROLE_USER",
      "parts": [{"text": "List the files in your workspace."}]}}}'
```

The response is a task with a `contextId` (the session). Send later messages with that `contextId`. The first call
restores the Actor from its golden snapshot. `SendStreamingMessage` streams the
reply. In the UI, open `http://localhost:8080`, pick the `pig` agent in `kagent` and chat.

After a worker pod is deleted or replaced, the first message to an existing session can be refused with
`-32004 input was not accepted; retry after the session becomes available` (A2A error reason
`KAGENT_SEND_NOT_ACCEPTED`). In our measurements the refusal came after about 11 seconds and the immediate retry
succeeded, with the stored state intact. It is platform behaviour, seen with kagent's Claude harness as well as this
one, so a client retries that call. A normal suspend and restore between turns does not show it.

## 7. An approval round trip

Bind an MCP server with `requireApproval`. Any streamable-HTTP MCP server reachable by DNS name from the cluster
works; here one in the namespace `tools`:

```sh
cat > mcp-values.yaml <<'EOF'
mcpServers:
  - name: demo-tools
    url: http://demo-tools.tools.svc.cluster.local:8080/mcp
    description: Demo MCP server
    protocol: STREAMABLE_HTTP
    requireApproval: true
EOF
./pig-kagent.sh install -n kagent -f my-values.yaml -f mcp-values.yaml --set image="$IMAGE"
```

Ask the agent to use one of the server's tools (the model has to decide to call it; a scripted model that never calls a
tool never pauses). Instead of running it, the task stops in the `input-required` state.
The status message carries a human-readable description and, under the extension key
`https://kagent.dev/extensions/hitl/v1` in its metadata, a request:

```json
{"type": "tool_approval_request", "hint": "...",
 "tools": [{"id": "<tool id>", "call_id": "<call id>", "name": "<tool name>", "args": {}}]}
```

The ids are generated (UUIDs), so a decision needs three values from the `input-required` task returned by your last
call: the task id, its context id, and the `id` of each entry of `tools`. Save the response of the call that paused
(for example `curl ... > task.json`) and read them:

```sh
jq -r '.result.task | .id, .contextId, .status.message.metadata["https://kagent.dev/extensions/hitl/v1"].tools[].id' task.json
```

You can decide in the UI, or through the gateway: answer on the same task and context with a message that declares the extension:

```sh
curl -s -H 'X-User-Id: admin@kagent.dev' -H 'A2A-Version: 1.0' -H 'Content-Type: application/json' \
  http://localhost:8083/agents/kagent/pig -d '{
    "jsonrpc": "2.0", "id": "r2", "method": "SendMessage",
    "params": {"message": {"messageId": "m2", "role": "ROLE_USER",
      "taskId": "<task.id>", "contextId": "<task.contextId>",
      "extensions": ["https://kagent.dev/extensions/hitl/v1"],
      "metadata": {"https://kagent.dev/extensions/hitl/v1": {
        "type": "tool_approval_response",
        "approvals": [{"id": "<tools[].id from the request>", "approved": true}]}},
      "parts": [{"text": "approved"}]}}}'
```

Set `"approved": false` and an optional `"rejection_reason"` to reject; the model is told the call was rejected and why.
Send one decision for each entry of `tools`, each naming that entry's `id`. A decision whose id is unknown or repeated
(for example a copied placeholder) does not get an error reply: the task moves to `TASK_STATE_FAILED`, a terminal state
that cannot be resumed, and a later message on it is answered with a JSON-RPC error. Start a new task with a new
message. One approval is pending at a time.

## 8. Clean up

```sh
./pig-kagent.sh uninstall -n kagent
kubectl delete secret pig-model-key -n kagent        # if you created it
kind delete cluster --name pig
docker rm -f kind-registry
```
