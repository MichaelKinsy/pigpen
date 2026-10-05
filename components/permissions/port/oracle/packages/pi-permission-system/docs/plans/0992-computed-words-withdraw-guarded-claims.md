---
issue: 992
issue_title: "pi-permission-system: a computed argument can spell a find/fd/sort withdrawing option the retraction guard never sees"
---

# A computed argument that may spell an option withdraws `find`'s, `fd`'s, and `sort`'s read claim

## Release Recommendation

**Release:** ship independently

The roadmap tags this step `Release: independent`, outside the "declared-effects" batch.
The guard step is `fix!:` (operator decision; see Goals), so the release is a major bump of `pi-permission-system`.

## Problem Statement

The pure-reader core admits `find`, `fd`, and `sort` as readers until an argument names a write-capable option, and the option guards in `RETRACTION_GUARDS` (`src/access-intent/bash/command-effects.ts`) compare each argument's quote-resolved text against those spellings.
An argument whose value the shell decides — a variable, a command substitution, a glob — never matches, so it can carry `-delete` or `-o` past the guard.

Under `external_directory_read: {"*": "allow"}` with `external_directory_write` at `ask`, a user sees:

1. The agent runs `A=-delete; find ~/other $A`.
2. No prompt appears: the operand `~/other` resolves on `external_directory_read` alone.
3. `find` receives `-delete` and removes the files under `~/other`.

`O=-o; sort "$O" ~/other/out in` writes `~/other/out` the same way.
Since [#924] every argument word already carries `ArgWord.computed`, and `sed`/`awk` withdraw on any computed word; the three option guards ignore the flag.

## Goals

- A computed argument that may reach `find`, `fd`, or `sort` as a word beginning with `-` withdraws the command's read claim, so its operands consult both surfaces.
  A computed argument that provably cannot begin with `-` (`"x$y"`, `packages/*/docs`, `\(`) leaves the claim standing.
- A `tree-sitter-bash` `number` node (`2` in `-maxdepth 2`, `-20`) counts as spelled exactly, so a digit argument is not computed.
- **Breaking** (`fix!:` with a `BREAKING CHANGE:` footer, operator decision): on upgrade with no config edit, a `find`/`fd`/`sort` whose computed argument may lead with `-` and whose operand sits where `_read` and `_write` disagree now prompts where it ran silently — for example `find "$dir" -name x` against an external `$dir` under `external_directory_read: allow` / `external_directory_write: ask`.
  The roadmap's `Commit type: fix:` for this step is corrected to `fix!:`, matching [#609]'s precedent for a change that newly prompts.

## Non-Goals

- `sed` and `awk` keep the any-computed rule: a computed word there can be the script or program itself, which no leading-character test bounds.
- Teaching each guard which positions take an option value (`find -name X`, `sort -k N`) was priced and rejected at the gate: a per-guard grammar for a narrower relief than the leading-dash rule, measured below.
- A reassigned `$HOME`/`$PWD` (`HOME=-delete; find "$HOME"`) resolves to its startup value, so the word is not computed and this rule cannot see it; that is the variable vocabulary's gap, filed as [#995] and placed as the next roadmap step.
- Resolving an escape or an ANSI-C string to the value the program receives (`\(` → `(`, `$'\t'` → a tab) in `resolveNodeText`: it would change the value every path token carries, far outside the guards.
  The leading-dash predicate reads the escape structurally instead.
- `scripts/measure-core-coverage.mjs`'s transcribed guard table stays frozen: it pins the roster it measured, by its own header.

## Background

- `node-text.ts` — `readArgWord(node)` → `ArgWord { value, computed }`; `computed` is `hasComputedPart(node) || !isSpelledExactly(node)`.
  `isSpelledExactly` switches over node types and answers `false` for any type it does not list, which is why a `number` node is computed today.
- `command-effects.ts` — `proveCommandEffect(headWord, argWords)` looks up the word's `ClaimWithdrawal` in `RETRACTION_GUARDS`; `find`/`fd`/`sort` use `optionGuard(guard)`, which calls `retractsClaim(value, guard)` per word; `sed`/`awk` use their own predicates, which open with `argWords.some(({ computed }) => computed)`.
- Two consumers build argument words, both through `readArgWord`: `token-collection.ts`'s `commandArgumentWords` (the path tokens' effect), and `command-enumeration.ts`'s `readCommandUnit`, whose `CommandWord extends ArgWord` reaches `wrapper-analysis.ts`'s `isTransparentWrapper` → `proveCommandEffect` (the indirection-floor exemption).
  A field added to `ArgWord` in `readArgWord` flows through both spreads with no edit there.
