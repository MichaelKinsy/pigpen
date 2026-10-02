# context-info

A Go extension for PiG that shows what fills the context window and what the
session costs. It shows nothing until you ask. In the background it counts tool
calls and reads the session at start and after compaction to calibrate its token
estimate.

| Command | Shows |
|---|---|
| `/context` (or `Ctrl+Shift+I`) | The context window: system prompt (with skills), tool definitions, conversation, cache, model and thinking level, available room. Uses the provider's live token count when there is one and an estimate (self-calibrating characters per token) when there is not, for example right after compaction. |
| `/tools` | Every tool, grouped by source, active or not, with call counts. |
| `/cost` | Tokens and cost by kind and by model, and the active model's per-million-token rates. |
| `/prompts` | The current system prompt. `/prompts agents` lists agent definitions from `<config home>/agents/*.md`; `/prompts <name>` shows one. |
| `/context-footer [on\|off]` | A two-line status footer: directory and git branch, context percentage, model, thinking level, cost, tokens, tools and calls. |

The footer **replaces PiG's footer**, so it is off by default. Start PiG with
`--context-footer` to have it from the start, or use `/context-footer on`. Turning it
off gives PiG's footer back. While it is off, the extension makes no footer or status
updates.

## Cost figures are estimates

Cost is tokens times per-million-token rates. The rates come from the active model.
Models billed by subscription carry no price, so for GitHub Copilot models the
extension falls back to a list-price table in `extension.go` (dated 2025-06-01,
verify before relying on it). A model that is in neither shows tokens and `$0.00`.

## Install

```sh
pig install ./components/context-info
```

The extension builds from source on first use (Go toolchain required) or fuses into a
Piglet Binary. No network, no Node runtime. It reads the session through the host and
files under the PiG config home (`agents/`); it writes nothing. The footer starts
`git` to show the branch, at most once every two seconds.
`pig package validate ./components/context-info` validates the Package.

## Test

The tests use PiG's public SDK through a fake host. From the Pigpen root:

```sh
PIG_BIN=/path/to/pig npm run test:go-ports -- -race
```

MIT. © Michael Kinsy. See [CREDITS.md](CREDITS.md) for what changed when it moved here.
