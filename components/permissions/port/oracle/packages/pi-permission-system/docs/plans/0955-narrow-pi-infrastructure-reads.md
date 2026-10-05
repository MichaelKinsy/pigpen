---
issue: 955
issue_title: "pi-permission-system: piInfrastructureDirs includes bare agentDir, so auth.json and mcp-oauth/ are auto-allowed reads that an explicit deny cannot stop"
---

# Narrow the Pi infrastructure read bypass and let a targeted deny override it

## Release Recommendation

**Release:** ship independently

The roadmap's open-issue sweep records this issue as out of scope for Phase 15, and it carries no `Release:` batch tag.
All three behavior commits are breaking (`fix(pi-permission-system)!:`), so the release is a major.

## Problem Statement

`computeExtensionPaths()` lists bare `agentDir` in `piInfrastructureDirs`.
`isPiInfrastructureRead` matches each entry with `flavor.isWithin`, so every file under `~/.pi/agent/` is auto-allowed for the read-only tools (`read`, `find`, `grep`, `ls`).
That includes the credential store (`auth.json`), the MCP OAuth store (`mcp-oauth/`), `models.json` (which can hold provider `apiKey`s), `trust.json`, every session transcript, crash logs, and this package's own permission review log.

The bypass also returns from `describeExternalDirectoryGate` before policy resolution.
So an `external_directory` or `external_directory_read` deny that names one of these paths never fires.

Planning corrected two claims in the issue, both measured:

- The issue says no configuration closes this.
  That is true only for the `external_directory` family.
  A `path`, `path_read`, or per-tool `read` deny on `auth.json` blocks today, because the path gate and the per-tool gate still run after the bypass.
- The issue says a file entry such as `settings.json` needs new matching.
  It doesn't: `PosixPathFlavor.isWithin` returns `true` on equality (`if (pathValue === directory) return true;` in `src/path/path-flavor.ts`), so a literal file path already matches itself.

## Goals

- **Narrow the list.**
  Replace bare `agentDir` with the entries Pi's harness reads:
  - the directories `agents/`, `extensions/`, `git/`, `npm/`, `prompts/`, `skills/`, and `themes/`;
  - the files `settings.json`, `SYSTEM.md`, `APPEND_SYSTEM.md`, and `AGENTS.md`.
- **Carve out the package's own logs directory.**
  Exclude `globalLogsDir` (`extensions/pi-permission-system/logs`) from the bypass even though `extensions/` stays.
  This makes ADR 0010's "the agent reading its own log" row true again.
- **Let a targeted deny override the bypass.**
  Resolve the `external_directory`-family policy before the bypass.
  Skip the bypass when the result is a **targeted** deny: `state === "deny"`, with a `matchedPattern` that is neither `undefined` (the universal `permission["*"]` fallback) nor `"*"` (the family catch-all).
- Classify the change as **breaking**.
  Reads that are auto-allowed today start prompting (or denying), and a deny that was silently skipped now fires.
  Each behavior commit is `fix(pi-permission-system)!:` with a `BREAKING CHANGE:` footer, and a migration guide names the recovery rules.

## Non-Goals

