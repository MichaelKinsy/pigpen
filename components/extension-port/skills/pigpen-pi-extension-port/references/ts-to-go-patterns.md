# TypeScript to Go patterns for Pi extensions

Patterns for step 3 and 4 of the porting Skill. Each entry says what to preserve and
what the Go SDK gives you. Signatures are from PiG 0.3.0 (`extensions/sdk`); confirm them
against the SDK of the PiG you target. `sdk` is
`github.com/MichaelKinsy/PiG/extensions/sdk`.

## Registration shape

| Pi | Go |
|---|---|
| `export default function (pi) { ... }` | `func Extension() *sdk.Extension { e := sdk.New("name"); ...; return e }` (package name from the directory, `_` for `-`) |
| `pi.on("event", (event, ctx) => ...)` | `e.OnEvent(sdk.EventXxx, func(ctx sdk.Context, data map[string]any) (any, error))` |
| `pi.registerTool({...})` | `e.Tool(name, description, sdk.Schema{...}, func(ctx sdk.Context, params map[string]any) (any, error))` and the `ToolWith...` variants |
| `pi.registerCommand(name, {handler})` | `e.Command(name, description, func(ctx sdk.Context, args string) error)` |
| `pi.registerFlag`, `registerShortcut` | `e.Flag`, `e.Shortcut` |
| `pi.registerProvider`, message/entry renderers | `e.RegisterProvider`, `e.MessageRenderer`, `e.EntryRenderer` (stand-ins: lines, not components) |

The extension's identity is its **directory name**. A copy under another name fails with
`register:name_mismatch`; keep the name when you copy a port for testing.

## Event mapping

Names are identical (`sdk.EventSessionBeforeSwitch == "session_before_switch"`). The handler's
`data` map is the event payload as JSON; numbers arrive as `float64`, arrays as `[]any`,
objects as `map[string]any`. Read fields with checked type assertions, and treat a missing
field like `undefined`.

Return values follow Pi's result objects:

- cancel: `return map[string]any{"cancel": true}, nil` (`session_before_switch`, `session_before_fork`,
  `session_before_compact`, `session_before_tree`);
- block: `map[string]any{"block": true, "reason": "..."}` (`tool_call`);
- modify: the event's result fields (`tool_result` `content`/`isError`/`details`, `input` `action`/`text`,
  `before_agent_start` `systemPrompt`/`message`, `context` `messages`);
- nothing: `return nil, nil`. `undefined` and "no result" are the same.

Notes:

- Handlers of one event run in registration order, as in Pi. Do not add sorting.
- An error returned from a handler surfaces as `extension_error`; Pi does the same for a thrown
  error. Do not swallow errors to "keep going" unless the original does.
- A session replacement (`newSession`, `fork`, `switchSession`, `reload`) restarts extensions. State kept in
  the extension is lost exactly as in Pi; do not persist to hide it.
- Events PiG's host does not emit are listed as `missing (the host never emits this event)` in the
  surface table; the gap scanner reports them.

## Async and Promise to goroutines

A Promise is a contract, not a goroutine (see PiG `docs/extension-authoring.md`).

| TypeScript | Go |
|---|---|
| `await x()` in a handler | call it and return the result; the SDK runs each handler on its own goroutine, so blocking preserves `await` |
| `Promise.all([a(), b()])` | start both, join both (`sync.WaitGroup` or channels), return after both; do not detach |
| `Promise.race` / `AbortSignal` | `select` on `ctx.Done()`; `ctx.Err()` after; test cancellation before and during the work |
| fire and forget (`void foo()`) | a goroutine that has an owner: its own cancellation, error reporting, and a join at `session_shutdown` |
| `setTimeout(fn, ms)` | `time.AfterFunc` or a `select` with `time.After` inside an owned goroutine; stop it on shutdown |
| `setInterval` | a ticker in an owned goroutine, stopped on shutdown |
| single-threaded ordering (a callback never overlaps another) | one goroutine draining a queue, or a mutex; say which in a comment and test the ordering |
| "only the newest state is sent, never overlapping" | a single worker with a latest-value slot (coalescing), not one goroutine per event |

Do not use `go func()` to return early from a handler: it changes ordering, drops the error and
cancels the handler-scoped `sdk.Context` when the handler returns.

## Events that arrive in any order

Pi delivers an extension's events in the order it emitted them, but data an extension gathers
(child output lines, results of parallel calls, a slice built from map iteration) may not: a
JavaScript object keeps insertion order and a Go map never does. Where the original's output order
matters, key the data with a sequence or sort explicitly and test with an event order that differs
from the natural one. A Go test that passes only because the events arrive in the order they were
written proves nothing.

## A model call from an extension

`ctx.ModelRegistry().Complete/Stream` sends the `modelStream` host call; the host answers with
`model_stream_event` notifications and then the call result. Script it in layer 1 with
`HostOptions.ModelStream` and `ModelText("...")` / `ModelError("...")` from the fake host; do not
hand-write the frames. Under the differential check use `pigeq llm --script`.

