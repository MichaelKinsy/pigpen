# `oh-my-hermes-pig` Parity Report (`PARITY.md`)

**Status:** Verified (`1/1` oracle case PASS, `0` SDK/fuse gaps, `0` built-in PiG tool leaks, `0` accidental network calls, `0` leaked PIDs)  
**Upstream:** `rlaope/oh-my-hermes` @ `e1c729b799f266891aa5724ecad223d587b21205` (`v5.0.1`, MIT) layered onto `pig-hermes` (`NousResearch/hermes-agent` @ `45e9584556a97f50a3cb6394a6e7f6afa97bb66a`, MIT)  
**Host:** PiG `v0.8.0` (build `73585394f40c`, Go `go1.27.4`)  
**Composition:** `extends: local:../pig-hermes/piglet.yaml` (single fused binary, zero duplicated Hermes code)

## Measured Verification Results

| Check | Command | Result |
|---|---|---|
| Go unit + fakehost tests (`-race`) | `go test -race -count=1 ./...` (`oh-my-hermes`, `pig-hermes`, `pig-hermes-core`) | **PASS** (all 10 packages green, 0 races) |
| Cross-platform static check | `GOOS=linux\|darwin\|windows go vet ./...` | **PASS** |
| Go SDK & fuse-hazard scan | `pigeq gaps --go components/oh-my-hermes/extensions/oh-my-hermes` | **PASS** (`0` gaps, `0` fuse hazards) |
| Fused Piglet binary build | `npm run build:piglet -- oh-my-hermes-pig` | **PASS** (`~/.local/bin/oh-my-hermes-pig`, 63.9 MB fused binary) |
| Mutation proof | `pigeq mutate --unit-only --go components/oh-my-hermes/extensions/oh-my-hermes --mutations components/oh-my-hermes/port/mutations.json` | **PASS** (`3/3` KILLED) |
| Hermetic `pigeq llm` oracle proof | `OH_MY_HERMES_BIN=~/.local/bin/oh-my-hermes-pig python3 -B piglets/oh-my-hermes-pig/proof/oracle.py` | **PASS** (`1/1` case, 7 turns, 6 tool calls, 0 net, 0 leaked PIDs; proves live `omh_todo`↔`todo_list` state sharing across fused extensions) |
| Pigpen repo gates | `npm run check` | **PASS** (42 skills, 46 Packages, 72 provenance records, 102 components) |
| Live PTY/ANSI visual proof | `OH_MY_HERMES_BIN=~/.local/bin/oh-my-hermes-pig python3 -B piglets/oh-my-hermes-pig/proof/ansi.py` | **PASS** (`truecolor=1141`, `sgr=2465`, classic PiG emerald-green crowned winged-pig login + 5 `omh-*.json` themes, `exit=0`, `0` net, `0` leaked PIDs) |

## Wave 2 Look, Feel & Interactive Overlay Additions
- **OMH Crowned Winged-Pig branding:** `ui.registerSprite("omh-pig")` + `ui.setLogin(...)` (`41x5` `OH-MY-HERMES` brand, `32x14` hero, `16x14` mascot).
- **5 real PiG theme JSONs:** auto-installed to `<agentDir>/themes/omh-{sky,amber,crimson,mono,kanagawa}.json` and applied via `ctx.SetTheme` on startup and `/omh-theme <name>`.
- **Interactive overlays (`overlays.go`):** `/omh-model` (category → preset/custom model + thinking picker persisted to `routing/model-chains.json`), `/dag` (run picker + pinned `sdk/kit` box-frontier widget + ready-node advancement), and `/bots` (3-level Profile → Sessions/Teams/Delegations → Detail drill-down), with clean non-UI fallback.

## Wave 3 Flagship Skills, Opt-In LSP & Maestro
- **10 Flagship OMH Workflow Skills (`components/oh-my-hermes/skills/pigpen-omh-*/SKILL.md`):** `ulw-context`, `ulw-interview`, `ulw-research`, `ulw-plan`, `ulw-work`, `dag-ulw`, `ulw-maestro`, `ulw-loop`, `ulw-qa`, and `ulw-perf` bundled into `oh-my-hermes-pig/piglet.yaml` (all passing Pigpen `quality-gates.mjs`).
- **4 Opt-In `lsp_*` Tools + `omh_maestro` (`lsp_maestro.go`):** `lsp_definition`, `lsp_references`, `lsp_rename` (UTF-16 column conversion), and `lsp_diagnostics` over pure-Go JSON-RPC 2.0 stdio gated behind `OMH_LSP_TOOLS=1`, plus `omh_maestro` (`coding_executor_handoff/v1` prepare/dispatch/list/get).
- **Inherits Live Hermes Plugin Runtime:** `oh-my-hermes-pig` automatically inherits `pig-hermes`'s full runtime Hermes plugin loader/translator (`/plugins`, `plugins` CLI, Python NDJSON bridge).

