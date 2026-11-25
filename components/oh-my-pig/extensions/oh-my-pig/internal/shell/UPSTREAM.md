# Upstream reference: `internal/shell`

Port of omp's `bash` tool and `proc://` internal URL protocol (https://github.com/can1357/oh-my-pi, MIT, tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed

- `packages/coding-agent/src/tools/bash.ts`
- `packages/coding-agent/src/tools/bash-interceptor.ts`
- `packages/coding-agent/src/tools/shell-tokenize.ts`
- `packages/coding-agent/src/tools/path-utils.ts` (`resolveToCwd`, `resolveShellDirectory`)
- `packages/coding-agent/src/tools/tool-timeouts.ts`
- `packages/coding-agent/src/tools/output-meta.ts`
- `packages/coding-agent/src/prompts/tools/bash.md` (see "Changed on purpose" below)
- `packages/coding-agent/src/prompts/tools/async-result.md`
- `packages/coding-agent/src/prompts/tools/async-notify.md`
- `packages/coding-agent/src/prompts/session/launch-completion.md`
- `packages/coding-agent/src/prompts/internal-urls/proc.md`
- `packages/coding-agent/src/prompts/internal-urls/proc-reference.md`
- `packages/coding-agent/src/internal-urls/proc-protocol.ts`
- `packages/coding-agent/src/exec/bash-executor.ts`
- `packages/coding-agent/src/exec/non-interactive-env.ts`
- `packages/coding-agent/src/async/job-manager.ts`
- `packages/coding-agent/src/async/auto-background.ts`
- `packages/coding-agent/src/async/notify.ts`
- `packages/coding-agent/src/async/job-control.ts`
- `packages/coding-agent/src/launch/services.ts`
- `packages/coding-agent/src/session/async-job-delivery.ts`
- `packages/coding-agent/src/session/async-notify.ts`
- `packages/coding-agent/src/session/launch-completion.ts`
- `packages/coding-agent/src/session/harness-tags.ts`
- `packages/tui/src/tools/bash.ts`
- `packages/tui/src/tools/output-meta.ts`
- `packages/tui/src/tools/streaming-output.ts`
- `packages/tui/src/tools/proc-render.ts`
- `packages/tui/src/tools/daemon.ts`
- `packages/tui/src/tools/wait.ts`
- `packages/tui/src/tools/job-label.ts`
- `packages/utils/src/sanitize-text.ts`
- `packages/utils/src/spill-url.ts`
- `packages/utils/src/format.ts`

## Changed on purpose (system-shell port)

- `bash.md`:
  - Replaced `"Persistent bash 5.3."` with `"Persistent bash."` because commands run via the host's `bash` binary (which may be macOS `/bin/bash` 3.2 or Homebrew/Linux bash 5.x).
  - Removed `"Internal URIs work as paths for builtins/coreutils, redirects, globs."` and `"In-process builtins, ahead of PATH: …"` because commands run in the system shell rather than omp's embedded Rust shell (`omp-shell`).
  - Removed `` `; `pty` = interactive` `` for finite commands because there is no interactive terminal-emulator overlay forwarding user keystrokes yet; finite commands with `pty: true` emit omp's fallback notice and run without a terminal, while named services still get a real Unix PTY (`pty_darwin.go`, `pty_linux.go`).
- `proc.md` & `proc_reference.md`: removed the `proc://<id>/streams` promise from `proc.md` and marked `proc://<id>/streams` unavailable in `proc_reference.md` because live intermediate `|` pipe and redirected-file tapping cannot be ported faithfully onto system `bash` without changing shell semantics (exit status / `PIPESTATUS` / `pipefail`, stdio buffering, SIGPIPE propagation, process-substitution subshell lifetime, and arbitrary shell grammar).
- `proc://<id>/mode`: rejects `persist` and `detached` with an explicit unsupported error instead of claiming persistence, because services run in-process without an out-of-process daemon and are reaped when the session ends.
- Incomplete or oversized shell-state capture notices: when a requested state capture finishes incomplete after a normal exit (e.g. `exec`, overridden `EXIT` trap, redirected/closed state FD, or exceeding the 64 MiB pipe capture budget), the result appends a bracketed notice `[Shell state changes of this call were not saved (...); earlier session state was kept.]` instead of dropping silently; individual entries over 4 MiB are skipped with `[Skipped persisting oversized shell entries (>4.0MB): ...]` while keeping smaller changes in the same call.
- macOS `/bin/bash` 3.2 `declare -p` control-byte quirk: bash 3.2's own `declare -p` builtin prepends an extra `0x01` byte when quoting values containing literal `0x01` or `0x7f`; bash 5.x round-trips these bytes verbatim.
## Shell binary selection per platform

