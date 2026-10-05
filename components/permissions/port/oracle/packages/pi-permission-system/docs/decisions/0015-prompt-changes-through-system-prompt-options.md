---
status: accepted
date: 2026-10-02
---

# 0015 — Prompt changes go through `systemPromptOptions`, never a returned prompt

## Status

Accepted.
Supersedes [ADR 0014](0014-tool-surface-is-node-local-prose.md).

## Context

ADR 0014 relocated the tool surface: on every `before_agent_start`, this extension removed the tool list and rules Pi wrote near the top of the prompt and rendered this session's own at the end.
It returned the result as `{ systemPrompt }`, which kept a subagent child's inherited identity byte-identical to its parent's.

A returned prompt is not a text edit Pi composes with anything else.
`ExtensionRunner.emitBeforeAgentStart` stores it as `forceSystemPrompt`, and `buildSystemPromptState` then sends that string as the whole prompt, with no sections.
Every handler later in the chain still runs, but an edit it makes to `systemPromptOptions` no longer reaches the provider.
Built-in extensions load after file extensions, so every one of them is later in the chain.

Pi 0.99.2's `builtin:mcp` lists `codemode` and `deferred` MCP servers by adding a `sections.mcp_servers` entry from its own handler.
With this extension loaded, the count of `<mcp_servers>` in the first provider request was 0, against 1 without it, measured on Pi 0.99.2 and 1.0.0 with one configured server ([#999]).
The model was never told the server existed.

ADR 0014's [#962] amendment had considered the options route and set it aside: no option removes Pi's `<tools>` and `<rules>`, so the surface could not be relocated that way.
That is still true.
What changed is the price of the string route.

## Decision

The `before_agent_start` handler never returns `systemPrompt`.
It states every prompt change by mutating `event.systemPromptOptions`, which Pi renders after the whole chain has run.
Pi documents this as the preferred path ("Prefer changing prompt sections, selected tools, or guidelines so Pi can append a transcript delta").

| Node           | Prompt         | Tool surface                                                             | Skills                                                  |
| -------------- | -------------- | ------------------------------------------------------------------------ | ------------------------------------------------------- |
| root           | Pi-authored    | nothing beyond `setActiveTools`; Pi renders `<tools>`/`<rules>` in place | denied skills removed from `systemPromptOptions.skills` |
| root           | custom         | nothing; Pi writes no tool surface there ([#980])                        | same                                                    |
| subagent child | custom, always | `sections.tools` and `sections.rules`, which Pi places after `<cwd>`     | same                                                    |

- On a prompt Pi wrote, narrowing the active set is enough.
  From Pi 0.86, the runner sets an unedited `selectedTools` to the live active set after the chain, and Pi renders both `<tools>` and the tool-dependent `<rules>` bullets from it.
- A child states its own surface from the same parts ADR 0014 rendered from (`toolSnippets`, each allowed tool's `promptGuidelines`, and the `promptGuidelines` no registered tool contributes), now as untagged section contents.
  An empty `tools` section, written when no allowed tool has a snippet, clears a peer's list, because Pi leaves an empty section out.
- Denied skills are judged on `systemPromptOptions.skills` itself, by name, never on the rendered prompt.
  The prompt a handler reads is rendered before that turn's tool changes, and Pi writes `<skills>` only when `read` or `bash` is selected, so on the turn either returns from a full denial that prompt lists no catalogue while the one Pi renders afterward does.
- Skill entries for the path gate are still parsed from the rendered prompt, read before `skills` is narrowed, so they cover every catalogue the prompt lists, including one Pi did not render.

The peer floor is Pi 1.0.0 ([#970]), so there is no string path left for older hosts.
Both prompt layouts ADR 0014 handled, the ≤0.85 header shape and the 0.86 section shape, are deleted.

## Consequences

- **Sections another extension adds reach the provider.**
  `<mcp_servers>` is the case that surfaced this; any extension adding a section from `before_agent_start` gets the same fix.
- **The inherited prefix is retired.**
  The root's tool list now sits where Pi writes it, inside the region `@gotgenes/pi-subagents` copies into a child.
  `@gotgenes/pi-subagents` cuts the parent's `<tools>` and `<rules>` out of that region ([#1009], released before this change), so a child still states only its own tools.
  Parent and child now share only the preamble, about 171 characters (measured during [#890]'s planning, not re-measured here), where ADR 0014 kept the whole identity shared.
  The loss falls only on hosts that reuse the leading system text ([#180]); Anthropic's cache was never reached by it (ADR 0014).
  The operator chose this over the alternative below on [#999].
- **Version skew.**
  This package paired with a pi-subagents release that predates [#1009] gives a child two tool lists: the parent's, inherited, and its own.
  Nothing can enforce the pairing, because neither package depends on the other.
- **A restored tool is listed on the turn it returns.**
  From Pi 1.0, `toolSnippets` holds every registered tool's snippet, and Pi renders `<tools>` from the reconciled active set, which ends ADR 0014's one-turn lag.
- **Accepted residual: a denied skill in text Pi did not render stays visible.**
  The old string pass removed denied entries from every `<available_skills>` block in the prompt.
  The options pass reaches only the catalogue Pi renders from `systemPromptOptions.skills`, so a skill an operator listed in their own `SYSTEM.md` or `--append-system-prompt` text stays visible.
  This follows [#919]/[#932]: this extension does not edit text it did not write.
  Exposure is not authorization; the skill `input` gate and the path gate's skill-entry match still enforce `deny`.
- **Not defended: another extension's explicit `selectedTools` edit.**
  Pi lets an explicit edit win over the live loadout, which could put back a tool this extension withheld.
  That was already true under ADR 0014, which changed only the prompt text; the `tool_call` gate still blocks the call. earendil-works/pi#9932 (open) reports the neighboring forced-prompt defect.

## Alternatives considered

- **Pointer sections.**
  A custom `sections` entry whose name matches a built-in replaces that built-in's content in place.
  Writing constant text into Pi's `<tools>`/`<rules>` (identical in every node) and the real surface into new tail sections would have kept the prefix with no forced prompt.
  It was declined as new mechanism: two invented section names, a pointer sentence the model must follow, and a coupling to Pi's replace-in-place rule.
- **Keep the forced prompt.**
  Declined: the defect stays until Pi offers a way to remove its built-in sections, and any later extension's section is lost meanwhile.
- **Suppress Pi's built-ins by setting `customPrompt` to Pi's own preamble.**
  Not offered: other extensions read `customPrompt` as the operator's own text, among them `@gotgenes/pi-subagents`' portable prompt and this extension's [#980] branch.

[#180]: https://github.com/gotgenes/pi-packages/issues/180
[#890]: https://github.com/gotgenes/pi-packages/issues/890
[#919]: https://github.com/gotgenes/pi-packages/issues/919
[#932]: https://github.com/gotgenes/pi-packages/issues/932
[#962]: https://github.com/gotgenes/pi-packages/issues/962
[#970]: https://github.com/gotgenes/pi-packages/issues/970
[#980]: https://github.com/gotgenes/pi-packages/issues/980
[#999]: https://github.com/gotgenes/pi-packages/issues/999
[#1009]: https://github.com/gotgenes/pi-packages/issues/1009
