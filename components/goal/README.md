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

**Not ported, and refused.** Whatever this port does not do yet is answered with one message, never ignored:

> @pi-in-go/pigpen-goal (Go port) does not support <feature> yet. Install the original extension, npm:pi-goal-x@0.32.3, which runs unchanged in PiG.

- Commands: `/goal`, `/sisyphus`, `/goal-cancel`, `/goal-status`, `/goal-refresh`, `/goal-recovery`, `/goal-focus`,
  `/goal-settings`, `/goal-tweak` and `/goal-resume` answer with the message (a notification) and do nothing else.
- Automatic continuation: `/goal-direct` and `/sisyphus-direct` refuse, and create nothing, unless `maxAutonomousRuns` is `0`
  (the original's default is to start runs). A goal found at session start that the original would run is reported the same way.
- Settings: `/goal-direct` and `/sisyphus-direct` refuse while a setting this port does not implement is present in the
  project file, the global file or the environment (everything except `maxAutonomousRuns`, `disableContracts`,
  `autoSelectSingleGoal` and `strictExecutionContract`; the environment variables `PI_GOAL_ROOT`, `PI_GOAL_DISABLE_TASKS`,
  `PI_GOAL_OBJECTIVE_MAX_CHARS`, `PI_GOAL_NETWORK_RECOVERY_*`, `PI_GOAL_PROMPT_PROFILE` and `PI_GOAL_AUTO_CONFIRM`).
- Registers no model tools: `get_goal`, `create_goal`, `update_goal`, `set_goal_tasks` and `update_goal_task` do not exist in
  this port, so the model cannot call them, and no goal text reaches the model's prompt, no usage is counted, the dashboard
  and the auditor are absent. See [port/PORT.md](port/PORT.md) for the inventory.
