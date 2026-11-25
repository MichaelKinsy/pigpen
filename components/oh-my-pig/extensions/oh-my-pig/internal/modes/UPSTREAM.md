# Upstream: `internal/modes`

Follows omp release tag **`v19.1.1`** (`31ffee513f`).

## Upstream files followed

- `packages/coding-agent/src/slash-commands/builtin-registry.ts`
- `packages/coding-agent/src/slash-commands/available-commands.ts`
- `packages/coding-agent/src/slash-commands/builtin-modes.ts`
- `packages/coding-agent/src/slash-commands/builtin-lifecycle.ts`
- `packages/coding-agent/src/slash-commands/builtin-session.ts`
- `packages/coding-agent/src/slash-commands/builtin-collaboration.ts`
- `packages/coding-agent/src/slash-commands/builtin-control.ts`
- `packages/coding-agent/src/slash-commands/builtin-commit.ts`
- `packages/coding-agent/src/slash-commands/builtin-marketplace.ts`
- `packages/coding-agent/src/slash-commands/builtin-skills.ts`
- `packages/coding-agent/src/slash-commands/helpers/{context-report,usage-report,effort,parse}.ts`
- `packages/coding-agent/src/plan-mode/{state,plan-files,approved-plan,plan-autosave,plan-protection,settings}.ts`
- `packages/coding-agent/src/tools/{plan-mode-guard,resolve,shell-tokenize}.ts`
- `crates/edit/src/path_policy.rs` (`enforce_write`)
- `packages/coding-agent/src/task/structured-subagent.ts` (`PLAN_MODE_TOOLS`, `createPlanModeAgent`, `assertPlanControlsAllowed`)
- `packages/coding-agent/src/goals/{state,runtime,settings,tools/goal-tool}.ts`
- `packages/coding-agent/src/modes/{magic-keywords,loop-limit,loop-condition,turn-budget,interactive-mode}.ts`
- `packages/coding-agent/src/session/{btw-turn,btw-history,session-handoff,agent-session,model-controls}.ts`
- `packages/tui/src/prompt/{magic-keywords,markdown-prose}.ts`
- `packages/tui/src/overlays/copy-targets.ts`
- `packages/tui/src/tools/goal.ts`
- `packages/utils/src/prompt.ts` (`format` post-render pass)
- Embedded prompt templates (`internal/modes/prompts/*.md`): 16 templates copied verbatim from `packages/coding-agent/src/prompts/` (`plan-mode-*.md`, `goal-mode-active.md`, `goal-continuation.md`, `goal-budget-limit.md`, `guided-goal-interview.md`, `goal.md`, `btw-user.md`, `ultrathink-notice.md`, `chillax-notice.md`, `orchestrate-notice.md`, `workflow-notice.md`, `jevify-notice.md`) and 1 from `packages/agent/src/compaction/prompts/handoff-document.md`.

Template branch defaults:
- `plan-mode-active.md`: `proposeIsCommand` reflects live `toolcmd.CommandNames(ctx)` (true when `propose` is mounted via bash transport, false when top-level), `isHashlineEditMode=true`, `iterative=false` (parallel workflow), `askAvailable`/`taskAvailable`/`scoutAvailable` read from active tools.
- `workflow-notice.md`: `taskBatch=true`, `scoutAvailable=true`, `evalTools=true`, `py=true`, `js=true`, `freshAgents=false`, `perCallModel=false`.
- `btw-user.md`: `inject=false`.

## Tests ported

