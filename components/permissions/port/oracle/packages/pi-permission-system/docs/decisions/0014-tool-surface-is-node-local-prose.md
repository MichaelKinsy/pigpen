---
status: superseded
date: 2026-09-08
---

# 0014 — A session's tool-surface prose is its own, rendered at the tail

## Status

Superseded by [ADR 0015](0015-prompt-changes-through-system-prompt-options.md).
The relocation below was carried by a returned `systemPrompt`, which Pi sends as the whole prompt and which therefore dropped the sections later extensions add, `<mcp_servers>` among them.
ADR 0015 states every prompt change through `systemPromptOptions` instead, and gives up the inherited prefix this decision kept.
The rationale below remains useful context.

## Context

This extension narrows what the agent is shown before it starts: denied tools are removed from the active set, and the `Available tools:` listing is narrowed to match so inherited tool documentation does not lie to the agent about its own capability.

Until now it did that by rewriting `event.systemPrompt` in place — filtering the bullets out of the section Pi wrote, and pruning the tool-dependent bullets out of `Guidelines:` beneath it.

Both sections sit near the top of Pi's preamble, at offset 171 of the assembled prompt.
`@gotgenes/pi-subagents` copies everything ahead of the skills catalogue into a child's prompt verbatim ([pi-subagents ADR 0006]), specifically so the child's leading bytes match its parent's — a property [#180] and [#400] exist to create, measured by [#180]'s reporter at 8,333 shared tokens worth roughly 40 seconds of prompt processing on a local model.

So the rewrite edited bytes a child inherits, and everything after the first changed bullet diverged.
Measured in this repo's configuration during [#890]'s planning: a child sharing a 57,423-character identity with its parent shared **365 characters** of it.

The rewrite was not wrong and neither was the prefix.
The conflict was arithmetic: a shared identity and an honest per-session tool list cannot both hold while the list lives inside the identity.

Two further facts bounded the choice.

Pi's `buildSystemPrompt` writes **no** `Available tools:` section at all under `customPrompt`, which is how a child's prompt is assembled — so the inherited copy was the child's only tool prose, and narrowing it in place was the only thing keeping it honest.

