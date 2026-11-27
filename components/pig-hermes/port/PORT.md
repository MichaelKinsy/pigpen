# `pig-hermes` — Port Evidence & Architecture Mapping (`PORT.md`)

**Port Kind:** **Kind 4** (standalone harness port of Nous Research's Hermes Agent as a fused PiG v0.8.0 Go Piglet)  
**Oracle Kind:** Differential protocol/tool-behavior oracle via `pigeq llm` + fakehost Layer-1 tests + `pigeq mutate --unit-only`  
**Upstream Repository:** `https://github.com/NousResearch/hermes-agent`  
**Frozen Upstream Commit:** `45e9584556a97f50a3cb6394a6e7f6afa97bb66a` (MIT License, `Copyright (c) 2025 Nous Research`)  
**PiG Target:** `v0.8.0` (build `73585394f40c`, upstream Pi `1.6.0`, Go `go1.27.4`)  
**Derived Piglet Readiness:** Exposes `components/pig-hermes-core`, versioned `sdk.EventBus` channels, named `sdk.SystemPromptSections`, and a single composable `clihook` dispatcher so `oh-my-hermes-pig` layers via `extends: local:../pig-hermes/piglet.yaml`.

---

## Architecture Mapping (Hermes Agent → PiG v0.8.0 + `pig-hermes`)

| Hermes Agent Subsystem | Upstream Source | `pig-hermes` Realization | Notes / Lost Behavior |
|---|---|---|---|
| File & Patch Tools (`read_file`, `write_file`, `patch`, `search_files`) | `tools/file_tools.py`, `file_operations.py`, `fuzzy_match.py`, `patch_parser.py` | `extensions/pig-hermes/filetools/` | Pure Go stdlib + optional `rg` on PATH; 1-indexed `<n>\|` lines, 9-strategy/4-tier fuzzy match + V4A patch, `.ipynb` extraction. |
| Terminal & Background Processes (`terminal`, `process_manage`) | `tools/terminal_tool.py`, `process_registry.py`, `approval.py` | `extensions/pig-hermes/terminal/` | Persistent cwd, dangerous-command confirmation, background process registry with ring buffers & clean shutdown; cross-platform `procattr_*.go`. |
| Curated Memory (`memory`) | `tools/memory_tool.py`, `memory_tool_store.py` | `pig-hermes-core/memory` + `memoryskills/` | Atomic batch ops (`add`/`patch`/`remove`; `replace` rejected), `\n§\n` delimiter, `MEMORY.md` (2200) / `USER.md` (1375), frozen session snapshot, `MemoryProvider` seam. |
| Skills (`skills_list`, `skill_view`, `skill_manage`) | `tools/skills_tool.py`, `skill_manager_tool.py` | `extensions/pig-hermes/memoryskills/` | Progressive disclosure across project > local > extra dirs, YAML frontmatter, path-traversal guards. |
| 3-Tier System Prompt & Context Files | `agent/system_prompt.py`, `prompt_builder.py`, `SOUL.md` | `extensions/pig-hermes/memoryskills/` | Populates named `sdk.SystemPromptSections` (`hermes.identity`, `hermes.memory`, `hermes.skills`, `hermes.context`, `hermes.tools`, `hermes.environment`) in-place via `sections.Set`. |
| Sessions, Search & Profiles (`session_search`) | `tools/session_search_tool.py`, `persistence/`, `hermes_constants.py` | `extensions/pig-hermes/sessions/` + PiG native sessions | PiG owns JSONL session trees, `/new`, `/resume`, `/fork`, `/tree`, `/compact`; `session_search` searches PiG JSONL files with AND/OR fallback, scroll, echo suppression. |
| Delegation, Todo, Clarify & Cron | `tools/{delegate_tool,todo_tool,clarify_tool,cronjob_tools}.py`, `cron/jobs.py` | `extensions/pig-hermes/agents/` | `delegate_task` via `ctx.CreateAgentSession` + `delegation.ResolveRoute` + `EventBus` channels; unified `todo.Default()`; `clarify`; `cronjob_manage` atomic `jobs.json`. |
| Web, Code Exec & MCP | `tools/{web_tools,code_execution_tool,mcp_tool}.py` | `extensions/pig-hermes/webmcp/` | Pure Go `net/http` `web_search`/`web_extract`, `execute_code` subprocess runner, `mcp_servers` translated to PiG native `RegisterMcpServer`. |
| CLI, Slash Commands & Skins | `hermes_cli/{commands,skin_engine,main}.py` | `extensions/pig-hermes/surface/` + `pig-hermes-core/cli` | 13 Hermes slash commands, 9 built-in skins, status bar, tool renderers, composable `clihook` subcommands (`profile`, `skills`, `cron`, `status`, `doctor`). |
| Messaging Gateway / ACP / Ink TUI / RL Batch | `gateway/`, `acp_adapter/`, `tui_gateway/`, `batch_runner.py` | `OUT_OF_SCOPE_LISTED` | Platform daemons/bridges; PiG provides native TUI + `--mode rpc`, Pigpen ships `pig-acp`. |

## Verification Ledger

- **`go test -race -count=1 ./...`**: PASS across `components/pig-hermes-core` and all 8 packages of `components/pig-hermes/extensions/pig-hermes` (including `fakehost_test.go` wire-protocol integration test).
- **Cross-platform `go vet`**: PASS for `GOOS=linux`, `GOOS=darwin`, `GOOS=windows`.
- **`pigeq gaps --go`**: 0 blocking gaps, 0 fuse hazards (`1` non-blocking `PARTIAL Context.SetTheme`).
- **`pigeq mutate --unit-only`**: `6/6` mutants KILLED (`surface_profile_flag_passthrough_print_mode`, `filetools_read_detects_nul_binary`, `memoryskills_rejects_replace_in_parser`, `webmcp_ssrf_blocks_loopback_ip`, `agents_cron_preserves_unknown_fields`, `sessions_search_caps_tool_output`).
- **`pig package validate` & `pig install --validate-only --json`**: PASS (`form: factory`, `language: go`, `packable: true`, 18 tools, 13 slash commands, 8 handlers, 5 flags).
- **`npm run build:piglet -- pig-hermes`**: PASS (`dist/bin/pig-hermes`, fused Go binary).
- **`python3 -B piglets/pig-hermes/proof/oracle.py`** (`pigeq llm`): PASS (`3/3` cases, `0` built-in tool leaks, `0` network attempts, `0` leaked PIDs).
- **`npm run check`**: PASS.