- **macOS (`darwin`) / Linux (`linux`) / other Unix (`!windows`)**: `exec.LookPath("bash")` first (so modern bash 5.x from Homebrew/Nix is preferred over macOS `/bin/bash` 3.2), then `/bin/bash`, then `sh` (`-c`). When the fallback resolves to non-bash `sh`, the bash-only state/preamble wrapper is skipped with a clear notice so dash/busybox ash do not fail with syntax errors.
- **Windows (`windows`)**: Git Bash at `%ProgramFiles%\Git\bin\bash.exe` or `%ProgramFiles(x86)%\Git\bin\bash.exe`, then `exec.LookPath("bash.exe")` (`-c`).

## Per-session isolation, persistence & integrator hooks

- Every piece of state (jobs, services, shell snapshots, spawned process groups, fallback spill artifacts, delivery contexts) is strictly keyed by `sessionID`.
- `Register` installs `lifecycle.OnEnd("shell", defaultRuntime.DisposeSession)` (reaching the shared package-level runtime regardless of how many extension instances `Register` created) and `OnSessionShutdown` calls `lifecycle.End(sessionID)` only for the ending session, never disposing the shared runtime or affecting sibling/child sessions.
- Shell snapshots stream nonce-framed baseline and post-run dumps over an in-memory Unix pipe (`ExtraFiles[0]` → random per-call high FD in `[30, 189]` guarded by `/dev/fd/<fd>` pipe checks so `exec <fd>>file` cannot capture the post dump or inherited env). On Windows (`statePipeSupported == false`, where `ExtraFiles` is unsupported and cannot be executed on macOS CI; verified structurally and under POSIX bash via `TestPass3_T1_WindowsFallbackNeverWritesInheritedEnvToDisk`), the wrapper keeps the baseline strictly in memory (`__omp_win_base`) and diffs BASE vs POST via `awk` inside the EXIT trap so ONLY changed/added/removed records touch disk (never writing inherited environment secrets to disk). Structured Go-side diffing (`computeShellDelta`) round-trips multi-line variables/aliases/functions verbatim on both macOS `/bin/bash` 3.2 and bash 5.x, ignores self-changing vars (`BASHPID`, `EPOCHREALTIME`, `EPOCHSECONDS`, `RANDOM`, `SRANDOM`, `SECONDS`, `LINENO`, `BASH_*`, `_`), persists `IFS`, `PS4`, `unset`, `unset -f`, `unalias`, `allexport` (`set -a`/`set +a`), `set -e`, `pipefail`, `set -x` (suppressing traces around wrapper lines), and `shopt` options, merges only per-call deltas for overlapping runs, survives `exec`, user `EXIT` traps, `IFS=,`, and `set -o noclobber`, drops exited PGIDs immediately on command exit, and sweeps stale `omp-shell-state-<deadpid>-*` directories.
- `LocalSpillRoot func(ctx sdk.Context) (string, error)` and `WriteSessionSpill(ctx sdk.Context, data []byte) (string, error)`: wired to `urls.LocalRoot` and `jobs.SpillFunc` so bash spills and async result spills share one per-session 0-based allocator that scans existing `<N>.log` files on first use and creates files exclusively (`O_WRONLY|O_CREATE|O_EXCL`, never overwriting on session resume); `SpillWriter` remains as a fallback and `ArtifactStore` defaults to a per-session temp directory with omp's 16 MiB (3 MiB head + rolling tail) cap and `local://<N>.log` notice.
- `SessionSetting func(ctx sdk.Context, key string) any` (alongside operator-layer `Setting`): evaluates omp's two-view `bash.patterns` + `bash.allowedEnvVars` policy (`tools/bash.ts:846-921`) where operator layers cannot be loosened by project layers and project deny/prompt rules tighten operator allow rules.
- `KillAgentHook func(sessionID, agentID string) (status string, message string)`: wired to `agents.KillAgent` so `proc://<id>/kill` kills the agent registration, emits omp's cancel + `## Completed (1)` report, and marks the job consumed.
- `CommandPreamble func(ctx sdk.Context) (preamble string, release func())`, `DirectCommand func(ctx sdk.Context, command string, cwd string, timeout time.Duration) (output string, exitCode int, timedOut bool, cancelled bool, handled bool)`, `InvokedCommand func(ctx sdk.Context, command string) (name string, ok bool)`, `CommandNames func(ctx sdk.Context) []string`, `ToolCommandDispatches func(ctx sdk.Context) []map[string]any`, and `PrepareLoadout sdk.ToolPrepareLoadoutFunc`: wired by `internal/toolcmd`. `release()` is called once the sync call, background job, or service finishes; `InvokedCommand` (with `CommandNames` fallback) refuses `pty: true` pre-execution before `DirectCommand` or spawn with omp's exact text (`tools/bash.ts:1899-1904`); `DirectCommand` distinguishes timeouts from cancellations; `ToolCommandDispatches` attaches `details.toolCommands` (and `details.toolCommandsSole`) when wired; `PrepareLoadout` lets `bash` hide mounted tool declarations from the model while keeping them directly callable.

