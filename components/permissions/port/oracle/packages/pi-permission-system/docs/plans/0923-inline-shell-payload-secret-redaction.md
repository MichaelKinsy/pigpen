---
issue: 923
issue_title: "pi-permission-system: a secret inside an inline-shell payload or heredoc body escapes command redaction"
---

# Mask a secret inside an inline-shell payload

## Release Recommendation

**Release:** ship independently

[#923] is not a step in the Phase 15 roadmap — the phase's spine is token roles and declared effects, and this is a `logging/` exposure.
The architecture doc's Open-issue sweep list already records it as out of scope for the roadmap, and it carries no `Release:` tag, so it is independently releasable.
The observable step is a `fix:`, which cuts a patch on its own.

## Problem Statement

[#920] masks a secret bound to a sensitive name inside a logged bash command string, anchored to a tree-sitter node in three binding forms.
An inline-shell payload is a single `raw_string` or `string` node, so `bash -c 'TOKEN=sk-secret deploy'` carries no `variable_assignment` node and the outer `command` field is written verbatim.

The enumerator, meanwhile, sets `executedUnit` to the unquoted payload for that unit, and `executedUnit` is a `COMMAND_BEARING_LOG_KEYS` member masked on its own — it re-parses to a real `variable_assignment`.
So one review-log record holds the same secret masked under one key and unmasked under another.

Reproduced on `main` at planning time, with a warmed parser and the real `collectCommands` / `redactCommandSecrets`:

```text
units:         [{"text":"bash -c 'TOKEN=sk-secret deploy'","wrapperKind":"opaque-payload","executedUnit":"TOKEN=sk-secret deploy"}]
command:       bash -c 'TOKEN=sk-secret deploy'   ← written verbatim
executedUnit:  TOKEN=[redacted] deploy            ← masked
```

269 of 17 981 records in the local review log carry both keys, and the shape of the first one found is exactly this wrapper form.

The issue names a second context — a heredoc body — and asks for an explicit decision on it.
That decision is **declined**, on the measurement in § Measurements: a non-interpolating heredoc body is where an agent writes source code to a file, and applying the three rules to those bodies produced six matches in the corpus, all six false positives of exactly the embedded-Python class ADR 0010 declined.

## Goals

- Mask a value bound to a sensitive name inside an **inline-shell payload** — the argument `classifyWrapperWords` already tags `"opaque-payload"`: `eval`'s first argument, and the argument after a `-c` short-flag cluster for `bash`/`sh`/`dash`/`zsh`/`ksh`.
- Restrict the widening to payloads the package already knows are shell, by reusing the wrapper analyzer's own vocabulary rather than a second copy of it.
  A blanket recursion into string nodes is what re-admits the false-positive class grammar anchoring removed.
- Recover the payload's spans from a re-parse of its **verbatim inner slice**, offset by the slice's start index, so the masked line stays quote-balanced and byte-aligned with the command as written.
- Handle a payload nested inside a payload, bounded by depth.
- Decide the heredoc question explicitly and record it as an accepted residual in ADR 0010 rather than leaving it implicit.

The change is **not breaking**.
The review log is a diagnostic artifact with no parser contract, the change only removes content from it, nothing a user configures changes meaning, and the prompt path is untouched.

## Non-Goals

- **Heredoc bodies, interpolating or not.**
  Declined on the measurement below, and recorded in the ADR amendment as an accepted residual rather than a tracked follow-up.
  A non-interpolating body (`<<'EOF'`) is literal data the package parses for no other reason, and it is where an agent writes Python and TypeScript to disk; an interpolating body (`<<EOF`) is 3 of 918 bodies in the corpus.
  No follow-up issue is filed: re-opening this needs a report of a real leak, which is the same bar ADR 0010 set for [#920].
- **Herestring payloads** (`cmd <<< "TOKEN=sk-x"`).
  Measured at **0** occurrences across 8 056 unique real commands.
  Recorded as a residual in the ADR amendment; the operator declined it at the clarification gate.
- **Non-shell interpreter payloads** (`python3 -c`, `node -e`, `perl -e`).
  These are deliberately *not* in `SHELL_WRAPPER_NAMES`, and that exclusion is what keeps `command-redaction.test.ts`'s embedded-Python control green — see § Invariants at risk.
- **Value-shape secret detection.**
  Still declined on ADR 0010's measured evidence; the `grep`-pattern vector still has no name bound to it.
- **Flag-argument masking** (`--token abc`).
  Unchanged from [#920]'s non-goals.
- **Threading the enumerator's units into the writer.**
  `executedUnit` already holds the unquoted payload, but it is `nothingNew`-filtered and unwraps nested indirection, so it carries no span the writer could map back onto `command`.
  Passing structure into `writeLine` would also break the one constraint the whole masking design rests on — that `writeLine` is the only place a line is produced and sees only strings.
- **`src/access-intent/bash/redirect-analysis.ts`'s device gap.**
  Found while planning this issue and filed separately as [#951]: `redirectMayWriteFile` proves a write for a `/dev/null` destination, so `2>/dev/null` withholds [#803]'s `core-reader` exemption.
  Different module, different surface; dispositioned out of scope for Phase 15.

## Background

### Where the payload rule has to look

`redactCommandSecrets` (`src/logging/command-redaction.ts:51`) parses the command once, walks the tree collecting `MaskSpan`s from three node rules, and splices right-to-left.
Every rule matches a node, so a payload's interior — which is one token to the outer parse — is unreachable from that walk.

The wrapper analyzer already knows which argument is the inline program, but not as a node:

- `classifyWrapperWords(words)` (`src/access-intent/bash/wrapper-analysis.ts:41`) returns `"opaque-payload"` for the shapes in question, over `CommandWord[]` (`{ text, offset }`).
- The private `opaquePayload(words)` (line 205) returns the payload's *unquoted text*, computed as `args[shortFlagCIndex(args) + 1]` — the index arithmetic this change needs, inlined.
- The private `readCommandWords(node)` (`src/access-intent/bash/command-enumeration.ts:435`) is the one node→`CommandWord[]` walk: it skips a leading `variable_assignment` and reports `command_name` as `words[0]`.

`wrapper-analysis.ts`'s module doc states its charter explicitly — "Pure and word-based; the AST walk that produces the words lives in `command-enumeration.ts`" — so the word-index answer belongs there and the node answer belongs in `command-enumeration.ts`.

### The narrow parser interface already exists

`BashReparser` (`src/access-intent/bash/parser.ts:100`) is `{ parse(input): { rootNode, delete() } | null }` — deliberately narrower than `TSParser`, which also carries the parser's own `delete()` that would destroy the process-wide memoized parser for every later command.
`unresolved-salvage.ts` is the precedent: it re-parses a fragment through `BashReparser`, and `program.ts` passes the full `TSParser` positionally where structural typing accepts it.

### Constraints from AGENTS.md and the package skill

- The boundary sentence — *a value bound to a sensitive name is masked, whether the name is a log key, a shell variable, or a request header field; a secret with no name bound to it is not* — stays **true and unchanged**.
  This change widens where a shell variable is *found*, not what counts as a name, so none of its five verbatim copies moves.
- `writeLine` is the only place a log line is produced; the masker is called from `prepareLogLine` and stays there.
- Redaction is structural, never value-shape (ADR 0010).
- The masker is best-effort and never throws — it sits under the fail-closed `tool_call` boundary, where a raised mask costs the whole log line.
- The prompt is never redacted (ADR 0010); the approver must see the real command.
- `docs/architecture/architecture.md` carries the module entries for every file touched here, and § Directory vocabulary places the masker in `logging/`.

## Design Overview

### The seam, split along the two charters

Two new exports, each on the side of the boundary its module owns:

```typescript
// src/access-intent/bash/wrapper-analysis.ts — pure, word-based
/**
 * Index within `words` of the inline-shell payload argument, or `-1`.
 * `eval` takes its program as the first argument; a shell takes it after the
 * `-c` short-flag cluster.
 */
export function inlineShellPayloadIndex(words: readonly CommandWord[]): number;

// src/access-intent/bash/command-enumeration.ts — the node walk
/** The inline-shell payload argument node of a `command` node, or `null`. */
export function inlineShellPayloadNode(command: TSNode): TSNode | null;
```

`inlineShellPayloadIndex` is the arithmetic `opaquePayload` inlines today, lifted and named; `opaquePayload` is rewritten on top of it so the two cannot drift.
`inlineShellPayloadNode` walks the same filtered child list `readCommandWords` reports words from, which is why the Tidy-First preparation extracts that walk first.

### The masker's recursion

```typescript
// src/logging/command-redaction.ts
export function redactCommandSecrets(command: string): string {
  if (!command) return command;
  try {
    const parser = getWarmBashParser();
    if (!parser) return command;
    return applyMaskSpans(command, collectSpansIn(parser, command, 0, 0));
  } catch {
    return command;
  }
}

/**
 * Every mask span in `source`, shifted to its position in the original command,
 * including the spans of any inline-shell payload `source` carries.
 */
function collectSpansIn(
  parser: BashReparser,
  source: string,
  offset: number,
  depth: number,
): MaskSpan[] { /* … */ }
```

`collectSpansIn` parses `source`, calls the existing **unmodified** `collectMaskSpans` on the tree, then shifts the whole batch once — `spans.map((s) => ({ ...s, start: s.start + offset, end: s.end + offset }))`.
The Tidy-First assessor corrected the design here: threading an `offset` parameter through `collectMaskSpans` and its three rule helpers adds surface area to four functions that are currently correct and reached only through `redactCommandSecrets`, and a nested tree's indices already start at 0 relative to its own source.
Shifting once at the boundary is the same result with no signature change.

The parser is threaded as `BashReparser`, not `TSParser`, so the recursion structurally cannot call `delete()` on the process-wide parser.
That is a type-level guarantee rather than a test, and it is why the narrow interface exists.

Payload discovery and the slice:

```typescript
/** The payload's verbatim inner text and its offset, so a re-parsed span maps back. */
function payloadSlice(node: TSNode): { readonly text: string; readonly start: number } {
  const text = node.text;
  const quote = text.at(0);
  const quoted =
    (quote === "'" || quote === '"') && text.length >= 2 && text.endsWith(quote);
  return quoted
    ? { text: text.slice(1, -1), start: node.startIndex + 1 }
    : { text, start: node.startIndex };
}
```

The slice is the node's raw inner text, **not** `resolveNodeText` — that resolves `$HOME`/`$PWD` expansions and concatenates children, which destroys the offset correspondence the whole design rests on.
Because the slice excludes the quotes, no recovered span can reach them, so the masked payload stays quoted exactly as it was written.
A `string` payload holding an escaped quote (`bash -c "API_KEY=\"sk\""`) re-parses with the escape intact and the span covers it whole, so the outer quotes stay balanced too.

Depth is bounded at 4, matching `wrapper-analysis.ts`'s `MAX_UNWRAP_DEPTH`.
No unbounded recursion is possible anyway — a payload is a strict sub-span of its `command` node, so each level is strictly shorter — but the bound makes the cost statable.

Overlap needs no new handling: `applyMaskSpans` already sorts by start ascending then end descending and drops any span contained in one already kept, so an outer span that swallows a payload wins over the payload's own.

### Why this is the false-positive-free half of the widening

The payload set is the *shell* set.
`python3 -c`, `node -e`, and `perl -e` are not in `SHELL_WRAPPER_NAMES`, so their payloads are never re-parsed — which is exactly why the embedded-Python control stays green.
The heredoc half has no such filter: a body is a body, and 915 of 918 in the corpus are `<<'EOF'` bodies holding source code.

### Call-site sketch

Nothing changes at the call site — `prepareLogLine` already calls `maskCommandFields(details)` ahead of the width cap and for both streams.
The widening is entirely inside `redactCommandSecrets`, which is the argument for having put the pass at the writer in the first place.

## Measurements

All taken at planning time against the author's real review log, parsed with the real `tree-sitter-bash` and the shipped rule set.
The log grows while the session runs, so each row states the run's own record count.
Every number is **measured**, not estimated.

| Probe                                                                      | Result                                                                         |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| Corpus                                                                     | 13 120 944 bytes, 17 981 records, 8 056 unique `command`/`executedUnit` values |
| Records carrying both `command` and `executedUnit`                         | 269                                                                            |
| Inline-shell payload units                                                 | 48 across 42 commands                                                          |
| …whose payload re-parse yields a mask                                      | **0**                                                                          |
| Commands whose logged text differs between the shipped masker and this one | **0 of 8 056**                                                                 |
| Commands the shipped masker already changes                                | 4                                                                              |
| Heredoc bodies                                                             | 918 across 878 commands — 611 under a `heredoc_redirect`, 306 under an `ERROR` |
| …interpolating (`<<EOF`)                                                   | 3 → **0** masks                                                                |
| …non-interpolating (`<<'EOF'` / `<<"EOF"`)                                 | 915 → **6** masks, **6 false positives**                                       |
| Herestrings (`<<<`)                                                        | **0**                                                                          |
| Commands with a recovering (error) parse                                   | 354 = 4.4 %                                                                    |

The six non-interpolating heredoc matches, verbatim regions (median of the false-positive class, not a sample):

```text
word/key         NAMES = sorted(RULES.keys(), key=len, reverse=True)
word/key         sorted(hits.items(), key=lambda x:-len(x[1]))
assignment/key   iter_errors(inst), key=lambda e: e.path)
header/tokens    console.log("tokens:", JSON.stringify(coll…
word/TOKEN       'bash -c \'TOKEN=abc deploy\'',        ← #920's own spike vectors
assignment/KEY   `KEY="it's a secret" deploy`,          ← #920's own spike vectors
```

Three are `key=lambda` / `key=len` inside embedded Python — the exact class ADR 0010 measured at 10 false positives for a raw-string scan and 0 for a node-anchored rule.
That is the argument for declining heredocs: the rule stays node-anchored, but the *body* it re-parses is not shell, so the anchor buys nothing.

Masker cost per command, median of three runs over the 8 056-command corpus:

| Variant                          | Cost      |
| -------------------------------- | --------- |
| Shipped (parse + walk)           | 0.050 ms  |
| **+ inline-shell payload**       | 0.061 ms  |
| + interpolating heredoc too      | 0.063 ms  |
| + every heredoc body             | 0.123 ms  |

Target vectors and controls, run through a prototype of the design:

```text
MASKED    bash -c 'TOKEN=[redacted] deploy'
MASKED    sh -c "API_KEY=[redacted] curl https://x"
MASKED    eval "TOKEN=[redacted] deploy"
MASKED    bash -c "API_KEY=[redacted] deploy"                     ← payload held $SECRET
MASKED    bash -c 'bash -c "TOKEN=[redacted] x"'                  ← nested
MASKED    bash -c TOKEN=[redacted]                                ← already masked today
MASKED    bash -ec 'export OPENROUTER_KEY=[redacted]'
MASKED    /bin/bash -c 'MY_KEY=[redacted] deploy'
MASKED    bash -c 'curl -H "Authorization:[redacted]" https://x'  ← quote balance
MASKED    bash -c 'echo hi' && bash -c 'TOKEN=[redacted] x'
MASKED    TOKEN=[redacted] bash -c 'API_KEY=[redacted] x'         ← two spans, outer + payload
UNCHANGED bash -c 'python3 -c "print(sorted(d, key=lambda x: x[1]))"'
UNCHANGED bash -c 'sort --key 2 f.txt'
UNCHANGED bash -c 'grep -r "sk-ant-oat01-abc" .'
UNCHANGED bash -c 'curl -H "Content-Type: application/json" https://x'
UNCHANGED bash --help
UNCHANGED bash -c
UNCHANGED eval
UNCHANGED cat > .env <<'EOF'\nAPI_KEY=sk-secret\nEOF                ← declined, by design
```

The prototype's first run swallowed the closing quote on the header case, because it applied a bare placeholder instead of the span's own `replacement`.
That is now a named killing mutation in step 5 rather than a bug the corpus could not have surfaced — it has zero real occurrences.

## Module-Level Changes

### Source

- `src/access-intent/bash/wrapper-analysis.ts` — export `inlineShellPayloadIndex(words)`; rewrite the private `opaquePayload(words)` on top of it.
  `shortFlagCIndex` stays private and becomes its single caller's helper.
  No behavior change: `hasShortFlagC` and `classifyWrapperWords` are untouched.
- `src/access-intent/bash/command-enumeration.ts` — extract the private `commandWordNodes(node): TSNode[]` out of `readCommandWords` (the Tidy-First preparation), then export `inlineShellPayloadNode(command)` over it.
  The name is `commandWordNodes`, **not** `commandArgumentNodes`: `token-collection.ts:255` already has a `commandArgumentWords` with a *different* filter (it drops `command_name` and requires `ARG_NODE_TYPES`), and `architecture.md:1064` records that the two walks differing is a finding, not an accident.
- `src/logging/command-redaction.ts` — `redactCommandSecrets` delegates to a new private `collectSpansIn(parser, source, offset, depth)`; new private `payloadSlice(node)` and `inlineShellPayloads(root)`; new `MAX_PAYLOAD_DEPTH`.
  `collectMaskSpans`, `maskSpanOf`, the three rule helpers, `openQuoteAt`, `maskSpan`, and `applyMaskSpans` are **unchanged**.
  Imports gain `inlineShellPayloadNode` from `#src/access-intent/bash/command-enumeration` and `BashReparser` from `#src/access-intent/bash/parser`.
  The module doc comment gains the payload rule and its restriction.

No dependency cycle: `command-enumeration.ts` imports `#src/types`, `./nested-execution`, `./parser`, `./redirect-analysis`, `./wrapper-analysis` — none reaches `logging/`.
No `package.json` `exports` surface changes; `logging/` and `access-intent/` are both internal.

Symbol sweep for the two new exports and the extracted private, run at planning time: `opaquePayload` and the string `readCommandWords` appear in no `src/` or `test/` file beyond their own, and `readCommandWords` appears in prose only at `docs/architecture/architecture.md:1064` and `docs/retro/phase-15-token-roles-declared-effects-sandbox-seam.md:12` — both statements about the walk *differing* from `commandArgumentWords`, which the extraction preserves.
Predicted **unchanged**; the claim is that renaming nothing and keeping `readCommandWords` as a name leaves both sentences true.

### Tests

- `test/access-intent/bash/wrapper-analysis.test.ts` — a new `describe("inlineShellPayloadIndex")` block: `eval` at 1, `bash -c` at 2, `-ec`/`-xc` clusters, `--` before `-c`, `/bin/bash -c`, a non-shell (`python3 -c` → `-1`), an indirection wrapper (`sudo ls` → `-1`), a bare `bash` → `-1`, and `bash -c` with no following argument.
- `test/access-intent/bash/program.test.ts` — a new `describe("inlineShellPayloadNode")` block over parsed trees, covering the leading-`variable_assignment` skip (`TOKEN=x bash -c '…'`), the three payload node types, and `null` for an ordinary command.
  Predicted otherwise unchanged: `readCommandWords` and `commandUnitText` are private and exercised only end-to-end through `collectCommands`, so the extraction has no test signature to break.
- `test/logging/command-redaction.test.ts` — a new `describe("a secret inside an inline-shell payload")` block under the existing `"once the parser is warm"`: every MASKED row above, plus the nesting case, the two-span case, the quote-balance case, and the four control rows.
  The existing `"leaves an assignment inside another language's source"` case gains a sibling wrapping it in `bash -c '…'` — the pin that the payload set is the shell set.
- `test/logging/logging.test.ts` — one case in the existing `describe("masking a secret inside a command string")`: a review entry whose `command` is `bash -c 'TOKEN=sk-secret deploy'` is written masked, and one asserting the same record's `executedUnit` and `command` now agree.
- `test/presentation/tool-ask-payload.test.ts` — predicted **unchanged**.
  Its `"carries the command unmasked, because the approver must see what runs"` case uses `KEY="sk-secret-value" curl https://x`, which is not a wrapper; the invariant it pins is that the payload never reaches the masker at all.

### Docs

- `docs/decisions/0010-permission-log-secret-exposure.md` — a new dated amendment, in the same style as the 2026-09-15 one it supersedes in part: the payload half of that amendment's third residual is closed, the heredoc half becomes an **accepted** residual with the six-false-positive measurement, and the herestring is named as a third.
  The 2026-09-15 bullet's `Tracked as [#923]` becomes a pointer to the new amendment.
  Stays `status: accepted`.
- `docs/configuration.md` — line 1304 (`A command the parser could not fully resolve, and a secret inside an inline-shell payload (bash -c '…') or a heredoc body, are masked only as far as the parse reached.`) drops the payload clause and keeps the heredoc one, with the reason.
  The example block at 1290–1295 gains a `bash -c '…'` row.
- `docs/architecture/architecture.md` — three module entries: `command-redaction.ts` (line 990) gains the payload rule and the "the payload set is the shell set" constraint; `wrapper-analysis.ts` (line 887) gains `inlineShellPayloadIndex`; the `command-enumeration.ts` entry gains `inlineShellPayloadNode`.
  No roadmap step-mark: [#923] is not a Phase 15 step, and its Open-issue sweep bullet at line 1165 is a record of the disposition at filing time — predicted **unchanged**.
- `.pi/skills/package-pi-permission-system/SKILL.md` — the `## Log writes` governing-record line gains `#923` to its `(Refs #647, #920)` list.
  The boundary sentence in the same paragraph is predicted **unchanged**: it names what counts as a *name*, and this change widens only where a shell variable is found.
- `docs/troubleshooting.md` — predicted **unchanged**.
  Line 57 reads "masked only where a name binds the secret", which stays true.
- `README.md` — predicted **unchanged**.
  Its *Guessing what is sensitive* non-goal describes the technique, which does not change.
- `docs/migration/0746-review-log-fields.md`, `docs/plans/0920-*.md`, `docs/retro/0920-*.md` — predicted **unchanged**; each is a true statement about a past release.

## Test Impact Analysis

1. **New tests the change enables.**
   `inlineShellPayloadIndex` is a pure `CommandWord[] → number`, so the flag-cluster and `--` cases become direct unit tests instead of properties inferred from `executedUnit` strings.
   `inlineShellPayloadNode` makes the leading-`variable_assignment` skip assertable on its own, where today it is only visible through a `BashCommand.text`.
2. **Tests that become redundant.**
   None.
   `wrapper-analysis.test.ts`'s `executedUnitOf` cases still exercise the unquoting and the `nothingNew` filter, which `inlineShellPayloadIndex` does not answer.
3. **Tests that must stay as they are.**
   `command-redaction.test.ts`'s `"values no name is bound to"` block, all three cases — they are the false-positive boundary, and the widening must not move it.
   `logging.test.ts:252` (`"masks a sensitive-keyed value whole, however long it was"`) pins the mask/cap composition the recursion inserts nothing into.
   `test/presentation/tool-ask-payload.test.ts:75` pins that the prompt is never masked.

## Invariants at risk

| Invariant                                                                                                 | Constituency                                                         | Pinned by                                                                                                                                                                                                                                                                                                                                        |
| --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Every rule matches a parse node, never a substring of the command text ([#920])                           | Anyone who reads the review log for the command                      | `command-redaction.test.ts:140` `"leaves an assignment inside another language's source"`. **Load-bearing and load-bearingly fragile**: it stays green only because `python3` is absent from `SHELL_WRAPPER_NAMES`. Step 5 adds the `bash -c 'python3 -c "…key=lambda…"'` sibling that pins the nesting boundary, which no existing case reaches |
| The masker is best-effort and never throws; a cold parser or a failed parse yields what the walk resolved | The gate — `writeLine` is under the fail-closed `tool_call` boundary | `command-redaction.test.ts:195` `"returns the command unchanged rather than raising"`, plus `"masks what a recovering parse still resolved"` (line 182). The recursion adds nested `parse` calls, all inside the same outer `try`                                                                                                                |
| A masked argument stays quoted the way it was written ([#920]'s review finding)                           | Anyone re-reading a logged command as shell                          | No test reaches it *inside a payload* today. Step 5 adds `bash -c 'curl -H "Authorization: Bearer sk-z" https://x'`, whose killing mutation is the exact bug the prototype hit                                                                                                                                                                   |
| A cap is not redaction; a sensitive-keyed value is masked whole however long it was                       | Anyone reading the review log                                        | `logging.test.ts:252` — must stay green with no edit                                                                                                                                                                                                                                                                                             |
| The prompt is never redacted (ADR 0010)                                                                   | The human answering the ask                                          | `test/presentation/tool-ask-payload.test.ts:75` — must stay green with no edit                                                                                                                                                                                                                                                                   |
| `getWarmBashParser()`'s parser is never `delete()`d by a consumer                                         | Every later command in the process                                   | The `BashReparser` parameter type. `tsc`, not a test — the interface has no `delete`                                                                                                                                                                                                                                                             |
| The zero-false-positive property: no real command logs differently                                        | Anyone reading the review log                                        | **Measured, not tested**: 0 of 8 056 unique real commands differ between the shipped masker and a prototype of this one. Not reproducible in CI — the corpus is the author's log                                                                                                                                                                 |

The quantitative invariant is the masker's per-command cost.
Measured baseline **0.050 ms**; measured post-change **0.061 ms** (+22 %), median of three runs over 8 056 real commands.
The re-parse is bounded by the payload, which is strictly shorter than the command the gate already parsed once in the same tool call.

## TDD Order

1. **`refactor(pi-permission-system): name the inline-shell payload's argument index`** Red: new `describe("inlineShellPayloadIndex")` cases in `test/access-intent/bash/wrapper-analysis.test.ts` (the nine listed in Module-Level Changes).
   Green: export `inlineShellPayloadIndex(words)` from `wrapper-analysis.ts` and rewrite the private `opaquePayload` on top of it.
   `refactor:` and not `feat:` — nothing a user observes changes, and `opaquePayload`'s answer is byte-identical.
   Killing mutations, one per class: (a) return `shortFlagCIndex(args)` instead of `shortFlagCIndex(args) + 1` — every shell case goes red on an off-by-one while the `eval` case stays green; (b) drop the `eval` branch — only the `eval` case goes red; (c) return the index even when `shortFlagCIndex` is `-1` — the `bash --help` and `bash -c` after `--` cases go red.

2. **`refactor(pi-permission-system): extract the command's word-node walk`** Tidy-First preparation, from the assessor's report.
   Extracts the private `commandWordNodes(node): TSNode[]` out of `readCommandWords`, which becomes a thin map over it.
   No behavior change; verified by `pnpm run check` plus the existing `program.test.ts` staying green untouched.
   Prepares step 3: `inlineShellPayloadNode` must walk the *exact same* filtered child list in the exact same order, and two walks with the same filter written twice is the drift `architecture.md:1064` already records as a finding about the neighboring module.
   Killing mutation: none — a pure extraction has no behavior to kill.

3. **`refactor(pi-permission-system): answer which node holds an inline-shell payload`** Red: new `describe("inlineShellPayloadNode")` cases in `test/access-intent/bash/program.test.ts`.
   Green: export `inlineShellPayloadNode(command)` from `command-enumeration.ts`, over `commandWordNodes` and `inlineShellPayloadIndex`.
   `refactor:` — no consumer imports it until step 5.
   Killing mutations: (a) walk `node.child(i)` unfiltered instead of `commandWordNodes(node)` — the `TOKEN=x bash -c '…'` case goes red on an index shift while the plain `bash -c '…'` case stays green; (b) return the node for any `command` regardless of `inlineShellPayloadIndex` — the `sudo ls` and `python3 -c` cases go red.

4. **`fix(pi-permission-system): stop writing a named secret inside an inline-shell payload`** Red: the new `describe("a secret inside an inline-shell payload")` block in `test/logging/command-redaction.test.ts`, covering every MASKED and UNCHANGED row in § Measurements, the nesting case, the two-span case, the quote-balance case, and the `bash -c 'python3 -c "…"'` boundary pin.
   Write and run this block **before** implementing — [#920]'s retro records that skipping the Red run left the mutation pass as the step's only discrimination evidence.
   Green: `collectSpansIn`, `payloadSlice`, `inlineShellPayloads`, `MAX_PAYLOAD_DEPTH` in `command-redaction.ts`; `collectMaskSpans` and the rule helpers unchanged; the parser threaded as `BashReparser`.
   `fix:` because `redactCommandSecrets` is already wired into `prepareLogLine` — a masked log line is observable the moment this lands, so this is the step that reaches the changelog, and its subject names the outcome rather than the seam.
   Killing mutations, one per class: (a) delete the payload recursion from `collectSpansIn` — every new MASKED case goes red, every existing case stays green; (b) make `payloadSlice` always return `{ text: node.text, start: node.startIndex }` (never strip the quote pair) — the quoted-payload cases go red while `bash -c TOKEN=sk-x` stays green, because the outer word rule already covers it; (c) drop the `+ offset` shift on the returned batch — every payload case goes red with the mask spliced at the top of the command; (d) set `MAX_PAYLOAD_DEPTH = 1` — only the nested `bash -c 'bash -c "TOKEN=x"'` case goes red; (e) in `applyMaskSpans`, substitute a bare `REDACTED_PLACEHOLDER` for `span.replacement` — only the quote-balance case goes red, and this is the prototype's actual first-run bug.

5. **`test(pi-permission-system): pin that both log keys report the same command`** `test:` and not `fix:` — the cases pass on step 4's implementation, so nothing a user observes changes here and the changelog must not carry the fix twice.
   Red: the two `logging.test.ts` cases — a review entry whose `command` is `bash -c 'TOKEN=sk-secret deploy'` is written masked, and a record carrying both keys writes the same masked text under each.
   Green: nothing new — the cases pass on step 4's implementation, which is the point: the second one is the inconsistency the issue reports, expressed as an assertion.
   If either goes green without an edit, keep it; a test that documents an invariant the previous step established is the cheapest pin there is.
   Killing mutation: remove `"executedUnit"` from `COMMAND_BEARING_LOG_KEYS` — the both-keys case goes red and the `command` case stays green, which is the asymmetry the issue named.
   Commit body carries `Refs #923`.

6. **`docs(pi-permission-system): record the payload widening and accept the heredoc residual`** The ADR 0010 amendment, `docs/configuration.md`'s line 1304 and example block, the three `architecture.md` module entries, and the package SKILL's `Refs` list — exactly as listed in Module-Level Changes.
   Verification, run and recorded at planning time so `/tdd-plan` can re-run it:
   - `pnpm exec rumdl check` on each touched markdown file → expected `Success: No issues found`.
   - `grep -n "or a heredoc body" packages/pi-permission-system/docs/configuration.md` → expected **no match**.
     Measured at planning time: one match, line 1304, the sentence this step rewrites.
   - `grep -rn "or a heredoc body" packages/pi-permission-system/docs/` → expected to return exactly `architecture.md:1166`, the Open-issue sweep bullet recording the disposition at filing time.
     Measured at planning time: two matches — that bullet and `configuration.md:1304`.
   - `grep -rln "inline-shell payload" packages/pi-permission-system/docs/` → the historical files are `plans/0490-*.md`, `plans/0744-*.md`, `plans/0920-*.md`, `retro/0920-*.md`, `architecture/architecture.md`, and `decisions/0010-*.md`, all measured at planning time and all expected to still match; `configuration.md` matches only if the rewritten sentence keeps the phrase.

## Risks and Mitigations

- **A nested payload re-admits the false-positive class through an interpreter.**
  `bash -c 'python3 -c "key=lambda x: 1"'` re-parses the shell payload, and the interpreter's own payload is a `string` node inside it.
  Mitigated by the payload set being the shell set — `python3` is not in `SHELL_WRAPPER_NAMES`, so the inner payload is never re-parsed, and the `string` node matches no rule (`key=lambda…` is not a `word`, and `print(` cannot match the header field pattern).
  Pinned by step 4's boundary case.
  This is a risk about a mechanism being **absent** — the absence of `python3` from a set — so the test exercises the absence rather than the shell happy path.
- **The masker destroys the process-wide parser.**
  A recursion handed `TSParser` could call `delete()`, killing masking and every later bash gate for the process.
  Mitigated by the type: `BashReparser` has no `delete`, and `unresolved-salvage.ts` is the precedent.
  Verified by `tsc`, not a test.
- **A nested parse throws and costs a log line.**
  Mitigated by the existing outer `try`, unchanged.
  Note the [#920] retro's lesson: a control-flow mutation here produces *too few* reds, because the outer `catch` returns the input too — so step 4's mutations are all value mutations.
- **The masked payload is no longer valid shell.**
  A span that reached a quote would unbalance the line.
  Mitigated structurally: the payload slice excludes the quote pair, so no recovered span can reach it, and the header rule's `openQuoteAt` is computed against the slice's own text.
  Pinned by step 4's quote-balance case and mutation (e).
- **A heredoc leak we chose not to close.**
  `cat > .env <<'EOF'` with `API_KEY=…` stays unmasked, and that is a realistic leak.
  Mitigated by stating it as an accepted residual in ADR 0010 with the measurement, so the log's limitation is written down rather than implied, and by the same `permissionReviewLog: false` guidance ADR 0010 already gives for a credential-handling session.
  The alternative was measured at 6 false positives and 0 true positives.
- **Parse cost on a pathological payload.**
  Measured at +22 % per command over 8 056 real commands, bounded by the payload's length and depth 4.
  If this proves wrong in practice, a length ceiling on the recursion is a one-line follow-up.

## Open Questions

- Whether a heredoc body should ever be masked.
  Closed for now as an accepted residual rather than a tracked follow-up — re-opening it needs a report of a real leak, which is the bar ADR 0010 set for [#920] and the bar that report met.
  No issue is filed, deliberately: an open issue would imply the decision is provisional when the measurement says it is not.
- Whether a herestring payload should be masked.
  Declined at the clarification gate; 0 occurrences measured.
  Recorded in the ADR amendment as a residual.

[#803]: https://github.com/gotgenes/pi-packages/issues/803
[#920]: https://github.com/gotgenes/pi-packages/issues/920
[#923]: https://github.com/gotgenes/pi-packages/issues/923
[#951]: https://github.com/gotgenes/pi-packages/issues/951
