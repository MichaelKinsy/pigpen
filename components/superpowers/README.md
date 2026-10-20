# superpowers (Skills Package)

The fifteen Skills of [obra/superpowers](https://github.com/obra/superpowers) by Jesse Vincent, as a PiG Skills Package:
brainstorming before building, writing and executing plans, test-driven development, systematic debugging, requesting
and receiving code review, verification before completion, git worktrees, finishing a branch, parallel and
subagent-driven development, and writing Skills. The text is Jesse Vincent's, at release v7.0.0 (see
[CREDITS.md](CREDITS.md)). It also ships a small Go extension, the port of the original's Pi extension, that injects the
`using-superpowers` bootstrap (below). Nothing else runs at start-up, and no Node.

```sh
pig install ./components/superpowers
```

Skills load on demand: the model sees each description and loads a Skill when it applies, or you load one with
`/skill:pigpen-superpowers-brainstorming`. Start with `pigpen-superpowers-using-superpowers`, the Skill that tells the
agent to check for Skills before it answers.

## What is changed

Only what PiG's Skill format and Pigpen's Skill gate require, all recorded in [port/ADAPTATIONS.md](port/ADAPTATIONS.md):
each Skill's `name` and directory carry the `pigpen-superpowers-` prefix, and the 41 references to a Skill by its plugin name
(`superpowers:test-driven-development`) name the renamed Skill. Every other byte is the original's; the unmodified tree is
in [port/oracle/skills](port/oracle/skills).

## The bootstrap extension

`extensions/superpowers` ports the original's `.pi/extensions/superpowers.ts` to Go. Like the original it adds the text of the
`using-superpowers` Skill, followed by a short Pi tool mapping, to the model's context as one user message: in the first
request of a session, in every request of that first agent run, and again in the first request after a compaction (after
the compaction summary, never before the compaction). It is not saved in the session. It starts injecting again on a new
session and stops when the agent run ends. The proof is the original under Pi against the port under PiG on the same
scripted scenarios, and the original's own tests as twins: see [port/PORT.md](port/PORT.md).

Two things differ from the original, both recorded there: the extension carries the shipped `using-superpowers` Skill inside it
instead of reading `skills/using-superpowers/SKILL.md` at run time, so the bootstrap names the renamed Skills; and it learns
where the Package's Skills were loaded from by asking the host (a Go extension has no path of its own), which only matters
for the original's `resources_discover` contribution of the Skills directory, a no-op when the Package is installed.

The Piglet `pig-essentials` selects this Package's Skills but not this extension, so it does not inject the bootstrap.

## The Node helpers, in Go

The original's Skills tell the model to run two Node programs. Here they are tools of `extensions/superpowers-tools`, so no Node is
needed at run time:

- `brainstorm_server` is the visual companion of `brainstorming` (`scripts/server.cjs`, `start-server.sh`, `stop-server.sh`):
  `action: "start"` takes the scripts' options (`project_dir`, `host`, `url_host`, `idle_timeout_minutes`, `open`) and returns the
  `server-started` JSON with the session URL; `action: "stop"` takes the `session_dir`. The server runs inside the extension
  process: there is no foreground or background mode and no owner-PID watchdog, because it ends with the session.
- `render_skill_graphs` is `writing-skills/render-graphs.js`: it renders the ` ```dot ` diagrams of a Skill's `SKILL.md` to SVG
  (`combine` for one SVG). It runs graphviz's `dot`, as the original does, so graphviz must be installed.

The tools' prompt guidelines tell the model to use them instead of `bash scripts/start-server.sh` and `node ./render-graphs.js`. The
scripts themselves still ship inside the Skills, byte for byte as the original has them, and are inert without Node. Proof and
differences: [port/PORT.md](port/PORT.md).

Pair it with `tintinweb-subagents` for the Skills that dispatch subagents (`subagent-driven-development`,
`dispatching-parallel-agents`).
