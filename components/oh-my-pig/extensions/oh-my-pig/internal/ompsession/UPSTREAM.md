# Upstream reference: `internal/ompsession`

Reads omp's session files as PiG sessions, so the Binary resumes a session omp wrote. Not a port of an omp
feature: omp has no counterpart, and nothing here is written to omp's directories.

Upstream: https://github.com/can1357/oh-my-pi (MIT, tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed

- `packages/coding-agent/src/session/session-paths.ts` (`getDefaultSessionDirName`: where omp keeps a project's sessions)
- `packages/coding-agent/src/session/session-manager.ts` (the entry types of a session file)
- `packages/snapcompact/src/snapcompact.ts` (`archiveSourceText`, `toPlainText`: the text of an archived history)
- `packages/agent/src/compaction/messages.ts` (`convertMessageToLlm`: what omp sends for a compaction summary)

## How a session is found

| Command line | What happens |
|---|---|
| `--resume`, `--continue`, `--session <id>`, `--fork <id>` | The extension's CLI hook (`cli_hook.go`, PiG's `extensions/sdk/clihook`) runs before PiG parses the command line and brings the translations of this project's omp sessions up to date in PiG's session directory. PiG's own picker, newest-session rule and id lookup then find them. |
| `--session <file>`, `--fork <file>` naming an omp session | The file is translated and PiG is given the translation's path. |
| no session flag, `--no-session` | Nothing is read. |
| `/import-omp [latest\|<file>] [--full]` in a session | Switches to the translation (`internal/modes/import_omp.go`). |

A translation is `<omp file name>-omp.jsonl`. It keeps the omp session's id and name, and its file time is the
omp file's, so `--continue` takes whichever of the PiG and omp sessions was written last. `.omp-sessions.json`
beside the translations records the size and time of each source: an unchanged source costs one `stat`.

A translation the user continued in PiG is PiG's session from then on. When omp writes more to the source, the
translation is not replaced; the start that notices says so once.

## What is translated

| omp | PiG |
|---|---|
| line 1 `title` record | dropped |
| `session` header | Pi's members only (`title`, `titleSource` dropped) |
| `model_usage`, `ttsr_injection` | dropped; children take the dropped entry's parent |
| `title_change` | `session_info` (`name`) |
| `model_change` with `model: "provider/id"` | `model_change` with `provider`, `modelId` |
| `message` with role `custom` or `developer` | `custom_message` |
| `compaction` with `method: "snapcompact"` | `summary` is followed by the archive's source text; `preserveData` dropped |
| every other entry | copied byte for byte |

## Not carried over

| omp | Why |
|---|---|
| Snapcompact frames (history drawn as images) | PiG sends a compaction summary as text. The archive's source text holds the same history, so the model reads as text what omp showed it as images; the summary's own note that some sections are images is then out of date. |
| Entries before the first one the latest compaction kept (default) | They are not sent to the model, and a session of hundreds of megabytes exceeds the 128 MiB frame PiG hands an extension its entries in. `/import-omp --full` writes them all. |
| `elideDataUrls` on the archive text | Not ported; data URLs in an archive stay as they are. |
| Eval kernels, browser tabs, background jobs, `agent://` handles | State of the omp process, not of the session file. |
| omp's artifacts directory beside a session (`artifact://` of earlier turns) | Not copied; earlier `artifact://` ids do not resolve. |
| A session directory set only in PiG's `settings.json` | The hook runs before PiG reads its settings; it knows `--session-dir`, `PIG_CODING_AGENT_SESSION_DIR` and the default. `/import-omp` asks the running session and has no such limit. |
| `PIG_USE_PI_DIRS=1` | The hook does nothing: PiG then shares Pi's session directory. |
