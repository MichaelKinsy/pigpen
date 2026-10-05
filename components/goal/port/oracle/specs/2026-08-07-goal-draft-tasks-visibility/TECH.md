# Tech: Complete goal presentation — protected tasks in the dialog frame + scrollback via renderCall

Spec: `2026-08-07-goal-draft-tasks-visibility`

## Problem

Two surfaces both omitted the tasks when the goal was presented:

1. **Dialog frame.** `runGoalQuestionnaire` renders into pi's editor slot. The
   churn guard bounds the dialog to the terminal height (frame-overflow
   invariant, see 2026-08-04-goal-confirmation-scroll-fix). The pre-fix
   context-heavy branch of `fitDialogLines` kept the head (border + question)
   and the tail (options/footer/bottom border) and sliced everything between —
   including the "Tasks proposed for confirmation:" section. The user confirmed
   a draft without ever seeing the task plan.
2. **Scrollback.** `propose_goal_draft`'s `renderCall` rendered only
   "propose_goal_draft <objective>" — no task list — and the durable proposal
   summary (`buildProposalSummary`) is written to the transcript only after the
   dialog decision.

## Design

### Channel 1 — dialog frame: protected tasks middle

The render already records a structural `protectedCount` (lines up to the
question line). Two additions:

- `render()` tracks `optionsStartIndex` — set when the options block begins —
  as the structural tail anchor (robust under themed/ANSI rendering and
  wrapping).
- At the fit site, when the dialog is a proposal confirmation (single
  select-mode question with context that contains the tasks marker
  "Tasks proposed for confirmation:" or the tweak box "┌─ TASKS ─"),
  `findProposalPresentationSegments` locates the tasks segment (marker line
  through the last `[ ]` / `[x]` / `[~]` line, contiguous) and the tail start.

`fitDialogLines` gains a proposal branch (new optional
`ProposalPresentationSegments` parameter, default null):

1. Under the bound → return unchanged (byte-identical to 383ae52).
2. Otherwise keep `head + tasks + tail` (options/footer/bottom border),
   sacrificing only the objective-box middle in-frame — the full objective is
   always in the scrollback presentation, so nothing of the goal is omitted.
3. When room is short: strip blank spacing lines below the head first, then
   drop task lines from the end (never options/footer/border first); the
   fallback caps the tail from its end (border/footer first) so the output
   **never exceeds `maxDialogLines`** in any branch.

### Channel 2 — scrollback: complete tool-call display ("the user can just scroll")

`propose_goal_draft`'s `renderCall` now renders the full objective verbatim
plus the complete task list: "Tasks proposed for confirmation:" with every
`[ ] id: title` line for explicit tasks, or "Tasks derived from the objective
(confirm or ask the agent to adjust):" for the F2 derived fallback, plus
auto-continue / block-completion notes when set. pi renders the tool-call
display into the transcript the moment the tool call starts — *before* the
dialog opens — so the complete goal is in the buffer and scrollable while the
dialog is open. `renderCall` is existing `ToolDefinition` API; no pi-tui /
pi-coding-agent changes.

### Channel 3 — drafting prompt

`goalDraftingPrompt` requires the agent to write the COMPLETE goal — every
section (objective, success criteria, boundaries, constraints, verification
contract) and the full task list — into its message before calling
`propose_goal_draft`, so the user can scroll up and re-read it while the
dialog is open. Omission is forbidden. (Soft guarantee; `renderCall` is the
deterministic one.)

## Why renderCall (alternatives)

- **Pre-dialog transcript emission via `pi.sendMessage` / `appendEntry`:** not
  available on the tool `ExtensionContext` (command-context only). Rejected.
- **Post-decision summary only:** arrives after the user already decided; also
  omits the contract sections. Still emitted (as before) but not the channel.
- **Prompt-only:** soft guarantee. `renderCall` is deterministic and is the
  hard guarantee; the prompt is belt-and-suspenders.

## Invariants

- `fitDialogLines` output length ≤ `maxDialogLines` in every branch — the
  proposal fallback caps the tail from its end (bottom border, footer, then
  options) before spending room on tasks, so degenerate budgets can never
  overflow the bound.
- Under the bound, `render()` output is byte-identical to 383ae52 (the slice
  never runs).
- Proposal detection is structural (tasks marker line + `[ ]`-style task lines
  + options start index), not content-regex over styled lines.
- The protected head (top border + question) is always visible; the bottom
  border is always the last rendered line; the options/footer stay visible
  whenever the bound allows; tasks are never silently omitted from the overall
  presentation (frame and/or scrollback).

## Alternatives considered

- **Reordering tasks to sit directly after the question line** (option offered
  during drafting): not chosen — the protected-middle keeps the existing
  layout for content that fits and only reorganizes under pressure.
- **Paging the context (PgUp/PgDn/←/→):** user-rejected direction
  (2026-08-04-goal-confirmation-scroll-fix) — "the user can just scroll".
- **Removing the churn guard to show the full dialog:** would trigger pi's
  shrink full-render on close and erase scrollback — the opposite of scrolling.
  Explicitly NOT an option per user direction.
