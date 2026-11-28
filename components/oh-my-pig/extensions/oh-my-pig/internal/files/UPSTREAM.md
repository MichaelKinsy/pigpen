# Upstream Provenance: `internal/files`

Ported from omp (`https://github.com/can1357/oh-my-pi`, MIT, tag `v19.1.1`, commit `31ffee513f`).
(`internal/hashline` and `internal/files/edit.go` are owned by the dedicated `OmpEdit` lane.)

## Upstream Files Followed
- `packages/coding-agent/src/tools/read.ts`
- `packages/coding-agent/src/tools/read-selector.ts`
- `packages/coding-agent/src/tools/read-format.ts`
- `packages/coding-agent/src/tools/read-archive.ts`
- `packages/coding-agent/src/tools/read-json.ts`
- `packages/coding-agent/src/tools/read-path-resolution.ts`
- `packages/coding-agent/src/tools/write.ts`
- `packages/coding-agent/src/tools/write-content.ts`
- `packages/coding-agent/src/tools/auto-generated-guard.ts`
- `packages/coding-agent/src/tools/path-utils.ts`
- `packages/coding-agent/src/workspace-tree.ts`
- `packages/utils/src/format.ts` (`formatAge`)
- `packages/coding-agent/src/tools/conflict-detect.ts` (`scanFileForConflicts`, `formatConflictWarning`, `formatConflictSummary`, `formatScanCapNote`)
- `packages/coding-agent/src/utils/file-display-mode.ts`
- `packages/coding-agent/src/utils/markit.ts` (document conversion dispatch for `.docx`, `.pptx`, `.xlsx`, `.epub`, `.pdf`)
- `packages/tui/src/tools/line-ranges.ts`
- `packages/tui/src/tools/streaming-output.ts`
- `packages/tui/src/tools/output-meta.ts`
- `packages/utils/src/ar/limits.ts`, `reader.ts`, `open.ts`, `registry.ts`, `paths.ts`, `write.ts`, `zip.ts`, `tar.ts`
- `crates/edit/src/path_policy.rs`
- `crates/edit/src/modes/hashline/prefixes.rs`
- `packages/coding-agent/src/prompts/tools/read.md` (rendered with `IS_HL_MODE=true`, unported source promises removed — see below)
- `packages/coding-agent/src/prompts/tools/write.md` (rendered for stdlib-writable archives without SQLite write promise — see below)

## Sentences Removed from `read.md` and `write.md` (Unported Sources)
To avoid promising unsupported selectors/targets to the model:
- Removed from `read.md`:
  - `Bare code: declarations only; re-read ONLY footer-named omissions, NEVER guess .. / ….`
  - `Also, with selectors in help://read/sources ... SQLite, archives (..., 7z, RAR and more), JSON/JSONL by jq filter, executable metadata, video, SVG as image, merge conflicts.`
  - Changed `skips folding and the display-width limit` to `skips the display-width limit` (folding is not ported; the 768-char column cap is ported).
  - Kept accurate descriptions for: line selectors, block open/close context, `:raw`, selected files, directories, URLs, PDF/DOCX/PPTX/XLSX/EPUB extracted text (`internal/markit`), `.ipynb` notebooks (cell text), raster images, and stdlib archives (`ZIP, JAR, APK, WHL, TAR, TAR.GZ, TGZ`).
- Narrowed in `write.md`:
  - Replaced `Archive members and SQLite rows are writable too: help://read/sources.` with `Archive members (`archive.ext:member`) are writable too for ZIP, TAR, and TAR.GZ/TGZ archives.` (SQLite row writes and non-stdlib `.tar.zst`/`.asar` archive writes are not ported).

