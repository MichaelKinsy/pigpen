# oh-my-hermes-pig

An unofficial Go homage to **`rlaope/oh-my-hermes`** (`v5.0.1`) layered cleanly onto **`pig-hermes`** via `extends: local:../pig-hermes/piglet.yaml` in a single fused Go Piglet binary on PiG `v0.8.0`.

> Unofficial and not affiliated with `oh-my-hermes` contributors, Nous Research, or `oh-my-openagent`. Pinned against `rlaope/oh-my-hermes` commit `e1c729b799f266891aa5724ecad223d587b21205`.

## Architecture (`extends:` Composition)

`oh-my-hermes-pig` duplicates zero code from `pig-hermes`. It inherits all 18 Hermes tools, 13 Hermes slash commands, 3-tier system prompt, and `pig-mcp-adapter` from `pig-hermes`, and plugs into `components/pig-hermes-core` seams:
- `delegation.RegisterRouteResolver` → category/identity model-chain routing (`omh_delegate_route`)
- `memory.RegisterGlobalProvider` → `OmhMemoryProvider` episodic prefetch & provenance (`omh_remember`)
- `todo.Default()` → unified session checklist (`omh_todo` + `todo_list`)
- `usage.Default()` & `events.ChannelSubagentStart`/`Stop` → live delegation ledger & `omh_run_summary`
- `cli.RegisterSubcommand` → composable headless CLI subcommands (`hud`, `model-chains`, `dag`, `omh-doctor`)
- `sdk.SystemPromptSections.Set` → appends `omh_awareness`, `omh_delegation_protocol`, `omh_main_calibration` alongside `hermes_*`

## Build

```sh
npm run generate && npm run check
PIG_SOURCE_ROOT=/path/to/PiG PIG_BIN=/path/to/pig npm run build:piglet -- oh-my-hermes-pig
```
