# oh-my-hermes Component

Fused Go PiG extension porting the core orchestration, category model routing, DAG execution, evidence-boundary ledger, unified `omh_todo`, `OmhMemoryProvider`, slash commands, themes, and CLI subcommands of `rlaope/oh-my-hermes` (`v5.0.1`).

Built directly on `components/pig-hermes-core` seams (`delegation.RegisterRouteResolver`, `memory.RegisterGlobalProvider`, `todo.Default()`, `usage.Default()`, `events.Channel*`, `cli.RegisterSubcommand`, and `sdk.SystemPromptSections.Set("omh_*", ...)`) with zero code duplication from `pig-hermes`.