21 test functions in `modes_test.go`, including golden byte-exact comparisons against omp `v19.1.1` `prompt.render` (`testdata/plan-mode-active.golden.txt`, `testdata/plan-mode-approved.golden.txt`), upstream cases from `test/plan-mode/approved-plan.test.ts`, `test/goals/goal-runtime.test.ts`, `test/goals/goal-tool.test.ts`, `test/agent-session-goal-midrun-compaction.test.ts`, `test/loop-limit.test.ts`, `test/loop-condition.test.ts`, `test/interactive-mode-loop-provider-error.test.ts`, and `test/agent-session-magic-keywords.test.ts`, plus fake-host tests enforcing PiG's command-only call guards, `withSession` replacement, non-blocking `agent_end` compaction approval (delayed host compaction + exact cancellation matching), `ToolExposureCodemode` vs `ToolExposureDirect` host callability for mounted `propose`, `internal/gate` policy, symlink escapes and host session-ID error fail-closed, Escape abort during loop grace wait and condition execution, goal+loop simultaneous abort, Windows shell refusal without `cmd.exe`, `/force` in plan mode, `/force`/`/tools`/`/context` mounted-tool filtering (`AdvertisedTools` + `~` rows), plan mode tool save/restore preserving mounted tools and order without empty `SetActiveTools`, `/btw` sidecar storage with full tool context, and two-session `Register`-twice + `lifecycle.End` isolation.

## Settings omp reads for this feature

| Setting key | Default | Honoured | Notes |
|---|---|---|---|
| `plan.enabled` | `true` | default | Always enabled; no settings store wired yet |
| `plan.defaultOnStartup` | `false` | default | Not auto-entered on startup |
| `plan.autosave` | `false` | no | Manual Save in plan review dialog works; auto-save not wired to settings |
| `plan.autosaveDir` | `""` | no | Manual Save prompts for path |
| `goal.enabled` | `true` | default | Always enabled |
| `goal.statusInFooter` | `true` | yes | Status bar shows `GOAL used/budget` |
| `goal.continuationModes` | `["interactive"]` | default | Auto-continue active via `agent_before_settle` |
| `loop.mode` | `"prompt"` | default | Only `prompt` mode implemented (`compact`/`reset` omitted — `NewSession` is command-only in PiG) |
| `loop.conditionTimeoutMs` | `30000` | default | 30s timeout passed to `ExecWithOptions` |
| `btw.inject` | `false` | default | `btw-user.md` rendered with `inject=false` |
| `magicKeywords.enabled` | `true` | default | All 5 magic keywords enabled by default |
| `magicKeywords.<id>` | `true` | default | Individual toggles supported in `MagicKeywordSettings` |

## Feature-level omissions and deviations inside ported modes/commands (not ported / differs)

