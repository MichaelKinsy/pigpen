# Port record: pi-acp

| Input | Identity |
|---|---|
| Original | `svkozak/pi-acp` 0.0.34, commit `b0581c9c1d675e634234674484247008b03d69b4` (2026-09-24), MIT (Sergii Kozak) |
| Original's ACP layer | `@agentclientprotocol/sdk` 0.26.0 (Zed Industries, Apache-2.0), protocol version 1, `schema/schema.json` |
| Oracle | the original's own test suite (125 test titles in 37 files, `port/upstream-tests.json`) as twins, and the reference SDK client (`ClientSideConnection` 0.26.0) plus the pinned schema for the wire |
| Target | PiG 0.3.0+0.87.1 from `staging/team/lead/release-all-next` `63c6ba456bf5edd35c8042ce420ad700dd59a4ad`, go1.27.1 |

## What kind of port this is

pi-acp is **not a Pi extension**. It is a Node executable that speaks ACP JSON-RPC on its own stdio and
starts `pi --mode rpc` as a child. A PiG extension cannot be an ACP server: a fused extension must not write
protocol bytes to the host's stdout (roadmap, "Packaging boundary"). So the port has two parts:

- **`extensions/acp/cmd/pig-acp`**: the companion executable, plain Go with no dependency (not even the PiG SDK),
  its own module (PiG rejects a factory and a `package main` in one module). It is the explicit protocol
  entrypoint, separate from the TUI: `pig-acp --pig <pig or Piglet Binary>`.
- **`extensions/acp`**: a small Go extension on the public SDK. `/acp` prints the editor configuration for the
  running executable and the declared capability limits. A Piglet needs at least one extension to build a Binary,
  and this one belongs to the feature.

Because the source has no Pi extension API, the Skill's differential harness (`pigeq run/record/check`) has nothing to
host: the original and the port are compared by **ported tests** (exact twins), a **scripted ACP client against the
protocol schema**, and the **ACP reference client** (`@agentclientprotocol/sdk` `ClientSideConnection`) against a real
built Piglet Binary. See "Results" below.

## Contract table

