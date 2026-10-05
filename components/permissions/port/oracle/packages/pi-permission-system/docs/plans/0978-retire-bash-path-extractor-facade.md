---
issue: 978
issue_title: "pi-permission-system: bash-path-extractor.ts has no production caller; retire the facade or narrow its 1300-line test"
---

# Retire the bash external-path facade and give `externalAccesses()` one test home

## Release Recommendation

**Release:** ship independently

The Phase 15 roadmap step `#### [#978] The bash-path facade nobody calls` is tagged `Release: independent`, and the `Release batches` subsection lists it as "(no release)".
Every commit here is `test:`, `refactor:`, or `docs:`, so `cliff.toml` cuts nothing; `/ship` lands it and dispatches no release.

## Problem Statement

`extractExternalPathsFromBashCommand` (`src/handlers/gates/bash-path-extractor.ts`, 23 lines) has no production caller.
Both bash path gates read the injected `BashProgram` directly.
Its only consumer is its own 1300-line test file, `test/handlers/gates/bash-path-extractor.test.ts`, which predates `BashProgram` and has kept re-testing it through a seam nothing uses.
That is the concentrated test-design cluster the Phase 15 craftsmanship scout found: [#821] and [#839] each landed their cases in two files, because a new projection case had two plausible homes.

## Goals

- Retire the facade (operator decision): delete `src/handlers/gates/bash-path-extractor.ts` and its test file.
- Keep every facade case that has no equivalent elsewhere, rewritten against `BashProgram.parse(...).externalAccesses()` directly.
- Give `BashProgram.externalAccesses()` **one** test home (operator decision): a new `test/access-intent/bash/program-external-accesses.test.ts` that takes `program.test.ts`'s `externalPaths` describe as well as the migrated cases, so `program.test.ts` shrinks instead of growing.
- Non-breaking: no production behavior changes; the only `src/` change is deleting a module nothing imports.

## Non-Goals

- Moving `program.test.ts` describes that assert **both** slices (`workdir seed (#574)`, `effect attribution (#807)`, `path operands of a command the parse dropped (#875)`, `an interpreter's inline script (#863)`, and the `derives both slices from a single parse` test).
  Each tests a feature across `pathRuleCandidates()`, `externalAccesses()`, and `commands()`, so it belongs to `BashProgram` as a whole; only the slice-scoped `externalPaths` describe moves.
- Renaming the moved describe's existing test names (several carry an issue number); the move is verbatim.
- Consolidating the `vi.hoisted` `realpathSync` mock into a shared helper: 18 test files inline their own, and the Tidy-First assessor declined it as the wrong abstraction for this change.
- The Phase 15 findings narrative (`architecture.md` lines ~1120–1122) that describes the cluster as found; it records the phase's discovery, not current structure.
- `docs/architecture/history/phase-2-complexity-duplication.md`, which names `bash-path-extractor.ts` as it was in Phase 2; history is not rewritten.

## Background

- `BashProgram` (`src/access-intent/bash/program.ts`) parses once and exposes `externalAccesses(): BashExternalPath[]`; the facade is `(await BashProgram.parse(command, normalizer)).externalAccesses().map(({ path }) => path.value())`.
- `rg`/`grep` over `src/`, `test/`, `docs/`, and `.pi/` for `bash-path-extractor` and `extractExternalPathsFromBashCommand` finds only the module, its test file, `docs/architecture/architecture.md` (module-tree line 969, the roadmap step, the sweep list, and the Phase 15 findings), and the Phase 2 history file.
- The facade test file mocks `node:os` (`homedir` → `/mock/home`) and replaces `node:fs` wholesale with an identity `realpathSync`.
  `program.test.ts` uses the real `homedir()` and a **pass-through** `node:fs` mock whose hoisted `realpathSync` is controllable (lines 1–19).
  The new file takes the `program.test.ts` header, so a migrated case that expected `/mock/home/...` is rewritten to `join(homedir(), ...)`.
- The `externalPaths` describe spans `program.test.ts` lines 496–1328 (833 lines, measured) and holds 81 tests (measured: `vitest run -t externalPaths`).
  It declares its own `cwd`/`normalizer` and a `beforeEach` resetting `realpathSync`; its only module-scope dependencies are the imports (`BashProgram`, `PathNormalizer`, `pathFlavorForPlatform`, `win32PathFlavor`, `createTmpFixture`, `homedir`, `join`) and the hoisted `realpathSync` (the Tidy-First assessor confirmed nothing reaches into a sibling describe).

## Design Overview

### The equivalence audit

Every one of the facade file's 133 tests was classified at planning time (an `Explore` subagent produced the table; its totals were wrong and were recounted, and three verdicts were corrected, as noted below).
A case is **equivalent** when an existing test feeds the same shell construct and asserts the same projection outcome — at the `BashProgram` layer (`program.test.ts`), the collection/classification unit layer (`token-collection.test.ts`, `token-classification.test.ts`) when the construct needs no more parser behavior than that test exercises, the gate layer (`bash-external-directory.test.ts`), or, for the five presentation tests, `path-ask-payload.test.ts` / `agent-renderer.test.ts`.
A different construct is not equivalent: `a | b` is not `a && b`, an in-cwd negative is not an outside-cwd positive, and a posix case is not a win32 one.

Result: **56 equivalent, 77 move** (133 total).
Corrections to the subagent's table: `bare // token normalizes to root` (line 414) and `bare /// …` (419) are **move**, because `token-classification.test.ts` returns `//` as-is and the collapse to `/` is the normalizer's; `detects path in redirect target` (263) is **equivalent**, since it is the same command as line 632, which the table itself marked equivalent.

Equivalent cases (deleted, not migrated), by facade line:

| Facade line            | Facade test                                 | Equivalent                                                                                     |
| ---------------------- | ------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| 48                     | detects absolute path outside CWD           | `program.test.ts` "returns absolute paths resolving outside cwd"                               |
| 56                     | detects multiple absolute paths outside CWD | `token-collection.test.ts` "collects all argument tokens after the command name"               |
| 78, 86, 94             | three glob-bearing absolute paths           | `program.test.ts` `glob-bearing path tokens (#821)` `it.each` (same commands)                  |
| 139                    | for loop's word-list operand                | `program.test.ts` "flags a for loop's home-relative word-list operand" (same command)          |
| 149                    | select statement's word-list operand        | `token-collection.test.ts` "collects the operand a select statement names directly"            |
| 157                    | case subject                                | `program.test.ts` "flags an absolute case subject" (same command)                              |
| 165                    | in-cwd word-list operand                    | `program.test.ts` "leaves an in-cwd word-list operand off the external slice"                  |
| 175, 1063              | `../` escaping cwd, no `cd`                 | `program.test.ts` "reproduces cwd-based resolution when no workdir is given"                   |
| 193                    | relative paths within CWD                   | `program.test.ts` "excludes paths within cwd" (same command)                                   |
| 201                    | bare command, no path arguments             | `program.test.ts` "does not flag a bare word naming nothing" (`git status`)                    |
| 211, 587               | flags are not paths                         | `token-classification.test.ts` "flag (leading dash) → null"                                    |
| 229, 566               | `FOO=/bar` assignment                       | `token-collection.test.ts` "skips variable assignment prefixes"                                |
| 263, 632               | output redirect target                      | `program.test.ts` "still projects a plain redirect destination"                                |
| 273, 281, 577          | URLs                                        | `token-classification.test.ts` "URL → null"                                                    |
| 291                    | `@scope/package`                            | `token-classification.test.ts` "@scope/package → null"                                         |
| 409                    | bare `/`                                    | `token-classification.test.ts` "bare-slash token (filesystem root) → returned as-is"           |
| 452                    | multi-line `node -e`                        | `token-collection.test.ts` "a flag-supplied script contributes no token" (the multi-line row)  |
| 478, 486               | quoted `--regexp=` / `awk -F':'` value      | `token-collection.test.ts` "reads a quoted glued value on a recognized flag as a flag (#957)"  |
| 545                    | unquoted heredoc body                       | `token-collection.test.ts` "returns empty array for heredoc-only content (SKIP_SUBTREE_TYPES)" |
| 615                    | substitution in a quoted heredoc            | `token-collection.test.ts` `operands hosted in a heredoc body (#741)` `it.each`                |
| 666                    | deduplication                               | `program.test.ts` "keeps the effect when the two proofs agree" (`toHaveLength(1)`)             |
| 684, 709, 727          | `sed` script / `-e` / `-f`                  | `token-collection.test.ts` `collectCommandTokens — pattern-first commands` `sed` rows          |
| 736, 899               | `sed -i ''` (BSD)                           | `token-collection.test.ts` "consumes an empty suffix argument (the BSD idiom)"                 |
| 883                    | `sed -i` (GNU)                              | `token-collection.test.ts` "declines a non-empty argument, which GNU reads as the script"      |
| 907                    | `sed -i.bak`                                | `token-collection.test.ts` "leaves a glued suffix alone"                                       |
| 747, 756               | `grep` pattern / `-e`                       | `token-collection.test.ts` `grep` rows                                                         |
| 776                    | `awk -F:`                                   | `token-collection.test.ts` "reads a quoted glued value … (#957)" (`awk -F':'`)                 |
| 787                    | `rg` pattern                                | `token-collection.test.ts` "rg: skips the pattern positional …"                                |
| 807                    | `sd` two patterns                           | `token-collection.test.ts` "sd: skips the first two positionals …"                             |
| 826                    | unknown command, generic extraction         | `token-collection.test.ts` "collects all argument tokens after the command name"               |
| 845                    | `--` end of flags                           | `token-collection.test.ts` "grep: end-of-flags (--) …"                                         |
| 917, 925               | `grep -A 3` / `--regexp=harmless`           | `program.test.ts` `flag spellings of a pattern-first command (#823)` `it.each`                 |
| 933                    | `--regexp=/etc/passwd` value                | `program.test.ts` "does not project a pattern flag's own value" (same command)                 |
| 1035                   | `cd` into a subdir, in-cwd `../`            | `program.test.ts` "folds a sequence of current-shell cd commands"                              |
| 1108, 1122, 1133, 1147 | four ask-payload tests                      | `path-ask-payload.test.ts` `buildBashExternalDirectoryAskPayload` (both tests)                 |
| 1217                   | win32 `/dev/null` redirect                  | `bash-external-directory.test.ts` "does not prompt for a /dev/null redirect target"            |
| 1266, 1271             | win32 `/tmp`, `/usr` kept as typed          | `program.test.ts` "keeps a non-mount POSIX absolute literal (Git Bash semantics)"              |
| 1283                   | bash external-directory denial              | `agent-renderer.test.ts` bash `external_directory` denial (line ~189)                          |

Move cases (77), by facade line and the step that migrates them:

- Step 2, shell syntax (43): 65, 102, 110, 120, 128, 183, 219; pipe/`;`/`&&` 239, 247, 255; quoting 301, 309, 317, 325; devices 337, 345, 353, 361, 369, 377, 386; bare slash 396, 401, 414, 419, 424, 432; `node -e` 444, 460; quoted flag beside an operand 494; tokenizer 504, 513, 521; heredoc 533, 539, 551, 558; substitution/subshell 599, 607, 622; redirects 640, 648, 656.
- Step 3, pattern-first commands (19): 678, 692, 701, 718, 767, 796, 816, 836, 855, 863, 872; regex arguments 949, 957, 965, 973, 981, 989, 997, 1005.
- Step 4, `cd` prefix and win32 (15): 1015, 1027, 1043, 1055, 1072, 1082; 1179, 1184, 1189, 1194, 1222, 1240, 1245, 1250, 1276.

### The new file's shape

```typescript
// test/access-intent/bash/program-external-accesses.test.ts
// header: program.test.ts lines 1–19 (hoisted realpathSync, pass-through node:fs mock)
describe("BashProgram", () => {
  describe("externalAccesses", () => {
    const cwd = "/projects/my-app";
    const normalizer = new PathNormalizer(pathFlavorForPlatform(process.platform), cwd);
    beforeEach(() => { realpathSync.mockReset(); realpathSync.mockImplementation((p) => p); });
    // …moved externalPaths contents, then migrated concern describes
  });
});
```

The moved describe is renamed `externalPaths` → `externalAccesses` (the method's real name) in step 1; its contents are verbatim.
Migrated cases go into behavior-named describes (`statement separators`, `quoted strings`, `safe device paths`, `the filesystem root`, `shell comments`, `heredocs`, `command substitution and subshells`, `redirect operators`, `pattern-first commands`, `regex arguments`, `a leading cd`, `win32 drive paths`, `Git Bash device paths and mounts`), or into the moved describe's existing sibling when one already names the concern (the two glob cases join `glob-bearing path tokens (#821)`; the win32 cases join `win32 projection (injected platform, no vi.mock node:path)`).
A migrated describe's name carries no issue number (the `testing` skill).
Each migrated test uses a local helper so the act stays explicit:

```typescript
async function externalValuesOf(command: string, at: PathNormalizer = normalizer) {
  return (await BashProgram.parse(command, at)).externalAccesses().map(({ path }) => path.value());
}
```

Migration tightens the weak assertions: every `toContain`, `not.toHaveLength(0)`, and `length > 0` becomes a full `toEqual([...])`, with the expected value taken from a run rather than predicted (the audit flagged 27, including facade lines 219, 239, 401, 432, 599, 1043, 1063-style `length > 0`, 1179, 1184).
Where several cases share one shape (the three `/dev/std*` negatives), an `it.each` table is acceptable.

## Module-Level Changes

- `test/access-intent/bash/program-external-accesses.test.ts` — **new**: the moved `externalPaths` describe (renamed `externalAccesses`) plus the 77 migrated cases; about 1,550 lines (estimated).
- `test/access-intent/bash/program.test.ts` — the `externalPaths` describe (lines 496–1328) is removed; 2916 → ~2083 lines (833 removed, measured span), 342 → 261 tests (measured).
  Re-check its imports after the removal: `win32PathFlavor`, `createTmpFixture`, `homedir`, and `join` all have users outside the span (measured), but confirm with `pnpm run check` and Biome.
- `test/handlers/gates/bash-path-extractor.test.ts` — shrinks per step (each migrated case and its equivalents deleted in the same commit), then **deleted** in step 5.
- `src/handlers/gates/bash-path-extractor.ts` — **deleted** in step 5.
- `docs/architecture/architecture.md` — module-tree line 969 (`bash-path-extractor.ts  Thin facade …`) removed in step 5; the roadmap step heading `#### [#978] The bash-path facade nobody calls` and the Mermaid node `S978["#978<br/>The facade nobody calls"]` gain `✅`, with a `Landed:` note, in step 6.
- Predicted unchanged: `test/presentation/path-ask-payload.test.ts` and `test/presentation/agent-renderer.test.ts` (all five presentation cases are equivalent), `.pi/skills/package-pi-permission-system/SKILL.md` (names neither symbol, grep-verified), and `.fallowrc.json` (test files are not zoned; the deleted module adds no edge).

## Test Impact Analysis

1. New tests the change enables: none — it relocates tests; the migrated cases now exercise `BashProgram` without a facade in between.
2. Redundant tests removed: the 56 equivalent cases in the table above.
3. Tests that must stay as-is: the 81 moved `externalPaths` tests (verbatim) and every cited equivalent (the table is the claim that they carry the deleted cases' weight).

Count invariant (measured baseline): `program.test.ts` + `bash-path-extractor.test.ts` = 342 + 133 = 475 tests.
After step 5: `program.test.ts` 261 + `program-external-accesses.test.ts` 158 (81 + 77) = 419 = 475 − 56.
Each step's commit keeps the sum of the three files equal to 475 minus the equivalents deleted so far.

## Invariants at risk

- Phase 15 Outcome for this step: "no test file re-tests `BashProgram` through an unused facade; the `bash-path-extractor.ts` module-tree entry matches the decision."
  Pinned by the file's deletion and step 5's `rg extractExternalPathsFromBashCommand` returning nothing.
- The regression cases earlier steps landed through the facade ([#821] globs, [#839] statement operands, [#823] flag spellings, [#957] quoted flag values, [#583] `find /`, the `sed` address-pattern reproducer).
  Each is either cited in the equivalence table or migrated; the killing mutations below prove the migrated ones still bite at their new site.
- Constituency: the `bash` `external_directory` gate's users.
  The gate reads `externalAccesses()` directly, so the tests that pin it are now at the layer it calls.

## TDD Order

No step changes behavior, so there is no red phase: each step's check is that the relocated tests pass and that its killing mutation turns them red.

1. **`test(pi-permission-system): give BashProgram.externalAccesses its own test file`.**
   Create `program-external-accesses.test.ts` with `program.test.ts`'s header (lines 1–19, plus only the imports the moved block uses) and move the `externalPaths` describe (lines 496–1328) verbatim under `describe("BashProgram") > describe("externalAccesses")`; delete it from `program.test.ts`.
   Verify: the new file runs 81 tests and `program.test.ts` 261; `pnpm run check` and `pnpm run lint` are clean (orphaned imports).
   Killing mutation: in `src/access-intent/bash/program.ts`, make `externalAccesses()` return `[]` — every positive test in the new file goes red, proving the moved block runs against the real module and not a stale mock.
2. **`test(pi-permission-system): pin the shell-syntax external-access cases on BashProgram`.**
   Migrate the 43 step-2 cases into the new file, rewritten with `externalValuesOf` (`/mock/home/...` → `join(homedir(), ...)`; the in-cwd `~` case builds its own `PathNormalizer` at `join(homedir(), "myproject")`), with weak assertions tightened to `toEqual` values taken from a run.
   In the same commit, delete those 43 plus the equivalent cases from the same facade blocks (lines 48–666 in the table) from the facade file.
   Killing mutations: make `isSafeSystemPath` (`src/path/safe-system-paths.ts`) return `false` → the six device negatives (337–377) go red, 386 stays green (predicted); remove `"comment"` from `SKIP_SUBTREE_TYPES` (`node-text.ts`) → the comment cases (513, 521) go red; remove `"heredoc_body"` from it → the heredoc cases (533, 539, 551, 558) go red.
3. **`test(pi-permission-system): pin the pattern-first command external-access cases on BashProgram`.**
   Migrate the 19 step-3 cases and delete them plus their equivalents (the `command-aware extraction`, `flag spellings`, and `regex arguments` blocks) from the facade file.
   Killing mutations: delete the `["sed", SED_CONFIG]` entry from `PATTERN_FIRST_COMMANDS` (`token-collection.ts`) → the `sed` cases (678, 692, 718, 981) go red; delete the `["grep", GREP_CONFIG]` entry → the `grep` regex cases (949, 957, 965, 973, 1005) go red.
4. **`test(pi-permission-system): pin the cd-prefix and win32 external-access cases on BashProgram`.**
   Migrate the 15 step-4 cases (the win32 ones build `new PathNormalizer(win32PathFlavor, "C:/projects/app")` and pass it to `externalValuesOf`; the drive-letter cases 1179/1184 get a full `toEqual` from a run) and delete them plus the remaining equivalents (1035, 1063, 1217, 1266, 1271) from the facade file.
   Killing mutations: make `foldCd` (`bash-path-resolver.ts`) return `base` unchanged → the `cd`-prefix cases that depend on a folded base (1015, 1043, 1055, 1072, 1082) go red, 1027 (an absolute operand) stays green (predicted); make `MSYS_DRIVE_MOUNT_PATTERN` (`msys-bash-tokens.ts`) match nothing (`/(?!)/`) → the mount cases (1240, 1245, 1250) go red.
5. **`refactor(pi-permission-system): retire the unused bash external-path facade`.**
   The facade file now holds only equivalent cases (the four ask-payload tests and the denial test); delete it and `src/handlers/gates/bash-path-extractor.ts`, and remove the module-tree line from `docs/architecture/architecture.md`.
   Verify: `rg -n 'extractExternalPathsFromBashCommand|bash-path-extractor' packages/pi-permission-system/src packages/pi-permission-system/test` prints nothing; `pnpm run check`, `pnpm run lint`, `pnpm --silent fallow dead-code`, and the full package suite pass; the three-file count is 419.
6. **`docs(pi-permission-system): mark the facade retirement landed`.**
   Mark the roadmap heading and Mermaid node `✅` and add a `Landed:` note naming the commits and the measured result (56 equivalent, 77 migrated, `program.test.ts` 2916 → its measured post-change line count).

## Risks and Mitigations

- **An "equivalent" verdict is wrong and a regression case is lost.**
  The table names the equivalent for every deleted case, so a reviewer can open each pair; where a verdict rests on the unit layer (`token-collection`, `token-classification`), the construct needs no parser behavior beyond what that unit test parses.
  When the implementing session doubts a row, it migrates the case; the count invariant shifts by one and the `Landed:` note records the real split.
- **The mock headers differ.**
  The facade suite's wholesale `node:fs` mock leaves `lstatSync` undefined, whereas the new file uses the real one, so the existence probe now actually runs for bare tokens such as `input.txt` and `notes.txt`.
  None exists under `/projects/my-app`, so the outcome should be unchanged; the migrated assertions are tightened from a run, which catches any divergence.
- **The real `homedir()` differs per host.**
  The migrated `~` cases build their expected values with `join(homedir(), ...)`, as `program.test.ts` already does, and `realpathSync` stays identity-mocked, so a symlinked home directory does not change the value.
- **Weak-assertion tightening changes what a test claims.**
  A `toEqual` value from a run can pin a surprising projection (an extra path).
  If a run shows a value the facade test's name contradicts, stop and file an issue rather than pinning it.

## Open Questions

None.

[#583]: https://github.com/gotgenes/pi-packages/issues/583
[#821]: https://github.com/gotgenes/pi-packages/issues/821
[#823]: https://github.com/gotgenes/pi-packages/issues/823
[#839]: https://github.com/gotgenes/pi-packages/issues/839
[#957]: https://github.com/gotgenes/pi-packages/issues/957
