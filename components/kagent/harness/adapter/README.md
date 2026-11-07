# kagent-pig: the adapter

`kagent-pig` runs [PiG](https://github.com/MichaelKinsy/PiG) as a native Harness of [kagent](https://github.com/kagent-dev/kagent).
It serves kagent's private A2A endpoint (port 80, `/readyz` on 8081) through kagent's own Go packages
(`adk/pkg/app`, `harness/runtime/a2a`, `harness/runtime/continuation`, pinned at
`v0.0.0-20261106154534-3548b7aaf4d9`) and drives one `pig --mode rpc` process per turn.

Licence: Apache-2.0 (this directory is part of the byo-ext tree). kagent is Apache-2.0 and PiG and Pi are separate
projects; this program is not affiliated with or endorsed by either.

## How a turn runs

1. `Run` writes the generated `models.json`, the policy file and the system prompt into the ephemeral `PIG_HOME`
   (`/run/kagent/pig`, never inside `/data`), then starts `pig --mode rpc --offline` in `/data/workspace` with
   `--session-dir /data/pig/sessions --session-id <continuation id>`. Before sending the prompt it checks `get_state`: model,
   provider and session id must match, otherwise PiG silently forked or fell back and the turn is refused.
2. PiG events become runtime events: text deltas, tool calls and results (MCP tools carry `MCPServer`/`MCPTool`), and one
   `ModelCall` per assistant `message_end`. Provider error text never reaches a failure message.
3. The policy extension (fused into the Piglet Binary) turns a gated call or an `ask_user` question into an
   `extension_ui_request{confirm}`. The adapter closes pig's stdin, waits for exit (nothing of the batch has run), writes
   `<DurableDir>/adapter/pig-pending.json`, and returns `Outcome{Pending}`. No process is held while parked.
4. `Resume` runs the approved MCP `tools/call` itself (or builds `Tool call was rejected by user.` followed by ` Reason: <trimmed reason>`
   when the reason is not blank, or formats the answers), appends one `toolResult` per call of the interrupted assistant message to the
   session file (siblings are reported "Not executed"), and starts a fresh pig with the prompt `Continue.`.
5. Tool artifacts are always closed: at the park the announced siblings of the gated call get `ToolDeclined`; the gated call gets its
   result from the continuation (`ToolDeclined` when rejected); a call that never got a result because of a cancel, a crash or an
   abandon is closed with `ToolUnfinished` (`IsError` set). Usage that pig writes after the cancel grace, while it is shut down, is
   still reported as a `ModelCall`. A restore that reports `ErrSessionAbandoned` has no event sink, so it cannot close anything.

## Environment

| Variable | Meaning |
|---|---|
| `KAGENT_BOOTSTRAP_JSON` | Controller descriptor (read by kagent's bootstrap package, never passed to pig) |
| `KAGENT_PIG_BIN` | pig binary, a fused Piglet Binary. An absolute path to an existing executable regular file owned by root or by the process user and not under `/data`; default `/usr/local/bin/pig`. There is no `PATH` lookup, because the model's tools can write to `/data` and a planted `pig` there would sit between the adapter and PiG. `-check` verifies this |
| `KAGENT_PIG_TOOLS` | built-in `--tools` allowlist, `name(,name)*`; default `read,bash,edit,write,grep,find,ls`. `ask_user` and one `mcp__<server>__*` pattern per bound MCP server are always added, because a non-empty `--tools` hides every MCP tool that is not named |
| `KAGENT_PIG_LOG_LEVEL` | `debug`, `info`, `warn`, `error`; overrides the compiled level |
| `KAGENT_PIG_CONTEXT_WINDOW`, `KAGENT_PIG_MAX_TOKENS` | model limits (kagent compiles none); defaults 128000 and 8192. PiG compacts by the context window |
| `KAGENT_PIG_HOME` | ephemeral `PIG_HOME` directory; when set it is used or startup fails. Default order: `/run/kagent/pig` if writable, else `<tmp>/kagent-pig-<uid>` (INFO, once, with the reason). Mode 0700 |
| `KAGENT_PIG_PASS_ENV` | extra variable names pig and its tools may inherit (never `KAGENT_*`) |
| provider key variables | `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `AZURE_OPENAI_API_KEY`, `GOOGLE_API_KEY` (becomes `GEMINI_API_KEY`), `MISTRAL_API_KEY`, `AWS_BEARER_TOKEN_BEDROCK`, `OLLAMA_API_BASE`, `OLLAMA_API_KEY`; set by the controller to its credential placeholder |

pig gets an allowlist only: `PATH HOME TZ LANG LC_* TMPDIR USER`, the egress trust-bundle variables (`SSL_CERT_*`,
`REQUESTS_CA_BUNDLE`, `AWS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`), proxy variables, plus
`PIG_HOME`, `PIG_OFFLINE=1`, `PI_SKIP_VERSION_CHECK=1`, `KAGENT_PIG_POLICY` and the generated `KAGENT_PIG_HDR_n` header values.
`PATH` is rebuilt, not copied: `/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin` first, then the other inherited
absolute entries, and the entries under `/data` last, so a file the model writes into `/data/.local/bin` never shadows a system binary.
kagent projects only the egress CA, and several tools treat the CA variable as the whole trust store, so the adapter writes
`PIG_HOME/ca-bundle.pem` (system roots plus the egress bundle) and points the CA variables of pig and its tools at it.

## Files

| Path | Content |
|---|---|
| `/data/workspace` | cwd of every pig process (constant: PiG's session lookup is cwd-scoped) |
| `/data/pig/sessions` | PiG session JSONL; exactly one file per id (startup assertion) |
| `/data/adapter/state.json` | kagent continuation id (UUID or `[A-Za-z0-9._-]`) |
| `/data/adapter/pig-pending.json` | pending record, mode 0600, atomic replace; phases `waiting executing result continuing complete canceled abandoned` |
| `/run/kagent/pig` or `/tmp/kagent-pig-<uid>` | ephemeral `PIG_HOME` (under Substrate `/run/kagent` is a read-only projection, so `/tmp` is the normal choice; it is a tmpfs that counts against the Actor's memory, at most half of `limits.memoryBytes`; these files are small, a few KB for `models.json`, the policy and the prompt, plus the CA bundle at most a few hundred KB, and nothing else is written there by the adapter; pig adds only small caches): `agent/models.json`, `kagent-policy.json`, `system-prompt.md`, `ca-bundle.pem`;  `agent/` is deleted and rebuilt every turn, so nothing the model writes there (`mcp.json`, settings, trust decisions) is read by the next pig |

## Compiled configuration

Unsupported fields stop startup with every field named (`internal/config`): `sse_tools`, `stdio_tools`, `remote_agents`,
`sub_agents`, `memory`, `agent_plugins`, `skills_directory`, `output`, `token_passthrough`, `allowed_headers`, per-server and
per-model TLS options, `api_key_passthrough`, `gemini_vertex_ai`, `gemini_anthropic`, `sap_ai_core`, Foundry's Anthropic format,
`openai.token_exchange`, `openai.n > 1`, `gemini.base_url`, runtime `token_propagation`. Fields accepted without effect
(for example `context_config.compaction`, unsupported sampling parameters, timeouts, `stream=false`) are logged once at startup as
`configuration accepted but not honoured`. Header values and provider keys are never written into a PiG-readable file:
they are `${KAGENT_PIG_HDR_n}` / `${PROVIDER_KEY}` references, because PiG runs a header that starts with `!` as a shell command.

## Failure modes

- A crash after `executing` or `continuing` was recorded: `runtime.ErrSessionAbandoned`; an approved call is never re-executed.
- The MCP server unreachable before `tools/call` was sent: the request stays pending and the human can answer again.
- Any other MCP error after the request left: abandoned (the call may have run).
- pig cannot be reaped after SIGKILL: `runtime.CancellationError`.
- Session header version other than 3, or a session file that no longer ends at the recorded assistant entry: the resume fails closed.
- Two session files with one id (the id was used from another cwd): the turn is refused.
- A confirm request whose call is not in the assistant message the session ends at, whose tool name differs, or whose input is not an object: the turn fails and nothing is parked.
- MCP server names are derived from the URL host label and capped at 40 bytes so the policy extension's approval gate (a name prefix match) survives PiG's 64 character tool name shortening; the compiled limits of the policy parser (64 servers, 32 headers, 256 tools, 2048 byte URL, 3600 s timeout) are enforced at startup.

## Threat notes and limits

- Workspace context files (`AGENTS.md`, `CLAUDE.md`) are loaded into pig's system prompt, as the Claude and Codex harnesses do. The model can
  write them, so they carry model-chosen instructions across turns.
- pig's environment, which its `bash` tool inherits, holds the provider key variables and the `KAGENT_PIG_HDR_n` header values. By kagent's design
  these are the credential placeholder or literal non-secret values; Secret-backed credentials are injected by the gateway outside the sandbox.
- The allowlist and the approval gate are model-facing filters, not network controls: `bash` can reach any origin the Actor's egress grant allows.
- An approved MCP result is injected with at most 50 KiB of text and 1 MiB of base64 image data; the rest is replaced by a
  `[truncated: N bytes ...]` line and the dropped byte count is logged (never the content).
- The adapter's own MCP client trusts the same combined bundle as pig (system roots plus the egress CA), not the egress-only file.
- Cancelling while the approved call is in flight (phase `executing`), or when pig cannot be reaped, writes `abandoned`, not `canceled`: the call may
  have run, so the next restore reports `runtime.ErrSessionAbandoned`. Cancelling after the result was recorded appends that result to the session first.
- A pending record that cannot be decoded or validated is moved to `pig-pending.json.corrupt` and reported once as `ErrSessionAbandoned`; the Actor
  takes new work afterwards.

## Checks

`kagent-pig -check` validates the bootstrap configuration and the pig binary (path rules above, then `pig version`), then exits
without touching `/data`. The Go tests, the PiG v0.7.0 session fixtures and the opt-in real-pig scenarios live in the Pigpen
repository (`components/kagent/harness/adapter`) and are not part of this exported tree.