- Extending the bypass to `bash` ([#956]).
  This change unblocks it, because the widened surface no longer includes credentials.
  It stays its own issue.
- Canonicalizing the infrastructure list ([#1018], filed by this planning session).
  The list is compared un-canonicalized against a canonical boundary value, so a symlinked `agentDir` loses the bypass.
  It fails closed and is independent of which entries are listed.
- Yielding the bypass to a targeted `ask`.
  The operator chose deny only: a typical `external_directory: {"*": "ask"}` would otherwise erase the bypass, and an `ask` on an infra path keeps today's behavior.
- Yielding to a catch-all or universal-fallback deny.
  Measured: `{"*": "deny", "read": "allow"}` resolves `external_directory_read` to `deny` with `matchedPattern: undefined`, and `external_directory: "deny"` / `{"*": "deny"}` resolve with `matchedPattern: "*"`.
  Those configs keep their skill and package reads.
- Any change to redaction or to what the review log persists (ADR 0010's redaction boundary).
- `globalNodeModulesRoot` and `piPackageDir` stay as they are.
  Both are package installation trees.
- Changing `piInfrastructureReadPaths` semantics, except that the logs exclusion also wins over a user entry covering it.

## Background

- `src/config/extension-paths.ts` — `computeExtensionPaths(agentDir, piPackageDir?)` builds the immutable `ExtensionPaths`, including `piInfrastructureDirs` and `globalLogsDir` (`getGlobalLogsDir(agentDir)` = `<agentDir>/extensions/pi-permission-system/logs`).
- `src/path/pi-infrastructure-read.ts` — `isPiInfrastructureRead(toolName, normalizedPath, infrastructureDirs, cwd, flavor)` gates on `READ_ONLY_PATH_BEARING_TOOLS`.
  It then matches glob entries with `wildcardMatch` and plain entries with `flavor.isWithin(path, expandHomePath(dir))`, plus the project-local `.pi/{npm,git}`.
- `src/path/path-normalizer.ts` — `PathNormalizer.isInfrastructureRead(toolName, accessPath, infraDirs)` hands `accessPath.boundaryValue()` (canonical) to the leaf.
- `src/session/permission-session.ts` — `getInfrastructureReadDirs()` concatenates `paths.piInfrastructureDirs` and `config.piInfrastructureReadPaths`.
- `src/handlers/gates/tool-call-gate-pipeline.ts` — `ToolCallGateInputs.getInfrastructureReadDirs()`.
  The pipeline reads it once per call and passes it to `describeExternalDirectoryGate`.
- `src/handlers/gates/external-directory.ts` — `describeExternalDirectoryGate` returns `null` for in-cwd paths.
  It then runs the infra bypass (a `GateBypass` with `decidedBy: {kind: "infrastructure_read"}`), and only then computes `surface` / `preCheck` via `resolveExternalDirectoryPolicy`.
- Pi's own resource roots under `agentDir` come from `resource-loader.ts` in the Pi checkout: `skills`, `prompts`, `themes`, `extensions`, and `SYSTEM.md` / `APPEND_SYSTEM.md`.
  The global context file `AGENTS.md` comes from the same loader's candidate list.
  `npm/` and `git/` are `package-manager.ts`'s install roots, and `agents/` is the `pi-subagents` convention.
- AGENTS.md principle 5 ("mechanism is forever") applies to the logs carve-out.
  The operator chose the small exclusion mechanism over a documented recommendation, because the default should not expose the log the package itself writes.

### Measurements (this planning session)

The repro ran through the real path: `makeFakePi` over the real factory, a real global `config.json`, and a `read` tool call to `<agentDir>/auth.json`, with `agentDir` realpath'd.

| Config                                                          | Before                 |
| --------------------------------------------------------------- | ---------------------- |
| `external_directory: {"*": "deny"}`                             | allowed (bypass)       |
| `external_directory_read: {<auth.json>: "deny"}`                | allowed (bypass)       |
| `external_directory: {<auth.json>: "deny"}`                     | allowed (bypass)       |
| `path_read: {<auth.json>: "deny"}`                              | blocked by `path_read` |
| `path: {<auth.json>: "deny"}`                                   | blocked by `path_read` |
| `read: {<auth.json>: "deny"}`                                   | blocked by `read`      |
| control: `/etc/hosts` under `external_directory: {"*": "deny"}` | blocked                |

The operator's review log holds 486 `infrastructure_auto_allowed` entries (measured), bucketed by root:

- `agentDir/skills` 372
- global `node_modules` 41
- `agentDir/npm` 40
- `agentDir/extensions` 19, of which 2 were reads of this package's own review log
- `settings.json` 5
- `APPEND_SYSTEM.md` 4
- `agentDir/sessions` 4
- 1 with no path

Under the final design, the 4 `sessions/` reads and the 2 review-log reads lose the bypass: 6 of 486 (1.2%, measured).
The operator's own config already allows `~/.pi/agent/*` on `external_directory_read`, so none of those 6 would prompt for them.

## Design Overview

### 1. The list (data)

```ts
const AGENT_DIR_INFRASTRUCTURE_ENTRIES = [
  "agents", "extensions", "git", "npm", "prompts", "skills", "themes",
  "settings.json", "SYSTEM.md", "APPEND_SYSTEM.md", "AGENTS.md",
] as const;

const piInfrastructureDirs = [
  ...AGENT_DIR_INFRASTRUCTURE_ENTRIES.map((entry) => join(agentDir, entry)),
  ...(globalNodeModulesRoot ? [globalNodeModulesRoot] : []),
  ...(piPackageDir ? [piPackageDir] : []),
];
const piInfrastructureExcludedDirs = [globalLogsDir];
```

Files and directories share one list, because `isWithin` covers equality.
A path "within" a file cannot exist, so a file entry admits exactly that file.
The field keeps the name `piInfrastructureDirs`: a rename touches every fixture for no behavior, and the JSDoc will say entries may be files.

### 2. The exclusion (mechanism)

A new plain data interface lives in `src/path/pi-infrastructure-read.ts`:

```ts
export interface InfrastructureReadScope {
  /** Roots (dirs, files, or globs) whose reads are auto-allowed. */
  readonly dirs: readonly string[];
  /** Directories never auto-allowed, even inside a root. Wins over `dirs`. */
  readonly excludedDirs: readonly string[];
}
```

The rest of the change wires that interface through four places:

- **Session.**
  `PermissionSession.getInfrastructureReadScope()` replaces `getInfrastructureReadDirs()`.
  It returns `{ dirs: [...paths.piInfrastructureDirs, ...config.piInfrastructureReadPaths ?? []], excludedDirs: paths.piInfrastructureExcludedDirs }`.
- **Gate inputs and gate.**
  The rename carries through `ToolCallGateInputs` and into `describeExternalDirectoryGate`'s `infraDirs` parameter, which becomes `infraScope`.
- **Normalizer.**
  `PathNormalizer.isInfrastructureRead(toolName, accessPath, scope)` unwraps the scope for the leaf.
- **Leaf.**
  `isPiInfrastructureRead` keeps its array signature and gains a trailing `excludedDirs: readonly string[] = []`.
  The ~26 existing leaf-test calls stay untouched.
  The exclusion check runs right after the tool-name guard and returns `false` for any path `flavor.isWithin` an excluded dir (after `expandHomePath`).

Consumer call site (the gate), Tell-Don't-Ask preserved:

```ts
if (normalizer.isInfrastructureRead(tcc.toolName, accessPath, infraScope)
    && !isTargetedDeny(preCheck)) {
  return { action: "allow", decidedBy: { kind: "infrastructure_read" }, … };
}
```

Design-review check (`design-review` skill):

- **Parameter relay.**
  The scope replaces one relayed array with one relayed object, so the relay depth is unchanged.
  Packaging both lists means no second parameter threads through the pipeline, gate, and normalizer.
- **ISP.**
  The normalizer reads both fields, and the leaf takes the two arrays it reads.
- **Fallow.**
  `fallow guard` (reported by the Tidy-First assessor) shows `session/` and `handlers/` may already import `path/`.
  `config/` produces `readonly string[]` and needs no new import.

### 3. The targeted-deny override (ordering)

`describeExternalDirectoryGate` hoists `surface` and `preCheck` above the bypass.
The bypass applies only when `!isTargetedDeny(preCheck)`, where:

```ts
/** A deny from a rule naming this path, not a catch-all or the universal fallback. */
function isTargetedDeny(check: PermissionCheckResult): boolean {
  return check.state === "deny"
    && check.matchedPattern !== undefined
    && check.matchedPattern !== "*";
}
```

When the bypass yields, the gate builds its ordinary descriptor with that `preCheck`.
The runner then blocks it under `decidedBy: rule`, and `orderDenyFirst` ([#899]) runs it ahead of any earlier `ask`.
`resolveExternalDirectoryPolicy` is side-effect free, so resolving on a call the bypass then admits changes no log or event.

Edge cases:

- **Last-match-wins.**
  `{"<auth.json>": "deny", "*": "deny"}` reports `matchedPattern: "*"`, so the bypass still applies.
  That ordering already makes the targeted rule dead for non-infra paths too, and the migration guide tells users to put the catch-all first.
- **Per-agent frontmatter deny.**
  The resolver folds it in, so it overrides the same way.
- **Yolo.**
  Yolo mode re-permits `ask` only and never `deny`, so a yielded deny still blocks.

### Breaking classification

Breaking on upgrade with no user edit, in three observable ways:

1. A targeted `external_directory`-family deny on an infra path now blocks.
2. Reads under `~/.pi/agent/` outside the harness entries (`sessions/`, `auth.json`, `models.json`, `mcp.json`, `mcp-oauth/`, `trust.json`, `bin/`, crash logs) reach the `external_directory_read` gate.
3. Reads of the package's own logs reach the gate.

The recovery is `external_directory_read: {"~/.pi/agent/sessions/*": "allow"}` or a `piInfrastructureReadPaths` entry.
The logs dir can be re-allowed only by an `external_directory_read` rule, because the exclusion wins over `piInfrastructureReadPaths`.

## Module-Level Changes

### Source

- `src/path/pi-infrastructure-read.ts`:
  - add the exported `InfrastructureReadScope` interface (step 1);
  - add the trailing `excludedDirs` parameter and its check (step 4);
  - update the JSDoc, which lists what qualifies.
- `src/path/path-normalizer.ts`:
  - `isInfrastructureRead(toolName, accessPath, scope: InfrastructureReadScope)` passes `scope.dirs` (step 1);
  - it also passes `scope.excludedDirs` (step 4).
- `src/session/permission-session.ts` — `getInfrastructureReadDirs()` → `getInfrastructureReadScope()`.
  It returns `excludedDirs: []` in step 1 and `paths.piInfrastructureExcludedDirs` from step 4.
- `src/handlers/gates/tool-call-gate-pipeline.ts`:
  - rename the interface member and its JSDoc;
  - rename the local `infraDirs` → `infraScope`;
  - update the class JSDoc bullet "infrastructure-dir list from `getInfrastructureReadDirs()`".
- `src/handlers/gates/external-directory.ts` changes in three steps:
  - param `infraDirs: string[]` → `infraScope: InfrastructureReadScope` (step 1);
  - hoist `surface`/`preCheck` above the bypass (step 2);
  - add the `isTargetedDeny` guard and update the header JSDoc (step 3).
- `src/config/extension-paths.ts`:
  - entry list replaces bare `agentDir` (step 5);
  - new `piInfrastructureExcludedDirs` field and JSDoc (step 4).
- `src/config/config-schema.ts` — `piInfrastructureReadPaths` `markdownDescription` names the narrowed `agentDir` entries and the logs exclusion (step 5).
- `schemas/permissions.schema.json` — regenerated with `pnpm run gen:schema` in the same commit (step 5).

### Tests

- `test/helpers/gate-fixtures.ts` — `makeGateInputs` override `getInfrastructureReadDirs` → `getInfrastructureReadScope`, default `{ dirs: [], excludedDirs: [] }` (step 1).
- `test/helpers/handler-fixtures.ts`:
  - rename the `getInfrastructureReadDirs` spy override and its JSDoc (around line 302 and lines 373–375) (step 1).
- `test/helpers/session-fixtures.ts` — the hand-built `ExtensionPaths` gains `piInfrastructureExcludedDirs: ["/test/agent/logs"]` (step 4; required field, so `tsc` forces it).
  `piInfrastructureDirs: ["/test/agent", "/test/agent/git"]` stays, because it is a unit fixture, not `computeExtensionPaths` output.
- `test/handlers/tool-call-events.test.ts` — the `getInfrastructureReadDirs` override at line ~237 becomes a scope (step 1).
- `test/handlers/gates/tool-call-gate-pipeline.test.ts` — the `getInfrastructureReadDirs` test at lines 127–136 is renamed (step 1).
- `test/session/permission-session.test.ts` — the two `getInfrastructureReadDirs` tests at lines 324–345 become scope assertions (step 1).
  Step 4 adds the `excludedDirs` source.
- `test/path/path-normalizer.test.ts` — the four `isInfrastructureRead` calls at lines 362–383 take a scope (step 1).
  Step 4 adds an exclusion case.
- `test/handlers/gates/external-directory.test.ts` changes in two steps:
  - the `gateUnderTest(tcc, infraDirs, …)` helper wraps its array as `{ dirs: infraDirs, excludedDirs: [] }`, so its ~25 callers stay unchanged (step 1);
  - add the targeted-deny cases (step 3).
- `test/handlers/native-tool-target-acceptance.test.ts` and `test/handlers/external-directory-symlink-acceptance.test.ts` call `describeExternalDirectoryGate` directly (5 calls).
  Step 1 updates the infra argument to a scope literal; re-derive the count with `grep -rn "describeExternalDirectoryGate(" test` at the step.
- `test/path/pi-infrastructure-read.test.ts` — new `describe("excluded directories")` (step 4).
- `test/config/extension-paths.test.ts` changes in two steps:
  - step 4 adds a `piInfrastructureExcludedDirs` test;
  - step 5 rewrites the `piInfrastructureDirs` assertions (lines 50–97: the bare-`agentDir` `toContain`, the `toHaveLength(2)` at the null-discovery case, and the `toEqual` at line 88).
- `test/composition-root.test.ts` — new end-to-end `describe("Pi infrastructure reads")` (steps 3–5) through the real factory.
  It must `realpathSync` the `mkdtemp` agentDir and re-stub `PI_CODING_AGENT_DIR`: on macOS `/var` → `/private/var`, and the un-canonicalized list never matches otherwise ([#1018], measured).

### Docs

All in step 6 unless noted:

- `docs/migration/0955-pi-infrastructure-read-narrowed.md` (new) — a before/after table per config.
  It gives the recovery rules, catch-all-first ordering advice, and the note that the logs exclusion wins over `piInfrastructureReadPaths`.
- `README.md` — add the migration guide row to the docs table (beside `0644-project-trust-gating.md`).
- `docs/configuration.md` `#### Pi Infrastructure Read Auto-Allow`:
  - item 1 becomes the entry list;
  - add the logs exclusion;
  - add that a targeted `external_directory` deny overrides the bypass, while `"*"` and the universal fallback do not;
  - fix the opening sentence ("even when `external_directory` is `ask` or `deny`").
- `docs/decisions/0010-permission-log-secret-exposure.md` — the "agent reading its own log" row's *Closed by owner-only modes* cell.
  The claim "`isPiInfrastructureRead` does not auto-allow it" was false before this change; it becomes true through the excluded logs dir.
  Reword it to name the exclusion.
- `docs/architecture/architecture.md` changes in four places:
  - module entries `extension-paths.ts` (line ~891), `pi-infrastructure-read.ts` (~955), `tool-call-gate-pipeline.ts` (~971: `getInfrastructureReadDirs` → `getInfrastructureReadScope`), and `external-directory.ts` (~976: the targeted-deny yield);
  - the Mermaid `ExtDir` node label (~294), "Pi infrastructure reads auto-allowed before gate" → "auto-allowed unless a targeted deny matches";
  - the `[#955]` sweep bullet (~1264) is predicted **unchanged**: it records a disposition, and this issue has no roadmap step or `✅` mark.
- `.pi/skills/package-pi-permission-system/SKILL.md` — the `handler-fixtures.ts` and `gate-fixtures.ts` bullets name `getInfrastructureReadDirs` (lines 241, 243).
  They become `getInfrastructureReadScope` in step 1, with the rename.

Predicted unchanged:

- `docs/cross-extension-api.md` and `docs/opencode-compatibility.md`, which name only the `infrastructure_auto_allowed` resolution, and that is unchanged.
- `src/authority/decision-source.ts`: `infrastructure_read` is unchanged.

## Test Impact Analysis

1. **Tests the change enables.**
   - Leaf exclusion tests, where an excluded dir inside an included root is not admitted.
   - Targeted-deny tests at the gate unit level, with a resolver stub returning each of the three deny shapes.
   - An end-to-end matrix at the composition root that pins the issue's scenario through real config.
2. **Tests made redundant.**
   None.
   The `extension-paths.test.ts` `toContain(agentDir)` / `toHaveLength(2)` assertions are rewritten, not removed.
   One `toEqual` for the full list replaces the per-entry `toContain`s, which is stronger.
3. **Tests that stay as-is.**
   - The ~26 leaf `isPiInfrastructureRead` calls, since the trailing parameter defaults to `[]`.
   - The bypass-shape tests in `external-directory.test.ts` (`decidedBy`, log event, decision), since the `gateUnderTest` default resolver returns `ask` and does not yield.
   - `runner.test.ts`'s `infrastructure_read` descriptors.

## Invariants at risk

- **The infra containment uses the canonical boundary value** ([#418], [#511]).
  `normalizer.isInfrastructureRead` must keep passing `accessPath.boundaryValue()`.
  `test/path/path-normalizer.test.ts` `describe("isInfrastructureRead")` pins it, along with `external-directory-symlink-acceptance.test.ts`.
- **A bypass records `decidedBy: {kind: "infrastructure_read"}`, never inferred** ([#726]).
  `external-directory.test.ts` line ~92 pins it, and step 3 must not route a yielded deny through the bypass shape.
- **Deny-first ordering** ([#899]).
  A yielded deny becomes a descriptor carrying `preCheck` deny, so `orderDenyFirst` must run it ahead of an earlier `ask`.
  The composition-root step-3 test covers it implicitly; no new test is needed because `orderDenyFirst` is unchanged.
- **Write tools never get the bypass.**
  `external-directory.test.ts` "does NOT bypass for write tools targeting infra dirs" pins it.
- **Constituencies.**
  - Skill reads (372 of 486 measured) stay auto-allowed for every config, including catch-all-deny configs.
    The step-5 composition-root test reads `<agentDir>/skills/x/SKILL.md` under `external_directory_read: {"*": "deny"}` and expects allow.
  - Pi package reads (`npm/`, global `node_modules`) are likewise unchanged.

## TDD Order

1. **`refactor(pi-permission-system): carry infrastructure reads as an InfrastructureReadScope`**
   - **Prepares:** steps 3–5 thread `excludedDirs` through four layers; landing the rename first keeps those commits small (Tidy-First assessor, Recommended).
   - **Change:**
     - Add `InfrastructureReadScope`.
     - Rename `getInfrastructureReadDirs` → `getInfrastructureReadScope` (returning `excludedDirs: []`) on `PermissionSession` and `ToolCallGateInputs`.
     - Change `PathNormalizer.isInfrastructureRead` and `describeExternalDirectoryGate` to take the scope; the normalizer passes only `scope.dirs` to the unchanged leaf.
     - Update every fixture and call site listed under Tests (step 1), and the two SKILL.md fixture bullets.
   - **Verify:** the full suite stays green with no assertion changes beyond the rename and the scope shape, and `pnpm run check` passes.
   - **Killing mutation:** none, because this is a pure refactor.
     The rename's correctness is `tsc` plus the unchanged bypass tests.
2. **`refactor(pi-permission-system): resolve external-directory policy before the infrastructure bypass`**
   - **Prepares:** step 3 becomes a one-condition change.
   - **Change:** hoist `surface` and `preCheck` (`resolveExternalDirectoryPolicy`) above the bypass block; the bypass stays unconditional.
   - **Verify:** the full suite stays green.
     Before committing, grep `test/` for bypass-reaching tests that use a bare `makeResolver()` (whose `resolve` returns `undefined`); none reach the bypass today (the pipeline test's three bare resolvers use an empty infra scope), but re-check at the step.
   - **Killing mutation:** none (refactor).
3. **`fix(pi-permission-system)!: a targeted external_directory deny now blocks a Pi infrastructure read`**
   - **Red, gate unit (`external-directory.test.ts`, new `describe("infrastructure bypass under a deny")`):**
     - The resolver returns `deny` with `matchedPattern: "/test/agent/git/x/SKILL.md"`.
       Expect a `GateDescriptor` whose `preCheck.state` is `deny`, not a bypass.
     - Same with `matchedPattern: "*"`: expect the bypass (`decidedBy: infrastructure_read`).
     - Same with `matchedPattern: undefined`: expect the bypass.
     - Targeted `ask` (`matchedPattern: "/test/agent/git/x/SKILL.md"`, state `ask`): expect the bypass.
   - **Red, end to end (`composition-root.test.ts`):**
     - `external_directory_read: {"<agentDir>/git/x/SKILL.md": "deny"}` → `read` of it blocks with a reason naming `external_directory_read`.
     - `external_directory: {"*": "deny"}` → the same read is allowed.
   - **Green:** add `isTargetedDeny` and the `&& !isTargetedDeny(preCheck)` guard.
   - **Killing mutations:**
     - (a) Make `isTargetedDeny` return `false` unconditionally: this kills the targeted-deny unit test and the e2e block.
     - (b) Drop `&& check.matchedPattern !== "*"`: this kills the catch-all unit test and the e2e catch-all allow.
     - (c) Drop `&& check.matchedPattern !== undefined`: this kills the universal-fallback unit test.
     - (d) Replace `check.state === "deny"` with `check.state !== "allow"`: this kills the targeted-`ask` unit test.
   - **Footer:** `BREAKING CHANGE: an external_directory or external_directory_read deny whose pattern names a Pi infrastructure path now blocks the read; a bare "*" or the universal fallback still does not.`
4. **`fix(pi-permission-system)!: the Pi infrastructure read bypass no longer covers the permission logs`**
   - **Red, leaf (`pi-infrastructure-read.test.ts`, `describe("excluded directories")`):**
     - With `dirs: ["/a"]` and `excludedDirs: ["/a/x/logs"]`:
       - `/a/x/logs/r.jsonl` → `false`;
       - `/a/x/config.json` → `true`;
       - `/a/x/logs` itself → `false`.
     - A `~`-prefixed excluded dir expands.
     - On win32, a mixed-case excluded dir still excludes.
   - **Red, other units:**
     - `path-normalizer.test.ts` — the exclusion is passed through.
     - `extension-paths.test.ts` — `piInfrastructureExcludedDirs` equals `[getGlobalLogsDir("/test/agent")]`.
     - `permission-session.test.ts` — the scope's `excludedDirs` comes from `paths`, and a config `piInfrastructureReadPaths` entry covering the logs dir is still excluded.
   - **Red, end to end:** with `external_directory_read: {"*": "deny"}`, a `read` of `<agentDir>/extensions/pi-permission-system/logs/<review log>` blocks.
     `<agentDir>/extensions/pi-permission-system/config.json` is allowed.
   - **Green:**
     - Add the `piInfrastructureExcludedDirs` field and its fixture.
     - Add the leaf's trailing `excludedDirs` parameter and check.
     - The normalizer passes `scope.excludedDirs`, and the session sources it from `paths`.
   - **Killing mutations:**
     - (a) Delete the leaf's exclusion loop: this kills the leaf tests and the e2e log block.
     - (b) Make `computeExtensionPaths` return `piInfrastructureExcludedDirs: []`: this kills the `extension-paths` test and the e2e log block.
     - (c) Have the session return `excludedDirs: []`: this kills the session test and the e2e block.
     - (d) Have the normalizer omit `scope.excludedDirs`: this kills the normalizer test.
   - **Footer:** `BREAKING CHANGE: reads of the package's own logs directory now go through the external_directory gate; allow them with an external_directory_read rule.`
5. **`fix(pi-permission-system)!: the Pi infrastructure read bypass covers only Pi's harness entries, not all of ~/.pi/agent`**
   - **Red (`extension-paths.test.ts`):**
     - Assert `piInfrastructureDirs` with one `toEqual` against the eleven `agentDir` entries, then the discovered root, then `piPackageDir`.
     - Rewrite the null-discovery `toHaveLength(2)` case to the eleven entries.
     - Assert bare `/test/agent` is absent.
   - **Red, end to end:** under `external_directory_read: {"*": "deny"}`:
     - blocked: `read` of `<agentDir>/auth.json`, `<agentDir>/sessions/s.jsonl`, `<agentDir>/mcp-oauth/t.json`;
     - allowed: `read` of `<agentDir>/skills/x/SKILL.md`, `<agentDir>/settings.json`, `<agentDir>/APPEND_SYSTEM.md`.
   - **Green:**
     - Add the `AGENT_DIR_INFRASTRUCTURE_ENTRIES` list.
     - Update the `ExtensionPaths.piInfrastructureDirs` JSDoc (entries may be files).
     - Update `config-schema.ts`'s `markdownDescription`, then `pnpm run gen:schema`.
   - **Killing mutations:**
     - (a) Re-add bare `agentDir` to the list: this kills the `toEqual` and the three e2e blocks.
     - (b) Drop `"settings.json"` from the entries: this kills the `toEqual` and the e2e `settings.json` allow.
     - (c) Drop `"APPEND_SYSTEM.md"`: this kills the e2e `APPEND_SYSTEM.md` allow.
   - **Footer:** `BREAKING CHANGE: read-only tools no longer auto-read everything under ~/.pi/agent; sessions/, auth.json, models.json, mcp.json, mcp-oauth/, trust.json, bin/, and crash logs now go through the external_directory gate. Restore one with an external_directory_read allow rule, e.g. "~/.pi/agent/sessions/*": "allow".`
6. **`docs(pi-permission-system): document the narrowed Pi infrastructure read bypass`**
   - **Change:** the migration guide, the README row, `configuration.md`, the ADR 0010 row, and the `architecture.md` module entries plus the Mermaid node, as listed under Docs.
   - **Verify:**
     - `pnpm exec rumdl check` on each touched file.
     - `grep -rn "getInfrastructureReadDirs" packages/pi-permission-system .pi/skills` returns nothing.
     - `grep -n "agentDir\`, \`agentDir/git" packages/pi-permission-system/src/config/config-schema.ts` returns nothing.

No `Co-authored-by:` trailer: the issue is the operator's own, and no third-party mechanism is adopted.

## Risks and Mitigations

- **A targeted deny that should fire reports `"*"` because a later catch-all wins (last-match-wins).**
  The migration guide tells users to write the catch-all first.
  This is the same ordering every other surface already requires.
- **Yielding re-resolves on every infra read.**
  `resolveExternalDirectoryPolicy` is already called on every non-bypassed external read, and on the bypassed ones it adds one resolve with no I/O beyond what the manager does today.
  It is not measured; the risk is low because the gate already pays this on every outside-cwd non-infra read.
- **A user reads `~/.pi/agent/sessions/` through the `read` tool in a workflow and starts getting prompts.**
  The migration guide and the `BREAKING CHANGE` footer name the recovery rule.
  The measured frequency on the operator's log is 4 of 486.
- **Extension configs under `extensions/` may hold secrets** (an extension's `config.json`).
  This stays out of scope.
  The operator chose to keep `extensions/` for extension source and config reads (19 of 486 measured), and a targeted deny now closes any one of them.
- **The composition-root test silently passes without the bypass** when `agentDir` sits behind a symlink.
  Mitigated by `realpathSync` in the new describe's setup, with a comment citing [#1018].

## Open Questions

- Whether `subagent-sessions/` (a `pi-subagents` sibling of `sessions/`) needs its own migration-guide line.
  It was never in the list explicitly, and the bare-`agentDir` entry covered it; the guide's generic recovery rule covers it too.

[#418]: https://github.com/gotgenes/pi-packages/issues/418
[#511]: https://github.com/gotgenes/pi-packages/issues/511
[#726]: https://github.com/gotgenes/pi-packages/issues/726
[#899]: https://github.com/gotgenes/pi-packages/issues/899
[#956]: https://github.com/gotgenes/pi-packages/issues/956
[#1018]: https://github.com/gotgenes/pi-packages/issues/1018
