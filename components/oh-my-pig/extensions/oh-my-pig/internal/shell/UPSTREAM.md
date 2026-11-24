# Upstream reference: `internal/shell`

Port of omp's `bash` tool (https://github.com/can1357/oh-my-pi, MIT, commit `4df653426f40`).

## Upstream files followed

- `packages/coding-agent/src/tools/bash.ts`
- `packages/coding-agent/src/tools/bash-interceptor.ts`
- `packages/coding-agent/src/tools/bash-pty-selection.ts`
- `packages/coding-agent/src/tools/shell-tokenize.ts`
- `packages/coding-agent/src/tools/tool-timeouts.ts`
- `packages/coding-agent/src/prompts/tools/bash.md` (rendered with default branches: `hasEval=false`, `asyncEnabled=true`, `defaultTimeoutSec=300`, `hasLaunch=true`, `autoBackgroundEnabled=true`, `autoBackgroundSeconds=60`)
- `packages/coding-agent/src/exec/bash-executor.ts`
- `packages/coding-agent/src/async/job-manager.ts`
- `packages/coding-agent/src/async/auto-background.ts`
- `packages/coding-agent/src/launch/services.ts`
- `packages/tui/src/tools/bash.ts`
- `packages/tui/src/tools/streaming-output.ts`
- `packages/tui/src/tools/output-meta.ts`
- `packages/utils/src/format.ts`
- `packages/coding-agent/src/prompts/tools/async-result.md`
- `packages/coding-agent/src/session/async-job-delivery.ts`

## Persistent shell state design

Without an in-process brush shell, `internal/shell` persists per-session shell state using a **save/restore snapshot file** (`state.go`) rather than a long-lived interactive bash process:
- Before each command, the wrapper sources the session's snapshot file (`state.sh`) and runs the command via `eval` with `shopt -s expand_aliases`.
- An `EXIT` trap dumps updated state into a fresh temporary file (`declare -p`, `declare -f`, `alias -p`, `shopt -p`, `set +o`, filtering read-only and runtime-internal variables such as `PWD`/`OLDPWD`/`SHLVL`/`PIPESTATUS`/`BASH*`).
- Only when the command completes without timeout or cancellation is the temporary file atomically renamed over `state.sh`. A timed-out or killed command discards its temp file, so it never kills or corrupts the session's state.
- Overlapping calls while the session shell is busy run in an isolated temporary shell with omp's `SESSION_SHELL_BUSY_NOTICE`; explicit `async: true` jobs also run isolated.
- **What persists**: exported and non-exported shell variables (including arrays), shell functions, aliases, `shopt` options, and `set -o` options (excluding `monitor`/`interactive`/`stdin`/`hashall`/`history`/`histexpand`).
- **What does NOT persist**: working directory (`cwd` always resets to the explicit `cwd` parameter if given, else the session directory), open file descriptors, background shell jobs (`jobs`), traps, and read-only variables.
## Exported API for `wait`, `proc://`, and `artifact://`

Package `shell` exposes process-wide accessors backed by `DefaultRuntime()` (and corresponding methods on `*Runtime`, `*JobManager`, `*ServiceManager`, `*ArtifactStore`):

- `DefaultRuntime() *Runtime`
- `ListJobs() []JobSnapshot`
- `GetJob(id string) (JobSnapshot, bool)`
- `CancelJob(id string) bool`
- `WaitJob(ctx context.Context, id string) (JobSnapshot, bool, error)`
- `WaitAnyJob(ctx context.Context) (JobSnapshot, bool, error)`
- `ListServices() []ServiceSnapshot`
- `GetService(name string) (ServiceSnapshot, bool)`
- `StopService(ctx context.Context, name string) (bool, error)`
- `ArtifactPath(id string) (string, bool)`

Background job completion delivery uses `sdk.Context.SendMessage("async-result", FormatAsyncResultMessage(jobID, text), true, sdk.SendMessageOptions{TriggerTurn: &true, DeliverAs: "followUp", Details: ...})`, rendering omp's `<system-notice>` template (`prompts/tools/async-result.md`).

## Tests ported

