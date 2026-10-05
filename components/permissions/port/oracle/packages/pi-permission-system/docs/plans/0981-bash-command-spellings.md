---
issue: 981
issue_title: "bash surface pattern matching is asymmetric: patterns get ~ expanded, command values do not"
---

# Give a bash command unit its spellings, starting with the home-expanded one

## Release Recommendation

**Release:** ship independently

This issue is not a step in the Phase 15 roadmap (`docs/architecture/architecture.md` never references it), so it carries no batch tag.

## Problem Statement

A `bash` rule whose pattern starts with `~`, `$HOME`, or `${HOME}` never matches a command typed with that same prefix.
`compileWildcardPattern` (`src/policy/wildcard-matcher.ts`) expands the home prefix on the **pattern** only, while the bash gate hands the manager the command unit's text exactly as typed.
The reporter's `"~/.pi/agent/bin/memory-index": "allow"` therefore compiles to `^/Users/rocky/.pi/agent/bin/memory-index$` and is matched against the literal `~/.pi/agent/bin/memory-index`, so every invocation falls through to `"*": "ask"`.

The issue frames this as a dead allow rule (fail-closed).
Planning found it is one symptom of a design gap, and that two of its consequences fail open:

- **Deny bypass.**
  With `"*": "allow"` and `"~/bin/danger *": "deny"`, the command `~/bin/danger --now` resolves `allow via *`.
- **Dead session grant.**
  "Allow for this session" on `~/.pi/agent/bin/memory-index` records the pattern `~/.pi/agent/bin/memory-index` (`suggestBashPattern`), which compiles expanded and never matches the next identical invocation.

