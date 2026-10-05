---
issue: 924
issue_title: "pi-permission-system: sed/awk are unconditionally excluded from the pure-reader core, so print-only invocations still consult external_directory_write"
---

# `sed` and `awk` are read-only until the command line withdraws the claim

## Release Recommendation

**Release:** ship independently

The Phase 15 roadmap step for this issue (the `#### [#924]` step, "sed and awk are read-only until an argument withdraws the claim") is tagged `Release: independent` and belongs to no batch.
It ships as `fix:` commits and regresses no existing relief, so there is nothing to wait for.

## Problem Statement

The pure-reader core in `src/access-intent/bash/command-effects.ts` excludes `sed` and `awk` outright, because their program text and `-i` flag can write.
So a plainly read-only `sed -n '1,80p' ~/other/SKILL.md` attributes its operand to both directional surfaces and takes the more restrictive answer.
Under the configuration [#800] recommends (`external_directory_read: {"*": "allow"}`, `external_directory_write` at `ask`), `cat` on the same file is silent while `sed -n` prompts.
In the reporter's session, two skills outside the project loaded silently with `cat` and a third prompted because the agent paged it with `sed -n 1,80p`.

## Goals

- `sed` and `awk` join the pure-reader core as presumed readers whose claim the command line withdraws unless the package can **prove** the invocation read-only.
- The proof is an allowlist, not a denylist: an option, script command, or program construct the prover does not recognize withdraws the claim.
- Any computed argument word (a variable expansion other than a plain `$HOME`/`$PWD`, a command substitution, an arithmetic expansion) withdraws the claim, since it can spell `-i`.
- Fix the wrapper-path quoting bypass found while planning: `isTransparentWrapper` hands `proveCommandEffect` each word's raw source text, so `xargs find . '-delete'` and `xargs sort '-o' /tmp/x` are exempted from the indirection floor (measured).
  Both paths hand the core quote-resolved words after this change.
- Not breaking: the change only moves `sed`/`awk` operands from "both surfaces" to "`_read` alone" when proven, and re-floors two wrapped spellings that were wrongly exempt.
  The bypass fix newly prompts on `xargs find . '-delete'`, which is the correct verdict for a deletion and was never documented behavior.

## Non-Goals

- `gawk`, `nawk`, `mawk`, and `gsed` stay outside the core (operator decision); the roster grows by exactly `sed` and `awk`.
- A computed argument to `find`/`fd`/`sort` (`A=-delete; find ~/x $A`) still proves a read — [#992] owns that, as its own Phase 15 step directly after this one, because it newly prompts on `find . -name "$pat"`.
- The write target a `sed` `w` command or an `awk` `print >` names is not collected as a path token; it never was (the script positional is skipped by the pattern-first walker), and ADR 0009's script-content residual is unchanged.
- `commandEffects` user declarations ([#880]) and wrapper-keyed transparency ([#963]) are later steps.
- `scripts/measure-core-coverage.mjs` keeps its transcribed 21-word roster: it is pinned so a re-run is comparable to its recorded figures, and a head-word-only count would over-credit `sed -i`.
  Its header claims a drift check in `command-effects.test.ts` that does not exist (tidy-first assessor, verified by grep); that is out of scope here.
- `docs/configuration.md`'s read-only `bash` allowlist recipe (the paragraph naming `sort -o`, `sed -i`, and in-place `awk` redirects) is unchanged: it is about `bash` rules, and the core decides direction only.

## Background

- `proveCommandEffect(headWord, argWords: readonly string[])` is the single entry point for the core.
  Its two production callers are `collectCommandTokens` in `token-collection.ts` (via `commandArgumentWords`, which returns `resolveNodeText` per argument node) and `isTransparentWrapper` in `wrapper-analysis.ts` (which passes `CommandWord.text`).
- `CommandWord` is `{ text, offset }`, built once in `command-enumeration.ts:readCommandUnit` from the raw node `.text`, quotes included.
  That is correct for its other consumers (unwrapping, text slicing), and wrong for the core, which matches option spellings.
- `RETRACTION_GUARDS` maps `find`/`fd`/`sort` to option-spelling guards; `proveCommandEffect` runs `argWords.some(word => retractsClaim(word, guard))` itself.
  A withdrawn claim yields `{ effect: "unproven", source: "retracted" }`.
- `hasComputedPart(node)` and `resolveNodeText(node)` in `node-text.ts` are the two facts an argument word needs; `commandArgumentWords` reads only the second today.
- The pattern-first walker already knows `sed`'s script positions (`SED_CONFIG`) for path collection; that table decides which words are path candidates and is not touched.
- A core word also lifts the indirection-wrapper floor (ADR 0013 §11), so admitting `sed`/`awk` makes `xargs sed -n p` and `find … -exec sed -n 1p {} +` resolve by their own `bash` rules.
- A `src/` module with no importer fails `fallow dead-code` (`unused-files: error`), so each prover module lands in the step that wires it.

## Design Overview

### The observed scenario

With `external_directory_read: {"*": "allow"}` and `external_directory_write: {"*": "ask"}`:

| Command                                        | Today                       | After                |
| ---------------------------------------------- | --------------------------- | -------------------- |
| `cat ~/x/SKILL.md`                             | silent                      | silent               |
| `sed -n '1,80p' ~/x/SKILL.md`                  | asks (`external_directory`) | silent               |
| `awk '{print $1}' ~/x/data`                    | asks                        | silent               |
| `sed -i 's/a/b/' ~/x/f`                        | asks                        | asks                 |
| `awk '{print > FILENAME}' ~/x/f`               | asks                        | asks (`>` withdraws) |
| `sed -n "$range" ~/x/f`                        | asks                        | asks (computed word) |
| `xargs find . '-delete'` under `find *: allow` | floor exempt (bug)          | floored              |

### The argument word

```typescript
// node-text.ts — the product of reading one argument node
export interface ArgWord {
  /** The string the shell passes after quote removal (`resolveNodeText`). */
  readonly value: string;
  /** Whether only running the command decides the value (`hasComputedPart`). */
  readonly computed: boolean;
}
export function readArgWord(node: TSNode): ArgWord;
```

`commandArgumentWords` returns `readArgWord` per argument node.
`CommandWord` becomes `interface CommandWord extends ArgWord { text; offset }`, and `readCommandUnit` fills both from the same node.
`isTransparentWrapper` passes `unwrapped.words.slice(1)` straight through, so the core sees the resolved value on both paths.
`ArgWord` lives in `node-text.ts` because that module already owns "what an argument node's value is"; `command-effects.ts`, `sed-invocation.ts`, and `awk-invocation.ts` import the type from it, and `node-text.ts` imports none of them, so no cycle forms.

### The guard table becomes word → predicate

```typescript
type ClaimWithdrawal = (args: readonly ArgWord[]) => boolean;
const RETRACTION_GUARDS: ReadonlyMap<string, ClaimWithdrawal> = new Map([
  ["find", optionGuard({ exactWords: … })],
  ["fd", optionGuard({ … })],
  ["sort", optionGuard({ … })],
  ["sed", sedWithdrawsReadClaim],
  ["awk", awkWithdrawsReadClaim],
]);
// proveCommandEffect: if (guard?.(argWords)) return RETRACTED_EFFECT;
```

`optionGuard` wraps the existing `RetractionGuard` shape and reads `.value`; it ignores `.computed` ([#992] changes that).
The call site stays one line and Tell-Don't-Ask: the core asks each word's own predicate, never a second table.

### `sed-invocation.ts` — `sedWithdrawsReadClaim(args)`

Returns `true` (withdraw) unless every check below passes.

1. **No computed word anywhere.**
   GNU `sed` permutes options, so even a trailing `"$f"` could be `-i`.
2. **Option walk (allowlist).**
   Everything up to a `--` that starts with `-` (and is not `-` alone) is an option, wherever it sits, because GNU permutes.
   Accepted: short clusters over `n E r s u z`; `e` inside a cluster ends it, taking the remainder or the next word as a script; `--expression=S` and `--expression S`; the exact long words `--quiet --silent --regexp-extended --separate --unbuffered --null-data --posix --debug --sandbox`.
   Anything else withdraws: `-i`, `-I`, `-f`, `-l`, `--in-place`, `--file`, `--version`, and every abbreviation (`--expr`), so no prefix-matching rule is needed.
   An `-e` after the first positional withdraws: GNU reads it as a script, BSD as a file, and the two disagree about which word is the script.
3. **Script selection.**
   The `-e` scripts when any exist, else the first positional; no script at all withdraws.
4. **Script grammar (allowlist).**
   Commands separated by `;`, newlines, and whitespace; `#` comments; `{`/`}` balanced.
   An optional address or address range: a number, `$`, `first~step`, `/re/`, `\cREc`, a regex followed by `I`/`M`, and a second address that may also be `+N`/`~N`; then any number of `!`.
   Commands: `p P d D n N g G h H x = z F`, `l`/`q`/`Q` with an optional number, `b`/`t`/`T`/`:` with a label to `;` or newline, `s` with flags drawn from `g p i I m M` and digits, and `y`.
   Anything else withdraws: `w W r R e v a i c L`, `s///w`, `s///e`, and any unknown character.
   A delimiter must be punctuation (not alphanumeric, backslash, space, or newline).
5. **Bracket expressions (dialect variance).**
   In a regex (an address, or `s`'s first section; never a replacement or `y` operand), a delimiter inside a bracket expression withdraws.
   BSD `sed` treats `[/]` as a bracket and GNU as a delimiter, so `s/[/]/x/w out` parses as a harmless error on one and a write on the other.
   Refusing the one shape where the two tokenizations diverge is what lets a single scan speak for both.

### `awk-invocation.ts` — `awkWithdrawsReadClaim(args)`

1. No computed word anywhere.
2. Options before the program: `-F sep`/`-Fsep`, `-v a=b`/`-va=b`, and `--`; any other option-looking word, anywhere in the argument list, withdraws (`-f`, `-e`, `-i`, `--source`, `--file`, `-W`, `--version`).
3. The program is the first positional; none withdraws.
4. The program text contains none of `>`, `|`, `system`, `@`.
   `>` covers `print >`/`>>` and also the comparison `NR>=100`, which is an accepted over-retraction; `|` covers pipes to and from commands (`|&` included); `system` covers `system()`; `@` covers gawk's `@include`/`@load`/indirect calls.
   That is every way a POSIX or GNU awk program can write a file or run a command.

### Prototype measurement

A disposable spike ran the real tree-sitter parser over the 981 distinct commands containing `sed`/`awk` in the local review log (`~/.pi/agent/extensions/pi-permission-system/logs/pi-permission-system-permission-review.jsonl`, all `session_approved` entries), found every bare `sed`/`awk` command node, built `ArgWord`s with `resolveNodeText` + `hasComputedPart`, and applied a prototype of the two provers above (measured, 2026-09-28):

| Word  | Proven read-only | Withdrawn                                                  |
| ----- | ---------------- | ---------------------------------------------------------- |
| `sed` | 913 / 1013 units | 78 `-i`, 14 computed, 3 `--version`, 1 `--file`, 4 grammar |
| `awk` | 134 / 218 units  | 29 `>`, 10 `\|`, 8 computed, 37 options (`-f`, long forms) |

Two of `sed`'s grammar withdrawals were a prototype defect: it applied the bracket rule to replacements (`s/\.[0-9]\+/[]/g`), which rule 5 above excludes.
The `awk` `>` bucket includes a real `print > ("cg-mmd-" n ".mmd")` write alongside `NR>=100` comparisons.
The corpus is the operator's own traffic, so it bounds frequency, not reachability; soundness rests on the allowlists, not on the corpus.

### Consumer check (wrapper path)

A spike through `BashProgram.parse` measured today's floor exemptions: `xargs sort -o /tmp/x` → none, `xargs sort '-o' /tmp/x` → `core-reader`, `xargs find . '-delete'` → `core-reader`, `xargs grep foo` → `core-reader`.
The second and third are the bypass step 3 closes.

## Module-Level Changes

- `src/access-intent/bash/node-text.ts` — add `ArgWord` and `readArgWord(node)`.
- `src/access-intent/bash/command-effects.ts` — `proveCommandEffect(headWord, argWords: readonly ArgWord[])`; `RETRACTION_GUARDS` becomes `ReadonlyMap<string, ClaimWithdrawal>` with `optionGuard`; `coreAdmissions()` gains a `sed`/`awk` group ("read-only until the command line says otherwise — see `sed-invocation.ts` / `awk-invocation.ts`"); the in-code exclusion table drops its `awk`/`sed` rows (`gawk`/`nawk` keep a row); the `RETRACTION_GUARDS` doc comment's "exactly why `sed` is excluded" sentence is rewritten.
- `src/access-intent/bash/sed-invocation.ts` (new) — `sedWithdrawsReadClaim`.
- `src/access-intent/bash/awk-invocation.ts` (new) — `awkWithdrawsReadClaim`.
- `src/access-intent/bash/token-collection.ts` — `commandArgumentWords` returns `ArgWord[]` via `readArgWord`.
- `src/access-intent/bash/wrapper-analysis.ts` — `CommandWord extends ArgWord`; `isTransparentWrapper` passes the words, not `.text`.
- `src/access-intent/bash/command-enumeration.ts` — `readCommandUnit` fills `value`/`computed` with `readArgWord`.
- `test/access-intent/bash/command-effects.test.ts` — local `prove()` helper; roster 23 words; `sed`/`awk` leave the "outside the core" list (`gawk` stays).
- `test/access-intent/bash/sed-invocation.test.ts`, `awk-invocation.test.ts` (new).
- `test/access-intent/bash/wrapper-analysis.test.ts` — the `words()` stand-in fills `value` (outer quotes stripped) and `computed` (a `$` outside single quotes).
- `test/access-intent/bash/program.test.ts` — floor-exemption cases for the quoted bypass and for `xargs sed -n p`.
- `test/access-intent/bash/token-collection.test.ts` — "gives an argument-hosted execution's tokens their own attribution" (`sed -e "$(cat /etc/shadow)" f.txt`) expects `f.txt` as `retracted` rather than `UNPROVEN_EFFECT`, and its comment says why (a computed script); measured as the only non-roster test that flips when `sed`/`awk` join the core.
- `test/handlers/gates/bash-effect-invariants.test.ts` — the outcome pins under a directional grant.
- `docs/configuration.md` — the roster between the `PURE_READER_CORE` markers (parity-tested, so it lands with each roster change); the exclusion sentence; the withdrawal table gains `sed`/`awk` rows and a sentence on script/program proof and computed words; the wrapper-transparency examples may name `xargs sed -n p`, with `xargs sed -i` still prompting.
- `docs/architecture/architecture.md` — `node-text.ts`, `command-effects.ts` (roster count 21 → 23, the predicate table), `wrapper-analysis.ts` (resolved words) entries; two new module-tree entries; `#### [#924]` heading and Mermaid node marked `✅` with a `Landed:` note; the `[#880]` step's constraint example `xargs sed -n keeps its floor` becomes a non-core word (`xargs git log`), since `sed -n` is now core.
- `docs/decisions/0013-permission-policy-model.md` — a dated `### Amendment` under §7: a core word may be guarded by a proof over its script or program text as well as its options, when the proof is an allowlist that fails closed on computed words and dialect-divergent shapes.
- Predicted unchanged: `token-collection.ts`'s `SED_CONFIG`/`AWK_CONFIG` (path-candidate selection is a different question), `scripts/measure-core-coverage.mjs` (Non-Goals), `.pi/skills/package-pi-permission-system/SKILL.md` (grep finds no `sed`/`awk` core claim), and the `bash-command-metamorphic` pins (a full-suite spike with `sed`/`awk` added to the roster failed only the five tests named here).

## Test Impact Analysis

1. New unit tests the change enables: the two provers are pure functions over `ArgWord[]`, testable without a parse; `readArgWord` makes the wrapper path's input testable for quote resolution.
2. Redundant tests: none; the `find`/`fd`/`sort` guard tests keep pinning the option half through `prove()`.
3. Tests that stay as they are: the `bash-effect-invariants` composition tests, which exercise the real resolver, manager, and gate; the new outcome cases join them rather than replace them.

The parser's input domain is every `sed`/`awk` shape agents write; the prototype ran over the full 981-command corpus above, and step 5's verify re-runs the committed provers over it (the spike is recreated from this plan's description, then deleted).

## Invariants at risk

- **The bare-basename rule** ([#807]): `./sed` and `/bin/sed` prove nothing.
  Pinned by `command-effects.test.ts` "the bare-basename rule"; step 4 adds `/bin/sed` and `./awk` rows.
- **A retracted core word is not floor-exempt** ([#803]): `xargs sort -o` stays floored.
  Pinned by `program.test.ts`'s floor-exemption describe; step 3 adds the quoted spellings, and steps 4–5 add `xargs sed -i`.
- **Guards read only their own word** ("a guard belongs to its own word only" describe): stays green through the predicate reshape, and step 4 adds `cat -i` → read.
- **A redirect's syntax proof is absolute**: `sed -n p f > ~/x/out` still attributes `~/x/out` as a write; `collectRedirectTokens` is untouched, and step 4's gate test pins it.
- **Wrapper metamorphic pin** (`time ${cmd}` never loosens the verdict): measured green with `sed`/`awk` in the roster.

## TDD Order

1. **Generalize the guard table to predicates.**
   `RETRACTION_GUARDS` maps each word to `(args: readonly string[]) => boolean`; `optionGuard(guard)` adapts the existing shape; `proveCommandEffect` calls `guard?.(argWords)`.
   Behavior-preserving; the existing find/fd/sort tests stay green unchanged.
   Prepares: step 4 adds entries to a table whose contract already fits them.
   Commit: `refactor(pi-permission-system): give each guarded core word a withdrawal predicate`.
2. **Route `command-effects.test.ts` through a local `prove(word, args: string[])` helper.**
   Pure call-name substitution; no assertion changes.
   Prepares: step 3's signature change edits one helper body instead of every call site.
   Commit: `test(pi-permission-system): call proveCommandEffect through one test helper`.
3. **The core reads quote-resolved words on both paths.**
   Add `ArgWord`/`readArgWord` to `node-text.ts`; `proveCommandEffect` takes `readonly ArgWord[]`; `optionGuard` reads `.value`; `commandArgumentWords` returns `readArgWord`s; `CommandWord extends ArgWord`, filled by `readCommandUnit`; `isTransparentWrapper` passes the words; the `prove()` helper maps strings to literal `ArgWord`s; `wrapper-analysis.test.ts`'s `words()` fills the new fields.
   Red: `program.test.ts` floor exemption is `undefined` for `xargs find . '-delete'`, `xargs sort '-o' /tmp/x`, and `xargs fd foo '--exec' rm`, while `xargs grep foo` stays `core-reader` (the control).
   Killing mutation: in `readCommandUnit`, set `value: word.text` — the three quoted cases go back to `core-reader`.
   Run `pnpm --filter @gotgenes/pi-permission-system run check` after the commit (shared interface).
   Commit: `fix(pi-permission-system): a quoted withdrawing option keeps a wrapped find, fd, or sort floored`.
4. **`sed` is a presumed reader: the option walk and the minimal script grammar.**
   Create `sed-invocation.ts` with rules 1–3 of the design and a grammar limited to addresses (number, `$`, `first~step`, ranges, `!`), `;`/newline separators, and the commands `p P d D n N g G h H x = z F l q Q`; everything else withdraws for now.
   Wire `["sed", sedWithdrawsReadClaim]`, add `sed` to the roster, `ROSTER`, and the `docs/configuration.md` marker block; drop `sed` from the "outside the core" list and the in-code exclusion table.
   Update the `token-collection.test.ts` attribution test (`f.txt` → `retracted`).
   Tests (`sed-invocation.test.ts`, one `describe` per class): proven — `-n '1,80p'`, `-n -e 1p -e '$p'`, `-ne 5q`, `--quiet 10,20p`, `'1d;$d'`, `-- -n`-style file after `--`; option withdrawal — `-i`, `-i.bak`, `-i ''`, `-ni`, `-I`, `--in-place`, `--in-place=.bak`, `--in`, `-f s.sed`, `--file=s.sed`, `-l 5`, `--version`, `-e p` after a positional; computed — `"$range"p`, a computed trailing file; grammar withdrawal — `w out`, `1w out`, `a text`, `r f`, `e cmd`, `v`, an unbalanced `}`.
   Gate outcome (`bash-effect-invariants.test.ts`, "a directional grant covers only its own direction"): `sed -n '1,80p' /outside/notes.md` → `allow`; `sed -i 's/a/b/' /outside/notes.md` → not `allow`, surface `external_directory`; `sed -n p /outside/a > /outside/b` → not `allow`, surface `external_directory_write`.
   Killing mutations: (a) option class — make `sedWithdrawsReadClaim` skip the option walk (treat every `-…` word as accepted): the `-i` family goes green-to-red; (b) computed class — delete the computed check: the `"$range"p` case fails; (c) grammar class — add `w` to the accepted commands: `w out` fails.
   Commit: `fix(pi-permission-system): sed that only prints resolves on the read surface`.
5. **`sed`'s full script grammar.**
   Extend the grammar with `/re/` and `\cREc` addresses (with `I`/`M`), `+N`/`~N` second addresses, `{`/`}`, `#` comments, `b t T :` labels, `s` (flags `g p i I m M` and digits; `w`/`e` flags withdraw), and `y`, plus the bracket-expression rule over regex sections only.
   Tests: proven — `-n '/x/,/y/p'`, `'s/a/b/g'`, `'s|a|b|'`, `'y/abc/xyz/'`, `'/^#/d'`, `'/start/,/end/{/skip/!p}'`, `'s/\.[0-9]\+/[]/g'` (brackets in a replacement), `'s/[[:space:]]*$//'`; withdrawn — `'s/a/b/w out'`, `'s/a/b/e'`, `'s/[/]/x/w out'` (delimiter in a bracket), `'saxayaw'` (alphanumeric delimiter), an unterminated `s/a/b`.
   Verify: recreate the corpus spike over the committed prover and confirm 913-plus of the 1013 `sed` units prove (the prototype's grammar defect is gone), then delete it.
   Killing mutations: (a) accept `w` as an `s` flag — `'s/a/b/w out'` fails; (b) drop the delimiter-in-bracket check — `'s/[/]/x/w out'` fails; (c) apply the bracket rule to replacements — `'s/\.[0-9]\+/[]/g'` fails.
   Commit: `fix(pi-permission-system): sed substitutions and regex addresses resolve on the read surface`.
6. **`awk` is a presumed reader.**
   Create `awk-invocation.ts` per the design; wire `["awk", awkWithdrawsReadClaim]`, add `awk` to the roster, `ROSTER`, and the marker block; drop it from the "outside the core" list and the exclusion table (keep `gawk`/`nawk`).
   Tests (`awk-invocation.test.ts`): proven — `'{print $1}'`, `-F: '{print $2}'`, `-F ':' '…'`, `-v n=3 'NR==n'`, `'/```mermaid/,/```/'`, `'BEGIN{f=0} f'`; withdrawn — `'{print > "out"}'`, `'{print >> FILENAME}'`, `'{print | "sh"}'`, `'"date" | getline d'`, `'BEGIN{system("rm x")}'`, `'@include "x"'`, `'NR>=100'` (accepted over-retraction), `-f prog.awk`, `-i inplace`, `--source '{print}'`, a computed program, a computed file.
   Gate outcome: `awk '{print $1}' /outside/data` → `allow`; `awk '{print > FILENAME}' /outside/data` → not `allow`, surface `external_directory`.
   Floor: `program.test.ts` — `xargs awk '{print}'` → `core-reader`, `xargs awk -f p.awk` → none.
   Killing mutations: (a) drop `>` from the forbidden set — the `print >`/`>>` cases fail; (b) drop the option allowlist — `-f`/`-i inplace` fail; (c) drop the computed check — the computed program fails.
   Commit: `fix(pi-permission-system): awk that only reads its input resolves on the read surface`.
7. **Docs.**
   `docs/configuration.md` prose (exclusion sentence, withdrawal table rows, a paragraph on script/program proof, computed words, and the BSD/GNU bracket refusal); ADR 0013 §7 amendment; `architecture.md` module entries, new tree entries, `✅` on `#### [#924]` and its Mermaid node, the `Landed:` note, and the `[#880]` constraint example.
   Recompute the metric: `grep -cE '"(sed|awk)"' packages/pi-permission-system/src/access-intent/bash/command-effects.ts` reads 0 today (measured) and is predicted at 2 or more (the two guard entries on their own lines, plus the roster line).
   Commit: `docs(pi-permission-system): document sed and awk as presumed readers`.

## Risks and Mitigations

- **A grammar hole admits a writing script.**
  The grammar is an allowlist over a closed command set, and the write/exec commands (`w W e r R v`, `s///w`, `s///e`) are named in withdrawal tests; the killing mutations prove each class is load-bearing.
- **Dialect divergence (GNU vs BSD) in which word is the script.**
  An `-e` after a positional withdraws, options are recognized wherever they sit, and a delimiter inside a bracket withdraws, so the prover never needs to know which `sed` is installed.
- **Floor lift for wrapped `sed`/`awk`.**
  `xargs sed -n p` now resolves by its own `bash` rule.
  That is §11's documented effect of core admission, and a withdrawn claim (`xargs sed -i`) keeps the floor, which step 4's `program.test.ts` case pins.
- **The bypass fix newly prompts.**
  Only for quoted withdrawing options behind a wrapper, which were wrongly exempt; no documented example relies on it.
- **`awk` over-retraction on comparisons.**
  Measured at about half of the 29 `>` withdrawals; they keep today's behavior, so this costs relief, never safety.

## Open Questions

- Whether `awk`'s `>` rule should distinguish a pattern-position comparison from a `print`/`printf` redirect.
  Defer until the review log shows the over-retraction matters; it needs awk grammar, not a character scan.

[#800]: https://github.com/gotgenes/pi-packages/issues/800
[#803]: https://github.com/gotgenes/pi-packages/issues/803
[#807]: https://github.com/gotgenes/pi-packages/issues/807
[#880]: https://github.com/gotgenes/pi-packages/issues/880
[#963]: https://github.com/gotgenes/pi-packages/issues/963
[#992]: https://github.com/gotgenes/pi-packages/issues/992
