# pig-hermes

An unofficial Go homage to Nous Research's **Hermes Agent** built as a single fused Go Piglet binary on PiG `v0.8.0`.

> Unofficial and not affiliated with or endorsed by Nous Research. Pinned against `NousResearch/hermes-agent` commit `45e9584556a97f50a3cb6394a6e7f6afa97bb66a`. See `PARITY.md` for exact ported, host-native, `HOST`-blocked, and out-of-scope surfaces.

## What It Ships

- **Zero PiG built-in tools** (`tools: []`); exposes Hermes's tool surface:
  - File & Patch: `read_file`, `write_file`, `patch` (fuzzy replace + V4A multi-file diff), `search_files`
  - Terminal: `terminal` (persistent cwd, approval guard), `process_manage` (background session registry)
  - Memory & Skills: `memory` (`MEMORY.md` / `USER.md`, `\n§\n` delimiter, atomic batch ops, frozen prompt snapshot), `skills_list`, `skill_view`, `skill_manage`
  - Sessions & Orchestration: `session_search`, `delegate_task` (`CreateAgentSession` + pluggable `RouteResolver`), `todo_list`, `clarify`, `cronjob_manage`
  - Web, Code & MCP: `web_search`, `web_extract`, `execute_code`, plus `~/.hermes/config.yaml` `mcp_servers` → PiG native `builtin:mcp` adapter
- **Hermes CLI & Interactive Surface**: 13 slash commands (`/memory`, `/skills`, `/profile`, `/skin`, `/toolsets`, `/tools`, `/cron`, `/usage`, `/status`, `/personality`, `/yolo`, `/compress`, `/todos`), 9 built-in skins, and headless CLI subcommands (`profile`, `skills`, `cron`, `status`, `doctor`).
- **Extensible by Design**: Built on `components/pig-hermes-core` with versioned `EventBus` channels, named `SystemPromptSections`, and a shared `clihook` dispatcher so derived Piglets such as `oh-my-hermes-pig` cleanly `extends: local:../pig-hermes/piglet.yaml`.

## Build

```sh
npm ci --ignore-scripts
npm run stage
PIG_SOURCE_ROOT=/path/to/PiG PIG_BIN=/path/to/pig npm run build:piglet -- pig-hermes
```
