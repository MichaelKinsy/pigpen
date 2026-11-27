# `oh-my-hermes` — Port Evidence & Architecture Mapping (`PORT.md`)

**Port Kind:** **Kind 4** (standalone orchestration layer port extending `pig-hermes` via `extends: local:../pig-hermes/piglet.yaml`)  
**Oracle Kind:** Hermetic `pigeq llm` behavioral oracle (`piglets/oh-my-hermes-pig/proof/oracle.py`) + fakehost Layer-1 tests + `pigeq mutate --unit-only`  
**Upstream Repository:** `https://github.com/rlaope/oh-my-hermes`  
**Frozen Upstream Commit:** `e1c729b799f266891aa5724ecad223d587b21205` (`v5.0.1`, MIT License, `Copyright (c) 2026 oh-my-hermes contributors`)  
**Base Piglet:** `piglets/pig-hermes/piglet.yaml` (`^0.1.0`)

## Seam Composition Map

| `oh-my-hermes` Surface | `pig-hermes-core` Seam Used | Tool / Command / Hook |
|---|---|---|
| Category & identity model routing | `delegation.RegisterRouteResolver` | `omh_delegate_route`, `/omh-model`, `model-chains` CLI |
| DAG / Team / Loop orchestration | `events.ChannelSubagentStart` / `Stop` | `omh_dag`, `omh_team`, `omh_loop`, `/dag`, `dag` CLI |
| Unified checklist | `todo.Default()` | `omh_todo` (shares live state with `todo_list`) |
| Evidence-gated completion | Atomic `$OMH_HOME/runtime/completion/` | `omh_completion` |
| Episodic memory provider | `memory.RegisterGlobalProvider` | `omh_remember`, `OmhMemoryProvider` |
| Roles & identity profiles | Atomic `$OMH_HOME/routing/identities.json` | `omh_roles` |
| Run summary & multi-profile board | `usage.Default()` + `paths.ResolveProfilePaths` | `omh_run_summary`, `/bots`, `/omh-hud`, `hud` CLI |
| Prompt calibration sections | `sdk.SystemPromptSections.Set` (underscore names) | `omh_awareness`, `omh_delegation_protocol`, `omh_main_calibration` |
| Headless CLI subcommands | `cli.RegisterSubcommand` / `RegisterFlagPreprocessor` | `hud`, `model-chains`, `dag`, `omh-doctor`, `--omh-home` |

## Verification Ledger

- **`go test -race -count=1 ./...`**: PASS (`github.com/MichaelKinsy/pigpen/oh-my-hermes`, including `fakehost_test.go` integration across all 9 `omh_*` tools, 5 slash commands, `delegation.ResolveRoute`, `OmhMemoryProvider`, unified `todo.Default()`, and `hermes_*`+`omh_*` prompt section preservation).
- **Cross-platform `go vet`**: PASS for `GOOS=linux`, `GOOS=darwin`, `GOOS=windows`.
- **`pigeq gaps --go`**: 0 gaps, 0 fuse hazards.
- **`npm run build:piglet -- oh-my-hermes-pig`**: PASS (62.6 MB single fused binary containing both `pig-hermes` and `oh-my-hermes` via `extends: local:../pig-hermes/piglet.yaml`).
- **`PIG_HERMES_BIN=~/.local/bin/oh-my-hermes-pig python3 -B piglets/oh-my-hermes-pig/proof/oracle.py`**: PASS (`1/1` case, 7 turns, 6 tool calls, proves `omh_todo` -> `todo_list` live shared store, all 18 Hermes + 9 OMH tools advertised, 0 PiG built-in tool leaks, 0 accidental network calls, 0 leaked PIDs).
- **`npm run check`**: PASS.