| ID | Upstream file and symbol | Behavior | Go | Test | Disposition |
|---|---|---|---|---|---|
| A1 | `index.ts` stdio wiring | ndjson JSON-RPC 2.0 on stdin/stdout, one message per line, serialized writes | `internal/jsonrpc` | `jsonrpc_test.go`, `main_test.go` | mapped |
| A2 | `index.ts` stdout writer | a destroyed stdout never crashes; the run ends with exit 0 | `jsonrpc.Conn`, `main.run` | "stdout writer: resolves even if stdout is destroyed" (twin), `TestServing` | mapped |
| A3 | `index.ts` shutdown | stdin `end`/`close`, SIGINT, SIGTERM dispose every pi child, exit 0 | `Serve`, `main` | `TestServeSessionFlow` "closing stdin disposes every pi child", `TestE2EClosingStdinStopsThePigChild` | mapped |
| A4 | `index.ts` `--terminal-login` | starts the pi executable with inherited stdio, returns its status; ENOENT prints how to install and exits 1 | `main.run` | `TestTerminalLogin` | mapped (text says PiG) |
| A5 | `agent.ts` `initialize` | protocol version 1 only; `agentInfo`; capabilities: `loadSession`, `mcpCapabilities {http:false, sse:false}`, `promptCapabilities {image, audio:false, embeddedContext:<env>}`, `sessionCapabilities {list, delete}` | `Agent.Initialize` | `TestServeInitialize`, embedded-context twins | mapped; env `PIG_ACP_ENABLE_EMBEDDED_CONTEXT`, `PI_ACP_ENABLE_EMBEDDED_CONTEXT` still accepted |
| A6 | `auth.ts` | one terminal auth method; `_meta["terminal-auth"]` only when the client asks | `AuthMethods` | auth-methods twins | mapped (label `Launch pig`) |
| A7 | `auth-required.ts` | 11 substrings turn an error into AUTH_REQUIRED (-32000) with the auth methods | `MaybeAuthRequiredError` | `TestAuthRequiredError` | mapped |
| A8 | `agent.ts` `newSession` | absolute cwd; spawn; `get_state` + `get_available_models` in parallel; zero models, auth errors and config failures clean up the new session only (file, map entry, child); models/modes/configOptions; startup info; one live child per connection; commands and usage after the response | `Agent.NewSession` | new-session twins, `TestSessionConfigOptions`, `TestNewSessionPolicies` | mapped |
| A9 | `agent.ts` `loadSession` | absolute cwd; close a live copy; stored or discovered session; restore with `--session`; configuration must succeed before history is replayed; failure closes the restored child and leaves history and map entry alone | `Agent.LoadSession` | load twins, `TestLoadConfigurationFailure` | mapped |
| A10 | `agent.ts` history replay + `pi-messages.ts` | user/assistant text, tool results as synthetic tool calls, bash as terminal | `LoadSession`, `Normalize*` | "loadSession replays toolResult", pi-messages twins | mapped |
| A11 | `agent.ts` `listSessions` | scoped to the last session cwd when none is given; cursor paging by 50; invalid cursor reads as 0 | `Agent.ListSessions` | list twins, `TestNewSessionPolicies` | mapped |
| A12 | `agent.ts` `deleteSession` | removes the file and the map entry; unknown ids succeed | `Agent.DeleteSession` | delete twins | mapped |
| A13 | `agent.ts` `setSessionMode`, `setSessionConfigOption`, `unstable_setSessionModel` | thinking level and model; the update pair is sent only after pi's own state was read back and is consistent; the model change refreshes usage | same | model-thinking-levels twins (about 40), context-usage twins | mapped |
| A14 | `agent.ts` `prompt` built-ins | `/compact`, `/session`, `/name`, `/steering`, `/follow-up`, `/changelog`, `/export`, `/autocompact` are handled in the adapter and never reach the model | `Agent.Prompt` | builtin twins, `agent_builtin_test.go` | mapped; `/changelog` looks beside the pig binary (gap, below) |
| A15 | `agent.ts` `prompt` result | `error` maps to `cancelled` if cancel was requested, else `end_turn` | `Agent.Prompt` | `TestPromptRouting` | mapped |
| A16 | `agent.ts` `cancel` | ignores unknown sessions, never restores one | `Agent.Cancel` | "cancel ignores stale session IDs" twin | mapped |
| A17 | `agent.ts` available commands | pi `get_commands` (extension commands hidden, skill commands per setting) merged with the built-ins; prompt files as the fallback | `ToAvailableCommandsFromPiGetCommands`, `MergeCommands` | pi-commands, merge twins, `TestAgentCommandsAdvertised` | mapped |
| A18 | `agent.ts` startup info / update notice | markdown block (version, context, skills, prompts, extensions); `quietStartup` suppresses it; **npm update notice** | `BuildStartupInfo` | startup-info twins | mapped except the update notice: **skipped, gap** |
| S1 | `session.ts` turn lifecycle | a turn ends only on `agent_settled`; retry runs and `turn_end`/`agent_end` never end it | `Session` | session-events twins | mapped |
| S2 | `session.ts` queue | prompts queue behind a running turn; cancel clears the queue and aborts; queue depth in `session_info_update` | `Session.Prompt/Cancel` | queue twins, `session_extra_test.go` | mapped |
| S3 | `session.ts` streaming | `text_delta` to `agent_message_chunk`, `thinking_delta` to `agent_thought_chunk`, tool call events, monotonic status | `Session.handlePiEvent` | session-events twins | mapped |
| S4 | `session.ts` tool locations and diffs | relative paths resolved against the cwd, `edit` line from a unique `oldText`, structured diff from before/after file contents | same | session-diff twins | mapped |
| S5 | `bash.ts` | bash as a display terminal via `_meta` (`terminal_info`, `terminal_output` deltas, `terminal_exit`) | `Bash*` | `TestBashHelpers`, tool-call twins | mapped |
| S6 | `session.ts` usage | `usage_update` from `get_session_stats.contextUsage` before the turn resolves; never breaks the turn | `Session.PublishContextUsage` | usage twins | mapped |
| S7 | `session.ts` retry notices | `auto_retry_start` / `auto_retry_end` chunks | same | retry twins | mapped |
| S8 | `session.ts` compaction notices | `auto_compaction_start` / `auto_compaction_end` chunks | same | compaction twins | mapped as upstream; **PiG never sends these names**, see finding F1 |
| S9 | `session.ts` extension UI | `select` and `confirm` become `session/request_permission`; `input` and `editor` are cancelled with a visible notice; `notify` becomes a chunk with its severity; anything else is cancelled | same | permission twins, `TestSessionExtensionUI`, `TestE2EExtensionSelectBecomesPermissionRequest` | mapped |
| S10 | `prompt.ts` | text, resource links, embedded resources, images, audio marker | `PromptToPiMessage` | prompt twins | mapped (marker names pig-acp) |
| S11 | `pi-tools.ts` | tool result text: diff, content, stdout/stderr/exit code, JSON | `ToolResultToText` | pi-tools twins | mapped |
| S12 | `slash-commands.ts` | prompt template files, `$1`/`$@` expansion, bash-style quotes | `LoadSlashCommands`, `ExpandSlashCommand` | slash twins | mapped; directories are PiG's |
| P1 | `process.ts` | spawn `pig --mode rpc --no-themes [--session file]`, ENOENT/EACCES to a spawn error, handshake `get_state`, correlate ids, drop unknown responses, per-request timeout | `internal/pirpc` | request-timeout, session-path twins | mapped |
| P2 | `process.ts` `getAvailableThinkingLevels` | non-empty list of non-empty strings, or an "invalid levels" error | `Process.GetAvailableThinkingLevels` | thinking-level-rpc twins | mapped |
| P3 | `command.ts` | Windows `.cmd`/`.bat` launchers | `pirpc.ShouldUseShell` | pi-command twins | mapped; the simulated-Windows spawn twin is skipped |
| P4 | `pi-sessions.ts`, `session-store.ts`, `paths.ts`, `pi-settings.ts` | session discovery, title and `updatedAt`, map file, settings merge | `pisessions.go`, `store.go`, `paths.go`, `settings.go` | list twins, `store_paths_test.go` | mapped; PiG directories (`~/.pig`, `.pig/`, `PIG_HOME`) |
| L1 | README "Limitations" | no `fs/*` or `terminal/*` delegation; MCP servers accepted but not started; extension slash commands not supported | absent by design | `TestServeSessionFlow` (never calls fs/terminal), `TestE2EMcp...` | approved as upstream; advertised capabilities match |