## Approval patterns (`bash.patterns` & `CRITICAL_BASH_PATTERNS`): tripwire, not a sandbox

`EvaluateBashToolDecision` (registered into `internal/approval` via `init()`) faithfully ports omp's `BashTool.approval` (`tools/bash.ts:205-448, 834-894`):
- Evaluates two-view operator (`Setting`) + session (`SessionSetting`) `bash.patterns` and `bash.allowedEnvVars`, `CRITICAL_BASH_PATTERNS`, and bare mounted tool-command lines (`read` tier), delegating policy resolution (`resolvePolicy`), `denyError` (`Tool "bash" is blocked by tool policy.\nReason: ...` vs user policy), `sessionApprovalScope`, and the single `EventToolCall` veto to `internal/approval` (`internal/approval/UPSTREAM.md`).
- `bash.patterns` matching is a **tripwire, not a sandbox** (matching omp upstream). Deny/prompt rules hold against quoting, leading `NAME=value`, `;`, `&&`, `|`, newline chains, `( )` subshells, and here-doc bodies, and share upstream's known bypasses:
  - Wrapper command words in front (`env`, `command`, `sudo`, `time`, `!`, `exec`, `nohup`)
  - Absolute binary paths (e.g. `/usr/bin/git push`)
  - Options placed before a subcommand (e.g. `git -C . push`)
  - Compound shell grammar (`{ ...; }`, `if ...; then ...; fi`)
  - Nested shells and indirection (`bash -c`, `sh -c`, `eval`, `$(...)`, backticks, variables as command names, here-strings)
  - `CRITICAL_BASH_PATTERNS` raw-text regex limitations (`rm -rf "/"`, `rm -rf ~`, `rm -rf $HOME`, `curl x | sudo bash`)
- `Execute` itself does not re-check `bash.patterns`; enforcement runs in `internal/approval`'s `EventToolCall` hook (matching omp's wrapper gate).

## Settings read by omp

