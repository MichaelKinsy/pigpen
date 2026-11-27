# Upstream Reference: `internal/commit`

Port of omp's selective-commit eval namespace (`commit`) and change journal (https://github.com/can1357/oh-my-pi, MIT, release tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed (`v19.1.1`)

- `packages/coding-agent/src/commit/selective/attribution.ts` → `attribution.go` (Myers O(ND) diff matching `crates/diff/src/lib.rs` jsdiff v9 tie-breaking, `HistorySteps`, `Attribute`, `BuildContent`, `Classify`, `cancelReinsertions`, `reconcileNetHunks`)
- `packages/coding-agent/src/commit/selective/journal.ts` → `journal.go` (`ChangeJournal`, `changes.jsonl`, gzipped `blobs/`, `LineDelta`, inline threshold 4096, snapshot cap 8 MiB, `MAX_DELTA_EDITS=1024`, tip LRU cache 32 MiB)
- `packages/coding-agent/src/commit/selective/patch.ts` + `packages/coding-agent/src/utils/git-diff-paths.ts` → `patch.go` (`FormatTextPatch`, `EncodeGitDiffPath`)
- `packages/coding-agent/src/commit/selective/preview.ts` → `preview.go` (`FormatPatchPreview`, `wordRow`, `WIDE_LINE=240`, `WORD_CONTEXT=40`, `MAX_WORD_WORK=2^24`, UTF-16 code-unit length and surrogate-safe `cutKept`)
- `packages/coding-agent/src/commit/selective/draft.ts` + `packages/coding-agent/src/autoresearch/git.ts` → `draft.go`, `git.go` (`CreateDraft`, `Draft`, `Selection`, `Take`, `Drop`, `Only`, `Show`, `Diff`, `Summary`, whole-file frozen blobs, gitlink handling, `worktreeToGit` clean/autocrlf conversion, glob/suffix matching)
- `packages/coding-agent/src/commit/selective/apply.ts` + `crates/vcs/src/git/{read,diff,mutate,patch,filter}.rs` → `apply.go`, `git.go` (`CommitSelection`, `ComposeMessage`, `LogSubjects`, private index with sparse-checkout preservation, expected-HEAD CAS retries, hook execution, shared-index sync and lock retry, `amend` own-commit guard)
- `packages/coding-agent/src/commit/selective/prelude-definition.ts`, `prelude.js`, `prelude.py` → `prelude.go`, `assets/prelude.js`, `assets/prelude.py` (`DraftFor`, `Register`, `PreludeDefinition`, `MAX_DRAFTS=8`, `describeCall`, strict argument validation)
- `packages/coding-agent/src/prompts/tools/commit.md`, `commit-guidance.md` → `assets/commit.md`, `assets/commit-guidance.md` (embedded verbatim)
- `packages/coding-agent/src/sdk.ts:2648-2656, 2830-2843`, `tools/write.ts:1080-1089`, `edit/index.ts:791-801`, `tools/ast-edit.ts:465-488`, `tools/conflict-uri.ts:157,417` → `RecordFileChange`, wired into `internal/files` (`write`, `edit`), `internal/ast` (`ast_edit` resolve apply), and `internal/urls` (`conflict://` write hook), excluding paths under the session's artifacts directory.

## Settings

| Key | Default | Honoured | Notes |
|---|---|---|---|
| `commit.enabled` | `true` | Yes (via `Setting`) | Gates the `commit` eval prelude, `<namespaces>` listing, `help://eval/commit`, and system-prompt guidance block. |
| `tools.outputMaxColumns` | `768` | Yes (via `Setting`) | Byte column cap passed to `FormatPatchPreview` for `diff()` display (0 disables). |

## Not Ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| Embedded-shell file-change recording (`bash` builtins / redirections and eval `sh()` `shellChangeSnapshots`) | `tools/bash.ts:1070`, `eval/shell-bridge.ts:112`, `commit/selective/journal.ts:131` | oh-my-pig executes `bash` and eval `sh()` via the host system shell rather than omp's in-process Rust `pi-shell` VM, so builtins (`sed -i`, `cp`, `mv`, `rm`, `tee`) and shell redirections (`>`, `>>`) cannot report before/after snapshots to the journal. Writes made through `bash` or `sh()` therefore appear as unrecorded changes (under `Unrecorded:` or `left out: unrecorded`, or `Review:` when rewriting in-scope lines), matching how upstream treats writes by spawned external programs. |
| Isolated-subagent patch merge recording (`task` isolation runner) | `task/isolation-runner.ts:866-886` | Worktree-isolated subagent execution (`task.isolation`) is not ported in `internal/agents`. `RecordFileChangeForAgent` is provided if isolation is added later. |
| In-process `gix` / `pi-natives` VCS backend | `crates/vcs/src/git/*.rs` | Go standard library only (no cgo / Rust crate). All git reads, `worktreeToGit` filtering (via temporary `GIT_OBJECT_DIRECTORY`), private-index patching, hooks, and `update-ref` CAS run through the `git` executable. |
| `commit.js` / `commit.py` hash algorithm compatibility with Bun.hash | `commit/selective/journal.ts:146,374` | Uses Go's `crypto/sha256` (truncated to uint64 base-36) instead of `Bun.hash` (wyhash) for blob keys and `sum` checksums. Journal format (`changes.jsonl`, gzipped `blobs/`, record schema) is identical; a journal created by Go replays cleanly in Go (cross-process replay against a Bun-written journal would treat mismatched `sum` as unreplayable). |
| Agentic/conventional commit generators (`omp commit` CLI subcommand) | `commit/agentic/*`, `commit/conventional/*` | Out of scope for the selective-commit eval prelude and `/commit` slash command (now ported in `internal/modes`). |
| `Intl.Segmenter` ICU word segmentation for `FormatPatchPreview` | `commit/selective/preview.ts:26` | Uses a pure-Go Unicode word/punctuation/whitespace/CJK segmenter matching `Intl.Segmenter("en", { granularity: "word" })` across ASCII, Latin, punctuation, CJK, and emoji without external ICU dependencies. |