The gap underneath is that the bash surface has no notion of a command's *spellings*.
Path surfaces already supply them: `AccessPath.matchValues()` gives the typed, absolute, and canonical forms, and `evaluateAnyValue` lets the last rule matching any of them decide.
The bash surface passes one value, so every equivalence the pattern side applies and the value side does not is a dead rule in both directions.
Open third-party PR [#917] hit the same gap from the relative/absolute side and proposed a multi-spelling intent for it.

The issue's preferred fix, expanding the value inside the matcher, cannot be done safely.
On the bash surface a home prefix means whatever the *program* says `HOME` is: `HOME=/tmp/evil; ~/bin/tool` runs `/tmp/evil/bin/tool`, and the matcher cannot see the program.
The bash path projection already guards this case (`ShellVariables.spellsReboundHome`, [#694], [#995], ADR 0009's 2026-09-30 amendment).

## Goals

- A bash command unit carries the other spellings the shell runs identically, produced by the program analysis that knows what they mean.
- The manager evaluates a unit's typed text and its spellings as aliases of one invocation through `evaluateAnyValue`: the position of the rule decides, not which spelling it matched.
- The first spelling producer is the home-expanded form of a unit whose text opens with `~`, `$HOME`, or `${HOME}`, produced only while the program does not rebind `HOME`.
- Fixes all three observed scenarios: the dead allow, the deny bypass, and the dead session grant.
- The prompt, decision value, review log, and session-approval suggestion keep showing the command as typed.
- **Not breaking.**
  No default or output shape changes.
  `docs/configuration.md` § Home Directory Expansion in Patterns already promises that pattern keys "in any permission surface" expand the home prefix; this makes the bash surface meet that contract.
  A config with a home-prefixed bash rule sees that rule take effect, in whichever direction it was written.
  The commit is `fix(pi-permission-system):`.

## Non-Goals

- **Argument spellings.**
  `cat ~/notes` matches a pattern written `cat ~/notes`, as it does today, but not `cat /Users/me/notes`.
  Relative, absolute, and canonical argument spellings are [#917]'s scope.
  That PR should plug a second spelling producer into the seam this plan builds, instead of adding its own `alias-values` intent.
  File access by any spelling is already gated on the `path` and `external_directory` surfaces.
- **The forwarded-serving wire.**
  A subagent's forwarded bash ask carries `matchValues: [command]` (`accessFactsFromValue`, `src/handlers/gates/helpers.ts`), and the serving node rebuilds a `tool` intent from it as typed (`buildResolvedIntentFromMatchValues`).
  The child has already resolved every config rule with spellings, so this residual is limited to a rule or session grant the **serving** node holds and the child does not.
  It behaves exactly as it does today; it is not a regression.
  Fixing it means stamping the deciding unit's spellings onto `PermissionCheckResult`, paired with `command`, and that pairing deserves its own design: [#1019].
- **The bash pattern side's path normalization.**
  `expandHomePath` builds the pattern with `path.join`, which normalizes `.` and `//` segments across a whole command pattern and, on win32, turns every `/` into `\`.
  Measured with `path.win32.join` on macOS, not on a Windows host: `~/bin/x *` compiles to `C:\Users\me\bin\x *`, which the verbatim spelling `C:\Users\me/bin/x` cannot match.
  So on Windows a home-prefixed bash rule stays dead after this change, as it is today (fail-closed).
  Filed as [#1020].
- **The bash advisory cold path.**
  `resolveBashAdvisoryCheck` falls back to a whole-string `tool` intent before the parser is warm.
  Its documented contract is "never weaker than before" (pre-[#309]), which it keeps by staying unchanged.
- **Wrapper inner commands and whole-string resolves.**
  `resolveWrapperUnit`'s inner command (`executedUnit`), the trivially-empty path, and the [#452]/[#712] whole-string check resolve with no spellings.
  A wrapper's first word is the wrapper (`sudo`, `env`), so its unit has no home spelling anyway.
  A floor-exempt inner command is a pure-reader core command, so it never opens with a home prefix.
  The whole string has no program analysis behind it.
- **Changing `wildcard-matcher.ts`.**
  The matcher keeps expanding patterns only; values arrive already spelled by the party that knows what they mean.
  Only its doc comment changes, to state that contract.

## Background

- `src/policy/wildcard-matcher.ts` — `compileWildcardPattern` applies `expandHomePath` to the pattern and only the `windowsSeparators` fold to the value.
  Its doc comment already names the rule this violates: both halves of a fold must apply together, or a rule goes inert ([#653]).
  `probeValuesForSurface` (`permission-manager.ts`) works around the same asymmetry locally.
- `src/path/expand-home.ts` — one prefix table (`~`, `$HOME`, `${HOME}`), a private `afterHomePrefix`, and `expandHomePath`, which uses `join(homedir(), rest)`.
  **`join` must not touch a command string.**
  Measured (`node -e`, `path.join`): the rest `/evil /x/../../safe` becomes `/Users/chris/safe`, so a command `~/evil /x/../../safe` would gain a spelling matching an allow rule on `~/safe` while it runs `~/evil`.
  The value spelling is a pure substitution: `homedir()` plus the rest, verbatim.
- `src/access-intent/bash/shell-variable-expansion.ts` — `ShellVariables` knows which of `HOME`/`PWD` a program rebinds (`scan`), and already exposes `spellsReboundHome` and `readTilde`.
  ADR 0009 keeps the resolvable vocabulary closed at `HOME`/`PWD` and in this one module.
  `HOME` is the case its 2026-09-30 amendment calls "not a widening", because patterns already resolve it.
- `src/access-intent/bash/command-enumeration.ts` — `makeCommandUnit` builds each `BashCommand` from `readCommandUnit(node, scope.words)`; `scope.words` is the program's `WordReader`, bound to its `ShellVariables`.
  A unit's text excludes prefix assignments and hosted redirects, and its first word is the command name.
- `src/handlers/gates/bash-command.ts` — `resolveOnBashSurface` emits `{ kind: "tool", surface: "bash", input: { command } }` for each unit, for the whole-string checks, and for a wrapper's inner command.
- `src/access-intent/input-normalizer.ts` — `normalizeInput`'s `bash` case strips leading comment lines and returns `values: [matchValue]`, `resultExtras: { command }`.
- `src/policy/permission-manager.ts` — `check()` dispatches `path-values`, then falls through to `tool`; both feed `buildCheckResult`, which already calls `evaluateAnyValue`.
- `src/policy/permission-resolver.ts` — `toResolvedIntent` unwraps `access-path` and passes every other kind through unchanged.
- `src/service/permissions-service.ts` — `ResolverForService` is a module-private interface, so widening `AccessIntent` reaches no published type.

## Design Overview

### Reproduction

The probe was a disposable Vitest file, deleted before commit.
It drove `resolveBashAdvisoryCheck` with the parser warm, which reaches the gate's own `resolveBashCommandCheck`.
That ran over a real `PermissionResolver` and a real `PermissionManager` (`createInMemoryManager`).
The config was synthetic: written in-process, mirroring the reporter's rule.
Deterministic code with no cache, so one trial per row.
The control row is the absolute spelling, which shows the rule itself is live.

| Rules (`bash`)                                                                            | Command                             | Today                                    |
| ----------------------------------------------------------------------------------------- | ----------------------------------- | ---------------------------------------- |
| `*: ask`, `~/.pi/agent/bin/memory-index: allow`                                           | `~/.pi/agent/bin/memory-index`      | `ask via *`                              |
| same                                                                                      | `$HOME/.pi/agent/bin/memory-index`  | `ask via *`                              |
| `*: ask`, `<home>/.pi/agent/bin/memory-index: allow`                                      | `~/.pi/agent/bin/memory-index`      | `ask via *`                              |
| `*: ask`, `~/.pi/agent/bin/memory-index: allow` (control)                                 | `<home>/.pi/agent/bin/memory-index` | `allow via ~/.pi/agent/bin/memory-index` |
| `*: allow`, `~/bin/danger *: deny`                                                        | `~/bin/danger --now`                | `allow via *`                            |
| `*: allow`, `~/bin/danger *: deny` (control)                                              | `<home>/bin/danger --now`           | `deny via ~/bin/danger *`                |
| `*: ask` + session grant `~/.pi/agent/bin/memory-index` (the `suggestBashPattern` output) | `~/.pi/agent/bin/memory-index`      | `ask via *`                              |
| `*: ask`, `~/bin/tool: allow`                                                             | `HOME=/tmp/evil; ~/bin/tool`        | `ask via *` (must stay)                  |
| `*: ask`, `cat ~/notes: allow`                                                            | `cat ~/notes`                       | `allow` (argument position, unchanged)   |

Measured blast radius on this operator's traffic: the local review log (23205 entries, 975 on the `bash` surface) holds **0** commands opening with `~`, `$HOME`, or `${HOME}`.
The reporter's log shows 7+ asks a day from one such command.

### The `bash-command` intent

A new gate-emitted intent carries a unit's typed text and its spellings.
It is string-only, so it stays on the manager's side of the ADR 0002 boundary.

```typescript
/**
 * One bash command unit, with the other spellings the shell runs identically.
 *
 * `command` is the unit as typed: the prompt, decision value, and
 * session-approval suggestion read it. `spellings` come from the program
 * analysis — the only party that knows what a spelling means in this program —
 * and the manager evaluates them with `command` as aliases of one invocation.
 */
export interface BashCommandAccessIntent {
  kind: "bash-command";
  surface: "bash";
  command: string;
  spellings: readonly string[];
  agentName?: string;
}

export type AccessIntent =
  | ToolAccessIntent
  | AccessPathAccessIntent
  | BashCommandAccessIntent;
export type ResolvedAccessIntent =
  | ToolAccessIntent
  | PathValuesAccessIntent
  | BashCommandAccessIntent;
```

The gate emits it for **every** unit, with or without spellings, as the assessor recommended.
The alternative (PR [#917]'s choice) keeps `tool` when there are no spellings, which would make one surface produce two intent kinds depending on the data, and every reader would handle both forever.
The whole-string, trivially-empty, and wrapper-inner resolves inside `bash-command.ts` emit it too, with `spellings: []`, so the gate has one route.
The `tool` intent stays for bash queries that do not come from the gate's unit fold: the advisory cold path, the pipeline's unparsed-alias fallback, the serving wire, and `checkPermission`.

### Manager: one bash normalization, two entry points

`normalizeInput`'s `bash` case becomes an exported `normalizeBashCommand(command, spellings)`:

```typescript
export function normalizeBashCommand(
  command: string,
  spellings: readonly string[],
): NormalizedInput {
  const matchValue = stripBashCommentLines(command) || command;
  return {
    surface: "bash",
    values: [...new Set([matchValue, ...spellings])],
    resultExtras: { command },
  };
}
```

The `tool` bash case calls it with `[]`, and `check()` gains a `bash-command` branch that calls it with the intent's spellings and passes the triple to `buildCheckResult` unchanged.
After the `path-values` early return, `intent` narrows to `tool | bash-command`, so `tsc` forces the branch.
`buildCheckResult` already uses `evaluateAnyValue`, so rule position decides.
For example, `{"~/bin/x": "allow", "*": "ask"}` still answers `ask` for `~/bin/x`, because `*` is the later rule and matches the typed text.

### The spelling producer

`ShellVariables` owns what `HOME` means in a program, so it owns the spelling:

```typescript
/**
 * `text` with a leading `~` / `$HOME` / `${HOME}` replaced by the startup
 * home, verbatim otherwise — or `undefined` when it has none or the program
 * rebinds `HOME`.
 */
spellHomeAtStart(text: string): string | undefined {
  if (this.rebound.has("HOME")) return undefined;
  const rest = splitHomePrefix(text); // the exported prefix split, renamed from afterHomePrefix
  if (rest === undefined || rest.startsWith("\\")) return undefined;
  return homedir() + rest;
}
```

- Pure substitution, never `join`, for the bypass measured above.
- A backslash rest is rejected because bash does not tilde-expand `~\x`.
  `expand-home.ts` accepts a backslash for win32 *paths*, which is a different language.
- `rebound` is the existing set `scan` fills, prefix assignments included (`HOME=/tmp ~/bin/x`), so a rebinding anywhere in the program withdraws the spelling.
- `$HOME` is resolved to `homedir()` exactly as `resolveReference` already does for path tokens; this inherits that module's stance and adds no new assumption.

`WordReader` delegates `spellHomeAtStart`, next to `spellsReboundHome`.
`BashCommand` gains `readonly spellings?: readonly string[]`, set by `makeCommandUnit` through `makeUnit`'s option bag only when a spelling exists.
An absent field keeps every existing exact-equality assertion on `collectCommands`/`parseBashCommandsSync` output green: no current fixture's unit text opens with a home prefix (grep below).

Only the **unit text's leading prefix** is spelled, which is exactly what the pattern side expands.
`echo ~/x` opens with `echo`; a quoted `"~/bin/x"` opens with `"`; a subshell's whole-emit opens with `(`; none gets a spelling.
The commands nested inside them are emitted as their own units and are spelled on their own.

Consumer call site (`bash-command.ts`), Tell-Don't-Ask on the unit:

```typescript
function resolveCommandUnit(cmd: BashCommand, …): PermissionCheckResult {
  const base = resolveOnBashSurface(cmd.text, cmd.spellings ?? [], agentName, resolver);
  // floors and tags unchanged
}
```

### Edge cases

- The wrapper floor ([#481], [#490]) and the unparsed floor ([#840]) read only `state`, after resolution, so a unit that newly resolves `allow` through a spelling is still floored.
- A `*` in a program-path pattern matches `..` text: `~/bin/*` matches `~/bin/../../tmp/evil` once spelled.
  This is not new: the absolute spelling `<home>/bin/../../tmp/evil` already matches `~/bin/*` today, because the bash surface matches text.
  The spelling extends the same text semantics to the `~` spelling, and the configuration doc gains a sentence saying so.
- A home directory containing whitespace makes an unquoted `$HOME/x` word-split at run time.
  The path projection already treats a resolved `$HOME` as exact (`isSpelledExactly`), and this plan inherits that stance rather than deciding it differently in a second place.

### Design review

- The new intent adds one field each to `AccessIntent` and `ResolvedAccessIntent`, which are discriminated unions, not dependency bags.
- No parameter relay: `spellings` is produced by the enumerator, carried on the unit it describes, and read by the one function that resolves that unit.
- No new import edge crosses a directory boundary.
  The edges added are `shell-variable-expansion.ts` → `#src/path/expand-home`, which it already imports (`hasHomePrefix`), and `input-normalizer.ts`'s existing imports.

## Module-Level Changes

- `src/access-intent/access-intent.ts` — add `BashCommandAccessIntent`, widen both unions, and update their doc comments.
- `src/access-intent/input-normalizer.ts` — extract and export `normalizeBashCommand`.
  The `NormalizedInput.values` doc comment says "MCP is the only surface producing more than one"; change it to name bash spellings too.
- `src/policy/permission-manager.ts` — add the `bash-command` branch to `check()` and update the `check()` doc comment (which lists `tool | path-values`).
- `src/policy/permission-resolver.ts` — predicted **unchanged in code**: `toResolvedIntent` passes the new kind through.
  The doc comment on `resolve` is touched only if `tsc` objects to the family-fold spread `{ ...resolved, surface }` against `surface: "bash"`.
  `bash` is never a family, so the fallback is to type `surface` as `string`, documented as always `"bash"`.
- `src/path/expand-home.ts` — rename `afterHomePrefix` → `splitHomePrefix` and export it (3 internal callers).
- `src/access-intent/bash/shell-variable-expansion.ts` — add `ShellVariables.spellHomeAtStart`.
- `src/access-intent/bash/node-text.ts` — add `WordReader.spellHomeAtStart` delegate.
- `src/access-intent/bash/command-enumeration.ts` — `BashCommand.spellings`; `makeUnit`'s `WrapperFacts` bag gains `spellings` (renamed `UnitFacts` if the field no longer fits the name); `makeCommandUnit` sets it.
- `src/handlers/gates/bash-command.ts` — `resolveOnBashSurface(command, spellings, agentName, resolver)` emits `bash-command`; `resolveCommandUnit` passes `cmd.spellings ?? []`; the other three call sites pass `[]`.
- `src/policy/wildcard-matcher.ts` — doc comment only: home expansion is pattern-side, and a value producer supplies the expanded spelling, because only it knows what the prefix means for that value.
- `src/service/bash-advisory-check.ts` — predicted **unchanged**: the warm path inherits the fix through `resolveBashCommandCheck`, and the cold path's `tool` intent is a Non-Goal.
- `src/handlers/gates/tool-call-gate-pipeline.ts`, `src/handlers/gates/runner.ts` — predicted **unchanged**: their bash `tool` intents are not the unit fold's.
- `test/helpers/handler-fixtures.ts` — the `makeHandler` intent→`surfaceCheck` adapter becomes an exhaustive `switch` with a `bash-command` arm mapping to `surfaceCheck("bash", { command }, …)`, so `makeBashCommandCheck` keeps its contract.
- `test/helpers/gate-fixtures.ts` — `makePathDispatchResolver`'s `else` assumes `access-path`; it becomes an exhaustive `switch`.
  A new `bashCommandOf(intent)` reader handles the `tool` and `bash-command` kinds.
- `test/handlers/gates/bash-command.test.ts` — migrate 5 `toHaveBeenCalledWith({ kind: "tool", … })` assertions (lines ~34, ~114, ~143, ~167, ~537) and the `.input.command` reader (~44); add the new cases.
- `test/handlers/gates/bash-command-metamorphic.test.ts` — the two `.input.command` readers (~44, ~188) go through `bashCommandOf`.
- `test/handlers/gates/tool-call-gate-pipeline.test.ts` — the `toMatchObject` on `input` (~370) moves to the new shape if it observes a unit-fold intent.
- `test/service/bash-advisory-check.test.ts` — the warm-path resolver dispatch moves to `bashCommandOf`; the cold-path assertions (~50, ~81) stay `tool`.
- `test/access-intent/input-normalizer.test.ts`, `test/policy/permission-manager-unified.test.ts`, `test/policy/permission-resolver.test.ts`, `test/access-intent/bash/shell-variable-expansion.test.ts`, `test/access-intent/bash/sync-commands.test.ts` — new cases (TDD Order).
- `docs/configuration.md` — § Home Directory Expansion in Patterns says bash *path values* are expanded but not command units.
  Add: a command unit opening with `~`/`$HOME`/`${HOME}` matches a home-anchored bash pattern, as does its absolute spelling, unless the command rebinds `HOME`.
  Add: argument position matches as typed.
  Add: on the bash surface `*` matches text, `..` included. § `bash` Surface gains a one-line pointer to it.
- `docs/architecture/architecture.md` — module-tree entries for `access-intent.ts` (the union now includes `bash-command`), `input-normalizer.ts`, `wildcard-matcher.ts` (the pattern-side constraint), `shell-variable-expansion.ts` (`spellHomeAtStart`), `node-text.ts`, `command-enumeration.ts` (`BashCommand.spellings`), and `bash-command.ts`.
  Also the prose at line ~524 (`normalizeInput()` → `evaluateAnyValue()`), which gains the `bash-command` entry point.
  No Mermaid node names the bash intent; the MCP diagram at ~349 is unaffected (checked).
- `docs/decisions/0009-bash-path-projection-completeness-contract.md` — a short amendment: the command-pattern surface is a second consumer of `ShellVariables`' `HOME` vocabulary, under the same rebinding rule, and the resolvable set stays closed.
- `.pi/skills/package-pi-permission-system/SKILL.md` — line 52 ("`mcp` is the only surface producing multiple candidates") becomes "`mcp` and bash spellings".
- Grep evidence for unchanged fixtures: `grep -rn 'text: "~\|text: "\$HOME\|text: "\${HOME}' test` matches only `test/presentation/agent-renderer.test.ts:204`, a path fact, not a `BashCommand`.

## Test Impact Analysis

1. New tests the seam enables: the manager can be asked about spellings directly (`permission-manager-unified.test.ts`), with no parse.
   The spelling producer can be tested over parsed programs (`sync-commands.test.ts`) and over `ShellVariables` directly, with no resolver.
2. Redundant tests: none.
   The existing `tool`-intent bash tests in `permission-manager-unified.test.ts` keep pinning the `[]` path through `normalizeInput`.
3. Tests that must stay as-is: the advisory cold-path assertions (`bash-advisory-check.test.ts` ~50, ~81) and every whole-string/[#712] case in `bash-command.test.ts`, which pin that those routes carry no spellings.

## Invariants at risk

| Invariant                                                                         | Constituency                    | Pinned by                                                                                                         |
| --------------------------------------------------------------------------------- | ------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| Most-restrictive across units ([#301], [#306])                                    | every bash caller               | `bash-command.test.ts` existing chain cases (unchanged)                                                           |
| Whole-string deny reaches an unparsed command ([#452], [#712], [#875])            | deny rules on unparseable input | `bash-command.test.ts` whole-string cases, migrated to assert `spellings: []`                                     |
| Wrapper floor ([#481], [#490]) and unparsed floor ([#840]) apply after resolution | allow rules on wrappers         | new case: `sudo ~/bin/x` with `~/bin/*: allow` stays `ask` via `<indirection-bash-wrapper>`; existing floor cases |
| A rebound `HOME` is not resolved ([#995])                                         | every `HOME` consumer           | new `sync-commands.test.ts` and end-to-end cases (`HOME=/tmp/evil; ~/bin/tool` stays `ask via *`)                 |
| Rule position decides across candidates ([#928])                                  | mcp and now bash                | new manager case `{"~/bin/x": "allow", "*": "ask"}` → `ask`                                                       |
| Prompt and session suggestion show the command as typed                           | the human at the dialog         | new manager case asserting `result.command` is the typed text                                                     |
| Advisory answer never weaker than before ([#309])                                 | service callers                 | cold path unchanged; existing `bash-advisory-check.test.ts` cold cases                                            |

## TDD Order

1. **`test(pi-permission-system): read bash intents through one fixture helper and dispatch fixture adapters exhaustively`**
   - Add `bashCommandOf(intent)` to `test/helpers/gate-fixtures.ts`, returning the command for a bash `tool` intent and `undefined` otherwise.
   - Route the `.input.command` readers in `bash-command.test.ts`, `bash-command-metamorphic.test.ts`, `bash-advisory-check.test.ts`, and `tool-call-gate-pipeline.test.ts` through it.
   - Turn `makeHandler`'s adapter and `makePathDispatchResolver` into `switch`es over `intent.kind` with a `never` default.
   - This prepares steps 3 and 5, which then add one arm per helper instead of editing about 6 read sites.
   - Behavior-preserving; the suite stays green.
   - Killing mutation: make `bashCommandOf` return `""` unconditionally.
     The decomposition cases in `bash-command.test.ts` and the metamorphic table must go red.
2. **`refactor(pi-permission-system): extract normalizeBashCommand with an alias list`**
   - Test surface: `test/access-intent/input-normalizer.test.ts`.
   - `normalizeBashCommand("~/x", ["/h/x"])` returns `values` equal to `["~/x", "/h/x"]` and `resultExtras` equal to `{ command: "~/x" }`.
   - A spelling equal to the typed text is deduplicated.
   - Comment stripping applies to the command, not the spellings.
   - The `tool` bash case calls the function with `[]`; the existing `normalizeInput` bash cases stay green.
   - Update the `NormalizedInput.values` doc comment.
   - No consumer passes spellings yet, hence `refactor:`.
   - Killing mutations:
     - Drop `...spellings` from `values`: the alias case goes red.
     - Remove the `Set`: the dedupe case goes red.
3. **`refactor(pi-permission-system): add a bash-command access intent the manager evaluates as aliases`**
   - Add `BashCommandAccessIntent` to both unions and the `check()` branch, plus one arm in each fixture `switch` from step 1 (`tsc` forces them in this commit).
   - Test surface: `test/policy/permission-manager-unified.test.ts` over a real `createInMemoryManager`:
     - `bash: {"*": "ask", "~/bin/x": "allow"}` with `{ command: "~/bin/x", spellings: [homedir() + "/bin/x"] }` → `allow`, `matchedPattern: "~/bin/x"`, `command: "~/bin/x"`.
     - The same rules with `spellings: []` → `ask`; this is today's behavior through the new kind.
     - `{"~/bin/x": "allow", "*": "ask"}` with the spelling → `ask`, because position decides.
     - `{"*": "allow", "~/bin/danger *": "deny"}` with `command: "~/bin/danger --now"` and its spelling → `deny`.
   - `test/policy/permission-resolver.test.ts`: a session rule `sessionRule("bash", "~/bin/x")` matches the intent through its spelling.
   - Still no gate emits it, hence `refactor:`.
   - Credit PR [#917]'s multi-spelling intent: `Co-authored-by: Ilker Ulusoy <ilker@ilkerulusoy.com.tr>` in the final paragraph, below `Refs #981`.
   - Killing mutations:
     - Make the branch call `normalizeBashCommand(intent.command, [])`: the allow, deny, and session cases go red.
     - Set `resultExtras` to `{ command: values.at(-1) }`: the `command` assertion goes red.
     - Route the branch through a first-value-wins evaluation (`evaluate(surface, values[0], …)`): the allow case goes red.
4. **`refactor(pi-permission-system): spell a bash command unit's leading home prefix`**
   - Rename and export `splitHomePrefix`; add `ShellVariables.spellHomeAtStart`, the `WordReader` delegate, `BashCommand.spellings`, and the `makeCommandUnit` wiring.
   - Test surface `test/access-intent/bash/shell-variable-expansion.test.ts` (`UNREBOUND`, and `scan` over parsed roots, as the file's existing `describe`s do):
     - `~/bin/x` → `homedir() + "/bin/x"`; `~` → `homedir()`.
     - `$HOME/bin/x` and `${HOME}/bin/x` → the same spelling.
     - `~\x`, `~user/x`, `$HOMEDIR/x`, and `${HOME:-/tmp}/x` → `undefined`.
     - `~/evil /x/../../safe` → `homedir() + "/evil /x/../../safe"` verbatim.
     - After `scan` of `HOME=/tmp; ~/x`, `export HOME=/tmp`, and `HOME=/tmp ~/x` → `undefined`.
   - Test surface `test/access-intent/bash/sync-commands.test.ts` (real warm parse):
     - `~/bin/x --y` → `[{ text: "~/bin/x --y", spellings: [homedir() + "/bin/x --y"] }]`.
     - `HOME=/tmp/evil; ~/bin/x` → the `~/bin/x` unit has no `spellings`.
     - `echo ~/x`, `"~/bin/x"`, and `sudo ~/bin/x` → no `spellings`.
     - `echo $(~/bin/x)` → the nested unit carries the spelling, and the enclosing one does not.
   - Not yet consumed by the gate, hence `refactor:`.
   - Killing mutations:
     - Replace the substitution with `expandHomePath(text)`: the verbatim `..` case goes red.
     - Delete the `rebound.has("HOME")` guard: the rebound cases in both files go red.
     - Drop `|| rest.startsWith("\\")`: the `~\x` case goes red.
     - Delete the `spellings` line in `makeCommandUnit`: the `~/bin/x --y` case goes red.
5. **`fix(pi-permission-system): match a home-prefixed bash rule against a command typed with ~ or $HOME`**
   - `resolveOnBashSurface` emits `bash-command`; `resolveCommandUnit` passes `cmd.spellings ?? []`; the other three call sites pass `[]`.
   - Migrate the 5 `toHaveBeenCalledWith` assertions in `bash-command.test.ts` and the `tool-call-gate-pipeline.test.ts` `toMatchObject`.
   - Add a `describe("home-prefixed command spellings")` in `bash-command.test.ts` over real `createInMemoryManager` + `PermissionResolver` + `parseBashCommandsSync`: the reproduction table above, each row's "Today" column flipped where the Goals say.
     - Dead allow, `$HOME` spelling, absolute rule → `allow`.
     - Deny bypass → `deny`.
     - Session grant → `allow` via `session`.
     - Rebound `HOME` → `ask via *`.
     - `cat ~/notes` → unchanged.
     - `sudo ~/bin/x` with `~/bin/*: allow` → `ask` via `<indirection-bash-wrapper>`.
     - An unparseable command → unchanged.
   - Body: `Refs #981`, then the `Co-authored-by: Ilker Ulusoy <ilker@ilkerulusoy.com.tr>` trailer in the final paragraph.
   - Killing mutations:
     - Make `resolveCommandUnit` pass `[]`: the allow, deny, and session rows go red, and the rebound and wrapper rows stay green.
     - Make the whole-string resolve pass the first unit's spellings: the migrated whole-string assertion (`spellings: []`) goes red.
6. **`docs(pi-permission-system): document bash command spellings`**
   - `docs/configuration.md`, the architecture entries, the `wildcard-matcher.ts` doc comment (code comment only), the ADR 0009 amendment, and the package skill line, all as listed in Module-Level Changes.
   - Re-read each edited prose region for split sentences (`markdown-conventions`).

## Risks and Mitigations

- **A spelling that over-matches is a bypass.**
  Mitigated by pure substitution (the `join` bypass was measured and is pinned by step 4's verbatim case), by spelling only a leading prefix, by the rebinding guard, and by spelling only the forms bash expands (no `~\`).
- **`tsc` rejects the resolver's family-fold spread against `surface: "bash"`.**
  Fall back to `surface: string`, documented; `bash` is never a family, so the fold never runs for it.
- **A fixture outside the grepped set dispatches on `kind === "tool"` for a gate-emitted bash intent and silently falls through.**
  Step 1's exhaustive `switch`es make the shared helpers fail at compile time.
  For inline mocks, step 5 runs the full suite before committing (`testing` skill: a shared-helper change runs the full suite).
- **The deny direction changes observable decisions on upgrade.**
  A `~`-prefixed deny that never fired now fires.
  That is the documented contract, and the change is recorded as a fix, not a breaking change (Goals).

## Open Questions

- Should `PermissionCheckResult` report which spelling matched (PR [#917]'s `matchedAlias`)?
  Deferred: for a home spelling the typed command and the pattern already read alike in the prompt.
  The question returns with argument spellings, where they do not.

[#301]: https://github.com/gotgenes/pi-packages/issues/301
[#306]: https://github.com/gotgenes/pi-packages/issues/306
[#309]: https://github.com/gotgenes/pi-packages/issues/309
[#452]: https://github.com/gotgenes/pi-packages/issues/452
[#481]: https://github.com/gotgenes/pi-packages/issues/481
[#490]: https://github.com/gotgenes/pi-packages/issues/490
[#653]: https://github.com/gotgenes/pi-packages/issues/653
[#694]: https://github.com/gotgenes/pi-packages/issues/694
[#712]: https://github.com/gotgenes/pi-packages/issues/712
[#840]: https://github.com/gotgenes/pi-packages/issues/840
[#875]: https://github.com/gotgenes/pi-packages/issues/875
[#917]: https://github.com/gotgenes/pi-packages/pull/917
[#928]: https://github.com/gotgenes/pi-packages/issues/928
[#995]: https://github.com/gotgenes/pi-packages/issues/995
[#1019]: https://github.com/gotgenes/pi-packages/issues/1019
[#1020]: https://github.com/gotgenes/pi-packages/issues/1020