Plan mode is enforced only where a tool resolves a write target (as omp's `enforce_write`, via `internal/gate.Check`) and through `gate.PlanActive` for subagents (`internal/agents`); there is no `tool_call` hook and `checkpoint`/`rewind` are not gated (they write no files and run no processes).

| Feature | Upstream source | Why not ported / how it differs |
|---|---|---|
| Plan-mode gate on `github` `pr_create`/`pr_checkout`/`pr_push` | `internal/modes/plan.go` (`planGatePolicy`) | Deliberate tightening not present in omp: `planGatePolicy` denies mutating `github` `OpExec` ops while planning with the working-tree read-only error |
| Plan-role model switch & restore + tier slider in plan review | `plan-mode/model-transition.ts`, `interactive-mode.ts:5978-6060` | Depends on omp's role-model settings (`modelRoles.plan`) not wired to PiG |
| Plan-mode convergence forced wire `tool_choice: "required"` | `agent-session.ts:13791` | PiG SDK has no API to set wire `tool_choice` for a boundary continuation (3-reminder cap `PLAN_MODE_REMINDER_MAX = 3` is ported) |
| "Approve and execute" directly from `propose` (`agent_end`) | `interactive-mode.ts:7128-7135` | PiG restricts `ctx.NewSession` to command contexts (`command_only_calls.go:8-21`); `agent_end` dialog offers compact/keep + points to `/plan-review`, where "Approve and execute" works via `withSession` + `copyLocalArtifacts` |
| `plan.autosave` automatic save on approval | `plan-mode/plan-autosave.ts` | Settings store not wired; manual Save in plan review dialog works |
| Plan external `$EDITOR` editing and inline section annotations | `interactive-mode.ts:7011-7069` | Subprocess extensions cannot suspend PiG's terminal for `$EDITOR`; PiG's `Select`/`Input`/`Editor` dialogs are used |
| Goal `todo_context` injection | `agent-session.ts:8954-8959`, `goal-todo-context.md` | Requires cross-area read of `internal/todo` phases (integrator wiring) |
| `loop.mode` `compact` and `reset` actions | `interactive-mode.ts:3710-3714` | `ctx.NewSession` is refused outside command contexts (`agent_end` cannot reset session) |
| `/loop` condition Windows shell requirement | `modes/loop-condition.ts:100-106` | Uses Git Bash (`%ProgramFiles%\Git\bin\bash.exe` / `bash.exe` / `sh.exe`) on Windows and refuses with an error if no POSIX bash is installed (never falls back to `cmd.exe /C`) |
| `/btw` follow-ups, `<inject>` steering, copy (`c`), branch (`b`) | `modes/controllers/btw-controller.ts` | Requires omp's custom docked BTW panel & keybindings; sidecar storage and full branch context are ported |
| `/btw` sidecar location under `local://btw/` | `session/btw-history.ts:104-110` | Stored under `<local-root>/btw/` (visible under `local://` and copied by `copyLocalArtifacts` on fresh-session plan execution), whereas omp keeps sidecar history under `<artifactsDir>/btw-history/` outside `local://` |
| `/clear` in-place context reset keeping session ID | `builtin-lifecycle.ts:283` | Command description keeps omp's verbatim text for oracle parity, but PiG SDK has no in-place context reset: `/clear` calls `ctx.NewSession` (same as `/new`) |
| `/force` wire `tool_choice` override | `builtin-control.ts:60` | PiG SDK has no `setForcedToolChoice` API; injects a hidden `forced-tool-notice` in `before_agent_start` |
| `/effort` `auto` thinking level | `builtin-modes.ts:860`, `model-controls.ts:662` | PiG has no auto-thinking classifier; `ultrathink` injects notice only (never mutates pinned level) |
| `chillax` automatic thinking-level reduction | `session/model-controls.ts:65-74,730-745` | Ported `modelTakesPerMessageEffort` checks `compat.supportsPerMessageEffort` / `compat.supportsConfigurationUpdate`, which PiG's model objects do not carry; effectively notice-only in PiG |
| `/switch` temporary-only vs persisted model, fuzzy ids, `@role` aliases, `alt+p` hint | `builtin-modes.ts:460-505` | Command description keeps omp's verbatim `alt+p` text for oracle parity (PiG cycles models with `Ctrl+P`); PiG SDK exposes `SetModelNamed(spec)` only (no temporary-only flag, fuzzy matching, or `@role` resolver) |
| `/rename` automatic title generation when title omitted | `builtin-lifecycle.ts:615` | Command description keeps omp's verbatim `(omit title to generate)` text for oracle parity, but requires omp's tiny-model title generator; bare `/rename` prints that no title could be generated |
| `/dump all`, `/dump anon`, and LLM request JSON sidecar | `builtin-collaboration.ts:234-283` | PiG SDK does not expose raw provider request JSON or subagent archive writer |
| `+Nk`/`+Nk!` eval `budget` helper & `agent()` hard ceiling | `eval/agent-bridge.ts:179-183` | `modes.TurnBudgetFor(ctx)` is exported for `internal/eval` to consume; no notice text is injected (matching omp) |

## Complete Built-in Slash Command Inventory (omp `v19.1.1` vs PiG)

Total built-in slash commands in omp `v19.1.1`: **89** (plus dynamic `/skill:<name>`, already supplied by PiG's built-in `ExpandSkillCommand` when `enableSkillCommands` is true, default true).

Summary counts across the 89 built-in commands:
- **PiG's own (built-in, not registered here to avoid collision)**: **17** (`settings`, `model`, `tree`, `export`, `share`, `copy`, `session`, `changelog`, `hotkeys`, `fork`, `login`, `logout`, `new`, `compact`, `resume`, `debug`, `quit`) — `/copy <arg>` is additionally intercepted in `EventInput` for omp's `code|cmd|link|local` subcommands.
- **Ported here (`internal/modes`)**: **20** primary commands (+ 2 aliases: `/rewind`, `/q`) — `plan`, `plan-review`, `goal`, `guided-goal`, `loop`, `btw`, `context`, `usage`, `handoff`, `branch` (+ alias `rewind`), `clear`, `tools`, `effort`, `switch`, `rename`, `dump`, `jobs`, `queue`, `force`, `exit` (+ alias `q`).
- **Owned by another area**: **1** (`todo` in `internal/todo`).
- **Not ported**: **51** (including `/add-dir`, `/remove-dir`, `/dirs` — removed because PiG SDK has no multi-root workspace prompt/tool integration).

| Command (aliases) | omp behaviour (`v19.1.1`) | State here | Notes / PiG differences / why not ported |
|---|---|---|---|
| `/plan [prompt]` | Toggle plan mode (read-only exploration, `local://<slug>-plan.md`, `propose` + review) | **ported here** | Active/paused/off, `internal/gate` policy on resolved targets, `propose` special command via `toolcmd`, `Select`/`Input`/`Editor` review, aborts active turn on pause |
| `/plan-review` | Re-open plan review for latest `local://*-plan.md` | **ported here** | Runs in command context; supports all options including "Approve and execute" via `NewSession` + `withSession` + `copyLocalArtifacts` |
| `/goal [set\|show\|pause\|resume\|drop\|budget\|<objective>]` | Persistent autonomous objective with token budget, hidden `goal` tool, auto-continue | **ported here** | Full subcommands, `goal` tool, lazy usage/wall-clock tracking, `agent_before_settle` auto-continue with stall detection, Esc abort pause |
| `/guided-goal [rough objective]` | Interview user in chat then create goal | **ported here** | Activates `goal` tool and sends `guided-goal-interview.md` |
| `/loop [count\|duration] [--while\|--until '<cmd>'] [prompt]` | Repeat prompt after each turn with iteration/duration limit and shell exit-code predicate | **ported here** | Full parser, POSIX `--while`/`--until` (no `cmd.exe` fallback), Escape abort via `ctx.Signal()` during 800 ms grace wait and condition command, pauses on abort and provider error |
| `/btw [question]` | Ephemeral side question using context without disturbing turn, or browse history | **ported here** | Uses `ctx.Complete` (`toolChoice: "none"`) with full branch context; stores history in sidecar under `local/btw/` (never touches main session journal) |
| `/context` | Show context window breakdown with ASCII bars | **ported here** | Text breakdown via `GetContextUsage` + `GetSystemPrompt`/`GetAllTools`/`GetBranch`; omp interactive viewer overlay not ported |
| `/usage [show\|reset]` | Show token usage & provider quota limits | **ported here** | Session token & cost report; provider rate-limit quota bars and reset credits not exposed by PiG SDK |
| `/handoff [focus]` | Background compaction with handoff document prompt | **ported here** | Calls `ctx.Compact` with omp's `handoff-document.md` prompt |
| `/branch` (`rewind`) | Rewind to previous user message, keeping old path as a branch | **ported here** | Both `/branch` and `/rewind` registered; uses `ctx.Select` + `ctx.NavigateTree` + `SetEditorText` |
| `/clear` | Clear conversation context in place, keeping the session | **ported here (partial)** | Calls `ctx.NewSession` (PiG SDK has no in-place reset keeping the same session ID) |
| `/tools` | List active (`*`), inactive (`-`), and mounted (`~`) tools | **ported here** | Uses `toolcmd.AdvertisedTools` for `*`/`-` and appends `~ <name>` from `toolcmd.MountedNames` |
| `/effort [level]` | Set reasoning effort (`off`..`max`) or pick via selector | **ported here** | Uses `GetModelInfo`/`SetThinkingLevel`/`Select`; PiG also has `/thinking`; `auto` not in PiG |
| `/switch [model[:level]]` | Session-only model switch with fuzzy ids, `@role`, `:level` | **ported here (partial)** | Calls `SetModelNamed` + `SetThinkingLevel`; PiG has no temporary-only switch, fuzzy matching, or `@role` aliases |
| `/rename [title]` | Rename current session (omit title to generate) | **ported here (partial)** | Calls `ctx.SetSessionName`; automatic title generation omitted; PiG also has `/name` |
| `/dump [all\|anon]` | Copy session transcript to clipboard (and write LLM request JSON to tmp) | **ported here (partial)** | Plain transcript copy via `ctx.CopyToClipboard`; `all`/`anon` zip archives and LLM request JSON sidecar not ported |
| `/jobs [full\|kill <id>\|kill all]` | List or cancel async background jobs | **ported here** | Reads and cancels jobs in `internal/jobs`; omp live sheet overlay not ported |
| `/queue <message>` | Queue follow-up message behind active turn | **ported here** | Calls `ctx.SendUserMessage(msg, "followUp")` |
| `/force <tool> [prompt]` (`/force:`) | Force next turn to use a specific tool | **ported here (partial)** | Injects hidden `forced-tool-notice` in `before_agent_start` (PiG SDK has no wire `tool_choice` override) |
| `/exit` (`/q`) | Exit application | **ported here** | Calls `ctx.Shutdown()`; PiG only has `/quit` without `/exit` or `/q` aliases |
| `/todo [...]` | View/modify agent todo list | **owned by `internal/todo`** | Registered by `internal/todo` |
| `/settings` | Open omp settings menu | **PiG's own (differs)** | PiG builtin opens PiG's settings menu (not omp's settings schema) |
| `/model` (`models`) | Open model role/thinking selector | **PiG's own (differs)** | PiG builtin `/model [provider/model]` picks/switches model without omp's role-model hub; `models` alias absent in PiG |
| `/tree` | Navigate session tree | **PiG's own (same)** | PiG builtin `/tree` navigates branches |
| `/export [--themes] [path]` | Export session to HTML file | **PiG's own (differs)** | PiG builtin `/export [path]` exports `.html` or `.jsonl`; omp's `--themes` flag not in PiG |
| `/share` | Share session via encrypted server/gist | **PiG's own (differs)** | PiG builtin `/share` uploads to PiG's expiring gateway (not omp's encrypted share server/gist) |
| `/copy [code\|cmd\|link\|local]` | Pick text/code/cmd/link/local to copy | **PiG's own + extended here** | Bare `/copy` copies last assistant message via PiG builtin (no interactive picker); `/copy code\|cmd\|link\|local` intercepted in `EventInput` here |
| `/session [info\|delete\|pin]` | Session info, delete, and OAuth account pinning | **PiG's own (differs)** | PiG builtin `/session` takes no arguments and prints stats only; `delete` and `pin` subcommands not available (colliding builtin) |
| `/changelog [full\|last [N]]` | Show changelog entries | **PiG's own (differs)** | PiG builtin `/changelog` shows PiG's changelog without `full`/`last [N]` subcommands |
| `/hotkeys` | Show keyboard shortcuts | **PiG's own (differs)** | PiG builtin `/hotkeys` shows PiG's keybindings |
| `/fork [pane\|window\|tab]` | Fork session (or into multiplexer/Tern pane) | **PiG's own (differs)** | PiG builtin `/fork` forks from a chosen user message in-place (no `pane\|window\|tab` multiplexer or Tern split) |
| `/login [provider\|url]` | OAuth login + manual redirect URL paste | **PiG's own (differs)** | PiG builtin `/login [provider]` runs PiG OAuth login (no `/login <redirect-url>` manual callback paste) |
| `/logout [provider]` | Provider OAuth logout | **PiG's own (differs)** | PiG builtin `/logout` takes no provider argument and opens a selector |
| `/new` | Start a new session | **PiG's own (same)** | PiG builtin `/new` starts a fresh session |
| `/compact [mode] [focus]` | Compact context (`soft`/`remote`/`snapcompact`/`handoff`) | **PiG's own (differs)** | PiG builtin `/compact [instructions]` has no `soft`/`remote`/`snapcompact`/`handoff` mode subcommands (use `/handoff` here for handoff) |
| `/resume [id\|@claude\|@codex]` | Resume session or import foreign session | **PiG's own (differs)** | PiG builtin `/resume` opens PiG's session picker (no direct `<id>` arg or `@claude`/`@codex` foreign import) |
| `/debug` | Open debug tools selector | **PiG's own (differs)** | PiG builtin `/debug` writes a debug log file (no interactive debug selector) |
| `/quit` (`q`) | Quit application | **PiG's own (same)** | PiG builtin `/quit`; `/q` alias registered here |
| `/skill:<name>` | Expand and invoke a discovered skill | **PiG's own (same)** | PiG natively expands `/skill:<name>` via `ExpandSkillCommand` |
| `/add-dir <path>` | Add workspace directory to session & rebuild prompt | **not ported** | PiG SDK has no multi-root workspace API; tools and prompt builder only consume single `ctx.Cwd()` |
| `/remove-dir <path>` | Remove workspace directory from session | **not ported** | Same as `/add-dir`: no multi-root workspace support in PiG SDK |
| `/dirs` | List session workspace directories | **not ported** | Same as `/add-dir`: no multi-root workspace support in PiG SDK |
| `/vibe [prompt]` | Persistent fast/good worker director mode | **not ported** | Requires omp's `VibeSessionRegistry` and 5 ephemeral `vibe_*` worker tools |
| `/advisor [on\|off\|status\|dump\|configure]` | Second-model turn advisor | **not ported** | Requires omp's background advisor runtime and `advise` tool |
| `/prewalk [into\|restart\|off]` | One-shot model handoff at next edit/write | **not ported** | Requires omp's prewalk classifier, model roles, and todo-gated mid-turn model swap |
| `/fast [on\|ultra\|off\|status]` | Toggle provider priority/ultrafast service tier | **not ported** | PiG SDK has no API to set provider `service_tier` / `speed` |
| `/slow [on\|off\|status]` | Toggle provider flex/low-priority service tier | **not ported** | PiG SDK has no API to set provider `service_tier` / low-priority fallback |
| `/extended-context [on\|off\|status]` | Toggle extended context window tier | **not ported** | PiG SDK has no API to toggle provider extended-context beta flags |
| `/skillful [on\|off\|status]` | Toggle listing skills in system prompt | **not ported** | Prompt skill listing is owned by `internal/prompt` / PiG settings |
| `/computer [on\|off\|status]` | Toggle computer-use eval prelude | **not ported** | Computer-use desktop control prelude not ported |
| `/ratchet [flow and goal]` | Eval hillclimbing kickoff | **not ported** | Requires omp's `ratchet` eval prelude |
| `/modelpreset [list\|save\|switch\|delete]` | Save/switch role-model presets | **not ported** | Depends on omp's role-model settings schema (`internal/settings`) |
| `/setup` (`providers`) | Interactive provider setup wizard | **not ported** | Custom TUI wizard; PiG uses `/login` and `pig auth` |
| `/security <subcommand>` | Native security scan planner/runner | **not ported** | Requires omp's `security_scan` backend and SARIF/lineage store |
| `/trace` | Open session trace in stats dashboard | **not ported** | Requires omp's `@oh-my-pi/omp-stats` HTTP server + SQLite store |
| `/stats [--port\|--host]` | Launch local stats dashboard | **not ported** | Requires omp's `@oh-my-pi/omp-stats` server |
| `/collab [start\|view\|list\|stop\|status]` | Live session sharing via relay | **not ported** | Requires omp's collab relay protocol and host/guest runtime |
| `/join <link>` | Join shared collab session | **not ported** | Requires omp's collab guest runtime |
| `/leave` | Leave collab session | **not ported** | Requires omp's collab runtime |
| `/browser [headless\|visible]` | Toggle browser eval-prelude mode | **not ported** | Owned by `internal/browser` / `internal/ternbrowser` |
| `/open [link]` | Open last conversation link in OS browser | **not ported** | No cross-platform browser opener contract in PiG SDK; `/copy link` is ported |
| `/commit [instructions]` | Selective commit of agent-made changes | **not ported** | Requires omp's `eval` `commit` prelude and selective change journal |
| `/live` | Realtime Codex voice mode | **not ported** | Requires WebRTC audio + Codex realtime backend |
| `/record` | Screen recording (`omp play`) | **not ported** | Requires omp's TUI frame recorder |
| `/pause` | Freeze all agents until resumed | **not ported** | Requires omp's global `agentPauseGate` + fullscreen pause overlay |
| `/forget-approvals` | Clear remembered file/folder approvals | **not ported** | Requires omp's `approval-cache` subsystem |
| `/ssh <subcommand>` | Manage SSH hosts | **not ported** | Requires omp's SSH config/protocol backend (`ssh://`) |
| `/fresh` | Reset provider stream state without clearing transcript | **not ported** | PiG SDK has no API to prune provider stream state |
| `/delete` | Delete current session file and start a new one | **not ported** | PiG SDK has no `dropSession` API to unlink the active session file safely |
| `/shake [elide\|images\|thinking]` | Drop heavy content from context in place | **not ported** | PiG SDK has no API to rewrite past context entries outside compaction |
| `/pin [session id]` | Pin session at top of resume list | **not ported** | PiG's `/resume` picker does not read omp's session-pins store |
| `/unpin [session id]` | Unpin session from resume list | **not ported** | PiG's `/resume` picker does not read omp's session-pins store |
| `/tan <work>` | Run background tangential agent | **not ported** | Requires omp's tan controller and subagent runner |
| `/omfg <complaint>` | Forge TTSR rule from complaint | **not ported** | Requires omp's TTSR rule engine |
| `/cleanse [request] [--all]` | Fix diagnostics with parallel subagents | **not ported** | Requires omp's cleanse multi-agent loop |
| `/retry` | Retry last failed agent turn | **not ported** | PiG SDK has no `session.retry()` API |
| `/memory <subcommand>` | Inspect/maintain memory backends | **not ported** | Requires omp's memory/hindsight backends |
| `/move [<path>]` | Move session to another directory | **not ported** | PiG SDK cannot change host process cwd or relocate session files |
| `/wt` (`worktree`) `[<branch>]` | Move session into new git worktree | **not ported** | Requires `/move` host cwd relocation |
| `/restart` | Restart binary with same launch flags | **not ported** | Extension cannot exec-replace the PiG host binary |
| `/marketplace <subcommand>` | Manage omp marketplace plugins | **not ported** | PiG uses `pig install` / `pig config` packages instead of omp marketplace |
| `/plugins` (`plugin`) `[list\|enable\|disable]` | Manage omp npm/marketplace plugins | **not ported** | PiG uses `pig list` / `pig config` |
| `/reload-plugins` | Reload omp plugins | **not ported** | PiG uses built-in `/reload` |
| `/skills <subcommand>` | Search/install from `skills.omp.sh` | **not ported** | Requires omp's `skillshare` registry client |
| `/extensions` (`status`) | Open Extension Control Center overlay | **not ported** | Custom omp dashboard overlay; PiG has `pig status` / `pig config` |
| `/agents` | Open Agents Hub overlay | **not ported** | Custom omp overlay; depends on subagent model-role/advisor/prewalk config |
| `/git [revision]` | Open split-diff Git TUI overlay | **not ported** | Custom fullscreen Git TUI overlay |
| `/local [name]` | Open `local://` file in `$EDITOR` | **not ported** | Subprocess extensions cannot suspend PiG's terminal to run an interactive `$EDITOR` |
| `/hub` (`subagents`) | Open live Agent Hub overlay | **not ported** | Custom omp overlay; depends on omp's live subagent observer registry |
| `/mcp <subcommand>` | Manage MCP servers | **not ported** | PiG manages MCP via `pig mcp` and built-in `mcp` extension |
