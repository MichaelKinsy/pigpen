---
issue: 995
issue_title: "pi-permission-system: `$HOME` and `$PWD` resolve to their startup values after the command reassigns them"
---

# Retro: #995 — `$HOME` and `$PWD` resolve to their startup values after the command reassigns them

## Stage: Planning (2026-09-30T17:42:45Z)

### Session summary

Planned making a rebound `HOME`/`PWD` computed, using a structural rebinding scan over the program's parse roots and a leading-tilde rule, delivered through two new collaborators: `ShellVariables` (the closed rebinding set) and `WordReader` (node-text reads bound to it).
The operator chose the recommended option on every gate question: detection covers structural bindings, bare-word names, and `eval`/`source`/`.`; the tilde rule covers both the rebound case and the inherited-`HOME` dash lead; the commit type is `fix:`.
The plan has 9 steps: 3 preparatory refactors, 1 test migration, 4 fixes (mechanism, then data, then two tilde steps), and 1 docs step.

### Observations

- Measured, not assumed: Pi runs `/bin/bash`, which on macOS is bash 3.2, and there `~` follows a reassigned `HOME`.
  Homebrew bash 5.3 does not, so the issue comment's bash 5.3 observation understates the tilde defect in Pi's real shell.
- `os.homedir()` returns the inherited `HOME` verbatim (`HOME=-h node` prints `-h`), so an unrebound `$HOME` is already exact; only a literal `~` word needed its dash lead fixed.
- A text scan for `\bHOME\b` would over-mark 27 of the 53 `$HOME`-bearing logged commands (all `env -i HOME="$HOME"`).
  Over-marking drops correct projections, so it is not the safe direction; the rule is structural (`variable_name` outside a plain reference).
- Review-log measurement (10,153 distinct commands, spike discarded): 2 structural bindings, 0 bare-word names, 9 `eval`/`source`/`.`, 1 of them referencing `$HOME`/`$PWD`/`~`.
- The architectural question the operator raised: node-text reads were per-node pure functions, so program context has to be handed in.
  A node cannot find its root: `TSNode` has no parent pointer, `parse-view.ts` re-parents nodes, and salvaged roots are separate trees.
  The operator agreed to explicit threading behind a `WordReader` collaborator (it threads the finished reader rather than the raw vocabulary).
- A flooring alternative (a program that rebinds `HOME` asks) was rejected in the design: `X=/etc` sits under the same accepted residual, so it buys nothing.
- `ShellVariables` must stay a set of rebound names, never values; ADR 0009 declined same-program assignment dataflow, and this seam makes it cheaper to reopen.

#### Deferred tidyings

- `packages/pi-permission-system/src/access-intent/bash/token-collection.ts`: its free functions only relay the `WordReader` (5 functions pass it through with no read of their own, per the Tidy-First assessor).
  Turning them into methods on a collector object that holds the reader would remove the relay, but it restructures a 1006-line file and was rejected as scope creep for this issue.
  The operator asked that the next `/plan-improvements pi-permission-system` consider it.

## Stage: Implementation — TDD (2026-10-01T05:01:30Z)

### Session summary

All 9 plan steps landed, plus one fix and two docs commits that answered the pre-completion review.
`ShellVariables.scan` decides once per program which of `HOME`/`PWD` it rebinds, and a `WordReader` over it reaches every word read in the path walk and the command enumeration.
The pi-permission-system suite went from 5088 to 5174 tests.

### Observations

- Step 1 deviation: `WordReader` moved to step 2, because `fallow` would flag it unused until the resolver consumed it.
- Step 3 deviation: the log redactor keeps one module-level startup-value reader instead of scanning its own parse; it masks by names, and no spelling of `HOME`/`PWD` is sensitive.
- Step 5 design gap (operator chose option A): the plan assumed a rebound reference's fallback text drops out at the shape classifiers.
  It does not, because `normalizePathPolicyLiteral` → `expandHomePath` re-expands a leading `~`/`$HOME`/`${HOME}` on the `path` surface and in the existence probe.
  The failing test showed `HOME=-delete; find /etc "$HOME"` still projecting `/Users/chris`.
  The resolver now drops a token spelled from a rebound `HOME` before projection (`ShellVariables.spellsReboundHome`, with `hasHomePrefix` added to `expand-home.ts` so the prefix recognition is not re-derived).
  That filter also covered the rebound `~`, so step 7 carried only the word half.
