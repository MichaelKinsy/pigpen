# Upstream Provenance: `internal/files` and `internal/hashline`

Ported from omp (`https://github.com/can1357/oh-my-pi`, MIT, commit `4df653426f40`).

## Upstream Files Followed
- `packages/coding-agent/src/tools/read.ts`
- `packages/coding-agent/src/tools/read-selector.ts`
- `packages/coding-agent/src/tools/read-format.ts`
- `packages/coding-agent/src/tools/write.ts`
- `packages/coding-agent/src/tools/path-utils.ts`
- `packages/coding-agent/src/workspace-tree.ts`
- `packages/coding-agent/src/edit/index.ts`
- `packages/coding-agent/src/edit/schemas.ts`
- `packages/tui/src/tools/line-ranges.ts`
- `packages/coding-agent/src/prompts/tools/read.md` (rendered with `IS_HL_MODE=true`, `BINARY_VIEWS=false`)
- `packages/coding-agent/src/prompts/tools/write.md`
- `crates/pi-edit/prompts/hashline.md`
- `crates/pi-edit/src/store.rs`
- `crates/pi-edit/src/text.rs`
- `crates/pi-edit/src/diff_string.rs`
- `crates/pi-edit/src/session.rs`
- `crates/pi-edit/src/modes/hashline/{format,input,tokenizer,parser,block,clipboard,apply,mismatch,types}.rs`

## Tests Ported
- `internal/hashline/hashline_test.go`: 6 tests (file hash normalization, line endings & BOM, all hashline ops parsing, range/gap/cut/paste application, brace/python/markdown block resolution, stale/unrecognized tag mismatch messages).
- `internal/files/files_test.go`: 6 tests (all read selector forms + context lines + `:raw`, read-then-edit + stale/invented tag rejection, CRLF & no-trailing-newline preservation, write + parent dirs + `MV` + `REM` + outside-cwd path handling, directory listing & raster image read, registration smoke test).

## Not Ported (Wave 1)

| Feature | Reason |
|---|---|
| `read` PDF, Office documents (markit), SQLite, archives, Jupyter notebooks, JSON `?q=` jq filter, video frames/contact sheets, SVG `:img` rasterization, `:conflicts`, profiler summaries, executable binary views | Out of scope for wave 1 |
| `read` URL site-specific scrapers, external reader backends (`trafilatura`/`lynx`/Jina/Firecrawl), artifact persistence | See `internal/web/UPSTREAM.md` for what `web.Fetch` omits |
| `read`/`write`/`edit` internal URL schemes (`skill://`, `artifact://`, `local://`, `xd://`, `agent://`, `proc://`, `ssh://`, `mcp://`, etc.) | Out of scope for wave 1 |
| `read` AST structural summary (bare-code declaration-only elision) | Requires tree-sitter (`pi-ast`); pure-Go wave 1 reads full lines |
| `read` repeat-read loop hint & speculative read caching | Session-level optimization deferred |
| `write` LSP formatting, LSP diagnostics, auto-generated file guard, chmod `+x` for shebangs, hashline prefix auto-stripping, truncated-read-projection guard, archive/SQLite writes | Out of scope for wave 1 |
| `edit` LSP diagnostics, auto-repair, fuzzy recovery, streaming previews, non-hashline edit modes (`replace`, `patch`, `apply_patch`, `sloppy`), `ast_edit` | Out of scope for wave 1 |
| `hashline` tree-sitter block resolution (`PUT N*:`, `PUT >N*:`, `CUT N*`) | Replaced with pure-Go heuristic supporting Markdown headings, brace/paren/bracket blocks, and indentation-scoped blocks |

## Sentences Removed from `read.md` (Unported in Wave 1)
- `:conflicts lists one line per unresolved merge block. SVG/SVGZ default text; :img rasterizes an SVG/SVGZ into an image, :raw original. ... :img rejects them. Video requires ffmpeg/ffprobe: bare preview grid+metadata, :412 frame, :1h5m42s/:90s/:01:23 time.`
- `- Bare code: declarations only; re-read ONLY footer-named omissions, NEVER guess `..`/`…`.`
- `- SQLite: file.db tables; :table schema/rows; :table:key by primary key; ?limit=, ?where=, ?q=SELECT.`
- `- Archives: ZIP/JAR/APK/WHL, compressed TAR, RAR/7z/ISO/CAB/DEB/RPM/CPIO/AR/LZH/ARJ/ASAR, compressed streams; member via archive.ext:member/path.`
- `- JSON/JSONL/NDJSON: file.json?q=<jq filter>; &raw=true unquoted strings, &compact=true one-line values, &offset=&limit= page results; `&` inside the filter → %26.`
- `- PDF/documents: extracted text; notebooks: editable cells;`

## Result `details` and Multi-File `patch`
- `edit` and `write` populate `details["patch"]` (for PiG's frontend `toolResultDiff`) and `details["diff"]` via `sdk.GenerateUnifiedPatch`, alongside omp's `EditToolDetails` / `WriteToolDetails` fields (`resolvedPath`, `path`, `op`, `firstChangedLine`, `oldText`, `newText`, `move`, `sourcePath`, `perFileResults`).
- For a multi-section `edit` touching several files, each section's unified patch is stored in `perFileResults[i].diff` and concatenated in section order (each patch already ends with `\n`) into the single `details["patch"]` / `details["diff"]` string.
