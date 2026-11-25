# `internal/ansi` Native View-ID Contract (`VIEW-IDS.md`)

This document specifies the exact `sdk/kit` native view trees (`CallView` and `ResultView`) emitted by `internal/ansi`'s native builders (`native_*.go`) when `surface.Native() == true`, pinned to **omp `v19.1.1`** (`31ffee513f27a01bba7d4dfbe12bae239cdc646a`).

The Tern frontend (`piglets/pig-tern/frontend/omp_views.go`) maps `frontend.ToolCard.ResultView` (and `CallView`) from these trees directly to Tern TSP nodes.

## General Rules

1. **Two surfaces, zero terminal chrome in native mode.** When `surface.Native() == true`, `register.go` dispatches to the native builders (`native_*.go`). Native trees contain **no** terminal box borders (`╭╮╰╯│─`), no `$ ` shell prompt chrome, no terminal gutter padding, no ANSI escape sequences, and no `omp.tool.head` / `omp.tool.frame.*` nodes. (When `!surface.Native()`, terminal builders `term_*.go` run instead and emit no `omp.*` role IDs.)
2. **IDs equal omp's native roles.** Every semantic node carries its omp role as `kit` node `ID`.
3. **Uniqueness.** PiG's host view validator (`coding/extension/host/subprocess/view_kit.go:617-623`) rejects duplicate non-empty IDs within one view. Repeating roles use `<role>#<index>` (0-based); nested repeating roles use `<role>#<parent>.<child>`.
4. **Raw payloads in node fields only.** All payload lives in the kit node's own fields (`Text`, `Lines`, `Lines.List`, `Lines.Progress`, `Markdown.Text`, `Diff.Diff` / `Diff.FilePath`, `Image.Data()` / `Image.MimeType` / `Image.Filename`). Never encode payload into `ID`. All text is sanitized per `packages/utils/src/sanitize-text.ts` (ANSI escapes, C0 except `\t`/`\n`, DEL, and C1 U+0080–U+009F stripped); code, diff, and run output payloads preserve literal `\t` tabs while prose/labels expand tabs to 3 spaces.
5. **Dedicated head children on container roles.** Containers carry no text of their own. Every section/fold/file container carries a dedicated head child (`<role>.head`, `omp.ask.fold.head`, `omp.tool.file`, `omp.tool.source.head`, `omp.tool.diagnostics.head`, `omp.job.tile.head`, `omp.task.agent.row`).
6. **Node cap.** Every view stays strictly below PiG's 4096-node host limit (`nvMaxNodes = 3600`, `nvMaxLinesPerNode = 2000`).
7. **Tool root unwrapping.** Each call view roots at `omp.tool.<name>.call` (`*kit.Container`) and each result view roots at `omp.tool.<name>` (`*kit.Container`). `omp_views.go`'s `isOmpToolRootWrapper` unwraps these roots directly under the `tool` node so child selectors match omp's tree.

---

## Per-Tool Native Trees (`v19.1.1`)

### `read` (file, image, URL, `proc://`, `cfg://`)

Upstream: `packages/tui/src/tools/read.ts:790-873`, `fetch.ts:153-218`, `proc-render.ts:453-526`, `cfg-render.ts:84-104`, `native-view.ts:115-202`.

- **File read CallView (`omp.tool.read.call`, `*kit.Container`)** — empty body (`read.ts:796`: inline head only).
- **File read ResultView (`omp.tool.read`, `*kit.Container`)**
  - On error (`read.ts:823-826`):
    - `omp.tool.error` (`*kit.Text` or `*kit.Lines` if multiline JSON): full sanitized error message with leading `Error:` stripped.
  - On text/code (`read.ts:862-870`):
    - `omp.tool.read.markdown` (`*kit.Markdown`, when markdown and not `:raw`): raw markdown content (`details.displayContent.text` or stripped result text).
    - OR `omp.tool.read.code` (`*kit.Lines`): raw unprefixed source lines (`details.displayContent.text` or stripped result text).
    - `omp.tool.stats` or `omp.tool.notice` (`*kit.Text`, optional, `read.ts:833-840`): `resolved <path>`, `First line exceeds …`, and/or truncation/artifact notice (`omp.tool.notice` when truncation or artifactError is present, else `omp.tool.stats`).
  - On image (`read.ts:846-859`):
    - `omp.tool.read.image` (`*kit.Image`): decoded image bytes, `MimeType`, `Filename` (shortened path or `suffixResolution.to` as alt).
    - `omp.tool.output` (`*kit.Lines`, optional): non-empty detail text.
    - `omp.tool.stats` / `omp.tool.notice` (`*kit.Text`, optional).
