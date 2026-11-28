# Upstream reference: `internal/debug`

Port of omp's `debug` tool and DAP client/session subsystem (https://github.com/can1357/oh-my-pi, MIT, release tag `v19.1.1`, commit `31ffee513f`).

## Load mode at `v19.1.1`

- `DebugTool.loadMode = "discoverable"` (`packages/coding-agent/src/tools/debug.ts:580`).
- Registered with `Exposure: sdk.ToolExposureCodemode`, `DefaultActive: false`, and `toolcmd.Mountable("debug", ...)` (reading `debug.enabled` via package variable `Enabled`). With `tools.commands` enabled (default `true`) and `bash` active, `internal/toolcmd` mounts `debug` as a bash command and executes it through `Context.ExecuteTool`; with `tools.commands` disabled, `toolcmd` activates `debug` top-level.
## Upstream files followed (`v19.1.1`)

- `packages/coding-agent/src/tools/debug.ts`
- `packages/coding-agent/src/prompts/tools/debug.md` (no template branches)
- `packages/coding-agent/src/tools/tool-timeouts.ts` (`TOOL_TIMEOUTS.debug`: default 30s, min 5s, max 300s)
- `packages/coding-agent/src/tools/path-utils.ts` (`resolveToCwd`, `formatPathRelativeToCwd`)
- `packages/coding-agent/src/tools/tool-errors.ts` (`ToolAbortError`)
- `packages/coding-agent/src/dap/types.ts`
- `packages/coding-agent/src/dap/defaults.json`
- `packages/coding-agent/src/dap/config.ts`
- `packages/coding-agent/src/dap/client.ts`
- `packages/coding-agent/src/dap/session.ts`
- `packages/coding-agent/src/jsonrpc/message-framing.ts`
- `packages/coding-agent/src/exec/non-interactive-env.ts`
- `packages/coding-agent/src/lsp/config.ts` (`hasRootMarkers`, `resolveCommand` local bin resolution)
- `packages/coding-agent/src/utils/config-file-sources.ts`
- `packages/tui/src/tools/debug.ts` (`DebugSessionSnapshot`, `DebugToolDetails`, `formatLocation`, `formatSessionSnapshot`)

## Settings read in omp

| Key | Default | Honoured | Notes |
|---|---|---|---|
| `debug.enabled` | `true` | Via `debug.Enabled` | Package-level `Enabled func(ctx sdk.Context, key string, def bool) bool` defaults to `true`; wired into `toolcmd.Mountable("debug", ...)` and assignable by the integrator/settings area. |
| `tools.maxTimeout` | `0` (no global cap) | Via `Config.MaxTimeoutSec` | Defaults to `0`; clamped through `clampDebugTimeout` (floor 5s, ceiling 300s). |
| `tools.commands` | `true` | Delegated to `internal/toolcmd` | `debug` declares `loadMode = "discoverable"` upstream and registers `Exposure: sdk.ToolExposureCodemode` + `toolcmd.Mountable`. |

## Changed on purpose

