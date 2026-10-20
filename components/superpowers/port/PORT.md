# Port record: obra/superpowers (Skills)

| Input | Identity |
|---|---|
| Original | `obra/superpowers` v7.0.0 by Jesse Vincent, https://github.com/obra/superpowers, commit `bb92a77741419a4ab5f06e711a283343f1ada0c3`, MIT |
| Kind | a Skills Package: Markdown Skills, no extension, no code that runs |
| Original kept at | `port/oracle` (the unmodified `skills/` tree and `LICENSE`) |
| Target | PiG 0.4.1 |

## What ships

The fifteen Skills of the original, built from `port/oracle/skills` by `port/adapt.py`. The only changes are the ones PiG's
Skill format and Pigpen's Skill gate require, listed in [ADAPTATIONS.md](ADAPTATIONS.md): the `pigpen-superpowers-` prefix
on each Skill's `name` and directory, and the 41 references of the form `superpowers:<skill>` rewritten to the new names.
Every other byte is the original's.

## What does not ship

- The original's Node bootstrap extension and its hooks (a Package here runs no Node code).

## What ships but needs Node

Two Node helper scripts ship as the original has them, inside their Skills, and run only when the model starts them:
the visual companion server of `brainstorming` (`scripts/server.cjs`, with its browser-side `helper.js`) and the
flowchart renderer of `writing-skills` (`render-graphs.js`). On a machine without Node they cannot run.

## Checks

There is no behavior to run against the original, so there are no scenarios or mutations: the check is that the shipped tree
equals the original except for the recorded changes. `node scripts/skills-drift.mjs` (part of `npm run quality`) rebuilds
every file of `skills/` from `port/oracle/skills` with the rules of `port/adapt.py` and reports a missing, extra or differing
file, a changed executable bit, and a stale `port/ADAPTATIONS.md` or `CREDITS.md`; `python3 port/adapt.py` regenerates the
tree and the table. `node scripts/skills-drift.mjs --upstream superpowers=<git checkout of obra/superpowers>` also compares
`port/oracle` byte for byte with the pinned commit. `npm run check` (the Skill gate: names, directories, licenses,
provenance) and `pig package validate components/superpowers` complete the set.