## Wave 4 Fullscreen TUI Views, Onboarding/CLI & 11 Additional `omh_*` Tools
- **3 Fullscreen `sdk.RemoteComponent` Interactive TUI Views (`fullscreen_views.go`, `overlays.go`):**
  - `/dag [run_id]`: fullscreen box-frontier graph pager (`╭─╮│╰─╯`, `│`/`─`/`▼`, `[-]`/`[+]` fold, `d` details, `e` evidence, `[`/`]` run cycle, `r` refresh) + upgraded inline folding lane tree widget (`▾`/`▸`).
  - `/bots` (`Ctrl+S`): 3-level live multi-profile board (Profiles with 24-cell `▁▂▃▄▅▆▇█` sparklines & 10-cell `▰`/`▱` memory gauges → Sessions/Teams table → Session Messages).
  - `/omh-model`: single-screen arrow-key table (`↑/↓` category, `←/→` head model ring, `-/+` effort bar, `d` reset default, `Enter` atomic save, `Esc` cancel).
- **OMH Onboarding & 4 Additional CLI Subcommands (`cli_extended.go`):** `omh-setup`, `quickstart`, `recommend`, `probe`, plus `--onboard model-setup` flag preprocessor and `OnSessionStart` guided onboarding card (total: **8 OMH CLI subcommands**).
- **11 Additional `omh_*` Tools (`tools_extended.go`):** `omh_status`, `omh_hud`, `omh_context`, `omh_recommend`, `omh_probe`, `omh_capabilities`, `omh_memory`, `omh_resume`, `omh_gather_evidence` (session-cwd confinement, per-prefix flag allowlist, process-group timeout), `omh_document_plan` (mutex-guarded chunk persistence), and `omh_source_trust` (fail-closed ceiling) — total **21 `omh_*` + 4 opt-in `lsp_*` = 25 OMH tools** layered on top of all 25 inherited Hermes tools.

## Wave 5 Live Styled Overlays, Theme Fidelity & Active Orchestration Enforcement
- **Full ANSI Theme Styling & Live Auto-Refresh (`fullscreen_views.go`, `overlays.go`, `themes_branding.go`):** colorized `/dag`, `/bots`, `/omh-model`, and `belowEditor` inline DAG widget using active OMH theme truecolor; implemented `sdk.RemoteComponentInvalidator` + `Dispose()` (1s `/dag` ticker preserving node-0 selection, 5s `/bots` ticker with off-lock I/O and bounded 40-file JSONL fallback); added real `/dag` uniform centered boxes + parent→child midpoint edge bus (`╰─┬─╯`/`╭─┴─╮`/`┼`/`▼`), `/bots` read-only `state.db` + 8×4 sha256 identicon avatars + 2-col dividers, and `/omh-model` Custom Identities (`Space` toggle honored in `RouteRouter.Resolve`), `providers` + unserved `!` markers, and `model-efforts.json` ladders. Fixed `omh-sky` (`#00CED1`) and `omh-crimson` (`#FF6A3D` vs `#FF3D6E`) palette contrast.
- **Active Orchestration Hooks & Interop (`hooks_enforcement.go`, `todo_completion_memory.go`, `tools_extended.go`, `surface_cli.go`):** `EventToolCall` enforces `plan_stage=awaiting_acceptance` edit guard and result-aware repeat-call loop breaker (threshold 8, 300s expiry, poller exemption); `EventToolResult` auto-updates `omh` HUD status and `belowEditor` DAG widget; `childSessionRegistry` ensures delegated children receive `SectionOMHChildCalibration` and auto-latch DAG nodes on stop; `OmhMemoryProvider.Prefetch` merges `omh_memory capture` records; adds `omh_interact`, `omh_role` canonical alias, `/ulw`, `/omh-status`, interactive `/omh-theme` picker, and non-conflicting `Ctrl+Alt+B` `/bots` shortcut (zero startup warnings).