## UI and dialogs

| Pi | Go (blocking; the host UI loop is not blocked) |
|---|---|
| `await ctx.ui.select(title, options)` returns `string \| undefined` | `choice, ok, err := ctx.Select(title, options)`; `!ok` is `undefined` (dismissed) |
| `await ctx.ui.confirm(title, message)` | `ok, err := ctx.Confirm(title, message)` |
| `await ctx.ui.input(title, placeholder)` | `value, ok, err := ctx.Input(title, placeholder)` |
| `await ctx.ui.editor(title, prefill)` | `text, ok, err := ctx.Editor(title, prefill)` |
| `ctx.ui.notify(msg, level)` | `ctx.Notify(msg, level)` (`info`, `warning`, `error`) |
| `ctx.ui.setStatus`, `setWidget`, `setTitle` | `ctx.SetStatus`, `ctx.SetWidget` (lines only), `ctx.SetTitle` |
| `{ timeout }` option | `SelectWithOptions(..., sdk.DialogOptions{Timeout: ms})`; RPC honors it, interactive dialogs ignore it (partial) |
| `{ signal }` option | the handler's `ctx.Done()`; partial |
| `ctx.hasUI` | `ctx.HasUI()` (false in print and JSON mode) |

`choice !== "Yes"` in TypeScript is true for a dismissed dialog; the Go equivalent is
`!ok || choice != "Yes"` (the `!ok` is redundant but documents the case). A dismissed dialog and a
refused dialog are different events; scenario both. Component factories (`ctx.ui.custom`, custom editors,
custom footers) cannot cross the process boundary; use `RemoteComponent` and record it as a stand-in.

## exec and child processes

- `pi.exec(cmd, args, opts)` maps to `ctx.Exec(cmd, args)` or `ctx.ExecWithOptions`. Pi's `exec` never
  rejects: a command that cannot start resolves with `code: 1`; PiG's host reports the same through the
  result. Branch on `res.ExitCode`. An `error` from `Exec` is a host or transport failure: return it,
  do not turn it into "not a repository".
- `stdout` is text; JavaScript decodes invalid UTF-8 with replacement characters. Go strings keep the bytes.
  If the original measures or trims the output, port the JavaScript semantics (below).
- `opts.timeout` is milliseconds (a JS number: fractions and huge values are legal): `ExecOptions.Timeout float64`.
- The command runs in the session's working directory. `ctx.Cwd()` is the same directory; never `os.Chdir`.
- Node's own `child_process` (used when the original does not call `pi.exec`): port to `os/exec`
  with `exec.CommandContext` and an explicit kill timeout; kill the process group where the original does.
  Put `SysProcAttr{HideWindow: true}` in `procattr_windows.go` and nothing in `procattr_other.go`
  (`//go:build !windows`). Test with the test binary re-executed as the fake tool (Skill step 3).
- Environment: `os.Getenv` in the extension process sees the host's environment. Tests set it with
  `t.Setenv`. Never read another process's environment.

## Error text fidelity

- The message a user sees is part of the contract. Copy it exactly, including punctuation and the
  count/plural wording ("file(s)"), and cite the upstream line.
- `String(err)` and `err.message` differ (`Error: msg` versus `msg`); `fmt.Errorf("%w")` prefixes differ
  again. Match what the original shows, then scenario it (`extension_error` and tool `isError` results are
  in the trace).
- A tool that throws returns an error result with the thrown text; a tool that returns `{isError: true}` is
  different. Go: return an `error` (or `sdk.NewToolError(text)`) for the former; check how `ToolResult` encodes
  the latter. Checked with the harness on PiG 0.3.0: `throw new Error("tool failed with 7")` and
  `fmt.Errorf("tool failed with %v", n)` give identical `tool_execution_end` results, and a thrown command error
  and a returned one give identical `extension_error` records; one changed character in either fails the check.
- `null` versus `undefined` versus `""` versus `0` are different observable values. Use pointers, presence
  booleans or `json.RawMessage` where the original distinguishes them; the SDK uses `*int`, `*bool`,
  `sdk.Bool(...)` for this.

## Strings, numbers, ordering

- `trim()` removes JavaScript whitespace: U+0009-000D, U+0020, U+00A0, U+1680, U+2000-200A, U+2028,
  U+2029, U+202F, U+205F, U+3000 and **U+FEFF**; it does **not** remove U+0085. Go's `strings.TrimSpace`
  does the opposite on both counts. Write `jsTrim` (see `dirty-repo-guard`) and test it.
- `.length`, `slice`, `substring`, `padEnd` count UTF-16 code units; Go counts bytes. Choose deliberately
  (`unicode/utf16`, runes, or terminal cell width) and test non-ASCII and astral characters.
