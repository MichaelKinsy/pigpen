---
issue: 945
issue_title: "pi-permission-system: a command hosted in a consumed flag argument has its operands dropped (ADR 0009 positional invariance)"
---

# A command hosted in a quoted argument keeps its operands

## Release Recommendation

**Release:** ship independently

Phase 15's `Release batches` subsection lists [#945] under "Independently releasable" with commit type `fix:`, and the only batch in the phase is `declared-effects` ([#880], [#881]).
Nothing downstream in Track A — [#863], [#859], [#609] — needs to ship in the same release as this fix.

## Problem Statement

An argument node is read for its **text** but never searched for the **executions it hosts**, in both of `token-collection.ts`'s command walkers.
So a double-quoted command substitution in argument position loses the nested command's operands entirely.

Measured against the shipped collector and the real `tree-sitter-bash` parser (disposable spike, working tree at `d7df5dd2`):

```text
sed -e "$(cat /etc/shadow)" f.txt   -> ["f.txt"]                 # consumed flag argument (the issue's case)
grep "$(cat /etc/shadow)" f.txt     -> ["f.txt"]                 # pattern positional
grep pat "$(cat /etc/shadow)"       -> ["$(cat /etc/shadow)"]    # pattern-first ordinary operand
echo "$(cat /etc/shadow)"           -> ["$(cat /etc/shadow)"]    # generic command argument
```

`/etc/shadow` reaches neither the `path` nor the `external_directory` surface in any of the four.
The command **enumerator** is a separate walker and still emits `cat /etc/shadow` as a unit, so `bash:` rules fire; the loss is confined to the two path projections.

The issue reports one of the four — the `discharge.consumed` branch of `collectPatternCommandTokens`.
The scope was widened to the whole class at the planning gate (operator decision), because all four share one cause and ADR 0009 claims the guarantee for all four positions.

Two controls establish that this is a quoting accident rather than a design boundary:

```text
echo $(cat /etc/shadow)             -> ["/etc/shadow"]                # unquoted: already correct
diff <(cat /etc/shadow) f.txt       -> ["/etc/shadow","f.txt"]        # ADR 0009's own worked example
```

The unquoted forms work because the child node is then a `command_substitution`, which is outside `ARG_NODE_TYPES` and falls through to the ordinary recursion.
Quoting wraps it in a `string` node, which both walkers intercept as an argument and never descend.
`collectRedirectTokens` and `collectStatementOperandTokens` already pair the text read with a `collectHostedExecutionTokens` search; the two command walkers do not.

ADR 0009 states the guarantee this violates:

> These guarantees are **positional-invariant**: they hold for a command's own operands wherever that command appears.
> A command nested in a substitution is itself gated ([#306]), so its operands are projected whether the substitution sits in argument position (`diff <(cat /etc/shadow)`) […] This is a guarantee, not a residual

## Goals

- A command hosted in a **quoted** argument has its operands projected, in every argument position of both command walkers — the consumed flag argument, the pattern positional, a pattern-first command's ordinary operand, and a generic command's argument.
- The projected token keeps the **nested** command's own effect attribution, not the enclosing command's.
- The argument's own text keeps whatever role its walker already gave it: a spent pattern positional stays unprojected, a `script-file` value stays projected, a generic operand stays projected.
  This change **adds** the nested command's operands and removes nothing.
- The change is **not** breaking.
  Measured: 13 of 7653 corpus commands gain a candidate, 0 lose one, and **0** gain a prompt shape they did not already have — every one of the 13 already carried an `external_directory` candidate, so the gain is an extra evidence line on an ask that was already firing.
  Precedent for the classification is [#741] / [#742], whose equivalent new projections shipped as `fix:`.

## Non-Goals

- **Suppressing the argument's own raw text.**
  `echo "$(cat /etc/shadow)"` still emits the useless token `$(cat /etc/shadow)` beside the newly-correct `/etc/shadow`.
  That over-surface predates this change and is [#609]'s `TokenRole` territory; nothing here should anticipate its shape.
- **`..` as a whole segment** ([#859]).
  Four of the fifteen newly-projected strict tokens are git revision ranges (`HEAD..origin/main`, `$FLOOR..HEAD`, `$b..$c`), which is exactly [#859]'s false-positive class — the next step in Track A. This change makes that class slightly more visible in the review log and the ask dialog's evidence list; it creates no new prompt in the corpus, and [#859] removes the noise at the classifier.
- **Interpreter inline scripts** ([#863]).
  Once `node -e "…"` carries the `script` role, the search added here is what keeps `node -e "$(cat /etc/shadow)"` projecting `/etc/shadow`; the two changes compose and neither anticipates the other.
- **`token-collection.ts`'s three near-identical prefix-skip loops and its hand-rolled child loop.**
  Recorded in `docs/architecture/architecture.md` as [#609]'s own tidy-first prep.
- **New coverage in `test/handlers/gates/bash-path-extractor.test.ts`.**
  It drives the same seam, but `program.test.ts` already pins the end-to-end chain and the extractor's blocks are unquoted-only by design.

## Background

`src/access-intent/bash/token-collection.ts` turns a parsed bash AST into `PathToken[]` — each token paired with the `TokenEffect` its position proved.
Four collectors read argument text:

| Collector                       | Reads an argument's text | Searches it for hosted executions |
| ------------------------------- | ------------------------ | --------------------------------- |
| `collectRedirectTokens`         | yes                      | yes ([#741])                      |
| `collectStatementOperandTokens` | yes                      | yes ([#839])                      |
| `collectPatternCommandTokens`   | yes                      | **no**                            |
| `collectGenericCommandTokens`   | yes                      | **no**                            |

`collectHostedExecutionTokens(node)` is the existing private helper both of the first two call.
It walks with the root-inclusive `forEachExecutionIn`, so it reads no text of its own and descends only genuine execution contexts (`command_substitution`, `process_substitution`) — which is exactly right for a `string` node whose `string_content` must never become a path candidate.
It is already called at five sites in the file (lines 54, 122, 205, 613, 771).

`ARG_NODE_TYPES` is `word`, `concatenation`, `string`, `raw_string`.
Only `string` and `concatenation` can host an execution: a `word` has no substitution child, and a `raw_string` is single-quoted, so `tree-sitter-bash` emits no `command_substitution` under it.
The call is therefore a measured no-op for the other two — confirmed by the control `grep -e '$(cat /etc/shadow)' f.txt` -> `["f.txt"]`, unchanged before and after.

Downstream, `BashPathResolver` dedups by resolved path and folds two attributions with `mergeTokenEffects`, so **token order is presentational only** — it cannot change which effect a path ends up carrying.
This is what makes a single insertion point per walker safe even though it emits the hosted operands ahead of the argument's own text.

Constraint from the package skill: over-suppression is unrecoverable, over-surfacing is recoverable (ADR 0009's layering principle).
This defect is on the unrecoverable side, which is why the roadmap gives it the phase's first step despite a zero-traffic population for the narrow branch.

## Design Overview

One guarded call per walker, at the single point that dominates every argument-node exit of that walker's loop.

### `collectPatternCommandTokens`

The loop has four exits for an argument node: the consumed discharge (`continue`), the declined discharge (falls through), the flag branch (`continue`), and the positional branch (skip or emit).
All four are reached after `isArgNode` and `text` are computed, so one call there covers each exactly once:

```typescript
const isArgNode = ARG_NODE_TYPES.has(child.type);
const text = resolveNodeText(child);
// An argument is read for its text below, and its text alone — but a quoted
// substitution in that position really runs, and its own operands are
// candidates wherever it sits (ADR 0009's positional invariance, #741). The
// unquoted spelling reaches this through the `!isArgNode` recursion; the
// quoted one parses as a `string` and would otherwise stop here.
if (isArgNode) tokens.push(...collectHostedExecutionTokens(child));
```

The `!isArgNode` branches below already recurse via `collectPathCandidateTokens(child)`, so the guard is what prevents a double visit.

### `collectGenericCommandTokens`

Two argument-node exits — the `!seenCommandName` first-word skip and the ordinary emit — both sit after the `COMMAND_PREFIX_TYPES` branch:

```typescript
if (ARG_NODE_TYPES.has(child.type))
  tokens.push(...collectHostedExecutionTokens(child));
```

The trailing `collectPathCandidateTokens(child)` recursion handles only non-argument children, so again there is no double visit.

### Measured behavior, before and after

Captured by running the real `collectCommandTokens` over each command, with the two lines absent and present (spike, reverted):

| Command                                            | Before                         | After                                        |
| -------------------------------------------------- | ------------------------------ | -------------------------------------------- |
| `sed -e "$(cat /etc/shadow)" f.txt`                | `["f.txt"]`                    | `["/etc/shadow","f.txt"]`                    |
| `awk -v x="$(cat /etc/shadow)" '{print}' f.txt`    | `["f.txt"]`                    | `["/etc/shadow","f.txt"]`                    |
| `sed -i "" -e "$(cat /etc/shadow)" f.txt`          | `["f.txt"]`                    | `["/etc/shadow","f.txt"]`                    |
| `grep -A "$(cat /etc/shadow)" pattern /etc/passwd` | `["/etc/passwd"]`              | `["/etc/shadow","/etc/passwd"]`              |
| `grep -f "$(echo x)" /etc/passwd`                  | `["$(echo x)","/etc/passwd"]`  | `["x","$(echo x)","/etc/passwd"]`            |
| `grep "$(cat /etc/shadow)" f.txt`                  | `["f.txt"]`                    | `["/etc/shadow","f.txt"]`                    |
| `grep pat "$(cat /etc/shadow)"`                    | `["$(cat /etc/shadow)"]`       | `["/etc/shadow","$(cat /etc/shadow)"]`       |
| `echo "$(cat /etc/shadow)"`                        | `["$(cat /etc/shadow)"]`       | `["/etc/shadow","$(cat /etc/shadow)"]`       |
| `cat "prefix$(cat /etc/shadow)"`                   | `["prefix$(cat /etc/shadow)"]` | `["/etc/shadow","prefix$(cat /etc/shadow)"]` |
| `grep -e '$(cat /etc/shadow)' f.txt` (control)     | `["f.txt"]`                    | `["f.txt"]`                                  |

Effect attribution, also measured: in `sed -e "$(cat /etc/shadow)" f.txt` the new `/etc/shadow` carries `{effect: "read", source: "core"}` — `cat`'s own proof — while `f.txt` stays `{effect: "unproven", source: "unproven"}`, because `sed` is not in the pure-reader core.
That asymmetry is the discriminating signal the tests assert on: a token that inherited the enclosing command's attribution would read `unproven`.

### Measured blast radius

Instrument: the real collector and both shape classifiers run over every distinct intact `toolName: "bash"` command in the local review log (7653 commands, 2026-09-19), with the two lines applied as a spike and the accepted-token sets diffed.

| Measurement                                                       | Before | After      |
| ----------------------------------------------------------------- | ------ | ---------- |
| Commands whose `external_directory` candidate set changes         | —      | 13 (0.17%) |
| Commands whose `path` candidate set changes                       | —      | 14 (0.18%) |
| `external_directory` tokens gained                                | —      | 15         |
| `path` tokens gained                                              | —      | 20         |
| Tokens lost, either surface                                       | —      | 0          |
| Of the 13, those with **no** prior `external_directory` candidate | —      | **0**      |

The last row is the one that settles the breaking-change question: every affected command was already raising an `external_directory` decision, so no command acquires a prompt it did not already have.
Of the 15 gained strict tokens, 11 name real paths the nested command genuinely touches (`/tmp/base.sha`, `/dev/null` ×4, `/tmp/t1.js`, `/tmp/gist2/README.md`, two `.tgz` globs, a Time Machine backup glob, `/`) and 4 are git revision ranges — [#859]'s class, left to [#859].

The full 4511-test suite was run with the spike applied and stayed green, so no existing assertion pins the dropped behavior and no ordering assertion breaks.
That also means nothing currently pins the correct behavior either, which is what the new tests are for.

## Module-Level Changes

- `src/access-intent/bash/token-collection.ts`
  - `collectPatternCommandTokens`: one guarded `collectHostedExecutionTokens(child)` call after `isArgNode` / `text` are computed, with the comment above.
  - `collectGenericCommandTokens`: one guarded `collectHostedExecutionTokens(child)` call after the `COMMAND_PREFIX_TYPES` branch.
  - No signature, type, or export changes.
  - The `collectHostedExecutionTokens` doc comment gains a sentence naming the argument-node caller, so the helper's five-site list stays accurate.
- `test/access-intent/bash/token-collection.test.ts`
  - The `collectCommandTokens — generic commands` describe block gains a local `tokensOf` helper, matching the one the pattern-first block already has (Tidy-First prep).
  - New cases in the existing `a consumed flag argument, whatever node type it is (#823)` block.
  - A new nested describe under the pattern-first block for the positional and ordinary-operand classes.
  - A new nested describe under the generic block for the generic-argument class.
- `test/access-intent/bash/program.test.ts`
  - A small `#945` block asserting the end-to-end chain: `/etc/shadow` reaches `externalAccesses()` for a quoted substitution.
- `docs/architecture/architecture.md`
  - The `token-collection.ts` module-tree entry: the sentence "Also projects the operands of a command hosted in a redirect destination or an interpolating heredoc body" widens to include a quoted argument, in both walkers.
  - The `#### [#945]` step heading gains `✅`, and the step's `Cause` / `Target` / `Outcome` / `Impact` bullets are rewritten for the widened scope — the current text describes the single `discharge.consumed` branch and cites "0 of 5922", both superseded.
  - The `S945` Mermaid node gains `✅`.
  - Landed in the implementation doc-update commit, not deferred to `/ship`.

Predicted **unchanged**, with the claim each rests on:

- `src/access-intent/bash/nested-execution.ts` — no new node type is an execution host; `forEachExecutionIn` is called with a `string`/`concatenation` node, which it already handles as "may contain a context".
- `src/access-intent/bash/node-text.ts` — `ARG_NODE_TYPES` and `SKIP_SUBTREE_TYPES` are read, not changed.
- `src/access-intent/bash/bash-path-resolver.ts` — consumes `PathToken[]`; the shape is unchanged and the dedup already merges duplicate attributions.
- `src/access-intent/bash/command-enumeration.ts` — a separate walker that already enumerates the nested command; measured unchanged for every case in the table above.
- `docs/decisions/0009-bash-path-projection-completeness-contract.md` — the guarantee already claims argument position; the code is moving to match the record, so no amendment is owed.
- `test/handlers/gates/bash-path-extractor.test.ts` — unquoted-only blocks; the full-suite spike run left it green.
- `.pi/skills/package-pi-permission-system/SKILL.md` — names `token-collection.ts` only as the owner of the ADR 0009 walkers, with no per-branch prose.

## Test Impact Analysis

No test becomes redundant and none is removed: the existing `#823` block's substitution cases are the **unquoted** spellings (`grep -A $(cat /etc/shadow) pattern /etc/passwd`, `grep -f $(echo x) /etc/passwd`), which reach the nested command through the `!isArgNode` recursion.
They stay exactly as they are and become the control half of a before/after pair — the new cases are their quoted counterparts.

The extraction this change enables is nothing structural; what it enables is a **grid**: each of the four argument positions now has a quoted and an unquoted row, and an asymmetry between them is legible where a flat list would hide it.

Cases and the class each pins (every expectation below is the measured `After` column, not a prediction):

| Class                                      | Case                                               | Expected                                              |
| ------------------------------------------ | -------------------------------------------------- | ----------------------------------------------------- |
| consumed flag argument, `script` role      | `sed -e "$(cat /etc/shadow)" f.txt`                | `["/etc/shadow","f.txt"]`                             |
| consumed flag argument, `value` role       | `grep -A "$(cat /etc/shadow)" pattern /etc/passwd` | `["/etc/shadow","/etc/passwd"]`                       |
| consumed flag argument, `script-file` role | `grep -f "$(echo x)" /etc/passwd`                  | `["x","$(echo x)","/etc/passwd"]`                     |
| declined `suffix` discharge                | `sed -i "" -e "$(cat /etc/shadow)" f.txt`          | `["/etc/shadow","f.txt"]`                             |
| pattern positional (spent, unprojected)    | `grep "$(cat /etc/shadow)" f.txt`                  | `["/etc/shadow","f.txt"]`                             |
| pattern-first ordinary operand             | `grep pat "$(cat /etc/shadow)"`                    | `["/etc/shadow","$(cat /etc/shadow)"]`                |
| generic command argument                   | `echo "$(cat /etc/shadow)"`                        | `["/etc/shadow","$(cat /etc/shadow)"]`                |
| generic `concatenation` argument           | `cat "prefix$(cat /etc/shadow)"`                   | `["/etc/shadow","prefix$(cat /etc/shadow)"]`          |
| control: single quotes run nothing         | `grep -e '$(cat /etc/shadow)' f.txt`               | `["f.txt"]`                                           |
| control: effect attribution                | `sed -e "$(cat /etc/shadow)" f.txt`                | `/etc/shadow` is `read`/`core`, `f.txt` is `unproven` |

The single-quoted control is the one that keeps the change honest: it shares every other property with the first row and must **not** gain a token, because `tree-sitter-bash` emits no substitution under a `raw_string`.
A fix that searched argument text rather than argument *executions* would light it up.

## Invariants at risk

This change touches the surface [#823] (Phase 14, pattern-first flag bookkeeping) and [#741] / [#742] (nested-execution projection) refactored.
Their documented outcomes and the tests that pin each:

| Invariant                                                                                                                                             | Source                                                 | Pinned by                                                                                                                                        | Still holds                                                                                                                                                                                                   |
| ----------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A pending consumption discharges on whatever node type follows, so a number/expansion/substitution cannot shift the positional count onto the operand | [#823]                                                 | `token-collection.test.ts` `"a consumed flag argument, whatever node type it is (#823)"` (lines 218–272) — real parser, real collector, no mocks | Yes: the added call emits extra tokens and touches no counter (`positionalsSeen`, `hasExplicitScript`, `pendingConsumption` are untouched). Measured — every case in that block is unchanged before and after |
| A positional is spent by any node the shell passes as a word                                                                                          | [#823]                                                 | `"a pattern positional the parser does not type as an argument (#823)"` (lines 273–309)                                                          | Yes, same reason; measured unchanged                                                                                                                                                                          |
| The table lists a flag as consuming only where it consumes on every supported platform                                                                | [#823]                                                 | `"flag spellings a pattern-first command accepts (#823)"` (lines 310–471)                                                                        | Yes — no table entry changes                                                                                                                                                                                  |
| A prefix-position substitution's operands are projected while a prefix assignment's literal value stays out                                           | [#742]                                                 | `"a command hosted in a prefix position (#742)"` (line 500)                                                                                      | Yes — the `COMMAND_PREFIX_TYPES` branch is untouched and `continue`s before the new call                                                                                                                      |
| A heredoc body's prose stays out of the path surface while its substitution's operands enter it                                                       | [#741]                                                 | `"operands hosted in a heredoc body (#741)"` (line 661)                                                                                          | Yes — `heredoc_body` is not in `ARG_NODE_TYPES`, so the guard never fires for it                                                                                                                              |
| A nested execution's tokens keep their own command's attribution                                                                                      | [#741] / [#807]                                        | `describe("effect attribution")` (line 960)                                                                                                      | Yes, and newly extended: the measured `read`/`core` on `/etc/shadow` under a non-core `sed` is a fresh assertion of it                                                                                        |
| An operand-side argument node is both read and searched for hosted executions                                                                         | [#839], `history/phase-14-capability-axis.md` line 631 | `describe("statement operands")` (line 718)                                                                                                      | Yes — this change makes the two command walkers match what the statement walker already does, which is the invariant generalizing rather than regressing                                                      |

Quantitative invariant: the corpus diff in Design Overview — 15 `external_directory` tokens gained, 20 `path` tokens gained, **0 lost on either surface**, 0 commands newly asking.
Re-run the diff after implementation.

Constituency check: the projection's consumers are the `path` and `external_directory` gates and the ask dialog's evidence list.
The gates gain 11 real paths they could not previously decide on.
The dialog gains at most one evidence line per affected command, all on prompts that were already opening.
Neither constituency loses anything — the measured loss count is zero on both surfaces.

## TDD Order

1. **`test:` Give the generic-commands block the `tokensOf` shorthand its sibling already has.**
   Test surface: `test/access-intent/bash/token-collection.test.ts`, `describe("collectCommandTokens — generic commands")` (line 472).
   Add a block-local `async function tokensOf(cmd: string): Promise<string[]>` mirroring the one in the pattern-first block (line 126) — `parseCommandNode`, `commandTokens`, `tree.delete()` in a `finally`.
   No behavior change and no existing test body changes; the suite must stay green.
   This is the Tidy-First preparation: step 4's new generic-command cases land in this block, and without the helper each would carry its own `try`/`finally` boilerplate against the file's own established shorthand.
   Killing mutation: none — a pure refactor with no new assertion.
   Commit: `test(pi-permission-system): add a tokensOf helper to the generic-commands block`.

2. **`fix:` A command hosted in a quoted argument of a pattern-first command keeps its operands.**
   Test surface: `test/access-intent/bash/token-collection.test.ts`.
   New cases in the existing `"a consumed flag argument, whatever node type it is (#823)"` block for the `script`, `value`, `script-file`, and declined-`suffix` classes, plus a new nested `describe("an execution hosted in a quoted positional or operand (#945)")` under the pattern-first block for the pattern-positional and ordinary-operand classes, plus the single-quoted control.
   Expected values are the `After` column of the Test Impact Analysis table.
   Green by adding the guarded `collectHostedExecutionTokens(child)` call to `collectPatternCommandTokens`.
   Killing mutation: delete that pushed call — all six new pattern-first cases go red; the single-quoted control and every existing case stay green.
   Second mutation, for the attribution class: make the new call push `{ token, effect }` pairs re-stamped with the enclosing `effect` (e.g. `.map((t) => ({ ...t, effect }))`) — the `sed -e "$(cat /etc/shadow)"` attribution assertion goes red (`read`/`core` becomes `unproven`) while the token-list assertions stay green.
   Commit: `fix(pi-permission-system): project the operands of a command hosted in a quoted argument`.

3. **`fix:` A command hosted in a quoted argument of a generic command keeps its operands.**
   Test surface: `test/access-intent/bash/token-collection.test.ts`, a new nested `describe("an execution hosted in a quoted argument (#945)")` under the generic-commands block, using step 1's `tokensOf`.
   Covers `echo "$(cat /etc/shadow)"` and the `concatenation` case `cat "prefix$(cat /etc/shadow)"`.
   Green by adding the guarded call to `collectGenericCommandTokens`.
   Killing mutation: delete that pushed call — both new cases go red; step 2's pattern-first cases stay green, which is what proves the two walkers are independently pinned.
   Split from step 2 deliberately: the two walkers are different state machines, and a single commit covering both would let either line be deleted with only half the suite reacting.
   Commit: `fix(pi-permission-system): project the operands of a command hosted in a generic command's quoted argument`.

4. **`test:` Pin the end-to-end chain through `BashProgram`.**
   Test surface: `test/access-intent/bash/program.test.ts`, a new `#945` block.
   Asserts that `sed -e "$(cat /etc/shadow)" f.txt` puts `/etc/shadow` in `externalAccesses()` — the collector's token surviving classification, base resolution, and the outside-cwd boundary decision.
   This is characterization of behavior steps 2–3 already delivered, so it must pass on first run; that makes it an invariant pin rather than a red step.
   Killing mutation: revert either of steps 2–3's pushed calls — the new block goes red, confirming the pin is not vacuous.
   Commit: `test(pi-permission-system): pin a quoted substitution's operands reaching externalAccesses`.

5. **`docs:` Update the module entry and mark the roadmap step complete.**
   `docs/architecture/architecture.md`: widen the `token-collection.ts` entry's hosted-execution sentence to name a quoted argument in both walkers; rewrite the `#### [#945]` step's `Cause` / `Target` / `Outcome` / `Impact` for the widened scope, replacing the "0 of 5922" population line with the measured corpus figures; add `✅` to the step heading and to the `S945` Mermaid node.
   Verify by re-running the corpus diff and confirming the numbers written match what it prints.
   Commit: `docs(pi-permission-system): record the widened hosted-argument projection`.

## Risks and Mitigations

| Risk                                                                                                                  | Mitigation                                                                                                                                                                                                                                                                                                                                                                         |
| --------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| The added call double-visits a node the existing recursion already reaches, duplicating tokens                        | Both calls are guarded by `ARG_NODE_TYPES`, and every existing recursion site in both walkers is on the complementary `!isArgNode` path. Verified by measurement, not argument: the `After` column shows no duplicated token, and the 4511-test suite is green under the spike                                                                                                     |
| The search reads argument *text* rather than argument *executions*, turning a quoted string's content into candidates | `collectHostedExecutionTokens` descends only `NESTED_EXECUTION_CONTEXTS` members and reads no text of its own. The single-quoted control (`grep -e '$(cat /etc/shadow)' f.txt` -> `["f.txt"]`) is the test for this specific failure, and it is measured unchanged                                                                                                                 |
| Token reordering changes which effect a path carries                                                                  | `BashPathResolver` dedups by resolved path and merges attributions through `mergeTokenEffects`, so order cannot decide an effect. Read at `bash-path-resolver.ts` `projectRuleCandidates` and `recordExternal`                                                                                                                                                                     |
| A user sees a new prompt on upgrade                                                                                   | Measured: 0 of 7653 corpus commands gain a prompt shape — all 13 affected commands already carried an `external_directory` candidate. This bounds observed frequency, not reachability: a user whose command's only strict candidate is a newly-gained one would see a new ask. Classified `fix:` on the [#741] / [#742] precedent, with the population stated in the release note |
| [#859]'s revision-range noise grows                                                                                   | 4 of the 15 gained strict tokens are rev-ranges. They ride prompts that were already opening, so the cost is evidence lines rather than asks, and [#859] is the next Track A step                                                                                                                                                                                                  |
| The roadmap step's text is left describing the narrow branch                                                          | Step 5 rewrites it in the same commit that marks it `✅`, per the package skill's rule against deferring the marker to `/ship`                                                                                                                                                                                                                                                     |

## Open Questions

None.
The scope question — narrow branch versus whole class — was settled at the planning gate with the corpus measurement in hand.
No follow-up issue is owed: the residual this change leaves (the argument's own raw substitution text still emitted as a token) is already owned by [#609].

[#306]: https://github.com/gotgenes/pi-packages/issues/306
[#609]: https://github.com/gotgenes/pi-packages/issues/609
[#741]: https://github.com/gotgenes/pi-packages/issues/741
[#742]: https://github.com/gotgenes/pi-packages/issues/742
[#807]: https://github.com/gotgenes/pi-packages/issues/807
[#823]: https://github.com/gotgenes/pi-packages/issues/823
[#839]: https://github.com/gotgenes/pi-packages/issues/839
[#859]: https://github.com/gotgenes/pi-packages/issues/859
[#863]: https://github.com/gotgenes/pi-packages/issues/863
[#880]: https://github.com/gotgenes/pi-packages/issues/880
[#881]: https://github.com/gotgenes/pi-packages/issues/881
