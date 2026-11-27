# pig-rex

PiG for the [Rex](https://www.superlogical.com/rex/docs) terminal multiplexer.
When run inside a Rex pane (`REX_BLOCK` set and `rex` on `PATH`), it starts
`rex whoami --autostart=false --json` once in the background at `session_start`
and injects one context line naming the Rex session, tab (when present), and
pane (block) on `before_agent_start`. Outside Rex, the extension is completely
inert.

**This Piglet is optional.** Its extension and Skill live in the standalone
Package [`components/rex`](../../components/rex/README.md), which works in plain
`pig` without this Piglet (`pig install ./components/rex`).

**Source only, not a published release.** No signed Piglet Binary is published
yet; use the local checkout commands below. See [RELEASE-BLOCKERS.md](../../RELEASE-BLOCKERS.md).

```sh
npm run stage
pig piglet validate dist/staged/piglets/pig-rex/piglet.yaml
pig --piglet dist/staged/piglets/pig-rex/piglet.yaml
```

## What is included

- **Go extension `rex`** (`components/rex/extensions/rex`):
  - Inert unless `REX_BLOCK` is non-empty and `rex` is on `PATH`.
  - Starts `rex whoami --autostart=false --json` once in the background on
    `session_start` with a 2 s deadline (non-blocking startup).
  - Appends one context line on `before_agent_start`:
    - When session and pane are identified: names the session ID, tab ID (when
      present), and pane ID.
    - When `rex whoami` answers without a pane ID, or fails to answer (timeout,
      non-zero exit, or invalid JSON): appends the corresponding fallback line
      advising the agent not to rely on `$REX_BLOCK`. Failures never reach the
      agent.
  - Registers no tools and no status/resume hooks (PiG already handles OSC 7501
    program status and OSC 7577 program resume).
- **Skill `pigpen-rex`** (`components/rex/skills/pigpen-rex/SKILL.md`): tells
  the agent to run `rex skill show` and follow the guide printed by the
  installed `rex` CLI (and notes `~/.agents/skills/rex` if already installed via
  `rex skill setup`).

## Layout

```text
piglet.yaml                 authored composition, input to staging
catalog.json                presentation metadata
provenance.json             original work metadata (MIT)
../../components/rex/       standalone Package: extensions/rex/ (Go) and skills/pigpen-rex/
```