| Behaviour | Upstream | Here | Why |
|---|---|---|---|
| Project trust gating for project-supplied adapter commands & project-local bins | `utils/config-file-sources.ts:55-57` and `lsp/config.ts:304-317` always load cwd/project `dap.{json,yaml,yml}` and project-local bins (`node_modules/.bin`, `.venv`, `bin`), relying on omp's `exec` approval prompt (`tools/debug.ts:545-549`). | Calls `sdk.Context.IsProjectTrusted()`. When untrusted (or on error), only user-level/home configs and `PATH` binaries are used; project configs and project-local bins are skipped, and a one-time note is appended to the tool result text for that session. | Integrator decision: PiG owns project trust rather than omp's per-tool approval callback, so untrusted repositories cannot execute arbitrary binaries on `launch`/`attach`. |
| Per-PiG-session isolation via `internal/lifecycle` | `dap/session.ts:1891` uses one process-wide `dapSessionManager` singleton. | One shared package-level `defaultRegistry` keyed strictly by PiG `sessionID`, cleaned up via `lifecycle.OnEnd("debug", ...)` and `e.OnSessionShutdown -> lifecycle.End(id)`. Empty/unknown session IDs never alias a shared bucket and never tear down other sessions. | PiG runs multiple parent/child sessions in one process and builds a fresh extension instance per session/reload without emitting `session_shutdown` for child sessions. |
| `Dispose` kills process group after adapter self-exit (Unix only) | `dap/client.ts:582-583,751-753` returns early from `dispose()` once `#handleProcessExit` sets `#disposed = true`. | `connClosed` and `releaseOnce` are tracked separately; on Unix `Dispose()` always signals the adapter's process group (`-pid`), closes sockets, and removes Unix socket files even after self-exit, while on Windows `taskkill` is skipped once `c.exited` is closed so a reaped PID cannot be killed after reuse. | Guarantees no orphaned debuggee descendants outlive session shutdown on Unix without risking PID-reuse kills via `taskkill` on Windows. |
| `js-debug-adapter` runtime fallback when `node` is absent | `dap/config.ts:157` falls back to `process.execPath` (Bun itself). | Looks up `node`, then `bun` on `PATH`; reports `js-debug-adapter` unavailable if neither is installed. | This extension is a Go binary (`os.Executable()` cannot execute `dapDebugServer.js`). |
| `continue` / `step_*` timeout vs context deadline | `tools/debug.ts:598` creates `AbortSignal.timeout(timeoutSec*1000)` racing `#prepareStopOutcome`'s identical timer. | `Execute` passes a cancellation-only context; `awaitStopOutcome` owns the single stop-wait timer and returns omp's `"Program is still running after Ns. Use pause to interrupt and inspect state."` consistently regardless of adapter round-trip latency, while user cancellation returns `"Operation aborted"`. | Eliminates latency-dependent races and prevents Go's `"context deadline exceeded"` / `"context canceled"` from leaking to the model. |
| Delve `outputMode: "remote"` & prompt failure before `initialized` | `dap/config.ts:477-485` sets only `mode` (`debug`/`exec`) for `dlv`; `dap/session.ts:1518` waits for `initialized` even if `launch`/`attach` already rejected. | `ResolveLaunchOverrides` also sets `outputMode: "remote"` for `dlv` so debuggee stdout/stderr arrive as DAP `output` events rather than being discarded on socket-mode stdout; `completeConfigurationHandshake` wakes immediately without sending `configurationDone` if the pending `launch`/`attach` fails before `initialized` arrives (so omp's combined `DAP <cmd> failed ... DAP configurationDone also failed` message appears only when `initialized` arrives before the failed start response; otherwise the bare start error is returned). | Verified against real Delve 1.27.2 (`dlv dap`): without `outputMode: "remote"`, debuggee output goes to `dlv`'s own stdout instead of DAP `output` events, and on launch failure `dlv` returns `success: false` without ever sending `initialized`. |
| macOS `/private` path standardization (display vs wire) | `packages/utils/src/dirs.ts:148-158` (`standardizeMacOSPath`) strips `/private` on `getProjectDir()` once, so omp both prints and sends the `/private`-stripped path on the DAP wire (`tools/debug.ts:607-608,674`, `dap/session.ts:326-327,538`). | PiG hands extensions the realpath (e.g. `/private/var/...` or `/private/tmp/...`). `internal/debug` keeps unstandardized paths on the DAP wire (launch `program`/`cwd`, attach `cwd`, `setBreakpoints` `source.path`, breakpoint map keys) so real adapters like `dlv` match DWARF debug info in binaries compiled under the realpath, while standardizing model-facing text and `details` via `dirs.StandardizeMacOSPath`. | Sending a `/private`-stripped `source.path` to `dlv` against a binary compiled under `/private/var/folders/...` causes `setBreakpoints` to stay pending (`could not find file`); keeping wire paths raw preserves real-debugger functionality while matching omp's displayed paths. |

## Tests ported

- 44 Go test functions (+ 22 subtests) in `debug_test.go`, covering:
  - `test/debug/dap-config.test.ts` (12 upstream cases): custom JSON adapter + extension selection, partial user overrides over built-ins, `.omp/dap.yaml` loading, relative command resolution, invalid adapter skipping, missing `dlv` reporting for Go source and package directories, nested module `bin/dlv` precedence over cwd and PATH, explicit `dlv` nested lookup, cwd fallback, `go.work` root resolution, re-resolving after an earlier miss.
  - `test/dap-config-precedence.test.ts` (1 upstream case): cwd `dap.json` overrides `.omp/dap.yaml` field by field.
  - `test/debug/dap-launch-failures.test.ts` (21 upstream cases): invalid Content-Length framing, preserving `launchDefaults.args`, preferring launch/attach failures over `configurationDone` cascade (including delayed launch errors), `skipAttachRequest` preattached adapters, ENOENT spawn error naming adapter, `debugpy` missing-module hint on both launch and attach (and untouched for other adapters), wedged/failing stdin write resilience, delayed Unix socket connect and wedged Unix socket cleanup, wedged TCP client-addr cleanup, fast Unix socket connect rejection, ghost TCP socket resilience, `WaitForTcpServerListening` split-chunk banner and EOF gating, `DebugTool` launch/attach validation (directory rejection including `.` without `./`, `dlv` directory `mode=debug` & binary `mode=exec` through the tool, preferring `dlv` over `gdb` for Go package dirs, unavailable adapter hints for `debugpy`/`dlv`/custom/`none` through the tool, attach target validation and explicit adapter defaults without pid/port).
  - `test/debug/dap-multi-session.test.ts` (6 upstream cases): recursive child session tree via `startDebugging`, targeting a running attach child before `stopped`, reactivating a live session on child termination, focus preservation when late workers attach, per-session thread aggregation preserving identical thread IDs/names, `runInTerminal` stdout draining bounded at 128 KiB.
  - Additional Go tests: strict subprocess fake DAP adapter (enforces `launch`/`attach` before `configurationDone` and validates request arguments) across stdio (all 28 actions), TCP, real TCP child session (`ConnectDapClient`), and socket `--client-addr`; defaults.json insertion-order preservation; continue timeout returning `"Program is still running..."` under adapter latency; `Dispose` killing orphaned process-group children after adapter self-exit; exit detection when a descendant inherits stderr; project trust gating (trusted vs untrusted + once-per-session note); `Register` twice + two-session isolation + `lifecycle.End` cleanup; empty session ID rejection; `resolveToCwd` workspace alias `/` and internal URL rejection; live `dlv dap` integration test (skips when `dlv` is absent from `PATH`).

## Not ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| Tool approval callback & approval detail formatter (`DEBUG_READONLY_ACTIONS` read/exec tiering) | `tools/debug.ts:57-68,545-557` | PiG SDK `ToolDefinition` has no `approval` / `formatApprovalDetails` callbacks; `IsReadonlyAction` is exported if host approval wiring is added later. Untrusted-project protection is provided via `IsProjectTrusted()`. |
| Plugin-root `.dap.json` discovery (`getPreloadedPluginRoots`) | `utils/config-file-sources.ts:61-65` | Depends on omp's Claude plugin/marketplace discovery subsystem not present in PiG. Project (when trusted), user (`.omp`, `.claude`, `.codex`, `.gemini`), and home `dap.{json,yaml,yml}` files are all loaded. |
| Interactive `/debug` slash command & TUI diagnostics menu (logs, raw SSE, JSC remote debugger, profiler, report bundle, system/terminal info, protocol probe) | `packages/coding-agent/src/debug/*`, `packages/tui/src/apps/debug/*` | Local TUI diagnostics menu unrelated to the DAP `debug` tool; JSC remote debugger (`bun:jsc`) is Bun-specific. |
| Terminal & Tern-native TUI card renderer (`debugToolRenderer`) | `packages/tui/src/tools/debug.ts` | Renderers live in the skin/frontend lanes; `Details` emits every field `debugToolRenderer` and `describeDebugResult` read (`action`, `success`, `snapshot`, and all per-action fields). |
| Full YAML specification (anchors, multi-line block scalars, tags) in `dap.yaml` | `lsp/config.ts:49-55` (Bun YAML) | No third-party YAML module allowed; stdlib `yaml.go` parses nested mappings, block/flow sequences, quoted/unquoted scalars, booleans, numbers, nulls, and comments used in `dap.yaml`. |