- Total Go tests: 19 test functions in `bash_test.go`, covering:
  - `bash-failure-result.test.ts`: zero exit without exit-code footer, empty output exact text (`(no output, exit code 0)` vs `(no output)\n\nCommand exited with code 3`), non-zero exit as non-throwing error result with `details.exitCode` and `Command exited with code N`, timeout warning-state result with single `[Command timed out after Ns]`, abort/cancellation notice, pipeline ending in `head`/`tail`.
  - `bash-session-state.test.ts`: variable (exported & local), function, and alias persistence across calls while `cwd` resets to the session directory, plus proof that a timed-out command does not corrupt saved session state.
  - `bash-executor.test.ts`: `isPersistentShellCdCommand` table, `cwd` honoring, timeout killing descendant child processes (verified by PID with no process left behind).
  - `bash-job-lifecycle.test.ts`: async job (`bg_N`) lifecycle, completion delivery, TERM cleanup on cancel, and `timeout: 2` auto-backgrounding with exact start notice and `<system-notice>` timeout follow-up (`[Command timed out after 2 seconds]\nWall time: X.XX seconds`).
  - `bash-interceptor.test.ts`: leading `cd` normalization check order, compound command interception, piped stdin exclusion, leading env assignment stripping, quoted/escaped/commented/redirected/unsupported syntax exclusions, unavailable tool exclusion.
  - `bash-pty-selection.test.ts`: `CanUseInteractiveBashPty` predicate (`pty=false`, `PI_NO_PTY=1`, `hasUI`).
  - Additional boundary/error tests: output truncation over spill threshold with artifact spill and `artifact://<id>` notice, auto-background promotion, named service with `ready.log` and stop cleanup, mode validation errors (`name` + `async`/`timeout`, `ready` without `name`, nonexistent/file `cwd`, timeout clamping).
## Not ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| Embedded brush-core shell & ~66 in-process builtins (`pi-shell`, `pi-builtins`) | `crates/pi-shell/`, `crates/pi-builtins/` | Rust native addon; wave 1 runs commands via the system shell (`bash`, else `sh`; Git Bash on Windows) with no cgo. |
| Persistent shell traps, open FDs, read-only vars, and shell-internal `jobs` table | `exec/bash-executor.ts`, `crates/pi-shell/` | Snapshot-file persistence (`declare -p`/`declare -f`/`alias -p`/`shopt -p`/`set +o`) preserves variables, functions, aliases, and options without a long-lived shell process, but cannot persist open FDs, traps, or read-only variables. |
| Interactive local PTY overlay (`pty: true`) | `tools/bash-interactive.ts`, `crates/pi-natives` (`PtySession`) | Requires a PTY module (not in stdlib; no new Go modules allowed in wave 1). Calls with `pty: true` emit omp's exact fallback notice and run without a terminal. |
| ACP client-bridge terminal routing | `tools/bash.ts:1275-1545` | PiG SDK has no ACP `createTerminal` bridge API. |
| `direnv` / `.envrc` preflight loading | `exec/direnv.ts`, `exec/bash-executor.ts` | Deferred from wave 1; no `direnv` settings surface wired yet. |
| Shell output minimizer (`shellMinimizer.*`) | `crates/pi-natives`, `exec/bash-executor.ts` | Rust native minimizer; deferred from wave 1. |
| Terminal graphics (Kitty/Sixel) extraction | `utils/terminal-graphics.ts` | Deferred from wave 1. |
| Internal URL virtual filesystem (`skill://`, `artifact://`, `proc://` as shell paths/cwd) | `internal-urls/url-filesystem.ts` | Depends on embedded shell filesystem hook and wave-2 internal URL router. |
| `git worktree add` rewrite & `gh` cache invalidation | `tools/bash-worktree-rewrite.ts`, `tools/gh-cache-invalidation.ts` | Depends on omp's worktree-clone CLI and GitHub cache subsystem. |
| Approval policy (`bash.patterns`, `CRITICAL_BASH_PATTERNS`) | `tools/bash.ts:79-315,510-599` | PiG's host owns tool approval; SDK `ToolDefinition` has no `approval` callback. |
| Interceptor rules using PCRE lookahead/lookbehind (`echo/cat > file`, `nohup/&`, `docker compose up`, `--watch`) | `exec/settings.ts:48-77` | Go's `regexp` (RE2) does not support lookbehind/lookahead; the 6 RE2-compatible rules (`read`, `grep`, `glob`, `edit`) are ported and disabled by default matching omp (`bashInterceptor.enabled: false`). |
| Out-of-process project daemon broker for services | `launch/client.ts`, `launch/services.ts` | Wave 1 runs named services in-process inside `ServiceManager` with `ready.log` / `ready.port` probing and `session_shutdown` cleanup. |
| Steering-signal early backgrounding | `tools/bash.ts:1222`, `async/auto-background.ts` | PiG SDK `sdk.Context` does not expose a separate `steeringSignal` channel distinct from `ctx.Done()`. |
