---
issue: 1001
issue_title: "pi-permission-system: Pi's native MCP tools (`mcp__<server>__<tool>`) bypass the `mcp` surface"
---

# Route Pi's built-in MCP tools to the `mcp` surface

## Release Recommendation

**Release:** ship independently

The roadmap records this issue as out of scope for Phase 15 (bash token roles and declared effects), so it carries no `Release:` batch tag.
It closes a live policy bypass, and every step below lands in one release.

## Problem Statement

Pi 0.99.0 added built-in MCP support.
`builtin:mcp` registers each server tool as its own tool named `mcp__<server>__<tool>`, and Pi 0.99.2 sanitized that name so every character outside `[A-Za-z0-9_]` becomes `_` (`danger-srv` → `mcp__danger_srv__wipe`).
`classifyToolKind` routes only the literal tool name `mcp` to the `mcp` surface, so these tools resolve as generic extension tools on their own name with the value `*`.

What the operator sees: with `"*": "allow"` and `"mcp": { "danger-srv": "deny" }`, the model calls `mcp__danger_srv__wipe` and the call runs.
The only rule that reaches the tool is a top-level key naming it (`"mcp__danger_srv__wipe": "deny"`, or a surface wildcard such as `"mcp__danger_srv__*"`), and 0.99.2's rename silently broke any such key written against 0.99.0's `-` spelling.
The issue's follow-up comment confirms the bypass on Pi 1.0.0.

## Goals

- A Pi MCP tool call resolves on the `mcp` surface under the same candidate names a proxied call gets (`<server>_<tool>`, `<server>:<tool>`, `<server>`, `<tool>`, `mcp_call`, `mcp`), plus its full Pi name.
- A server rule matches in either spelling: the operator's `mcp.json` spelling (`danger-srv`) and Pi's sanitized spelling (`danger_srv`).
- Configured server names come from Pi's own two files: `~/.pi/agent/mcp.json` and, in a trusted project, `<cwd>/.pi/mcp.json`.
- A Pi MCP tool keeps extension-tool input semantics: `path`/`external_directory` gating reads its top-level `path`, and the ask prompt and review log still show its arguments.
- Session approvals for a Pi MCP tool are recorded on the `mcp` surface, so they match the next call.
- Tool exposure agrees with the gate: a Pi MCP tool is withheld exactly when its `mcp` candidates resolve to `deny`.
- An existing top-level `mcp__…` key keeps applying (relocated onto the `mcp` surface at load) and raises a config-issue notice telling the operator to port it; a migration guide documents the port.
- A forwarded or service `mcp` query evaluates the value it carries rather than the status probe `mcp_status`.
- **Breaking (`fix!:`):** existing `mcp` rules start applying to Pi's MCP tools on upgrade with no config edit.
  With `"*": "ask"` and `"mcp": "allow"`, those tools move from *ask* to *allow*; with an `mcp` server deny, they move from allowed to *deny*.
  A config with no `mcp` rules and no top-level `mcp__…` keys is unaffected, since both paths fall back to `*`.

## Non-Goals

