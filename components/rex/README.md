# rex Package

Go extension (PiG public Go extension SDK) and Skill for running PiG inside the
[Rex](https://www.superlogical.com/rex/docs) terminal multiplexer.

**It works on its own.** Installing this Package is enough; no Piglet is needed.
The [`pig-rex` Piglet](../../piglets/pig-rex/README.md) is an optional composition
that selects this same Package:

```sh
pig package validate ./components/rex
pig install ./components/rex
```

## What it provides

- **Go extension `rex`** (`extensions/rex`): completely inert unless `REX_BLOCK`
  is set (non-empty) and `rex` is on `PATH`. On `session_start`, it starts
  `rex whoami --autostart=false --json` once in the background with a 2 s
  deadline and returns immediately. On `before_agent_start`, it waits up to the
  remaining deadline and appends one context line to the system prompt naming
  the Rex session, tab (when present), and pane (block). If `rex whoami`
  answers without a pane ID, or fails to answer (timeout, non-zero exit, or
  invalid JSON), it appends the corresponding fallback line advising the agent
  not to rely on `$REX_BLOCK`, and never surfaces an error to the agent.
- **No tools and no status/resume hooks:** PiG itself handles Rex program status
  (OSC 7501) and program resume (OSC 7577).
- **Skill `pigpen-rex`** (`skills/pigpen-rex/SKILL.md`): tells the agent to run
  `rex skill show` and follow the guide printed by the installed `rex` CLI.

[MIT](LICENSE), by Michael Kinsy; see `provenance.json`.
