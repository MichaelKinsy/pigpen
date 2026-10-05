---
issue: 977
issue_title: "pi-permission-system: an argument after a redirect escapes the command it belongs to, bypassing bash deny rules"
---

# The words after a redirect belong to the command

## Release Recommendation

**Release:** ship independently

The Phase 15 roadmap step for #977 is tagged `Release: independent` and belongs to no batch.
It is a `fix:`: rules newly fire only on shapes that currently get past them.

## Problem Statement

Where a redirect sits in a bash command should not change which rules apply to the command.
Bash accepts a redirect anywhere in a simple command (`git 2>/dev/null push --force` runs `git push --force`), so a user's `git push *: deny` should hold however the redirect is placed.
Today it does not.

`tree-sitter-bash` 0.25.1 declares a file redirect's target `repeat1($._literal)`, so every word after a redirect parses as another destination of it rather than as an argument of the command.
The close operators `>&-` / `<&-` take an optional destination the same way.
Three consumers read the misparse, and planning found a fourth:

1. **Command enumeration** drops the trailing words with the redirect, so the unit is `git` and a `git push *` deny never fires.
2. **Effect proofs** read only the command node's own argument words, so `find ~/x 2>/dev/null -delete` never shows the `-delete` retraction guard its flag, and `~/x` is proven a `read (core)`.
3. **Path collection** attributes every trailing word the redirect operator's effect, so `grep pat 2>/dev/null ~/x/f.txt` projects `~/x/f.txt` as a `write (syntax)`.
4. **The log masker** looks for an inline-shell payload among the command's own words, so `bash 2>/dev/null -c 'TOKEN=abc123 …'` is written to the review log unmasked.

Planning also found the same bypass without any misparse.
When the redirect sits *inside* the `command` node (a leading `2>/dev/null git push --force`, which the grammar parses correctly, or a mid-command herestring), the enumerator strips only a `variable_assignment` prefix.
So the redirect's text lands in the unit (`2>/dev/null git push --force` matches no `git push *` rule), and its node lands in the word list, where it reads as the head word.
Because of that, `>/dev/null bash -c 'rm -rf /tmp/x'` loses the opaque-wrapper floor it has without the redirect, and the masker misses its payload too.

## Goals

- A word the grammar hung on a statement-level redirect reaches every place the command's other arguments reach: the unit text, the word list the wrapper analysis and the masker read, the effect proof and its retraction guards, the pattern-first walkers, and the path projection under the command's own attribution.
- A redirect hosted inside the `command` node is excluded from the unit text and the word list, wherever it sits.
- A hosted redirect that may write a file withholds the core-reader floor exemption, exactly as a statement-level one does.
- The grammar's quirk is corrected **once**, at the parser boundary, so every walker (present and future), the salvage re-parse, and the masker read one corrected tree.
- Non-breaking.
  A command with no redirect keeps a byte-identical unit text and identical path slices; a command with a trailing redirect and no words after it is untouched.

## Non-Goals