- Every guarded option begins with `-`: `find`'s nine exact words, `fd`'s `-x`/`-X`/`--exec`/`--exec-batch`, `sort`'s `-o`/`--output`.
- The package skill: default to least privilege, and prefer a config pattern over a runtime mechanism — this change adds no config; it corrects a proof.

## Design Overview

### Amended during implementation

Pre-completion review found three words that split inside double quotes, so the predicate below was widened before shipping: a quoted `"x$@"` / `"x${arr[@]}"`, an indirect `"x${!a}"`, and finally any quoted variable, which may be a nameref (`declare -n s='arr[@]'`).
Every quoted parameter expansion now counts as possibly splitting, so `"x$y"` **withdraws** the claim wherever this plan says it reads; the operator chose that over documenting the limit, at a measured cost of 3 more `find` units.
ADR 0013's 2026-09-29 amendment and `docs/configuration.md` describe the shipped rule.

### How the evidence was produced

A disposable Vitest spike (not committed) read 70,961 unique bash commands from real artifacts: every `bash` tool call in the 1,867 session transcripts under `~/.pi/agent/sessions/`, plus every `command` in the local review log.
It parsed each with the production `getParser()`, enumerated every `command` node with its own tree walk (independent of the collectors), read argument words exactly as `commandArgumentWords` does (every named child but the prefix and a redirect, through `readArgWord`), and called the real `proveCommandEffect`.
The rule variants were applied in the spike, beside the unmodified production code; n = 1 per command, since the parser is deterministic and uncached.
No control was needed beyond the current rule itself, which is the "today" column.

Measured, `find` units proven read today: 2,119 of 2,285; `sort` 1,504 of 1,507; `fd` 17 of 19.
Units newly withdrawn by each rule:

| Rule                                                                     | `find` | `sort` | `fd` |
| ------------------------------------------------------------------------ | ------ | ------ | ---- |
| Any computed word withdraws                                              | 625    | 3      | 4    |
| Any computed word, digits spelled exactly                                | 165    | 2      | 3    |
| A computed word that may lead with `-`, digits spelled exactly (adopted) | 96     | 2      | 2    |

Of those, newly withdrawn `find` units whose operand text begins with `/`, `~`, or `$HOME` — an estimate of the ones that can reach an external-directory ask, not a resolution against a cwd: 17 under the middle rule, 3 under the adopted one.
None of the 96 was an actual write; each used a variable or glob for an ordinary reason (`find "$pkg/src" -name '*.ts'`), so the user-visible effect in practice is new prompts, and the protection is against an agent passing an option through a variable.
Across all `number` nodes in the corpus (about 79,000), none had a child, and parents were `command`, `file_redirect`, `for_statement`, and similar.

### Step A — a digit is spelled exactly