## Findings about the hosts and about the original

- **F1: compaction event names.** The original handles `auto_compaction_start` and `auto_compaction_end`. Pi 0.84 to 0.99 and
  PiG 0.3.0 emit `compaction_start` (reason `manual`, `threshold` or `overflow`) and `compaction_end`. The original's two notices never
  appear against these hosts. Ported as upstream (twins keep the old names). Fixing it changes observable behavior, so it needs
  owner approval: recommended, map `compaction_start`/`compaction_end` with reason `threshold` or `overflow` to the same two texts.
- **F2: update notice.** See A18. PiG has no npm package; `pig update` uses a signed manifest.
- **F3: `/changelog`.** pi-acp reads the installed Pi package's `CHANGELOG.md`. PiG binaries carry none; the port looks for one beside the binary
  and otherwise says it could not find one.
- **F4: directories.** The original reads `~/.pi/...` and `PI_CODING_AGENT_DIR`. PiG keeps everything in `~/.pig` and never reads `~/.pi`;
  `PIG_USE_PI_DIRS=1` (shared mode) selects Pi's directories. The port follows PiG's rules, and its session map lives under `PIG_HOME`.
- **F5: RPC differences that mattered.** None blocked the port: `get_available_thinking_levels`, `get_session_stats.contextUsage`,
  `set_session_name`, `export_html`, `get_commands`, `agent_settled`, `extension_ui_request` all exist in PiG 0.3.0.

