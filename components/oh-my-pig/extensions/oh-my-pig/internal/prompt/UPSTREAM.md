# internal/prompt — Upstream Mapping

Upstream repository: `oh-my-pi` (MIT, commit `4df653426f40ee1eb78ef408528eb9e6a5275b9d`).

## Upstream Files Followed

- `packages/coding-agent/src/prompts/system/system-prompt.md` — static system prompt sections and tool-gated policy rules (`piglets/oh-my-pig/prompts/system.md` and embedded `system-prompt.md`).
- `packages/coding-agent/src/prompts/system/project-prompt.md` — `<project-context>`, `<workstation>`, `<repo-rules>`, `<active-repo-context>`, trailing `<critical>`, and append prompt (`project-prompt.md`).
- `packages/coding-agent/src/prompts/system/active-repo-context.md` — single direct-child git repo note (`active-repo-context.md`).
- `packages/coding-agent/src/prompts/system/date-cwd-reminder.md` — per-turn local date & cwd `<system-reminder>` (`date-cwd-reminder.md`).
- `packages/coding-agent/src/prompts/system/personalities/{default,friendly,pragmatic}.md` — bundled personality presets (`personalities/`).
- `packages/coding-agent/src/system-prompt.ts` — prompt assembly, `PERSONALITY.md` override, `dedupeContainedContextFiles`, `dedupeAlwaysApplyRules`, workstation OS/Arch formatting, skill-reader gating.
- `packages/coding-agent/src/session/date-cwd-reminder.ts` — `DateCwdReminderInjector` (keeps system prompt byte-stable; attaches `<system-reminder>` to first user turn and new user turns on date/cwd change).
- `packages/coding-agent/src/utils/active-repo-context.ts` — single direct-child git repo detection.
- `packages/coding-agent/src/utils/prompt-path.ts` — slash normalization (`normalizePromptPath`).
- `packages/coding-agent/src/discovery/at-imports.ts` — `@path/to/file` import expansion inside context files.
- `packages/coding-agent/src/discovery/helpers.ts` & `builtin.ts` — standalone `AGENTS.md`/`CLAUDE.md` walk-up with home-ceiling rules and sticky `RULES.md` discovery.
- `packages/coding-agent/src/extensibility/skill-descriptions.ts` — `previewSkillDescription` (bounded 100-rune routing preview).
- `packages/utils/src/prompt.ts` — `format()` post-render normalizer (blank-line collapsing, closing-tag blank-line stripping, table compaction, fenced-block preservation).

## Host Ownership (No Double Injection)

