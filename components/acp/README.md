# acp: Agent Client Protocol for PiG

Lets an editor that speaks the [Agent Client Protocol](https://agentclientprotocol.com) (ACP), such as
[Zed](https://zed.dev), drive a PiG agent: sessions, streamed answers, tool calls with diffs and terminal output,
cancel, model and thinking-level selection, context usage, and permission requests for extension dialogs.

It is a Go port of [pi-acp](https://github.com/svkozak/pi-acp) by Sergii Kozak (MIT). See [CREDITS.md](CREDITS.md)
and the port record, [port/PORT.md](port/PORT.md). **Pinned protocol: ACP version 1, schema of
`@agentclientprotocol/sdk` 0.26.0.** Built against PiG 0.4.0 (Pi 1.0.0); the numbers under Evidence were first recorded on PiG 0.3.0, and the end-to-end scenarios, the ACP reference client run and the Go tests pass again on 0.4.0.

The Package has two parts:

| Part | What it is |
|---|---|
| [`extensions/acp/cmd/pig-acp`](extensions/acp/cmd/pig-acp) | **The protocol entrypoint**, a separate executable with no dependency (not even the PiG SDK). The editor starts it and speaks ACP on its stdin and stdout; it starts `pig --mode rpc` for each session. It is not the TUI and shares nothing with it. |
| [`extensions/acp`](extensions/acp) | A small Go extension on the public SDK. `/acp` prints the editor configuration for the running agent and the limits below. |

An extension cannot speak ACP itself: a fused extension must not write protocol bytes to the host's stdout. So
**installing this Package, or building the `acp` Piglet, does not make the Piglet Binary an ACP server.** The editor
runs `pig-acp`, and `pig-acp --pig <your pig or Piglet Binary>` runs the agent. There is no one-command Binary yet
(see [RELEASE-BLOCKERS.md](../../RELEASE-BLOCKERS.md)).

## Use

Build the companion (Go 1.26 or later) and put it on your `PATH`:

```sh
cd components/acp/extensions/acp/cmd/pig-acp
go build -trimpath -o pig-acp .
pig-acp --version        # pig-acp 0.1.0 (ACP protocol version 1)
```

Zed, `settings.json` (`/acp` prints this with your paths filled in):

```json
{
  "agent_servers": {
    "PiG": { "type": "custom", "command": "pig-acp", "args": ["--pig", "/path/to/pig"], "env": {} }
  }
}
```

Other ACP clients run `pig-acp --pig <pig>` and speak newline-delimited JSON-RPC on stdio. `--pig` also takes a built Piglet
Binary (`--pig ./acp`). `--piglet <name-or-file>` starts pig with `--piglet ...`, and `--pig-arg <arg>` (repeatable) passes any
other argument. Without `--pig`, `PIG_ACP_PIG_COMMAND` (or `PI_ACP_PI_COMMAND`) is used, then `pig`.

Signing in: if pig has no model or key, `session/new` fails with the ACP "authentication required" error (-32000) and one
terminal auth method, `pig-acp --terminal-login`, which starts pig interactively in the editor's terminal.

| Flag or variable | Meaning |
|---|---|
| `--pig <command>` | the pig executable or Piglet Binary to drive |
| `--piglet <value>`, `--pig-arg <arg>` | extra pig arguments |
| `--terminal-login` | start pig interactively (sign in, configure keys) and exit with its status |
| `--version`, `--help` | print and exit |
| `PIG_ACP_PIG_COMMAND` | the pig executable when `--pig` is not given |
| `PIG_ACP_ENABLE_EMBEDDED_CONTEXT=true` | advertise embedded-context prompts (off by default) |
| `PIG_HOME`, `PIG_CODING_AGENT_DIR`, `PIG_CODING_AGENT_SESSION_DIR`, `PIG_USE_PI_DIRS=1` | pig's own directories are used as pig uses them (a leading `~` is expanded) |

The session map (ACP session id to pig session file) is `<PIG_HOME>/pig-acp/session-map.json`. Sessions are pig's own session
files, so they show up in `pig` too. `pig-acp` reads prompt templates from `<agent dir>/prompts` and `<cwd>/.pig/prompts` and
settings from `<agent dir>/settings.json` and `<cwd>/.pig/settings.json`. It never reads `auth.json` for itself; like pi-acp, it
reads a file that pig's `edit` or `write` tool changes, before and after, to send the diff to the editor.

## What is implemented

Advertised in `initialize`, and only these: `loadSession`, prompt `image` (embedded context only by the variable above; no
audio), `sessionCapabilities` `list` and `delete`, MCP `http` and `sse` **false**.

- `session/new`, `session/load` (history replayed, including tool results), `session/list` (paged by 50, scoped to the last
  cwd), `session/delete` (idempotent), `session/prompt`, `session/cancel`, `session/set_mode` (thinking level),
  `session/set_config_option` (model, thinking level), `session/set_model`, `authenticate`.
- Streaming: `agent_message_chunk`, `agent_thought_chunk`, `tool_call` and `tool_call_update` with locations, structured
  diffs for edit and write, `execute` calls with terminal output for bash, `usage_update`, `available_commands_update`,
  `config_option_update`, `current_mode_update`, `session_info_update`.
- Adapter-side slash commands that never reach the model: `/compact`, `/autocompact`, `/session`, `/name`, `/steering`,
  `/follow-up`, `/changelog`, `/export`. Prompt template files expand as `/name args`.
- **Permissions.** An extension `select` or `confirm` dialog becomes `session/request_permission`; the editor's answer is
  sent back to the extension. A client that fails or cancels means "cancelled". Nothing is ever executed locally in place of an
  editor's answer. `input` and `editor` dialogs are cancelled with a visible notice; `notify` becomes a chunk with its level.
- One live pig child per ACP connection: a new session or load closes the previous child. Closing stdin (or SIGINT/SIGTERM)
  stops every child and exits 0. Nothing but protocol messages is written to stdout.

## Not supported (by design, as upstream)

- File system and terminal delegation: `pig-acp` never calls `fs/*` or `terminal/*` on the editor; pig reads and writes files itself.
- MCP servers passed by the editor are accepted and stored, never started (PiG's own MCP support is configured in PiG).
- Extension slash commands (`/name` of an extension is not advertised, and as a prompt it runs without ending a turn: drive
  extensions through tools). `session/fork`, `session/resume`, `session/close` are Method not found.
- The npm "New version available" notice: PiG has no npm package (`pig update` uses a signed manifest).

## Evidence

Everything below was first run on PiG 0.3.0 and re-run on 0.4.0 (the 11 end-to-end scenarios, the reference client's 30 checked messages, the Go tests) with a temporary `HOME`, `PIG_HOME` and agent directories.

- **Twins of the upstream tests:** 124 exact twins with their upstream titles plus 1 named skip, checked by
  `pigeq twins check` against the upstream ledger (`port/upstream-tests.json`). Two more skipped tests name the remaining gaps (the npm update notice, the compaction event names); see `port/PORT.md`.
- **Unit and protocol tests:** `go test -race ./...` in both modules (a scripted ACP client over pipes against `Serve`).
- **End to end:** 11 scenarios (streaming, bash, diff, cancel, load/list/delete across two adapter runs, config options, a real
  permission round trip through a Go extension tool, no-model auth error, missing pig, MCP accepted, exit on stdin close) pass against
  a real `pig` **and against the built `acp` Piglet Binary** (`PIG_ACP_E2E_PIG=<binary> go test -run E2E`).
- **Reference client:** `components/acp/port/interop` runs the ACP SDK's `ClientSideConnection` 0.26.0 against `pig-acp` and the
  Piglet Binary and validates every message against the pinned schema: `npm ci --ignore-scripts` in `port/interop`, then
  `PIG_ACP=<pig-acp> PIG=<pig or Binary> PIGEQ=<pigeq> npm run test:acp-interop`. The run needs the `pigeq` built from
  this repository (`PIG_BIN=<pig> npm run build:pigeq` writes `dist/bin/pigeq`), which has the `llm` command; so does the twin
  ledger check (`pigeq twins check`).
- **Mutations:** 105 deliberate defects (`port/mutations.json`, `node components/acp/port/mutate.mjs`), 104 killed and 1 documented
  equivalent; results in `port/mutation-results.txt`.

**Not verified:** Zed and JetBrains themselves (no editor could be run here). The reference client is the interop evidence.