## Architecture decisions

- **Kind of port:** Skill kind 4 (a protocol adapter). `pigeq gaps` says NOT AN EXTENSION for the original. There is no
  scenario/golden equivalence lane: the proof is the twin ledger, the protocol tests, the end-to-end scenarios, the reference client
  with schema validation, and mutations (`port/mutate.mjs`, because `pigeq mutate` replays scenarios).
- **Who starts whom:** the editor starts `pig-acp`, and `pig-acp` starts `pig --mode rpc` per session (as upstream). The Skill's
  kind-4 row suggests the Piglet's extension starts and supervises the companion. That is not possible for ACP: the client, not the
  agent, owns the process, and the protocol runs on the companion's stdio. The extension therefore only reports the
  configuration and the limits (`/acp`); it registers no handler on the session.
- **No dependency:** the companion imports neither PiG nor a third-party module (the JSON-RPC layer replaces the ACP SDK's
  `AgentSideConnection`). It is its own module because PiG rejects a factory and a `package main` in one module.
- **One child per session, one live child per connection**, and the child is the selected pig, so a Piglet Binary is a valid `--pig`.

## Deviations from the original (each observable, each chosen)

| ID | Original | Port | Why |
|---|---|---|---|
| D1 | `RequestError.invalidParams("cwd must be ...")` passes the text as `data`, so clients see only "Invalid params" | the text is in `message` (and `data` is null) | clients show the message |
| D2 | prompt result resolves, then the idle `session_info_update` is emitted | the idle notice is flushed before the prompt result | a client that stops listening at the result has seen the whole turn |
| D3 | promise chain for `session/update` | unbounded FIFO queue and one writer goroutine | never blocks the agent on a slow client |
| D4 | process ends when stdin ends | `Serve` first waits up to 3 s for requests in flight, then disposes the children | a one-shot `echo request \| pig-acp` still gets its answer |
| D5 | `JSON.stringify(x, null, 2)` keeps key order | Go sorts map keys | `/session` fallback and the tool-result JSON fallback only |
| D6 | UTF-16 `slice(0, 80)` for fallback titles | runes | no split surrogate pair |
| D7 | directories `~/.pi/...`, `PI_CODING_AGENT_DIR`, `.pi/` | PiG's (`~/.pig`, `PIG_HOME`, `XDG_CONFIG_HOME/pig`, `PIG_CODING_AGENT_DIR`, `PIG_CODING_AGENT_SESSION_DIR`, `.pig/`, a leading `~` expanded as pig does), Pi's agent, session and project directories with `PIG_USE_PI_DIRS=1`; the session map stays under PiG's config root (`<PIG_HOME>/pig-acp`) in both modes | PiG never reads `~/.pi` and keeps its own state under its config root |
| D8 | agent name `pi-acp`, "Launch pi", "not supported by pi-acp" | `pig-acp`, "Launch pig", "not supported by pig-acp" | identity. File names (`pi-session-<id>.html`) and the words "pi" in `/export` and `/name` messages are unchanged |
| D9 | `PI_ACP_PI_COMMAND`, `PI_ACP_ENABLE_EMBEDDED_CONTEXT` | `PIG_ACP_PIG_COMMAND`, `PIG_ACP_ENABLE_EMBEDDED_CONTEXT`; the `PI_ACP_*` names are still read | migration |
| D10 | no flags beyond `--terminal-login` | `--pig`, `--piglet`, `--pig-arg`, `--version`, `--help`; the advertised terminal auth method repeats them, so a login starts the agent the sessions use | drive a Piglet Binary; explicit entrypoint |
| D11 | `startupInfo` header `pi v<version>` from `pi --version` | `pig v<version>` from `pig --version` (`0.3.0+0.87.1`) | identity |
| D12 | npm update notice, also under `quietStartup` | none (named skip) | see F2 |
| D13 | `/changelog` finds the npm package's file | looks for `CHANGELOG.md` beside the executable (one level up first) | PiG binaries carry none; the answer is then "Changelog not found" |