- **A heredoc's tail.**
  A `heredoc_redirect` hosts its own argument words, redirects, and a `| …` / `&& …` statement (`cat <<EOF | rm -rf /tmp/x` enumerates as `cat`; `cat <<EOF > /tmp/o` projects no path).
  That is a different grammar production and is filed as [#979], the Phase 15 step directly after this one, which extends this plan's seam.
- **[#941]'s absorbed `-` operand.**
  `git commit -F - <<'EOF'` still enumerates as `git commit -F`; this plan pins that as an invariant (the project config's `git commit -F` deny rule is spelled against it) and changes nothing about heredocs.
  The pin is [#941]'s proposed regression case, so `/ship` may close [#941] with it.
- **A statement whose parse failed.**
  A `redirected_statement` with `hasError` is left exactly as the grammar produced it: its units are already floored (ADR 0013 §10), and its redirects are the [#814] shapes whose attribution must not move.
  So `git 2>/dev/null push --force $((` still asks rather than denies.
- **Trailing words after a compound body** (`{ a; } 2>/dev/null b`, `( a ) > f b`).
  Bash rejects these as syntax errors (measured: `bash -c '{ echo a; } >/dev/null b'` exits 2 with "syntax error near unexpected token `b'"), so nothing runs and the tree is left as-is.
- **A pattern-first flag whose argument follows a redirect** (`grep -e 2>/dev/null pat f`).
  The pending consumption discharges on the redirect, so `pat` is read as an operand: it over-surfaces, which is the recoverable direction, and it is not the issue's shape.
- **Upgrading `tree-sitter-bash`.**
  Two upstream PRs would fix the grammar (tree-sitter/tree-sitter-bash#331 and #333, both open); 0.25.1 is the latest published version (`pnpm view tree-sitter-bash versions`).
- **The `bash-path-extractor.ts` facade** ([#978]) gets no case here.

## Background

- `src/access-intent/bash/parser.ts` owns the local `TSNode` interface (nine members: `type`, `text`, `startIndex`, `endIndex`, `childCount`, `isNamed`, `hasError`, `previousSibling`, `child(i)`) and the memoized `getParser()` / warmed `getWarmBashParser()` that every consumer parses through: `program.ts`, `sync-commands.ts`, `logging/command-redaction.ts`, and `unresolved-salvage.ts` (through the injected `BashReparser`).
  `test/helpers/fake-ts-node.ts` already builds plain-object `TSNode`s, so a node that is not a web-tree-sitter object is an established shape.
- `redirect-analysis.ts` owns reading a `file_redirect` node: `redirectEffectForDestination`, `redirectMayWriteFile` (the fail-closed floor-exemption refusal, [#803]), and `redirectTargetIndex` ([#609]'s helper naming the redirect's own target).
- `command-enumeration.ts` builds each `BashCommand` unit: `commandWordNodes` (skips `variable_assignment` only), `commandUnitText` (slices the command from its first non-assignment child to its end), `readCommandWords` (offsets into that text, which `wrapper-analysis.ts`'s `executedUnitOf` slices), `makeCommandUnit`, and `redirectedScope` (a statement-level `file_redirect` that may write marks `writesViaRedirect`).
  `inlineShellPayloadNode` reads `commandWordNodes` for the log masker ([#923]).
- `token-collection.ts`'s two command walkers already treat a `file_redirect` child of a `command` as a non-argument: the generic walker recurses into it and reaches `collectRedirectTokens`, and the pattern-first walker excludes it from positional counting through `EXECUTION_HOST_TYPES`.
  `commandArgumentWords` (the retraction guards' input) reads only argument node types.
  `COMMAND_PREFIX_TYPES` names the `command_name` / `variable_assignment` skip, but three sites still spell it out: `commandArgumentWords`, `collectEmbeddedOptionValues`, and `bash-path-resolver.ts`'s `cdLiteralTarget` (re-derived: `grep -n 'command_name" || child.type === "variable_assignment' src/access-intent/bash/*.ts` prints three lines).
  The roadmap reassigned that tidy to this step from [#609].
- `bash-path-resolver.ts` walks `redirected_statement` twice: `walkCurrentShellSequence` and `foldPipelineFirstStage`, the latter for [#454]'s mis-grouped `cd a && pnpm x 2>&1 | tail`.
- AGENTS.md principle 5 (mechanism is forever) applies.
  The correction is a new runtime mechanism, justified because the alternative is the same knowledge scattered across five walker branches; it is a no-op on the real root in the common case and becomes deletable when the grammar is fixed upstream.

## Design Overview

### The observed scenario

Measured at `629a174d6d8f2aab9025f1780ae9b24c7557d7eb` with a disposable Vitest spike (not committed).
It ran the real `BashProgram.parse` and `resolveBashCommandCheck` over a real filesystem-backed `PermissionManager` (`createManagerWithConfig`) and a real `PermissionResolver`, under `bash: {"*": "allow", "git push *": "deny", "find * -delete*": "deny", "rm *": "deny"}`.
The input strings are the issue's repros plus the variants below, which are hand-written but run through the whole real path.
Each command's verdict is deterministic (no cache, no model), so n = 1 per row.

| Command                              | Unit today                     | Verdict today | After this plan                     |
| ------------------------------------ | ------------------------------ | ------------- | ----------------------------------- |
| `git push --force` (control)         | `git push --force`             | deny          | deny                                |
| `git 2>/dev/null push --force`       | `git`                          | **allow**     | deny                                |
| `2>/dev/null git push --force`       | `2>/dev/null git push --force` | **allow**     | deny                                |
| `git <<< x push --force`             | `git <<< x push --force`       | **allow**     | deny                                |
| `find ~/x 2>/dev/null -delete`       | `find ~/x`                     | **allow**     | deny                                |
| `bash -c 'rm -rf /tmp/x'` (control)  | opaque-payload wrapper         | ask           | ask                                 |
| `>/dev/null bash -c 'rm -rf /tmp/x'` | not a wrapper                  | **allow**     | ask                                 |
| `sudo 2>/dev/null rm -rf /`          | `sudo` (indirection)           | ask           | ask, with `executedUnit` `rm -rf /` |

The same spike's path slices:

- `grep pat 2>/dev/null ~/x/f.txt`: projects `~/x/f.txt` as `write (syntax)` where `grep pat ~/x/f.txt` projects `read (core)`.
- `find ~/x 2>/dev/null -delete`: projects `~/x` as `read (core)` where `find ~/x -delete` projects `unproven (retracted)`.

A second spike over `redactCommandSecrets` wrote `bash 2>/dev/null -c 'TOKEN=abc123 curl x'` and `>/dev/null bash -c 'TOKEN=abc123 curl x'` unmasked, where `bash -c 'TOKEN=abc123 curl x'` masks to `TOKEN=[redacted]`.

The "After" column is predicted, not measured.

**Real-traffic frequency (measured).**
A census parsed the 8804 intact distinct bash commands in the local review log with the real `tree-sitter-bash` 0.25.1.

- 4 error-free `redirected_statement`s carry words after a `file_redirect`'s target, every one with a `command` body (`2>/dev/null --include="*.ts"`, `2>/dev/null -type d -path "*@earendil-works/pi-ai"`, `2>&1 pat pat file`, `2>/dev/null --exclude-dir=node_modules`).
- 0 hold a redirect inside a `command` node.
- 0 hold a close-operator redirect with a word after it.

The census script (`/tmp/census977*.mjs`, not committed) transcribes no walker; it counts node shapes only.

### Where the correction lives (operator decision: the parser boundary)

The grammar's output is corrected where it enters the package: `getParser()`'s `parse()` returns a tree in which the words a statement-level `file_redirect` carries after its own target are handed back to the command they belong to.
Every consumer already parses through that one accessor, so the enumerator, the path resolver, both token walkers, the masker, the advisory path, and the salvage re-parse all read the corrected tree with no branch of their own.
The rejected alternative was a shared helper that each walker called in its `redirected_statement` branch.
That is about five call sites, and a future walker could silently miss it.

The corrected shape is the one the grammar already produces for a leading redirect, and the one upstream PR tree-sitter/tree-sitter-bash#333 produces for a mid-command one: the redirect is a child of the `command`, between its words.

```text
git 2>/dev/null push --force

grammar (0.25.1):
(redirected_statement
  (command (command_name (word "git")))
  (file_redirect (file_descriptor "2") ">" (word "/dev/null") (word "push") (word "--force")))

corrected:
(command (command_name (word "git"))
  (file_redirect (file_descriptor "2") ">" (word "/dev/null"))
  (word "push") (word "--force"))
```

A `redirected_statement` is corrected when all of these hold:

- It has no parse error (`hasError` is false).
- Its body reaches a `command`: either the body is a `command`, or it is a `list` whose rightmost descendant is one.
  The list case matters because the grammar groups `cd a && git 2>/dev/null push --force` as `(redirected_statement (list …) (file_redirect …))`, while bash gives the redirect and its words to `git`.
- At least one of its `file_redirect` children carries words after its own target.

The rewrite:

1. Take every statement child from the body up to and including the last `file_redirect` that carries trailing words, in source order.
   Move each one into the rightmost command as a child after the command's own children.
   A `file_redirect` with trailing words is split: the redirect is truncated after its target, and its trailing words follow it.
2. The command's range grows to the end of the last moved node.
   So does each node on the path from the statement down to the command (a `list` spine).
3. Children of the statement after that point stay at the statement.
   If none remain, the corrected body takes the statement's place, exactly as the grammar parses the same command with the redirect written first.
4. Any other node is presented as-is, and a node's children are corrected wherever they sit, so a statement nested in a pipeline, a list, a substitution, or a subshell is corrected too.

For a close operator (`>&-`, `<&-`), every named child after the operator is a trailing word, because the operator names no target.
Measured: `bash -c 'printf "%s|" >&2 x y'` prints `x|y|`, and `echo >&- hi` fails writing `hi` to the closed stdout.

### The shapes

```typescript
// redirect-analysis.ts
/** Redirect node types a `command` or a statement can host. */
export const REDIRECT_NODE_TYPES: ReadonlySet<string>; // file_redirect, herestring_redirect, heredoc_redirect

/** The redirect's own target; now `undefined` for `>&-` / `<&-` even when a word follows. */
export function redirectTargetIndex(redirect: TSNode): number | undefined;

/**
 * The child index where the words the grammar appended after the redirect's
 * own target begin (words bash passes to the command), or `undefined` when
 * none follow.
 */
export function trailingArgumentIndex(redirect: TSNode): number | undefined;

// redirect-arguments.ts
/**
 * tree-sitter-bash's parse with each word it hung on a redirect handed back to
 * the command it belongs to. Returns `root` itself when nothing needs it.
 */
export function reattachRedirectArguments(root: TSNode): TSNode;

// parser.ts
export const getGrammarParser: () => Promise<TSParser>; // tree-sitter-bash's own output, for the correction's tests
export const getParser: () => Promise<TSParser>; // parse() returns reattachRedirectArguments(tree.rootNode)
```

`reattachRedirectArguments` reads `TSNode` only.
It calls `trailingArgumentIndex` and nothing else from outside its module, and it holds no state beyond the view it returns.

The view's contract, each item a test:

1. **Fast path.**
   When no statement qualifies, the returned root *is* the real root (`toBe`).
   In the measured corpus that is 8800 of 8804 commands (the 4 above are the rest).
   Every walker then runs on exactly the objects it runs on today.
2. **Offsets stay source offsets.**
   Every node satisfies `node.text === source.slice(node.startIndex, node.endIndex)`, and each node's children are in non-decreasing `startIndex` order.
   A moved word keeps its real `startIndex`; the masker shifts spans by it, and `commandUnitText` slices by it.
   Measured: web-tree-sitter's `startIndex` is a UTF-16 string index (`echo é—😀 2>/dev/null x` puts `x` at 22 = `src.indexOf("x")`), which is what existing callers already assume.
3. **`previousSibling`** of a child of a rewritten node is that node's previous child, or `null` for the first child.
4. **`hasError`** is the real node's for a node presented as-is, and `false` for a rewritten node.
   Only error-free statements are rewritten, so a rewritten node contains no error.

How the view is built (lazily wrapped, eagerly copied, or cached per node) is the implementing session's call within this contract.

### The enumerator, for a redirect inside a `command`

The correction puts redirects inside commands, and today a redirect inside a command defeats the enumerator.
So this half is required, not optional; it also closes the leading-redirect bypass that needs no misparse.

```typescript
// command-enumeration.ts (private)
function commandWordNodes(command: TSNode): TSNode[];
// named children, minus variable_assignment and REDIRECT_NODE_TYPES

function commandUnitText(command: TSNode): string;
// the word nodes' text joined by the verbatim source gap between consecutive
// words, or by one space where a hosted redirect sat in that gap; a command with
// no word (a pure assignment) keeps its whole text, as today

function readCommandWords(command: TSNode): CommandWord[];
// each word's offset into commandUnitText's result
```

A command with no hosted redirect produces the byte-identical text it does today.
Today's text runs from the first non-assignment child to the node's end, and the node ends at its last child.
The new text runs from the first word to the last word with verbatim gaps, so a line continuation (`git push \` + newline + `--force`) and doubled spaces survive unchanged.
A hosted herestring after the last word (`cat f <<< hi`) now leaves the unit (`cat f`), consistent with a statement-level redirect.
Measured: 0 commands in the corpus hold a hosted redirect, so no real unit text changes.

`makeCommandUnit` asks the command's own hosted `file_redirect`s the same question `redirectedScope` asks a statement's: may one of them write a file (`redirectMayWriteFile`)?
If so, the unit is scoped `writesViaRedirect` before `isTransparentWrapper` reads it.
Both callers share one private predicate over a node's `file_redirect` children.

Without this, fixing the head word would *grant* an exemption.
`>/tmp/o xargs grep foo` today is no wrapper at all, because its head word is `>/tmp/o`.
With the head word fixed, the unit becomes an indirection wrapper around a core reader, which exempts it unless the hosted write withholds the exemption.

### The resolver's `cd` target

`cdLiteralTarget` returns `null` at the first named child that is not an argument, so a hosted redirect between `cd` and its target makes the base unknown.
That is fail-closed, but wrong once the correction puts redirects there.
It skips `COMMAND_PREFIX_TYPES` and `REDIRECT_NODE_TYPES` alike.

### Effect attribution after the move

A reattached word takes the command's proof instead of the operator's:

- `grep pat 2>/dev/null ~/x/f.txt` projects `~/x/f.txt` as `read (core)`.
- `cat >&- ~/x/a` projects `read (core)` where it is `unproven` today.
- `rg -l x | xargs ls -1t 2>&1 ~/x` keeps its core-reader exemption, because `2>&1` writes no file once `~/x` is no longer read as its destination.

Each is the issue's expected behavior, and each can mean fewer prompts than today for that shape.
The list-body case narrows one over-attribution.
In `xargs cat && xargs ls 2>/tmp/o x`, the redirect moves into `xargs ls`, so `xargs cat` keeps its exemption.
The same command without the trailing `x` still withholds the exemption from both units, because the grammar hangs that redirect off the whole list and the enumerator over-attributes it on purpose ([#803]).
Bash gives the redirect to `xargs ls` alone, so the corrected spelling is the precise one and the uncorrected one stays conservative.

### Credit

The design adopts no third-party mechanism.
The corrected shape matches upstream PR tree-sitter/tree-sitter-bash#333's output as a compatibility target, and the commit body cites it as a reference, with no `Co-authored-by:` trailer.

## Module-Level Changes

- `src/access-intent/bash/token-collection.ts`: step 1 exports `COMMAND_PREFIX_TYPES` and uses it in `commandArgumentWords` and `collectEmbeddedOptionValues`.
  In step 7, the doc comments on `collectRedirectTokens` ("Every other child is an `operand`: a word the grammar appends after the target belongs to the redirected command (#977)") and on `PathToken` / `TokenRole` are reworded: the corrected tree hands the collector a redirect holding only its own target.
  No logic change is predicted.
- `src/access-intent/bash/bash-path-resolver.ts`: `cdLiteralTarget` imports `COMMAND_PREFIX_TYPES` (step 1) and also skips `REDIRECT_NODE_TYPES` (step 4).
- `src/access-intent/bash/parser.ts`: step 2 splits the memoized grammar parser (`getGrammarParser`, exported) from `getParser`.
  Step 7 makes `getParser`'s `parse()` return `reattachRedirectArguments(tree.rootNode)` with the tree's own `delete()`, and updates the module doc comment on why two accessors exist.
  `getWarmBashParser` / `warmBashParser` read `getParser` and so warm the corrected one.
- `src/access-intent/bash/redirect-analysis.ts`:
  - Step 3 exports `REDIRECT_NODE_TYPES`.
  - Step 5 makes `redirectTargetIndex` return `undefined` for `>&-` / `<&-`, adds `trailingArgumentIndex`, and rewrites the "Only the first" paragraph to say the parser boundary reattaches those words.
  - `redirectMayWriteFile` is unchanged.
    On the corrected tree it only ever sees a truncated redirect, so `cmd 2>&1 arg` stops refusing because of `arg`.
- `src/access-intent/bash/command-enumeration.ts` (step 3): `commandWordNodes`, `commandUnitText` and `readCommandWords` (built together), `makeCommandUnit` (the hosted-write scope), and a private predicate shared with `redirectedScope`.
- `src/access-intent/bash/redirect-arguments.ts` (new, step 6): `reattachRedirectArguments` and its private view construction.
  It lives in `access-intent/bash/` beside `redirect-analysis.ts`, the same fallow zone, so no boundary edit is needed.
- **Predicted unchanged**, with the claim each rests on:
  - `src/logging/command-redaction.ts`: it parses through `getWarmBashParser` and finds payloads through `inlineShellPayloadNode`, and both are corrected upstream of it.
  - `src/access-intent/bash/wrapper-analysis.ts`: it reads only `CommandWord` text and offsets, whose contract is unchanged.
  - `unresolved-salvage.ts`, `sync-commands.ts`, `program.ts`: each parses through the corrected accessor.
    The salvage only re-parses `hasError` regions, which the correction never touches.
  - `nested-execution.ts`: a moved word keeps its subtree, so the substitutions it hosts are still found.
  - `handlers/gates/bash-path-extractor.ts`: no production caller ([#978]).
  - `test/helpers/fake-ts-node.ts`: the correction's tests use real grammar parses, not fabricated nodes.
- `test/access-intent/bash/redirect-analysis.test.ts`: step 2 points its parse helper at `getGrammarParser`, because this module reads the grammar's redirect node (the correction calls it on uncorrected trees).
  Step 5 adds the close-operator and trailing-index cases.
- `test/access-intent/bash/redirect-arguments.test.ts` (new, step 6).
- `test/access-intent/bash/parser.test.ts`: step 2 adds grammar-parser memoization; step 7 adds corrected vs. grammar output.
- `test/access-intent/bash/program.test.ts`, `test/handlers/gates/bash-command-metamorphic.test.ts`, `test/logging/command-redaction.test.ts`: new cases in steps 3, 4, and 7.
- `test/access-intent/bash/token-collection.test.ts` (step 7): the case "is an operand when it is a word after the first destination" (`grep pat 2>/dev/null f.txt`) moves out of "a redirect child that is not a proven literal target", since `f.txt` is no longer a redirect child.
  It becomes a "word after a redirect's target" case asserting `f.txt`'s effect: `read (core)`, where the uncorrected tree gives `write (syntax)`.
  Its roles assertion stays green before and after, so on its own it cannot discriminate.
- `docs/architecture/architecture.md` (step 8):
  - Module-tree entries for `parser.ts`, the new `redirect-arguments.ts`, `redirect-analysis.ts`, `command-enumeration.ts`, and `token-collection.ts`.
    For `token-collection.ts`, the "Constraint: only the first destination takes the role, because `tree-sitter-bash` 0.25.1 parses the words after a redirect as further destinations… (#977)" sentence is reworded to the corrected tree.
  - `bash-path-resolver.ts`: the `cd` target skips a hosted redirect.
  - The roadmap `#### [#977]` heading and the Mermaid node `S977` gain `✅`, plus a `Landed:` note with the corpus re-measurement from step 7.
- `docs/decisions/0009-bash-path-projection-completeness-contract.md` (step 8): a dated `### Amendment` at the top of the amendments, with frontmatter `amended:` and the `## Status` line.
  The "Only the first destination" bullet's "their attribution is [#977]'s" is replaced by the reattachment.
  The residual bullet's "is the command's operand and is covered here" is kept and cites the correction.
- `.pi/skills/package-pi-permission-system/SKILL.md`: predicted unchanged.
  Its one redirect sentence ("a redirect's literal first destination reaches both by its `redirect-destination` role") stays true.
  Step 8 greps it for `destination`, `redirect`, and `#977` to confirm.

## Test Impact Analysis

1. **Newly possible.**
   The quirk becomes a pure tree transformation testable on the grammar's own output.
   Today it is observable only through each consumer's result, so each consumer's tests carried a copy of it.
   `redirect-arguments.test.ts` asserts corrected shapes as S-expressions over real grammar parses, and the view's offset and sibling contract over every node of each case.
2. **Redundant or reframed.**
   `token-collection.test.ts`'s trailing-word role case is reframed as an effect case (above).
   `redirect-analysis.test.ts`'s "names only the first destination when words follow it" stays: that module still reads uncorrected redirects, from the correction itself.
3. **Stays as-is.**
   These are the consumer-level tests that exercise the composition:
   - `program.test.ts`'s `does not admit the words the grammar appends after the target` (`find /usr 2>/dev/null -type d` → `["/usr", "/dev/null"]`).
     Predicted green after step 7: `-type` is flag-shaped and `d` is a bare name that does not exist, so neither is a rule candidate.
   - The #814 cases, the #454 fold cases, and the floor-exemption cases.

## Invariants at risk

| Invariant (constituency)                                                                                                               | Source            | Pinned by                                                                                                                                                                                                                                  | Why it holds                                                                                                |
| -------------------------------------------------------------------------------------------------------------------------------------- | ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------- |
| Only a redirect's own target takes `redirect-destination`; the words after it are not admitted by role (users with `path_write` rules) | [#609] `Outcome:` | `program.test.ts` "does not admit the words the grammar appends after the target"; `token-collection.test.ts` "a redirect's own target"                                                                                                    | the corrected redirect holds only its target                                                                |
| An unresolvable redirect proves nothing (users with a read grant)                                                                      | [#814]            | `redirect-analysis.test.ts` "a redirect the parser could not resolve"; `token-collection.test.ts` "is an operand when the parse could not resolve the redirect"; `program.test.ts` "does not admit a redirect the parse could not resolve" | an erroring statement is never rewritten; step 6 adds `cat <> rw.txt extra` to the fast-path identity cases |
| A redirect that may write withholds the core-reader exemption (users relying on the floor)                                             | [#803]            | `program.test.ts` "a statement that writes through a redirect"                                                                                                                                                                             | statement-level check unchanged; step 3 adds the hosted check and its tests                                 |
| A partially parsed statement's units are floored (every user)                                                                          | [#840]            | `bash-command-metamorphic.test.ts` "a parse it could not resolve fails closed"                                                                                                                                                             | rewritten nodes are error-free, so `hasError` is unchanged wherever an error exists                         |
| The salvage only adds (every user)                                                                                                     | [#875]            | `bash-command-metamorphic.test.ts` "the salvage only ever adds to what the primary parse found"                                                                                                                                            | the salvage re-parses erroring regions only                                                                 |
| Masked spans land on the command as written (log readers)                                                                              | [#923]            | `command-redaction.test.ts` "a secret inside an inline-shell payload"                                                                                                                                                                      | moved words keep real offsets (view contract 2)                                                             |
| A mis-grouped `cd a && … 2>&1 \| tail` still folds `cd a` (users with `external_directory` rules)                                      | [#454]            | `program.test.ts` "folds a leading current-shell cd across a redirect-then-pipe"                                                                                                                                                           | no trailing words there, so no rewrite; step 7 adds the trailing-word variant                               |
| Unit text of a command with no hosted redirect is byte-identical (users with `bash` rules and session approvals)                       | issue Goals       | new pin in step 3 (doubled space and a line continuation) plus the existing unit-text assertions                                                                                                                                           | the gap rule reproduces today's slice                                                                       |
| `git commit -F - <<'EOF'` enumerates as `git commit -F` (this repo's project deny rule)                                                | [#941]            | new pin in step 3                                                                                                                                                                                                                          | heredoc statements are not rewritten, and the command node ends at `-F`                                     |

Two invariants are quantitative, and both are measured at step 7.
First, the unit texts and path slices over the review-log corpus change for exactly the 4 census commands (the prediction).
Second, the fast path returns the real root for every other command.

## TDD Order

1. **Name the command-prefix child types once.**
   Export `COMMAND_PREFIX_TYPES` from `token-collection.ts`, then replace its literal re-spellings in `commandArgumentWords`, `collectEmbeddedOptionValues`, and `bash-path-resolver.ts`'s `cdLiteralTarget`.
   This prepares steps 4 and 7: step 4 edits `cdLiteralTarget`, and step 7 relies on `commandArgumentWords` reading the reattached words, so both should read the named set rather than a third copy.
   No new tests; the unchanged package suite is the verification.
   Commit: `refactor(pi-permission-system): name the command-prefix child types once`.
2. **Expose the grammar's own parse beside the corrected one.**
   In `parser.ts`, memoize the grammar parser as `getGrammarParser`, and have `getParser` return that same parser for now.
   Point `redirect-analysis.test.ts`'s parse helper at `getGrammarParser`.
   This prepares steps 5–7: once step 7 corrects `getParser`, any test of the uncorrected shape must still see it, or the trailing-word cases pass vacuously on a truncated redirect.
   Test (`parser.test.ts`): `getGrammarParser` returns one instance across calls.
   Killing mutation: make `getGrammarParser` call `initParser()` directly without memoization; the identity test goes red.
   Commit: `refactor(pi-permission-system): expose the grammar's own parse beside the corrected one`.
3. **Gate a command whose redirect comes before or inside its words.**
   Export `REDIRECT_NODE_TYPES` from `redirect-analysis.ts`.
   In `command-enumeration.ts`, change `commandWordNodes` to skip hosted redirects, build `commandUnitText` and `readCommandWords` together from the word nodes with the gap rule, and have `makeCommandUnit` scope a hosted write through a predicate shared with `redirectedScope`.
   Tests:
   - `program.test.ts`: `2>/dev/null git push --force` → unit `git push --force`; `git <<< x push --force` → `git push --force`; `>/dev/null bash -c 'rm -rf /tmp/x'` → `wrapperKind: "opaque-payload"`, `executedUnit: "rm -rf /tmp/x"`.
   - `program.test.ts`: `>/tmp/o xargs grep foo` → exemption withheld; `2>&1 xargs grep foo` → `core-reader`.
   - `program.test.ts` byte-identity pin: `git  push \` + newline + `--force` keeps its verbatim text.
   - `program.test.ts` [#941] pin: `git commit -F - <<'EOF'`, then `feat: x`, then `EOF` → `["git commit -F"]`, and `git commit -F /tmp/msg.txt` keeps its operand.
   - `bash-command-metamorphic.test.ts`: a new describe, "placing a redirect before or inside a command does not weaken its decision", with the leading (`2>/dev/null <bare>`) and hosted-herestring positions over the existing bare cases.
   - `command-redaction.test.ts`: `>/dev/null bash -c 'TOKEN=abc123 curl x'` → `>/dev/null bash -c 'TOKEN=[redacted] curl x'`.
   Killing mutations, one per class:
   - (a) Make `commandWordNodes` skip only `variable_assignment`: kills the opaque-wrapper, `executedUnit`, and masker cases.
   - (b) Make `commandUnitText` slice from the first word to the node's end: kills the hosted-herestring unit and its metamorphic rows.
   - (c) Delete the hosted-write scoping in `makeCommandUnit`: kills `>/tmp/o xargs grep foo`.
   - (d) Treat every hosted redirect as writing: kills `2>&1 xargs grep foo`.
   - (e) Join every word with one space: kills the byte-identity pin.
   The [#941] pin is an invariant pin: it passes at Red, and mutation (b) does not touch it.
   Its own mutation is to make `commandUnitText` include hosted `heredoc_redirect` text, which must turn it red.
   Commit: `fix(pi-permission-system): gate a command whose redirect comes before or inside its words`.
4. **Resolve paths after a `cd` whose redirect precedes its target.**
   `cdLiteralTarget` also skips `REDIRECT_NODE_TYPES`.
   Test (`program.test.ts`, posix normalizer, cwd `/projects/app`): `2>/dev/null cd /tmp && cat a` projects `/tmp/a` among the external accesses.
   Killing mutation: remove the redirect skip; the base becomes unknown and `/tmp/a` disappears.
   Commit: `fix(pi-permission-system): resolve paths after a cd whose redirect precedes its target`.
5. **Name where a redirect's trailing words begin.**
   In `redirect-analysis.ts`, `redirectTargetIndex` answers `undefined` for `>&-` / `<&-`, and `trailingArgumentIndex` is added.
   Tests (`redirect-analysis.test.ts`, grammar parser):
   - `grep pat 2>/dev/null f.txt` → the trailing index names `f.txt`.
   - `pnpm x 2>&1 arg` → names `arg`.
   - `cmd >&- arg` → target `undefined`, trailing index names `arg`.
   - `cat a > out.txt` and `echo hi >&-` → `undefined`.
   Killing mutations:
   - Drop the close-operator check: `cmd >&- arg`'s target names `arg` and its trailing index is `undefined`.
   - Make `trailingArgumentIndex` return `undefined` unconditionally: the three naming cases go red.
   No consumer reads either function's new answer yet: `collectRedirectTokens`' role for `cmd >&- arg` is `operand` either way, since `>&-` proves no `syntax` effect.
   Commit: `refactor(pi-permission-system): name where a redirect's trailing words begin`.
6. **Build the parse view.**
   Add `redirect-arguments.ts` with `reattachRedirectArguments`, not yet wired.
   Before writing the cases, print each case's grammar parse, so every expected shape is read off the real tree, not assumed.
   Tests (`redirect-arguments.test.ts`, grammar parser + explicit call), asserting S-expressions of `type` and `text`:
   - **Command body:** `git 2>/dev/null push --force` (no `redirected_statement` left).
   - **Trailing redirect kept:** `cmd a > out b 2>&1` (`2>&1` stays at the statement).
   - **Several redirects:** `cmd <in 2>err arg1 >out arg2`.
   - **List spine:** `cd a && git 2>/dev/null push --force`.
   - **Nesting:** `git 2>/dev/null push --force | tail` and `x=$(git 2>/dev/null push --force)`.
   - **Close operator:** `cmd >&- arg`.
   - **Fast path:** `git push --force 2>/dev/null`, `cat <> rw.txt extra`, and `{ a; } 2>/dev/null b` each return the root itself (`toBe`).
   - **View contract:** over every node of each rewritten case, `text` equals the source slice, children are ordered, and `previousSibling` is the previous child.
   Killing mutations, one per class:
   - Handle only a `command` body: kills the list spine.
   - Drop the `hasError` guard: kills the `cat <> rw.txt extra` identity case, provided its grammar parse carries words after its first named child (confirm when printing; if it does not, find an erroring spelling that does).
   - Leave moved redirects untruncated: kills every shape case.
   - Give a rewritten node its real node's `text`: kills the contract.
   - Keep the `redirected_statement` wrapper when nothing remains at it: kills the command-body case.
   - Recurse only from the root's direct children: kills the nesting cases.
   Commit: `refactor(pi-permission-system): build a parse view that hands a redirect's trailing words back to their command`.
7. **Wire the correction in.**
   `getParser`'s `parse()` returns `reattachRedirectArguments(tree.rootNode)`.
   Tests:
   - `parser.test.ts`: `getParser()` presents `git 2>/dev/null push --force` as a `command` whose words include `push`; `getGrammarParser()` still does not.
   - `bash-command-metamorphic.test.ts`: the step-3 describe gains the mid-command (`git 2>/dev/null push --force`), after-flag (`git push 2>&1 --force`), and list (`cd a && git 2>/dev/null push --force`) positions.
   - `program.test.ts`:
     - `find ~/x 2>/dev/null -delete` → `~/x` is `unproven (retracted)`.
     - `grep pat 2>/dev/null ~/x/f.txt` → `~/x/f.txt` is `read (core)`, `/dev/null` is `write (syntax)`.
     - `sudo 2>/dev/null rm -rf /` → unit `sudo rm -rf /`, `executedUnit: "rm -rf /"`.
     - `rg -l x | xargs ls -1t 2>&1 ~/x` → exemption `core-reader`.
     - `cd 2>/dev/null /tmp && cat a` → `/tmp/a`.
     - `cd a && git 2>/dev/null push --force | tail ; cat ../b` → `../b` resolves under `cwd/a` ([#454]).
   - `command-redaction.test.ts`: `bash 2>/dev/null -c 'TOKEN=abc123 curl x'` → `bash 2>/dev/null -c 'TOKEN=[redacted] curl x'`.
   - `token-collection.test.ts`: reframe the trailing-word case as above.
   Killing mutation: make `getParser` return the grammar parser unwrapped; every test added in this step goes red, and step 6's tests stay green (they call the function directly).
   All consumer classes share this one seam, so a per-class mutation is not meaningful here.
   Per-class discrimination lives in steps 3 and 6.
   Verify:
   - Run the full package suite.
   - Run a disposable corpus spike (not committed) comparing `commands()`, `pathRuleCandidates()`, and `externalAccesses()` at step 1's parent against this step, over the review log's intact distinct bash commands.
     Predicted: exactly the 4 census commands differ, each only by its reattached words.
     Record the measured count in the `Landed:` note.
   Commit: `fix(pi-permission-system): check the words after a mid-command redirect against the command's own rules`, with body `Refs tree-sitter/tree-sitter-bash#233, tree-sitter/tree-sitter-bash#333`.
8. **Docs.**
   Update the architecture module tree and the roadmap (`✅` on `#### [#977]` and `S977`, `Landed:` note), plus the ADR 0009 amendment, per Module-Level Changes.
   Get the amendment date from `date -u +%F`.
   Grep `docs/` and `.pi/skills/package-pi-permission-system/SKILL.md` for `further destination`, `#977`, and `repeat1`, and leave no stale claim.
   Commit: `docs(pi-permission-system): record that a redirect's trailing words are reattached at the parser boundary`.

## Risks and Mitigations

| Risk                                                                                                                                  | Mitigation                                                                                                                                                                                                                                                                                                               |
| ------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| A view's `text` or offsets disagree with the source, silently mis-slicing unit text or masked spans                                   | View contract 2 is a test over every node of every rewritten case; moved words keep real offsets                                                                                                                                                                                                                         |
| The correction touches every parse, so a defect in it reaches every command                                                           | The fast path returns the real root unless a statement qualifies (8800 of 8804 in the corpus); step 7's corpus diff must match the 4-command prediction                                                                                                                                                                  |
| Rewriting an erroring statement moves an `ERROR` node or turns a [#814] unproven destination into a command operand with a core proof | The `hasError` guard; `cat <> rw.txt extra` identity case; existing #814 tests                                                                                                                                                                                                                                           |
| Fixing the head word grants a floor exemption the redirect should withhold                                                            | Step 3's hosted-write scoping, with mutation (c) killing its test                                                                                                                                                                                                                                                        |
| The list-body case narrows the over-attributed exemption for an earlier unit                                                          | Bash semantics give the redirect to the last command; documented in Design Overview; the uncorrected spelling stays conservative                                                                                                                                                                                         |
| An upstream grammar fix changes the raw shape, and the correction mis-fires                                                           | `tree-sitter-bash` is pinned `^0.25.1`, so a minor bump could land. A #333-style fix leaves nothing to correct (fast path); a #331-style fix adds `argument` children to `redirected_statement`, which the correction does not read. Step 6's grammar-parse shape tests fail on either, forcing a review at upgrade time |
| A walker compares node identity, and views break it                                                                                   | `grep -rnE '(===\|!==) (node\|child\|redirect\|destination)\b' src/access-intent/bash src/logging` returned nothing at planning time; `redirectTargetIndex` compares indices by design                                                                                                                                   |

## Open Questions

- Should the correction also retire `redirectTargetIndex`'s "first named child after the operator" rule in `collectRedirectTokens` once every redirect it sees is truncated?
  It stays correct and is cheap; revisit only if [#979]'s extension makes it misleading.

[#454]: https://github.com/gotgenes/pi-packages/issues/454
[#609]: https://github.com/gotgenes/pi-packages/issues/609
[#803]: https://github.com/gotgenes/pi-packages/issues/803
[#814]: https://github.com/gotgenes/pi-packages/issues/814
[#840]: https://github.com/gotgenes/pi-packages/issues/840
[#875]: https://github.com/gotgenes/pi-packages/issues/875
[#923]: https://github.com/gotgenes/pi-packages/issues/923
[#941]: https://github.com/gotgenes/pi-packages/issues/941
[#978]: https://github.com/gotgenes/pi-packages/issues/978
[#979]: https://github.com/gotgenes/pi-packages/issues/979