`isSpelledExactly` gains `case "number": return childrenSpelledExactly(node);`.
The grammar's `number` can carry an expansion child (`10#$x`); `hasComputedPart` already reports that as computed, and `childrenSpelledExactly` answers `false` for it too, so the case is fail-closed on its own.
The value is `node.text` (`resolveNodeText`'s default), which is what bash passes: a number is an ordinary word to the shell.
This also relieves `sed`/`awk` of a digit argument (measured: 1 `awk` unit in the corpus).

### Step B — `ArgWord.mayLeadWithDash`

```typescript
export interface ArgWord {
  readonly value: string;
  readonly computed: boolean;
  /**
   * Whether the program may receive this argument, or a word split from it,
   * beginning with `-` — the shape every guarded option has.
   */
  readonly mayLeadWithDash: boolean;
}

export function readArgWord(node: TSNode): ArgWord {
  const value = resolveNodeText(node);
  const computed = hasComputedPart(node) || !isSpelledExactly(node);
  return {
    value,
    computed,
    mayLeadWithDash: computed ? mayExpandToDashWord(node) : value.startsWith("-"),
  };
}
```

A non-computed word is known exactly, so the field is the plain fact.
For a computed word, the private `mayExpandToDashWord(node)` (placed below `readArgWord`) answers `true` when either holds:

1. **Word splitting.**
   An unquoted `simple_expansion`, `expansion`, `command_substitution`, or `arithmetic_expansion` sits anywhere in the word outside a double-quoted `string`, since its result can split into a new word of any shape.
   The walk does not descend into a `command_substitution` or `process_substitution` body, whose words belong to the inner command.
2. **An undetermined leading character.**
   The first character the source spells (after an opening `"`) is `-`; a non-literal part (an expansion, a substitution, an ANSI-C `$'…'` string, a backtick); an unescaped `*`, `?`, `[`, or `{` (a glob or brace can produce any leading character); or an escape `\` followed by `-` or nothing.

Otherwise `false`: a literal leading character that is not `-` survives globbing, brace expansion, and escape removal, because each of those preserves a literal prefix.
The field is required, not optional — an absent field reading as falsy would be the fail-open direction.

Prototype outcomes, recorded from the spike's implementation of this predicate over `find . <arg>` (`computed` / `mayLeadWithDash`):

| Argument                 | Computed                        | May lead with `-` |
| ------------------------ | ------------------------------- | ----------------- |
| `$A`                     | true                            | true              |
| `"$O"`                   | true                            | true              |
| `$(echo -o)`             | true                            | true              |
| `` `echo -o` ``          | true                            | true              |
| `"$pkg/src"`             | true                            | true              |
| `x$y` (unquoted, splits) | true                            | true              |
| `""$x`                   | true                            | true              |
| `'x'$y`                  | true                            | true              |
| `*`                      | true                            | true              |
| `-*`                     | true                            | true              |
| `\-delete`               | true                            | true              |
| `{-delete,}`             | true                            | true              |
| `$'-o'`                  | true                            | true              |
| `-t$'\t'`                | true                            | true              |
| `"x$y"`                  | true                            | false             |
| `packages/*/docs`        | true                            | false             |
| `\(`                     | true                            | false             |
| `x{a,-b}`                | true                            | false             |
| `~/*.ts`                 | true                            | false             |
| `<(cmd)`                 | true                            | false             |
| `<(cmd $a)`              | true                            | false             |
| `"$HOME/x"`              | false                           | false             |
| `2`                      | true before Step A, false after | false             |
| `-20`                    | true before Step A, false after | true              |

### Step C — the option guards read the field

```typescript
function optionGuard(guard: RetractionGuard): ClaimWithdrawal {
  return (argWords) =>
    argWords.some((word) =>
      word.computed ? word.mayLeadWithDash : retractsClaim(word.value, guard),
    );
}
```

A computed word's `value` is not what the program receives, so matching it against option spellings is meaningless; the only sound question is whether it could be an option at all.
A literal word keeps today's spelling match, so `find . -name -delete.txt` still reads.
`sed`/`awk` are untouched.
The wrapper path (`isTransparentWrapper`) inherits the rule through `proveCommandEffect`, so `xargs find . $A` keeps its floor.

### Consumer-side check

`optionGuard` asks each word one question (Tell-Don't-Ask on data): the word carries the fact, and the guard never reaches into a node.
`mayLeadWithDash` is a fact about the word, not about options, so [#880]'s `unlessOption` stems can reuse it when that step re-enters `command-effects.ts`.
No import edge is added: `command-effects.ts` already imports the `ArgWord` type from `node-text.ts`.

## Module-Level Changes

- `src/access-intent/bash/node-text.ts` — `isSpelledExactly` gains the `number` case (Step A); `ArgWord` gains `mayLeadWithDash`, `readArgWord` sets it, and the private `mayExpandToDashWord` lands below it; the `ArgWord.computed` doc comment's list of rewrites is unchanged (Step B).
- `src/access-intent/bash/command-effects.ts` — `optionGuard` reads `mayLeadWithDash` for a computed word; the doc comments on `proveCommandEffect`, the `RetractionGuard` block, and `RETRACTION_GUARDS` say a computed word that may lead with `-` withdraws (Step C).
- `test/helpers/arg-words.ts` (new) — `literalArgWords(...values)` builds exactly-spelled words; after Step B it also sets `mayLeadWithDash: value.startsWith("-")`, the same rule `readArgWord` applies to a literal word.
- `test/access-intent/bash/sed-invocation.test.ts`, `awk-invocation.test.ts`, `command-effects.test.ts` — the local `literal()`/`prove()` builders use `literalArgWords`; the hand-written computed literals (`{ value: "$range", computed: true }`, 5 sites across the three files) gain `mayLeadWithDash: true`.
- `test/access-intent/bash/wrapper-analysis.test.ts` — the `argWordOf` stand-in sets `mayLeadWithDash: computed || value.startsWith("-")`, the conservative reading; no case there passes a computed guarded option (verified by the Tidy-First assessor), so none flips.
- `test/access-intent/bash/node-text.test.ts` — the exact-spelling table's `toEqual({ value, computed: false })` rows gain `mayLeadWithDash` (true only for the `-'i'""` row, whose value is `-i`); digit rows join it; a new `describe` pins the predicate table above.
- `test/access-intent/bash/token-collection.test.ts`, `test/access-intent/bash/program.test.ts`, `test/handlers/gates/bash-external-directory.test.ts` — new cases only, through the real parse (no `ArgWord` literals there).
- Predicted unchanged: `token-collection.ts`, `command-enumeration.ts`, `wrapper-analysis.ts`, `sed-invocation.ts`, `awk-invocation.ts` — each obtains words through `readArgWord` or receives them typed, and none constructs an `ArgWord` literal (`grep -rn "computed:" src/` matches only `node-text.ts`).
- `docs/configuration.md` — § pure-reader core: after the `find`/`fd`/`sort` table, a sentence that an argument whose value the shell decides withdraws the claim when it may arrive beginning with `-` (`$opt`, `"$O"`, `*`), with `"x$y"` and `packages/*/docs` as examples that leave it standing.
- `docs/decisions/0013-permission-policy-model.md` — §7's "Stem matching is fail-closed over option forms" bullet gains the computed-word clause (a dated amendment line, as [#924]'s was).
- `docs/architecture/architecture.md` — the `node-text.ts` entry names `mayLeadWithDash` and the `number` spelling; the `command-effects.ts` entry states the option guards' computed-word rule; this issue's roadmap step gains `✅` on its heading and Mermaid node plus a `Landed:` note; its `Commit type:` and the Release batches entry change to `fix!:`.

## Test Impact Analysis

1. New tests the change enables: a direct table over `readArgWord(...).mayLeadWithDash` for every shape class, which pins the predicate without a guard in between.
2. Redundant tests: none; the existing literal-spelling guard tests (`-delete.txt` stays read, clustered `-uo`) still exercise the literal branch Step C keeps.
3. Tests that stay as-is because they exercise the layer: the `sed`/`awk` computed tests (the any-computed rule is intentionally untouched), and `program.test.ts`'s quoted-option wrapper table (quote removal is literal, not computed).

## Invariants at risk

From [#924]'s `Landed:` note and this issue's roadmap `Outcome:`:

- `sed`/`awk` withdraw on any computed word — pinned by `sed-invocation.test.ts` > "a computed argument" (`$range`, `$f`) and `awk-invocation.test.ts` > "a computed argument"; both call the predicate directly, so a Step C edit leaking into them would turn them red.
  Step B's fixture edit adds `mayLeadWithDash: true` there; add one case per file with a computed word whose `mayLeadWithDash` is `false` (`"x$y"`) that still withdraws, so the invariant is pinned independently of the new field.
- A quoted withdrawing option keeps a wrapped `find`/`fd`/`sort` floored — `program.test.ts` > "a withdrawing option spelled with quotes"; the quoted word is not computed, so it rides the literal branch, unchanged.
- `find -exec … {} +`'s empty brace stays exact — `node-text.test.ts`'s `{}` row; unchanged.
- `find . -name '*.ts'` still proves a read (roadmap `Outcome:`) — `raw_string` is spelled exactly; TDD step 4 adds the case through the real parse in `token-collection.test.ts`, since `command-effects.test.ts`'s `prove()` bypasses parsing.

## TDD Order

1. **`test(pi-permission-system): build literal argument words through one helper`** Add `test/helpers/arg-words.ts` with `literalArgWords(...values: string[]): ArgWord[]` returning `{ value, computed: false }`; replace the local `literal()` in `sed-invocation.test.ts` and `awk-invocation.test.ts` and the map inside `command-effects.test.ts`'s `prove()`.
   Prepares Step 3's required field: one builder gains it instead of three.
   No behavior change; the suite stays green.
2. **`fix(pi-permission-system): a digit argument no longer withdraws sed's or awk's read claim`** Red: `node-text.test.ts`'s exact table gains `["a number", "2", "2"]` and `["a negative number", "-20", "-20"]` (expecting `computed: false`); `token-collection.test.ts` gains `sed -n 1p 2` (a file named `2`), attributing every token as `{ effect: "read", source: "core" }` where today the digit withdraws `sed`'s claim and every token reads `retracted`.
   Green: the `number` case in `isSpelledExactly`.
   Killing mutation: delete `case "number":` from `isSpelledExactly` — kills both table rows and the `sed -n 1p 2` attribution.
3. **`refactor(pi-permission-system): carry whether an argument may lead with a dash`** Red: a new `describe("whether a word may lead with a dash")` in `node-text.test.ts` over `readArgWord` with every row of the prototype table (post-Step-A values), asserting `{ computed, mayLeadWithDash }` with `toEqual` on those two fields plus `value`.
   Green: `ArgWord.mayLeadWithDash`, `readArgWord`, `mayExpandToDashWord`; update `literalArgWords` (`mayLeadWithDash: value.startsWith("-")`), the 5 computed literals (`true`), the `wrapper-analysis.test.ts` stand-in, and the exact table's expected objects.
   `refactor:` because no guard reads the field yet, so nothing user-visible changes.
   Run `pnpm --filter @gotgenes/pi-permission-system run check` right after: the field is required on a shared interface.
   Killing mutations, one per class:
   - Make the word-splitting walk return `false` — kills `x$y`, `""$x`, `'x'$y`.
   - Treat a non-literal leading part as safe — kills `$A`, `"$O"`, `$(echo -o)`, `` `echo -o` ``, `"$pkg/src"`, `$'-o'`.
   - Drop `*?[{` from the undetermined leading characters — kills `*`, `{-delete,}` (`-*` stays true through the `-` rule).
   - Treat a leading `\` as literal whatever follows — kills `\-delete`.
   - Make `mayExpandToDashWord` return `true` unconditionally — kills `"x$y"`, `packages/*/docs`, `\(`, `x{a,-b}`, `~/*.ts`, `<(cmd)`, `<(cmd $a)`.
   - Let the walk descend into substitution bodies — kills `<(cmd $a)`.
   - Set a literal word's field to `false` — kills the exact table's `-'i'""` row and `-20`.
4. **`fix(pi-permission-system)!: a computed argument that may spell an option withdraws find's, fd's, and sort's read claim`** Red, unit (`command-effects.test.ts`, per guard): `find . $A`-shaped words (`{ value: "$A", computed: true, mayLeadWithDash: true }`) retract for `find`, `fd`, and `sort`; a computed word with `mayLeadWithDash: false` keeps `CORE_READ` for each.
   Red, real parse (`token-collection.test.ts`): `A=-delete; find /etc $A` attributes `/etc` as `retracted`; `find "$dir" -name x` retracts; `find /etc -name "x$y"` and `find /etc -maxdepth 2 -name '*.ts'` stay `core` reads; `sort "$O" /etc/out in` retracts.
   Red, wrapper (`program.test.ts`): `xargs find . $A` is not exempt; `xargs find . "x$y"` stays `core-reader`.
   Red, gate (`bash-external-directory.test.ts`): `A=-delete; find /outside $A` records `effectSource: "retracted"`, beside the existing literal `-delete` case.
   Green: `optionGuard` per Step C; update the doc comments.
   Commit footer: `BREAKING CHANGE: a find, fd, or sort whose argument is computed and may begin with "-" (such as $opt, "$dir", or *) now consults both the read and write surfaces for its operands, so an operand where they disagree prompts where it previously resolved on the read surface alone.` Killing mutations:
   - Revert `optionGuard` to `retractsClaim(word.value, guard)` for every word — kills every retract row.
   - Change the condition to `word.computed || retractsClaim(...)` — kills the unit rows with `mayLeadWithDash: false` and the real-parse `find /etc -name "x$y"` row.
   - Delete the `sort` entry's `optionGuard` wiring (map it to `() => false`) — kills the `sort "$O"` row, proving each guard is covered.
5. **`docs(pi-permission-system): document the computed-word rule for find, fd, and sort`** `docs/configuration.md`, ADR 0013 §7 amendment, the two `architecture.md` module entries, this issue's roadmap step `✅` (heading and Mermaid node), its `Landed:` note, and `fix!:` in the step and the Release batches list.
   Verify: `pnpm exec rumdl check` on each file; `grep -n "#992" docs/architecture/architecture.md` shows the `✅` on both the heading and the node.

## Risks and Mitigations

- **A leading-character rule that misses a shape is a fail-open.**
  Every rewrite the shell applies before the program sees a word — expansion, splitting, globbing, brace expansion, escape and quote removal — either is listed as undetermined or preserves a literal prefix; Step 3 pins one row per class and names a killing mutation for each, and the unknown-structure default in the walk is `true`.
- **A test double drifting from `readArgWord`.**
  `literalArgWords` applies `readArgWord`'s literal rule (`value.startsWith("-")`) rather than a hand-picked constant, and the `wrapper-analysis.test.ts` stand-in reads conservatively (`computed || …`).
- **New prompts surprise a user.**
  Measured at 3 external-looking `find` units in about 71,000 commands; the `BREAKING CHANGE:` footer and the configuration paragraph name the shape that prompts and the ones that do not.
- **A reassigned `$HOME` defeats the rule** — out of scope here, filed as [#995] and sequenced directly after this step.

## Open Questions

- None blocking.
  Whether [#880]'s `unlessOption` reuses `mayLeadWithDash` is that step's call.

[#609]: https://github.com/gotgenes/pi-packages/issues/609
[#880]: https://github.com/gotgenes/pi-packages/issues/880
[#924]: https://github.com/gotgenes/pi-packages/issues/924
[#995]: https://github.com/gotgenes/pi-packages/issues/995
