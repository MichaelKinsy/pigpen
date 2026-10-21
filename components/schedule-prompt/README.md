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

- `/schedule-prompt`: a menu with **Jobs** (a full-width overlay: select with the arrow keys, `a` add, `t` toggle, `s` switch between
  session-bound and shared, `x` remove, `c` clean up disabled jobs, `q` quit; jobs of other sessions are listed read-only) and
  **Settings** (widget visibility and whether new jobs bind to the session, saved to the project settings file).

- **The per-task `model` mode**: a job (or `add`/`update` call) with `model` runs its prompt in its own agent session with that model
  (`provider/id`, an id, or part of an id or name, among the models with configured auth), instead of the chat. The session has the default
  tools; `extensions` and `skills` (`true` or a list of names) load them, `notify: true` wakes the parent with the answer. A start marker, then a
  done or error marker with the answer or the error, are posted to the chat.

## Requirements

The model mode runs on PiG's child agent sessions (`Context.CreateAgentSession`, PiG branch `feat/sdk-child-agent-sessions`): this package
builds against an SDK that has them.

Everything in the original is ported. Differences from the original are numbered in [port/PORT.md](port/PORT.md): long timers fire on time (the original fires a one-shot more
than 24.8 days away at once and an interval of 25 days or more every millisecond: a Node limit), the global settings file lives in PiG's
agent directory, and a store file that is not `{"jobs": [...]}` is refused instead of crashing on a missing member.

MIT. The original's license is kept at [LICENSE](LICENSE); see [CREDITS.md](CREDITS.md).
