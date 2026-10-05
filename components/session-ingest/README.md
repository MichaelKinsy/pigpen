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

Common parameters: `start` and `limit` (paging), `maxCharsPerItem` (truncate long
text, 0 = do not), `format` (`compact_markdown` by default, or `json`).

Not covered: the tool reads every `message` entry in file order. It does not follow
the session tree, so a forked session shows both branches in sequence; it ignores
compaction and other non-message entries.

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
