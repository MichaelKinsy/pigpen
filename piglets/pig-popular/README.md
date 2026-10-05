# pig-popular

PiG with Go ports of popular Pi extensions. Each member is a Package in `components/` with its own README,
credit, proof (`port/`) and row in `ports/ports.json`.

| Member | Original | What it adds |
|---|---|---|
| `todo` (`rpiv-todo`) | `@juicesharp/rpiv-todo` 2.11.0 by juicesharp, MIT | the `todo` tool, `/todos`, a live overlay |
| `powerline` (`powerline-footer`) | `pi-powerline-footer` 0.19.1 by Nico Bailon, MIT as declared in package.json | the powerline status bar and `/powerline` (partial: no custom editor, queue or welcome header) |
| `goal` (`pi-goal-x`) | `pi-goal-x` 0.32.3 by tmonk, MIT | durable goal files under `.pi/goals` and `/goal-direct`, `/sisyphus-direct`, `/goal-list`, `/goal-pause`, `/goal-unfocus`, `/goal-clear` (partial: no scheduler, drafting or auditor) |
| `permissions` (`pi-permission-system`) | `@gotgenes/pi-permission-system` 39.0.3 by MasuRii and Christopher D. Lasher, MIT | allow, ask and deny rules for tool calls and a `tool_call` gate (partial: no approval dialog, full bash parsing or path rules) |
| `subagents` (`pi-subagents`) | `pi-subagents` 0.73.1 by Nico Bailon, MIT | agent and chain definitions and discovery, `subagent` list and get, single-agent and chain runs through a child pig, nesting to depth 2 and 4 at once (partial: no parallel, async, workflow or fleet features) |
| `ask-user-question` (`rpiv-ask-user-question`) | `@juicesharp/rpiv-ask-user-question` 2.11.0 by juicesharp, MIT | the `ask_user_question` tool (partial: dialogs, not the tabbed questionnaire) |

Build it from the repository root (see `docs/` for the build steps): `npm run stage`, then build the staged
manifest `dist/staged/piglets/pig-popular/piglet.yaml` with `pig piglet build`.
