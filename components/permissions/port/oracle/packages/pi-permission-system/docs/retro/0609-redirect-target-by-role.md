---
issue: 609
issue_title: "Allow Bash commands without automatically allowing output redirects"
---

# Retro: #609 — Allow Bash commands without automatically allowing output redirects

## Stage: Planning (2026-09-24T22:07:43Z)

### Session summary

Planned the Phase 15 step for #609: `PathToken` gains a required `TokenRole`, and a redirect's literal first destination with a syntax-proven effect skips the shape gate and the existence probe at projection.
I prototyped the design and measured it against 8746 real review-log commands, filed #977 and #978, dispositioned both into Phase 15, corrected the #609 roadmap step, and committed the plan (`docs/plans/0609-redirect-target-by-role.md`).

### Observations

- The issue is third-party (`hcrosse`), but the owner's comment settled the direction against ADR 0013 before this session, so the gate covered design, not whether to build it.
- **Pre-existing grammar quirk (filed as #977).**
  `tree-sitter-bash` 0.25.1's `file_redirect` has `destination: repeat1(...)`, so words after a redirect parse as extra destinations.
  I measured it through the real `resolveBashCommandCheck`: `git 2>/dev/null push --force` and `find ~/x 2>/dev/null -delete` both resolve `allow` under explicit denies.
  Upstream tree-sitter/tree-sitter-bash#233 is closed, but the default branch still has `repeat1`.
  The operator chose to keep #609 on the first destination only and make #977 the step right after it.
- **Roadmap errors corrected in the disposition commits.**
  1. The suggested migration note `path_write: {"*": "allow"}` lifts a `path` write deny: explicit directional entries go after the sugar, and last match wins (measured on `.env`).
  2. "An unconfigured install prompts on `echo hi > out.txt`" is false: the unmatched-promotion guard keeps `path` silent, and the measured count of new prompts with no config was 1 in 8746.
  3. `> /dev/null` is already a `path_write` candidate today, so the #951 interaction note was wrong.
- The prototype showed that both admission rules are load-bearing.
  Without the first-destination rule it admitted `-type`, `d`, and `--include=*.ts`; without the literal-target rule it admitted `$3`, `$tmp`, and `$f`.
- The operator chose to admit input-redirect targets too (one uniform rule).
  On this corpus it gained 0 tokens beyond the output-only variant.
- The `path_write: {"*": "ask"}` recipe from the issue comment already asks on `path` for 6842 of 8746 commands before this change, because unproven tokens consult both directions.
  The plan's docs step avoids presenting it as a redirect-only switch.
- Tidy-First assessor: it recommended one `test:` prep (route 19 effect `toEqual`s through a projection helper) and rejected both roadmap-assigned preps.
  Its claim that `bash-path-extractor.ts` has no production caller checked out on a grep; the operator moved the prefix tidy to #977 and the facade question to #978.
- Co-author trailers for steps 4–5 credit `hcrosse` (#609's provenance mechanism) and `pikujs` (#785's unconditional emission).

#### Deferred tidyings

- `src/access-intent/bash/token-collection.ts` / `bash-path-resolver.ts`: three literal re-spellings of `COMMAND_PREFIX_TYPES`, now #977's prep.
- `src/handlers/gates/bash-path-extractor.ts` and its 1300-line test: no production caller, now #978.

## Stage: Implementation — TDD (2026-09-24T22:46:05Z)

### Session summary

All six plan steps landed as six commits: the `test:` projection prep, `redirectTargetIndex`, `hasComputedPart`, the required `PathToken.role`, the `fix!:` projection by role, and the docs.
The `pi-permission-system` suite went from 4614 to 4648 tests (+34), and every step's named killing mutation turned its tests red.
The re-run corpus spike matched the plan exactly: over 8753 commands, `path` +90 tokens in 60 commands, `external_directory` +97 in 62, 0 lost, and no gained token flag-shaped or computed.

### Observations

- Step 1 deviated in form: instead of wrapping 19 call sites, it changed the two helpers (`tokensOf`, `attributedTokens`) to return the `{ token, effect }` projection, which has the same effect with two edits.
  The assessor's count missed one more exact `PathToken` assertion (the `node -e "$(cat /etc/shadow)"` case in the interpreter describe), which failed at step 4 and was routed through the same `tokenEffectsOf` projection in that commit.
- Step 4's `syntax`-source mutation **survived** the plan's `cat <> rw.txt` case, because that parse leaves an `ERROR` child after the operator, so the word is not the target index and the first-destination rule covers it anyway.
  I added `cat <> ~/rw.txt` (the split where the parse leaves a well-formed-looking redirect), which the mutation kills.
- The mutation for step 1 was applied in the same tool batch as its green-copy `cp`, and the tools ran concurrently, so the saved copy held the mutation; `git checkout` restored the file because it had no uncommitted green edit.
  Sequence the save and the mutation in separate turns.
- I emitted `\u2014` escapes three times in `Edit` bodies (source comment, ADR 0013 staging line, architecture edit); each time I caught it with a grep and replaced it by a parenthetical or a scripted substitution.
- A test block inserted with a Python script skipped `pi-autoformat`, so the pre-commit Biome hook reformatted `program.test.ts` and rejected the first `fix!:` commit; it landed on re-stage.
- Pre-completion reviewer: **WARN**.
  It independently re-derived the redirect spellings (fd-prefixed, `>&-`, `<>`, herestrings, heredocs, quoted/expanded, substitution targets, redirects on compound statements) and found no gap.
  Reviewer warnings: the TDD retro entry was missing (this entry); two decision-surface consumers outside the diff, `logging/command-redaction.ts` (for `node-text.ts`, which only gained an export) and `command-enumeration.ts` (for `redirectMayWriteFile`, which is unchanged), are not re-covered by in-range tests.

## Stage: Final Retrospective (2026-09-25T04:43:42Z)

### Session summary

One continuous trunk session planned, implemented, shipped, and retro'd #609, released as `pi-permission-system-v34.0.0`.
Planning prototyped the design against 8746 real review-log commands, which surfaced a pre-existing bash deny bypass (filed #977) and refuted three claims in the roadmap step; implementation matched the plan's corpus prediction exactly (+90 `path`, +97 external, 0 lost).

### Observations

#### What went well

- Prototyping the fix and diffing it over the real corpus was the move that paid for the session.
  The first prototype run admitted `-type`, `d`, and `--include=*.ts` as write paths, which led straight to the `tree-sitter-bash` `repeat1` destination quirk and, through a real `resolveBashCommandCheck` run, to a deny bypass (`git 2>/dev/null push --force` allowed under `git push *: deny`).
  A design reasoned from the issue alone would have shipped that over-admission.
- Measuring roadmap claims before planning around them corrected three: the suggested migration note (`path_write: {"*": "allow"}`) would lift a user's `path` write denies, an unconfigured install does not prompt in the working directory, and `> /dev/null` was already a `path_write` candidate.
  The first would have shipped in a `BREAKING CHANGE:` footer as a harmful recommendation.
- The Tidy-First assessor refuted both preparatory steps the roadmap assigned, finding that `bash-path-extractor.ts` has no production caller (a grep confirmed it), and proposed the one prep that actually shrank the change.
- The pre-completion reviewer was asked to re-derive redirect spellings rather than check the tests' own, and it enumerated heredocs, herestrings, fd-close, and substitution targets independently.

#### What caused friction (agent side)

- `other` — em-dashes left the model as a literal `\u2014` escape five times: two planning `Edit` batches on `architecture.md` were rejected whole, and a TS doc comment in `redirect-analysis.ts`, ADR 0013's staging line, and a retro heading each needed a repair pass.
  Impact: two rejected batches and three repair tool calls; the TS-comment instance passes every gate (`unicode-escapes.mjs` scans markdown only) and was caught only by a manual grep.
  It recurred once more while this retro entry was written, where `pi-autoformat` decoded the prose separators and the stage timestamp's date was also mistyped (corrected from the `date` output).
- `instruction-violation` (self-identified) — during step 1's mutation check, the `cp` saving the green file and the mutating `Edit` sat in one tool batch, ran concurrently, and the saved copy held the mutation; `git checkout` recovered only because the file had no uncommitted green edit.
  Impact: no rework, but on a step with uncommitted green work the restore would have committed the mutation or lost the edit.
- `instruction-violation` (self-identified) — the plan recorded the assessor's count of 19 exact `PathToken` assertions without re-grepping, which the `/plan-issue` prompt asks for; a 20th (the `node -e "$(cat /etc/shadow)"` case) broke at step 4.
  Impact: one extra fix folded into the step-4 commit.
- `instruction-violation` (self-identified) — test blocks inserted with Python scripts skipped `pi-autoformat`, so the Biome pre-commit hook reformatted `program.test.ts` and rejected the first `fix!:` commit; the `edit-tool` skill already says to append source with `Edit`/`Write`.
  Impact: one rejected commit, re-staged.
- `missing-context` — the plan named `cat <> rw.txt` as the case killing the `syntax`-source mutation without tracing it; that parse leaves an `ERROR` child after the operator, so the word is never the target index and the mutation survived.
  Impact: one added test (`cat <> ~/rw.txt`); the TDD prompt's "fewer reds than predicted is a finding" rule caught it as designed.
- `instruction-violation` (user-caught) — the tidy-reassignment gate offered "move both out of #609" without saying whether #609 still proceeded, and the operator answered with "So do we pause #609 here then?".
  The answer ("No, #609 doesn't need to pause") was composed only in two thinking blocks, and the turn emitted nothing but a second `ask_user` — confirmed in the transcript, where that assistant message holds two `thinking` parts and a `toolCall` and no `text` part.
  `clarification-gates` already said to answer a question without re-offering the menu; it did not say the answer must be visible or that the turn should end there.
  The operator reports the same pattern (reply only in reasoning, then another gate) several times on `claude-opus-5-5`.
  Impact: an unanswered question and an extra gate round; landed as a `clarification-gates` amendment.

#### What caused friction (user side)

- None beyond the above; the operator's retro comment is what surfaced the unanswered-question pattern, which the agent had not noticed.

### Diagnostic details

- **Model-performance correlation** — planning, TDD, and the retro ran on `claude-opus-5-5` (209 then 7 assistant turns); `/ship` ran on `claude-sonnet-5` (29 turns), appropriate for a checklist stage with no judgment calls.
  Both subagents (`tidy-first-assessor`, `pre-completion-reviewer`) ran on `claude-sonnet-5` per their transcripts; both produced findings the main session verified and acted on, so no mismatch.
- **Feedback-loop gap analysis** — `pnpm run check` ran after every interface-changing step and the full package suite ran at step 4 and after step 5, so no gap; the one late catch was the Biome hook at commit, a formatting check rather than a verification gap.

### Changes made

1. `.pi/prompts/tdd-plan.md` — step 3 now says to run the green-file `cp` in its own tool call before the mutating `Edit`, since calls in one batch run concurrently.
2. `.pi/skills/clarification-gates/SKILL.md` — "When the operator answers with a question" now requires the answer in a visible message and ending the turn there, with no follow-up `ask_user` in the same turn.
