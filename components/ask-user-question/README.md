# rpiv-ask-user-question (Go port, partial)

A Go extension for PiG: the `ask_user_question` tool, which lets the model put up to four structured questions
(2-4 options each, single or multiple choice, an optional preview per option) to you when it would otherwise guess.
Every question gets a "Type something." row for a free-text answer; dismissing a dialog declines the questionnaire.

It is a port of `@juicesharp/rpiv-ask-user-question` 2.11.0 (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

## What is ported

This is **slice A**: the tool and everything the model sees and the equivalence checks can reach.

- The tool, its JSON Schema (limits enforced by the host), its description and prompt guidance, and the config
  overrides (`~/.config/rpiv-ask-user-question/config.json`: `guidance.description`, `guidance.promptSnippet`,
  `guidance.promptGuidelines`).
- Line-terminator normalisation, the validation (reserved and duplicate labels, option counts), the answer envelope
  and decline text, the `rpiv:ask-user:prompt` and `rpiv:ask-user:blocked` events.
- The sequential dialog walker (one select or input dialog per question), which is what the original runs on RPC hosts.
  **In a terminal this port runs the same dialogs**, where the original shows its tabbed questionnaire.
- Removing the tool from the active set on hosts without a UI (`before_agent_start`).

**Not ported yet (slice B):** the interactive questionnaire (tabs, per-option notes, submit tab, side-by-side preview
pane, external editor, collapse key), the locales, and the terminal bell. See [port/PORT.md](port/PORT.md).

```sh
pig install ./components/ask-user-question
```
