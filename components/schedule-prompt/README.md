# pigpen-schedule-prompt

Go port of [pi-schedule-prompt](https://github.com/tintinweb/pi-schedule-prompt) ("Pi's Heartbeat", by tintinweb, MIT): the agent
schedules future prompts, reminders and recurring jobs for itself. Go only; no Node at runtime.

## What it does

- A `schedule_prompt` tool: `add`, `remove`, `list`, `enable`, `disable`, `update` and `cleanup`. A schedule is a 6-field cron
  expression (with seconds), an interval (`5m`, `1h`, `30s`, `2d`), a relative time (`+10s`, `+5m`, `+2d`) or an ISO timestamp
  (any date string JavaScript's `new Date` reads).
- A job fires by injecting its prompt into the chat as a user message (preceded by a `scheduled_prompt` marker); one-shots disable
  themselves; cron next-run times are croner 10.0.1's, in the local time zone, DST included.
- A live widget below the editor (status, name, schedule, prompt, next and last run, run count); it hides itself when there is no job.
- Jobs live in `<cwd>/.pi/schedule-prompts.json` (the original's file, written atomically), bound to the session that created them unless
  the project or global settings say `defaultJobScope: "workdir"`.
- Safety: duplicate names, past or too-near timestamps, and the recursion guard are refused as the original refuses them; disabled jobs
  of a session are removed when it ends.

## Status (0.2.0): what is not ported yet

Everything in the original is ported except two parts, each refused with the program's message until its slice lands:

> @pi-in-go/pigpen-schedule-prompt (Go port) does not support <feature> yet. Install the original extension, npm:pi-schedule-prompt@0.4.1, which runs unchanged in PiG.

- **`/schedule-prompt`** (the Jobs overlay, the add flow and Settings): slice 3.
- **The per-task `model` mode** (`model`, and with it `notify`, `extensions` and `skills` on a subagent job): slice 4. It needs an
  in-process agent session; the Go SDK has none yet (PiG branch `feat/sdk-child-agent-sessions`). A tool call that passes `model` is a tool
  error; a job that already has one (made by the original) reports the refusal as its run's error when it fires.

Differences from the original are numbered in [port/PORT.md](port/PORT.md): long timers fire on time (the original fires a one-shot more
than 24.8 days away at once and an interval of 25 days or more every millisecond: a Node limit), the global settings file lives in PiG's
agent directory, and a store file that is not `{"jobs": [...]}` is refused instead of crashing on a missing member.

MIT. The original's license is kept at [LICENSE](LICENSE); see [CREDITS.md](CREDITS.md).
