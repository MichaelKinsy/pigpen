---
issue: 859
issue_title: "pi-permission-system: git revision ranges are classified as path candidates, raising false external_directory asks"
---

# `..` is a path signal only as a whole segment

## Release Recommendation

**Release:** ship independently

Phase 15's roadmap step for this issue carries `Release: independent`, and the `Release batches` subsection lists it under "Independently releasable" with commit type `fix:`.
The only batch in the phase is `declared-effects` ([#880], [#881]), which this step is not part of.

## Problem Statement

A git revision range raises an `external_directory` prompt that names the range as an "external path".
The reporter's scenario, with the session cwd at `/home/user/work`:

```bash
cd ~/work/llama.cpp && git fetch origin master -q; git log --oneline cc83d7b48..origin/master
```

The prompt listed `external path: /home/user/work/cc83d7b48..origin/master`.
That token is a revision argument to `git log`, and the command touches nothing outside the cwd.
Three-dot ranges (`main...feature`) produce the same result.

Two mechanisms combine to produce it:

1. `classifyTokenAsPathCandidate` accepts any token where `token.includes("..")` is true, so `cc83d7b48..origin/master` counts as a parent-traversal candidate.
   A `..` inside a segment never traverses anything.
2. `cd ~/…` is non-literal by design (`literalTextOf` returns `null` for a `~`-prefixed `cd` target), so the effective base becomes unknown.
   `projectExternalPaths` then flags any *relative* strict candidate conservatively ([#393]).

`classifyTokenAsRuleCandidate` has the same substring test, so a slashless range (`v1..v2`) also becomes a `path` rule candidate.

## Goals

- Treat `..` as a parent-traversal signal only when it forms a whole path segment: the token is exactly `..`, or the `..` is bounded on each side by a separator (`/` or `\`) or a token edge.
- Use one private predicate for both classifiers, so "parent traversal" has a single definition in the module.
- `cd ~/x && git log HEAD..origin/main` no longer yields an external access for the range, and `git log v1..v2` under the same base no longer yields a `path` rule candidate for it.
- Leave every whole-segment spelling that is accepted today accepted, including `..\foo`.
  Keep the [#645] existence probe as the fallback for a token that exists on disk.
- Non-breaking: no config key, default, or output shape changes.
  The only observable change is fewer false-positive asks, so the commit type is `fix:`.

## Non-Goals

- **Folding a `~`-prefixed `cd` into a known base.**
  The issue calls the unknown base "defensible" on its own, and `literalTextOf`'s `~` exclusion is deliberate.
  Resolving `cd ~/x` to a known base is a different change with its own risk.
- **Brace expansion** (`cat {..,y}/z` reads `../z`).
  With a known base this is already missed today: the literal resolves as one in-cwd segment (measured: `externalAccesses()` is `[]`).
  With an unknown base it is flagged today only because the substring test happens to match it, and this change removes that.
  Filed as [#968] and deferred to a later phase beside [#822] (operator decision, recorded in the roadmap's sweep list).
- **The downstream `Authorizer` concern** the reporter mentions (a link that defers on `external_directory`) is [#882]'s question.
- **Reworking `probeBareToken`** or the unknown-base conservatism in `bash-path-resolver.ts`.
  Neither file changes.
- **ADR 0009 amendment.**
  Its guarantee bullet already names the shape as "parent-traversal (`../x`)", which the new predicate matches exactly.
  The "Definitely a path … `..`" line sits in the ADR's Context section, a historical record, so it is not edited.

## Background

- `src/access-intent/bash/token-classification.ts`: the three pure classifiers.
  `classifyTokenAsPathCandidate` is the strict gate for `external_directory`: `/`, `~/`, `includes("..")`, and drive-letter absolutes.
  `classifyTokenAsRuleCandidate(token, flavor)` is the broader gate for `path`: `startsWith(".")`, `flavor.hasPathSeparator`, `includes("..")` (commented `// bare ".." (no slash)`), and drive-letter absolutes.
  `WINDOWS_DRIVE_PATH_PATTERN` and `URL_PATTERN` are private module constants in the "Private rejection predicate" region.
- `src/access-intent/bash/bash-path-resolver.ts`:
  - `projectExternalPaths` calls `classifyTokenAsPathCandidate`.
    On `null` it falls back to `probeBareToken`, which returns `null` under an unknown base before it touches the filesystem.
    Under a known base it promotes the token only when `entryExists` confirms it.
  - `projectRuleCandidates` falls back to the same probe.
  - Neither function changes.
- ADR 0009 (`docs/decisions/0009-bash-path-projection-completeness-contract.md`): a shape-classified parent-traversal token (`../x`) is a guarantee.
  A bare token naming an existing entry is the [#645] probe's guarantee.
  The layering principle holds that over-suppression is unrecoverable, so this plan measures what it drops rather than arguing it.
- This issue's Phase 15 roadmap step in `docs/architecture/architecture.md` states this exact target.
  The step also carries a health-metric row: substring `..` tests, baseline 2, target 0.
- AGENTS.md / package skill constraints that apply:
  - The classifiers stay policy-free (ADR 0009).
  - Token shape is judged after `$HOME`/`$PWD` expansion.
  - Before asserting a bash repro string, trace the token through the classifier (done below).

### Third-party provenance

`TacoTakumi` filed the issue and posted the predicate `/(^|[/\\])\.\.($|[/\\])/` with unit tests in [a comment](https://github.com/gotgenes/pi-packages/issues/859#issuecomment-5486843282).
The operator adopted that design and extended it to both classifiers, as the roadmap step specifies.
The implementing commits therefore carry the `Co-authored-by:` trailer recorded in TDD Order.

## Design Overview

### The predicate

```typescript
/**
 * A `..` standing as a whole path segment: exactly `..`, or a `..` bounded by
 * a separator (`/` or `\`) or a token edge on both sides. A `..` inside a
 * segment is not a traversal: a git revision range (`HEAD..origin/main`,
 * `v1..v2`, `a...b`) never names a parent directory. A real file whose name
 * carries `..` is still reached through the resolver's existence probe.
 */
const PARENT_TRAVERSAL_SEGMENT_PATTERN = /(^|[/\\])\.\.($|[/\\])/;

function hasParentTraversal(token: string): boolean {
  return PARENT_TRAVERSAL_SEGMENT_PATTERN.test(token);
}
```

Both classifiers call `hasParentTraversal(token)` where they now call `token.includes("..")`.
The backslash counts as a separator in both classifiers regardless of flavor.
That keeps today's acceptance of `..\foo` and `foo\..` unchanged: the strict classifier takes no flavor, and on win32, Git Bash hands a `\` to Windows file APIs as a separator.

In `classifyTokenAsRuleCandidate` the branch is now reachable only by a POSIX backslash form (`foo\..`, `a\..\b`).
A token that is exactly `..`, or that starts with `../`, is already accepted by `startsWith(".")`, and any `/`-bearing token by `hasPathSeparator`.
The branch stays so that one definition governs both gates, and its trailing comment is corrected, since `// bare ".." (no slash)` is already wrong today.

### Token trace (the issue's repro through the classifiers)

| Token                                 | Strict: before → after | Rule: before → after        | Strict surface after          |
| ------------------------------------- | ---------------------- | --------------------------- | ----------------------------- |
| `cc83d7b48..origin/master`            | accept → `null`        | accept → accept (separator) | probe → `null` (unknown base) |
| `HEAD..origin/master`                 | accept → `null`        | accept → accept (separator) | probe → `null` (unknown base) |
| `v1..v2`                              | accept → `null`        | accept → `null`             | probe → `null` (unknown base) |
| `main...feature`                      | accept → `null`        | accept → `null`             | probe → `null` (unknown base) |
| `../secret`, `a/../b`, `foo/..`, `..` | accept → accept        | accept → accept             | unchanged                     |
| `..\foo`                              | accept → accept        | accept → accept             | unchanged                     |

Under a known base, an in-segment token that exists on disk is still promoted by the probe.
If it is a symlink out of the tree, it is still flagged external, by the same `collectIfExternal` call the strict branch uses.

### How the evidence was produced

- **Scenario repro:** a disposable vitest spike called the real `BashProgram.parse` (real `tree-sitter-bash`, collector, resolver) with a POSIX `PathNormalizer`.
  The cwd was `/home/user/work` and the commands were synthetic spellings of the issue's repro, since the reporter's session was not available.
  Measured at `60c22b0a`: `cd ~/x/sub && git log HEAD..origin/master` flags `/home/user/work/HEAD..origin/master`.
  `v1..v2` and `main...feature` behave the same.
  The control `git log HEAD..origin/master` with no `cd` flags nothing, which isolates the unknown base as the second factor.
- **Corpus diff:** a second spike ran `externalAccesses()` and `pathRuleCandidates()` over 8360 distinct bash commands from the local review log (`~/.pi/agent/extensions/pi-permission-system/logs/pi-permission-system-permission-review.jsonl`).
  Truncated `…` commands were excluded, and the normalizer cwd was `/Users/chris/development/pi/pi-packages`.
  It ran once at `HEAD` and once with the predicate temporarily applied to both classifiers.
  The corpus is one author's log under one policy, and the source is deterministic, so n = 1 per condition.

| Surface                                   | Tokens lost   | Commands affected | Tokens gained |
| ----------------------------------------- | ------------- | ----------------- | ------------- |
| `external_directory` (`externalAccesses`) | 28 (measured) | 27 (measured)     | 0 (measured)  |
| `path` (`pathRuleCandidates`)             | 92 (measured) | 85 (measured)     | 0 (measured)  |

Every lost token is a revision range (`v0.84.4..HEAD`, `9135d33^..HEAD`, `origin/main..main`, `$BASE..HEAD`), a bare `...`, or prose inside a quoted argument (`Link to [target](../src/target.ts).\n`, `(../pi resolves)`).
None of them contains a whole `..` segment, so none can traverse upward lexically.
Only 2 commands lose *every* external access, which means only there does an ask disappear rather than just get shorter: `cd ~/development/pi/pi-packages && … git diff --name-only $BASE..HEAD`, and a `printf 'Link to [target](../src/target.ts).\n'` command.
Both were false positives.
In the other 25, the ask still fires, carried by the `cd` target or another token, and only its evidence list gets shorter.

- **Existing suite:** with the predicate applied, the package's full suite passed (4584 tests, 168 files, measured).
  No existing test asserts that an in-segment `..` is accepted, so the Red comes entirely from new tests.

## Module-Level Changes

- `src/access-intent/bash/token-classification.ts`
  - Add the private `PARENT_TRAVERSAL_SEGMENT_PATTERN` and `hasParentTraversal(token)` in the "Private rejection predicate" region, beside `WINDOWS_DRIVE_PATH_PATTERN`.
    It is added below the classifiers that call it, per the stepdown rule.
  - `classifyTokenAsPathCandidate`: `token.includes("..")` → `hasParentTraversal(token)`.
    The doc bullet becomes "Parent-traversal paths (carrying a whole `..` segment)".
  - `classifyTokenAsRuleCandidate`: the same substitution, with the trailing comment corrected (see Design Overview).
    The doc comment's `must start with "/" or "~/" or contain ".."` becomes `… or carry a whole ".." segment`.
- `test/access-intent/bash/token-classification.test.ts`
  - In `describe("path-candidate acceptance gate")`, the test `parent-traversal (contains ..) → returned as-is` is renamed to `(a whole .. segment)` and gains `foo/..`, `a/../b`, `..\\foo`, and `foo\\..`.
  - A new sibling test covers `.. inside a segment (revision ranges) → null` for `HEAD..origin/master`, `cc83d7b48..origin/master`, `v1..v2`, and `main...feature`.
  - In `describe("rule-candidate acceptance gate (broader than path)")`, the same rename applies, plus a sibling test: slashless `v1..v2` and `main...feature` → `null`, `HEAD..origin/master` → accepted via its separator (pinned: the range stays a `path` rule candidate by its separator, independent of the `..` rule), and POSIX `foo\\..` → accepted.
- `test/access-intent/bash/program.test.ts`
  - In `describe("externalPaths")` → `describe("effective working directory projection")`, right after `flags relative paths conservatively after a non-literal cd`:
    - `does not flag a revision range after a non-literal cd`: `cd ~/x && git log HEAD..origin/main` → `externalAccesses()` values `toEqual([join(homedir(), "x")])`, which is the `cd` target alone.
    - A control: `cd ~/x && cat a/../../b` is still flagged.
  - In `describe("pathRuleCandidates")` → `describe("existence-probe bare-token promotion (#645)")` (which already holds the unknown-base probe case): `cd ~/x && git log v1..v2` → tokens `toEqual(["~/x"])`.
  - In `describe("bare tokens escaping the tree via symlink (#645)")`: a symlink named `v1..v2` that points outside the cwd is still flagged by `cat v1..v2`.
    This pins the fall-through from the strict gate to the probe.
- `src/access-intent/bash/bash-path-resolver.ts`: **predicted unchanged.**
  The claim rests on `projectExternalPaths` and `projectRuleCandidates` already routing a classifier `null` to `probeBareToken` (verified by reading lines 466–474 and 562–568, and confirmed by the Tidy-First assessor).
- `docs/architecture/architecture.md`
  - Module-tree entry for `token-classification.ts` (the line ~929 entry): `classifyTokenAsPathCandidate (strict: /, ~/, .., Windows drive-letter)` becomes `(strict: /, ~/, a whole .. segment, Windows drive-letter)`.
  - Phase 15 step heading `#### [#859] …` → `#### ✅ [#859] …`, plus a `Landed:` note under it.
  - Mermaid node `S859["#859<br/>.. as a whole segment"]` → `S859["✅ #859<br/>.. as a whole segment"]`.
  - Health-metric row "Substring `..` tests": recompute with `grep -c 'includes("..")' packages/pi-permission-system/src/access-intent/bash/token-classification.ts`.
    It measures 2 at planning and is predicted to read 0.
    Leave the dated `Baseline` column alone.
- `docs/decisions/0009-bash-path-projection-completeness-contract.md`: **predicted unchanged** (see Non-Goals).
  Its guarantee bullet says "parent-traversal (`../x`)".
- `docs/configuration.md`: **predicted unchanged.**
  Line 742 already says "parent-traversal (`../`)".
- `.pi/skills/package-pi-permission-system/SKILL.md`: **predicted unchanged.**
  It names the classifiers and not their `..` rule (grepped: its only `..` mention is Pi's containment idiom).

## Test Impact Analysis

1. **New tests enabled:** direct unit coverage of the in-segment vs. whole-segment boundary for both classifiers, plus two end-to-end pins at the resolver level, one for each surface, under the issue's own unknown-base trigger.
2. **Redundant tests:** none.
   The existing `..` assertions (`../../etc/passwd`, `../foo`, `..`) become the whole-segment positive class and stay.
3. **Tests that must stay as-is:** `flags relative paths conservatively after a non-literal cd` (`cd "$DIR" && cat ../x`) and `flags even a within-cwd relative path after a non-literal cd` (`src/../within.txt`).
   They exercise the unknown-base conservatism this change must not weaken for real traversals.

## Invariants at risk

| Invariant                                                                          | Constituency                                                                      | Pinned by                                                                                                                                                                                |
| ---------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| ADR 0009: a shape-classified parent-traversal token (`../x`) reaches both surfaces | Every policy with an `external_directory` or `path` rule; least-privilege default | `token-classification.test.ts` whole-segment positives; `program.test.ts` `flags relative paths conservatively after a non-literal cd`                                                   |
| Unknown-base conservatism ([#393]) still flags a real traversal                    | Users running `cd "$DIR"` / `cd ~/x` compound commands                            | `program.test.ts` `flags even a within-cwd relative path after a non-literal cd` (`src/../within.txt`), plus the new `a/../../b` control                                                 |
| [#645] probe catches a bare token escaping via symlink                             | Symlink-bypass defense                                                            | New `v1..v2` symlink test in `bare tokens escaping the tree via symlink (#645)`                                                                                                          |
| [#945]/[#863] outcomes (hosted operands, inline scripts)                           | Same corpus and prompt shape                                                      | Untouched: this change edits no collector code, and those steps' `token-collection.test.ts` and `program.test.ts` pins stay green (full suite measured green with the predicate applied) |

I opened each named test.
`program.test.ts` mocks only `node:fs.realpathSync` (identity by default) and runs the real parser, collector, classifiers, and resolver, so it pins the layer under change.

The quantitative invariant is the corpus diff in Design Overview: 0 tokens gained on either surface, and every lost token is non-traversing.
Re-run the spike after implementation (source below) and confirm the figures match.

### The corpus spike

Write this to `packages/pi-permission-system/test/spike-859.test.ts`.
Run it with `SPIKE_OUT=/tmp/ext859-<label>.json pnpm --filter @gotgenes/pi-permission-system exec vitest run test/spike-859.test.ts` once at the pre-change commit and once at the post-change commit, diff the two files, then delete the spike.

````typescript
import { readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { it } from "vitest";
import { BashProgram } from "#src/access-intent/bash/program";
import { posixPathFlavor } from "#src/path/path-flavor";
import { PathNormalizer } from "#src/path/path-normalizer";

const LOG = join(
  homedir(),
  ".pi/agent/extensions/pi-permission-system/logs",
  "pi-permission-system-permission-review.jsonl",
);

it("measures", async () => {
  const seen = new Set<string>();
  for (const line of readFileSync(LOG, "utf8").split("\n")) {
    if (!line.trim()) continue;
    let e: Record<string, unknown>;
    try {
      e = JSON.parse(line) as Record<string, unknown>;
    } catch {
      continue;
    }
    if (e.toolName !== "bash") continue;
    const c = typeof e.command === "string" ? e.command : undefined;
    if (!c || c.endsWith("…")) continue;
    seen.add(c);
  }
  const n = new PathNormalizer(
    posixPathFlavor,
    "/Users/chris/development/pi/pi-packages",
  );
  const out: Record<string, { ext: string[]; rule: string[] }> = {};
  for (const c of seen) {
    const p = await BashProgram.parse(c, n);
    out[c] = {
      ext: p.externalAccesses().map(({ path }) => path.value() ?? ""),
      rule: p.pathRuleCandidates().map(({ token }) => token),
    };
  }
  writeFileSync(process.env.SPIKE_OUT ?? "/tmp/ext859.json", JSON.stringify(out));
}, 600_000);
````

The log grows with use, so the command count will exceed 8360 on a later run.
Compare the *classes* of lost tokens, not only the totals.
The totals that must hold are **0 gained** on both surfaces and **no lost token with a whole `..` segment**.

## TDD Order

Every commit in this plan that carries the adopted design ends with a final trailer paragraph (below any `Refs #859` line):

```text
Co-authored-by: TacoTakumi <195555+TacoTakumi@users.noreply.github.com>
```

The address was resolved from `gh api users/TacoTakumi` (`id` 195555).
Verify each commit with `git interpret-trailers --parse`.

1. **Extract the parent-traversal test (Tidy First, behavior-preserving).**
   The friction it prepares: the fix changes behavior at two call sites.
   Lifting the current `token.includes("..")` semantics into one private `hasParentTraversal(token)` first turns the fix into a single-function-body edit that a reviewer can tell apart from the dedup.
   - Change: add `function hasParentTraversal(token: string): boolean { return token.includes(".."); }` in the "Private rejection predicate" region, and switch both classifiers to call it.
     No test changes.
   - Verify: `pnpm --filter @gotgenes/pi-permission-system exec vitest run test/access-intent/bash/token-classification.test.ts` is green, and `pnpm run check` passes.
   - Sanity mutation (not committed): make `hasParentTraversal` return `false`.
     The existing `parent-traversal … → returned as-is` test under `classifyTokenAsPathCandidate` must go red on `..` and `../foo`.
     That proves the relocated call is pinned at its new site.
   - Commit: `refactor(pi-permission-system): route both classifiers' .. test through one predicate`.
     No co-author trailer, since it carries none of the adopted design.
2. **Require a whole `..` segment.**
   - Red, in `token-classification.test.ts`: the renamed whole-segment positive tests plus the new in-segment tests for both classifiers (Module-Level Changes).
     Expected red: the in-segment `null` assertions (strict `HEAD..origin/master`, `cc83d7b48..origin/master`, `v1..v2`, `main...feature`; rule `v1..v2`, `main...feature`).
     The whole-segment positives and the rule classifier's `HEAD..origin/master` separator pin pass at Red as invariant pins; the mutations below discharge them.
   - Red, in `program.test.ts`: `does not flag a revision range after a non-literal cd` (red: the values include `/projects/my-app/HEAD..origin/main`), and the `pathRuleCandidates` test `cd ~/x && git log v1..v2` → `["~/x"]` (red: `v1..v2` is present).
     Also add the `a/../../b` control and the `v1..v2` symlink test, both of which are invariant pins that pass at Red.
   - Green: add `PARENT_TRAVERSAL_SEGMENT_PATTERN` and replace `hasParentTraversal`'s body with `PARENT_TRAVERSAL_SEGMENT_PATTERN.test(token)`.
     Then update both classifiers' doc comments, the module header if it names the rule, and the rule classifier's trailing comment.
   - Killing mutations (apply each after Green, confirm red, revert):
     - In-segment class: set `hasParentTraversal`'s body back to `token.includes("..")`.
       This must fail every in-segment unit test and both `program.test.ts` range tests.
     - Rule-classifier call site: in `classifyTokenAsRuleCandidate` only, replace `hasParentTraversal(token)` with `token.includes("..")`.
       This must fail the rule classifier's `v1..v2`/`main...feature` tests and the `pathRuleCandidates` `v1..v2` test, while the strict-classifier tests stay green.
     - Token-edge class: change the pattern to `/(^|[/\\])\.\.[/\\]/` (drop the `$` alternative).
       This must fail the strict classifier's `..` and `foo/..` positives.
     - Backslash class: change both character classes to `[/]`.
       This must fail the strict classifier's `..\\foo` and `foo\\..` positives, and the rule classifier's POSIX `foo\\..` positive.
     - Probe fall-through: in `bash-path-resolver.ts`, make `projectExternalPaths` `continue` without calling `probeBareToken`.
       This must fail the `v1..v2` symlink test.
       Before this step, that test passed through the strict branch instead.
   - Verify: the full package suite passes, along with `pnpm run check` and `pnpm run lint`.
     `grep -c 'includes("..")' packages/pi-permission-system/src/access-intent/bash/token-classification.ts` reads 0.
   - Commit: `fix(pi-permission-system): stop reading a git revision range as a parent-directory path`, with a body that names the reporter's repro, `Refs #859`, and the co-author trailer as the final paragraph.
3. **Roadmap and architecture docs.**
   - The `docs/architecture/architecture.md` edits listed in Module-Level Changes: the module-tree wording, the `✅` on the step heading and the Mermaid node, the `Landed:` note (commit subjects plus the re-run corpus figures), and the recomputed metric.
   - Re-run the corpus spike (Invariants at risk) against the post-change commit and record the figures in the `Landed:` note.
   - Verify: `pnpm exec rumdl check packages/pi-permission-system/docs/architecture/architecture.md`.
   - Commit: `docs(pi-permission-system): mark #859 landed in the Phase 15 roadmap`.

## Risks and Mitigations

| Risk                                                                | Mitigation                                                                                                                                                                                                              |
| ------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A real parent traversal stops being flagged                         | The predicate matches exactly the segments `path.resolve` treats as traversal, and the corpus diff lost no whole-segment token (measured). Unit positives cover every edge class, and each class has a killing mutation |
| A real file named with an in-segment `..` escapes via symlink       | It falls through to the [#645] probe under a known base. The new `v1..v2` symlink test pins it, and the probe fall-through mutation proves it                                                                           |
| Brace expansion (`{..,y}/z`) loses its incidental unknown-base flag | Accepted by operator decision. It is already missed with a known base (measured), and it is tracked as [#968], deferred beside [#822]                                                                                   |
| Win32 / Git Bash backslash traversal (`..\foo`) regresses           | The backslash stays in the separator class for both classifiers regardless of flavor, and the backslash mutation pins it                                                                                                |
| The corpus measures one author's log under one policy               | Recorded as such. The claim that carries weight is structural: a token with no whole `..` segment cannot traverse lexically. The corpus confirms it on real data and does not replace it                                |

## Open Questions

- None blocking.
  Whether the rule classifier's `..` branch, which after this change only a POSIX backslash form reaches, should be deleted in favor of the #520 posture (a POSIX `\` is a filename character) can be decided if a later step reworks that classifier.
  Keeping it here preserves a single definition of "parent traversal" across both gates.

[#393]: https://github.com/gotgenes/pi-packages/issues/393
[#645]: https://github.com/gotgenes/pi-packages/issues/645
[#822]: https://github.com/gotgenes/pi-packages/issues/822
[#863]: https://github.com/gotgenes/pi-packages/issues/863
[#880]: https://github.com/gotgenes/pi-packages/issues/880
[#881]: https://github.com/gotgenes/pi-packages/issues/881
[#882]: https://github.com/gotgenes/pi-packages/issues/882
[#945]: https://github.com/gotgenes/pi-packages/issues/945
[#968]: https://github.com/gotgenes/pi-packages/issues/968
