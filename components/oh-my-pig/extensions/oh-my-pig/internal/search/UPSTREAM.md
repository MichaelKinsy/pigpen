# Upstream provenance: `internal/search`

## Upstream files followed
- `packages/coding-agent/src/tools/grep.ts`
- `packages/coding-agent/src/tools/glob.ts`
- `packages/coding-agent/src/tools/jfind/{index,keywords,lexical,text}.ts`
- `packages/coding-agent/src/prompts/tools/{grep,glob,find}.md` (rendered with `hasFind=true` — keeping the `find` guidance line in `grep.md` and `glob.md` — and `eagerDelegation=false`, dropping the `eagerDelegation` lines)
- `packages/tui/src/tools/{grouped-file-output,match-line-format,list-limit,output-meta}.ts`
- `packages/utils/src/path-tree.ts`
- `crates/pi-edit/src/store.rs` (`file_hash` XXH32 4-hex uppercase snapshot tag)
- `crates/pi-walker/src/lib.rs` (ignore and hidden-file rules)

## Tests ported
13 Go tests in `search_test.go`, covering upstream cases from `grep-snapshot-tags.test.ts`, `grep-pagination.test.ts`, `grep-path-lists.test.ts`, `glob.test.ts`, exact-text limit/pagination/column-truncation notices, plus gitignore/hidden/case/selector/cross-line/RE2-error/find-lexical coverage.

## Not ported

| Feature | Upstream | Reason |
|---|---|---|
| PCRE2 regex fallback (look-around, backreferences) | `crates/pi-natives/src/grep.rs` | Go's stdlib `regexp` is RE2-only (no cgo / no third-party modules); unsupported patterns return an explicit RE2 error |
| Internal URL filesystem (`local://`, `omp://`, `skill://`, `ssh://`, `artifact://`, `agent://`) | `packages/coding-agent/src/internal-urls/` | PiG SDK does not expose omp's internal URL router or VFS |
| Archive member grep (`foo.zip:bar.txt`) | `packages/coding-agent/src/tools/grep.ts` | Not ported in wave 1 |
| Semantic LLM judge cascade in `find` (filename ranking, sketch routing, passage verification) | `packages/coding-agent/src/tools/jfind/{cascade,passages,questions}.ts` | Requires omp's System One judge role; lexical keyword + IDF ranking is ported |
| Shared edit-store provenance recording (`recordFullySeenRows`) | `packages/coding-agent/src/edit/store.ts` | Snapshot tags (`[file#TAG]` / `# file#TAG`) are computed identically via `FileHash` (XXH32); integrator can unify with `internal/files` |
| Streaming `onUpdate` progress callbacks in `glob` / `find` | `packages/coding-agent/src/tools/glob.ts` | Not wired in wave 1 |
| Context lines (`contextBefore` / `contextAfter` settings) | `packages/coding-agent/src/tools/grep.ts` | Default in omp is 0; settings-driven context lines not ported |
