# Parity with omp

oh-my-pig follows omp at commit `4df653426f40ee1eb78ef408528eb9e6a5275b9d` (`main`, 2026-11-23, omp 19.1.1).
This file lists omp's surface at that commit and the state of each part here. It is the only claim of parity
this Piglet makes. A row says **same** only where the two were run on the same input and compared.

How things were compared:

- **Tools**: omp's own tool and the Go port were given the same files and commands, and the output text compared
  character for character. Edited files were compared byte for byte.
- **Drawing**: omp and oh-my-pig were run in two headless Terns on one scripted conversation, and the frames each
  sent to Tern compared by node kind, role and nesting ([`proof/`](proof/README.md)). Pixels, layout, pickers and
  overlays are not compared.
- **tests** means the port has its own Go tests and was not compared with omp.

## Summary

| Area | omp has | Here | Notes |
|---|---|---|---|
| Tools on by default | 13 | 10 | `eval`, `task`, `wait` are absent; `find` is keyword ranking only |
| All model-facing tools | 49 | 10 | the other 36 are off by default in omp or belong to a mode |
| Internal URL schemes | 19 | 0 | `skill://`, `artifact://`, `xd://` and the rest |
| System prompt | 1 | 1, with sections left out | sections about features absent here are omitted |
| Drawn parts compared in Tern | 11 | 6 the same | of the 5 that differ, 3 need a change in PiG, 1 needs omp's shell parser, 1 is on purpose |
| Slash commands | 90 | 1 of omp's own, 17 of the same name from PiG | PiG's are Pi's commands, which omp also descends from; not compared |
| Modes (plan, goal, loop, vibe, advisor, …) | 17 | 0 | |
| Shipped agents | 6 | 0 | needs `task` |
| Settings | 556 | 0 of omp's | PiG's settings apply |
| Key bindings | 73 | PiG's | not compared |
| CLI subcommands and launch flags | 52 and 62 | PiG's | not compared |
| Model providers and logins | 88 | PiG's | not compared |
| Extension points (hooks, custom tools, MCP, plugins) | 12 | PiG's | a user's PiG extensions and skills load; omp's do not |

## Tools

The descriptions and parameter schemas the model sees are omp's, copied. Each package's `UPSTREAM.md` under
[`components/oh-my-pig/extensions/oh-my-pig/internal/`](../../components/oh-my-pig/extensions/oh-my-pig/internal)
has the full list of what it leaves out.

| Tool | State | Compared with omp | Main things left out |
|---|---|---|---|
| `read` | ported | **same** on text files (whole file, ranges, comma lists, raw, tail, CRLF, no trailing newline, directories, tags) and on URLs (an HTML page whole, a ranged text read, a raw range) | PDF, Office, SQLite, archives, notebooks, media; internal URLs; the declarations-only summary and enclosing `class`/`def` lines, which omp takes from tree-sitter |
| `write` | ported | tests | LSP formatting and diagnostics; archive and SQLite writes |
| `edit` | ported (hashline) | **same**: files byte-identical after the same edits on two fixtures; response text, stale-tag refusal, in-session recovery, block ops, the refusal of lines never displayed | block ops resolve by brackets and indentation, not tree-sitter; the other edit dialects (`replace`, `patch`, `apply_patch`); LSP diagnostics; auto-repair |
| `grep` | ported | **same** on a fixture, tags included | PCRE-only patterns (look-around, backreferences) are refused; archive members; internal URLs |
| `glob` | ported | **same** on a fixture, limit notice included | internal URLs |
| `find` | partial | tests | omp ranks with a model; here keywords only |
| `bash` | ported | **same** on 12 commands: exit codes, empty output, cwd reset per call, variables, functions and aliases kept between calls, quoting, heredocs | runs the system `bash`, not omp's in-process shell and its built-in commands; no PTY mode; omp's approval patterns |
| `todo` | ported | tests | `/todo edit`, `/todo copy`; the native checklist in Tern; the forced first call |
| `ask` | ported | tests | omp's tabbed dialog with previews and notes: PiG's select and input dialogs are used |
| `web_search` | ported | tests, no live search; its page reader is the one `read` uses for URLs | providers that search through a chat model; scraping engines; site-specific readers |
| `eval`, `task`, `wait` | absent | | on by default in omp |
| `lsp`, `ast_edit`, `debug` | absent | | on by default in omp through `xd://` |
| `ast_grep`, `github`, `checkpoint`, `rewind`, `ida`, `security_scan`, `context_notes`, `new_context` | absent | | off by default in omp |
| `memory_edit`, `retain`, `recall`, `reflect`, `learn`, `manage_skill` | absent | | need omp's memory backends |
| `yield`, `goal`, `think`, `tts`, `generate_image`, `vibe_*` (5), `advise`, the autoresearch tools (4) | absent | | hidden or mode tools |
| `xd://resolve`, `xd://reject`, `xd://propose`, `xd://report_issue` | absent | | |

## System prompt

The model receives omp's prompt structure (Role, Runtime, Tool Policy, Workflow, Delivery, Critical, project
context) with omp's text, omp's date and working-directory reminder before the first user message, and the
context files and skills PiG discovers. Checked by reading the request a built Binary sends.

- Changed on purpose: the role line names oh-my-pig, not omp.
- The tool inventory and every tool-gated section name only the tools registered here.
- Left out: the sections on internal URLs, `xd://` devices, visuals (Mermaid, SVG, charts), reactions, intent
  tracing, secrets, subagent hand-off and eval preludes.

## Drawing in Tern

Last run of [`proof/run.sh`](proof/README.md) (oh-my-pig built on PiG's 0.8.0 release head, Tern 0.9.27):
**6 of 11 parts the same, 5 differ, 0 missing.**

| Part | State | Difference |
|---|---|---|
| Thinking block | **same** | |
| Tool card: `read` | **same** | |
| Tool card: `edit` (with its diff) | **same** | the head shows the path as the model wrote it; omp shows the absolute path and the first changed line |
| Tool card: a failing call | **same** | |
| Composer (context meter, editor, bar) | **same** | |
| Composer bar (model and effort chip, extras, usage, send) | **same** | |
| Tool card: `bash` | differs | the command is a `code` block; omp sends its `shell` view |
| Working row | differs | no Stop control; a `spinner` and `shimmer` where omp sends text nodes |
| User message | differs | no tools row (copy, rewind, time) |
| Assistant text | differs | no per-turn usage row |
| Surface root | differs, on purpose | `pig.session`, not `omp.session` |

What cannot match from inside a Piglet, because PiG does not give a frontend the data:

- **Stop control** on the working row and the composer: PiG's frontend interface has no interrupt call.
- **Per-turn usage row** and the copy button under an assistant message.
- **User message tools**: timestamp, copy and rewind buttons, badges.
- **Live token rate**, the retry countdown ring, context marks for compaction.
- **Todo checklist, agent pills, queue band, welcome card** as native nodes.
- **The shell view** of a bash command: omp compiles it with its own shell parser.

On purpose: oh-my-pig tells Tern it is `pig` with a `pig.session` surface, not `omp`.

## Not here at all

Subagents and the agent hub; plan, goal, loop, vibe and advisor modes; rewind and checkpoints; collaboration;
voice; the browser and computer tools; memory; omp's settings, key bindings, themes and status line
configuration; omp's extensions, hooks, plugins and marketplace; `/fork` into a Tern split and browser tabs in
Tern; session files in omp's format.