- Mid-step question from the operator: why does `find` withdraw its read claim when a path cannot change read/write?
  The answer was that the word may not be a path at all (`HOME=-delete; find "$HOME"` hands `find` the option `-delete`), and that withdrawal means unproven, not a write.
  Lead with the option case when explaining a guard withdrawal.
- Step 8: `command-effects.ts` reads a non-computed word's *value*, not `mayLeadWithDash`, so an unrebound tilde word is marked computed when the inherited home begins with `-`, and only then, which keeps `sed -n p ~/x` a proven read.
- `cat $HOME` projects nothing even with nothing rebound (a pre-existing quirk); one planned test row built on it was vacuous and was replaced.
- Measurement for the `Landed:` note: 0 of 10,226 distinct logged commands change projection, command units, or effects (a differential run of HEAD against the plan commit through the real `BashProgram.parse`).
  The log is live (this session's own commands land in it), so compare by command key, not line count; one `/tmp` probe result flipped between two runs at HEAD and is filesystem noise.
- Pre-completion review round 1: WARN.
  - The plan's step 6 rule (any argument spelled `HOME` rebinds) over-marked: `grep HOME ~/.bashrc` lost its projection.
    Planning had measured that over-marking drops projections and still accepted it for this rule; that was the planning error.
  - Four misses were unnamed: `let`, quoted `"eval"`/`e\val`, `trap`, and quoted `export "HOME=…"`.
  - The operator chose to narrow the argument rule to name-binding builtins and cover all four misses, in `fix(pi-permission-system): a quoted export, let, trap, or quoted eval counts as reassigning $HOME`.
- Pre-completion review round 2 (delta): WARN, ready for `/ship`.
  The remaining spellings were named as ADR 0009 residuals rather than covered: attached `printf -vHOME`, `read $'HOME'`, `coproc HOME`, `exec {HOME}>f`, a keyword-prefixed `time eval`, and the `printf -- -v HOME` over-mark.

## Stage: Sync (worktree) (2026-10-01T05:07:42Z)

### Session summary

`pnpm run lint` and `pnpm fallow dead-code` pass from the worktree root.
The plan's marker is `**Release:** ship independently`, and the only deferral is the `token-collection.ts` relay tidying recorded under `#### Deferred tidyings` in the planning entry.

**Peer session transcript:** `/Users/chris/.pi/agent/sessions/--Users-chris-development-pi-pi-packages-worktrees-issue-995--/2026-09-30T05-43-11-655Z_01a0f0d6-9c27-76d7-b66e-5290a90d0164.jsonl` — read with `read_session_file({ path: "<path>" })` for message-level verification at land/retro time.

### Observations

The pre-completion reviewer's final verdict was WARN with nothing blocking; the residual spellings it listed are in ADR 0009.

## Stage: Final Retrospective (2026-10-01T15:39:25Z)

### Session summary

The root `/ship` fast-forward-merged the peer branch, passed lint, `fallow dead-code`, and CI on e554a37b, closed #995, and released `pi-permission-system-v36.2.1`.
Across the issue, one peer session ran planning, TDD, and sync (planning and TDD on `claude-opus-5-5`, sync on `claude-sonnet-5-5`), landing 3 refactors, 1 test migration, 5 fixes, and 3 docs commits.
Both design defects that surfaced after planning were in the plan, not the code: a missed downstream re-expansion and an over-broad detection rule.

### Observations

#### What went well

- Per-step mutation testing in TDD was explicit and productive: each step named its mutations (drop a clause, widen a match, hard-code a lead), applied them with `perl`, and restored from a `/tmp` backup.
  It found two vacuous tests: the `cat $HOME` rule-candidate row, which projected nothing even unrebound, and a step-7 mutation (c) caught only at the `node-text` level, which led to a program-level `sed -n p ~/x` control.
- The pre-completion reviewer's differential probe (base against HEAD, 52 constructed inputs through the real `BashProgram.parse`) found a fail-open regression, `grep HOME ~/.bashrc` losing its projection, that 5174 green tests and a 10,226-command log measurement all missed.
  The reviewer ran on `claude-sonnet-5-5` and was the decisive gate for this issue.
- The step-5 deviation gate laid out how an unknown variable is handled today on each surface before offering options A and B, and the operator answered it in one exchange.

#### What caused friction (agent side)

- `missing-context` — the plan assumed a rebound reference's literal fallback (`$HOME/x`) would drop out at the shape classifiers like `$X/x`, but `normalizePathPolicyLiteral` → `expandHomePath` re-expands a leading `~`/`$HOME` to the startup home after classification.
  The package skill already says to trace a repro token through the classifier; planning traced it to the classifier and stopped there.
  Impact: a mid-TDD design gate (options A/B), one new collaborator method (`spellsReboundHome`, plus `hasHomePrefix` in `expand-home.ts`), and step 7 shrinking to the word half.
- `premature-convergence` — planning wrote down that over-marking drops projections and is not the safe direction, then accepted the step-6 rule (any argument spelled `HOME`/`PWD` rebinds) because the review log held 0 such arguments.
  A zero count over the log says nothing about the rule's false positives in shapes the log lacks; `grep HOME ~/.bashrc` was one constructed input away.
  Impact: a review round 1 WARN, one `fix:` commit narrowing the rule to name-binding builtins, one `docs:` commit, an amended roadmap note, and a delta review round.
- `other` — the sync stage (`claude-sonnet-5-5`) wrote a literal `\u2014` escape into the retro; self-identified, and `pi-autoformat` had already decoded it.
  Impact: one wasted tool call.
- `instruction-violation` (self-identified, at retro) — this ship ran `git pull`, the unpushed-commit count, the ancestry prediction, and the ff-merge in one `bash` call, so the unpushed root commit daae2328 was reported only after the merge instead of before step 4.
  Impact: none here, since the peer had rebased onto local `main` and the ancestry check passed.

#### What caused friction (user side)

- The operator's mid-step question (why does `find` withdraw its read claim?) showed the step-5 gate assumed the #992 guard vocabulary; the answer is already recorded in the TDD stage note.
- Nothing else; operator involvement was strategic (option A, narrowing the step-6 rule, residuals over more code).

### Diagnostic details

- **Model-performance correlation** — planning and TDD ran on `claude-opus-5-5`; sync on `claude-sonnet-5-5` (mechanical, appropriate).
  The `tidy-first-assessor` and both `pre-completion-reviewer` rounds ran on `claude-sonnet-5-5`; the reviewer's judgment-heavy differential probe was its strongest output, so no mismatch.
  The assessor recommended a raw parameter over a binding object; the operator chose the `WordReader` object instead, and the relay cost it predicted for `token-collection.ts` is the recorded deferred tidying.
- **Feedback-loop gap analysis** — TDD ran the scoped `vitest` file set after every edit and the full suite plus `check` before every commit; no gap.
  The gap was upstream of the tests: no constructed false-positive inputs for a detection rule until the reviewer supplied them.

### Changes made

1. `.pi/skills/package-pi-permission-system/SKILL.md`: the repro-tracing rule now traces a token through path normalization as well as the classifier.
2. `packages/pi-permission-system/docs/architecture/investigating-a-report.md`: the log-mining section now says a rule's zero hits bound nothing about its false positives, and to probe a fail-open rule with constructed benign inputs.
