# Adaptations

Every change between the unmodified original (`port/oracle/skills`, obra/superpowers v7.0.0, commit
`bb92a77741419a4ab5f06e711a283343f1ada0c3`) and the Skills shipped here (`skills/`), written by `port/adapt.py`.
Two kinds of change, and no others: the Skill `name` and its directory take the `pigpen-superpowers-` prefix
(Pigpen's Skill gate requires `pigpen-<name>` and a directory equal to the name), and a reference of the form
`superpowers:<skill>` becomes `pigpen-superpowers-<skill>`. All other files and bytes are as upstream.

| Upstream Skill | Skill here | `name` line | references rewritten (file: count) |
|---|---|---|---|
| `brainstorming` | `pigpen-superpowers-brainstorming` | changed | SKILL.md: 2 |
| `diagnosing-superpowers` | `pigpen-superpowers-diagnosing-superpowers` | changed | - |
| `dispatching-parallel-agents` | `pigpen-superpowers-dispatching-parallel-agents` | changed | - |
| `executing-plans` | `pigpen-superpowers-executing-plans` | changed | SKILL.md: 14 |
| `finishing-a-development-branch` | `pigpen-superpowers-finishing-a-development-branch` | changed | - |
| `receiving-code-review` | `pigpen-superpowers-receiving-code-review` | changed | - |
| `requesting-code-review` | `pigpen-superpowers-requesting-code-review` | changed | - |
| `subagent-driven-development` | `pigpen-superpowers-subagent-driven-development` | changed | SKILL.md: 6 |
| `systematic-debugging` | `pigpen-superpowers-systematic-debugging` | changed | SKILL.md: 2 |
| `test-driven-development` | `pigpen-superpowers-test-driven-development` | changed | writing-good-tests.md: 1 |
| `using-git-worktrees` | `pigpen-superpowers-using-git-worktrees` | changed | - |
| `using-superpowers` | `pigpen-superpowers-using-superpowers` | changed | SKILL.md: 2; references/claude-code-tools.md: 2; references/gemini-tools.md: 2 |
| `verification-before-completion` | `pigpen-superpowers-verification-before-completion` | changed | - |
| `writing-plans` | `pigpen-superpowers-writing-plans` | changed | SKILL.md: 5 |
| `writing-skills` | `pigpen-superpowers-writing-skills` | changed | SKILL.md: 4; testing-skills-with-subagents.md: 1 |

41 references rewritten in total.

## The extension

`.pi/extensions/superpowers.ts`, the original's Pi extension, is ported to Go in `extensions/superpowers` (see
[PORT.md](PORT.md)). It reads `skills/using-superpowers/SKILL.md` at run time; the port embeds the shipped Skill
(`extensions/superpowers/bootstrap_skill.md`, a copy of `skills/pigpen-superpowers-using-superpowers/SKILL.md`) and the text
of the original's `piToolMapping()` (`extensions/superpowers/pi_tool_mapping.md`), both written by this script, so the
injected bootstrap names the renamed Skills.

`extensions/superpowers-tools` is a Go port of the two Node helpers the Skills tell the model to run: the visual companion server of
`brainstorming` (`scripts/server.cjs`, `start-server.sh`, `stop-server.sh`) and `writing-skills/render-graphs.js`. They are the tools
`brainstorm_server` and `render_skill_graphs`; the tools' prompt guidelines tell the model to use them in place of the scripts. The
scripts stay in the Skills as the original has them (this tree is byte for byte the original's apart from the changes above) and are
inert without Node. The extension embeds the browser-side `helper.js` and `frame-template.html`, copied by this script.

## Not included

- `hooks/`, `scripts/`, `tests/`, `docs/`, `assets/` and the other harness manifests (Claude Code, Codex, Gemini,
  OpenCode, Cursor): they are not Skills.

The helper scripts inside Skills are kept as they are (`pigpen-superpowers-brainstorming/scripts/`: a visual
companion server in Node plus shell wrappers; `executing-plans` and `subagent-driven-development` shell helpers).
They run only when the model starts them, and the Node server needs Node on that machine; a machine without Node
simply cannot use the visual companion. `pigpen-superpowers-writing-skills/render-graphs.js` is a Node script of the same kind
(it renders the flowcharts of a Skill); it needs Node too and is otherwise inert.
