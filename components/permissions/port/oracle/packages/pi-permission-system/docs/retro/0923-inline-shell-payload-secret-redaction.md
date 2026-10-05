---
issue: 923
issue_title: "pi-permission-system: a secret inside an inline-shell payload or heredoc body escapes command redaction"
---

# Retro: #923 — a secret inside an inline-shell payload or heredoc body escapes command redaction

## Stage: Planning (2026-09-19T08:03:18Z)

### Session summary

Planned the inline-shell payload half of [#923] and **declined** the heredoc half on a measurement, against the author's 13 MB review log (17 981 records, 8 056 unique command values).
The plan is `docs/plans/0923-inline-shell-payload-secret-redaction.md`: six steps, one of them the Tidy-First assessor's `commandWordNodes` extraction, with the observable `fix:` landing at step 4.
A second defect surfaced from the operator's `xargs` question and was filed as [#951] with a Phase 15 disposition of its own.

### Observations

- **The heredoc decision was settled by measurement, not by argument.**
  Applying the three shipped rules to every heredoc body in the corpus produced 6 matches across 915 non-interpolating (`<<'EOF'`) bodies and 0 across the 3 interpolating ones.
  All 6 were false positives, and 3 of them were `key=lambda` / `key=len` inside embedded Python — the exact class ADR 0010 measured at 10-versus-0 when it chose grammar anchoring over a raw-string scan.
  The cost was also measured: every-heredoc-body is 0.123 ms/command against a 0.050 ms baseline, versus 0.061 ms for payloads alone.
  Declined as an **accepted residual** in ADR 0010 rather than a tracked follow-up: an open issue would imply the decision is provisional when the measurement says it is not.
- **`cat > .env <<'EOF'` is the more realistic leak and it stays open.**
  The issue's own example uses the bare `<<EOF` (interpolating) form, but a literal `.env` is more naturally written with a quoted delimiter.
  Interpolation turned out to be the wrong axis entirely — it separates "the shell expands `$VAR`" from "it does not", and `nested-execution.ts` uses it for an execution question, not a data question.
  That is why the option set offered "every heredoc body" as its own choice rather than folding it into the interpolating one.
- **The payload set being the *shell* set is the load-bearing safety property.**
  `python3`, `node`, and `perl` are absent from `SHELL_WRAPPER_NAMES`, which is the only reason `command-redaction.test.ts:140`'s embedded-Python control stays green under the widening.
  Nothing pins that today, so the plan adds a `bash -c 'python3 -c "…key=lambda…"'` nesting-boundary case and names it as a risk about a mechanism being **absent**.
- **The prototype found a defect the corpus could not.**
  A first prototype run swallowed the closing quote on `bash -c 'curl -H "Authorization: Bearer sk-z" …'`, because it applied a bare placeholder instead of the span's own `replacement`.
  Zero real occurrences, so the 8 056-command differential would never have surfaced it — it is now step 4's killing mutation (e).
  This is the same shape as [#920]'s reviewer finding on `openingQuoteOf`: quote balance is a property that has to be derived adversarially, not measured.
- **The measured verdict on the widening is 0 changed commands.**
  0 of 8 056 unique real commands log differently under a prototype of the design, and the payload rule has 0 true positives in the corpus too.
  The change is a consistency fix plus forward protection, and the plan says so rather than implying a leak was found.
- **The Tidy-First assessor corrected the design's shape.**
  It rejected threading an `offset` parameter through `collectMaskSpans` and its three rule helpers in favor of shifting the returned span batch once at the recursion boundary — same result, no signature change to four currently-correct functions.
  Adopted.
  Its one **Recommended** tidying (extract `commandWordNodes` from `readCommandWords`) is step 2.
  Its line-number estimates were off by 20–35 lines; re-grepped before the plan recorded them.
- **A near-miss on naming.**
  The assessor proposed `commandArgumentNodes`, but `token-collection.ts:255` already has `commandArgumentWords` with a **different** filter, and `architecture.md:1064` records that those two walks differing is a finding rather than an accident.
  Renamed to `commandWordNodes` to pair with `CommandWord` / `readCommandWords`.
- **The operator's aside about `xargs` was a real second defect.**
  `xargs ls` is already exempt under [#803]; what actually prompts is a `/dev/null` redirect anywhere in the statement, because `redirectMayWriteFile` proves a write for any non-descriptor destination and `/dev/null` is a `word`.
  The package already holds the fact (`src/path/safe-system-paths.ts:6`) and `redirect-analysis.ts` never consults it.
  Filed as [#951], dispositioned out of scope for Phase 15, with the [#609] interaction recorded (that step makes a bare creating redirect reach `path_write`, which would newly project `> /dev/null`).

#### Deferred tidyings

- `src/access-intent/bash/{program,sync-commands,unresolved-salvage,command-redaction}.ts` — four sites repeat the same parse-a-fragment-then-`delete()`-the-tree idiom with no abstraction over it; the assessor declined to unify them and step 4 makes it a fifth occurrence inside a recursion.

## Stage: Implementation — TDD (2026-09-19T14:56:08Z)

### Session summary

Ten commits over the plan's six steps plus four review-driven additions: two Tidy-First preparations, the payload-node query, the masking recursion, the writer-level pin, the doc sweep, then two `fix:` commits and two doc corrections that came out of the pre-completion review.
The package suite went from 4435 to 4511 tests (+76).
The reviewer returned FAIL on the first pass with one blocking finding and WARN on the delta re-review, with one non-blocking provenance finding now closed below.

### Observations

- **The reviewer found two real leaks the corpus could not, and one of them was the reported defect itself.**
  `payloadSlice` decided "quoted" by comparing a node's first and last character, which holds for `string`/`raw_string` and nothing else the grammar can put at a payload position.
  `bash -c 'TOKEN='"$SECRET"` (a `concatenation`, and an entirely ordinary interpolation idiom) and `bash -c $'TOKEN=sk-x'` (an `ansi_c_string`) were written verbatim.
  Neither shape occurs once in 8 138 real commands, so measurement was never going to surface them — deriving inputs from the stated invariant was, which is the same lesson [#920]'s review produced about `openingQuoteOf`.
- **The reviewer's *non-blocking* observation was the more serious of the two.**
  It noted in passing that a payload behind an indirection wrapper is never reached, and called it a pre-existing scope boundary rather than a defect.
  Checking it showed `sudo bash -c 'TOKEN=sk-secret deploy'` reproducing this issue's defect exactly — masked under `executedUnit`, verbatim under `command`, one record — and `xargs -I{} sh -c '…'` is in my own review log.
  Closing [#923] on the plan's literal scope would have left the reported inconsistency live one wrapper layer up.
  Worth generalizing: a reviewer's "matches the plan's scoping, so not a defect" is a claim about the plan, not about the issue.
- **A coarse mask was the right answer where a precise one has no offset.**
  A stitched `concatenation` payload's program is assembled across quote boundaries, so no constant shift maps a span in the program back onto the command.
  Rather than build a per-character offset map, the program decides *whether* a secret is bound and the whole argument is replaced when one is.
  `bash -c 'TOKEN='"$SECRET"` → `bash -c [redacted]`: the argument text is lost, which is the correct trade against writing the secret.
  An `ansi_c_string` needed no such compromise — skipping the leading `$` leaves a single quote pair, so it stays precise.
- **The peeling fix reused the existing walk rather than adding wrapper knowledge.**
  `inlineShellPayloadIndex` now peels over `innerCommandIndex` / `execTerminatorIndex`, tracking the payload's position in the original word list; a new private `directPayloadIndex` holds the non-peeling arithmetic.
  The reviewer independently confirmed `base += start` composes across four layers and a nested `find -exec … \;`, and that no gate-facing answer (`classifyWrapperWords`, `executedUnitOf`, `isTransparentWrapper`, `floorExemption`) moved.
- **One mutation produced zero reds, and that was the correct result.**
  Swapping `opaquePayload`'s `directPayloadIndex` back to the peeling `inlineShellPayloadIndex` reddened nothing.
  The two are provably equivalent at that call site — `unwrapIndirection` has already peeled by the time its opaque branch runs, so the peeling loop's first iteration returns with `base = 0`.
  An equivalent mutation, not a missing test; the reviewer confirmed the reading rather than taking it.
  The plan's step 1 mutation (a) was also mispredicted: it claimed the `eval` case would stay green under an off-by-one, but both branches share the `flagIndex + 2` return, so all 19 cases reddened.
- **The corpus differential was reported on evidence that did not cover the shipped code, and the operator caught it.**
  The delta review's WARN was that two numbers went unreconciled: **0** commands log differently, **4** commands are altered at all.
  The reconciliation is that they measure different things — 4 is the masker's absolute footprint, unchanged before and after; 0 is the pre-versus-post delta.
  But the "0" being carried forward came from the **plan's prototype**, which implemented only the payload-slice widening.
  The stitched coarse branch, the `ansi_c_string` case, and the indirection peeling were all added later and none of them was in the thing measured — and peeling in particular reaches `xargs -I{} sh -c '…'`, which *is* in this corpus.
  The post-fix checks were weaker than stated too: they compared the *count* of altered commands (4, then 4), which cannot see one command leaving the set as another enters.
  Re-measured properly at the end — real pre-change code at `refactor(pi-permission-system): answer which node holds an inline-shell payload` against real post-change code at `HEAD`, dumping every input→output pair over the same log and diffing the 8 142 commands present in both runs: **0 differ**, and the altered set is the same 4 in both.
  Those 4 are all [#920]'s pre-existing `Authorization:` header rule — three real `curl` calls (`"Authorization: token $(gh auth token)"`, `"Authorization: Bearer $TOK"`, and a two-header probe) plus one heredoc holding this issue's own spike vectors, reached through a recovering parse.
  Neither the coarse branch nor the peeling fires on any real command, so both are pinned by tests alone and not by measurement.
  The lesson is narrower than "measure again": a prototype measurement expires the moment the implementation gains a mechanism the prototype lacked, and the expiry is silent because the number still reads true.
- **A deviation from the plan, adopted on the assessor's and reviewer's agreement.**
  The plan put `inlineShellPayloadNode`'s tests in `program.test.ts`; they landed in a new `test/access-intent/bash/command-enumeration.test.ts` instead, named after the module under test as its siblings are.
  `program.test.ts` tests `BashProgram`, a different module.
- **The [#925] flake is no longer a flake.**
  `composition-root.test.ts` > `"blocks promptly when no session is draining the parent's inbox"` failed 3/3 at the plan commit (`docs: plan masking a secret inside an inline-shell payload (#923)`), before any source change of this issue, in the **package-alone** run — not just the root parallel one, which is the boundary [#925]'s body records.
  Durations cluster at 5.03–5.10 s against Vitest's 5 s default, so the timeout is the wall rather than a variable stall.
  Posted to [#925] rather than filed anew.

[#609]: https://github.com/gotgenes/pi-packages/issues/609

## Stage: Sync (worktree) (2026-09-19T15:28:01Z)

### Session summary

Pre-push checks (`pnpm run lint`, `pnpm fallow dead-code`) both pass on `issue-923-pi-permission-system-a-secret-inside-an` with no fix-up needed.
The plan's `**Release:** ship independently` marker stands — [#923] carries no roadmap `Release:` tag, so the root ship should cut a release for this package alone.
No deferred work beyond what the TDD stage note already names: the heredoc/herestring residuals are deliberately unfiled, and [#951] (the `/dev/null` exemption gap) and the sharpened [#925] reproduction are both already filed/posted, not deferred to land time.

**Peer session transcript:** `/Users/chris/.pi/agent/sessions/--Users-chris-development-pi-pi-packages-worktrees-issue-923--/2026-09-19T07-25-46-482Z_01a0b88e-9271-7320-b7e1-671154f9b086.jsonl` — read with `read_session_file({ path: "<above>" })` for message-level verification at land/retro time.

### Observations

Nothing beyond the TDD stage note's own record.
The corpus-measurement correction (the "0 differ" figure had been carried forward from a prototype that predated two of the three shipped mechanisms) was made and committed before this sync stage; see that entry for the full account.

## Stage: Final Retrospective (2026-09-19T19:58:52Z)

### Session summary

Shipped [#923] through the worktree lane: fast-forward-merged 16 commits, released `pi-permission-system` v33.0.2, closed the issue, and tore the worktree down — with no rework at any step.
This entry synthesizes all four stages (planning, TDD, sync, ship), whose dominant theme is that every real defect in this issue was found by adversarial derivation, and every number that needed correcting came from a measurement whose subject had moved.

### Observations

#### What went well

- **The reviewer found two leaks an 8 056-command corpus could not, and one was the reported defect itself.**
  Round 1's FAIL on `payloadSlice` (a `concatenation` and an `ansi_c_string` payload both read as unquoted) and its *non-blocking* note about indirection wrappers each pinned a real exposure with zero occurrences in the corpus.
  Deriving inputs from the stated invariant beat measuring the population, which is the same result [#920]'s review produced on `openingQuoteOf`.
- **The TDD loop ran mutation-first and caught a mispredicted plan.**
  Every step saved a green copy to `/tmp`, applied the plan's killing mutations via `python3` in-place rewrites, and restored — 14 mutations across six steps.
  Two produced results the plan did not predict (step 1's `eval` case reddened all 19 rather than staying green; `opaquePayload`'s peeling swap reddened nothing), and both were reasoned to a verdict rather than patched over.
- **The sync stage's dangling-SHA sweep earned its place.**
  The rebase over a sibling `#952` disposition commit rewrote every SHA, and the sweep caught `acec8edd` cited in the TDD stage note, rewording it to the commit's subject before the land.
  The `architecture.md` conflict was the add-only `[#N]:` reference-definition case the `worktrees` skill names, resolved by keeping both in ascending order.
- **`/ship`'s `PRE_MERGE` anchor rule fired for real (Refs [#899]).**
  The branch carried `docs(pi-permission-system): disposition #951 against Phase 15` *before* its plan commit, so `"$PLAN"^..HEAD` could not see it.
  Both anchors were checked and both yielded the same single package, but the rule is what made that a verified equality rather than an unexamined assumption.

#### What caused friction (agent side)

- `other` (stale evidence) — the "0 commands log differently" figure was carried from the plan's **prototype**, which implemented only the payload-slice widening; the stitched coarse branch, the `ansi_c_string` case, and the indirection peeling all landed afterward and none was in the measured artifact.
  The follow-up checks were weaker than reported too: they compared the *count* of altered commands (4, then 4), which cannot see one command leaving the set as another enters.
  User-caught, by a one-word challenge.
  Impact: one extra commit (`docs(retro): re-measure the corpus differential against the shipped code`) and about five tool calls to dump every input→output pair at both revisions and diff them; the claim survived at 0 of 8 142, so no code changed.
- `other` (deferring to a subagent's scoping) — the reviewer filed the indirection-wrapper gap as a pre-existing scope boundary rather than a defect, and accepting that framing would have closed [#923] with `sudo bash -c 'TOKEN=…'` still reproducing the reported inconsistency exactly.
  Self-identified during the TDD stage.
  Impact: one extra `fix:` commit, caught before ship — no rework, but the near-miss is the notable part.
- `other` (assessor estimates) — the `tidy-first-assessor`'s line-number references were off by 20–35 lines and its proposed name `commandArgumentNodes` collided in meaning with the existing `commandArgumentWords` (a different filter, whose divergence `architecture.md` records as a finding).
  Both self-caught at planning time by re-grepping and renaming to `commandWordNodes`.
  Impact: added friction but no rework.
- `other` (routine tool friction) — three `Edit` `oldText` mismatches, one `edits[1].newText` schema validation failure, and one `ERR_MODULE_NOT_FOUND` for `web-tree-sitter` in a `/tmp` spike probe.
  Impact: single retries each, no rework.

#### What caused friction (user side)

- Nothing to change.
  The one user intervention in four stages was an 11-character challenge to a number, at exactly the point where the number was wrong — a redirecting question rather than a correction, and the highest-leverage possible use of operator attention.
  The `xargs` aside during planning likewise turned into a separately filed defect ([#951]) rather than scope creep.

### Diagnostic details

- **Model-performance correlation** — planning and TDD ran on `claude-opus-5`, sync and ship on `claude-sonnet-5`, this retrospective on `claude-opus-5`.
  All three subagents (one `tidy-first-assessor`, two `pre-completion-reviewer` rounds) ran on `claude-sonnet-5`, attributed from their own transcripts under the session's `tasks/` directory.
  No mismatch to flag: the sonnet-5 reviewer's round-1 FAIL identified a real security leak by adversarial derivation, which is the judgment-heavy half of its job.
- **Escalation-delay tracking** — no `rabbit-hole` friction points; no sequence exceeded five consecutive tool calls on the same error.
  The longest same-target run was the corpus re-measurement (five calls), which was a deliberate procedure rather than a stuck loop.
- **Feedback-loop gap analysis** — verification ran incrementally throughout, not only at the end: `pnpm run check` and the package suite after every Red→Green pair, `pnpm run lint` before every commit, and full gates (`test`, `check`, `lint`, `fallow dead-code`) at the TDD stage's close, again at `/sync-worktree` step 2, and again at `/ship` step 5 on the merged tree.
  The ship-stage re-run is the one that covers the post-rebase tree neither earlier run saw.
- **Unused-tool detection** — nothing notable; no friction point had an undispatched tool that would have helped.

### Changes made

1. `.pi/skills/reproduction/SKILL.md` — added `## A prototype's measurement expires when the implementation diverges`: re-measure against real pre- and post-change code once the implementation gains a mechanism the prototype lacked, and diff the full input→output mapping rather than a count of changed inputs.
2. `.pi/skills/pre-completion/SKILL.md` — added `## A non-blocking observation can still be the issue's own defect` at the end of the file: the reviewer scopes findings against the plan, so check each non-blocking observation against the issue's reported symptom before accepting it.
3. `packages/pi-permission-system/docs/retro/0923-inline-shell-payload-secret-redaction.md` — this Final Retrospective stage entry.

No `AGENTS.md` change: both rules fail the admission test's second question — each fires at a trigger that already has a skill loader.

[#803]: https://github.com/gotgenes/pi-packages/issues/803
[#899]: https://github.com/gotgenes/pi-packages/issues/899
[#920]: https://github.com/gotgenes/pi-packages/issues/920
[#923]: https://github.com/gotgenes/pi-packages/issues/923
[#925]: https://github.com/gotgenes/pi-packages/issues/925
[#951]: https://github.com/gotgenes/pi-packages/issues/951