| Key | Default | Honoured |
|---|---|---|
| `bash.enabled` | `true` | Always registered by `Register` |
| `tools.approvalMode` | `"yolo"` | Yes via `Setting` (`yolo`, `write`, `always-ask`, `always-ask-cached` + 3-choice `Approve bash for this session` grant keyed by strict session ID and `bash\x00<tier>`) |
| `tools.approval` (`tools.approval.bash`) | `{}` | Yes via `Setting` (`allow` / `deny` / `prompt` user policy for `bash`) |
| `bash.patterns` / `bash.allowedEnvVars` | `[]` / `[]` | Yes via two-view `Setting` (operator authority) + `SessionSetting` (session view) + `EventToolCall` veto (`deny` / `prompt` / `allow` + `CRITICAL_BASH_PATTERNS`) |
| `bash.allowCompoundCommands` | `false` | No (see Not ported) |
| `bash.autoBackground.enabled` | `true` | Yes (`Setting` / `Config.AutoBackgroundEnabled`) |
| `bash.autoBackground.thresholdMs` | `60000` | Yes (`Setting` / `Config.AutoBackgroundThreshold`) |
| `async.enabled` | `true` | Yes (`Setting` / `Config.AsyncEnabled`) |
| `launch.enabled` | `true` | Yes (`Setting` / `Config.LaunchEnabled`) |
| `launch.persist` | `false` | Session-only (no out-of-process daemon) |
| `tools.maxTimeout` | `0` (seconds; `0` = no global cap, 3600s bash ceiling) | Yes (`Setting` / `Config.MaxTimeoutSec`) |
| `tools.artifactSpillThreshold` | `50` (KB → `* 1024` = 51,200 B; inline cap adds 2 KiB notice slack) | Yes (`Setting` / `Config.SpillThreshold`) |
| `tools.artifactHeadBytes` | `20` (KB → `* 1024` = 20,480 B; `0` disables middle elision) | Yes (`Setting` / `Config.HeadBytes`) |
| `tools.artifactTailBytes` | `20` (KB → `* 1024` = 20,480 B; caps tail when smaller than `spill - head`) | Yes (`Setting` / `Config.TailBytes`) |
| `tools.outputMaxColumns` | `768` (bytes/line; `0` disables) | Yes (`Setting` / `Config.MaxColumns`) |
| `bashInterceptor.enabled` | `false` | Yes (`Setting` + `SessionSetting` / `Config.InterceptorEnabled`; evaluated in `EvaluateBashToolDecision` before any prompt and in `Execute`) |
| `bashInterceptor.patterns` | 10 rules | Yes — all 10 upstream `DEFAULT_BASH_INTERCEPTOR_RULES` ported (the 4 JS lookaround rules implemented as exact JS-regex-equivalent Go matchers, differentially verified on 200,000 generated lines with 0 differences), plus user-rule `flags` (`i`/`m`/`s`) and once-per-session warning when a user pattern cannot compile under Go RE2 (see Not ported) |
| `bash.direnv` / `bash.direnvLoadTimeoutMs` | `"auto"` / `10000` | No (see Not ported) |
| `bash.gitGuard` | `false` | No (see Not ported) |
| `shellMinimizer.*` | enabled | No (see Not ported) |
| `display.shellVisualization` | `true` | No (see Not ported) |
| `worktree.clone` | `false` | No (see Not ported) |

## Tests ported

