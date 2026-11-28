# Upstream reference: `internal/approval`

Port of omp's unified tool approval engine for every tool (`tools.approvalMode`, `tools.approval.<tool>`, `tools.restrictedFolders`, `always-ask-cached` session-scoped target/folder grants, per-tool capability tiers, dynamic URL/action/pattern/interceptor hooks, `denyError` texts, headless refusal, interactive `ctx.Select` prompts, eval prelude gating via `CheckPrelude`, session-scoped grants released through `internal/lifecycle`, and subagent `yolo` inheritance with user-policy & restriction enforcement) at omp `v19.1.1` (`31ffee513f`).

## Upstream files followed

- `packages/coding-agent/src/tools/approval.ts` (`resolveApproval` / `resolvePolicy` :159-574, `sessionApprovalScope` :583-589, `denyError` :594-603, `formatPayloadValue` / `formatApprovalPrompt` :635-841, `requestApproval` select fallback :900-938)
- `packages/coding-agent/src/tools/approval-targets.ts` (`approvalTargetKey`, `approvalTreeSearchKey`, `isApprovalPublicUrl`, `approvalTargetNamesFile`, `approvalFolderListing`)
- `packages/coding-agent/src/tools/approval-cache.ts` (`approvalGrantKey`, `approvalCacheKeyForPath`, `approvalDirectoryKey`)
- `packages/coding-agent/src/tools/search-approval-targets.ts` (`searchApprovalCacheTargets`, `approvalRootCandidates`)
- `packages/coding-agent/src/extensibility/extensions/wrapper.ts` (approval gate :280-532, headless restriction & prompt refusals :476-490, user denial :523)
- `packages/coding-agent/src/eval/preludes.ts` (`approvePreludeInvocation` :149-187, ported as `CheckPrelude` and called by `internal/eval/bridge.go`)
- `packages/coding-agent/src/mcp/tool-bridge.ts` (`MCPTool.approval = "write"` :811, `formatApprovalArguments`)
- `packages/coding-agent/src/task/executor.ts` (subagent `tools.approvalMode: "yolo"` with user `tools.approval` & `tools.restrictedFolders` still enforced :1298-1301)
- `packages/coding-agent/src/internal-urls/router.ts` (`writeTier` :414-420, `readTier` :423-432)
- `packages/coding-agent/src/internal-urls/out-protocol.ts` (`replyTier` :58-65)
- `packages/coding-agent/src/tools/path-utils.ts` (`searchPathTier` :1518-1529)
- `packages/coding-agent/src/tools/hashline-header-path.ts` (`unwrapHashlineHeaderPath`, shared via `internal/hashline/header_path.go`)
- `packages/coding-agent/test/tools/approval.test.ts`
## Tool tier table (omp `v19.1.1`, `31ffee513f`)

| Tool | Tier / hook | Upstream line |
|---|---|---|
| `read` | Dynamic: max `urlReadTier(path)` (`ssh://` → `exec`), legacy `<file>.pdf:<member>` without line selector → `exec`, else `read` | `packages/coding-agent/src/tools/read.ts:1254-1262` |
| `write` | Dynamic: `urlWriteTier(hashline.UnwrapHeaderPath(path))` (`local://`/`agent://` → `read`; `out://` → `read` or tool-deny when disabled/subagent; `cfg://`/`conflict://`/`vault://`/plain file → `write`; `proc://`/`ssh://` → `exec`; read-only schemes → tool-deny `<scheme>:// URLs are read-only`); `approvalCacheTargets` | `packages/coding-agent/src/tools/write.ts:541-570` |
| `edit` | Dynamic: `strictestApproval` over every hashline section target and `MV` destination via `urlWriteTier`; default `write`; `approvalCacheTargets` | `packages/coding-agent/src/edit/index.ts:460-496` |
| `ast_edit` | Dynamic: `strictestApproval` over `paths[]` via `urlWriteTier` (non-string entry fails closed to `exec`; no session / no header unwrap); default `write` | `packages/coding-agent/src/tools/ast-edit.ts:176-204` |
| `bash` | Dynamic via `shell.EvaluateBashToolDecision`: `bashInterceptor.enabled` (all 10 rules, tool-owned deny before any prompt), two-view operator+session `bash.patterns` + `bash.allowedEnvVars` (`deny`/`prompt` from either view; `allow` requires BOTH views), `CRITICAL_BASH_PATTERNS` (`override: true`), bare mounted tool-command line → `read`, else `exec` | `packages/coding-agent/src/tools/bash.ts:834-894` |
| `eval` | `exec` | `packages/coding-agent/src/tools/eval.ts:291` |
| `task` | `exec` | `packages/coding-agent/src/task/index.ts:675` |
| `glob` | Dynamic: `searchPathTier` (`ssh://` → `exec`, else `read`) | `packages/coding-agent/src/tools/glob.ts:161` |
| `grep` | Dynamic: `searchPathTier` | `packages/coding-agent/src/tools/grep.ts:563` |
| `find` | Dynamic: `searchPathTier` | `packages/coding-agent/src/tools/jfind/index.ts:74` |
| `ast_grep` | Dynamic: `searchPathTier` | `packages/coding-agent/src/tools/ast-grep.ts:146` |
| `lsp` | Dynamic: `LSP_READONLY_ACTIONS` (`diagnostics`, `definition`, `type_definition`, `implementation`, `references`, `hover`, `symbols`, `status`, `capabilities`) → `read`, else `write` | `packages/coding-agent/src/lsp/tool.ts:314-318`, `lsp/servers.ts:26-36` |
| `debug` | Dynamic: `DEBUG_READONLY_ACTIONS` (`output`, `threads`, `stack_trace`, `scopes`, `variables`, `disassemble`, `read_memory`, `loaded_sources`, `modules`, `sessions`) → `read`, else `exec` | `packages/coding-agent/src/tools/debug.ts:57-68,545-549` |
| `github` | Dynamic: `GITHUB_READONLY_OPS` (`repo_view`, `file_read`, `search_issues`, `search_prs`, `search_code`, `search_commits`, `search_repos`, `run_watch`) → `read`, else `exec` | `packages/coding-agent/src/tools/gh.ts:69-78,140-144` |
| `ask` | `read` | `packages/coding-agent/src/tools/ask.ts:423` |
| `todo` | `read` | `packages/coding-agent/src/tools/todo.ts:687` |
| `wait` | `read` | `packages/coding-agent/src/tools/wait.ts:317` |
| `web_search` | `read` | `packages/coding-agent/src/web/search/index.ts:594` |
| `checkpoint` | `read` | `packages/coding-agent/src/tools/checkpoint.ts:55` |
| `rewind` | `read` | `packages/coding-agent/src/tools/checkpoint.ts:91` |
| `yield` | `read` | `packages/coding-agent/src/tools/yield.ts:316` |
| `goal` | Dynamic: `op == "get"` → `read`, else `write` | `packages/coding-agent/src/goals/tools/goal-tool.ts:65-66` |
| `resolve` / `reject` / `propose` | `read` | `packages/coding-agent/src/tools/resolve.ts:343,360,378` |
| `report_issue` | `write` | `packages/coding-agent/src/tools/report-tool-issue.ts:713` |
| `context_notes` / `new_context` | Dynamic (`text` present → `write`, else `read`) / `write` | `packages/coding-agent/src/tools/context-notes.ts:82-83,156` |
| `manage_skill` | `write` | `packages/coding-agent/src/tools/manage-skill.ts:42` |
| `memory_edit` / `recall` / `reflect` | `read` | `packages/coding-agent/src/tools/memory-{edit,recall,reflect}.ts` |
| `think` | `read` | `packages/coding-agent/src/tools/think.ts:53` |
| `security_scan` | `exec` | `packages/coding-agent/src/tools/security-scan.ts:109` |
| `browser` (eval prelude) | `exec` | `packages/coding-agent/src/tools/browser/prelude-definition.ts:71` |
| Mounted tools via `bash` | Outer bare tool-command line is `read`; nested `ctx.ExecuteTool` fires `EventToolCall` so the target tool's own tier/policy runs | `packages/coding-agent/src/tools/bash.ts:890-891`, `exec/tool-command-host.ts:109` |
| MCP (`mcp__*`) | `write` tier with `formatApprovalArguments` | `packages/coding-agent/src/mcp/tool-bridge.ts:811` |
| Unannotated extension tools | Default `exec` | `packages/coding-agent/src/tools/approval.ts:290,345` |

