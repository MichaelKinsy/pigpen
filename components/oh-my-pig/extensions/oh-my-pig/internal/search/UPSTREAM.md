# Upstream provenance: `internal/search`

Pinned upstream release: **omp `v19.1.1`** (commit `31ffee513f`).

## Upstream files followed
- `packages/coding-agent/src/tools/grep.ts`
- `packages/coding-agent/src/tools/glob.ts`
- `packages/coding-agent/src/tools/path-utils.ts`
- `packages/coding-agent/src/tools/file-recorder.ts`
- `packages/coding-agent/src/tools/jfind/{index,cascade,keywords,lexical,passages,questions,text,tree}.ts`
- `packages/coding-agent/src/prompts/judge/find.json`
- `packages/ai/src/judgment/{text,chat}.ts` and `packages/ai/src/judgment/text-judge*.md`
- `packages/coding-agent/src/prompts/tools/{grep,glob,find}.md` (embedded default branch: `hasFind=false`, `eagerDelegation=false`; `PrepareLoadout` switches `grep`/`glob` descriptions to `hasFind=true` whenever `find` is declared active in the session loadout)
- `packages/coding-agent/src/internal-urls/{router,url-filesystem}.ts` (adapted onto `internal/urls`)
- `packages/tui/src/tools/{grep,glob,find,grouped-file-output,match-line-format,list-limit,output-meta,read}.ts`
- `packages/tui/src/render/render-utils.ts` (`formatCodeFrameLine`, `toPathList`)
- `packages/utils/src/{format,path-tree,sanitize-text}.ts`
- `crates/xfs/src/{glob.rs,ignore.rs,grep/engine.rs,grep/render.rs,grep/scan.rs,grep/run.rs,walk/mod.rs}`
- `packages/utils/src/dirs.ts:148-158` (`standardizeMacOSPath` — omp standardizes `session.cwd` once at startup so `grep` `details.cwd`/`details.searchPath`/`details.displayTargets` (`grep.ts:1208,1469-1470,1456`), `glob` `details.cwd` (`glob.ts:369,401`), `find` `details.cwd` (`jfind/index.ts:200`), and out-of-cwd result paths (`formatRelToCwd`) carry the short spelling; the port applies `dirs.StandardizeMacOSPath` to reported text and `details` while keeping filesystem paths raw)
- `packages/coding-agent/src/tools/grep.ts:298-374,722-740,795-806,1057-1067,1190-1199` (archive-member grep: `resolveArchiveSearchPaths` via injected `ArchiveMemberReader` backed by `internal/files/archive_member.go`, temporary scratch file materialization with `omp-search-archive-*` prefix so `details.searchPath` and multi-target `details.scopePath` carry per-run temp paths, `archive:member` display remapping, binary/non-UTF-8 skipping, all-unreadable error, and `Skipped archive entries` note)