- **Context files (`AGENTS.md` / `CLAUDE.md`) and Skills**: PiG discovers both at startup and on `/reload` (`coding/cli/cli_runtime_build.go`) and delivers them to extensions in `before_agent_start` under `data["systemPromptOptions"]["contextFiles"]` and `data["systemPromptOptions"]["skills"]`. This package consumes those host-provided lists (applying omp's `@`-import expansion, containment deduplication, and `read`-tool skill gating) and returns `{"systemPrompt": rendered}` to force the complete prompt, which suppresses PiG's built-in `<project_context>` and `<skills>` XML wrappers. Neither context files nor skills are discovered or injected twice. `DiscoverContextFiles` is also implemented for standalone callers / tests that omit `ContextFiles`.
- **System prompt mechanism**: `piglets/oh-my-pig/piglet.yaml` sets `systemPrompt: { file: ./prompts/system.md }` containing omp's static text with `{{slot}}` placeholders; `before_agent_start` fills the slots from the session's live active tools (`ctx.GetActiveTools()`), model (`ctx.ModelQualified()`), workstation info, context files, skills, and sticky `RULES.md`, and returns `{"systemPrompt": rendered}` (which PiG applies as `forceSystemPrompt` for the run, bypassing PiG's `<tools>`/`<rules>`/`<docs>`/`<cwd>` wrappers). If the user passes `--system-prompt` or a custom `SYSTEM.md` without placeholders, the hook returns `(nil, nil)` and leaves the user's prompt untouched.

## Tests Ported

24 test cases total across `prompt_test.go` (20 ported from upstream + 4 oh-my-pig contract tests):
- `system-prompt-kernel.test.ts` (1 case): OS from platform + release, never `unknown`.
- `system-prompt-model.test.ts` (2 cases): model identifier present when set, omitted when empty.
- `system-prompt-personality.test.ts` (4 cases): default preset, `PERSONALITY.md` override, `none` omits even with file, empty file falls back to preset.
- `system-prompt-context-dedup.test.ts` (11 cases): all 11 containment/dedup/depth/fenced-block cases.
- `system-prompt-dedup.test.ts` (4 cases): date/cwd excluded from system prompt footer, active child repo context, discovered context dedup keeps closest copy, always-apply rule deduped against expanded context imports.
- `system-prompt-inventory.test.ts` (1 case): skills gated on active `read` tool and `Hide`.
- `date-cwd-reminder.test.ts` (4 cases): first-turn injection without mutating input, prepending before image blocks, no-op without user message, byte-stable prior turns when date/cwd changes.
- Additional contract tests: golden assembled prompt (`TestGoldenAssembledPrompt`), unregistered tools never named (`TestUnregisteredToolsNotNamed`), `piglets/oh-my-pig/prompts/system.md` matches embedded copy (`TestPigletSystemPromptMatchesEmbedded`), `PreviewSkillDescription` truncation, full wire-protocol `Register` end-to-end test (`TestRegisterEndToEnd`).

## Changed on purpose

- The role line reads "You are oh-my-pig's trusted coding assistant." where omp's reads "You are omp's trusted coding
  assistant.": oh-my-pig is an homage and does not tell the model, or through it the user, that it is omp.

## Not Ported

| Upstream feature | Why omitted |
|---|---|
| Visuals guidance (`renderMermaid`, `renderSvg`, `autoGraph`) and emoji `reactions` | oh-my-pig's Tern frontend does not render inline SVG images, auto-graphed Markdown tables, or opening-emoji reaction badges, and `pig-tern` has no Mermaid renderer. |
| `# Internal URLs` scheme registry (`skill://`, `rule://`, `memory://`, `agent://`, `history://`, `artifact://`, `local://`, `proc://`, `cfg://`, `ssh://`, `security://`, `vault://`, `issue://`, `pr://`, `mcp://`, `omp://`, `xd://`) | oh-my-pig's `internal/files` `read` tool reads local filesystem paths and does not implement omp's `InternalUrlRouter`. The prompt lists each skill's `SKILL.md` path instead of `skill://<name>` when a file path is present, and omits `# Internal URLs` unless `BuildOptions.InternalURLs` is explicitly supplied. |
| `# xd:// Tool Devices` (`xdevTools`, `xdevDocs`, `writeTransportOnly`) and Auto-QA `xd://report_issue` | oh-my-pig has no `xd://` virtual device transport or automated QA grievance sink. |
| Inline non-native tool catalog (`inlineToolDescriptors`, `nativeTools: false`, Harmony `namespace functions { … }`) | PiG uses native provider tool calling for all models; only the compact `# Tool Inventory` list mode is rendered. |
| Tool wire-name remapping (`customWireName`) and per-tool `readsSkillUris` capability flag | PiG's `sdk.Context.GetActiveTools()` returns tool names only (not labels, wire names, or custom capability flags). `BuildOptions.ToolLabels` supports labels when known; live hook uses tool names directly. |
| Intent tracing (`intentField` / `tools.intentTracing`) | oh-my-pig tool schemas do not inject an `_i` intent-tracing field. |
| Secret obfuscation notice (`secretsEnabled`, `$$HASH$$` tokens) | oh-my-pig has no output secret obfuscator. |
| Workspace directory tree (`includeWorkspaceTree`, `<workspace-tree>`, `<dir-context>` nested `AGENTS.md` scan) | Defaults to `false` in omp; requires omp's native walker / tree builder not ported in wave 1. |
| Additional workspace roots (`additionalWorkspaceRoots`, `/add-dir`, `/remove-dir`, `/dirs`) | PiG sessions have a single `cwd` and no multi-root directory management commands. |
| Subagent prompt mode (`subagent: true`, `# 5. Hand-off`, `delegationBias`, `eagerTasks`, `eagerTasksAlways`, `taskBatch`, `taskMaxConcurrency`, `taskIrcEnabled`, `scoutAvailable`) | Wave 1 has no `task` subagent runner or model-catalog delegation-bias axis; when a `task` tool is registered at runtime, the default eager-batch `# Delegation` guidance is rendered. |
| Eval preludes (`browserEnabled`, `computerEnabled`, appended prelude guidance blocks) | oh-my-pig has no `eval` tool or browser/computer preludes. |
| Custom Handlebars template overrides (`SYSTEM_TEMPLATE.md`, `--system-prompt-template`, `custom-system-prompt.md`) | PiG handles `SYSTEM.md` and `--system-prompt` natively (which our `before_agent_start` hook respects by passing through any custom prompt lacking our slot markers). Full Handlebars template evaluation is not ported. |
| SQLite-backed LLM skill description compression (`SkillDescriptionStore`, `createSkillDescriptionCompressor`) | Uses omp's deterministic `PreviewSkillDescription` (100-rune bounded preview) without background LLM compression or SQLite cache. |
| Full multi-provider rulebook discovery (`.cursor/rules`, `.windsurf`, `.cline`, TTSR rule bucketing, `rule://` protocol) | Sticky `RULES.md` (`<agentDir>/RULES.md` and nearest `.omp/RULES.md`) is ported; third-party rule providers and TTSR streaming rule interruption are omitted. |
| Mid-turn `developer` control message in `DateCwdReminderInjector` when date/cwd changes with no new user turn | PiG's `EventContext` message schema does not expose a synthetic `role: "developer"` message type; date/cwd updates attach on the next user turn instead. |
| Multi-block `systemPrompt: string[]` return for Anthropic head cache breakpoint splitting | PiG's `before_agent_start` `systemPrompt` override accepts a single `string`, not `[]string`. The two blocks (`system-prompt.md` and `project-prompt.md`) are joined with `\n\n`. |
| `NULL_PROMPT=true` env escape hatch | Test-only internal omp harness toggle; not needed in oh-my-pig. |
