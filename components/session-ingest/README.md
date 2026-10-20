# session-ingest

A Go extension for PiG that adds one tool, `read_session`. It lets the model (or
you, by asking it) look inside a Pi or PiG session transcript (`.jsonl`) without
pasting the whole file into the context window. Every mode caps its output at 50 KB /
2000 lines and says how to fetch the next page or narrow the request. `toc`, `tools`
and `stats` summarize; `turn` hides thinking and tool results unless `include` asks
for them; `slice` and `query` show message text as recorded, tool results included
(`maxCharsPerItem` shortens each item).

```text
read_session(path, mode = toc | query | slice | turn | tools | stats, ...)
```

| Mode | Answers | Output |
|---|---|---|
| `toc` (default) | What happened, turn by turn? | One row per user turn: time, request, tools used, errors, cost. |
| `query` | Where was X discussed? | Ranked hits (`query`, `regex`, `caseSensitive`), each with turn, role, tool, excerpt and the call that fetches it. |
| `slice` | Show messages N to M. | A page of normalized messages, optionally filtered by `roles`. |
| `turn` | Show turn N in full. | The user message, assistant messages and tool activity of one turn. `include` picks `text`, `thinking`, `tool_calls`, `tool_results`, `errors`, `usage` (default: text, tool_calls, errors). |
| `tools` | Which tools ran, on which files? | Calls, results and errors per tool, and the files touched by `read`, `write` and `edit` (`turn` restricts to one turn). |
| `stats` | How big was it? | Turns, messages by role, tool calls, errors, tokens, cost, models, duration. |

Common parameters: `start` and `limit` (paging; not for `stats`), `maxCharsPerItem` (truncate long
text, 0 = do not), `format` (`compact_markdown` by default, or `json`).

A parameter the chosen mode does not use (for example `query` with `mode=toc`, or `limit`
with `mode=stats`) is ignored, as in PiG's own tools. The schema is one flat object for
every mode and a call often carries schema defaults, so refusing them would fail calls
that are otherwise right. Each parameter's description names the modes that use it.

## What is read

`read_session` reads the **active branch** of the session tree. Session files from Pi and
PiG (v2 and later) give every entry an `id` and a `parentId`; the active branch is the
path from the last entry in the file back to the root, the branch Pi continues from. After a
fork (`/tree`, `/fork`) the abandoned branches are not part of the transcript and cannot turn
up in a query; every report says how many entries it left out (`off_branch_entries`, JSON
`session.offBranchEntries`). An entry whose parent is not in the file starts a new root, as in
Pi's `getBranch`. Entries without a `parentId` (v1 sessions) follow file order.

On the branch the transcript keeps, in order:

| Entry | Becomes |
|---|---|
| `message` | the message, with its role (`user`, `assistant`, `toolResult`, `bashExecution` as `$ command` and its output, `custom`, ...) |
| `compaction` | a `compactionSummary` message: the summary, `tokensBefore`, and the cost of the call that wrote it. Messages before the compaction stay readable; the toc marks the turn with `compaction:N` |
| `branch_summary` | a `branchSummary` message |
| `custom_message` | a `custom` message with its `customType` |
| `model_change`, `thinking_level_change` | the events listed in the toc |
| `session_info` | the session name (the latest on the branch) |
| `usage` | counted in `stats` tokens and cost; `stats` shows the part that belongs to no message |

Summaries are transcript items, so `messages` counts them and `roles` lists them. They do not
start a turn. `custom` (private extension state), `label` and `context_edit` entries are not
shown: they never become a message in the transcript.

## Install

```sh
pig install ./components/session-ingest
```

The extension builds from source on first use (Go toolchain required) or fuses into a
Piglet Binary. It needs no network and no Node runtime. The tool can read any file
path it is given, like PiG's own `read` tool, and returns text only.
`pig package validate ./components/session-ingest` validates the Package.

## Test

The tests use PiG's public SDK through a fake host. From the Pigpen root:

```sh
PIG_BIN=/path/to/pig npm run test:go-ports -- -race
```

MIT. © Michael Kinsy. See [CREDITS.md](CREDITS.md).