## `find` availability, judging & lifecycle
- In omp `v19.1.1` (`tools/jfind/index.ts:62-69`, `tools/index.ts:820`, `judgment/index.ts:405-410`), `find` has **no** standalone lexical-only tool mode: under default `find.enabled: "auto"`, `find` is active only when a native judge (a classifier model) is available; its lexical pass serves only as candidate ranking and `lexicalFallback` rows inside an `unavailable` / `partial` result when every judge request fails or times out.
- In Go here:
  - `find` registers with `DefaultActive: false` and raw `find.md` description (preserving omp's trailing newline, `jfind/index.ts:83`).
  - `SetSetting` / `Setting` (`var Setting func(ctx sdk.Context, key string) any`, mutex-guarded via `SetSetting` & `readSetting`) reads `find.enabled` (default `"auto"`) and `find.hitThreshold` (default `0.2`).
  - Under `"auto"` (default), `isFindEnabled` activates `find` only when `ModelRegistry.GetAvailableOfType(ModelTypeClassifier)` returns a classifier model, and executes judgments via `ModelRegistry.Classify` with remaining deadline in `ClassifierOptions.TimeoutMs`.
  - Under `"on"`, `find` may fall back to the session's active chat model via `Context.Complete` with omp's `TextJudge` prompt (block YAML state, raw-quoted questions, `disableReasoning: true`, strict complete-batch parsing, up to 2 retries with `submit_judgment`). It never falls back to an arbitrary un-selected model from `GetAvailable()`.
  - Under `"off"`, `find` stays disabled.
  - `search.Register` calls `toolcmd.RegisterAutoActivate("find", isFindEnabled)` so `toolcmd`'s `session_start` activates `find` inside its `BUILTIN_TOOLS` sort (immediately after `grep`, `tools/index.ts:622-652`); `updateFindToolActivation` also places `find` in `BUILTIN_TOOLS` order and syncs via `toolcmd.Sync(ctx)` when activated later (e.g. on `model_select`). Per-session activation tracking (`autoActivated`, `userRemoved`) lives in a package-level map keyed by session ID (never re-adding `find` once the user removes it), cleaned up via `lifecycle.OnEnd("search", ...)` and `session_shutdown` → `lifecycle.End(sid)`.
## Tests ported
36 Go tests across `search_test.go` (30 tests) and `store_test.go` (6 tests). Upstream coverage breakdown:
- `grep-snapshot-tags.test.ts` (2 of 2 cases: tag re-minting on same-length rewrite + restored-mtime rewrite; plus shared `hashline` store seen-lines test)
- `grep-pagination.test.ts` (4 of 6 cases: 20-file paging, 10-match multi-file cap, 50-match single-file cap, >5 capped files count, 50 KB byte-cap truncation notice + kept-only seen lines)
- `grep-exclude.test.ts` (5 of 8 cases: `!glob` exclusions, cwd-anchored `!dir/sub`, Windows separators + `..cache`, exclusion below named path, literal `!file` path)
- `grep-gitignore-note.test.ts` (2 of 2 cases: gitignored probe note + plain no-match)
- `grep-path-lists.test.ts` (7 of 35 cases: named NUL file + sanitizeText, directory binary-match note, exact brace union `{apps,phases}`, bracketed literal path `apps/[id]/page.tsx`, exact explicit files, directory-prefixed globs staying out of subdirs, single vs multi missing path wording)
- `multi-grep-path.test.ts` (3 of 7 cases: cross-tree `;` scan, explicit `.git/config` alongside `.`, overlapping file/dir dedup)
- `grep-internal-urls.test.ts` (8 of 46 cases: file-backed mutable/immutable URLs, virtual `Enumerator` containers, mixed URL+host `;` lists, virtual long-line windowing, URL line-range selectors, invalid tail selector rejection, `displayTargets`, URL globs)
- `glob.test.ts` (7 of 11 cases: root `/` rejection, over-cap and `< 1` `limit` rejection with omptype wording, undeclared arg rejection + misplaced `pattern`, lenient `PrepareArguments` validation, equal-mtime target-order preservation, limit footer notices, partial results on timeout)
- `glob-directories.test.ts` (3 of 6 cases: absolute/relative/Windows trailing-slash globs, multi-root directory merging, virtual URL directory globs)
- `path-utils-dotdot-selector.test.ts` (3 of 7 cases: `N..M` inclusive range, `N..` open-ended range, non-selector `foo:../bar.ts`)
- `crates/xfs/src/grep/engine.rs` (5 of 8 cases: `sanitize_braces`, valid quantifiers, stray-paren preservation, invalid-regex literal fallback, PCRE2 error)
- `crates/xfs/src/grep/tests.rs` (2 of 31 cases: ASCII & UTF-8 long-line match windowing)
- `crates/xfs/src/ignore/tests.rs` (5 of 24 cases: `.ignore` > `.gitignore` > `.git/info/exclude` > global excludes precedence, ancestor outside repo ignored, `.jj` repo ancestor root-rule filtering, nested repo exclude isolation)
- `jfind.test.ts` (14 of 22 cases: keyword extraction, passage windowing/selection/sketching/heat-merging, tree eligibility + `.pem` secret-ext fix + rendering, 72-file parallel 3-wave cascade + multi-batch wave-3 verification, stalled-judge deadline interruption, strict partial-reply retry, unavailable lexical fallback, complete empty result, threshold filtering, invalid scopes, non-null empty `stats.failures` JSON marshaling, BUILTIN_TOOLS activation order + raw description newline)
- Additional Go tests: omp oracle XXH32 hashes (`5B0D`, `1D54`), UTF-8 non-ASCII globs, glob symlinks, `PrepareArguments`/`PrepareLoadout` schemas & dynamic `hasFind`, native-judge gating + `find.enabled: "on"` opt-in + user-removal respect + multi-session `Register` × 2 & `lifecycle.End` isolation.

## Settings read by omp

| Setting | Default | Honoured |
|---|---|---|
| `grep.enabled` | `true` | Yes via `search.Setting(ctx, "grep.enabled")` (default `true`; deactivates `grep` at `session_start` when `false`) |
| `glob.enabled` | `true` | Yes via `search.Setting(ctx, "glob.enabled")` (default `true`; deactivates `glob` at `session_start` when `false`) |
| `grep.contextBefore` | `0` | Yes via `search.Setting(ctx, "grep.contextBefore")` (explicit tool `context`/`contextBefore` params override; default `0`) |
| `grep.contextAfter` | `0` | Yes via `search.Setting(ctx, "grep.contextAfter")` (explicit tool `context`/`contextAfter` params override; default `0`) |
| `find.enabled` | `"auto"` | Yes, via `search.Setting(ctx, "find.enabled")`: `"auto"` requires native classifier (`ModelTypeClassifier`), `"on"` also allows session chat model via `Context.Complete`, `"off"` disables `find` |
| `find.hitThreshold` | `0.2` | Yes, via `search.Setting(ctx, "find.hitThreshold")` |
| `find.verifyModel` | `undefined` | Not wired (uses same judge for wave 3) |

## Not ported

| Feature | Upstream | Reason |
|---|---|---|
| PCRE2 regex fallback (look-around, backreferences) | `crates/xfs/src/grep/engine.rs:131-134` | Go stdlib `regexp` is RE2-only (no cgo / no third-party modules); look-around/backreferences return an explicit error with a rewrite hint |
| Non-stdlib archive formats in archive-member grep (`7z`, `rar`, `tar.zst`, `tar.bz2`, `tar.xz`, `iso`, `cab`, `asar`, etc.) | `packages/coding-agent/src/tools/grep.ts:326-331` | Go stdlib supports ZIP (`.zip`/`.jar`/`.war`/`.ear`/`.apk`/`.whl`/`.ipa`/`.xpi`/`.vsix`/`.nupkg`/`.cbz`), TAR, and TAR.GZ/TGZ; other formats fail truthfully with `cannot open archive: Reading <format> archives is not supported in this Go standard-library port` |
| External HTTP(S) URL grep (`https://...`) | `packages/coding-agent/src/tools/grep.ts:169-201,705-721` | Requires `fetch` materialization helper with cache/raw-mode handling; fails with an explicit message pointing to `read` |
| Jupyter `.ipynb` cell-projection grep | `packages/coding-agent/src/tools/grep.ts:951-1021` | Requires omp's `notebookToEditableText` projection; raw `.ipynb` matches stay unanchored (no edit tags), matching omp's fallback |
| Comma and whitespace path-list delimiters | `packages/coding-agent/src/tools/path-utils.ts:602-660` | `;` and JSON-array lists are ported; comma/whitespace heuristic splitting is omitted to keep literal-filename safety simple without extra stat probes |
| Parallel directory walking & oversized-last grep scheduling | `crates/xfs/src/walk/parallel.rs`, `crates/xfs/src/grep/run.rs:970-1000` | Content search and `find` lexical scan run in parallel worker pools with reused buffers; directory traversal stays sequential via `os.ReadDir` |
| `find` Spotlight filename ranker & `find.verifyModel` override | `packages/coding-agent/src/tools/jfind/cascade.ts:336-347,543-580` | Spotlight is an omp-specific remote experiment service; wave 1 uses judge filename batches and wave 3 uses the resolved judge |
| `find` native System One judgment cache, session usage ledger journaling, `models.noJudge`/`models.offline` & `nativeJudgeRejection` cooldowns | `packages/coding-agent/src/judgment/{index,cache}.ts` | PiG SDK does not expose omp's `model_usage` ledger, role-chain rejection cache, or `models.noJudge`/`models.offline` policy; per-call usage is still reported in `FindStats` |
| Transient HTTP error retry in chat `TextJudge` | `packages/ai/src/judgment/chat.ts:57-87` | PiG SDK `Context.Complete` wraps host provider transport and does not surface raw HTTP status codes for retry classification |
| Streaming `onUpdate` progress callbacks in `glob` | `packages/coding-agent/src/tools/glob.ts:570-601` | `find` streams phase progress via `ctx.OnUpdate`; `glob` returns final (or partial-timeout) results directly |
| `eagerDelegation` description branch in `grep`/`glob` | `packages/coding-agent/src/tools/{grep,glob}.ts` | `hasFind` switches dynamically via `PrepareLoadout`; `eagerDelegation` stays on omp's default `false` branch because PiG has no `sessionDelegationBias` API |