- Remodeling the `mcp` surface (one call identity, the proxy as a registered reader, annotations): [#1014], the follow-up filed for the next improvement phase.
- Routing other registered MCP proxy tools: [#946].
- The first-prompt exposure gap for `direct` servers: [#1002].
  This plan makes exposure *agree* with the gate; it does not change *when* exposure runs.
- Reading MCP `readOnlyHint`/`destructiveHint` annotations as a declared direction: [#952].
- Carrying a forwarded `mcp` ask's full candidate list to the serving node.
  This plan evaluates the single forwarded target as-is, which can only be stricter than the child (see the `### Forwarded and service queries` design section); full fidelity is [#1014].
- Servers registered only through `pi.registerMcpServer()` get Pi's sanitized spelling alone, because no `mcp.json` holds their original name.
- Renaming the `path-values` resolved-intent kind, which this plan reuses for precomputed `mcp` values.
  Its name is pinned by ADR 0002's title and ADR 0008; the rename is recorded under [#1014].
- Changing proxied (`mcp` tool) candidate derivation beyond the server-name list now including the trusted project's `mcp.json`.
- Treating `-` and `_` as equal inside the wildcard matcher.
  Pi forbids two servers that differ only in `-`/`_`, which makes emitting both spellings safe; folding in the matcher would also widen proxied matching, where no such guarantee holds.

## Background

- `src/access-intent/tool-kind.ts` — `ToolKind` (`bash | mcp | skill | path | extension`) and `classifyToolKind`, the single name-based dispatch point; the compiler flags every exhaustive `switch` when a variant is added (#561's design).
  `resolveShellInvocation` is the precedent for gating a tool on a surface other than its name: an aliased shell tool is gated on `bash` while its invoked name is preserved in the prompt and log.
- `src/access-intent/mcp-targets.ts` — `createMcpPermissionTargets(input, configuredServerNames)` derives the proxy's candidate list from `{ tool, server, … }` input, with prefix/suffix server derivation (#928).
- `src/access-intent/input-normalizer.ts` — `normalizeInput` maps `(toolName, input)` to `{ surface, values, resultExtras }`; `buildInputForSurface` / `buildResolvedIntentFromMatchValues` build intents for service queries and forwarded serving.
- `src/policy/permission-manager.ts` — `check`, `getToolPermission`, `isToolFullyDenied`, `resolvePermissions` (merge → `normalizeFlatConfig` → `composeRuleset(defaults, baseline, config)`), `getConfigIssues` (already appends a fail-closed notice), `deriveSource`, `derivePolicyLoaderOptions` (sets project paths only when a cwd is given; `PermissionSession` withholds the cwd for an untrusted project, #644).
- `src/config/policy-loader.ts` — `getConfiguredMcpServerNames` reads `mcpServers` keys from the global `mcp.json` only, longest-first.
- `src/handlers/gates/tool.ts` — `describeToolGate` picks `gateSurface` (`bash` for a shell, else the tool name), which keys session approvals, the decision value, and the forwarded access facts.
- Every evaluation goes through `evaluateAnyValue`: rule position decides and candidate order only picks the reported target.
- Pi side, verified in the checkout and the pinned 1.0.0 tarball: `createMcpToolName` (`extensions/mcp/tools.ts`) builds `mcp__${server}__${tool}` with `[^A-Za-z0-9_]` → `_`, truncating to 64 characters with an 8-hex hash suffix on overflow or collision.
  `mcpNamespace` replaces only `-`, and `extensions/mcp/config.ts` rejects two servers whose namespaces collide.
  Pi reads `~/.pi/agent/mcp.json` and, when trusted, `<cwd>/.pi/mcp.json` (`config.ts` `loadMcpConfig`).
  Codemode scripts call these tools by the same name (`tools.mcp__linear__list_issues(...)`), and each call reaches `tool_call` (`core/nested-tool-calls.ts`).
- AGENTS.md / package skill constraints: least privilege; wildcard matching explicit and tested; config files are the source of truth; `permission-manager.ts` stays string-based (ADR 0002 lint guard); a declared field not read at runtime is a trap.

## Design Overview

### Classification: a sixth tool kind

`classifyToolKind` returns a new variant, `"mcp-tool"`, for a name shaped `mcp__<server>__<tool>` (a named predicate `isPiMcpToolName`: the `mcp__` prefix, then a non-empty server segment, `__`, and a non-empty remainder).
It is not `"mcp"`, because the `mcp` kind encodes the proxy's input shape: `getToolInputPath` reads `input.arguments.path`, and the preview formatter hides the input.
A Pi MCP tool's arguments are its top-level input, so classifying it as `mcp` would drop its `path` gating — a new bypass.
The Tidy-First assessor confirmed the variant over a separate resolver: the discriminator is the name alone, and the variant makes the compiler flag the five exhaustive switches (`normalizeInput`, `getToolInputPath`, `deriveSource`, `deriveDecisionValue`, `deriveSuggestionValue`'s default arm aside).

Per site:

| Site                                      | `mcp-tool` behavior                                                                   |
| ----------------------------------------- | ------------------------------------------------------------------------------------- |
| `normalizeInput`                          | surface `mcp`, values = Pi-name candidates + `"mcp"`, extras `{ target }`             |
| `getToolInputPath`                        | the `extension` arm (`input.path`, extractors)                                        |
| `deriveSource`                            | like `mcp` (`default` on the default layer, else `mcp`)                               |
| `deriveDecisionValue`                     | `check.target ?? toolName`                                                            |
| `describeToolGate`                        | gate surface `mcp` (session approval, decision, forwarded facts)                      |
| `isMcpCheck`                              | true                                                                                  |
| prompt evidence / review-log preview      | decided by `classifyToolKind(check.toolName)`: shown for `mcp-tool`, hidden for `mcp` |
| unregistered-tool hint                    | no proxy hint, like `mcp`                                                             |
| `getToolPermission` / `isToolFullyDenied` | evaluate the `mcp` surface over the candidates                                        |

### Candidate derivation

A new export in `mcp-targets.ts`:

```typescript
/** Candidates for a Pi MCP tool name, most specific first. */
export function createPiMcpToolTargets(
  toolName: string,
  configuredServerNames: readonly string[],
): string[];
```

1. Strip `mcp__`.
2. Server resolution: among configured names, take the longest whose sanitized form `s'` (Pi's `[^A-Za-z0-9_]` → `_`) makes `s'__` a prefix of the remainder.
   Configured names that tie on the same sanitized prefix are all kept (Pi's collision check covers only `-`/`_`).
   With no configured match, split at the first `__`.
3. Tool = the rest after `<server>__`.
4. Spellings = configured name(s) first, then the sanitized segment, deduplicated.
5. For each spelling `s`: `s_tool`, `s:tool`, then `s`; then `tool`, the full Pi name, `mcp_call`.
   `normalizeInput` appends `mcp`.

The emission is written inline with `McpTargetList`, not shared with `pushMcpToolPermissionTargets`: that helper's `resolvedTool.startsWith(server_)` shortcut is a proxy-naming quirk, and a Pi tool named `github_x` on server `github` must still emit `github_github_x` (assessor finding; Metz — duplicating five lines beats a discriminator parameter).

Worked example, `mcp__danger_srv__wipe` with `danger-srv` configured:

```text
danger-srv_wipe, danger-srv:wipe, danger-srv,
danger_srv_wipe, danger_srv:wipe, danger_srv,
wipe, mcp__danger_srv__wipe, mcp_call, mcp
```

### Server names from Pi's two files

`PolicyLoaderOptions` gains `projectMcpConfigPath`; `FilePolicyLoader.getConfiguredMcpServerNames` reads both paths through the existing `getConfiguredMcpServerNamesFromPaths` (one more element in its list, the cache stamp covering both).
`derivePolicyLoaderOptions` sets it to `join(cwd, ".pi", "mcp.json")` only when a cwd is given, so trust gating is inherited.
Proxied calls see the extra names too, which matches pi-mcp-adapter 5.0.0 reading the same files.

### Gate surface

`describeToolGate` computes `gateSurface = shell ? "bash" : kind === "mcp-tool" ? "mcp" : tcc.toolName`.
The pipeline's resolution is unchanged (`{ kind: "tool", surface: tcc.toolName, input }`); the manager normalizes it onto `mcp`.
`check.toolName` stays the Pi name, so the prompt shows it as the tool and `target` as the value.

### Exposure

`PermissionManager.getToolPermission` and `isToolFullyDenied` branch on `mcp-tool`: build the candidates and run `evaluateAnyValue("mcp", candidates, composedRules)`.
Because a Pi MCP tool's candidates do not depend on input, "fully denied" is exactly "the evaluation is `deny`".

### Legacy top-level keys: relocate and notify

After `normalizeFlatConfig` (lifted into `buildConfigRules` by a preparatory step), a pure `relocateMcpToolKeyRules(configRules)` in `policy/normalize.ts`:

- takes every config rule whose surface starts with `mcp__` and whose pattern is `*`, and rewrites it to `{ surface: "mcp", pattern: <old surface>, … }`, keeping `action`, `reason`, `origin`;
- moves the rewritten rules **after** every other config rule, in their original relative order, so they keep winning over `mcp` rules as they effectively did when `mcp` rules never reached these tools;
- leaves other patterns on such a surface untouched (they never matched before — an extension tool's value is always `*` — and stay inert);
- returns the relocated surface keys.

`synthesizeBaseline` reads the pre-relocation rules, so a legacy `allow` key does not newly auto-allow the proxy's discovery targets.
`ResolvedPermissions` gains `legacyMcpToolKeys`; `getConfigIssues` appends one notice naming them and the migration guide, the same way it appends the fail-closed notice.
The notice reaches the operator through the existing `ConfigIssueReporting` path at `session_start` and turn prep.
A wildcard key (`"mcp__danger_srv__*"`) works unchanged after relocation because the full Pi name is a candidate.

### Forwarded and service queries

Measured at planning (disposable Vitest spike through the real `PermissionManager` via `createManagerWithConfig`): with `{"*": "ask", "mcp": {"github": "allow", "danger": "deny"}}` and servers `github`, `danger`, `manager.check(buildResolvedIntentFromMatchValues("mcp", ["danger"], "a"))` returned `allow` with target `mcp_status`.
`buildInputForSurface("mcp", v)` returns `{}`, so the proxy derivation yields the status probe, and the synthesized baseline allows it whenever any `mcp` allow exists.
A forwarded `mcp` ask is therefore auto-approved by the serving node regardless of the child's target.
That is pre-existing for proxied calls; routing Pi MCP tools onto `mcp` would extend it to them, so this plan fixes it.

Fix: a value-bearing `mcp` query evaluates its values as-is — `buildResolvedIntentFromMatchValues` and `buildAccessIntentForSurface` emit the precomputed-values intent (`path-values`) for `mcp`, the same branch path surfaces use, and the manager's existing branch sets `target` from the matched value.
Evaluating only the child's winning target can only be stricter than the child: the serving node's extra rules are session approvals, which grant, so a missed alias misses a grant, never a deny.
A value-less `mcp` service query keeps the `{}` → `mcp_status` behavior.
The `path-values` doc comment is widened to "precomputed match values for a surface".

### Design review

- Repeated discriminators: the `mcp__` shape is tested only by `isPiMcpToolName`, consumed by `classifyToolKind` and `relocateMcpToolKeyRules`; every other site dispatches on the kind.
- `PolicyLoaderOptions` grows by one optional path; no consumer relays it.
- `ResolvedPermissions` grows by one field read only by `getConfigIssues`.
- No new import edges across zones: `policy/normalize.ts` → `access-intent/tool-kind.ts` already exists through `permission-manager.ts`'s zone; verify with `pnpm --silent fallow guard src/policy/normalize.ts` in that step.

## Module-Level Changes

- `src/access-intent/tool-kind.ts` — `ToolKind` gains `"mcp-tool"`; `isPiMcpToolName`; `classifyToolKind` and `isMcpCheck` updated; doc comment lists the variant.
- `src/access-intent/mcp-targets.ts` — `createPiMcpToolTargets`.
- `src/access-intent/input-normalizer.ts` — `normalizeInput` `mcp-tool` arm; `mcp` values intent in `buildResolvedIntentFromMatchValues` / `buildAccessIntentForSurface`.
- `src/access-intent/access-intent.ts` — `path-values` doc comment widened.
- `src/access-intent/tool-input-path.ts` — `mcp-tool` joins the `extension` arm.
- `src/policy/permission-manager.ts` — `buildConfigRules` extraction; relocation call; `legacyMcpToolKeys`; `getConfigIssues` notice; `deriveSource` arm; `getToolPermission` / `isToolFullyDenied` branch; `derivePolicyLoaderOptions` project MCP path.
- `src/policy/normalize.ts` — `relocateMcpToolKeyRules`.
- `src/config/policy-loader.ts` — `projectMcpConfigPath` option and read.
- `src/handlers/gates/tool.ts` — gate surface for `mcp-tool`.
- `src/handlers/gates/helpers.ts` — `deriveDecisionValue` arm.
- `src/presentation/tool-ask-payload.ts`, `src/tool-input/tool-preview-formatter.ts` — input-preview decision by kind.
- `src/presentation/permission-prompts.ts` — hint arm.
- Tests: `test/access-intent/{tool-kind,mcp-targets,input-normalizer,tool-input-path}.test.ts`, `test/policy/{normalize,permission-manager-unified}.test.ts`, `test/config/policy-loader.test.ts`, `test/handlers/gates/{tool,helpers}.test.ts`, `test/presentation/tool-ask-payload.test.ts`, `test/tool-input/tool-preview-formatter.test.ts`, `test/presentation/permission-prompts.test.ts`, `test/authority/forwarded-request-server.test.ts` (or `test/service/permissions-service.test.ts`), `test/composition-root.test.ts`.
- Docs:
  - `docs/configuration.md` — `### mcp Surface` (Pi MCP tools, the candidate table's new rows, both spellings, project `mcp.json`), the surface table row (`mcp` → "MCP calls: Pi's built-in MCP tools and the `mcp` proxy"), and the `Extension-provided tools like … mcp` note.
  - `docs/migration/1001-pi-mcp-tools-on-mcp-surface.md` — new: what changed, the before/after behavior table, how to port a top-level `mcp__…` key, the notice text.
  - `README.md` — the "Gates MCP" feature line names Pi's built-in MCP.
  - `docs/architecture/architecture.md` — `tool-kind.ts`, `mcp-targets.ts`, `policy-loader.ts`, `normalize.ts` module-tree entries and the MCP derivation paragraph (around line 351); this issue's sweep line stays (the roadmap records dispositions, not completion).
  - `.pi/skills/package-pi-permission-system/SKILL.md` — the upstream-assumptions row for `mcp__<server>__<tool>` changes from a coverage gap to the assumption this plan now rests on (the name shape and sanitization in `extensions/mcp/tools.ts`, the two `mcp.json` paths in `extensions/mcp/config.ts`).
- Predicted unchanged: `src/policy/synthesize.ts` (baseline reads pre-relocation rules; no edit), `src/access-intent/path-surfaces.ts` (`effectProvenByTool` keys on names, so `mcp__server__tool` stays "unknown direction", pinned by `path-surfaces.test.ts:161`), `test/presentation/dialog-renderer.test.ts` (its `mcp__github__create_issue` fixture is a hand-built payload), `schemas/permissions.schema.json` (top-level keys stay free-form).

## Test Impact Analysis

1. New tests the change enables: unit tests of `createPiMcpToolTargets` and `relocateMcpToolKeyRules` as pure functions; a composition-root test driving a real config, `mcp.json`, and a `tool_call` for `mcp__danger_srv__wipe` through the real manager.
2. Redundant tests: none; the proxy derivation tests stay as they are.
3. Tests that stay as-is: `mcp-targets.test.ts`'s proxy cases, `synthesize.test.ts`, and the `shell-tool-alias.test.ts` precedent.

Exact-equality assertions on `mcp` check results naming a Pi MCP tool: the only `mcp__` fixtures in `test/` are `path-surfaces.test.ts:161` and `dialog-renderer.test.ts:120–128`, neither produced by the code under change (grep at planning).

## Invariants at risk

- **#928's candidate order** (most specific first; `evaluateAnyValue` decides by rule position): pinned by `mcp-targets.test.ts` and `input-normalizer.test.ts`; proxied derivation is untouched.
- **#574's shell-alias routing:** `gateSurface` gains a second branch; `shell-tool-alias.test.ts` pins the `bash` branch.
- **#815's exposure question** (`isToolFullyDenied` ≠ catch-all): non-MCP tools keep `isSurfaceFullyDenied`; `before-agent-start.test.ts` and `permission-manager-unified.test.ts` pin them.
- **#352 path gating for extension and MCP tools:** `tool-input-path.test.ts` gains an `mcp-tool` case reading top-level `path`.
- **Origin attribution:** relocated rules keep `origin`; `/permission-system show` lists them under `mcp`.
- **ADR 0002 string boundary:** the manager still imports no `AccessPath`; the lint guard covers it.

## TDD Order

1. **`refactor(pi-permission-system): extract config-rule building from resolvePermissions`** Lift the `normalizeFlatConfig(...).map(origin)` block into `buildConfigRules(mergedPermission, origins)`.
   Prepares step 6's relocation call.
   No test change; the full suite stays green.
2. **`refactor(pi-permission-system): classify mcp__<server>__<tool> names as a Pi MCP tool kind`** Add `isPiMcpToolName` and the `"mcp-tool"` variant; every new switch arm behaves exactly like `extension` (`normalizeInput`, `deriveSource`, `deriveDecisionValue`, `getToolInputPath`); `isMcpCheck` untouched.
   Tests (`tool-kind.test.ts`): `mcp__a__b`, `mcp__danger_srv__wipe`, `mcp__a__b__c` → `mcp-tool`; `mcp`, `mcp__`, `mcp__a`, `mcp__a__`, `mcp___b` boundary cases → not.
   `tool-input-path.test.ts`: an `mcp-tool` reads top-level `path` and ignores `arguments.path`.
   Killing mutations: make `isPiMcpToolName` return `name.startsWith("mcp__")` (kills the `mcp__a` / `mcp__a__` cases); move `case "mcp-tool"` to the `mcp` arm in `getToolInputPath` (kills the path test).
3. **`fix(pi-permission-system): read MCP server names from the trusted project's .pi/mcp.json too`** `projectMcpConfigPath` in `PolicyLoaderOptions`, `FilePolicyLoader`, and `derivePolicyLoaderOptions`.
   Tests (`policy-loader.test.ts`): names from both files merged longest-first; a project-only server listed; no project path → global only; editing the project file invalidates the cache.
   `permission-manager-unified.test.ts` (via `createManagerWithProject`, or a loader-level test of `derivePolicyLoaderOptions`): a cwd-less manager reads no project `mcp.json`.
   Killing mutations: drop `projectMcpConfigPath` from the path list (kills the project-only test); omit it from the cache stamp (kills the invalidation test).
4. **`refactor(pi-permission-system): derive permission candidates from a Pi MCP tool name`** `createPiMcpToolTargets` with no consumer yet.
   Tests (`mcp-targets.test.ts`, new `describe("Pi MCP tool names")`): the worked example above in full order (`toEqual`); an unconfigured server → sanitized spelling only; longest configured prefix (`a` and `a_b` configured, `mcp__a_b__x` → server `a_b`); `.`-containing server (`my.srv` → `mcp__my_srv__x`) maps back; a tool named with the server prefix (`mcp__github__github_x`) still emits `github_github_x`; a tool containing `__` (`mcp__srv__get__x` → tool `get__x`); tied configured names both emitted.
   Killing mutations: drop the configured spelling from the spellings list (kills the worked example and `my.srv`); select the first matching configured name instead of the longest (kills `a_b`); split at the last `__` (kills `get__x`); reuse the proxy `startsWith` shortcut (kills `github_x`).
5. **`fix!(pi-permission-system): gate Pi's built-in MCP tools on the mcp surface`** `normalizeInput` `mcp-tool` arm → `{ surface: "mcp", values: [...createPiMcpToolTargets(name, servers), "mcp"], resultExtras: { target } }`; `deriveSource` arm like `mcp`; `isMcpCheck` includes `mcp-tool`; `describeToolGate` gate surface `mcp`; `deriveDecisionValue` arm returns the target; prompt evidence and review-log preview decided by `classifyToolKind(check.toolName)`; unregistered-tool hint arm.
   Tests: `input-normalizer.test.ts` (surface, values, target); `permission-manager-unified.test.ts` — the issue's three policy rows plus `"mcp": {"danger_srv": "deny"}`, `"mcp": {"danger-srv:wipe": "deny"}`, `"*": "ask"` + `"mcp": "allow"` → allow, and no `mcp` rule → `*`; `tool.test.ts` (gate surface, session approval surface `mcp`, decision value = target); `tool-ask-payload.test.ts` and `tool-preview-formatter.test.ts` (Pi MCP args shown and logged, proxy input still hidden); `composition-root.test.ts` — real config + global `mcp.json` with `danger-srv`, `tool_call` `mcp__danger_srv__wipe` blocked; an approve-for-session ask on a second tool lets its next call through.
   Killing mutations: make the `normalizeInput` `mcp-tool` arm return the extension shape (kills the manager rows and composition test); make `gateSurface` ignore `mcp-tool` (kills the session-approval test); make the preview decision use `isMcpCheck` (kills the args-shown tests).
   Commit footer: `BREAKING CHANGE: mcp permission rules now apply to Pi's built-in MCP tools (mcp__<server>__<tool>). A config with "mcp": "allow" or an mcp server rule changes how those tools resolve; see docs/migration/1001-pi-mcp-tools-on-mcp-surface.md.`
6. **`fix(pi-permission-system): keep top-level mcp__ tool keys working and ask the operator to port them`** `relocateMcpToolKeyRules` in `normalize.ts`; called on `buildConfigRules`' output in `resolvePermissions`; baseline from the pre-relocation rules; `legacyMcpToolKeys` on `ResolvedPermissions`; the `getConfigIssues` notice.
   Tests (`normalize.test.ts`): `*` rule relocated with origin/reason kept; moved after an `mcp` catch-all regardless of key order; non-`*` patterns untouched; non-`mcp__` surfaces untouched; returned keys.
   `permission-manager-unified.test.ts`: `"*": "allow"`, `"mcp": {"*": "allow"}`, `"mcp__danger_srv__wipe": "deny"` → deny for the Pi tool whatever the key order; `"mcp__danger_srv__*": "deny"` → deny; a legacy `allow` key does not allow `mcp_status`; `getConfigIssues` names the key and the guide; no legacy key → no notice.
   Killing mutations: relocate in place instead of appending (kills the key-order test); synthesize the baseline from post-relocation rules (kills the `mcp_status` test); drop the notice push (kills the issue test).
7. **`fix(pi-permission-system): withhold a Pi MCP tool exactly when its mcp rules deny it`** `getToolPermission` and `isToolFullyDenied` branch on `mcp-tool`.
   Tests (`permission-manager-unified.test.ts`, `before-agent-start.test.ts`): `"mcp": {"danger-srv": "deny"}` withholds `mcp__danger_srv__wipe` and not `mcp__other__x`; a relocated legacy deny withholds; `"mcp": {"*": "deny", "danger-srv": "allow"}` keeps `mcp__danger_srv__wipe`.
   Killing mutation: remove the `mcp-tool` branch from `isToolFullyDenied` (kills the server-deny test).
8. **`fix(pi-permission-system): resolve a forwarded or queried mcp target as itself, not as a status probe`** `buildResolvedIntentFromMatchValues` and `buildAccessIntentForSurface` emit the precomputed-values intent for a value-bearing `mcp` query; widen the `path-values` doc comment.
   Tests: the planning spike as a regression (`input-normalizer.test.ts` or `permission-manager-unified.test.ts`): `["danger"]` → `deny`, target `danger`; `["danger:wipe"]` with only a server rule → `deny`; a value-less `mcp` service query still resolves `mcp_status`; `forwarded-request-server.test.ts` or `permissions-service.test.ts` through the composition-root `ServingPolicy`: a forwarded `mcp` ask on a denied server is auto-denied, not auto-approved.
   Killing mutation: restore `buildInputForSurface`'s `{}` for `mcp` (kills the `danger` → `deny` test).
9. **`docs(pi-permission-system): document Pi MCP tools on the mcp surface and the top-level key port`** The doc list under Module-Level Changes, including the migration guide.
   Dry-run every JSON example in the guide through `createManagerWithConfig` in a disposable spike (not committed) and record each expected decision in the guide's table.

## Risks and Mitigations

- **Silent loosening on upgrade** (`"mcp": "allow"` now reaches Pi tools): breaking classification, footer, migration table, and changelog entry.
- **A relocated legacy `allow` loosening proxy discovery:** the baseline is synthesized pre-relocation (step 6 test).
- **Mis-split server names containing `__`** when unconfigured: configured names resolve first; the fallback is documented, and the full Pi name is always a candidate.
- **Hash-suffixed Pi tool names** (over 64 characters or a sanitized collision): the tool segment carries the suffix, so a tool-level rule may miss while server rules and the full name still match; documented in `configuration.md`.
- **Tied sanitized prefixes** (`a.b` and `a_b` both configured): both spellings are emitted, so a rule on either applies; documented as a known edge.
- **Pi renames the tools again:** `isPiMcpToolName` and `createPiMcpToolTargets` are the only readers, and the upstream-assumptions row names the Pi file to re-check.
- **Notice fatigue:** one notice per session start listing all legacy keys, not one per key.

## Open Questions

- When the relocation of top-level `mcp__…` keys is removed (turning the notice into a config error): decided under [#1014].

[#946]: https://github.com/gotgenes/pi-packages/issues/946
[#952]: https://github.com/gotgenes/pi-packages/issues/952
[#1002]: https://github.com/gotgenes/pi-packages/issues/1002
[#1014]: https://github.com/gotgenes/pi-packages/issues/1014
