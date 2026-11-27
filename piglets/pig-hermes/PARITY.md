# `pig-hermes` Parity Report (`PARITY.md`)

**Status:** Verified (`3/3` oracle cases PASS, `6/6` unit mutations KILLED, `0` blocking SDK/fuse gaps, `0` accidental network calls, `0` leaked PIDs)  
**Upstream:** `NousResearch/hermes-agent` @ `45e9584556a97f50a3cb6394a6e7f6afa97bb66a` (MIT)  
**Host:** PiG `v0.8.0` (build `73585394f40c`, upstream Pi `1.6.0`, Go `go1.27.4`)  
**Framing:** Unofficial Go homage to Nous Research's Hermes Agent on PiG. Not affiliated with or endorsed by Nous Research. Parity is a goal, not a promise.

## Summary Counts (from Full 270-Row Inventory)

| Classification | Count | Notes |
|---|---:|---|
| Ported in Go (`PORTABLE_GO_STDLIB` + `NEEDS_EXTERNAL_CLI`) | 189 | 18 Hermes tools, bounded memory & skills, 3-tier system prompt, `session_search`, delegation, todo, clarify, cron, web/MCP/code-exec, 13 slash commands, 9 skins, 5 CLI subcommands |
| Reused Natively from PiG v0.8.0 (`HOST_PIG_NATIVE`) | 50 | JSONL sessions, compaction, `pig-mcp-adapter` / `builtin:mcp`, providers/models/auth, TUI/RPC, built-in slash commands |
| PiG Host / SDK Blockers (`HOST`) | 4 | Cited below with exact `file:line` in `~/work/pig-080-release` |
| Out-of-Scope Platform Daemons (`OUT_OF_SCOPE_LISTED`) | 27 | Multi-platform messaging gateway (`gateway/`), systemd/launchd cron daemon, Ink `tui_gateway/`, Electron pane, `acp_adapter/` (Pigpen ships `pig-acp`), RL `batch_runner.py`, cloud sandboxes |

## PiG v0.8.0 Host Blockers (`HOST`)

1. **In-place `context_edit` session entry** (`~/work/pig-080-release/extensions/sdk/context.go`): SDK supports `AppendEntry` (custom entries) and `OnSessionBeforeCompact`, but cannot append an in-place non-compaction message rewrite entry.
2. **In-process `PIG_HOME` / profile hot-switching** (`~/work/pig-080-release/extensions/sdk/context.go`): Running host session dir cannot be hot-swapped mid-session by an extension; profile selection works at startup via `-p/--profile` (`clihook`) and `HERMES_PROFILE`.
3. **Full startup banner / 4-tier responsive chrome** (`~/work/pig-080-release/extensions/sdk/ui.go`): Outside `slots.frontend`, extensions style TUI via `SetStatus`, `SetWidget`, `SetFooter`, `SetTheme`, and `ToolRenderer`.
4. **Assistant-output rewrite hook (`transform_llm_output`)** (`~/work/pig-080-release/extensions/sdk/events.go`): PiG's `MarkdownTransformerFunc` is display-only; no model-visible assistant text post-transform hook exists yet.

## Measured Verification & Oracle Gate Results

| Check | Command | Result |
|---|---|---|
| Go unit + fakehost tests (`-race`) | `go test -race -count=1 ./...` (`pig-hermes-core` + `pig-hermes` + 7 subpackages) | **PASS** (all 9 packages green, 0 data races) |
| Cross-platform static check | `GOOS=linux\|darwin\|windows go vet ./...` | **PASS** (all 3 OS targets clean) |
| Go SDK & fuse-hazard scan | `pigeq gaps --go` (`pig-hermes-core` & `pig-hermes`) | **PASS** (`0` blocking gaps; `1` `PARTIAL Context.SetTheme`) |
| Unit mutation check | `pigeq mutate --unit-only --mutations components/pig-hermes/port/mutations.json` | **PASS** (`6/6` mutants KILLED, `0` survived, `0` invalid) |
| Package & extension validation | `pig package validate` + `pig install --validate-only --json` | **PASS** (`valid: true`, `form: factory`, `language: go`, `packable: true`, 18 tools, 13 commands, 8 handlers, 5 flags) |
| Fused Piglet binary build | `npm run build:piglet -- pig-hermes` | **PASS** (`dist/bin/pig-hermes`, 62.2 MB fused binary, verified & checksummed) |
| Pigpen repo gates | `npm run check` (`generate-index --check`, `npm-packages`, `quality-gates`, `go-only`, `ports`, `skills-drift`) | **PASS** |
| Hermetic `pigeq llm` oracle proof | `python3 -B piglets/pig-hermes/proof/oracle.py` | **PASS** (`3/3` cases, `14` LLM turns, `11` tool calls, `0` accidental network calls, `0` leaked PIDs, `0` built-in PiG tool leaks) |
