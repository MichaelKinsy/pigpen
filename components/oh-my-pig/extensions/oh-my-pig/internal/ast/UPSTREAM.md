# Upstream provenance: `internal/ast`

Pinned to omp release tag `v19.1.1` (`31ffee513f`).

## Upstream files followed
- `packages/coding-agent/src/tools/ast-grep.ts`
- `packages/coding-agent/src/tools/ast-edit.ts`
- `packages/coding-agent/src/tools/resolve.ts` (`PREVIEW_PENDING_NOTICE`, `queueResolveHandler`, `buildResolveReminderMessage`, `resolve`/`reject` staged-preview protocol)
- `packages/coding-agent/src/prompts/tools/ast-grep.md` (rendered with default `eagerDelegation=false`, `scoutAvailable=true`, which omits the conditional `eagerDelegation` bullet)
- `packages/coding-agent/src/prompts/tools/ast-edit.md`
- `packages/coding-agent/src/prompts/system/resolve-reminder.md` (embedded as `prompts/resolve-reminder.md`; injected as a hidden `resolve-reminder` steering message via `ctx.SendMessage` after staging a preview)
- `packages/coding-agent/src/tools/path-utils.ts` (`resolveToolSearchScope`, `toPathList`, `parseSearchPath`, delimited path-list recovery for `;`, `,`, whitespace, and mixed)
- `packages/coding-agent/src/tools/file-recorder.ts` (`formatResultPath`, `resultSnapshotPath`)
- `packages/tui/src/tools/ast-grep.ts` (`AstGrepToolDetails`)
- `packages/tui/src/tools/ast-edit.ts` (`AstEditToolDetails`)
- `packages/tui/src/tools/resolve.ts` (`ResolveDetails`)
- `packages/tui/src/tools/grouped-file-output.ts` (`formatGroupedFiles`, `formatFileMatches`)
- `packages/tui/src/tools/match-line-format.ts` (`formatMatchLine`)
- `packages/tui/src/tools/hashline-format.ts` (`formatHashlineHeader`)
- `packages/tui/src/render/render-utils.ts` (`formatCodeFrameLine`, `capParseErrors`, `formatParseErrors`, `PARSE_ERRORS_LIMIT`)
- `packages/tui/src/utils.ts` (`singleLine`)
- `crates/natives/src/ast.rs` (match/change structs, default limit 50, rule sorting, empty-candidate error)
- `crates/ast/src/search/mod.rs` (candidate discovery, `follow_links: false`, language filtering, overlap validation, atomic staging, bounded `Window`)
- `crates/ast/src/language/mod.rs` (language aliases, Rust `Path::extension` dotfile handling, uppercase `.C`/`.H`, full shell rc list, `parses_as` rules)
- `crates/ast/src/ops.rs` (`resolve_supported_lang` error text, `apply_edits` dedup, UTF-8 boundary check & overlap error)
- `crates/xfs/src/ignore.rs` (`.ignore` > `.gitignore` > `.git/info/exclude` > global excludes, ancestor loading up to nearest `.git`/`.jj`)
- `crates/builtins/src/ast_grep.rs` (`MatchJSON` `--json=stream` schema)
- `packages/utils/src/dirs.ts:148-158` (`standardizeMacOSPath` — omp standardizes `session.cwd` once so `ast_grep` `details.cwd`/`details.searchPath` (`ast-grep.ts:298-299`), `ast_edit` `details.cwd`/`details.searchPath` (`ast-edit.ts:349-350`), and out-of-cwd display paths (`formatPathRelativeToCwd`) carry the short spelling; the port applies `dirs.StandardizeMacOSPath` to reported text and `details` while keeping filesystem and `ast-grep` subprocess arguments raw)