## Result `details`
- `read` populates `resolvedPath` wherever omp does (directory, binary, out-of-range, unbounded, `:conflicts`, located/resource URLs, archive, and notebook reads — project-dir paths and project-derived virtual URLs (`conflict://`, `rule://`, `skill://`) standardized via `dirs.StandardizeMacOSPath` on macOS matching `dirs.ts:148-158`, `conflict-protocol.ts:45`, `rule-protocol.ts:64`, `skill-protocol.ts:111`, while located `local://`/`agent://` temp-dir realpaths keep their long `/private/...` spelling as in `local-protocol.ts:356,407` and `read.ts:2266,2292,3583`), `displayTarget` (from `urls.LocateSync` on linkable internal URLs such as `local://`, matching `read.ts:2082-2083`), `fileSize` (on text and image reads; omitted on binary, out-of-range, and unbounded reads — note `fileSize` is also set on located URL, out-of-range, and binary reads where omp omits it only if an older binary or caller passes them), `totalLines` (on single-window text/in-memory reads; omitted on multi-range and out-of-range reads), `rowPrefixes`, `raw` (when `:raw`), `isDirectory` (for directories and archive directory listings), `contentType` (`text/markdown` on located Markdown URL reads), `displayContent` (`{text, startLine, lineNumbers}`, including `:raw` reads using omp's `countTextLines`), `conflictCount` (when unresolved git merge-conflict blocks appear in displayed lines of a mutable file), `meta.truncation` (`totalBytes` = selected window bytes, `maxBytes` = read byte budget on all truncated non-partial reads matching `read.ts:3322-3331`, `read-format.ts:540-549`, `output-meta.ts:188`), `meta.limits.columnTruncated`, and `meta.source` (`{type, value}`, omitted on plain out-of-range reads as in omp), plus `ReadUrlToolDetails` (`kind`, `url`, `finalUrl`, `contentType`, `method`, `truncated`, `notes`, `meta`) for HTTP(S) URLs. `meta.pagedSource` is omitted (only used by omp's spill-skip path).
- `details.mediaPath` on whole-file Tern-viewable reads is attached in one place by `internal/tern`'s `tool_result` hook (`internal/tern/register.go:205`, `internal/tern/media.go`, standardized via `dirs.StandardizeMacOSPath` on macOS, matching `read.ts:2611-2615` + `packages/utils/src/dirs.ts:148-158`), so `internal/files` does not duplicate it.
- `write` populates `patch` and `diff` (unified patch via `sdk.GenerateUnifiedPatch` for PiG's frontend `toolResultDiff` and `internal/ansi`; extra relative to omp (omp emits neither diff nor patch on write)), `resolvedPath` (project-dir paths and `conflict://` writes standardized via `dirs.StandardizeMacOSPath` on macOS matching `write.ts:1028,1157`, `conflict-uri.ts:144,182`, and `dirs.ts:148-158`; located `local://` temp-dir paths keep their located spelling as in `local-protocol.ts:418`), `madeExecutable` (when shebang `chmod +x` flipped execute bits), `diagnostics` / `meta.diagnostics` (when `SetDiagnosticsHook` returns diagnostics), and the `write` `details.touchesNoFiles` flag (`touchesNoFiles: true`) on file-free handler writes (`out://`, `agent://`, `proc://<id>/kill`, etc., matching `write.ts:951-966`).
- `edit` populates `diff`, `patch`, `perFileResults[].patch` (needed by PiG's frontend `interactive_frontend.go` Tern diff card; extra relative to omp), `path`, `move`, `sourcePath`, and `perFileResults[].path` (all standardized via `dirs.StandardizeMacOSPath` on macOS, matching `edit/index.ts:259-267`, `crates/natives/src/edit.rs:746,749`, `crates/edit/src/session.rs:515`, and `dirs.ts:148-158`, along with cross-directory `Moved to <abs>` text in `internal/hashline/patcher.go`), and on failure returns `IsError: true` with `details: {diff: "", errorKind}` and a nil Go error so PiG keeps the details intact.

## Archive Rewrite Semantics (Matches omp `v19.1.1`)
Writing a member into an archive (`archive.ext:member`) rewrites the whole archive atomically via a sibling temporary file created with mode `0666` (umask-masked, matching `Bun.write` in `write.ts:719-729`; an existing archive's custom mode such as `0600` is replaced by the umask-masked mode on rename). Matching omp's `readArchiveEntries` / `encodeZip` / `encodeTar` (`packages/utils/src/ar/{open,zip,tar}.ts`), every rewritten archive is normalized:
- Every file member's mode becomes `0644` and timestamp becomes fixed (`1980-01-01 00:00:00 UTC` for ZIP, Unix epoch `0` for TAR/TAR.GZ); synthesized parent directories in TAR get `0755` and epoch `0`.
- Explicit directory records and empty directories are not preserved (only parent directories of retained files are synthesized for TAR; ZIP omits directory entries).
- File aliases (TAR hard links and TAR/ZIP symlinks targeting regular files) are flattened into regular files holding the target's bytes; TAR directory symlinks are dropped like directories; ZIP directory symlinks, dangling/escaping symlinks, TAR hard links to missing/unreadable/directory/cyclic targets, and sparse TAR members are refused with omp's exact error (leaving the original archive untouched).
- Non-regular special TAR records (FIFO, char/block devices) and unrepresentable member paths (`..` / empty) are dropped during indexing, matching omp.

## Delimited Multi-Path Reads & Unique Workspace-Suffix Resolution
- Delimited multi-path reads (`internal/files/read_multipath.go`, `internal/files/read.go`):
  - `splitDelimitedPathEntry` (`path-utils.ts:756-836`), `splitMixedUrlPathList` (`path-utils.ts:847-869`), `splitQueryPathList` / `splitTopLevelSemicolons` (`read.ts:924-963`), `#tryReadDelimitedPaths` / `#readDelimitedParts` (`read.ts:1488-1567`), `readDetailsLinkPath` (`read.ts:1056-1060`).
  - Supports `;` (unconditional fan-out, including all-missing lists, internal URL lists, mixed URL+local lists, and `?q=` query lists), `,` (when at least one part resolves), whitespace and mixed delimiters (when every part resolves), while preserving literal files whose names contain delimiters, `{...}` brace groups, and backslash-escaped delimiters.
  - Emits `Note: interpreted as N paths: …`, `[Could not read <part>: <message>]`, `details.notes`, `details.displayReadTargets` (flattening nested lists and using `suffixResolution.to` when resolved), and `details.displayReadTargetLinks`.
- Unique workspace-suffix resolution (`internal/files/read_multipath.go`, `internal/search/suffix.go`, `internal/files/read.go`, `internal/files/sources.go`, `internal/files/read_json.go`, `internal/files/conflicts.go`):
  - `findUniqueWorkspaceSuffix` (`path-utils.ts:1413-1458`), `findSuffixMatchCached` / `isRemoteMountPath` / `isNotFoundError` (`read-path-resolution.ts:7-36`), `prependSuffixResolutionNotice` / `markRawSuffixResolution` (`read-format.ts:56-58,719-727`), plus callers for files, directories, `:conflicts`, `:line:col` diagnosis (`read.ts:2459-2582`), stdlib archives (`read-archive.ts:171-215`), and JSON `?q=` queries (`read-json.ts:407-480`).
  - Delegates to `internal/search.FindSuffixMatches` (shared `walkDir` + `compileGlob` with `pattern: "**/" + escapeGlobMetachars(normalized)`, `hidden=true`, `gitignore=true`, `includeDir=true`, `includeSymlinks=true`, `.git`/`node_modules` pruning unless named in the pattern, 5s timeout, per-call memoization, and early exit on 2 matches).
  - Sets `details.suffixResolution` (`{ from, to, raw? }`) and prepends `[Path '<from>' not found; resolved to '<to>' via suffix match]` on non-raw non-directory reads; `:raw` keeps byte output clean and records `raw: true` in `details.suffixResolution`.
  - Suffix resolution also works for markit-converted documents (`.docx`, `.pptx`, `.xlsx`, `.epub`, `.pdf`); suffix resolution for unported target types (SQLite, video, non-stdlib archives) remains unported alongside those target types.
## Tests Ported (`internal/files`)
- `internal/files/files_test.go`: 6 tests (read selector forms including bare `:N`, read-then-edit + stale/invented tag rejection, CRLF & no-trailing-newline, write + parent dirs + `MV` + `REM` + outside-cwd, directory listing & raster image read, tool registration).
- `internal/files/v19_guards_test.go`: 9 tests porting upstream cases:
  - `write-line-endings.test.ts`: CRLF preservation + note, CRLF+BOM preservation + note, verbatim write when input contains CR.
  - `write-shebang-chmod.test.ts`: `#!` chmod + notice + `madeExecutable` detail, plain file no-op, Rust `#![` inner attribute no-op.
  - `write-read-projection.test.ts`: rejects shorter `Showing lines`, byte-budget, entity-labelled, unscanned, ASCII elision, and column-cap read projections.
  - `write-hashline-header.test.ts`: strips evidenced copied `[file#TAG]` + numbered prefixes with note; leaves unevidenced numbered text intact.
  - `tools/auto-generated-guard.test.ts`: blocks overwriting files with generated comment markers or generated filenames (`*.pb.go`).
  - `write-read-selector-misfire.test.ts`: refuses missing selector-suffixed empty write, semicolon-joined selector list, URI-like near-miss; preserves `@scope/pkg.txt`.
  - `read-column-truncation-snapshot.test.ts` + `read.ts`: 300-line `read.defaultLimit` paging, 768-char column cap keeping untruncated snapshot & marking cut lines unseen, `Path '...' not found` + `:line:col` diagnosis, non-ASCII archive path (`Ⱥ.zip`).
  - `gate.Check` refusal & URL identity, multi-session `Register` & `lifecycle.End` isolation.
  - `tools/read-directory-range.test.ts` + `tools/path-literal-colon-selector.test.ts`: directory range slices (`:30-40`, `:-5`), literal colon filename (`test:1-2`).
- `internal/files/pass2_fixes_test.go`: 7 tests (`net.Pipe` session-bearing `read`→`edit` shared `hashline.ForSession` store & wire JSON schemas, `read-raw-range.test.ts` open-ended `:raw` truncation + closed raw range terminator + oversized raw line, in-memory archive 1000-line read without 300-line cap, `withMutationQueue` panic propagation, >4 MiB streaming `[path#TAG]` hash + `:-N` tail + bounded huge line + bounded write, wire `details.conflictCount` for 2-way/diff3/windowed/clean reads, empty-file `[path#TAG]\n1:` read + `PUT 1.=1:` edit).
- `internal/files/urls_sources_test.go`: 2 tests (internal URL routing via fake `urls.Handler` for `read`/`write`/`edit`, 0-based `.ipynb` notebook projection, single-level ZIP/tar.gz directory listing + member reads, mixed tail `:1-2,-2`, `SetDiagnosticsHook`).
- `internal/files/url_seen_test.go` & `internal/files/oracle_test.go`: 4 tests (external URL reading with `:N-` continuation hints and `ReadUrlToolDetails`, seen-line enforcement, details patch, oracle parity checks).
- `internal/files/round3_fixes_test.go`: 5 tests (one-letter relative path vs Windows drive split `d:1-5`/`a:2-3`/`C:\...`, `formatAge` boundary table test, URL-backed header path + no resource notes across `local://`/`history://`/`agent://`, archive missing-member error text, `read` conflict registration + warning footer + `:conflicts` summary + `conflict://` read/write splice).
- `internal/files/round4_fixes_test.go`: 11 tests (`conflict://N` excerpt backing-file tag + edit follow-up, `out://` and read-only URL tool-policy denial prefix, located `skill://` `[Skill file: ...]` provenance + `contentType: text/markdown`, edit failure details preserved over wire, `:raw` `displayContent` `countTextLines` across all four oracle cases + non-raw context + comma-list `:raw`, `meta.limits.columnTruncated` + selected-window `meta.truncation.totalBytes`, raw HTML URL `contentType: text/html`, explicit `?q=` jq unsupported error, archive-member & notebook `meta.source`, `resolvedPath` on dir/binary/out-of-range & omission of port-only `totalLines`/`fileSize`/`meta`, macOS `/private` standardization on `skill://` and headers).
- `internal/files/archive_write_test.go`: 12 tests (ZIP & aliases, TAR/TAR.GZ/TGZ, creating missing archives & literal archive filenames, refusals for read-only/non-stdlib/invalid subpaths/dir & plain files, atomicity on failure, write guards, concurrent serialization, `conflict://` prefix recovery, TAR link flattening & special typeflags, TAR broken-link & sparse refusals, ZIP symlinks & unsafe names, rewritten archive permissions matching umask).
- `internal/files/read_multipath_test.go`: 3 tests (with subtests) porting `glob-validate-paths.test.ts`, `grep-path-lists.test.ts`, `read-mixed-path-list.test.ts`, and `read-raw-range.test.ts` (semicolon/comma/whitespace/mixed/nested delimited lists, literal delimiter precedence, `?q=` query lists, and unique suffix resolution for files, `:raw`, directories, archives, JSON queries, ambiguity, and `.gitignore`).
- `internal/files/read_markit_test.go`: 1 test (DOCX/PPTX/XLSX/EPUB reads via `internal/markit`, hashline `[path#TAG]` header & numbered rows, `:N-M` range, `:raw:1-3`, unique workspace-suffix resolution, ZIP-container member read `report.docx:word/document.xml`, and corrupt ZIP refusal with `fileSize` details).
## Settings Read by Upstream
| Key | Default | Honoured |
|---|---|---|
| `read.defaultLimit` | `300` | Yes via `files.Setting(ctx, "read.defaultLimit")` (clamped to `[1, DefaultMaxLines]`, default `300`) |
| `read.lineNumbers` | `false` | Hashline mode implies line numbers (`N:`) |
| `read.renderMarkdown` | `false` | No (plain text only) |
| `read.summarize.enabled` | `true` | No (requires tree-sitter AST; see Not Ported) |
| `read.summarize.prose` | `false` | No |
| `tools.outputMaxColumns` | `768` | Yes via `files.Setting(ctx, "tools.outputMaxColumns")` (default `768`, `0` disables, skipped in `:raw`) |
| `tools.artifactSpillThreshold` | `50` (KB) | Yes via `files.Setting(ctx, "tools.artifactSpillThreshold")` (KB → `* 1024` bytes, default `50 KB`) |
| `edit.blockAutoGenerated` | `true` | Yes via `files.Setting(ctx, "edit.blockAutoGenerated")` in `edit` and `write` (default `true`) |
| `edit.enforceSeenLines` | `true` | Yes via `files.Setting(ctx, "edit.enforceSeenLines")` in `edit` (default `true`) |
| `lsp.diagnosticsOnWrite` | `true` | Via `SetDiagnosticsHook` (wired by integrator) |
| `lsp.formatOnWrite` | `false` | No (see Not Ported) |

## Not Ported

| Feature | Reason |
|---|---|
| `read` SQLite (`file.db`, `:table`, `?q=SELECT`) | Requires a SQLite driver; forbidden by no-third-party-modules / no-cgo rule |
| `read` PDF visual page screenshots (`doc.pdf:page<N>.png`), native `pdf-inspector` OCR detection, & markit conversion of archive members (`archive.zip:doc.pdf`) | Text extraction for local DOCX, PPTX, XLSX, EPUB (pure-Go stdlib) and PDF (via `pdftotext` CLI when on PATH) plus ZIP-container member reads (`doc.docx:word/document.xml`) are ported; Chromium PDF screenshots, Rust `pdf-inspector`, archive-nested markit conversion, and URL/fetch markit conversion remain unported (see `internal/markit/UPSTREAM.md`) |
| `read` 7z, RAR, ISO, CAB, DEB, RPM, CPIO, AR, LZH, ARJ, ASAR, compressed streams | Formats not in the Go standard library (ZIP, JAR, APK, WHL, TAR, TAR.GZ/TGZ are supported) |
| `read` video frames / contact sheets (`ffmpeg`/`ffprobe`) | Requires external `ffmpeg`/`ffprobe` binaries |
| `read` SVG `:img` rasterization and HEIC/BMP transcoding / image auto-resize / `?q=` vision questions | Requires native image codecs/SVG rasterizer and a vision sub-call not available in Go stdlib |
| `read` executable binary views (ELF/PE/Mach-O, Ghidra/dsm) | Requires binary analysis backend (`dsm`) |
| `read` AST structural summary (`details.summary`, bare-code declaration-only elision) | Requires tree-sitter (`crates/ast`); pure-Go reads full lines up to `read.defaultLimit` (300), so `details.summary` is never emitted |
| `read` repeat-read loop hint & speculative read caching | Session-level optimization requiring conversation state |
| `read` multi-range streaming on files > 4 MiB | Streaming reader supports single-range, bare, `:raw`, `:-N` tail, and streaming `[path#TAG]` up to 64 MiB; multi-range comma lists require buffered reads |
| `write` SQLite row writes (`db.sqlite:table[:key]`) | Requires a SQLite driver; forbidden by no-third-party-modules / no-cgo rule |
| `write` non-stdlib archive formats (`.tar.zst`/`.tzst`, `.asar`) | omp also writes `tar.zst` (zstd codec) and `asar` (Electron ASAR format), which require codecs/encoders outside the Go standard library; this port writes stdlib formats (`zip` family `.zip`/`.jar`/`.war`/`.ear`/`.apk`/`.whl`/`.ipa`/`.xpi`/`.vsix`/`.nupkg`/`.cbz`, `tar`, and `tar.gz`/`.tgz`) and refuses `tar.zst`/`asar` with an explicit error (read-only formats like `7z`/`rar`/`iso`/`cab`/`tar.bz2`/`tar.xz`/etc. are refused with omp's exact read-only error) |
| `write` LSP formatting (`lsp.formatOnWrite`) | LSP area provides diagnostics hook only; format-on-write edits are not wired |
| `write` held-open SQLite file guard (`assertNotHeldOpen`) | Requires process-wide SQLite handle tracking not present since SQLite is not linked |
