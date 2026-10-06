# superpowers (Skills Package)

The fifteen Skills of [obra/superpowers](https://github.com/obra/superpowers) by Jesse Vincent, as a PiG Skills Package:
brainstorming before building, writing and executing plans, test-driven development, systematic debugging, requesting
and receiving code review, verification before completion, git worktrees, finishing a branch, parallel and
subagent-driven development, and writing Skills. The text is Jesse Vincent's, at release v6.4.2 (see
[CREDITS.md](CREDITS.md)). It carries no code that runs at start-up and no hook.

```sh
pig install ./components/superpowers
```

Skills load on demand: the model sees each description and loads a Skill when it applies, or you load one with
`/skill:pigpen-superpowers-brainstorming`. Start with `pigpen-superpowers-using-superpowers`, the Skill that tells the
agent to check for Skills before it answers.

## What is changed

Only what PiG's Skill format and Pigpen's Skill gate require, all recorded in [port/ADAPTATIONS.md](port/ADAPTATIONS.md):
each Skill's `name` and directory carry the `pigpen-superpowers-` prefix, and the 39 references to a Skill by its plugin name
(`superpowers:test-driven-development`) name the renamed Skill. Every other byte is the original's; the unmodified tree is
in [port/oracle/skills](port/oracle/skills).

## What is not included

The original's Node extension for Pi, which injects the `using-superpowers` bootstrap into the first model request, is not
shipped: a Package here runs no Node code. The visual companion of `brainstorming` is a Node server started by the model
through `bash`, and `writing-skills` has a Node script that renders flowcharts; both work only on a machine that has Node.

Pair it with `tintinweb-subagents` for the Skills that dispatch subagents (`subagent-driven-development`,
`dispatching-parallel-agents`).
