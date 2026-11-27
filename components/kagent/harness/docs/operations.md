# Operating the PiG harness

How the harness keeps state, how to read it, and what to do when something goes wrong. Install and upgrade steps are
in [deploy.md](deploy.md). The terms are kagent's: an *Actor* is the sandbox (a gVisor sandbox managed by Agent
Substrate) that runs one kagent Session; a *turn* is one user message handled by the harness.

## What runs in the Actor

- `/usr/local/bin/kagent-pig` is the adapter. It serves kagent's private A2A endpoint (port 80, `/readyz` on 8081) through
  kagent's own Go packages and drives PiG.
- PiG is a Piglet Binary (the `kagent` Piglet: Stock PiG with the `kagent-policy` extension fused in). The adapter starts
  one `pig --mode rpc --offline` process per turn in `/data/workspace` and holds no process between turns. The extension is
  inert without a policy file; the adapter writes one for every turn. It registers the MCP servers bound in the
  AgentTemplate, asks for approval before calls that need it, and offers `ask_user`.
- The Piglet's extension surface is closed: an extension placed in the working directory is never loaded, and the binary
  cannot update itself. It does not restrict PiG's file tools or `bash`: a tool allowlist is a filter for the model, not a
  network control. What the Actor can reach is decided by egress ([deploy.md](deploy.md#egress)).
- `pig` gets an allowlist of the environment only (`PATH HOME TZ LANG LC_* TMPDIR USER`, the trust-bundle and proxy
  variables, and the few variables the adapter sets), plus the names in `runtime.passEnv`. kagent projects only the egress
  gateway's CA, and several tools treat the CA variable as the whole trust store, so the adapter writes a bundle of the
  system roots plus the egress CA and points the CA variables of `pig` and its tools at it.
- Header values and provider keys are never written into a file PiG reads. They are `${KAGENT_PIG_HDR_n}` and
  provider-key references resolved from the process environment, because PiG runs a header that starts with `!` as a shell
  command.

## What is persisted, and where

kagent mounts one durable directory into the Actor at `/data`. Substrate snapshots that volume when the Actor
suspends and restores it when the Actor resumes. The harness keeps these there:

| Path | Content |
| --- | --- |
| `/data/workspace` | The working directory of every `pig` process and of the tools it runs. It never changes, because PiG's session lookup is scoped to the working directory. Files the agent creates stay here between turns. |
| `/data/pig/sessions` | PiG session files (JSONL), exactly one file per session id. |
| `/data/adapter/state.json` | The continuation id kagent's runtime library stores for the Actor (a UUID or `[A-Za-z0-9._-]`), which is the PiG session id. |
| `/data/adapter/pig-pending.json` | The pending record of an approval or question, mode 0600, replaced atomically. Phases: `waiting`, `executing`, `result`, `continuing`, `complete`, `canceled`, `abandoned`. |

Everything generated per turn lives outside `/data`, in an ephemeral PiG home: `agent/models.json`,
`kagent-policy.json`, `system-prompt.md` and `ca-bundle.pem`. The adapter prefers `/run/kagent/pig`, which the
unprivileged Actor user (uid 65532) cannot create in the Substrate sandbox, so in an Actor it uses the temporary directory
`/tmp/kagent-pig-65532` and logs `using a temporary directory for PIG_HOME`. Credentials and the policy file are therefore
never part of a snapshot.

The public conversation (tasks, history, streaming) is owned by kagent's task store in its PostgreSQL database and is
independent of `/data`.

## A session survives a restart

A new `pig` process runs each turn, with `--session-dir /data/pig/sessions --session-id <continuation id>`. Before the
prompt is sent, the adapter reads PiG's state and requires the model, the provider and the session id to match what it
expects; if PiG forked or fell back silently, the turn is refused. After Substrate suspends and resumes an Actor, or
restores `/data` into a new one, the next turn continues from the session file. The adapter reads the pending record
from `/data` on every call for the same reason, because Substrate may swap `/data` under a process that has just
started.

An approval pauses the turn without holding a process. The extension turns the gated call into a confirm request; the
adapter closes `pig`'s stdin and waits for it to exit (nothing of that batch has run), writes the pending record and
returns the task as `input-required`. When the person decides, the adapter runs the approved MCP `tools/call` itself, or
builds `Tool call was rejected by user.` followed by `Reason: ...`, or formats the answers to a question. It appends one
tool result for each call of the interrupted assistant message to the session file (sibling calls are reported
`Not executed` and the model may issue them again) and starts a fresh `pig` with the prompt `Continue.`.

## Logs

- `runtime.logLevel` (`KAGENT_PIG_LOG_LEVEL`) sets the adapter's level: `debug`, `info`, `warn` or `error`. It overrides
  the level of the compiled configuration. Provider error text never reaches a failure message.
- While the Actor is running, read its container logs with Substrate's `kubectl-ate` plugin:
  `kubectl ate logs actor session-<contextId> -a <atespace> [-f]` (`--source=lifecycle` shows Substrate's lifecycle
  events). The atespace of a kagent Agent is its namespace, and `kubectl ate get actors -a kagent` lists the Actors and
  their state. kagent suspends an Actor right after a turn, and for a suspended Actor the plugin answers
  `actor ... is not currently running on any worker pod`.
- For a suspended Actor read the worker pod instead: `kubectl -n kagent logs deploy/kagent-default` (the Deployment has the
  name of the WorkerPool). Every line carries the labels `ate.actor.name` (`session-<contextId>`) and
  `ate.actor.container.name`; the adapter's lines are JSON and have container name `kagent`.
- Installing the plugin: download `kubectl-ate-<os>-<arch>` and its `.sha256` from the
  [Substrate v1.0.0 release](https://github.com/kagent-dev/substrate/releases/tag/v1.0.0) (linux and darwin, amd64 and
  arm64), check the checksum, make it executable and put it on `PATH` as `kubectl-ate`; or `go install ./cmd/kubectl-ate`
  from the Substrate repository. It needs a kubeconfig and port-forwards to the Substrate API by itself. By default it acts
  as the shared identity `system:serviceaccount:ate-system:ate-client` and prints a warning saying so; `--token-file`
  makes it act as you.
- Unsupported compiled fields stop the adapter at startup with every field named ([deploy.md](deploy.md#model-credentials));
  the Agent then does not become `Ready` ([deploy.md](deploy.md#troubleshooting-by-condition)). Fields accepted without
  effect are logged once at startup as `configuration accepted but not honoured`.
- `kagent-pig -check` validates the bootstrap configuration and runs `pig version`, then exits without touching `/data`.
  It is meant to run inside an Actor: it needs the `KAGENT_BOOTSTRAP_JSON` descriptor and the control plane, and outside
  a cluster it stops with `decode bootstrap descriptor`. For a smoke test of an image outside a cluster use
  `docker run --rm --entrypoint pig IMAGE version`.
- Controller side: `kubectl -n kagent logs deploy/kagent-controller`. Substrate side: the `atelet` DaemonSet and the
  `ate-api-server` Deployment in the Substrate namespace.
- Traces and metrics: set `OTEL_*` through the chart's `env` ([deploy.md](deploy.md#telemetry)).

## Memory and limits

- Each Actor gets the CPU and memory limits of kagent's `config.runtimeDefaults.cpu` and `.memory` (chart defaults `1`
  and `1Gi`), for the whole Actor and for every Harness alike. They are not Harness fields.
- Measured on the PiG v0.7.0 release with a scripted mock model: starting `pig` for a turn takes about 45 ms; a running
  `pig` process has about 54 MB RSS (with the approval gate loaded); the adapter alone idles at about 37 MB. These numbers
  include no real model and no heavy tool use; a build, a test run or a large repository is the agent's own consumption,
  bounded by the Actor limit.
- The cgroup working set of the worker pod's Actor is the figure kubelet and cAdvisor report; RSS and PSS differ from it
  under gVisor, which accounts application memory as shared memory.
- kagent compiles no model limits, so the harness assumes a context window of 128000 tokens and 8192 output tokens.
  PiG compacts its session by the context window: set `runtime.contextWindow` and `runtime.maxTokens` to the real values of
  your model. `context_config.compaction` of the compiled configuration is accepted without effect.
- Substrate allows 32 environment variables per Actor; the chart caps `env` at 16 for that reason.

## Upgrading the image digest

Run the installer again with the new digest ([deploy.md](deploy.md#upgrade)). Existing Sessions keep the revision and
image they started with; Sessions created afterwards use the new one. The PiG session file format is a compatibility
surface: the adapter accepts only session header version 3, and the harness image pins the PiG version it was built and
tested with. Roll an image back by installing the previous digest; new Sessions then use it.

## After a worker pod is replaced: retry on `-32004`

After a worker pod is deleted or replaced (we measured `kubectl delete pod` on a worker), the first message to an
existing session can be refused with the JSON-RPC error
`-32004 input was not accepted; retry after the session becomes available` (A2A error reason
`KAGENT_SEND_NOT_ACCEPTED`, `retryAfterMs: 100`). In our measurements the refusal arrived after about 11 seconds, while
the controller marked the old worker draining and registered the new one, and the immediate retry succeeded in one to
three seconds with the session's stored state (the agent still recalled what it had been told). The behaviour is the
platform's, not the harness's: kagent's built-in Claude harness showed the same refusal. A normal suspend and restore
between turns does not show it. A client that can meet a worker replacement retries the call on `-32004`; the harness
cannot do that for it.

## Failure modes and `ErrSessionAbandoned`

| Situation | Result |
| --- | --- |
| A crash after `executing` or `continuing` was recorded | `runtime.ErrSessionAbandoned`. An approved call is never executed again. |
| The MCP server is unreachable before `tools/call` was sent | The request stays pending and the person can answer again. |
| Any other MCP error after the request left | Abandoned: the call may have run. |
| `pig` cannot be reaped after SIGKILL | `runtime.CancellationError`. |
| Session header version other than 3, or a session file that no longer ends at the recorded assistant entry | The resume fails closed. |
| Two session files with one id (the id was used from another working directory) | The turn is refused. |

`ErrSessionAbandoned` reaches the Agent's task as `deferred continuation cannot be resumed; start a new session`. A person
had decided on a pending call, and the harness recorded the decision and then failed before the continuation finished, so
the call may already have run.

What to do:

1. Start a new Session (a new `contextId`, or a new conversation in the UI).
2. If the interrupted action had an effect you care about (a write through an MCP tool), check its result in the
   target system before asking again.
3. If it happens repeatedly, read the Actor logs around the failure ([Logs](#logs)).

A turn that is only waiting for a decision (`input-required`) is not an error. A cancel while an approval is pending
discards the pending record without running the call.

## Common checks

```sh
./pig-kagent.sh check -n kagent                       # cluster-level causes
kubectl get agents.api.kagent.dev pig -n kagent -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
kubectl get workerpool -n kagent                      # pool desired/ready replicas
kubectl get egresspolicy -n kagent                    # grants the Agent selects
```