- `split("\n").filter(Boolean)` drops empty strings only; a whitespace-only line stays.
- JavaScript numbers are float64: integer-valued JSON numbers arrive as `float64` in a Go `map[string]any`.
- Object and `Map` insertion order is observable (model lists, tool lists). Go maps are unordered: keep a
  slice of pairs and an index; never sort to "make it deterministic".
- Regular expressions: JavaScript and RE2 differ (no lookahead, different `\b` on Unicode, `i` flag folding).
  Probe the original with the actual inputs and use a hand-written matcher where RE2 differs.
- `JSON.stringify` key order and spacing, `toString()` of numbers, and date formatting are observable when
  the original prints them. Events reach a Go handler as `map[string]any`, so the key order of an object the
  extension echoes back to a client or a model is lost: keep the wire struct with fixed field order, or record the
  order change as a deviation (pigpen-ahp found this porting a protocol host).

## Timers, clocks and races in tests

- A component or loop driven by a clock (a game tick, a debounce, a heartbeat) has no observation point to
  hold, so do not sleep in tests (L2, L6). Inject the clock: the component takes a step function or a ticker
  interface, tests advance it by hand, and one real-timer case checks the goroutine lifecycle (start, stop,
  no leak after the command returns). pigpen-pig-snake and pigpen-games both needed this.
- A value a test changes while a component goroutine reads it is a race the detector will find: use an
  `atomic` type or a mutex in the test seam, not a plain int.

## Ports whose bulk is not SDK glue

- Layer 1 (the fake host) is for what crosses the host boundary: registration, events, tools, commands, `ctx.*`
  calls, wire shapes. Pure logic (a protocol library, a parser, a game's rules) gets ordinary unit tests that
  import nothing from PiG; only the twin titles connect them to the original (`pigeq twins check`).
- When the oracle's own tests are red in your environment (a missing environment variable, a package manager
  version, tests that time out even for the unmodified original), prove that first and record it: run the original's
  suite, save the baseline counts and the environment recipe in `PORT.md`, and keep each red upstream case as a
  named skip with reason `oracle-red` plus a Go test of the behavior taken from the source (pigpen-ahp: 10 of 430).
- Third-party Go modules are allowed. "Public SDK only" restricts imports of PiG packages, not other modules.
  Ports that shipped with one (golang.org/x/net, a2a-go, a vendored client) built fused. Commit `go.sum`;
  vendored third-party code stays byte-for-byte with its license; `pigeq env` maps the SDK with a `replace`, which
  is what lets `go test` resolve it beside external dependencies (a `use` of the SDK fails with
  `sdk@v0.0.0: unknown revision`).

## Windows paths and processes

- Use `path/filepath` for filesystem paths and `path` only for URL-like paths. Never concatenate with `/`.
- The original may hard-code `/` or `~`: port the behavior the original has on Windows (Node normalizes
  some separators), and add a table test of Windows shapes (`C:\Users\x`, `\\server\share`, `C:/mixed/sep`).
  Drive letters and case-insensitive comparison need `strings.EqualFold` on Windows only.
- `os.UserHomeDir` uses `USERPROFILE` on Windows; Node's `os.homedir()` does too. Do not assume `$HOME`.
- Absolute-path tests (`path.isAbsolute`) differ: `filepath.IsAbs("/x")` is false on Windows.
- Child processes: `HideWindow` in a build-tagged file (above); a `.cmd` or `.bat` shim needs a shell on
  Windows, a plain executable does not.
- Line endings: `\r\n` from Windows tools survive `trim` only at the ends. Test both.
- `GOOS=windows go vet` is the minimum, native verification is the claim. Do not claim a target that
  passed only `go vet`.

## Gaps and known host differences

- **`pi.events.on/emit` (event bus): missing for Go.** An extension that listens for another extension's
  bus event (herdr's `herdr:blocked`) loses that behavior. Needs a PiG SDK feature or an approved exclusion.
  `pigeq gaps` reports it.
- **`ctx.newSession/fork/switchSession` `withSession`: missing for Go**, and a fresh context's UI calls after
  a replacement are not delivered by PiG's RPC UI. Do not rely on post-replacement UI in a port.
- PiG's RPC loop handles `new_session`, `fork` and `switch_session` inline: a dialog raised by a
  `session_before_*` handler during those RPC commands cannot be answered. Start the operation from a slash
  command (the harness driver does) instead.
- PiG does not raise Pi's "stale ctx" error for a captured context used after a replacement.
- `after_provider_response` and `cache_warming_decision` are subscribable but the host does not emit them.

- **A fused extension shares PiG's process.** A Go library that logs to stderr, prints, calls
  `os.Exit`, or installs signal handlers acts on the host itself. Route a library's logging to a
  discard or to `ctx` notifications, and read `pigeq gaps --go` output for these hazards.
- **An adapter drives an extension's UI through tools, not slash commands.** Slash commands run
  from an interactive prompt; a protocol adapter (acp, a2a) has no prompt to type one into, so
  the port exposes the same action as a tool the adapter can call.

Extend this list with each port's findings.
