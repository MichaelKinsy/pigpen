# Credits

The Skills in this Package are adaptations of public Skills. The adaptation
work (condensing, adding a mutation gate, removing the author's private workflow
references) is by **Michael Kinsy**, MIT. Each adapted file names its source at
the top and keeps the upstream license.

## mattpocock/skills

**Matt Pocock**, https://github.com/mattpocock/skills, MIT
(Copyright (c) 2026 Matt Pocock; license at
[`upstream/mattpocock-skills/LICENSE`](upstream/mattpocock-skills/LICENSE)).

Adapted from the `engineering` and `productivity` Skills at commit
`d81f3a183412e71a5b1e84ca21bc1a35eea03a60`:

| Skill here | Upstream Skill | What changed |
|---|---|---|
| `pigpen-tdd` | `engineering/tdd` | Condensed; explicit test-first mode; mutation gate; independent-oracle rule. |
| `pigpen-diagnosing-bugs` | `engineering/diagnosing-bugs` | Condensed; loop preference list kept; regression test must be able to fail. |
| `pigpen-handoff` | `productivity/handoff` | Index-not-store rule for what goes inline. |
| `pigpen-research` | `engineering/research` | Claim records (value, source, basis, reasoning); where findings go; no background agent required. |
| `pigpen-grilling` | `productivity/grilling` | One question at a time instead of rounds. |
| `pigpen-prototype` | `engineering/prototype` | Condensed; `LOGIC.md` and `UI.md` kept close to the source. |
| `pigpen-code-review` | `engineering/code-review` | Standards and spec axes only; subagents optional. |

The unmodified upstream files are kept in [`upstream/mattpocock-skills/`](upstream/mattpocock-skills)
(renamed from `SKILL.md` so they are not loaded as Skills). They are the current
files at the commit above. The adaptations were made from an earlier revision, whose
commit was not recorded, so the diff between an upstream file and its adaptation also
contains upstream changes made since.

## mitsuhiko/agent-stuff

**Armin Ronacher** (GitHub: mitsuhiko), https://github.com/mitsuhiko/agent-stuff,
Apache License 2.0 (license at
[`upstream/mitsuhiko-agent-stuff/LICENSE`](upstream/mitsuhiko-agent-stuff/LICENSE)).

`pigpen-commit` is adapted from `skills/commit/SKILL.md` at commit
`0865c849befd2021490679f96a8dee58c84ac857`. As Apache-2.0 section 4(b) requires,
the modified file states that it was changed: the body of the commit message is
strongly encouraged rather than optional, and the format rules were tightened. The
original is in [`upstream/mitsuhiko-agent-stuff/commit.md`](upstream/mitsuhiko-agent-stuff/commit.md).