- **URL read / `fetch` CallView (`omp.tool.fetch.call`, `*kit.Container`)** — empty body (`fetch.ts:153-156`).
- **URL read / `fetch` ResultView (`omp.tool.fetch`, `*kit.Container`, `fetch.ts:158-218`)**
  - On error or nil details: `omp.tool.error` (`*kit.Text` / `*kit.Lines`).
  - On success:
    - `omp.tool.fetch.content` (`*kit.Lines` when non-empty, up to 12 non-empty lines; or `*kit.Text` `"(no content)"`).
    - `omp.tool.stats` (`*kit.Text`): `<contentType> · <method> · <N lines> · <M chars> [· … K more lines] [· notes]`.
    - `omp.tool.notice` (`*kit.Text`, optional when `truncated`): `Output truncated [· Read local://<id>.log for full output]`.
- **`proc://` read CallView (`omp.tool.proc.call`, `*kit.Container`)** — empty body (`proc-render.ts:468`).
- **`proc://` read ResultView (`omp.tool.proc`, `*kit.Container`, `proc-render.ts:453-526`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - Single job (`details.proc.job`):
    - `omp.tool.proc.table` (`*kit.Lines` with 1:1 `List.Items`): `[type] id`, detail = intent/label, column = duration/status.
    - `omp.tool.proc.log` (`*kit.Lines`, optional): job log / error / result text.
  - Single service (`details.proc.daemon`):
    - `omp.tool.proc.log` (`*kit.Lines`): `terminalRows` or `log`.
  - Streams / evicted preview (`id != ""` without table lists):
    - `omp.tool.proc.preview` (`*kit.Lines`, optional): raw preview lines.
  - Root table (`proc://`):
    - `omp.tool.proc.table` (`*kit.Lines` with 1:1 `List.Items`): jobs, services (`service <name>`, meta), and agents (`agent <id>`, age).
- **`cfg://` read CallView (`omp.tool.cfg.call`, `*kit.Container`)** — empty body (`cfg-render.ts:97`).
- **`cfg://` read ResultView (`omp.tool.cfg`, `*kit.Container`, `cfg-render.ts:84-104`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - On success: `omp.tool.cfg.listing` (`*kit.Lines`): raw YAML settings listing.

### `write` (file, figure, `agent://`, `proc://`, `cfg://`)

Upstream: `packages/tui/src/tools/write.ts:117-154, 751-807`, `proc-render.ts:360-450`, `cfg-render.ts:107-143`.

- **File write CallView (`omp.tool.write.call`, `*kit.Container`)**
  - `omp.tool.write.figure` (`*kit.Markdown`, optional, `write.ts:142-146`): fenced `svg`/`mermaid`/`obj`/`ply`/`wrl`/`vrml`/`x3dv`/`stl`/`gltf`/`usda`/`usd` block (mermaid omitted until `argsComplete`).
  - `omp.tool.write.source` (`*kit.Lines`, when `args.content` non-empty, `write.ts:152-154`): raw source lines.
- **File write ResultView (`omp.tool.write`, `*kit.Container`)**
  - On error: `omp.tool.error` (`*kit.Text` / `*kit.Lines`).
  - On success / partial:
    - `omp.tool.stats` (`*kit.Text`, when partial and progress text present).
    - `omp.tool.write.figure` (`*kit.Markdown`, optional): closed fenced figure block.
    - `omp.tool.write.source` (`*kit.Lines`, when content non-empty): raw source lines.
    - `omp.tool.diagnostics` (`*kit.VStack`, `Gap: 0`, optional, `native-view.ts:353-390`):
      - `omp.tool.diagnostics.head` (`*kit.Text`): `"Diagnostics"`
      - `omp.tool.diagnostic#<index>` (`*kit.Text`, up to 5 sorted rows): `[severity] [file:]line:col message [source]` or unparsed line
      - `omp.tool.stats` (`*kit.Text`, optional): `+<N> more`
- **`agent://` write (`omp.tool.irc.call` / `omp.tool.irc`, `*kit.Container`, `proc-render.ts:360-405`)**
  - On error with 0 receipts: `omp.tool.error` (`*kit.Text`).
  - `omp.tool.proc.preview` (`*kit.Lines`, optional): raw message body.
  - `omp.tool.notice` (`*kit.Text`, optional on settled empty broadcast): result text or `"No live peers to broadcast to."`.
  - `omp.tool.irc.receipts` (`*kit.Lines` with 1:1 `List.Items`, when `len(receipts) > 1` or any failed): label = `to`, detail = `error`, column = `outcome`.
- **`proc://` write (`omp.tool.proc.call` / `omp.tool.proc`, `*kit.Container`, `proc-render.ts:408-450`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - `omp.tool.proc.preview` (`*kit.Lines`, when `action == "stdin"` and content non-empty).
  - `omp.tool.proc.cancelled` (`*kit.Lines` with 1:1 `List.Items`, when `details.proc.op == "cancel"`): cancelled job items + outcome status column.
- **`cfg://` write (`omp.tool.cfg.call` / `omp.tool.cfg`, `*kit.Container`, `cfg-render.ts:107-143`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - `omp.tool.cfg.change` (`*kit.Text`): `<previous> → <value>` (or `→ <value>` while pending).
  - `omp.tool.notice` (`*kit.Text`, optional when `details.cfg.effective` is set): `still <effective>: a higher-precedence layer overrides the saved value`.

### `edit` / `apply_patch`

Upstream: `packages/tui/src/tools/edit.ts:271-281, 1263-1383, 1465-1533`, `native-view.ts:50-60, 353-390`.

- **CallView (`omp.tool.edit.call`, `*kit.Container`)**
  - Single-section hashline `input`: `omp.tool.edit.change` (`*kit.Markdown`): raw change description/operations.
  - Multi-section hashline `input`: per file `omp.tool.edit.file#<index>` (`*kit.VStack`, `Gap: 0`):
    - `omp.tool.edit.file.head#<index>` (`*kit.Text`): `<displayPath>`
    - `omp.tool.edit.change#<index>` (`*kit.Markdown`, optional): raw section body
  - Preview diff (`args.previewDiff`, or `args.diff` with `args.op`): `omp.tool.edit.diff` (`*kit.Diff`, `Diff` + `FilePath`).
  - Non-diff replacement text / raw patch (`args.diff` without `op`, `args.newText`, `args.new_string`, `args.patch`): `omp.tool.write.source` (`*kit.Lines`, raw code lines preserving tabs).
- **ResultView (`omp.tool.edit`, `*kit.Container`)**
  - Single file (`edit.ts:1377-1382`):
    - `omp.tool.error` (`*kit.Text` / `*kit.Lines`, on error)
    - OR `omp.tool.edit.diff` (`*kit.Diff`, `Diff` = unified diff text, `FilePath` = path)
    - OR `omp.tool.notice` (`*kit.Text`, on no-op: `"No changes were made to <path>."`)
    - `omp.tool.diagnostics` (`*kit.VStack`, optional, see `write`).
  - Multi-file (`len(details.perFileResults) > 1`, `edit.ts:1334-1376`):
    - Per file `omp.tool.edit.file#<index>` (`*kit.VStack`, `Gap: 0`):
      - `omp.tool.edit.file.head#<index>` (`*kit.Text`): `<path> [→ <rename>] [+A −R]`
      - `omp.tool.edit.diff#<index>` (`*kit.Diff`), OR `omp.tool.error#<index>` (`*kit.Text`), OR `omp.tool.notice#<index>` (`*kit.Text`)
      - `omp.tool.diagnostics#<index>` (`*kit.VStack`, optional: head `omp.tool.diagnostics.head#<index>`, rows `omp.tool.diagnostic#<index>.<d>`, optional overflow `omp.tool.stats#<index>`)

### `bash`

Upstream: `packages/tui/src/tools/bash.ts:338-373, 487-557, 934-1077`, `tool-commands.ts:223-246`, `native-view.ts:212-303`.

- **CallView (`omp.tool.bash.call`, `*kit.Container`)**
  - `omp.run` (`*kit.VStack`, `Gap: 0`):
    - `omp.tool.bash.command` (`*kit.Lines`): raw unstyled command (`[cd <dir> && ] [ENV="..."] <command>`, no `$` prompt chrome).
    - `omp.run.foot` (`*kit.HStack`, `Gap: 1`, when `rc.ExecutionStarted`):
      - `omp.run.state` (`*kit.Text`): `"Running"`
      - `omp.run.time` (`*kit.Text`, optional): formatted duration
- **ResultView (`omp.tool.bash`, `*kit.Container`)**
  - `omp.run` (`*kit.VStack`, `Gap: 0`):
    - `omp.tool.bash.command` (`*kit.Lines`): raw command lines.
    - `omp.tool.bash.command-tool#<index>` (`*kit.VStack`, `Gap: 1`, optional per dispatched nested tool command in `details.toolCommands`):
      - `omp.tool.bash.command-tool.head#<index>` (`*kit.Text`): `<tool> [exit N]`
      - `omp.tool.output#<index>` (`*kit.Lines`, optional): raw output lines
    - `omp.tool.bash.step#<index>` (`*kit.HStack`, `Gap: 1`, optional per step in `details.steps`):
      - `omp.tool.bash.step.label#<index>` (`*kit.Text`): step label
      - `omp.tool.bash.step.note#<index>` (`*kit.Text`, optional): step note
    - `omp.tool.bash.output` (`*kit.Lines`, when stripped output is non-empty and not `(no output)`): raw sanitized output lines (notices stripped in omp order: background → output → exit → wall time → artifact).
    - OR `omp.tool.bash.nooutput` (`*kit.Text`, when traced steps exist and output is empty): `"Nothing written"` / `"Nothing written yet"`.
    - `omp.run.foot` (`*kit.HStack`, `Gap: 1`):
      - `omp.run.state` (`*kit.Text`): `"Done"` | `"Failed"` | `"exit N"` | `"Timed out"` | `"In background"` | `"Running"` | `"Cancelled"`
      - `omp.run.time` (`*kit.Text`, optional): formatted wall/elapsed time (e.g. `"202ms"`, `"1.2s"` — parsed into Tern `elapsed` by `omp_views.go`)
      - `omp.run.facts` (`*kit.Text`, optional): `Job … · Service … · Artifact … · Minimized … · <truncation notice>`

### `eval`

Upstream: `packages/tui/src/tools/eval.ts:723-934, 1281-1381`, `native-view.ts:241-303`.

- **CallView (`omp.tool.eval.call`, `*kit.Container`)**
  - One `omp.run` (single cell) or `omp.run#<index>` (multi-cell) (`*kit.VStack`, `Gap: 0`) per cell:
    - `omp.run.caption#<index>` (`*kit.Text`, multi-cell only): `<i>/<N> [title]`
    - `omp.tool.eval.input` / `omp.tool.eval.input#<index>` (`*kit.Lines`): raw cell source code
    - `omp.run.foot` / `omp.run.foot#0` (`*kit.HStack`, `Gap: 1`, first cell when `rc.ExecutionStarted`)
- **ResultView (`omp.tool.eval`, `*kit.Container`)**
  - Per cell in `details.cells` (or fallback from `rc.Args` + `res.Content`):
    - `omp.run` / `omp.run#<index>` (`*kit.VStack`, `Gap: 0`):
      - `omp.run.caption#<index>` (`*kit.Text`, multi-cell only): `<i>/<N> [title]`
      - `omp.tool.eval.input` / `omp.tool.eval.input#<index>` (`*kit.Lines`): raw cell source
      - Output segments (split around `cell.views` in order):
        - `omp.tool.eval.output` / `omp.tool.eval.output#<c>.<v>` (`*kit.Lines`): raw text or pretty JSON output
        - `omp.tool.eval.table#<c>.<v>` (`*kit.Lines` with 1:1 `List.Items`): header + table rows
        - `omp.tool.eval.markdown#<c>.<v>` (`*kit.Markdown`): markdown view text
      - `omp.run.status` / `omp.run.status#<index>` (`*kit.VStack`, `Gap: 0`, optional):
        - `omp.tool.stats` / `omp.tool.stats#<index>` (`*kit.Text`, optional): `<N> earlier`
        - Per status event: either `omp.tool.eval.status#<c>.<e>` (`*kit.Text`), or when expanded with preview:
          - `omp.tool.eval.status.event#<c>.<e>` (`*kit.VStack`, `Gap: 0`):
            - `omp.tool.eval.status#<c>.<e>` (`*kit.Text`)
            - `omp.tool.eval.status.preview#<c>.<e>` (`*kit.Lines`, up to 10 lines)
      - `omp.run.foot` / `omp.run.foot#<index>` (`*kit.HStack`, `Gap: 1`, when cell started):
        - `omp.run.state` / `omp.run.state#<index>` (`*kit.Text`)
        - `omp.run.time` / `omp.run.time#<index>` (`*kit.Text`, optional)
        - `omp.run.facts` / `omp.run.facts#<index>` (`*kit.Text`, optional on last started cell)
  - Attached images (`res.Content` image blocks): `omp.tool.read.image#<index>` (`*kit.Image`).

### `grep`, `glob`, `find`, `ast_grep`, `ast_edit`

Upstream: `packages/tui/src/tools/{grep,glob,find,ast-grep,ast-edit,grouped-file-output}.ts`.

- **`grep` CallView (`omp.tool.grep.call`, `*kit.Container`)** — empty body (`grep.ts:303-305`).
- **`grep` ResultView (`omp.tool.grep`, `*kit.Container`, `grep.ts:312-371`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - Per matched file (up to 2 collapsed, all expanded):
    - `omp.tool.search.file#<index>` (`*kit.VStack`, `Gap: 1`):
      - `omp.tool.file#<index>` (`*kit.Text`): clean file `<path>` (no appended match count).
      - `omp.tool.search.lines#<index>` (`*kit.Lines`): raw matched/context code lines without terminal `*N│` gutters (tabs preserved).
  - `omp.tool.stats` / `omp.tool.notice` (`*kit.Text`, optional): `<N> more files · skipped missing · binary matches · truncation`.
- **`glob` CallView (`omp.tool.glob.call`, `*kit.Container`)** — empty body (`glob.ts:304-306`).
- **`glob` ResultView (`omp.tool.glob`, `*kit.Container`, `glob.ts:312-377`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - `omp.tool.files`:
    - When `len(files) <= 6`: `*kit.VStack` (`Gap: 0`) of `omp.tool.file#<index>` (`*kit.Text`: raw file path).
    - When `len(files) > 6`: `*kit.HStack` (`Gap: 1`) of `omp.tool.chip#<index>` (`*kit.Text`: raw file path, capped at 200 + optional `omp.tool.stats` `+<N> more`).
  - `omp.tool.stats` / `omp.tool.notice` (`*kit.Text`, optional): truncation reasons, skipped missing paths.
- **`find` CallView (`omp.tool.find.call`, `*kit.Container`)** — empty body (`find.ts:397-406`).
- **`find` ResultView (`omp.tool.find`, `*kit.Container`, `find.ts:413-494`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - Partial / no details: `omp.tool.stats` (`*kit.Text`, progress line).
  - Zero hits: `omp.tool.find.range#<index>` (`*kit.Text`, lexical fallbacks) + `omp.tool.notice#<index>` (`*kit.Text`, failures).
  - Hits (4 collapsed / all expanded; 1 range collapsed / 3 expanded per hit):
    - `omp.tool.find.hit#<index>` (`*kit.VStack`, `Gap: 1`):
      - `omp.tool.file#<index>` (`*kit.Text`): `<rel> · <score> · <N> lines judged[, partial]`
      - `omp.tool.find.range#<h>.<r>` (`*kit.Text`): `L<start>-<end> (<p>)\n<snippet>`
  - `omp.tool.notice#<index>` (`*kit.Text`, failures), `omp.tool.stats` (`*kit.Text`, `<N> more hits · keywords: …`).
- **`ast_grep` CallView (`omp.tool.ast_grep.call`, `*kit.Container`)** — empty body (`ast-grep.ts:184-190`).
- **`ast_grep` ResultView (`omp.tool.ast_grep`, `*kit.Container`, `ast-grep.ts:192-262`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - Grouped matches: `omp.tool.search.file#<index>` > `omp.tool.file#<index>` + `omp.tool.search.lines#<index>`.
  - `omp.tool.notice` (`*kit.Text`, optional): limit reached / mis-scoped warning / parse issues.
- **`ast_edit` CallView (`omp.tool.ast_edit.call`, `*kit.Container`)** — empty body (`ast-edit.ts:312-320`).
- **`ast_edit` ResultView (`omp.tool.ast_edit`, `*kit.Container`, `ast-edit.ts:322-402`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - `omp.tool.context` (`*kit.Text`, optional when 1 rewrite op): collapsed pattern preview.
  - Single file: `omp.tool.ast_edit.diff` (`*kit.Diff`, `Diff` + `FilePath`).
  - Multi-file: `omp.tool.ast_edit.file#<index>` (`*kit.VStack`, `Gap: 0`):
    - `omp.tool.ast_edit.file.head#<index>` (`*kit.Text`): `<path> [+A −R]`
    - `omp.tool.ast_edit.diff#<index>` (`*kit.Diff`, `Diff` + `FilePath`)
  - `omp.tool.notice` (`*kit.Text`, optional parse issues), `omp.tool.stats` (`*kit.Text`, scope/searched/limit).

### `todo`

Upstream: `packages/tui/src/tools/todo.ts:550-595, 792-799`.

- **CallView (`omp.tool.todo.call`, `*kit.Container`)** — empty body (`todo.ts:792-795`).
- **ResultView (`omp.tool.todo`, `*kit.Container`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - Empty todos: `omp.tool.notice` (`*kit.Text`).
  - Non-empty todos:
    - `omp.todo.checklist` (`*kit.VStack`, `Gap: 0`):
      - `omp.todo.phase#<p>` (`*kit.VStack`, `Gap: 0`):
        - `omp.todo.phase.head#<p>` (`*kit.Text`, when phase has a title): plain `<name>` (no Roman numerals, per `tools/todo.ts:547-557`)
        - `omp.todo.item#<p>.<t>` (`*kit.Text`): `<checkbox> <content>` (`☑`/`◐`/`☒`/`☐`)
        - `omp.todo.more#<p>` (`*kit.Text`, optional when collapsed): `… <N> more todos`

### `ask`

Upstream: `packages/tui/src/tools/ask.ts:160-209`, `packages/tui/src/native/ask-card.ts:72-227`.

- **CallView (`omp.tool.ask.call`, `*kit.Container`)**
  - When `args.html` has named fields (`ask-card.ts:72-227`):
    - `omp.ask.card` (`*kit.VStack`, `Gap: 1`):
      - `omp.ask.card.head` (`*kit.HStack`, `Gap: 1`):
        - `omp.ask.card.tag` (`*kit.Text`): `"ask"`
        - `omp.ask.card.progress` (`*kit.Text`): `"<answered> of <total> answered"`
      - `omp.ask.card.body` (`*kit.VStack`, `Gap: 1`):
        - Question 0: `omp.ask.open#0` (`*kit.VStack`, `Gap: 1`)
        - Questions 1..N: `omp.ask.fold#<i >` (`*kit.VStack`, `Gap: 0`)
        - Each question contains:
          - `omp.ask.fold.head#<i >` (`*kit.HStack`, `Gap: 1`): `omp.ask.fold.mark#<i >` (`✓`/`○`/`!`) + `omp.ask.fold.question#<i >` (`*kit.Text`)
          - Summary:
            - Choice field: `omp.ask.pills#<i >` (`*kit.HStack`, `Gap: 1`) > up to 4 `omp.ask.pill#<i >.<p>` (`*kit.Text`), optional `omp.ask.pill.more#<i >` (`*kit.Text`, `+<N>`), optional `omp.ask.fold.note#<i >` (`*kit.Text`)
            - Answered text field: `omp.ask.fold.answer#<i >` (`*kit.Text`) (or wrapped in `omp.ask.pills#<i >` with `omp.ask.fold.note#<i >`)
            - Unanswered text field: `omp.ask.fold.well#<i >` (`*kit.Text`, placeholder)
          - `omp.ask.error#<i >` (`*kit.Text`, optional)
      - `omp.ask.skip` (`*kit.Text`, optional)
  - When no HTML fields yet: `omp.ask.card.body` (`*kit.Text`: `"Waiting for your answer…"`).
- **ResultView (`omp.tool.ask`, `*kit.Container`, `ask.ts:187-209`)**
  - Fallback (no `details.answers` or empty `answers` slice, including validation errors and unanswered timeouts): `omp.tool.error` (if `rc.IsError`) or `omp.tool.notice` (`*kit.Text`).
  - Answered fields (`len(details.answers) > 0`):
    - `omp.ask.card.body` (`*kit.VStack`, `Gap: 1`):
      - `omp.ask.fold#<i >` (`*kit.VStack`, `Gap: 0`):
        - `omp.ask.fold.question#<i >` (`*kit.Text`): question text (no duplicate `omp.ask.fold.mark`, since `omp.tool.answer` carries the check icon in Tern)
        - `omp.tool.answer#<i >` (`*kit.Text`, masked if password) OR `omp.tool.answer.off#<i >` (`*kit.Text`, `"(no answer)"`)
        - `omp.tool.answer.note#<i >.<n>` (`*kit.Text`, optional): `<option>: <note>`
    - `omp.tool.notice` (`*kit.Text`, when `details.timedOut`): `"auto-selected after timeout — not a user choice"`.

### `task` / `subagent`

Upstream: `packages/tui/src/tools/task.ts:1808-2194`, `agent-tree.ts`.

- **CallView (`omp.tool.task.call`, `*kit.Container`, `task.ts:2058-2105`)**
  - `omp.tool.context` (`*kit.Text`, optional): shared batch context
  - Per requested spawn: `omp.task.agent#<index>` (`*kit.VStack`, `Gap: 0`):
    - `omp.task.agent.row#<index>` (`*kit.Text`): `<name> [(<agent>)] · pending [· isolated] · <summary>`
    - `omp.task.assignment#<index>` (`*kit.Markdown`, optional): full brief
- **ResultView (`omp.tool.task`, `*kit.Container`, `task.ts:2107-2194`)**
  - `omp.tool.context` (`*kit.Text`, optional)
  - Per settled result then in-flight progress (recursive up to depth 8): `omp.task.agent#<index>` (`*kit.VStack`, `Gap: 0`):
    - `omp.task.agent.row#<index>` (`*kit.Text`): `<id> [(<agent>)] · <status> [· <model> [<thinking>]] [· background] [· isolated] [· <activity>] [· <req>] [· <tok>] [· <cost>] [· <dur>]`
    - `omp.task.assignment#<index>` (`*kit.Markdown`, when expanded)
    - `omp.tool.error#<index>.0` / `omp.tool.error#<index>.1` (`*kit.Text`, optional abortReason / error; single-hash `#<index>.<n>` IDs)
    - `omp.task.output#<index>` (`*kit.Markdown` for text output, or `*kit.Lines` for structured JSON / recentOutput)
    - `omp.tool.output#<index>` (`*kit.Lines`, optional stderr on failure)
    - `omp.task.meta#<index>` (`*kit.Text`, optional `Patch: … · Branch: …`)
    - Nested child `omp.task.agent#<childIdx>` nodes (or `omp.tool.notice` on depth cap)
  - When 0 agents (including validation errors): `omp.tool.error` (if `rc.IsError`) or `omp.tool.notice` (`*kit.Text`).
  - `omp.tool.stats` (`*kit.Text`, optional): `<N> succeeded · <M> failed · <A> aborted · <R> req · <dur>`.

### `wait`

Upstream: `packages/tui/src/tools/wait.ts:798-905`, `job-widget.ts:403-608`.

- **CallView (`omp.tool.wait.call`, `*kit.Container`)** — empty body (`wait.ts:885-887`).
- **ResultView (`omp.tool.wait`, `*kit.Container`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - On `details.interrupted`: `omp.tool.notice` (`*kit.Text`: `"interrupted by message"`).
  - Job tiles (sorted running → failed → cancelled → completed, up to 12; figure and blocks emitted only when `widget.state == "live"`, `job-widget.ts:404`; glyphs `●`/`✓`/`✕`/`■`, `job-widget.ts:370-375`; `widget.blocks` counted against `nvMaxNodes` with `omp.job.tile.more#<i >` on cap):
    - `omp.job.tiles` (`*kit.VStack`, `Gap: 1`):
      - `omp.job.tile#<i >` (`*kit.VStack`, `Gap: 0`):
        - `omp.job.tile.head#<i >` (`*kit.HStack`, `Gap: 1`):
          - `omp.job.tile.dot#<i >` (`*kit.Text`)
          - `omp.job.tile.title#<i >` (`*kit.Text`)
          - `omp.job.tile.id#<i >` (`*kit.Text`, when distinct from title)
          - `omp.job.tile.ring#<i >` (`*kit.Lines` with `Progress`, optional)
          - `omp.run.time#<i >` (`*kit.Text`, optional)
        - `omp.job.tile.figure#<i >` (`*kit.Text`, optional headline when `widget.state == "live"`)
        - Primary block visual (optional, when `widget.state == "live"`):
          - `omp.job.tile.chart#<i >` (`*kit.Text`, sparkline summary)
          - OR `omp.job.tile.meter#<i >` (`*kit.Lines` with `Progress`)
          - OR `omp.job.tile.bars#<i >` (`*kit.VStack`, `Gap: 0`) > `omp.job.tile.bar#<i >.<b>` (`*kit.HStack`, `Gap: 1`: `omp.job.tile.key#<i >.<b>`, `omp.job.tile.meter#<i >.<b>`, `omp.job.tile.num#<i >.<b>`) + optional `omp.job.tile.more#<i >`
          - OR `omp.job.tile.grid#<i >` (`*kit.Text`)
        - `omp.job.tile.lead#<i >` (`*kit.Text`, when no figure/visual) or `omp.job.tile.stage#<i >` (`*kit.Text`)
        - `omp.job.tile.stats#<i >.<b>` (`*kit.Text`, optional), `omp.job.tile.note#<i >.<b>` (`*kit.Text`, optional)
  - `omp.wait.more` (`*kit.Text`, when `len(jobs) > 12`): `+<N> more job(s) · /jobs`.
  - `omp.wait.agent#<i >` (`*kit.Text`, per active agent): `<badge> · <id> [· <activity>] [· ← <parentId>] [· <age>]`.

### `web_search`

Upstream: `packages/tui/src/tools/web-search.ts:308-420`.

- **CallView (`omp.tool.web_search.call`, `*kit.Container`)** — empty body (`web-search.ts:409-411`).
- **ResultView (`omp.tool.web_search`, `*kit.Container`)**
  - On error: `omp.tool.error` (`*kit.Text`).
  - `omp.tool.web_search.answer` (`*kit.Markdown`, optional synthesized answer).
  - `omp.tool.sources` (`*kit.VStack`, `Gap: 0`, when sources present):
    - `omp.tool.source#<i >` (`*kit.VStack`, `Gap: 0`):
      - `omp.tool.source.head#<i >` (`*kit.Text`): `<title> [· <domain> · <age>] [<url>]`
      - `omp.tool.excerpt#<i >` (`*kit.Text`, up to 3 lines when no synthesized answer)
  - `omp.tool.notice` (`*kit.Text`, when no response/answer/sources).
  - `omp.tool.stats` (`*kit.Text`, optional token/search usage).

### `lsp`, `github`, `debug`

- **`lsp` (`omp.tool.lsp.call` / `omp.tool.lsp`, `tools/lsp.ts:922-1147`):**
  - CallView: empty body.
  - ResultView: `omp.tool.error` (on error or server failure rows), `omp.tool.diagnostic#<i >` (`*kit.Text`), `omp.tool.search.file#<i >` (`omp.tool.file#<i >` clean path + `omp.tool.lsp.body#<i >`), `omp.tool.lsp.body` (`*kit.Markdown` for fenced hover, `*kit.Lines` for symbols/generic/other), `omp.tool.notice` (`"No result"`), `omp.tool.stats` (overflow count when capped).
- **`github` (`omp.tool.github.call` / `omp.tool.github`, `tools/github.ts:525-728`):**
  - CallView: `omp.tool.notice` (`"waiting for workflow data..."`) for `op=="run_watch"`, else empty.
  - ResultView:
    - Watch (`details.watch`): `omp.tool.github.watch` (`*kit.VStack`, `Gap: 1`) > optional `omp.tool.notice`, `omp.tool.github.job#<i >` (`*kit.Text`, jobs + expanded steps), `omp.tool.github.log#<i >` (`*kit.Lines`, failed log header + tail/reason).
    - Non-watch: `omp.tool.error` (on error), `omp.tool.notice` (`"no output"`), or `omp.tool.github.body` (`*kit.Markdown`).
- **`debug` (`omp.tool.debug.call` / `omp.tool.debug`, `tools/debug.ts:134-235`):**
  - CallView: empty body.
  - ResultView: `omp.tool.error` (on error), optional `omp.tool.debug.snapshot` (`*kit.Lines` with 1:1 `List.Items` of Session/Adapter/Status/CWD/Program/Stop reason/Frame/Instruction pointer/Location/Configuration/Exit code), `omp.tool.debug.output` (`*kit.Lines`) or `omp.tool.notice` (`"No output"`).

### `resolve`, `reject`, `propose`, `report_issue`, `retain`, `recall`, `reflect`, `think`, `goal`, `yield`, `checkpoint`, `rewind`, `browser`

- **`resolve` / `reject` (`omp.tool.resolve.call` / `omp.tool.resolve`, `tools/resolve.ts:197-236`):** `omp.tool.stats` (`*kit.Text`, reason) or `omp.tool.error` (on error).
- **`propose` / `report_issue` (`omp.tool.<name>.call` / `omp.tool.<name>`, `tools/resolve.ts:101-117`):** CallView empty; ResultView `omp.tool.output` (`*kit.Text`) or `omp.tool.error`.
- **`retain` / `recall` / `reflect` (`omp.tool.memory.call` / `omp.tool.memory`, `tools/memory.ts:89-296`):**
  - `retain`: `omp.memory.item#<i >` (`*kit.Text`) or `omp.tool.error`.
  - `recall`: `omp.memory.item#<i >` (`*kit.Text`, up to 10 lines) or `omp.tool.error`.
  - `reflect`: `omp.memory.reflect` (`*kit.Markdown`, up to 10 lines) or `omp.tool.error`.
- **`think` (`omp.tool.think`, `tools/think.ts:32-45`):** CallView emits `omp.think` (`*kit.Markdown`); ResultView is empty `*kit.Container`.
- **`goal` (`omp.tool.goal.call` / `omp.tool.goal`, `tools/goal.ts:71-205`):** CallView empty; ResultView `omp.tool.context` (`*kit.Text`, quoted objective), `omp.tool.stats` (`*kit.Text`, tokens/elapsed), optional `omp.tool.notice` (report), or `omp.tool.error`.
- **`yield` (`omp.tool.yield.call` / `omp.tool.yield`, `tools/yield.ts:101-131, 213-234`):** `omp.tool.error` (if error/aborted), `omp.tool.yield.data` (`*kit.Markdown` for string data, `*kit.Lines` for structured JSON), optional `omp.tool.notice` (empty/finalization note).
- **`checkpoint` / `rewind` / `browser` (`omp.tool.<name>.call` / `omp.tool.<name>`, `tools/default-renderer.ts:196-229`):** CallView `omp.tool.args` (`*kit.Text`, inline args summary <=160 chars); ResultView `omp.tool.output` (`*kit.Lines`, pretty JSON or raw text), `omp.tool.notice` (`"(no output)"`), or `omp.tool.error`.
