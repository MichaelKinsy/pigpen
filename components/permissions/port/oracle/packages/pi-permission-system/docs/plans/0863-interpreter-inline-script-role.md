---
issue: 863
issue_title: "pi-permission-system: a `node -e` script whose first line is a `//` comment is classified as an external_directory path, raising false asks"
---

# An interpreter's inline script is a script, not a path operand

## Release Recommendation

**Release:** ship independently

This issue is a Phase 15 step in the package's architecture roadmap, tagged `Release: independent` there.
It belongs to no release batch — the `declared-effects` batch is [#880] and [#881] — and this step's relief is immediate and unconditional the moment it lands.
The one behavior commit is `fix:`, so the release is a patch.

**Re-planned 2026-09-20.**
This plan was first written on 2026-09-06 as two changes, shelved when [#892]'s sandbox record was re-sequenced ahead of the phase, and reopened on 2026-09-18 when that re-sequencing was revised.
Since then its **Change A** has shipped as [#945] — in a *wider* form than this plan specified, covering every argument node of both walkers rather than only the consumed-flag branch.
So the old TDD item 2 is gone, and the plan is now single-change.
Every number below was re-measured at `c8d96d30` against a 7937-command corpus; the 2026-09-06 figures (5918 commands, at `1f5b983c`) are superseded and are not carried forward.

## Problem Statement

`node -e "<script>"` hands the token collector a program text in a flag's argument slot.
Nothing records that the token is a script, so `BashPathResolver` re-judges it by shape, and a script whose first line is a `//` comment starts with `/` — the leading-`/` branch of `classifyTokenAsPathCandidate`.
The whole program becomes one `external_directory` candidate and raises an ask for a "path" that is the script's own text.

Verified at `c8d96d30` by running the shipped `collectPathCandidateTokens` and both classifiers over the issue's own command:

```text
node -e "
// check which packages are installed
const fs = require('fs');
for (const pkg of ['pkg-a','pkg-b']) { … }
"

collected token: "// check which packages are installedconst fs = require('fs');for (const pkg of ['pkg-a','pkg-b']) {"
  classifyTokenAsPathCandidate  -> ACCEPTED    (external_directory — the reported ask)
  classifyTokenAsRuleCandidate  -> ACCEPTED    (path — the issue does not mention this)
```

Two facts the issue body does not carry, both established at planning time.

The token also reaches the broader **`path`** surface, because it contains `/`.
So this is not a `//`-shaped defect of the strict classifier: `python3 -c "# c\nprint(1)"` and `ruby -e '# x'` reach `path` with no `//` anywhere, and a fix confined to `classifyTokenAsPathCandidate` would leave most of the family standing.
Measured, the broad surface carries the bulk of it: 219 interpreter command nodes contribute a non-path-shaped `path` candidate against 20 contributing an `external_directory` one.

The newline collapse the issue observed ("line breaks collapsed by the token resolution") is a property of `tree-sitter-bash`, not of the gate.
A multi-line **double-quoted** string parses as one `string` node holding one `string_content` child per line, and `resolveNodeText` concatenates the children, so the newlines are dropped; a single-quoted `raw_string` keeps them.
This matters because it rules out the obvious alternative lever — "a token containing a newline is not a path" never fires on the reported command.

### Who reported it, and what changed under them

The issue is third-party (`kuoruan`), reopened by the operator on 2026-09-18 after a 2026-09-07 `NOT_PLANNED` close.
The reopening comment settles the direction and states that the committed plan stands; the gate this session ran therefore covered only what the intervening eleven days changed — [#945]'s landing, and a defect found while re-measuring (below).

## Goals

- `node -e`, `node --eval`, `node -p`, `node --print`, `bun -e`, `bun --eval`, `bun -p`, `bun --print`, `python -c`, `python3 -c`, `perl -e`, `perl -E`, and `ruby -e` hand their inline script to the collector as a **script**, so it reaches neither the `path` nor the `external_directory` surface.
- A script *file* operand keeps its operand role: `node build.js /tmp/x` still projects both tokens.
- ADR 0009's `PATTERN_FIRST_COMMANDS` bound is amended to admit this class, since the record as written forbids adding a command the table does not name.
- The change is **not** breaking: measured over 7937 real commands it removes 205 accepted `path` candidates and 17 accepted `external_directory` candidates, adds **zero**, and loses no token that names a real file.

## Non-Goals

- **Flooring an interpreter's inline script to `ask`.**
  Once the script text is no longer projected, `node -e "…"` is an opaque payload in the same sense as `bash -c "…"`, and only shells are in `WrapperKind: "opaque-payload"`.
  Filed as [#886] and deferred to a later phase by the roadmap's sweep list; recorded as an accepted residual in the ADR 0009 amendment.
  Nothing is lost relative to today: `node -e 'require("/etc/passwd")'` yields the single token `require("/etc/passwd")`, which is not an `external_directory` candidate at all and does not match `path: {"/etc/*": …}` — only the universal fallback ever saw it.
- **The quoted `--flag='value'` spelling** — filed as [#957] and adopted as its own Phase 15 step, after [#859] and ahead of [#609].
  Measured at `c8d96d30`, `node --eval='// x'` still projects `// x` after this change, because the flag branch is guarded on `child.type === "word"` and a quoted value makes the argument a `concatenation`.
  This is **not** interpreter-specific and not introduced here: `grep --regexp='/etc/passwd' f.txt` and `sed --expression='s/a/b/' f.txt` leak the same way at HEAD today.
  ADR 0009 § "What the projection deliberately omits" already names the mechanism (`rg -g'!docs'`) and declines widening flag detection to quoted tokens, so closing it is an amendment question rather than a ten-line edit — see [#957]'s first comment for why the declination's stated cost prices the naive lever and not the narrow one.
  Corpus population: **2 of 7937 commands**, both emitted by this issue's own planning spikes.
- **Short-flag clusters whose script flag is not first** — `perl -i -pe 's{…}'`, `python3 -uc`, `node -pe`.
  `classifyPatternCommandFlag` reads only `text.slice(0, 2)`, so these fall through to `regular-flag` and the script stays a positional.
  Closing them needs per-character scanning with two different rules, because `node -pe X` is `-p -e X` while `perl -ne X` and `python3 -uc X` are getopt (the first argument-taking char eats the rest of the token).
  Listing `perl -p` as *clustered*-consuming would be over-listing, which ADR 0009 says drops a real operand — the unrecoverable direction.
  Measured: after the change, 9 of the corpus's 188 `perl` command nodes still contribute a non-path-shaped `path` candidate, all of them `-pe` / `-0777 -pe` / `-i -pe`.
  Recorded as an ADR 0009 residual beside the existing `grep -ie pattern` one it matches exactly.
- **`deno eval`** — `eval` is a subcommand, not a flag, and `PATTERN_FIRST_COMMANDS` keys on a command basename with no subcommand vocabulary.
  Expressing it needs the recursive `subcommands` shape [#880] creates.
- **`..` as a whole segment** ([#859]) — the sibling false positive, its own Phase 15 step.
- **A `TokenRole` on `PathToken`** — [#609] owns it, and its plan may absorb these rows' `script` role into that vocabulary.
  Nothing here should anticipate its shape.
- **The `COMMAND_PREFIX_TYPES` re-spelling tidy and `bash-path-extractor.test.ts`'s duplication of `program.test.ts`** — both are [#609]'s own recorded tidy-first prep.
- **A permanent measurement instrument.**
  Operator decision at the planning gate: the repo's `scripts/measure-*.mjs` cannot import TypeScript and therefore *transcribe* the rules they measure, which here would mean a copy of six `PatternCommandConfig`s plus both classifiers — the same silent rot ADR 0009 complains about in its own table.
  The numbers below come from a disposable vitest spike over the real modules; its source is inlined in Test Impact Analysis so a later reader can re-run it rather than argue with the figures.

## Background

### Where the role is lost

`collectCommandTokens` (`packages/pi-permission-system/src/access-intent/bash/token-collection.ts`) dispatches on the command basename:

```typescript
const config = commandName ? PATTERN_FIRST_COMMANDS.get(commandName) : undefined;
if (config) return collectPatternCommandTokens(node, config, effect);
return [
  ...collectGenericCommandTokens(node, effect),
  ...collectEmbeddedOptionValues(node, effect),
];
```

`node`, `bun`, `python`, `python3`, `perl`, and `ruby` are absent from the map, so their arguments go down the generic path, where every `ARG_NODE_TYPES` child is emitted as a token regardless of the flag in front of it.

The vocabulary the fix needs already exists.
`PatternFlagRole` has a `script` value — "Supplies the pattern/script inline (`grep -e`, `sed --expression`)" — whose consumption discharges as `{ consumed: true }` with no token, and `PatternCommandConfig.patternPositionals` is read as `config.patternPositionals ?? 1`, so a literal `0` is honored.
These commands are simply not in the table.

### What [#945] already did

[#945] landed on 2026-09-19 as `3da8a63b` and `9eeece05`.
`collectPatternCommandTokens` now calls `collectHostedExecutionTokens(child)` for **every** `ARG_NODE_TYPES` child, above the consumption branch, and `collectGenericCommandTokens` does the same.
So the old Change A is not merely done, it is done more widely than this plan specified, and the case this plan used to reach through it already passes at HEAD:

```text
node -e "$(cat /etc/shadow)"   ->  tokens: ["/etc/shadow"]     (verified at c8d96d30)
```

Nothing in this plan's remaining work depends on collector logic; it is table rows.

### The bound ADR 0009 draws around that table

`docs/decisions/0009-bash-path-projection-completeness-contract.md` § "Where the bound sits" names three in-scope edits (a further spelling of a listed flag, a split when one spelling has different arity, a role correction) and then forbids this one outright:

> Adding a **flag** the table does not name, or a **command** it does not name, is the per-command option table rejected below and needs its own decision.
> There is no pressure to: the direction-of-failure rule makes an omission over-surface, so an unlisted flag costs a prompt, never an operand.

Issue #863 is the counter-evidence to the second sentence: an over-surface expensive enough to be filed as a bug by a third party.
So the change needs an amendment, not just a table edit — this is the "own decision" the record asks for.

The amendment's rule for what a row may assert stands unchanged and governs every new row:

> **Under**-listing a consuming flag over-surfaces; **over**-listing drops an operand.
> So a flag is listed as consuming only when it consumes on every supported platform **and in every command that shares the entry**, verified against each tool's parser rather than against a shared spelling.

### Measured blast radius

Instrument: a disposable vitest spike (source in Test Impact Analysis) importing the real `collectPathCandidateTokens`, `classifyTokenAsPathCandidate`, and `classifyTokenAsRuleCandidate`, run over every distinct `toolName: "bash"` command in the local review log with the width-capped ones dropped — 7937 commands, 2026-09-20.
The interpreter rows were applied as a spike and the accepted-token set diffed against the same corpus at `c8d96d30`.

| Measurement (measured)                                         | Before | After                                            |
| -------------------------------------------------------------- | ------ | ------------------------------------------------ |
| Commands with a non-path-shaped `external_directory` candidate | 93     | 77                                               |
| Commands with a non-path-shaped `path` candidate               | 514    | 332                                              |
| Interpreter **command nodes** in the corpus                    | 916    | 916                                              |
| …of those, contributing a non-path `external_directory` token  | 20     | 3                                                |
| …of those, contributing a non-path `path` token                | 219    | 18                                               |
| Commands whose token set changes at all                        | —      | 187                                              |
| Accepted `external_directory` tokens lost / gained             | —      | 17 / **0**                                       |
| Accepted `path` tokens lost / gained                           | —      | 205 / **0**                                      |
| …of the 205 lost, path-shaped                                  | —      | 3 (all `perl s/…/…/` scripts; none names a file) |

No real path is lost anywhere in the corpus.
The three "path-shaped" losses are perl substitution expressions whose `/` and `|` delimiters give them separators.

The 18 residual nodes split evenly and neither half is this change's business: 9 are `perl` cluster spellings (Non-Goals), and 9 are a script **file** handed a shell-string argument (`node ast-spike.mjs 'cat a'`), where the argument really is a positional and projecting it is correct.

Corpus usage of the commands being added, counted as parsed command nodes: `python3` 477, `node` 241, `perl` 188, `bun` 5, `ruby` 3, `python` 2.

### External facts, verified by execution

Re-run on this host (macOS, 2026-09-20) against today's binaries, because a `man` page answers whether a flag exists and not what the binary does with it:

| Command          | Flags listed                    | Evidence                                                                                                                                                                                                                                                         |
| ---------------- | ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `node` v26.9.0   | `-e`, `--eval`, `-p`, `--print` | `node -e 'console.log("E-OK")'`, `node --eval …`, `node -p '1+1'` → `2`, `node --print '2+2'` → `4`, `node --eval='…'`; `node -p t.js` evaluates `t.js` as *source* (`[eval]:1 t.js ^`) rather than running the file, so a following argument is always consumed |
| `bun` 1.4.2      | `-e`, `--eval`, `-p`, `--print` | `bun -e`, `bun --eval`, `bun -p '1+1'` → `2`, `bun --print '3+3'` → `6`; `bun -p` with no value errors `The argument '-p' requires a value but none was supplied.`, so it consumes unconditionally                                                               |
| `python3` 3.14.7 | `-c`                            | `python3 -c 'print("PC-OK")'`; `python3 -cu 'print("x")'` raises from `File "<string>", line 1`, evaluating the glued `u` as the script and confirming the getopt semantics the existing glued rule models                                                       |
| `perl` 5.34.1    | `-e`, `-E`                      | `perl -e 'print "PE-OK\n"'`, `perl -E 'say "PE2-OK"'`; `perl -e` with nothing after it errors `No code specified for -e.`                                                                                                                                        |
| `ruby` 4.0.7     | `-e`                            | `ruby -e 'puts "RE-OK"'`.  `-E` is deliberately **not** listed: on `ruby` it is `--encoding`, and `ruby -E utf-8 -e 'puts "RE2-OK"'` runs, proving `-E` consumed `utf-8` and not the script                                                                      |

`python` could not be run — no such binary on this host (`command not found`).
It shares `python3`'s row on the ground that every implementation the name reaches is a CPython-compatible front end where `-c` takes the following argument (CPython 2, CPython 3, PyPy).
If the implementing host has a `python`, verify it by execution and record the result; if it does not, the row ships on that basis and the ADR amendment says so.

### Constraints from AGENTS.md and the package skill

- The package skill's closing note applies directly: "When a plan or test asserts a specific bash repro string, trace the token through the classifier first."
  Done above, for both surfaces, and it is what caught the quoted `--eval=` gap the earlier draft of this plan asserted away.
- `PATTERN_FIRST_COMMANDS` names share a configuration object "only when they share a *parser*, which is narrower than being aliases".
  `node` and `bun` assert the same four flags but are different binaries with different parsers, so each gets its own object; `python` and `python3` share one, being the same interpreter family.
- Health-metric rows that grep for a name the phase has not created must be updated in the commit that creates it, or the phase-close verification silently breaks.
  The interpreter row's recompute command names five interpreters and not `bun`, so it is edited in the same commit.
- The roadmap step's `✅` mark, its Mermaid node, and its `Landed:` note belong to the implementation doc-update commit, not to `/ship`.

## Design Overview

One change: table rows.
No collector logic moves, and no new mechanism is introduced.

### The interpreter rows

One `PatternCommandConfig` per parser, all with `patternPositionals: 0`:

```typescript
const NODE_CONFIG: PatternCommandConfig = {
  flags: new Map<string, PatternFlagRole>([
    ["-e", "script"],
    ["--eval", "script"],
    ["-p", "script"],
    ["--print", "script"],
  ]),
  patternPositionals: 0,
};
```

`bun` carries an identical map in its own object, `python`/`python3` share `[["-c", "script"]]`, `perl` carries `-e`/`-E`, and `ruby` carries `-e` alone.
`PATTERN_FIRST_COMMANDS` gains six entries: `node`, `bun`, `python`, `python3`, `perl`, `ruby`.

`patternPositionals: 0` is what keeps a script **file** an operand.
The existing rows all skip a leading positional because `grep PATTERN file` really does put a pattern first; an interpreter does not — `node build.js /tmp/x` names two paths and no inline script, and the script only ever arrives through a flag.
`config.patternPositionals ?? 1` already honors a literal `0`, so no code change is needed to support it.

`hasExplicitScript` becomes inert under a zero budget (`positionalsSeen < 0` is never true), which is correct: with no positional to protect, a `script` flag's only job is to swallow its own argument.

Three spellings are covered by machinery already present, and each gets a test rather than a code path.
All three were **verified by running the spike**, not inferred from the branch structure:

| Spelling             | Path through the walker                                                                            | Measured tokens                               |
| -------------------- | -------------------------------------------------------------------------------------------------- | --------------------------------------------- |
| `node -e "// x"`     | `consume-next`, discharged `{ consumed: true }`, no token                                          | `[]`                                          |
| `node --eval=//x`    | `word` → `inline-value`, which pushes only for `script-file`                                       | `[]`                                          |
| `node -e"// x"`      | `concatenation` → positional → token `-e// x`, rejected by `rejectNonPathToken`'s leading-`-` test | `["-e// x"]`, neither classifier accepts      |
| `node --eval='// x'` | `concatenation` → positional → `embeddedOptionValueToken` split                                    | `["--eval=// x", "// x"]` — **leaks**, [#957] |

The fourth row is the correction to this plan's earlier draft, which asserted the `=`-embedded spelling was covered free.
It is: for the bare-`word` form only.
See Non-Goals.

### What the rows deliberately do not claim

`ruby -E` is an encoding flag; `python -m` names a module; `node --input-type=module` is an unrecognized flag whose `=`-embedded value keeps flowing through the blind split.
Each stays unlisted, so its value over-surfaces as a bare token that names nothing and the existence probe discards — ADR 0009's recoverable direction.
Measured at `c8d96d30` with the rows applied:

```text
ruby -E utf-8 -e 'code'              ->  ["utf-8"]     (script suppressed, encoding over-surfaced)
node --input-type=module -e "// x"   ->  ["module"]    (same)
```

### What a consumer sees

Nothing downstream changes shape.
`collectPatternCommandTokens` returns `PathToken[]` as before; the interpreter's script simply is not among them, so `BashPathResolver` never classifies it and neither `projectExternalPaths` nor `projectRuleCandidates` sees it.
The command enumerator is untouched, so `bash:` rules govern `node -e "…"` exactly as they do today, and `node -e "$(rm -rf ~/x)"` still enumerates `rm -rf ~/x` as its own unit.

## Module-Level Changes

### Production

- `src/access-intent/bash/token-collection.ts`
  - New `NODE_CONFIG`, `BUN_CONFIG`, `PYTHON_CONFIG`, `PERL_CONFIG`, `RUBY_CONFIG` constants in the config-constant region (currently `GREP_FLAGS` through `SD_CONFIG`), each with a doc comment recording the execution evidence for its rows and, for `RUBY_CONFIG`, why `-E` is absent.
  - `PATTERN_FIRST_COMMANDS` gains `node`, `bun`, `python`, `python3`, `perl`, `ruby`.
  - The map's own doc comment and the module docstring's `PATTERN_FIRST_COMMANDS` sentence gain the interpreter class, since the map is no longer only about pattern-first *matching* tools.

No other `src/` file changes.

### Predicted unchanged, with the claim each rests on

- `src/access-intent/bash/token-classification.ts` — the token never reaches it; the change is upstream of classification entirely.
- `src/access-intent/bash/bash-path-resolver.ts` — same, one layer further down.
- `src/access-intent/bash/command-enumeration.ts` and `wrapper-analysis.ts` — the enumerator is a separate walker over the same tree and reads no `PathToken`.
- `.pi/skills/package-pi-permission-system/SKILL.md` — this plan's earlier draft named a "role-aware for a pattern-first one" sentence here.
  It does not exist: `grep -rn 'PATTERN_FIRST\|pattern-first\|role-aware' .pi/skills/` matches nothing in the whole skills tree.
- `test/handlers/gates/bash-path-extractor.test.ts` — deliberately not extended; its duplication of `program.test.ts` is [#609]'s recorded tidy, and adding a third copy of this class is what that tidy exists to stop.
- `docs/architecture/architecture.md` line 1074 (`An interpreter's inline script … is projected ([#863])`) — a **Findings** sentence, a fixed snapshot of the state at phase open, in the same section as the dated corpus figures beside it.
  The step's `✅` mark and `Landed:` note are where completion is recorded; the findings narrative is not rewritten as steps land.

### Tests

- `test/access-intent/bash/token-collection.test.ts` — a new `describe` sibling inside the existing pattern-first block, tagged `#863`, matching the file's issue-tagged-sibling convention (`#823`, `#945`, …).
  Anchor it on the enclosing block's closing line and verify with `grep -n '^describe\|^});'`, per AGENTS.md.
- `test/access-intent/bash/program.test.ts` — a top-level `#863` block, a sibling of the existing cross-cutting per-issue blocks (`workdir seed (#574)`, `effect attribution (#807)`, `path operands of a command the parse dropped (#875)`), carrying the issue's literal repro end to end through `BashProgram` and asserting on both `externalAccesses()` and `pathRuleCandidates()`.

### Documentation

- `docs/decisions/0009-bash-path-projection-completeness-contract.md` — a dated amendment, `amended:` frontmatter bumped from `2026-09-15`, and the Status line's "as amended" date.
  It widens § "Where the bound sits" to admit a fourth in-scope edit — a command whose **inline script** the table can identify by flag role — and records why the third sentence of the old bound ("an omission costs a prompt, never an operand") is no longer a sufficient argument against adding one.
  It extends two existing residual bullets rather than adding new ones:
  - **A pattern-first flag spelling the table does not name** gains the cluster instance for interpreters (`perl -pe`, measured 9 of 188 `perl` nodes), beside the `grep -ie pattern` one it matches, and a pointer to [#957] for the quoted `--flag='value'` half, whose declination sentence that bullet already carries.
  - A new bullet for the interpreter payload's opacity, pointing at [#886] — the residual this change *creates*, and the one it must name, since the script text is now invisible to the path surfaces entirely.
- `docs/architecture/architecture.md`
  - The `token-collection.ts` module-tree entry: the `PATTERN_FIRST_COMMANDS` sentence gains the interpreter class and the per-parser split for `node`/`bun`.
    This is an active constraint, so it belongs in the tree under the repo's citation rule.
  - This step's `#### [#863]` heading gains `✅`, its `S863` Mermaid node label gains `✅`, and a `Landed:` bullet is added.
  - The step's `Target:` bullet gains `bun`, which it does not currently list.
  - The step's `Outcome:` bullet is corrected.
    It currently reads "the measurement script's monthly non-path count reads 0 for the first full month after landing".
    Measured at planning time, `measure-path-false-positives.mjs` reports `2026-05 12 / 2026-06 6 / 2026-07 4 / 2026-08 6` and has **no 2026-09 row at all** — no bash `external_directory` ask has fired this month, so that metric cannot demonstrate this fix either way.
    Replace it with the collector-level figure this plan measured (interpreter command nodes contributing a non-path `path` candidate: 219 → 18, the remainder being the two recorded residuals).
  - Health metrics: "Interpreter script-role commands in `token-collection.ts`" moves off its `0` baseline, and its recompute command gains `bun` — `grep -cE '"(node|bun|python|python3|perl|ruby)"'`.
    Run the edited command and write what it printed.
- `docs/opencode-compatibility.md` line 120 — "understands flag arity for `sed`, `awk`, `grep`, `rg`, and similar tools" gains the interpreter inline-script case, since a reader comparing coverage would otherwise miss it.

### Greps performed at planning time

- `PATTERN_FIRST_COMMANDS` across `packages/pi-permission-system/{src,test,docs}` and `packages/pi-permission-system/README.md`, excluding `docs/plans/`, `docs/retro/`, and `docs/architecture/history/` as historical records — production: `token-collection.ts`, `token-classification.ts` (a docstring reference, no edit needed).
  Tests: `token-collection.test.ts` (a comment), `bash-path-extractor.test.ts` (a comment) — neither needs an edit.
  Live docs: `architecture.md`, `docs/decisions/0009`, `docs/decisions/0013` (a rejected-alternative mention, no edit), `docs/opencode-compatibility.md`.
  The README does not mention it.
- `PATTERN_FIRST|pattern-first|role-aware` across the whole `.pi/skills/` tree — **no matches**, which is what retires this plan's earlier SKILL.md touch point.
- No export is added, removed, or renamed — the five new constants and the six map entries are all module-private — so no importer sweep is required and no `#src/` alias grep applies.

## Test Impact Analysis

### New tests the change enables

The interpreter class becomes assertable at the collector, where today it is only observable as a projected path three layers downstream.

### Existing tests that must stay as-is

Every `sed`/`awk`/`grep`/`rg`/`sd` case in `token-collection.test.ts` and `program.test.ts`.
The whole point of the per-parser configs is that the existing rows are untouched; the spike run confirmed all **4540** tests in the package stay green with the interpreter rows applied.

### Tests that become redundant

None.
This change adds table rows; it removes no behavior an existing test covers.

### The spike that produced the numbers

Operator decision: no permanent instrument (see Non-Goals).
The figures in Background come from this file, written to `packages/pi-permission-system/test/spike-863.test.ts`, run with `pnpm --filter @gotgenes/pi-permission-system exec vitest run test/spike-863.test.ts`, and deleted afterwards.
Re-create it to falsify the table rather than arguing with it.

````typescript
import { readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { describe, it } from "vitest";
import { getParser } from "#src/access-intent/bash/parser";
import {
  classifyTokenAsPathCandidate,
  classifyTokenAsRuleCandidate,
} from "#src/access-intent/bash/token-classification";
import { collectPathCandidateTokens } from "#src/access-intent/bash/token-collection";
import { posixPathFlavor } from "#src/path/path-flavor";

const LOG = join(
  homedir(),
  ".pi/agent/extensions/pi-permission-system/logs",
  "pi-permission-system-permission-review.jsonl",
);

function commands(): string[] {
  const seen = new Set<string>();
  for (const line of readFileSync(LOG, "utf8").split("\n")) {
    if (!line.trim()) continue;
    let entry: Record<string, unknown>;
    try {
      entry = JSON.parse(line) as Record<string, unknown>;
    } catch {
      continue;
    }
    if (entry.toolName !== "bash") continue;
    const cmd = typeof entry.command === "string" ? entry.command : undefined;
    // A command over `reviewLogFieldMaxWidth` is stored with a trailing `…`
    // and re-parses as garbage; excluding it is what the other instruments do.
    if (!cmd || cmd.endsWith("…")) continue;
    seen.add(cmd);
  }
  return [...seen];
}

describe("spike 863", () => {
  it("measures", async () => {
    const parser = await getParser();
    const out: Record<string, { strict: string[]; broad: string[] }> = {};
    for (const cmd of commands()) {
      const tree = parser.parse(cmd);
      if (!tree) continue;
      try {
        const tokens = collectPathCandidateTokens(tree.rootNode).map(
          (t) => t.token,
        );
        out[cmd] = {
          strict: tokens.filter((t) => classifyTokenAsPathCandidate(t) !== null),
          broad: tokens.filter(
            (t) => classifyTokenAsRuleCandidate(t, posixPathFlavor) !== null,
          ),
        };
      } finally {
        tree.delete();
      }
    }
    writeFileSync(
      process.env.SPIKE_OUT ?? "/tmp/spike-863.json",
      JSON.stringify(out),
    );
  }, 600_000);
});
````

Run it once at `HEAD`, once with the rows applied, and diff the two JSON files.
A token is "non-path-shaped" under the same predicate `measure-path-false-positives.mjs` uses:

```javascript
const NON_PATH_SHAPE = /\n|;|\s|^\/\/[^/]|[()"`{}]|\\\|/;
```

The per-node figures (916 interpreter command nodes, 219 → 18) come from the same spike with `collectPathCandidateTokens(tree.rootNode)` replaced by a walk that calls `collectCommandTokens(n)` on each `command` node whose `command_name` basenames to an interpreter.

### The parser's input domain, not the inputs I pictured

The scenario list the TDD steps assert was run as a table against the real parser before being written into the plan, because two of its entries behaved differently from the branch-reading prediction (the two `--eval=` spellings).
Run the whole table, not one example per class: the `-e"// x"` and `--eval='// x'` rows differ only in quoting and land on opposite sides.

## Invariants at risk

| Invariant                                                                                                        | Where it is documented                                      | What pins it                                                                                                                      |
| ---------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| A pattern-first command's real file operand is never eaten by a mis-listed flag                                  | ADR 0009 amendment 2026-08-29; [#823]                       | The existing `#823` blocks (`token-collection.test.ts`), plus this change's `node build.js /tmp/x` and `ruby -E utf-8 -e …` cases |
| Positional invariance — a nested command's operands are projected wherever the substitution sits                 | ADR 0009 § "What the projection guarantees"; [#741], [#945] | The `#945` blocks in `token-collection.test.ts` and `program.test.ts`; this change adds `node -e "$(cat /etc/shadow)"` on top     |
| A nested execution's tokens keep their own command's effect attribution, not the enclosing command's             | `token-collection.ts` docstring; [#807], [#945]             | [#945]'s attribution case, which the interpreter rows must not change; assert the projected `/etc/shadow` carries `cat`'s proof   |
| A redirect destination is collected independently of the command's own arguments                                 | [#741]                                                      | `node -e "x" > /tmp/out.txt` still projects `/tmp/out.txt` (verified at `c8d96d30` with the rows applied)                         |
| The command enumerator is unaffected — `bash:` rules still govern the interpreter invocation and any nested unit | ADR 0013 §10                                                | An assertion that `node -e "$(cat /etc/shadow)"` still enumerates two units                                                       |

The quantitative invariant is the corpus diff in Background: 205 `path` tokens and 17 `external_directory` tokens lost, 0 gained, 0 real paths, over 7937 commands.
Re-run the spike after implementation rather than citing this table — it is a measurement at `c8d96d30`, scoped to that commit and to one author's log.

A note on the third row, which is the one at genuine risk: [#945] is eleven days old and its attribution assertion lives in a block this change does not touch, but the interpreter rows change *which walker* runs for six command names.
`node -e "$(cat /etc/shadow)"` goes from the generic walker to the pattern-first one, and both call `collectHostedExecutionTokens` — the test asserting the effect stays `cat`'s must be run against the interpreter spelling, not only `sed`'s.

An invariant this plan explicitly does **not** claim: that no input of the leaking shape exists.
The quoted `--flag='value'` residual is bounded at 2 occurrences in one author's corpus, which bounds observed frequency and not reachability — which is why it is filed ([#957]) rather than argued away.

## TDD Order

1. **Red → green: an interpreter's inline script is not a path candidate.**

   Test surfaces: a new `#863` `describe` in `test/access-intent/bash/token-collection.test.ts`, and a `#863` block in `test/access-intent/bash/program.test.ts` through `BashProgram`.
   Covers, as equivalence classes:
   - *Script suppressed*: the issue's literal `node -e "…"` repro, `node --eval "// x"`, `node --eval=//x`, `node -p '1+1'`, `bun -e`, `bun --print`, `python3 -c "# c\nprint(1)"`, `python -c '# c'`, `perl -e '// x'`, `perl -E`, `ruby -e '# x'` — no token from the collector, and at the facade no external access and no rule candidate.
   - *Operand preserved*: `node build.js /tmp/x` projects both; `python3 script.py /tmp/x` projects both.
   - *Unlisted flag still over-surfaces*: `ruby -E utf-8 -e 'code'` projects `utf-8` and not the script; `node --input-type=module -e "// x"` projects `module` and not the script.
   - *Adjacent surfaces intact*: `node -e "x" > /tmp/out.txt` still projects `/tmp/out.txt`; `node -e "$(cat /etc/shadow)"` still projects `/etc/shadow` **carrying `cat`'s `read`/`core` attribution**, not the enclosing command's; the same command still enumerates two units.
   - *Known residual, pinned as such*: `node --eval='// x'` **does** project `// x`, and `perl -pe 's|a|b|' f.txt` **does** project the script.
     Assert the current behavior with a comment naming [#957] and the cluster residual respectively, so the day either is closed the test fails and points at its issue rather than silently over-asserting today's gap as intended.

   Killing mutations, one per class:
   - Delete the `["node", NODE_CONFIG]` entry from `PATTERN_FIRST_COMMANDS` → every *script suppressed* `node` case goes red; the `python3` / `perl` / `ruby` / `bun` cases stay green.
   - Remove `patternPositionals: 0` from `NODE_CONFIG` → `node build.js /tmp/x` drops `build.js` and goes red; the *script suppressed* cases stay green.
   - Add `["-E", "script"]` to `RUBY_CONFIG` → `ruby -E utf-8 -e 'code'` loses `utf-8` and goes red.
   - Extend the `inline-value` branch's push condition from `script-file` to include `script` → `node --eval=//x` goes red (it gains a `//x` token) while the spaced `node --eval "// x"` stays green — the mutation that separates the two spellings, and the reason the *unquoted* form is the one asserted here.
   - Re-stamp the hosted tokens in `collectPatternCommandTokens` with the enclosing `effect` → only the `node -e "$(cat /etc/shadow)"` attribution assertion goes red, which is what proves that case is testing [#945]'s invariant through the new walker and not just token presence.

   Commit: `fix(pi-permission-system): stop projecting an interpreter's inline script as a path`.

2. **Amend ADR 0009.**

   Widen § "Where the bound sits" to a fourth in-scope edit; record why the old bound's "an omission costs a prompt, never an operand" no longer settles the question; extend the *pattern-first flag spelling the table does not name* bullet with the interpreter cluster instance and the [#957] pointer; add the interpreter-payload-opacity residual citing [#886].
   Bump `amended:` and the Status line's "as amended" date.
   Verified by `pnpm exec rumdl check` and by re-reading the amendment against the rows actually shipped — every flag the amendment claims must be one the code lists, and vice versa.
   Commit: `docs(pi-permission-system): amend ADR 0009 to admit an interpreter's inline script`.

3. **Land the roadmap and reference-doc updates.**

   This step's `✅` heading mark, its `S863` Mermaid node, its `Landed:` bullet, the `bun` addition to its `Target:`, the corrected `Outcome:`, the interpreter health-metric row and its `bun`-inclusive recompute command, the `token-collection.ts` module-tree entry, and `docs/opencode-compatibility.md` line 120.
   Verified by running each edited recompute command and writing what it printed, and by `pnpm run lint`.
   Commit: `docs(pi-permission-system): record the interpreter script role in the roadmap and module tree`.

**Tidy First:** a fresh-context `tidy-first-assessor` read the three target files against this design and recommended **nothing** — the config-constant region is a highly uniform run of one literal per parser, and both test files already carry the issue-tagged-sibling shape this change needs.
It declined a shared `scriptFlagsConfig(...)` factory for `NODE_CONFIG`/`BUN_CONFIG` as the wrong abstraction: the two maps are byte-identical *by coincidence of spelling*, and the separate objects are the file's way of recording that they are separate parsers.
Do not introduce one during implementation.

## Risks and Mitigations

| Risk                                                                                                                        | Mitigation                                                                                                                                                                                                                                  |
| --------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A listed flag does not consume on some host, so the walker eats a real operand — the unrecoverable direction ADR 0009 names | Every row except `python` was verified by running the binary on 2026-09-20, with the transcript in Background. `python` shares `python3`'s row on a stated basis, and the implementing session re-verifies by execution if its host has one |
| `patternPositionals: 0` is an untried value                                                                                 | It is read as `config.patternPositionals ?? 1`, so `0` is honored by construction; the spike ran the full 4540-test package suite green with it, and step 1's second killing mutation pins it                                               |
| The change silently drops a real path from the corpus                                                                       | Measured rather than argued: 205 `path` and 17 `external_directory` accepted tokens lost, 3 of them path-shaped and all three perl substitution scripts, 0 gained, over 7937 commands. Re-run the spike after implementation                |
| The plan's spelling table is read as exhaustive and the quoted leak ships unnoticed                                         | It is asserted as a *pinned residual* in step 1 rather than omitted, so the test suite states the gap and names [#957]                                                                                                                      |
| Landing this makes [#957] look closed because the headline case is fixed                                                    | [#957] is a Phase 15 step with its own `Outcome:`, and this plan's Non-Goals state the leaking spelling explicitly. The pinned test is the enforcement                                                                                      |
| The cluster residual is read later as a bug rather than a decision                                                          | It is recorded in ADR 0009 beside the `grep -ie pattern` residual it matches, with the measured 9-of-188 figure and the reason both a getopt scan and a `-p` listing are wrong                                                              |
| The roadmap's interpreter metric grep silently under-reports because `bun` is not in its pattern                            | The recompute command is edited in the same commit as the rows, per the roadmap's own instruction about names a phase has not yet created                                                                                                   |
| The step's stated `Outcome:` cannot be observed, and the step is marked complete on an unmeasurable claim                   | The `Outcome:` line is corrected in step 3 to the collector-level figure, because `measure-path-false-positives.mjs` has no 2026-09 row — the monthly metric reads 0 for a reason unrelated to this fix                                     |
| A future reader takes the removed token as removed protection                                                               | The ADR amendment states the concrete finding: the projected token was the whole program text, `node -e 'require("/etc/passwd")'` never was an `external_directory` candidate, and it matched no `path` rule but the universal fallback     |

## Open Questions

- Whether `python` should ship a row without an execution check on some host.
  Resolved by the implementing session if a `python` is available; this host has none (`command not found`), so absent that the row ships on the CPython-front-end basis stated in the ADR amendment, and the question is closed either way rather than left standing.
- Whether [#609] folds these rows' `script` role into its `TokenRole` vocabulary or leaves them in the flag table.
  Deliberately left to that step's plan; nothing here anticipates the shape.
- Whether [#957]'s narrow lever survives contact with ADR 0009's recorded declination.
  That is [#957]'s own question, raised in its first comment; this plan depends on the answer only for whether its pinned residual test flips.

[#609]: https://github.com/gotgenes/pi-packages/issues/609
[#741]: https://github.com/gotgenes/pi-packages/issues/741
[#807]: https://github.com/gotgenes/pi-packages/issues/807
[#823]: https://github.com/gotgenes/pi-packages/issues/823
[#859]: https://github.com/gotgenes/pi-packages/issues/859
[#880]: https://github.com/gotgenes/pi-packages/issues/880
[#881]: https://github.com/gotgenes/pi-packages/issues/881
[#886]: https://github.com/gotgenes/pi-packages/issues/886
[#892]: https://github.com/gotgenes/pi-packages/issues/892
[#945]: https://github.com/gotgenes/pi-packages/issues/945
[#957]: https://github.com/gotgenes/pi-packages/issues/957
