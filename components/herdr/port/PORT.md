# herdr port record

Original: [`Hai-Fai-Solutions/herdr-pig`](https://github.com/Hai-Fai-Solutions/herdr-pig) at `3ec680471e8282e0a8a64545781069f822c0c212`, `extensions/herdr-agent-state.ts` (276 lines), Apache-2.0. The original is not published on npm; its documented install forms are `herdr plugin install Hai-Fai-Solutions/herdr-pig` and `pig -e /path/to/herdr-pig`.

The extension has no tools, commands or flags, so there is nothing to refuse. Equivalence is not yet evidenced: there are no `pigeq` goldens, scenarios or mutation run, and the Go tests (`extensions/herdr`) are Go-only, not the 7 tests of `test/herdr-agent-state.test.ts` run against both.

## Divergences

| Behavior | herdr-pig | Go port | State |
|---|---|---|---|
| Transport | socket JSON `pane.report_agent` / `pane.release_agent` | `"$HERDR_BIN_PATH" pane report-agent` / `release-agent` CLI | deliberate, needs owner decision |
| Source identity | `pig` | `custom:pig` | deliberate, needs owner decision |
| Retry | 0.5 s then 1.5 s retry | one 3 s call | deliberate, needs owner decision |
| Resume fallback | `pig --session <id>` | `pig --session-id <id>`, none for in-memory sessions | deliberate, needs owner decision |
| `session_start` | does not reset the blocked counters | resets them, so a session replacement in a kept-alive instance cannot leave the pane stuck blocked by a prompt or bus emitter that never ended | deliberate, needs owner decision |
| Blocked sources | `herdr:blocked` only | also `ui_prompt_start` / `ui_prompt_end`, counted per source | addition |
| Plugin install, status, startup refresh | in the original | in `piglets/herdr/herdr-plugin`; startup refresh dropped | documented |

## Fixed to match the original

- `agent_settled` reports idle only when `isIdle()` succeeds and returns true. Earlier versions also reported idle when the check failed (0.1.1).