Named gaps (skipped tests, never silent): the simulated-Windows `.cmd` spawn twin (Go cannot fake `runtime.GOOS`; the shell
predicate is tested with an explicit `goos`, Windows execution only with `GOOS=windows go vet`); the npm update notice; the
compaction event names (F1). Not verified in a real Windows or macOS run; `go vet` passes for both.

## Results (this revision, PiG 0.3.0+0.87.1 `63c6ba456`, go1.27.1)

| Check | Result |
|---|---|
| `pigeq twins check --ledger port/upstream-tests.json --go extensions/acp/cmd/pig-acp` | 124 exact twins, 1 named skip |
| `go test -race -timeout 100s ./...` (companion, all packages) and the extension module | pass; `-race -count=15` on `internal/acp` pass |
| End to end against real `pig` 0.3.0 | 11 of 11 |
| End to end against the built `acp` Piglet Binary (57.6 MB, `pig piglet build ... --format binary`, fused Go extension, 44 s) | 11 of 11 |
| Reference client run (`port/interop/run.mjs`) against `pig-acp` and the Piglet Binary | 12 checks, 30 messages schema-valid, exit 0 |
| Mutations (`port/mutate.mjs`) | 105 mutants (100 from the port, 5 from its review): 104 killed, 1 equivalent (`state-auth-error-ignored`: the same error is caught again by the configuration branch below), 0 survived, 0 invalid |
| `GOOS=windows|darwin|linux go vet ./...` | pass |

The first mutation run left 10 mutants alive; six needed a new test (`internal/acp/mutation_extra_test.go`), two needed a corrected
mutant, and one exposed racy twins (a test read `proc.prompts` before the session's goroutine had made the call; fixed with a wait).

## Under PiG 0.4.0 (Pi 0.99.1)

- The adapter relies on these RPC commands: `prompt`, `abort`, `get_state`, `get_available_models`, `set_model`,
  `get_available_thinking_levels`, `set_thinking_level`, `set_follow_up_mode`, `set_steering_mode`, `compact`,
  `set_auto_compaction`, `get_session_stats` (`contextUsage`), `set_session_name`, `export_html`, `get_messages`, `get_commands`,
  `extension_ui_response`; and these events: `message_update` (`text_delta`, `thinking_delta`, `toolcall_*`),
  `tool_execution_start|update|end`, `extension_ui_request` (`select`, `confirm`, `input`, `editor`, `notify`), `auto_retry_*`,
  `agent_start|end|settled`, `turn_end`. Re-run the end-to-end scenarios against the 0.4.0 `pig` to see a change.
- F1: the compaction events are already `compaction_start|end` in Pi 0.84+; nothing changes with 0.99.1.
- The extension uses only the public SDK: `sdk.New`, `Command`, `Context.Notify`. A 0.4.0 SDK that renames these breaks the
  extension's build, not the companion.
- Installed as a Package (not fused), the extension runs in its own process, so `os.Executable()` is PiG's extension cell runner.
  `/acp` then names the agent from `PIG_HARNESS_BINARY`, the variable PiG 0.3.0 sets for every extension process (its harness
  identity). It is not part of the public SDK; a public accessor for the host executable would remove this dependency.
- A PiG feature that would give a one-command experience: a Piglet Binary mode that serves ACP on stdio (`pig --mode acp`), or
  a Binary that carries a companion executable. Neither exists in 0.3.0.
