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
| Hermetic `pigeq llm` oracle proof | `python3 -B piglets/pig-hermes/proof/oracle.py` | **PASS** (`4/4` cases incl. `04-live-python-plugin`, `16` LLM turns, `12` tool calls, `0` accidental network calls, `0` leaked PIDs, `0` built-in PiG tool leaks) |
| Live PTY/ANSI visual proof | `PIG_HERMES_BIN=~/.local/bin/pig-hermes python3 -B piglets/pig-hermes/proof/ansi.py` | **PASS** (`truecolor=660`, `sgr=1440`, classic PiG emerald-green winged-pig login + Hermes dynamic footer + 9 `hermes-*.json` themes, `exit=0`, `0` net, `0` leaked PIDs) |

## Wave 2 Deep-Layer & Visual Parity Additions
- **Hermes Winged-Helmet PiG branding:** `ui.registerSprite("hermes-pig")` + `ui.setLogin(...)` (`41x5` gold `PIG-HERMES` brand, `32x14` winged-pig + caduceus hero, `16x14` mascot).
- **9 real PiG theme JSONs:** auto-installed to `<agentDir>/themes/hermes-*.json` on startup and `/skin <name>`.
- **Drop-in upstream `~/.hermes/state.db` session discovery:** read-only `sqlite3 -readonly -json` reader merged into `session_search` alongside PiG JSONL sessions.
- **Hermes lean-tail & tool-result pruning compaction:** deterministic structured summary with `[pruned N chars]` capping in `OnSessionBeforeCompact` (safely delegating to PiG native compaction on `previousSummary` / `isSplitTurn`).
- **Hardened `filetools`, `terminal`, `webmcp`:** symlink write-through, active Hermes `config.yaml` hard-deny, partial-read guard, trailing `&` auto-background + `WaitDelay` handling, and real DuckDuckGo HTML search parsing.

## Wave 3 Runtime Hermes Plugin Bridge & Deep Subsystems
- **Live Hermes Plugin Loader & Translator (`plugins/`):** discovers unmodified Hermes plugins (`~/.hermes/plugins/`, opt-in `./.hermes/plugins/`, `$HERMES_BUNDLED_PLUGINS`) in both `plugin.yaml` and portable Agent Plugin `plugin.json` (+ `mcp.json` with `cwd`/`./` resolution and `PLUGIN_ROOT`/`PLUGIN_DATA`) formats, runs Python plugins out-of-process over a bidirectional NDJSON JSON-RPC `python3` bridge, and dynamically translates their tools, lifecycle hooks (`pre_tool_call`, `post_tool_call`, `transform_tool_result`, `on_session_start`, `on_session_end`, `on_pre_compress`), slash commands, and underscore-only system-prompt sections (`hermes_plugin_*`) into PiG SDK primitives. Adds `/plugins` and `pig-hermes plugins list|info|enable|disable`.
- **7 Additional Hermes Tools (`media/`):** `tool_search`, `tool_describe`, `tool_call` (with recursion guard), `browser_exec` (`browser-use` CLI bridge), `vision_analyze`, `image_generate`, and `text_to_speech` (total: **25 Hermes tools**).
- **Progressive Subdirectory Hints & Session/Context Slash UX:** `EventToolResult` hook appending nested `AGENTS.md`/`CLAUDE.md`/`.cursorrules` (5-level walk, symlink confinement, SHA-256 dedup, 32 KiB cap) without breaking prompt caching, plus `/context` (`/ctx`), `/sessions`, `/save`, `/retry`, and `/undo` (total: **20 slash commands**).

## Wave 4 TUI Chrome, 5-Choice Approval, Setup & 7 Extended CLI Subcommands
- **Classic PiG Emerald-Green Branding + Hermes Dynamic Footer & Busy Indicators (`surface/chrome.go`):** updated winged-helmet PiG mascot/hero to official `pig-default` emerald green (`#48A381`), added live `SetFooterRenderer` with responsive width tiers (`<52`/`52-75`/`>=76`), 10-cell threshold-colored context/memory meter (`[█████░░░░░]`), `ExtensionStatuses()` passthrough, `/banner [pin|off]` 2-column Hermes summary card, and `/indicator [status|kaomoji|emoji|unicode|ascii]` (exact upstream 15-face kaomoji, emoji, braille, and ascii spinners).
- **5-Choice Dangerous-Command Approval Modal & Additive `command_allowlist` (`terminal/danger.go`, `terminal/terminal.go`):** `1. Allow once`, `2. Allow this session`, `3. Always allow` (additive YAML persistence supporting indentless lists, newline escaping, exact matching, and compound-operator bypass prevention), `4. Deny`, and `5. No, and tell Hermes what to do instead`.
- **7 Additional Hermes CLI Subcommands (`surface/cli_extended.go`):** `setup`, `config` (`show|get|set|unset|path|env-path|check` preserving `0600` mode and verbatim `.env`), `model`, `mcp` (`list|add|remove|test`), `sessions` (`list|stats|export|delete|rename|pin|unpin|pinned`), `insights`, and `logs` (total: **13 headless CLI subcommands**).
- **Hardened Plugin Python Discovery & Vision MIME Check:** `findPython3()` skips broken shims under isolated `HOME` and falls back to working system `python3`; `vision_analyze` rejects non-image local files; proven end-to-end by `04-live-python-plugin` oracle case.

## Wave 5 Look & Feel Overhaul & Runtime Parity
- **Borderless Glyph Tool Rows & Inline Diffs (`surface/tool_renderers.go`):** switched to `sdk.ToolRenderShellSelf` with per-tool emojis + friendly verbs, `tui.VisibleWidth`/`tui.TruncateToWidth` host-exact clamping, C0/tab sanitization, and `│`-guttered `+`/`-` diffs.
- **Hermes Composer Chrome & Live Animated Spinner (`surface/editor.go`, `surface/chrome.go`):** `SetEditorComponent` replaces row 0 with a skin-colored Hermes top rule while preserving PiG APC cursor markers verbatim; `SetWorkingIndicator` + 1s ticker drives live animated kaomoji/emoji/braille/ascii spinners with elapsed clock (zero double-spinner, clean teardown on `agent_end`/`session_shutdown`); footer adds live model, `cwd (git-branch)`, and busy/idle key hints.
- **Double-Bordered `RemoteComponent` Approval Modal (`terminal/approval_ui.go`):** warn-gold `╔═╗║╚═╝` modal with sanitized command preview, 10-line `v` paging, Kitty-compatible `tui.MatchesKey` `1-5`/`Esc`/`Ctrl+C` hotkeys, and inline `5` (`tell`) text entry.
- **Runtime Config, Real `/compress` & `/tools`, Delegate Live Control & Hardened `execute_code` (`surface/runtime_parity.go`, `agents/delegate.go`, `webmcp/execute_code.go`):** startup applies `agent.max_turns`, `agent.reasoning_effort` (without clobbering CLI `--reasoning`/`--thinking`), `disabled_toolsets`, and default model; `/compress` invokes `ctx.Compact`; `/tools enable|disable` updates `ctx.SetActiveTools` live; `delegate_task` adds `action: list|steer|stop` and caller `task_id`/`lane_id`; `execute_code` scrubs sensitive env keys by substring and spills large stdout up to a 32 MiB cap; adds `-q`/`-r <id>`/`--reasoning`/`chat` compat preprocessor + `tools`/`memory` CLI subcommands; strips `builtin:tool-search` in `piglet.yaml` for zero startup warnings.