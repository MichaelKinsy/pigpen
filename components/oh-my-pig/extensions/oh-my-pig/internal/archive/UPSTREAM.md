# Upstream Reference: `internal/archive`

Port of omp's read-only local history eval prelude (`archive` / `help://eval/archive`) (https://github.com/can1357/oh-my-pi, MIT, release tag `v19.1.1`).

## Upstream files followed (`v19.1.1`)

- `packages/coding-agent/src/prompts/tools/archive.md` → `assets/archive.md` (embedded verbatim; supplies the `<namespaces>` summary line `Read-only local history: recent projects, past sessions with recaps, full-text session search, prompt history.`)
- `packages/coding-agent/src/archive/prelude.js` → `assets/prelude.js` (embedded verbatim)
- `packages/coding-agent/src/archive/prelude.py` → `assets/prelude.py` (embedded verbatim)
- `packages/coding-agent/src/archive/prelude-definition.ts` → `prelude.go` (`Register`, `Prelude`, `Setting`, `parseArchiveParams` strict validation with unknown-key rejection, `resolveProject`, `describeArchiveCall`, non-nil `Details` bridge invariant)
- `packages/coding-agent/src/archive/archive.ts` → `archive.go` (`ArchiveProjects`, `ArchiveSessions`, `ArchiveSessionDetailByID`, `ArchiveSearch`, `ArchivePrompts`, `ArchiveRecaps`, record types with explicit `null` serialization, display formatting, prefix resolution error messages)
- `packages/coding-agent/src/session/search-query.ts` → `search_query.go` (`WordQuery`, `ParseWordQuery`, `Test`, `Locate`, Latin accent folding, prefix-phrase word matching)
- `packages/coding-agent/src/session/session-listing.ts` & `packages/coding-agent/src/session/transcript-index.ts` → `scanner.go` (top-level session discovery across `<agentDir>/sessions` and `~/.omp/agent/sessions`, stat-keyed scan cache, `isEmptySession`, `sessionDisplayName`, `SessionStatus` derivation, `SNIPPET_CONTEXT=100` snippets, bounded line reading, symlink escape filtering)
- `packages/coding-agent/test/archive-prelude.test.ts` → `archive_test.go`

## Settings

| Key | Default | Honoured | Notes |
|---|---|---|---|
| `archive.enabled` | `true` | Yes (via `Setting`) | Gates the `archive` eval prelude, `<namespaces>` listing, and `help://eval/archive`. |

## Stdlib-only Adaptations (No SQLite / No cgo)

Upstream omp maintains two SQLite databases via `bun:sqlite`:
- `history.db` (interactive editor prompt history, `session_titles` index, `session_recaps` idle-recap journal)
- `transcripts.db` (FTS5 background-daemon index over user/assistant transcript messages)

Because `oh-my-pig` is pure Go stdlib with zero third-party modules and zero cgo:
1. **Session & transcript scanning (`scanner.go`)**: Scans top-level `<root>/<bucket>/*.jsonl` files directly across both PiG's `<agentDir>/sessions` and `~/.omp/agent/sessions` (deduplicated by canonical realpath and session ID), using stat-keyed metadata caching, bounded line reads (8 MiB/line cap, 128 MiB/file cap), and scan caps (`maxSearchFiles=2000`, `maxHistoryScanFiles=2000`).
2. **Word search (`search_query.go`)**: Implements upstream's exact `WordQuery` phrase-prefix semantics (case- and Latin-diacritic-insensitive word-prefix matching, punctuation-as-separator within adjacent phrase tokens, `SNIPPET_CONTEXT=100`, `SEARCH_MESSAGE_MATCHES=5`, `SEARCH_MATCH_LINES=3`) in pure Go over titles, recaps, and user/assistant messages (excluding thinking, tool calls, and tool outputs).
3. **Prompts & Recaps**: Extracts user prompts from session JSONL files (plus optional `<agentDir>/history.jsonl` when present, including prompts without a session) and extracts recaps from `compaction` (`shortSummary`), `recap`/`idle_recap`, and recap `custom`/`custom_message` JSONL entries (plus optional `<agentDir>/recaps.jsonl` when present).
4. **No daemon catch-up notice**: Because transcript search scans JSONL files synchronously with `ctx` cancellation, searches never lag behind a background indexer and do not append the "Transcript index is still catching up" warning.