## Load mode at `v19.1.1`
- `ast_grep`: `loadMode = "discoverable"` (disabled by default via `astGrep.enabled = false`). Registered as an SDK tool with `Exposure: sdk.ToolExposureCodemode`, `DefaultActive: false`, and declared via `toolcmd.Mountable("ast_grep", ...)` so `internal/toolcmd` owns activation and executes it through PiG's host (`Context.ExecuteTool`).
- `ast_edit`: `loadMode = "discoverable"`, `deferrable = true` (enabled by default via `astEdit.enabled = true`). Registered as an SDK tool with `Exposure: sdk.ToolExposureCodemode`, `DefaultActive: false`, and declared via `toolcmd.Mountable("ast_edit", ...)` so `internal/toolcmd` mounts it in `bash` when the transport is active or activates it top-level when off.
- `resolve` / `reject`: special tools (`SPECIAL_TOOL_NAMES` in `tools/index.ts:966-971`). Registered with `Exposure: sdk.ToolExposureCodemode`, `DefaultActive: false`, and declared via `toolcmd.RegisterSpecialCommand("resolve"|"reject", astEditEnabled)` (matching omp's static `specialToolApplies` rule at `tools/index.ts:974-981`) so `internal/toolcmd` exposes them in the bash preamble outside `# Tool Commands` while the transport is active, and activates them top-level whenever `ast_edit` is enabled and the transport is off. Also exports `QueueResolveHandler`, `HasPendingPreview`, `ExecuteResolve`, and `ExecuteReject`.

## Engine & `ast-grep` CLI version dependencies
omp embeds `ast-grep-core` + tree-sitter grammars directly in Rust (`crates/ast`). Because cgo and new third-party Go modules are forbidden here, `internal/ast` locates `ast-grep` (then `sg`) on `PATH`, verifies identity and minimum version via `<bin> --version` (`ast-grep <major>.<minor>` >= `0.40`, rejecting unrelated binaries like Linux shadow-utils `/usr/bin/sg` and Windows `.cmd`/`.bat` shims), and invokes it as a child process using:
```
ast-grep run --pattern=<pat> [--rewrite=<out>] --lang=<lang> --json=stream --no-ignore=... -- <files...>
```

and, for files whose extension stock ast-grep's `SgLang::from_path` maps to a different language or no language (e.g. `.h` under `--lang=cpp`, `.ts`/`.js` under `--lang=tsx`, `.js` under `--lang=typescript`, uppercase extensions, dotfiles, or extensionless files):

```
ast-grep run --pattern=<pat> [--rewrite=<out>] --lang=<lang> --json=stream --stdin
```

References in ast-grep upstream (`https://github.com/ast-grep/ast-grep`):
- `crates/cli/src/run.rs` (`RunWithSpecificLang::produce_item`, `--stdin`, clap option definitions)
- `crates/language/src/lib.rs` (`SupportLang`, case-sensitive `from_extension`)
- `crates/cli/src/print/json_print.rs` (`--json=stream` NDJSON `MatchJSON` format)

Behaviours that depend on the installed `ast-grep` CLI version:
1. **Minimum version `>= 0.40.0`**: enforced by `<bin> --version` check; covers `--json=stream` NDJSON output with `replacement`/`replacementOffsets` (0.25.0+) and all 26 languages in `stockAstGrepLanguages` (including YAML added in 0.27.0 and HCL added in 0.39.3).
2. **Supported languages**: stock `ast-grep` CLI supports 26 languages in `stockAstGrepLanguages` (`bash`, `c`, `cpp`, `csharp`, `css`, `elixir`, `go`, `haskell`, `hcl`, `html`, `java`, `javascript`, `json`, `kotlin`, `lua`, `nix`, `php`, `python`, `ruby`, `rust`, `scala`, `solidity`, `swift`, `tsx`, `typescript`, `yaml`). Candidate files of omp-only languages (`toml`, `make`, `dockerfile`, `xml`, `sql`, `protobuf`, `ini`, `vue`, `svelte`, `diff`, `objc`, `starlark`, `r`, `ocaml`, `julia`, `graphql`, `cmake`, `clojure`, `erlang`, `fortran`, `powershell`, `astro`, `emacs-lisp`, `odin`, `verilog`, `tlaplus`, `just`, `regex`, `dart`, `markdown`, `zig`) are skipped before spawning and reported once in omp's missing-grammar style rather than triggering clap errors.
3. **Per-file `--stdin` spawn cost**: ast-grep's `--stdin` flag reads a single source buffer per invocation, so files whose extension disagrees with the requested language (`.h` under `cpp`, `.ts`/`.js` under `tsx`, extensionless files, dotfiles, uppercase extensions) spawn one child process per file rather than batching 200 files per process.
4. **File syntax-error reporting**: stock `ast-grep run --json=stream` does not emit per-file tree-sitter ERROR-node diagnostics on stderr and does not skip rewriting files that contain syntax errors elsewhere in the file. Pattern ERROR-node warnings from stderr (`Warning: Pattern contains an ERROR node...`) are rewritten with the language name in omp's shape (`<preview>: pattern is not valid <lang> on its own (contains an ERROR node)...`), and exit-8 pattern compile failures (`Cannot parse query as a valid pattern.`) become per-language parse issues (`<pat> (<lang>): Invalid pattern: <cause>`) without aborting other languages.
5. **JSON member-fragment auto-wrapping**: stock `ast-grep run` does not wrap `"key": $V` in `{ ... }` with selector `pair`.
6. **Binary absence**: `ast_grep` and `ast_edit` remain registered when no verified `ast-grep` binary is installed (matching omp, which never hides them); invoking either returns `IsError: true` with install instructions.
## Settings omp reads

| Key | Default | Honoured |
|---|---|---|
| `astGrep.enabled` | `false` | Yes — passed via `toolcmd.Mountable("ast_grep", ...)` reading `Enabled(ctx, "astGrep.enabled", false)` |
| `astEdit.enabled` | `true` | Yes — passed via `toolcmd.Mountable("ast_edit", ...)` reading `Enabled(ctx, "astEdit.enabled", true)` |
| `tools.commands` | `true` | Yes — `ast_grep`/`ast_edit` use `Exposure: sdk.ToolExposureCodemode` + `toolcmd.Mountable`; `resolve`/`reject` use `Exposure: sdk.ToolExposureCodemode` + `toolcmd.RegisterSpecialCommand` |
| `PI_MAX_AST_FILES` (env) | `1000` | Yes — caps files touched by `ast_edit` |
| `edit.mode` / `read.lineNumbers` | `hashline` / `false` | Not honoured (see not-ported table) |

## Tests ported
40 Go tests in `ast_test.go` (38 fake/unit tests + 2 real-binary integration tests that run when a verified `ast-grep >= 0.40` is on `PATH` and skip cleanly otherwise), covering:
- `ast-grep.test.ts`: parse-error normalization & 20-entry cap with `parseErrorsTotal`, glob combining with multi-level tree headers (`#`/`##`/`###`), multi-target global ordering + `skip` preserving untruncated `matchCount`, skipping unknown/extensionless files unless `lang` is explicit (verifying `--stdin` routing), explicit `cpp` for `.h` headers and multi-target `lang` forwarding (verifying `--stdin` routing), mixing host files/dirs with `local://` internal URLs, multi-line capture `singleLine` display row vs raw model text, shared session `hashline.ForSession(ctx)` snapshot + seen-line chain into `edit`.
- `ast-edit.test.ts`: schema shape (`ops: [{pat, out}]`, `paths`, no `preview`, strict `additionalProperties: false`), `+/-` hashline prefixes, preview -> `resolve` apply + fresh non-authored post-apply snapshot tags, explicit multi-file targets under their own paths, stale preview failure on `resolve`, glob combining + tree headers, host + `local://` internal URLs.
- `resolve.test.ts`: error when `resolve` has no pending preview (including `propose` hint when `gate.PlanActive` is true), `reject` with no pending preview succeeds cleanly, `resolve` and `reject` through pending preview invoker.
- Engine & safety: `--version` identity & min-version check (rejects Linux shadow-utils `sg` and `< 0.40`), Windows `.cmd`/`.bat` shim rejection, stock-CLI unsupported language skip without bogus parse issues, exit-8 pattern compile failure continuing across mixed languages + language-qualified ERROR-node diagnostic, symlink skipping during walks, parent `.gitignore` + `.git/info/exclude` + global excludes precedence, leading-`-` patterns/rewrites passed as attached `--pattern=`/`--rewrite=`, unknown `lang` returning omp's `Unsupported language` error, killed/crashed child failing instead of returning partial matches, streaming decode of >4 MiB match payloads + bounded `matchWindow`, matched-text & UTF-8 offset verification preventing corrupted writes, comma/whitespace path-list recovery, `gate.Check` blocking writes in plan mode before any file is touched, multi-session `lifecycle.End` isolation + repeated `Register` safety, language inference (`.env` skipped, `.C`/`.H` -> `cpp`, full shell rc list), and real-binary integration tests (`.ts`, `.h`+`cpp`, extensionless+`lang`, leading-`-` pattern, LF+CRLF+non-ASCII rewrites, and `.ts`+`.json`+`.py` mixed-language directory search & rewrite).
## Not ported

| Feature | Upstream | Reason |
|---|---|---|
| Embedded Rust/tree-sitter engine (`omp_ast`), 31 omp-only/WASM languages (including `tlaplus`/PlusCal, `toml`, `make`, `dockerfile`, `xml`, `sql`, `zig`, etc.), and on-demand WASM grammar downloads (`rerunWithGrammars`, `missingGrammarsNote`) | `crates/ast`, `crates/natives/src/ast.rs`, `packages/coding-agent/src/utils/grammars.ts` | cgo and third-party modules are forbidden; delegates to external stock `ast-grep` CLI (`>= 0.40`), which ships 26 built-in languages; omp-only languages are skipped with a missing-language note |
| Per-file tree-sitter ERROR-node detection on silent parses and skipping syntax-broken files during `ast_edit` | `crates/ast/src/search/mod.rs:has_parse_error` | External `ast-grep run --json=stream` does not emit tree-sitter ERROR-node diagnostics per file or skip rewriting files with unrelated syntax errors; stderr diagnostics (including pattern ERROR-node warnings and exit-8 compile failures) are still captured and capped |
| JSON member-fragment auto-wrapping (`"key": $V` -> `{ ... }` `pair` selector) | `crates/ast/src/ops.rs:compile_wrapped_fallback` | Internal to embedded `PatternError::MultipleNode` handling; stock `ast-grep run` has no automatic fallback |
| Tool `<examples>` blocks appended to wire descriptions (5 in `ast_grep`, 6 in `ast_edit`) | `packages/agent/src/agent-loop.ts:1109-1110`, `ast-grep.ts:173-194`, `ast-edit.ts:211-254` | PiG SDK `ToolDefinition` has no `Examples` field and does not inject omp's wire-level `<examples>` formatter |
| Tool approval tiers, `formatApprovalDetails`, and `approvalCacheTargets` | `ast-grep.ts:146-153`, `ast-edit.ts:176-204`, `resolve.ts:343, 358` | PiG SDK has no per-tool approval-tier / approval-cache hook; plan-mode write gating is enforced via `internal/gate` before any file write |
| `SoftToolRequirement` / `ToolChoiceQueue` forced `tool_choice` escalation on non-compliance | `packages/coding-agent/src/tools/resolve.ts`, `session/tool-choice-queue.ts` | PiG SDK has no `SoftToolRequirement` / `tool_choice` forcing API; `PREVIEW_PENDING_NOTICE` is prepended to model text and omp's hidden `resolve-reminder` message is injected via `ctx.SendMessage` (`DeliverAs: "steer"`, `TriggerTurn: false`, `display: false`). |
| Persistence of staged previews across reload, `/resume`, or `/fork` | `packages/coding-agent/src/tools/resolve.ts` (session-scoped in-memory queue) | Staged previews are held in an in-memory map keyed by session ID and cleared on `lifecycle.End`; a new session ID from `/fork` or process restart starts with an empty preview stack |
| Non-file-backed virtual internal URLs (`omp://`, `agent://`, etc.) and readable external web URL materialization in `ast_grep` | `packages/coding-agent/src/internal-urls/url-filesystem.ts`, `tools/fetch.ts` | External `ast-grep` binary operates on local files/stdin; internal URLs resolve via `urls.Locate` (covering file-backed schemes such as `local://`) |
| `edit.mode` / `read.lineNumbers` setting-driven plain-mode switching | `packages/coding-agent/src/utils/file-display-mode.ts` | No shared `edit.mode` reader contract across areas; emits hashline headers/anchors for mutable host files `<= 4 MiB` and falls back to plain lines for oversized/unreadable/immutable files |
| Multi-target `PI_MAX_AST_FILES` per-target accounting and duplicate-target preservation | `packages/coding-agent/src/tools/ast-edit.ts:107-131` | Candidate files are deduplicated across targets and capped globally by `PI_MAX_AST_FILES` so overlapping targets do not double-edit or double-count |
| Locale-collated (`String.prototype.localeCompare`) multi-target path sorting | `packages/coding-agent/src/tools/ast-grep.ts:52` | Uses UTF-8 lexicographical order (`a.Path < b.Path`), avoiding ICU/cgo dependency |
| Selective-commit file-change journaling (`session.recordFileChange`) | `packages/coding-agent/src/tools/ast-edit.ts:464-488` | No selective-commit journal hook on PiG SDK `Context` |
| Dynamic `eagerDelegation` / `scoutAvailable` prompt branching in `ast_grep` description | `packages/coding-agent/src/tools/ast-grep.ts:156-169` | Rendered with omp defaults (`eagerDelegation=false`) per wave 2 brief |
| In-memory `ast_match` N-API export & `ast-grep` shell builtin (`scan`/`outline`) | `crates/natives/src/ast.rs:515`, `crates/builtins/src/ast_grep.rs` | Outside `ast_grep`/`ast_edit` tool surface |
