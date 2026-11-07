# posthorse (Go port of pi-posthorse)

A Go extension for PiG that starts a fresh context window for long tasks **without an automatic model-written summary**.
It is a port of **pi-posthorse** 0.10.11 by Mitch Fultz (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime and makes no network request.

Tools: `new_context({handoff?})` (explicit retain-none reset once the foreground tools succeed), `get_context_remaining()`,
`notes({op,...})` (list, read, search, write, append; fork-private copies; `.pi/notes`) and `history({op,...})` (search and read earlier
conversation, images, `all: true` across the project's sessions). It also adds the request-local guidance section, one task-note reminder
before the rollover line, automatic no-summary rollover with a bounded recovery record and a retained recent tail
(`compaction.keepRecentTokens`), the note-scope guards for native file tools, and the tool and message cards. Settings are Pi's own
`compaction.*` (`enabled`, `reserveTokens`, `keepRecentTokens`); there are no separate settings.

```sh
pig install ./components/posthorse         # needs pig >= 0.7.0 (Pi 1.5 extension API)
```

Or take the [`pig-posthorse`](../../piglets/pig-posthorse/README.md) Piglet. Keep exactly one compaction-policy extension loaded.

## Differences from the original

* Only `Extension()` is exported; the TS re-exports `createPosthorse`, `registerPosthorseMessages` and `toolCards` have no Go-package equivalent.
* **Clicking a reminder or context-window card does not toggle it** (Ctrl+O expansion works): PiG renderers get no message identity or mouse events.
  Owner-approved fix lane `fix/071-renderer-click`; named sub-skip in the renderers twin.
* Host differences between Pi 1.5 and PiG that the original shares are recorded in [port/PORT.md](port/PORT.md); nothing is normalised away.
* Go extension stderr is not surfaced in print mode (`console.error` equivalent); PiG lane `fix/071-host-parity-ph`.

Evidence, contract tables, findings and the perf basket: [port/PORT.md](port/PORT.md).
