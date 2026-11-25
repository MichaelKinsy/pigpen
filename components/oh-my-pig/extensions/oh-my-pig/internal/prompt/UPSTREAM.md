# internal/prompt — Upstream Mapping

Upstream repository: `oh-my-pi` (MIT, tag `v19.1.1`, commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`).

## Upstream Files Followed

- `packages/coding-agent/src/prompts/system/system-prompt.md` — static system prompt sections and tool/feature-gated policy rules (`piglets/oh-my-pig/prompts/system.md` and embedded `system-prompt.md`).
- `packages/coding-agent/src/prompts/system/project-prompt.md` — `<project-context>`, `<workstation>`, `<repo-rules>`, `<active-repo-context>`, trailing `<critical>`, and append prompt (`project-prompt.md`).
- `packages/coding-agent/src/prompts/system/active-repo-context.md` — single direct-child git repo note (`active-repo-context.md`).
- `packages/coding-agent/src/prompts/system/date-cwd-reminder.md` — per-turn local date & cwd `<system-reminder>` (`date-cwd-reminder.md`).
- `packages/coding-agent/src/prompts/system/personalities/{default,friendly,pragmatic}.md` — bundled personality presets (`personalities/`).
- `packages/coding-agent/src/system-prompt.ts` — prompt assembly, `PERSONALITY.md` override, `dedupeContainedContextFiles`, `dedupeAlwaysApplyRules`, workstation OS/Arch formatting, skill-reader gating, `urls.Describe(host)` / `urls.Reference()` integration, `eval.EnabledPreludes(ctx)` raw guidance blocks, `quotePosixArgument` for `ovnfs`, `closeCodeFence` on context files.
- `packages/coding-agent/src/session/date-cwd-reminder.ts` — `DateCwdReminderInjector` (keeps system prompt byte-stable; per-root LRU state up to 8 roots, ordinal turn keys, sent-turn tracking, compaction/branch preservation, synthetic user turn prepend when only `compactionSummary`/`branchSummary`/`custom` is present).
- `packages/coding-agent/src/utils/active-repo-context.ts` — single direct-child git repo detection.
- `packages/coding-agent/src/utils/prompt-path.ts` — slash normalization (`normalizePromptPath`).
- `packages/coding-agent/src/utils/shell-quote.ts` — `quotePosixArgument` for `ovnfs`.
- `packages/coding-agent/src/discovery/at-imports.ts` — `@path/to/file` import expansion inside context files (per-chain cycle detection, repeated includes expanded, shared `MAX_AT_IMPORT_EXPANSIONS = 256` budget).
- `packages/coding-agent/src/discovery/helpers.ts`, `builtin.ts`, `agents.ts`, `builtin-defaults.ts`, `builtin-rules/*.md` & `capability/rule-buckets.ts` — standalone `AGENTS.md`/`CLAUDE.md` walk-up with home-ceiling rules, sticky `RULES.md` discovery, `.agents/rules` + `.omp/rules` + `.pig/rules` + user rules + 26 embedded bundled default rules, `ttsr.builtinRules` / `ttsr.disabledRules` / `PI_NO_RULES=1` filtering, `<domain-rules>` / `<generic-rules>` bucketing, and `urls.SetRulesProvider` wiring for `rule://<name>`.
- `packages/coding-agent/src/extensibility/skill-descriptions.ts` — `previewSkillDescription` (bounded 100-rune routing preview).
- `packages/utils/src/prompt.ts` — `format()` post-render normalizer (ECMA/Unicode whitespace trimming, blank-line collapsing, closing-tag blank-line stripping, table compaction, fenced-block preservation) and `closeCodeFence()`.
- `packages/utils/src/dirs.ts` — `standardizeMacOSPath` (`StandardizePromptCwd`: strips macOS `/private` prefix when both spellings resolve to the same real path).

## Oracle Test Tooling

`testdata/oracle/` contains the bun oracle (`oracle.ts` and `generate.ts`) and captured real `omp` 19.1.1 prompt blocks (`omp_block0..4.txt`, `omp_ui_block0.txt`). `oracle.ts` imports `packages/utils/src/prompt.ts` from the omp source at the tag (`$OMP_SRC`) to render omp's own templates and generate `testdata/golden_prompt.txt`, `testdata/golden_prompt_full.txt`, `testdata/golden_prompt_toolcmd.txt`, and `testdata/oracle/fixtures.json` (49 branch-covering option sets). `go test` reads only the stored fixtures and needs no bun runtime.

## Host Ownership (No Double Injection)

- **Context files (`AGENTS.md` / `CLAUDE.md`) and Skills**: PiG discovers both at startup and on `/reload` and delivers them to extensions in `before_agent_start` under `data["systemPromptOptions"]["contextFiles"]` and `data["systemPromptOptions"]["skills"]`. This package consumes those host-provided lists (applying omp's `@`-import expansion, containment deduplication, unclosed-fence closing, and `read`-tool skill gating) and returns `{"systemPrompt": rendered}` to force the complete prompt, which suppresses PiG's built-in `<project_context>` and `<skills>` XML wrappers.
- **Internal URLs**: Populated dynamically from `urls.Describe(host)` (and `urls.Reference()` when `read` is not active). The `# Internal URLs` heading and lead sentence render unconditionally, matching `system-prompt.md:67-76`.
- **Eval Preludes**: Populated dynamically from `eval.EnabledPreludes(ctx)` (`p.GuidanceText(ctx)` blocks appended raw as separate blocks between `⁂ Critical` and `<project-context>`; `browser` and `computer` prelude presence also gates `# 2. Verify` UI bullets).
- **Tool Commands**: Populated dynamically from `prompt.ToolCommands(ctx)` (nil by default; integrator wires `internal/toolcmd` docs). Active tools from `ctx.GetActiveTools()` are filtered through `toolcmd.AdvertisedTools(ctx, active)` so mounted and special commands that remain in PiG's active list for direct-call fallback stay out of `# Tool Inventory`; mounted command names still satisfy `{{#has tools ...}}` gates (e.g. `lsp`). Without the bash transport wired, output equals omp with `tools.commands: false`.
- **System prompt mechanism**: `piglets/oh-my-pig/piglet.yaml` sets `systemPrompt: { file: ./prompts/system.md }` containing omp's static text with `{{slot}}` placeholders; `before_agent_start` fills the slots from the session's live active tools (`ctx.GetActiveTools()`), model (`ctx.ModelQualified()`), workstation info, context files, skills, sticky `RULES.md`, registered URL handlers, enabled eval preludes, and mounted tool commands. If the user passes `--system-prompt` or a custom `SYSTEM.md` without placeholders, the hook returns `(nil, nil)` and leaves the user's prompt untouched.

## Tests Ported

30 top-level test functions (89 subtests total) across `prompt_test.go`:
- `TestPigletSystemPromptMatchesEmbedded` (1): `piglets/oh-my-pig/prompts/system.md` matches embedded `system-prompt.md`.
- `TestGoldenWave1Tools` (1), `TestGoldenDefaultToolsWithURLAndPrelude` (1), `TestGoldenToolCommandsDefaultSession` (1): byte-identical goldens generated from the omp `v19.1.1` bun oracle.
- `TestOracleFixtureParity` (49 subtests): byte-identical comparison (including blank lines) against stored omp `v19.1.1` oracle outputs covering every template branch.
- `TestUnregisteredToolsNotNamed` (1): unregistered tools omitted from tool-gated sections.
- `TestWorkstationOSFromPlatformAndRelease` (1): `system-prompt-kernel.test.ts`.
- `TestModelInWorkstationBlock` (1): `system-prompt-model.test.ts` (2 cases).
- `TestKeepsDateAndCwdOutOfSystemPrompt` (1), `TestActiveChildRepoContext` (1): `system-prompt-dedup.test.ts`.
- `TestPersonalityOverride` (4 subtests): `system-prompt-personality.test.ts`.
- `TestDedupeContainedContextFiles` (11 subtests): `system-prompt-context-dedup.test.ts` (all 11 cases).
- `TestDiscoverContextFilesAndAtImports` (1): `system-prompt-dedup.test.ts` (2 cases).
- `TestAtImportsUpstreamCases` (3 subtests): `discovery/at-imports.test.ts` (repeated include, diamond include, 256 expansion budget).
- `TestFormatUnicodeAndCloseCodeFence` (1): `packages/utils/src/prompt.ts` Unicode whitespace trimming and `closeCodeFence`.
- `TestSystemPromptInventoryUpstreamCases` (5 subtests): `system-prompt-inventory.test.ts` (mounted-only bare name, mounted-only omitted from inventory, delegation bias exclusivity, prelude guidance block, `read`-gated URL reference).
- `TestDateCwdReminderInjector` (11 subtests): `date-cwd-reminder.test.ts` (4 core + mid-loop rollover delivery, never rewrite sent turn, post-compaction `compactionSummary` prepend, same-ms/untimestamped ordinal keys, in-place content rewrite, interleaved per-root isolation, branch-back re-send).
- `TestSkillsHiddenAndReaderGated` (1): `system-prompt-inventory.test.ts`.
- `TestPreviewSkillDescription` (1): bounded 100-rune skill description preview.
- `TestMultipleSessionsAndDoubleRegister` (1): multi-session isolation across concurrent `Register` instances and `session_shutdown`.
- `TestRegisterEndToEnd` (1): full PiG SDK wire-protocol test.
- `TestSubagentSessionHook` (1): `SubagentSession` hook switches to `# 2. Hand-off` and omits `# Personality`.
- `TestIntentHookAndEnvOverride` (1): `Intent` hook gates intent line + blank line and honours `PI_INTENT_PROMPT`.
- `TestToolCommandsRunsBeforeActiveTools` (1): `ToolCommands` runs before `GetActiveTools` so synced active tools land in `# Tool Inventory` and gates.
- `TestSchemeHostHooksCfgAndOut` (1): `SettingsApproval` and `OutReplies` hooks default false and feed `urls.SchemeHost` to advertise `cfg://` and `out://`.
- `TestTaskIRCClauseDefaultAndHook` (1): `B asks A via write agent://<id>` clause renders by default when `task` is active and obeys `TaskIRC`.
- `TestToolInventoryPreservesActiveToolOrder` (1): `# Tool Inventory` preserves host `GetActiveTools()` order without re-sorting.
- `TestStandardizePromptCwd` (1): macOS `/private` stripping in `StandardizePromptCwd` and `RenderDateCwdReminder`.
- `TestToolInventoryFiltersMountedAndSpecialActiveTools` (1): mounted (`lsp`, `debug`, `ast_edit`) and special (`resolve`, `reject`, `propose`) tools kept in `GetActiveTools()` for fallback dispatch are excluded from `# Tool Inventory` via `toolcmd.AdvertisedTools`, while `# Tool Commands` and mounted-tool policy gates remain intact.
- `TestEvalPreludeGuidanceForSession` (1): `eval.Prelude.GuidanceText(ctx)` delivers session-specific `GuidanceFor(ctx)` text to each session.

## Settings Read

| Upstream setting / input | Upstream default at `v19.1.1` | Status in `internal/prompt` |
|---|---|---|
| `personality` (`system-prompt.ts:764`) | `"default"` | Builder option exists; hook uses `PersonalityNone` when `SubagentSession(ctx)` is true and `PersonalityDefault` (+ `<agentDir>/PERSONALITY.md`) otherwise |
| `includeModelInPrompt` (`system-prompt.ts:763`) | `true` | Builder honours `Model != ""`; hook passes `ctx.ModelQualified()` |
| `encourageEdits` (`system-prompt.ts:771`) | `true` | Builder option exists (default `true`); not wired to settings |
| `delegationBias` (model catalog `task/prompt-policy.ts:8`, not a user setting) | `"balanced"` | Builder option exists (default `"balanced"`); not derived from model catalog |
| `task.eager` (`system-prompt.ts:114`) | `false` | Builder option exists (`EagerTasks`, `EagerTasksAlways`); not wired to settings |
| `task.batch` (`system-prompt.ts:115`) | `true` | Builder option exists (default `true`); not wired to settings |
| `tui.renderMermaid` (`modes/settings.ts:448`) | `true` | Builder option exists (default `false`); not wired (`pig-tern` has no Mermaid renderer) |
| `tui.renderSvg` (`modes/settings.ts:460`) | `true` | Builder option exists (default `false`); not wired (`pig-tern` has no inline SVG renderer) |
| `tui.renderEmbeds` (`modes/settings.ts:473`) | `true` | Builder option exists (default `false`); not wired (`pig-tern` has no embed renderer) |
| `tui.autoGraph` (`modes/settings.ts:486`) | `"auto"` | Builder option exists (default `false`); not wired (`pig-tern` has no auto-graph renderer) |
| `tui.reactions` (`modes/settings.ts:519`) | `true` | Builder option exists (default `false`); not wired (`pig-tern` has no reaction badge) |
| `tools.intentTracing` (`tools/settings.ts:993`, `sdk.ts:497,4655`) | `true` (on in interactive TUI) | Wired via `prompt.Intent` hook (follows `tools.intentTracing` / interactive host only, NOT `PI_NO_INTENT`); `PI_INTENT_PROMPT` env override honoured (`system-prompt.ts:1138-1140`) |
| `out.enabled` / `tuiTranscript` (`out-protocol.ts:45-47`, `sdk.ts:4674`) | `true` in interactive TUI | Wired via `prompt.OutReplies` hook (nil/false until integrator assigns) feeding `urls.SchemeHost.OutReplies` |
| `settingsApproval` (`cfg-protocol.ts:352-354`, `sdk.ts:4673`, `main.ts:2445`) | `true` in interactive TUI | Wired via `prompt.SettingsApproval` hook (nil/false until integrator assigns) feeding `urls.SchemeHost.SettingsApproval` |
| `task.maxRecursionDepth` / `isIrcEnabled` (`irc/messaging.ts:16-20`, `sdk.ts:4664`) | `2` (`isIrcEnabled = true`) | Defaults to `true` when `task` is active; overridable via `prompt.TaskIRC` hook |
| `dev.autoqa` (`tools/settings.ts:1184`) | `true` | Builder option exists (`AutoQAEnabled`); not wired (`report_issue` tool/device not ported) |
| `tools.commands` / `tools.commandDocs` (`tools/settings.ts:1122-1139`) | `true` / `"catalog"` | Wired via `prompt.ToolCommands` hook (nil until integrator assigns `internal/toolcmd`) |
| `secrets.enabled` (`system-prompt.ts:122`) | `false` | Builder option exists; not wired (no output secret obfuscator) |
| `ovnfs.path` (`system-prompt.ts:117`) | `""` | Builder option exists (with POSIX single-quoting); not wired |

## Changed on Purpose

- The role line reads "You are oh-my-pig's trusted coding assistant." where omp's reads "You are omp's trusted coding assistant.": oh-my-pig is an homage and does not tell the model, or through it the user, that it is omp.

## Not Ported

| Upstream feature | Why omitted |
|---|---|
| Inline non-native tool catalog (`inlineToolDescriptors`, `nativeTools: false`, Harmony `namespace functions { … }`) | PiG uses native provider tool calling for all models; only the compact `# Tool Inventory` list mode is rendered. |
| Tool wire-name remapping (`customWireName`) and per-tool `readsSkillUris` capability flag | PiG's `sdk.Context.GetActiveTools()` returns tool names only (not labels, wire names, or custom capability flags). |
| Subagent extra prompt rules (`excludeAgentsMd`, appended `subagent-system-prompt.md`) | `prompt.SubagentSession` sets `Subagent: true` and `PersonalityNone` (`# 2. Hand-off`, no `# Personality`), while `excludeAgentsMd` filtering and `subagent-system-prompt.md` appending remain owned by `internal/agents`. |
| Default-on interactive blocks in live hook (`dev.autoqa`, `tui.reactions`) | `report_issue` QA reporting is not ported and `pig-tern` has no emoji reaction badges. |
| `commit` and `dsm` eval prelude guidance blocks | Neither the `commit` nor the `dsm` eval prelude is ported in `internal/eval`; `internal/prompt` renders whatever `eval.EnabledPreludes(ctx)` returns, so those guidance blocks are absent until those preludes exist. |
| Workspace directory tree (`includeWorkspaceTree`, `<workspace-tree>`, `<dir-context>` nested `AGENTS.md` scan) | Defaults to `false` in omp; requires omp's native walker / tree builder. |
| Additional workspace roots (`additionalWorkspaceRoots`, `/add-dir`, `/remove-dir`, `/dirs`) | PiG sessions have a single `cwd` and no multi-root directory management commands. |
| Custom Handlebars template overrides (`SYSTEM_TEMPLATE.md`, `--system-prompt-template`, `modelSystemPromptTemplates`, `custom-system-prompt.md`) | PiG handles `SYSTEM.md` and `--system-prompt` natively (which our `before_agent_start` hook respects by passing through any custom prompt lacking our slot markers). Full Handlebars template evaluation is not ported. |
| SQLite-backed LLM skill description compression (`SkillDescriptionStore`, `createSkillDescriptionCompressor`) | Uses omp's deterministic `PreviewSkillDescription` (100-rune bounded preview) without background LLM compression or SQLite cache. |
| Third-party rule providers & TTSR streaming interruption (`.cursor/rules`, `.windsurf`, `.cline`, live TTSR trigger matching) | Sticky `RULES.md`, `.agents/rules` / `.agent/rules` / `.omp/rules` / `.pig/rules`, user `rules/`, and omp's 26 bundled default rules are ported (with `<domain-rules>`, `<generic-rules>`, and `rule://<name>` resolution); third-party IDE rule providers and TTSR streaming rule interruption are omitted. |
| Session-journaled `DateCwdReminderJournal`, per-call context message markers, `systemPrompt` guard, and mid-turn `developer` control messages | PiG's `EventContext` carries only `messages` (no `systemPrompt`, no per-call marker symbol, no `role: "developer"` message type, and no session branch-entry journal API). Mid-loop rollovers are held until the next unsent user turn rather than dropped or retroactively applied. |
| Mid-run prompt rebuild on tool-set change | PiG invokes `before_agent_start` once at run start; extensions have no mid-run system-prompt rebuild hook. |
| Multi-block `systemPrompt: string[]` return for Anthropic head cache breakpoint splitting | PiG's `before_agent_start` `systemPrompt` override accepts a single `string`, not `[]string`. The static prompt, raw eval prelude guidance blocks, and `project-prompt.md` are joined with `\n\n`. |
| `NULL_PROMPT=true` env escape hatch | Test-only internal omp harness toggle; not needed in oh-my-pig. |
