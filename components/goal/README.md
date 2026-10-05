# pi-goal-x (Go port, partial)

A Go extension for PiG: durable goals kept as Markdown files with a JSON header under `.pi/goals/`, one session focused on one
goal, an event ledger, and the commands that act on them directly.

It is a port of `pi-goal-x` 0.32.3 (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

## Use

| Command | What it does |
|---|---|
| `/goal-direct <objective>` | Creates a goal from the text and focuses it (a `Verification contract:` line is kept) |
| `/sisyphus-direct <objective>` | The same, in Sisyphus mode |
| `/goal-list` | Lists the open goals |
| `/goal-pause` | Pauses the focused goal |
| `/goal-unfocus` | Drops this session's focus; the goal stays open in `.pi/goals` |
| `/goal-clear` | Archives the focused goal after a confirmation |

Settings: `maxAutonomousRuns`, `disableContracts`, `autoSelectSingleGoal` and `strictExecutionContract`, from `<project>/.pi/pi-goal-x-settings.json`,
`<agent dir>/pi-goal-x-settings.json` and the `PI_GOAL_*` environment variables, as the original reads them.

```sh
pig install ./components/goal
```

## What is ported

The goal record and its file (byte for byte, key order included), the goal pool and its snapshot, locks, the ledger, session
start with the focus entry and the scheduler restore (a goal that this session owns and that ran out of allowance, budget or
wait time is paused as the original does), and the six commands above.

**Not ported:** the scheduler's continuation (a goal is stored, and the port says so; it does not start runs by itself), `/goal`
and `/sisyphus` drafting, the auditor, the goal and task tools, the dashboard, `/goal-resume`, `/goal-focus`, `/goal-settings`
and the other commands, and usage accounting. See [port/PORT.md](port/PORT.md).
