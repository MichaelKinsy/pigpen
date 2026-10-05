---
issue: 999
issue_title: "mcp_servers prompt section missing when pi-permission-system is enabled"
---

# State the tool surface and skill filter through `systemPromptOptions`

## Release Recommendation

**Release:** ship now — batch "pi-1.0 prompt options" tail (this issue completes the batch)

At [#970]'s planning the operator deferred its release so one major carries both the `>=1.0.0` peer floor and this fix (an ad-hoc batch recorded in [#970]'s retro, with this issue as the tail).
[#970] has landed unreleased, and [#1009] (the pi-subagents cut) has already shipped as pi-subagents 21.9.1, so the double-listing window in Risks is closed on the pi-subagents side.
[#970] moved the ≤0.85 header-layout deletion into this issue, so this plan deletes both prompt layouts.

## Problem Statement

With pi-permission-system loaded, Pi 0.99.2 and 1.0.0 never show the model the `<mcp_servers>` section, so a connected `codemode`/`deferred` MCP server is invisible to it.
The operator measured the count of `<mcp_servers>` in the first provider request, with one configured server: 1 with builtin:mcp alone and 0 with this extension loaded, on both versions.

`AgentPrepHandler.handle` returns `{ systemPrompt }` whenever its pass changes the string.
Under default config that is every turn, because the ADR 0014 relocation always moves `<tools>`/`<rules>`.
`ExtensionRunner.emitBeforeAgentStart` stores the returned string as `forceSystemPrompt`, and `buildSystemPromptState` then returns that opaque string with no sections. builtin:mcp adds `mcp_servers` by editing `systemPromptOptions.sections` from its own handler, and built-ins load after file extensions, so its edit always lands on a prompt that is already frozen.

The bug is not specific to MCP.
Any section any later extension adds is dropped the same way.

## Goals

- Never return `systemPrompt` from `before_agent_start`; state every prompt change by mutating `event.systemPromptOptions`, so sections added later in the chain reach the provider.
- On a root whose prompt Pi authored, let Pi render `<tools>`/`<rules>` in place from the active set this handler already narrows with `setActiveTools`.
- On a subagent child, which is a `customPrompt` session, state the child's own tool surface as `sections.tools` and `sections.rules`, which Pi places after `<cwd>`.
- Filter denied skills out of `systemPromptOptions.skills` instead of rewriting `<available_skills>` in the string.
- Delete the string-relocation machinery in `tool-surface-prompt.ts` and the string-rewrite half of `skill-prompt-sanitizer.ts`, because nothing calls them once the handler stops forcing.
- Record the decision as ADR 0015, superseding ADR 0014's relocation.

Breaking-change classification: **not breaking**, so `fix:`.
The tool list moves from the tail back to Pi's own position, but ADR 0014 itself records that "the block's position is not a documented contract".
The model's authoritative capability list is still the request's `tools` array.
The breaking part of this sequence, the peer floor raise, belongs to [#970].

## Non-Goals

- **Keeping the inherited-prefix invariant (ADR 0014, [#890]).**
  The operator chose in-place over the "pointer" alternative, which would have overridden Pi's head `<tools>`/`<rules>` with constant text and rendered the real surface in new tail sections.
  Parent and child now share only the preamble, about 171 characters as measured during [#890]'s planning (not re-measured).
  That prefix pays only for hosts that reuse the leading system text ([#180]).
- **The pi-subagents cut**, which is [#1009].
  It is a prerequisite, not part of this plan.
- **pi-nocd's identical defect**, which is [#1000].
- **The peer-floor raise**, which is [#970] (landed).
- **Filtering an `<available_skills>` catalogue Pi did not render.**
  A catalogue written into an operator's `SYSTEM.md`, an `--append-system-prompt`, or another extension's text is no longer edited.
  The old string pass removed denied entries from every catalogue in the prompt.
  The options pass reaches only the one Pi renders from `systemPromptOptions.skills`.
  This follows the [#919]/[#932] precedent that this package does not edit text it did not write, and exposure is not authorization: the skill `input` gate and the path gate's skill-entry match still enforce `deny`.
  Skill entries are still parsed from the whole rendered prompt, so the path gate's view is unchanged.
- **Defending against another extension's explicit `selectedTools` edit.**
  Pi lets an explicit edit win over the live loadout, which could re-activate a tool this handler withheld.
  That is independent of this change, and the `tool_call` gate still blocks the call.
- **Reading guidelines from `systemPromptOptions.toolGuidelines` instead of `pi.getAllTools()`.**
  Both are Pi's attribution on 1.0; switching is cleanup, not this fix.

## Background

- `src/handlers/before-agent-start.ts`, `AgentPrepHandler.handle`:
  1. `turnPrep.prepare`, then agent name, `resolveExposedTools`, and `toolRegistry.setActive(allowedTools)`.
  2. Then `renderToolSurface(event.systemPrompt, …)`, unless the root is under a custom prompt ([#980]).
  3. Then `resolveSkillPromptEntries`.
  4. Returns `{ systemPrompt }` whenever the result differs from `event.systemPrompt`.
- `src/exposure/tool-surface-prompt.ts` is the ADR 0014 relocation.
  `detectPromptLayout` picks a layout, `settleRegion` and the removal functions strip the sections Pi wrote, and `renderSectionBlock` appends this session's own block.
  `toolSurfaceBullets` (private) computes the bullets, following `buildSystemPrompt`'s rules.
- `src/exposure/skill-prompt-sanitizer.ts`, `resolveSkillPromptEntries`, parses every `<available_skills>` block, classifies each entry, collects the visible entries for `setActiveSkillEntries`, and rewrites the string.
- Pi 1.0.0 mechanism, read in the `../../pi` checkout at `v1.0.0-2-g7fbbd5f4a`.
  `git diff v1.0.0 HEAD` over `system-prompt.ts`, `extensions/runner.ts`, and `agent-session.ts` is empty, so these files match the published 1.0.0.
  - `emitBeforeAgentStart` hands every handler the same `currentOptions` object, and `event.systemPrompt` is a getter that re-renders it on each read.
    A returned `systemPrompt` becomes `currentOptions.forceSystemPrompt`.
  - After the chain, `agent-session.ts` sets `selectedTools` to `getActiveToolNames()` unless a handler edited `selectedTools`, so `setActiveTools` alone narrows Pi's rendered `<tools>`/`<rules>`.
  - `buildSystemPromptSections` renders `preamble`, `tools`, `rules`, `docs` (only without `customPrompt`), then `addendum`, `project_context`, `skills`, `cwd`, then each custom `sections` entry in insertion order, skipping empty ones.
    A custom key that matches a built-in replaces that built-in's content in place; a new key lands after `<cwd>`.
  - Base `toolSnippets` holds every registered tool's snippet, not only the active ones.
  - Mutable options, `sections`, `toolGuidelines`, and the `handlerEditedTools` reconciliation all arrived in commit `9e05370b2`, tagged into `v0.86.0`.
- Pi's `docs/extensions.md` says: "Prefer changing prompt sections, selected tools, or guidelines so Pi can append a transcript delta.
  Returning `systemPrompt` … replaces the whole prompt for that run."
- Upstream: earendil-works/pi#9932 (open) reports a related forced-prompt defect, where a forced prompt keeps tools that `setActiveTools` removed.
  No upstream issue proposes letting a forced prompt compose with later section edits.
- AGENTS.md "Stale in-process extension code": this session's own Pi still runs the published handler, so a manual check of the fix needs a fresh Pi session.

## Design Overview

The repro is the operator's.
The counts come from real provider requests on 0.99.2 and 1.0.0 (the issue's comments).
The mechanism above was read from Pi's source; no spike was run.

### Decision table

| Node                          | Prompt          | Tool surface                                                | Skills                              |
| ----------------------------- | --------------- | ----------------------------------------------------------- | ----------------------------------- |
| root                          | Pi-authored     | nothing beyond `setActive`; Pi renders in place             | filter `systemPromptOptions.skills` |
| root                          | custom ([#980]) | nothing                                                     | filter `systemPromptOptions.skills` |
| child (`detector.isSubagent`) | custom, always  | set `sections.tools` (when any bullet) and `sections.rules` | filter `systemPromptOptions.skills` |

The handler always returns `{}`, or `{ message }` should one ever be added; never `systemPrompt`.

### Handler sketch

```typescript
async handle(event: BeforeAgentStartEvent, ctx): Promise<BeforeAgentStartEventResult> {
  this.turnPrep.prepare(ctx);
  const agentName = this.session.resolveAgentName(ctx, event.systemPrompt);
  // … surface, setActive(allowedTools), debug log — unchanged …
  const options = event.systemPromptOptions;
  if (this.isSubagentUnderCustomPrompt(event, ctx)) {
    Object.assign(options.sections, renderToolSurfaceSections({ allowedTools, toolSnippets: options.toolSnippets, … }));
  }
  const skills = classifySkillPromptEntries(event.systemPrompt, this.resolver, agentName, normalizer);
  this.session.setActiveSkillEntries(skills.entries);
  options.skills = options.skills.filter((skill) => !skills.deniedNames.has(skill.name));
  return {};
}
```

The skill classification reads `event.systemPrompt` after the sections mutation and before the skills filter.
The getter re-renders, so the parse sees Pi's full catalogue.

The predicate `statesOwnToolSurface` (`!hasCustomPrompt || isSubagent`) narrows to "subagent under a custom prompt".
A Pi-authored root no longer needs the pass at all.
A subagent child is always under a custom prompt (ADR 0014's [#980] amendment).
A detected subagent without a custom prompt is not a shape Pi produces; if one appeared, it would get Pi's own in-place `<tools>` and no extra sections.

### New pure function

```typescript
/** This session's tool surface as Pi section contents (no tags; Pi wraps them). */
export interface ToolSurfaceSections {
  readonly tools?: string; // absent when no allowed tool has a snippet
  readonly rules: string;
}
export function renderToolSurfaceSections(inputs: ToolSurfaceInputs): ToolSurfaceSections;
```

It is built on the existing `toolSurfaceBullets`.
`ToolSurfaceInputs.piAuthoredPreamble` is dropped when `renderToolSurface` goes, because only the removal read it.

`ToolSurfaceInputs` reads `allowedTools`, `toolSnippets`, `guidelinesByTool`, and `promptGuidelines`, and all four are used (ISP holds).

### Skill classification

```typescript
export function classifySkillPromptEntries(
  prompt: string,
  checker: SkillPermissionChecker,
  agentName: string | null,
  normalizer: PathNormalizer,
): { entries: SkillPromptEntry[]; deniedNames: ReadonlySet<string> };
```

`entries` is the visible (non-`deny`) set, exactly what `resolveSkillPromptEntries` returns today.
The filter matches `Skill.name` against `<name>` decoded from the rendered catalogue.
Pi renders `<name>` from `skill.name` through `formatSkillsForPrompt`, so the two are the same string.

### Idempotence

Pi re-normalizes the options from its base each run, so mutations do not accumulate in production.
The handler is still idempotent over a reused options object: assigning `sections` keys overwrites, and filtering by name is a fixed point.
The test fixture builds a fresh event per fire anyway (Tidy-First finding).

### Edge cases

- **Child with no snippet for any allowed tool:** `sections.tools` stays unset and only `sections.rules` is set, matching today's `renderSectionBlock`.
- **Another extension set `sections.tools` before us in a child:** it is overwritten.
  The [#901] contract makes the last writer correct.
- **Section names:** `tools` and `rules` pass Pi's `/^[a-z][a-z0-9_-]*$/` check, so Pi does not throw.

## Module-Level Changes

- `src/handlers/before-agent-start.ts`:
  - Use Pi's `BeforeAgentStartEvent` (typed after [#970]) or a narrow `Pick` that adds `sections` and `skills` to the lean payload.
  - Mutate the options and always return `{}`.
  - Rename `statesOwnToolSurface` to reflect the subagent-only branch.
  - Drop the `renderToolSurface`/`resolveSkillPromptEntries` imports.
  - Rewrite the class doc comment, which cites ADR 0014's relocation and [#890].
- `src/exposure/tool-surface-prompt.ts`:
  - Add `renderToolSurfaceSections` and `ToolSurfaceSections`.
  - Delete `renderToolSurface`, `PromptLayout`, `SECTION_LAYOUT`, `detectPromptLayout`, `lastCwdSectionClose`, `settleRegion`, and every removal function.
  - Also delete `findTaggedSection`, `laterPiSectionStart`, `renderSectionBlock`, `taggedSection`, `normalizePrompt`, `collapseExtraBlankLines`, `isSectionBodyLine`, `findSection`, and the constants only they read.
  - Delete the ≤0.85 header layout too (`HEADER_LAYOUT`, `removeToolSurfaceSections`, `renderHeaderBlock`, the footer handling, and their header-shaped tests), which [#970] left in place for this issue.
  - Rewrite the module doc comment, which describes relocation.
- `src/exposure/skill-prompt-sanitizer.ts`:
  - Add `classifySkillPromptEntries`.
  - Delete `resolveSkillPromptEntries`, `renderAvailableSkillsSection`, `removePromptRange`, and `encodeXml`.
  - Drop `SkillPromptSection.start`/`end` if nothing else reads them; `parseAllSkillPromptSections`' other callers must be grepped first.
- `test/exposure/tool-surface-prompt.test.ts` (1016 lines): keep and retarget the bullet-content tests onto `renderToolSurfaceSections`, and delete the removal and layout tests.
- `test/exposure/skill-prompt-sanitizer.test.ts`:
  - Retarget the `.prompt` assertions (around lines 82–179) onto `deniedNames`/`entries`.
  - Delete the string-rewrite regression tests (around lines 314–395), whose subject is gone.
  - Update the `start`/`end` assertion (around line 286) if those fields go.
- `test/handlers/before-agent-start.test.ts`:
  - `makeEvent` gains `sections: {}` and `skills: []` defaults and is typed as Pi's options.
  - About 25 `result.systemPrompt` assertions (lines 220–565) become assertions on `event.systemPromptOptions` and `result`.
- `test/composition-root.test.ts`:
  - The `tool-surface prose under a custom system prompt` describe (around line 1815) asserts `sections.tools` instead of `result.systemPrompt`.
  - The `systemPromptOptions: { cwd }` literals at around lines 535 and 1232–1281 gain `sections: {}`/`skills: []` wherever the handler now reads them.
- `test/helpers/handler-fixtures.ts`: add a shared options builder only if both test files need it.
- Predicted unchanged, from a grep for `renderToolSurface|resolveSkillPromptEntries|before_agent_start` over `test/`, which matched only the four files above:
  - `test/exposure/tool-surface-baseline.test.ts`, `test/exposure/tool-registry.test.ts`.
  - `src/exposure/tool-surface-baseline.ts`, `src/exposure/tool-registry.ts`.
  - `src/session/active-agent.ts`, which still reads `event.systemPrompt`.
- Docs:
  - `docs/decisions/0015-prompt-changes-through-system-prompt-options.md` (new), recording the decision, the rejected "pointer" and "keep forced" alternatives, the prefix cost, the [#1009] dependency, and the non-Pi catalogue residual.
  - `docs/decisions/0014-tool-surface-is-node-local-prose.md`: change the status to superseded by ADR 0015 and add a short pointer section.
  - `docs/configuration.md` around lines 1325–1335, in the tool-filtering notes:
    - Replace the "relocated" bullet.
    - Remove the "restored tool's line reappears one turn late" bullet.
      On 1.0, base `toolSnippets` holds every registered tool and Pi renders from the reconciled active set, so the lag should be gone; verify at implementation.
    - Rewrite the "recomputed and returned on every turn" bullet, since nothing is returned now.
    - Add the non-Pi skill catalogue residual.
  - `docs/architecture/architecture.md`: the module-tree entries for `tool-surface-prompt.ts` (line 1009) and `skill-prompt-sanitizer.ts` (line 1010).
    Grep the whole doc for `relocat`, `renderToolSurface`, and `forceSystemPrompt` prose, and for Mermaid node labels that describe the `before_agent_start` pass.
  - `.pi/skills/package-pi-permission-system/SKILL.md`:
    - Line 44 ("tool-surface prompt pass … prompt relocation").
    - Line 218 (upstream-assumptions row: "the shape `tool-surface-prompt.ts` rewrites").
    - Line 259 (testing bullet: "pi's sections removed, this session's rendered at the tail").
    - Add an upstream-assumption row: "`systemPromptOptions` mutations from `before_agent_start` reach the rendered prompt, and an unedited `selectedTools` is reconciled to the live active set" (`runner.ts`, `agent-session.ts`).
  - `README.md` has no relocation prose (checked: a grep for `relocat|tool list|Available tools` came up empty).

## Test Impact Analysis

1. **New tests enabled:**
   - `renderToolSurfaceSections` is unit-testable as data with no prompt fixture.
   - `classifySkillPromptEntries` exposes `deniedNames` directly.
   - Handler tests assert option mutations instead of string diffs.
2. **Redundant tests:**
   - Every layout-detection, region-removal, and [#919]/[#932] "do not destroy user text" test in `tool-surface-prompt.test.ts` loses its subject, because no text is edited any more.
   - The same goes for the skill string-rewrite tests.
   - Delete them in the step that deletes their subject.
3. **Must stay:**
   - The `shouldExposeTool` tests, the `setActive` and `policy changes across turns` tests ([#873]), and the bullet-content tests (filler, guideline attribution, the `promptGuidelines` carry-over).
   - The [#980] tests, retargeted: a custom-prompt root gets no `sections` keys and `{}`.

## Invariants at risk

| Invariant                                                         | Source                    | Pinned by after this change                                                                                                                    |
| ----------------------------------------------------------------- | ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| Relaxing a rule restores the withheld tool                        | [#873]                    | `policy changes across turns` describe; unchanged                                                                                              |
| A custom-prompt root gets no tool surface                         | ADR 0014 [#980] amendment | handler test: `sections` stays `{}`, result `{}`; composition-root `leaves a root session's custom prompt as Pi built it`                      |
| A child states its own tools                                      | ADR 0014                  | handler test and composition-root test on `sections.tools`                                                                                     |
| Denied tool's guidelines absent                                   | ADR 0014                  | `renderToolSurfaceSections` test for the child; for the root, Pi's `buildRules` over the reconciled `selectedTools` (Pi-side, not ours to pin) |
| Other extensions' `promptGuidelines` carried in the child's rules | [#962]                    | retargeted bullet test                                                                                                                         |
| Prompt stable across turns for a stable policy ([#437])           | ADR 0014                  | holds by construction: identical mutations each turn, so Pi's section diff is empty; the cross-turn test asserts equal options                 |
| Inherited identity shared with the parent                         | ADR 0014 / [#890]         | **deliberately retired** (operator decision); ADR 0015 records it                                                                              |
| User or other-extension text never edited                         | [#919] / [#932]           | holds by construction: no string is produced                                                                                                   |

## TDD Order

Prerequisite: [#970] has landed on `main`, so the devDependency is 1.0; both prompt layouts are still present and step 4 deletes them.
Re-read `tool-surface-prompt.ts` before step 1, since [#970] reshapes it.

1. **`refactor(pi-permission-system): render the tool surface as section contents`**
   - Prepares the child branch of step 4 (Tidy-First recommendation 1): `toolSurfaceBullets` is private today.
   - Add `renderToolSurfaceSections` and `ToolSurfaceSections` beside `renderSectionBlock`, unused by `src/` yet.
     Pin them with tests equal to `renderSectionBlock`'s output with the tags stripped: the `tools` and `rules` contents, and `tools` absent when no allowed tool has a snippet.
   - `fallow dead-code` may flag the export as unused until step 4; if it does, fold this step into step 4 instead.
   - Killing mutations:
     - Make `renderToolSurfaceSections` always return `tools: bullets.tools.join("\n")`.
       The "omits tools when no allowed tool has a snippet" test must go red.
     - Make it return `taggedSection("rules", bullets.rules)` for `rules`.
       The content-equality test must go red.
2. **`refactor(pi-permission-system): classify skill entries apart from the prompt rewrite`**
   - Prepares step 5 (Tidy-First recommendation 3).
   - Extract `classifySkillPromptEntries` returning `{ entries, deniedNames }`.
     `resolveSkillPromptEntries` becomes classify-then-rewrite, with its behavior unchanged.
   - New tests on `deniedNames`: a denied skill is named, an `ask` skill is not, and a skill denied in two catalogues is named once.
   - Killing mutations:
     - Filter `state !== "allow"` instead of `=== "deny"` when building `deniedNames`.
       The `ask`-skill test must go red.
     - Return an empty set.
       The denied test must go red.
3. **`test(pi-permission-system): build before_agent_start events with Pi's prompt options`**
   - Prepares steps 4–5 (Tidy-First recommendation 2).
   - `makeEvent` in `test/handlers/before-agent-start.test.ts` defaults `sections: {}` and `skills: []`, is typed against Pi's `NormalizedBuildSystemPromptOptions`, and returns a fresh event per call.
   - Callers keep a reference so a later step can read the options back.
   - The composition-root `before_agent_start` literals gain the same fields.
   - The suite stays green with no assertion changes; there is no mutation because there is no behavior.
4. **`fix(pi-permission-system): keep prompt sections other extensions add, such as <mcp_servers>`**
   - The handler never returns `systemPrompt` for the tool surface.
     The root does nothing beyond `setActive`, and a subagent child under a custom prompt assigns `renderToolSurfaceSections(…)` into `systemPromptOptions.sections`.
   - Skills still go through `resolveSkillPromptEntries` in this step, reading `event.systemPrompt` after the sections mutation, so `{ systemPrompt }` comes back only when a skill is denied.
   - In the same commit:
     - Delete `renderToolSurface` and all relocation machinery, along with their tests in `tool-surface-prompt.test.ts`.
     - Rewrite the handler tests (around lines 266–565) to assert options and `result`.
     - Retarget the composition-root `customPromptEvent` child test onto `sections.tools`.
   - Killing mutations:
     - Make `handle` return `{ systemPrompt: event.systemPrompt }` unconditionally.
       The "returns no override for a Pi-authored root with nothing denied" test must go red.
     - Drop the `isSubagent` condition so a root under a custom prompt also gets sections.
       The [#980] tests must go red.
     - Skip the `sections.rules` assignment.
       The child-rules test must go red.
5. **`fix(pi-permission-system): keep later prompt sections when a skill is denied`**
   - Filter denied skills out of `systemPromptOptions.skills` with `classifySkillPromptEntries`, and stop returning `systemPrompt` at all.
   - In the same commit, delete `resolveSkillPromptEntries`, the rewrite helpers, and their tests, and retarget the handler's skill tests (around lines 220–260 and 331) onto `systemPromptOptions.skills`.
   - Killing mutations:
     - Skip the `options.skills` filter.
       The "drops a denied skill from the options on every turn" test must go red.
     - Filter by `entries` membership instead of `deniedNames`, which drops skills absent from the rendered prompt (`disableModelInvocation`).
       Add a test with such a skill kept; it must go red.
     - Reintroduce `return { systemPrompt: … }`.
       A `toEqual({})` assertion on the denied-skill case must go red.
6. **`docs(pi-permission-system): record prompt changes through systemPromptOptions`**
   - ADR 0015, the ADR 0014 status, `configuration.md`, the `architecture.md` module tree and prose, and the `package-pi-permission-system` skill lines listed above.
   - Before removing the `configuration.md` "one turn late" bullet, verify its claim against `agent-session.ts` (base `toolSnippets` built from `this._toolRegistry.keys()`).

After step 6, start a fresh Pi session from this worktree with one `codemode` MCP server configured, then count `<mcp_servers>` in the first provider request.
Expect 1.

## Risks and Mitigations

- **Version skew with pi-subagents.**
  A user upgrading this package without a pi-subagents release carrying [#1009] gets children showing two tool lists: the parent's, inherited, and their own.
  Mitigation: ship [#1009] first.
  ADR 0015 and `configuration.md` state the pairing, and the CHANGELOG entry names it.
  Nothing can enforce it, because neither package depends on the other.
- **A child no detector recognizes** gets Pi's native custom-prompt rendering, with no tool prose but the right `tools` array.
  This is unchanged from ADR 0014's [#980] amendment.
- **The non-Pi skill catalogue residual** (see Non-Goals).
  A denied skill an operator listed in their own `SYSTEM.md` stays visible as text.
  It is gated on use.
- **Pi's mutation contract.**
  The design rests on the runner passing one shared options object and re-rendering the getter.
  That is Pi's documented `before_agent_start` contract ("Later handlers observe mutations made by earlier handlers"), and the new upstream-assumption row watches it.
- **[#970]'s reshaping of `tool-surface-prompt.ts`** may move or rename what step 1 builds on.
  Mitigation: re-read the file before step 1; the step's intent (export the bullets as section contents) does not depend on the exact shape.

## Open Questions

- Whether `SkillPromptSection.start`/`end` and `parseAllSkillPromptSections` stay exported after step 5.
  Decide from a grep once the rewrite is gone.
- Whether the `configuration.md` "one turn late" note is truly obsolete on 1.0.
  Step 6 verifies it.

[#180]: https://github.com/gotgenes/pi-packages/issues/180
[#437]: https://github.com/gotgenes/pi-packages/issues/437
[#873]: https://github.com/gotgenes/pi-packages/issues/873
[#890]: https://github.com/gotgenes/pi-packages/issues/890
[#901]: https://github.com/gotgenes/pi-packages/issues/901
[#919]: https://github.com/gotgenes/pi-packages/issues/919
[#932]: https://github.com/gotgenes/pi-packages/issues/932
[#962]: https://github.com/gotgenes/pi-packages/issues/962
[#970]: https://github.com/gotgenes/pi-packages/issues/970
[#980]: https://github.com/gotgenes/pi-packages/issues/980
[#1000]: https://github.com/gotgenes/pi-packages/issues/1000
[#1009]: https://github.com/gotgenes/pi-packages/issues/1009
