# pi-goal-x (Go port)

A Go extension for PiG that ports `pi-goal-x` 0.32.3 (see [CREDITS.md](CREDITS.md)) in full: durable goals kept as Markdown files with a
JSON header under `.pi/goals/`, the durable scheduler (claim, wait, wake, retry, stall, network recovery), the model tools
(`get_goal`, `create_goal`, `update_goal`, `set_goal_tasks`, `update_goal_task` and the drafting tools), usage and time accounting,
prompt injection and compaction handling, the goal dashboard and widgets, the independent completion auditor and the blocker oracle,
layered settings, and all 16 commands (`/goal`, `/sisyphus`, `/goal-direct`, `/sisyphus-direct`, `/goal-list`, `/goal-pause`,
`/goal-resume`, `/goal-cancel`, `/goal-clear`, `/goal-focus`, `/goal-unfocus`, `/goal-status`, `/goal-refresh`, `/goal-recovery`,
`/goal-settings`, `/goal-tweak`). It needs no Node runtime. Made with the [extension porting Skill](../extension-port/README.md);
it draws its own components through [components/pitui](../pitui/README.md).

```sh
pig install ./components/goal
```

## Differences from the original (stand-ins, each recorded in [port/PORT.md](port/PORT.md))

- The auditor and the oracle run in a child `pig` process (JSON-event mode, read-only tool allowlist): the Go SDK has no child
  session API. Three consequences are filed as SDK requests: a tool served by the parent cannot be registered in the child (the
  oracle's advice is a tagged reply block), `compactionEnabled=false` is not mapped, and providers registered only by the parent's
  extensions are unavailable in the child unless `auditorProjectResources` is on.
- Widgets are laid out at the terminal width and pushed again on a width change, instead of being re-rendered per frame.
- Two upstream end-to-end cases that need a live `pi` against a mock provider with 503/429 outages and one that runs Pi's own provider
  serializers are named skips in the twin ledger.

## Settings

All keys of the original (`<project>/.pi/pi-goal-x-settings.json`, `<agent dir>/pi-goal-x-settings.json`, `PI_GOAL_*` environment), with its
precedence, validation, diagnostics and `/goal-settings` menu.
