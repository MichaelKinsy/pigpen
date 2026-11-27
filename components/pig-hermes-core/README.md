# pig-hermes-core

Shared Go library Package for `pig-hermes` and derived Piglets such as `oh-my-hermes-pig`.

Provides:
- `paths`: `HERMES_HOME` and named profile path resolution
- `config`: `~/.hermes/config.yaml` and `.env` parsing/writing
- `memory`: `MEMORY.md` / `USER.md` store (`\n§\n` delimiter, frozen snapshots) and `MemoryProvider` interface
- `delegation`: `SpawnRequest` / `SpawnResult` types and pluggable `RouteResolver` hook
- `todo`: unified session-scoped checklist store
- `usage`: session + child-session usage/cost ledger
- `events`: versioned `sdk.EventBus` channel names and payload schemas
- `cli`: single `clihook.Register` owner with composable subcommand/flag registration