- `internal/shell`: 69 test functions across `bash_test.go`, `proc_delivery_test.go`, `review_fixes_test.go`, `pass2_fixes_test.go`, `pass3_fixes_test.go`, `round3_fixes_test.go`, and `round4_fixes_test.go` covering `bash-failure-result.test.ts`, `bash-session-state.test.ts`, `bash-cd-root.test.ts`, `bash-job-lifecycle.test.ts`, `bash-interceptor.test.ts`, `bash-tool-commands.test.ts`, `tool-command-approval-gate.test.ts` (`lexShellCommand` / `isToolCommandLine` parity including quoted `$` / mixed chains), `proc-service.test.ts`, `async-notify.test.ts`, `agent-session-async-delivery.test.ts`, `approval.test.ts`, `shell-tokenize.test.ts`, live settings read at call time, foreground job id return on release (`#discardForegroundJob`), `Tool "bash" is blocked by tool policy.\nReason: ` deny prefix, ellipsis-inside-byte-cap single/multi-chunk column truncation, omission of `meta.limits.columnTruncated.selectorBase`, `touchesNoFiles: true` on `proc://` kill writes, finished/cancelled `in <duration>` formatting, and `intent.For` propagation onto async bash jobs.
## Not ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| Embedded bash 5.3 VM (`omp-shell`) & ~95 in-process coreutils/extras (`omp-builtins`) | `crates/shell/`, `crates/builtins/` | Rust native addon; this Go port runs commands through the host system `bash` with no cgo. |
| User shell rc snapshot (`~/.bashrc`/`~/.zshrc` aliases and functions preloaded once) | `utils/shell-snapshot.ts`, `exec/bash-executor.ts:771` | Non-interactive `bash -c` does not source interactive rc files; capturing and replaying an interactive login shell rc snapshot is not implemented because host rc scripts can hang or mutate terminal state. |
| Live pipe/redirection stream tapping (`proc://<id>/streams[/<n>]` byte capture) | `crates/shell/src/taps.rs`, `crates/shell/src/bash/vm/{exec,redir}.rs`, `internal-urls/proc-protocol.ts:133-178`, `job-widgets/pipe-source.ts:68-85` | **Not portable without omp's embedded shell VM (`omp-shell`).** Streams are **always empty** for system-`bash` jobs in this port (`proc://<id>/streams` returns the empty-streams message and `proc://<id>/streams/<n>` returns `Stream not found` unless a synthetic `jobs.StreamProvider` is injected in tests). Rewriting `a \| b` into `a \| tee >(cat > ...) \| b` in real `bash` breaks shell semantics: shifts `PIPESTATUS` indices and `set -o pipefail` outcomes (extra `tee` stage), breaks SIGPIPE propagation to `a` when `b` exits early (`tee` buffers/ignores or changes write failure timing), alters stdout buffering (pipe-buffer sizing and async `>(...)` process-substitution drain races on exit), cannot observe runtime-expanded redirections (`> "$f"`, `exec 3>file`, loops/functions/`eval`), and fails to parse arbitrary POSIX/bash grammar without a full bash AST; `proc.md` and `proc_reference.md` therefore do not advertise live streams to the model. Because piped bash jobs have no tapped `PipeTextSources` (`pipe:<n>`), fallback widget `lines` counts only stdout+stderr lines (`outputTap`) rather than summing `outputTap` + each tapped pipe stream (`job-widgets/runtime.ts:378-382`, `tools/bash.ts:1743`). |
| Shell execution visualization traces & step streams (`details.trace`, `details.steps`, `ShellTrace`, `shell-viz`/`shell-steps`) | `crates/shell/` (`RunTrace`), `packages/tui/src/tools/shell-{steps,trace,viz}.ts` | Produced by the Rust embedded shell VM (`omp-shell`); system-shell execution does not emit AST/step traces and therefore omits `details.trace` and `details.steps`. |
| Interactive TUI PTY overlay for finite commands (`ui.custom` xterm + input-wait notifications) | `tools/bash-interactive.ts`, `packages/tui/src/tools/terminal-output.ts` | Finite `pty: true` calls fall back with omp's notice until a frontend forwards keystrokes; named services use pure-Go Unix PTYs (`pty_darwin.go`, `pty_linux.go`) and Windows services fall back to pipes (`pty_other.go`). |
| Out-of-process `omp` project daemon broker (`persist`/`detached` modes surviving process exit, cross-session service sharing) | `launch/services.ts`, `daemon/` | Named services run in-process per session in `ServiceManager` with `ready.log`, `ready.port`, `notify`, stdin, replace-on-restart, and `launch-completion` exit notices; `persist`/`detached` modes are rejected with a clear error because there is no external daemon process. |
| `proc://` read & kill `details` field differences (daemon & job snapshots) | `internal-urls/proc-protocol.ts`, `async/job-control.ts:224-250,490-497`, `packages/tui/src/tools/daemon.ts` | Port daemon snapshots use the service name as `id` (omp uses a broker UUID), omit broker-only fields (`command`, `cwd`, `generation`, `leases`, `outputBytes`, `owner`, `readyMatch`, `restartCount`, `scope`), use `policy.hold: "leases"` instead of `"persist"`, and include renderer convenience fields (`name`, `pid`, `persist`, `detached`); port job snapshots emit omp's deterministic fallback `widget` (`state: "fallback"` with `title`, `stage`, `blocks`, `updatedAt`) and match omp on `agentUrlId`, `resolvedModel`, `resolvedModelIdentity`, `resolvedThinkingLevel`, `proc.meta`, omitting `settledAtMs` on newly cancelled snapshots, and omitting `resultText`/`errorText`/`meta` once consumed; `proc://` directory reads carry `isDirectory: true` and omit `meta.pagedSource`. All fields read by omp's renderer (`key`, `state`, `pids`, `startedAt`, `exitedAt`, `policy.hold`) are present, and `proc://` write/kill results set `touchesNoFiles: true`. |
| AI-authored live job widget specs (`state: "live"` / `"authoring"`, cheap-model spec authoring & drift re-authoring) | `packages/coding-agent/src/job-widgets/{author,caption,registry}.ts` | Deterministic fallback views (`state: "fallback"` with `title`, `stage`, `stat` block `lines`/`lines/s`, and `updatedAt`) are ported in `internal/jobs/widget.go` and attached to `wait`, `proc://` read, and `proc://<id>/kill` job snapshots; background cheap-model spec authoring (`state: "live"` / `"authoring"`) and LLM command captioning are not ported. |
| Shell output minimizer (`shellMinimizer.*`, `details.meta.minimized`) and Spotlight shaping | `crates/shell/src/minimizer/`, `spotlight/` | Rust native command-specific filter table (~40k LOC) and Spotlight experiment harness; `details.meta.minimized` is omitted because outputs are not minimized. |
| Internal URL virtual filesystem (`skill://`, `local://`, `proc://` inside shell redirections/globs/cwd) | `internal-urls/url-filesystem.ts` | Depends on embedded shell VFS hooks; `proc://` itself works via the `internal/urls` router (`read`/`write`). |
| Persistent shell traps, open FDs, read-only vars, shell-internal `jobs` table, and `FORK_JOBS_NOTICE` | `exec/bash-executor.ts`, `tools/bash.ts:621` | Snapshot-file persistence preserves new/changed variables, functions, aliases, `shopt`, and `set -o` options (including `set -e` and `pipefail`) and merges overlapping deltas, but cannot serialize open FDs, traps, read-only variables, or live shell-internal job tables. |
| ACP client-bridge terminal routing | `tools/bash.ts:2215-2495` | PiG SDK has no ACP `createTerminal` bridge API. |
| `direnv` / `.envrc` preflight loading | `exec/direnv.ts`, `exec/bash-executor.ts` | Requires `direnv` subprocess preflight + `bash.direnv` settings integration not wired in this area. |
| `bash.gitGuard`, `git worktree add` rewrite, `apply_patch` shell shim, `gh` cache invalidation | `crates/shell/src/git/`, `tools/bash-{worktree-rewrite,apply-patch}.ts`, `tools/gh-cache-invalidation.ts` | Depend on embedded `git` builtin layer, omp's `worktree-clone` CLI, Codex `apply_patch` tool, and GitHub cache subsystem. |
| Compound `&&` per-segment allow (`bash.allowCompoundCommands`) | `tools/bash.ts:847-850,924-992` | `CRITICAL_BASH_PATTERNS` and two-view operator+session `bash.patterns` + `bash.allowedEnvVars` (`deny`/`prompt`/`allow`, including compound `deny`/`prompt`) are enforced via `EventToolCall` + `Context.Select`; per-segment `&&` allow (`bash.allowCompoundCommands`) is not ported. |
| Terminal graphics (Kitty/Sixel) extraction & image links in results | `utils/terminal-graphics.ts`, `utils/image-temp.ts` | No raw ANSI image decoder or image temp store in `internal/shell`. |
| Steering-signal early backgrounding & service readiness steer interrupt | `tools/bash.ts:2140-2173`, `launch/services.ts:406-424` | PiG SDK `sdk.Context` does not expose a separate `steeringSignal` channel distinct from `ctx.Done()`. |
| Process exit kill-on-drop on ungraceful SIGKILL / print-mode SIGINT without `session_shutdown` | `exec/bash-executor.ts:436-441` | Go extensions cannot run cleanup after SIGKILL; `lifecycle.OnEnd` + `OnSessionShutdown` reap all jobs, services, and `&` process groups concurrently on normal/interactive shutdown and child-session close. |
| User `bashInterceptor.patterns` rules using JS-only regex syntax (lookaround, backreferences, sticky `y` flag) | `tools/bash-interceptor.ts:23-34` | Go's `regexp` (RE2) does not support arbitrary JS lookaround/backreferences/sticky flags. All 4 lookaround rules in `DEFAULT_BASH_INTERCEPTOR_RULES` are matched by exact Go ports, and user rules honour JS `flags` (`i`, `m`, `s`); any custom user rule RE2 cannot compile notifies the user once per session via `ctx.Notify` instead of being silently dropped. |
| Windows graceful `SIGTERM` trap grace on cancel/timeout | `crates/shell/src/proctree.rs:846-888`, `internal/shell/proc_windows.go` | Windows has no POSIX `SIGTERM`; spawned shells and services are assigned to a Windows Job Object (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) via stdlib `syscall` lazy DLL calls so `&`-backgrounded descendants remain tracked after `bash.exe` exits and are terminated on cancel, timeout, or session shutdown/reload, but termination is immediate (`TerminateJobObject`) without a POSIX `SIGTERM` cleanup-trap grace window. |
