# Adaptations

Every change between the unmodified original (`port/oracle/skills`, @dietrichgebert/ponytail 4.10.0, commit
`1d95ff7d39de12d87014ea40d4e22201bddc501b`) and the Skills shipped here (`skills/`), written by `port/adapt.py`.
One kind of change: the Skill `name` and its directory take the `pigpen-` prefix (Pigpen's Skill gate requires
`pigpen-<name>` and a directory equal to the name). Every other byte is as upstream. The main Skill is also
copied to `extensions/ponytail/ponytail_skill.md`, which the extension embeds as its ruleset (the original reads
`skills/ponytail/SKILL.md` at run time).

| Upstream Skill | Skill here |
|---|---|
| `ponytail` | `pigpen-ponytail` |
| `ponytail-audit` | `pigpen-ponytail-audit` |
| `ponytail-debt` | `pigpen-ponytail-debt` |
| `ponytail-gain` | `pigpen-ponytail-gain` |
| `ponytail-help` | `pigpen-ponytail-help` |
| `ponytail-review` | `pigpen-ponytail-review` |
