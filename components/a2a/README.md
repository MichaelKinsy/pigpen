# a2a: PiG speaks A2A

Serve PiG tasks to [A2A](https://a2a-protocol.org) (Agent2Agent) agents, and call remote A2A agents from PiG.
Built on the upstream Go SDK [a2a-go](https://github.com/a2aproject/a2a-go) v2.6.0 (Apache-2.0, see
[CREDITS.md](CREDITS.md)), pinned to **A2A protocol 1.0**, JSON-RPC binding. It interoperates with
[kagent](https://github.com/kagent-dev/kagent), which speaks the same protocol through the same SDK; Kubernetes
orchestration is not ported.

**The listener is off unless you configure it.** Installing the Package opens no port. It registers three tools and one
command; the tools do nothing until a remote is configured.

```sh
pig install ./components/a2a          # builds from source on first use (Go toolchain), or fuses into a Piglet Binary
```

Original work (MIT) on a dependency (Apache-2.0). Go only, public PiG SDK, no Node. Not a published release.

## Serve PiG tasks

Create `$PIG_CODING_AGENT_DIR/a2a.json` (default `~/.pig/agent/a2a.json`; `PIG_A2A_CONFIG` names another file):

```json
{
  "listen": "127.0.0.1:8787",
  "name": "my-pig",
  "tokens": [
    {"name": "ci",   "tokenEnv": "A2A_TOKEN_CI",   "tenant": "team-a"},
    {"name": "alice", "tokenEnv": "A2A_TOKEN_ALICE"}
  ],
  "worker": {"cwd": "/srv/project", "provider": "anthropic", "model": "claude-sonnet-4-5", "passEnv": ["ANTHROPIC_API_KEY"]},
  "maxConcurrentTasks": 4,
  "taskTimeoutSeconds": 900,
  "taskRetentionDays": 30
}
```

`PIG_A2A_LISTEN=host:port` and `pig --a2a-listen host:port` override `listen` (flag over environment over file).
Tokens are **only** named by environment variable; the file never holds a secret, and an inline `token` key is refused.

What the server does:

| | |
|---|---|
| Endpoint | JSON-RPC 2.0 at `/`, agent card at `/.well-known/agent-card.json` (public; declares the Bearer scheme). Streaming (`SendStreamingMessage`) and `SubscribeToTask` work. Push notifications, an extended agent card, gRPC and REST are not offered: the card says so, and a call for one is refused with `@pi-in-go/pigpen-a2a does not support <feature> yet.` (never accepted and ignored: a message that carries a push configuration is refused whole). |
| Protocol | `A2A-Version: 1.0` is required on every call. Any other value, and a missing header (which the specification reads as 0.3), is answered `VERSION_NOT_SUPPORTED` before anything runs. |
| Tasks | Each A2A task is one turn of a PiG worker: a fresh `pig --mode rpc` process. Text goes in; assistant text streams out as artifact chunks; the task ends `completed`, `failed` or `canceled`. |
| Parts in | Text parts, raw parts that are text (`text/*`, JSON, XML, YAML, TOML, JavaScript; UTF-8 without NUL bytes) and data parts go into the prompt, each under a one-line `[attachment "name" (type)]` label in a code fence longer than anything inside it, so an attachment cannot close its own fence. png, jpeg, gif and webp raw parts (at most 8) are sent to the worker as images. Everything else is refused, with the reason, before any worker starts: a binary type, a raw part with no media type, a charset other than UTF-8, an empty part, a URL part (the worker would fetch a peer-supplied URL). The 256 KiB prompt limit counts the inlined attachments. The card's input modes list what is accepted; output is `text/plain`. |
| Persistence | Tasks are stored, one owner-only file each under `<stateDir>/tasks` (the file name is a digest of the task id, never the id), so `GetTask`, `ListTasks` and `SubscribeToTask` answer after the listener restarts, with the same per-principal ownership (another principal gets "task not found"). A task that was running when the listener stopped is reported `failed` with the reason (the operator's `/a2a` log says how many). A task that only grew (streamed text) is written at most once a second; a state change and the end of a task are on disk when they happen. Finished tasks are kept `taskRetentionDays` (default 30) days; an unfinished task is never dropped. A task file holds the conversation, so `stateDir` is as private as the session files. Give each listener its own `stateDir`: a second listener on the same directory would read the first one's running tasks as interrupted and mark them failed. `SubscribeToTask` on a finished task is refused by the specification (`UNSUPPORTED_OPERATION`): read it with `GetTask`. |
| Context | A `contextId` **is** a PiG session: a later task with the same `contextId` continues the conversation (the session file persists on disk). Tasks of one context run one at a time. |
| Cancellation | `CancelTask` sends the worker the RPC `abort`, waits for it, then kills the process group after `graceSeconds`. Closing the listener, a reload or quitting PiG cancels every running task. `taskTimeoutSeconds` fails a task that runs too long. |
| Authentication | Static bearer tokens (`Authorization: Bearer ...`, header only, compared in constant time on SHA-256 digests). No token, no work: a listener needs at least one, or `insecureNoAuth` on a loopback address. An `insecureNoAuth` listener answers only requests whose `Host` is a loopback name or address (`403` otherwise), so a web page that rebinds its own name to `127.0.0.1` cannot drive it. `tls.certFile`/`keyFile` serve HTTPS. |
| Tenants | Every token is a principal; tokens sharing a `tenant` share tasks and contexts, everything else is its own boundary. Task lists, `GetTask`, `CancelTask`, `SubscribeToTask` and follow-up messages are scoped to it, the session file is derived from `(principal, contextId)` so two tenants that pick the same `contextId` never share a conversation, and a request that names another tenant than its credential's is refused. This boundary is in the protocol only: every worker runs as the same operating-system user, so once you give workers file tools (below) a tenant can read the other tenants' session files. |
| Safety | The worker runs with **no tools** unless `worker.tools` names some (`pig --no-tools`), no extensions, skills or context files, an empty private working directory unless you set `worker.cwd`, and only an allowlisted environment (provider keys must be named in `passEnv`; the A2A tokens never reach it). It cannot start another listener. Requests are capped at 1 MiB, prompts at 256 KiB. Provider error text is never sent to the peer. |

Give the worker tools only with intent. PiG's file tools are **not confined** to `worker.cwd`: they take absolute paths and `~`.
`"worker": {"tools": ["read", "grep", "find", "ls"]}` lets every token holder read any file the operating-system user running PiG
can read, including PiG's own credentials (`auth.json`), SSH keys and other tenants' session files; adding `edit`, `write` or
`bash` lets them change or run anything that user can. Run a listener with tools as a dedicated user (or in a container) that
holds only the workspace and the provider key, one per trust boundary.

`worker.command` (or `PIG_A2A_PIG`) names the pig executable; the default is `pig` on `PATH`. In a Piglet Binary set it to the
Binary itself. A running server is shown by `/a2a`.

## Call remote agents

```json
{
  "remotes": {
    "kagent": {"url": "http://localhost:8083/api/a2a/kagent/helm-agent", "bearerTokenEnv": "KAGENT_TOKEN", "skipCard": true},
    "review": {"url": "https://agents.example.com/review", "bearerTokenEnv": "REVIEW_TOKEN"}
  }
}
```

| Tool | Use |
|---|---|
| `a2a_agents` | List the configured remotes with their cards (name, description, skills). |
| `a2a_send` `{agent, message, contextId?, taskId?, wait?}` | Send a task and wait; returns `state`, `taskId`, `contextId` and the reply. Streams progress. Cancelling the tool call cancels the remote task. With `wait: false` it returns as soon as the remote has the task (it keeps running there) and says how to follow it. |
| `a2a_task` `{agent, action: get|cancel|list|wait, taskId?, waitSeconds?}` | Inspect, cancel or follow remote tasks. `wait` follows a running task (SubscribeToTask where the remote streams, polling where it does not), streams its text as progress, and returns when the task is finished or needs input, or after `waitSeconds` (1 to 3600, default the remote's `timeoutSeconds`). Running out of time returns the task's current state, not an error, and leaves the task running; so does cancelling the wait. |

`/a2a` shows the listener and the remotes. Credentials: `bearerTokenEnv`, or `headerEnv` (`{"X-Api-Key": "MY_KEY_VAR"}`), both
environment variable names; a missing variable is an error before any request. Credentials go **only** to the configured origin
(scheme and host; an `https` remote's token is never sent over `http`): a card that names another origin is refused unless `skipCard` is set (kagent's card carries an in-cluster URL, so use `skipCard` and the
routable URL). Redirects are not followed. Only A2A 1.0 JSON-RPC interfaces are used; a card that offers only 0.3 is refused.
Data you send to a remote leaves the machine.

## Layout

```text
extensions/a2a/         the Go extension (package a2aext): config, auth, worker, executor, server, client, extension; tests
port/PORT.md            contract table, gaps, host findings, proof results (this is an original adapter: there is no Pi oracle)
port/scenarios,golden   host-level scenarios recorded from the port itself under PiG (self-recorded regression, not equivalence)
port/mutations.json     166 deliberate defects; every one is caught (port/mutation-run.txt is the earlier 109, mutation-run-a2a-1.txt the 166)
port/interop/kagent/    kagent-compatible endpoint used by scripts/interop-kagent.mjs
port/red.txt            the RED run of the tests on a stub; red-a2a-1.txt the RED run of the task store, parts, wait and refusal tests
```

Tests: `PIG_BIN=pig npm run test:go-ports` (unit and fake-host, `-race`); real PiG end to end:
`PIG_A2A_E2E_BIN=pig go test -run E2E` in `extensions/a2a` (temporary HOME, PIG_HOME and PIG_CODING_AGENT_DIR, a local fake
model server); the Piglet Binary hosting the listener: `PIG_A2A_BINARY=<binary built from piglets/a2a> go test -run Binary`; kagent: `KAGENT_GO_DIR=<kagent>/go PIG_BIN=pig node scripts/interop-kagent.mjs`.
