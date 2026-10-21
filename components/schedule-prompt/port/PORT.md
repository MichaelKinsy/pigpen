# Port record: pi-schedule-prompt

| Input | Identity |
|---|---|
| Original | `pi-schedule-prompt` 0.4.1 ("Pi's Heartbeat") by tintinweb, https://github.com/tintinweb/pi-schedule-prompt, commit `5556775276202c26654ff9323541fe6983f6ee38`, MIT |
| Oracle | Pi `1402973a` (1.3.0), Node 24.19.0, with the unmodified package at `port/oracle` (dependencies `croner` 10.0.1 and `nanoid` 5 from npm; `@earendil-works/pi-ai`, `pi-coding-agent` and `pi-tui` are Pi's own) |
| Target | PiG 0.5.0 (+1.3.0.1402973a), go1.27 |
| Original's tests | 5 vitest files, 103 `it` cases (`port/upstream-tests.json` lists their titles) |
| Kind | 1: a Pi TypeScript extension, run under Pi as the oracle |
| Owner approval | 2026-10-21 (PROGRAM.md, "port two heartbeat/watcher extensions into Pigpen") |

## Slice 1 (this revision): inventory and honest refusal

The package registers the `schedule_prompt` tool (the original's name, label, description and parameters) and the
`/schedule-prompt` command. Nothing else is registered and nothing runs: no scheduler, no storage, no widget, no events. Every use
of either is refused with the program's message:

> @pi-in-go/pigpen-schedule-prompt (Go port) does not support <feature> yet. Install the original extension, npm:pi-schedule-prompt@0.4.1, which runs unchanged in PiG.

The tool answers with a tool error (`isError`), the command with an `error` notification. Layer-1 tests: `refusal_test.go`.

## Inventory

Every observable behaviour of the original, its source, the Go SDK operation it needs and the slice that ports it. Classes:
**Go** (portable with the public SDK), **SDK** (needs a PiG SDK capability; named, with the Pi API it mirrors), **owner** (needs a decision).

| ID | Original | Behaviour | Class | Slice |
|---|---|---|---|---|
| M1 | `types.ts:75-141`, `tool.ts:22-27` | the `schedule_prompt` tool definition: name, label, description, TypeBox parameters (`StringEnum` actions and type, `minLength` model, `anyOf` extensions and skills) | Go | 2 (the definition is already registered in slice 1) |
| M2 | `tool.ts:29-285` | actions `add`, `remove`, `list`, `enable`, `disable`, `update`, `cleanup`; errors come back as result text `✗ Error: ...` with `details.error`, not as a tool error | Go | 2 |
| M3 | `tool.ts:33-46` | recursion guard: an `add` is refused when one of the last 10 session entries is a custom `scheduled_prompt` message | Go (`Context.GetEntries`) | 2 |
| M4 | `scheduler.ts:362-433` | schedule parsing: relative `+Ns/m/h/d`, intervals `Ns/m/h/d`, ISO timestamps through `new Date(string)` (V8's parser: ISO and legacy formats), the past and the under-5-second checks, the 6-field cron check | Go (a V8-compatible `Date.parse`; proven by a differential test against Node) | 2 |
| M5 | `scheduler.ts:197-209`, `croner` 10.0.1 | cron validation errors (croner's own messages) and next-run times in the local time zone, DST included | Go (a port of croner's pattern, `CronDate` and `Cron`; proven by a differential test against croner 10.0.1) | 2 |
| M6 | `storage.ts` | `.pi/schedule-prompts.json`, `JSON.stringify(store, null, 2)`, temp file and rename, a re-read on every operation, key order as a JavaScript object keeps it | Go | 2 |
| M7 | `settings.ts` | two-layer settings (global `<agent dir>/schedule-prompts-settings.json`, project `<cwd>/.pi/schedule-prompts-settings.json`), `sanitize`, partial save | Go; `<agent dir>` is PiG's (owner rule of 2026-10-20 for tintinweb-tasks, D2) | 2 |
| M8 | `scheduler.ts:63-295` | session binding (`isLoadedFor`), `start()` with the stale-`running` sweep, the timers (cron, interval, once), the past-timestamp disable, auto-disable of once jobs | Go | 2 |
| M9 | `scheduler.ts:197-262` | firing: the empty-content marker, `sendUserMessage(prompt, {deliverAs: "followUp"})`, the statistics written back (`lastStatus`, `lastRun`, `runCount`, `nextRun`), error handling | Go (`Context.SendCustomMessage`, `Context.SendUserMessage` on a retained context) | 2 |
| M10 | `index.ts:105-145` | `session_start` (auto-cleanup of this session's disabled jobs unless `startup`, then a fresh scheduler), `session_shutdown` (auto-cleanup, stop) | Go | 2 |
| M11 | `index.ts:32-71` | the `scheduled_prompt` message renderer (inline, subagent start, done, error) | Go (`Extension.MessageRenderer`) | 2 |
| M12 | `tool.ts:288-377` | tool call and result renderers | Go (`Extension.ToolRenderer`) | 2 |
| M13 | `cron:change` on `pi.events` | change events, subscribed by the widget | Go (`Context.Events`) | 2, 3 |
| M14 | `ui/cron-widget.ts` | the widget below the editor: header, rows, relative times, a 30 s refresh, hide and show | Go (a `kit` lines view; `pitui` `Container`, `Text`, `DynamicBorder`) | 3 |
| M15 | `index.ts:150-195`, `ui/jobs-view.ts`, `ui/add-flow.ts` | `/schedule-prompt`: Jobs overlay (select, add, toggle, scope, remove, cleanup), the add flow, Settings | Go (`Context.Custom` with a `RemoteComponent` and `OnHandle`; `Select`, `Input`, `Confirm`) | 3 |
| M16 | `subagent.ts`, `scheduler.ts:264-358` | the per-task `model` mode: a fresh in-process agent session with the chosen model, `notify`, `extensions`, `skills`; start, done and error markers | **SDK**: C2 `createAgentSession`, see below | 4 |
| M17 | `scheduler.ts` (`console.*`) | log lines on stderr | Go | 2 |

### Upstream defects found while reading (not fixed, listed for the owner)

- **U1** (`scheduler.ts:155-170`, `scheduler.ts:127`): Node's `setTimeout` and `setInterval` take at most 2^31-1 ms (24.8 days). A `once` job more than 24.8 days away, or an `interval` job of 25 days or more (`+30d`, `30d`), makes Node print a `TimeoutOverflowWarning` and use 1 ms: the once job fires at once, the interval job fires every millisecond. Cron jobs are not affected (croner waits in 30 s steps).

### SDK capability needed for slice 4 (reported to the Tern lead)

- **C2 `createAgentSession`** (already item C2 in `core-items.md`): the original calls `createAgentSession({cwd, agentDir, sessionManager: SessionManager.inMemory(cwd), settingsManager, modelRegistry: ctx.modelRegistry, model, tools, resourceLoader})`, `session.bindExtensions({})`, `session.subscribe(...)`, `session.prompt(text)`, `session.abort()`, `session.messages`, and filters the loader with `extensionsOverride` and `skillsOverride`. The Go SDK has no child-session API. Until then the `model`, `notify`, `extensions` and `skills` parameters are refused in slice 2 with the program's message.