## Settings read & two-view security rule

| Key | Default | Honoured |
|---|---|---|
| `tools.approvalMode` | `"yolo"` | Yes via strict-of-two-views (`Setting` operator view + `SessionSetting` session view): evaluates both views per call and takes the stricter outcome (`deny > prompt > allow`), so no total order between `always-ask` and `always-ask-cached` is assumed; fails closed to `always-ask` (notifying user once) when `Setting == nil` or operator config failed to load (`settings.StoreFor(ctx).LoadError() != nil`); subagents inherit `"yolo"` per `task/executor.ts:1298-1301` |
| `tools.approval` (`tools.approval.<tool>`) | `{}` | Yes via strict-of-two-views (`Setting` + `SessionSetting`): `allow` / `deny` / `prompt` for every tool; project layers can tighten, never loosen |
| `tools.restrictedFolders` | `[]` | Yes via strict-of-two-views (`Setting` + `SessionSetting`): overrides `yolo`, explicit `allow`, and cached grants (recursive searches rooted above a restricted folder and symlinks into it also match); fails closed headless with `HeadlessRestrictionError` |

## Interplay with `internal/gate` (plan mode)

Plan-mode write restrictions are enforced by `internal/gate` inside each tool after target path resolution; `internal/approval` runs in `sdk.EventToolCall` before `Execute` and does not duplicate `internal/gate`.

## Tests ported

- 7 test functions in `internal/approval/approval_test.go` plus checked-in differential tests `TestOmpApprovalDifferentialReduced` (3,873 recorded omp `v19.1.1` verdicts in `testdata/approval_diff_reduced.jsonl` across all 10 sections, all 29 compared tools, and all expectation classes) and `TestOmpApprovalUnwrapRoute` (all 17 bracketed `proc://<job>/kill` shapes end-to-end) in root `approval_ompdiff_test.go` (full 30,167-case corpus also passes with 0 mismatches via `OMP_APPROVAL_DIFF_CORPUS`).

## Not ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| Persistent SQLite grant store across sessions (`approval-cache.db`) | `tools/approval-cache.ts:1-148` | `always-ask-cached` target/folder grants (including `Approve File`, `Approve Directory`, and `Always Approve ...` choices and folder listings) are fully ported in-memory per session and released on `lifecycle.End`, without adding a SQLite driver/DB file across sessions. |
| Custom approval dialog (`ui.approve` compact/expandable preview) & `tool_approval_requested` / `tool_approval_resolved` events | `tools/approval.ts:876-899`, `wrapper.ts:427-464` | PiG SDK `sdk.Context` exposes `Select` (omp's own RPC/extension-host fallback at `tools/approval.ts:900-938`) and has no `ui.approve` dialog or approval-audit events. |
| Provider computer-use safety checks (`pendingSafetyChecks`) & ACP `acpApprovedArgs` bypass | `wrapper.ts:391-406,472-476` | Computer-use tool and ACP bridge are not ported. |