And the prefix is worth less than [#180] implies on some providers: Anthropic builds its cache prefix in the order `tools`, `system`, `messages`, and "modifying tool definitions … invalidates the entire cache", so a child whose tool array differs from its parent's never had a cache hit for its system prompt to lose.
The property pays where tool definitions are rendered *after* the system text — local inference engines with such a chat template, which is [#180]'s own constituency.

## Decision

The tool surface is **relocated, not narrowed**.

On every `before_agent_start`, in every node, `renderToolSurface` (`src/exposure/tool-surface-prompt.ts`):

1. removes the `Available tools:` section, Pi's "In addition to the tools above…" filler sentence, and the `Guidelines:` section, wherever Pi or this package wrote them (see the amendment below); and
2. renders this session's own, from parts, at the **end** of the prompt — past Pi's `Current working directory:` footer (from pi 0.86, its `<cwd>` section; see the section-shape amendment below), and so past everything a child inherits.

Each node then states its own tool surface, and no node edits another's bytes.

### Rendered from parts, not narrowed from text

The block is built from `event.systemPromptOptions.toolSnippets` intersected with the allowed set, and from each allowed tool's own `promptGuidelines` read off `pi.getAllTools()`, around Pi's three built-in bullets.

Rendering rather than filtering is what makes the child case work at all: a child's inherited identity carries no tool section to narrow, because its parent's node already relocated it.
A subtractive implementation would leave such a child with no tool prose.

It also retires a hard-coded table of eight exact Pi sentences that the old implementation matched by literal text.
That table attributed no third-party tool's guidelines — it could only recognize Pi's own wording — and would have broken silently on any upstream rewording.
Reading `ToolInfo.promptGuidelines` per tool is Pi's own attribution, so a `@gotgenes/pi-colgrep` bullet is now filtered with its tool like any built-in.

### Every node, not only children

The relocation is unconditional.
Applying it in children alone would leave the parent's list at offset 171 and the child's elsewhere, so the two identities would diverge there — 171 shared characters, worse than the 365 this change is fixing.

The cost is that every user of this extension sees the tool list move to the end of the prompt, whether or not they ever spawn a subagent, and whether or not anything is denied.
That is accepted: the block's position is not a documented contract, the model's authoritative capability list is the request's `tools` array either way, and recency arguably favors the tail.

### The block is always rendered

The handler therefore always returns a system-prompt override, where it previously returned `{}` when nothing was removed.
This is not a new cost — the override was already re-emitted every turn so skill filtering is reapplied ([#437]).

## Consequences

- A child's prompt states the tools it actually holds, covering **both** narrowings — `pi-subagents`' `tools:` allowlist and this package's policy — because a child's `toolSnippets` is already derived from its constructed tool set.
- The identity a child shares with its parent is the full identity minus the relocated sections, rather than the 365 characters it had been.
- A tool restored by a relaxed rule is still advertised one turn late: `toolSnippets` is rebuilt by Pi from the tools active at its last prompt build, so a tool withheld last turn has no snippet to render this turn.
  Unchanged by this decision.
- **Residual resolved in [#919] / [#932] — removal is now bounded to what Pi or this package wrote.**
  The two headers were matched on a line's *trimmed* text with nothing tying them to Pi's authorship, and the removed region ran to the next line ending in a colon.
  Both reporters hit it through a custom `SYSTEM.md`, where Pi writes no tool surface of its own, so every match was theirs: one prompt lost its tool list, both of its guideline bullets, its own trailing instruction, and Pi's `<project_context>` **opening** tag — swept because Pi's lead-in `Project-specific instructions and guidelines:` ends with a colon.
  The fix is the one this bullet nominated.
  The prompt is split at Pi's `Current working directory:` footer, which it writes last and unconditionally in both branches — the anchor `pi-subagents` uses for the skills catalogue.
  Above it, sections are removed only when `systemPromptOptions.customPrompt` is absent, because that is exactly when Pi wrote a preamble of its own; below it, always, since that region holds this package's own block and any peer's under [#901].
  A section is also bounded to its own body, so no match can sweep the prose after it.
- **Residual resolved in [#980]: an operator's custom prompt no longer receives a block.**
  [#919] kept appending this session's block after a custom prompt, so an operator who wrote their own tool list was shown two, and one who wrote none was shown sections Pi deliberately omits.
  It chose that over standing aside because every subagent child is a `customPrompt` session too, and the block is a child's only tool prose.
  See the amendment below for how the two cases are now told apart.
- **Accepted residual:** the prompt is still split on LF and the assembled body still `trimEnd`ed, so a CRLF-authored `SYSTEM.md` is not returned byte-for-byte even when nothing is removed.
- **Accepted residual:** a child running without this extension installed still inherits its parent's list, because nothing then relocates or restates it.
  Tracked as [#901], which records the contract a second writer must honor to stay order-independent with this one: membership from the live registry (`pi.getActiveTools()`, the one input that changes mid-chain), text from `toolSnippets`, guidelines from `getAllTools()`, and idempotent remove-then-render so the last writer in the chain is correct in either order.
- **Amendment in [#962]: pi 0.86's section-shaped prompt.**
  Pi 0.86 writes an untagged preamble followed by `<name>` sections joined by a blank line: the tool list is a `<tools>` section, the guidelines a `<rules>` section, and the working directory a `<cwd>` section in place of the footer.
  None of the three anchors above matched, so the whole prompt was read as head.
  Pi's unfiltered list then stayed at the top, the narrowed block landed below it, and in a Pi-authored prompt the first `Guidelines:` section in an AGENTS.md was removed as though it were Pi's.
  The layout is now decided once per prompt, from whichever cwd layer sits later (a footer or a `<cwd>` section quoted in a context file is always above Pi's own), never by version.
  On the section shape the head is searched only for Pi's `<tools>` and `<rules>`, each removed only when it closes before the first of `<docs>`, `<addendum>`, `<project_context>`, `<skills>`, and `<cwd>`, which is where Pi writes them.
  The block is rendered in the prompt's own shape, and the tail removes a relocated block in either shape, so the [#901] contract holds for a second writer on either shape.
  Removing Pi's `<rules>` would also drop the rules other extensions add through `systemPromptOptions.promptGuidelines`, so the relocated rules carry every such bullet no registered tool contributes; through 0.85 that field is the tools' own guidelines, and the filter leaves nothing.
  The section shape could have been reached through `systemPromptOptions` instead of the text, but no option removes Pi's `<tools>`/`<rules>` short of `customPrompt`, and a custom `tools` section replaces Pi's in place, inside the inherited region.
- **Amendment in [#980]: a root node under a custom prompt states no tool surface.**
  When Pi built the prompt from `systemPromptOptions.customPrompt` and `SubagentDetector.isSubagent(ctx)` answers `false`, the handler skips this pass and hands the prompt to the skill filter exactly as Pi built it, matching Pi, which writes no tool list or rules there.
  A node the detector recognizes as a child still renders its block, so "The block is always rendered" now reads: in every node whose prompt Pi authored, and in every subagent child.
  Such a root returns an override only when skill filtering changed the prompt, and `{}` otherwise.
  Both detection errors land on an existing behavior: a root misread as a child keeps the block it had before this amendment, and a child nothing recognizes gets Pi's native `customPrompt` prompt, with its tools still listed in the request's `tools` array.
  The inherited identity is unaffected, since the dropped block lay past everything a child copies.
  Suppressing the block in children too was considered and deferred until someone asks: a child cannot see its root's `customPrompt`, so it would need a cross-node signal or a config switch.
- A shared prompt-composer that owns prompt layout for every extension editing this string is the direction this points at; four packages currently anchor on Pi's literal section headers.
  Not built, and no issue filed.

### Interaction with prompt-projecting providers

`pi-claude-bridge` recovers a child's inherited region by searching the child's prompt for a captured parent prompt, to project only the parent's portable parts onto another harness ([#883], [pi-claude-bridge#88]).

Read against the published 0.7.0 tarball, `findInheritedPrompts` keys on `parent.assembledPrompt` — the parent's **full** assembled prompt — and looks for it as a substring of the child's.
A child never contains that, because `inheritedIdentity` truncates at the catalogue; so that matcher fails independently of anything this decision changes.
[pi-claude-bridge#89] proposes matching a tail-stripped key instead, which is the parent's identity region.

This decision makes that proposed key a verbatim substring of the child's prompt again, where the in-place rewrite had broken it at the tool list — the divergence [#890]'s reporter measured at offset 412.
**The end-to-end claim is unverified:** #89 is an open pull request, 0.7.0 is the latest published version, and no build carrying it has been exercised against this change.

[#180]: https://github.com/gotgenes/pi-packages/issues/180
[#400]: https://github.com/gotgenes/pi-packages/issues/400
[#437]: https://github.com/gotgenes/pi-packages/issues/437
[#883]: https://github.com/gotgenes/pi-packages/issues/883
[#890]: https://github.com/gotgenes/pi-packages/issues/890
[#919]: https://github.com/gotgenes/pi-packages/issues/919
[#932]: https://github.com/gotgenes/pi-packages/issues/932
[#901]: https://github.com/gotgenes/pi-packages/issues/901
[#962]: https://github.com/gotgenes/pi-packages/issues/962
[#980]: https://github.com/gotgenes/pi-packages/issues/980
[pi-subagents ADR 0006]: https://github.com/gotgenes/pi-packages/blob/main/packages/pi-subagents/docs/decisions/0006-inherited-prompt-is-identity-only.md
[pi-claude-bridge#88]: https://github.com/elidickinson/pi-claude-bridge/issues/88
[pi-claude-bridge#89]: https://github.com/elidickinson/pi-claude-bridge/issues/89
