# dev-skills

Eight general Skills for software work, as one PiG Package. They are adaptations of
public Skills, not copies: each one is based on a pinned upstream commit and lists what it changes
(see [CREDITS.md](CREDITS.md)); the names carry a `pigpen-` prefix.

| Skill | Use it when |
|---|---|
| `pigpen-tdd` | you want test-first work: seams, independent oracles, red before green, and a mutation gate. |
| `pigpen-diagnosing-bugs` | something is broken, failing or slow: build a fast pass/fail loop first, then localize, then fix and prove. |
| `pigpen-handoff` | a session ends and another must continue: an index document in the OS temp directory, not the repository. |
| `pigpen-research` | a question needs primary sources, with claims recorded as value, source, basis and reasoning. |
| `pigpen-grilling` | you want a plan stress-tested: one question at a time, each with a recommended answer. |
| `pigpen-prototype` | a design question needs throwaway code: a logic prototype or several UI variants. |
| `pigpen-code-review` | review a diff against standards and against its spec. User-invoked only. |
| `pigpen-commit` | write a Conventional Commits message with a descriptive body. |

Skills are text only: no scripts, no network, nothing runs by itself.

## Install

```sh
pig install ./components/dev-skills
```

`pig package validate ./components/dev-skills` validates the Package. The Skills expand
as `/skill:pigpen-tdd` and so on, and the model can load them by name.

## Staying true to the upstream

`upstream-map.json` maps every shipped file to the upstream file it was adapted from, at the pinned commit, with its
adaptations. `node scripts/dev-skills-drift.mjs` (part of `npm run quality`) fails when a shipped file or a vendored upstream
copy changes without its row, and `node scripts/dev-skills-drift.mjs --online --latest` checks the pins against the upstream
repositories and lists what upstream changed since.

## Not in this Package

The author's other Skills tie into a private workflow (spec management, planning,
ticketing, glossaries) or wrap third-party CLIs, and are not included. If you want
Matt Pocock's originals, install them from https://github.com/mattpocock/skills.

MIT. © Michael Kinsy for the adaptations; upstream licenses in `upstream/`.
