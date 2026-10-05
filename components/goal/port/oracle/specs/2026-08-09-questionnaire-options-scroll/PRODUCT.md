# PRODUCT — Questionnaire options must never be hidden

Date: 2026-08-09
Spec: `specs/2026-08-09-questionnaire-options-scroll/`

## Problem

The goal questionnaire (`goal_questionnaire` tool, whose custom dialog UI is
also used for proposal confirmation) does not show all options when the dialog
is height-bounded. Reproduced with a long question + 2 options at a 9-line
bound: only option 1 (the recommended one) renders; option 2 is silently
dropped. Because the questionnaire's transcript presentation is only the tool
name (`renderCall() { return new Text("goal_questionnaire", 0, 0); }`), there is
**no scrollback fallback** — a hidden option is a permanently blind option:
the user cannot see, let alone choose, it.

## Product decision (user-selected)

When the question + context + all options exceed the terminal-height bound:

- The **question and context are never truncated** (no `…` ellipsis slicing).
- The dialog keeps the **full content** and becomes **internally scrollable**
  so every option is reachable.
- The **churn-guard invariant is preserved**: the dialog frame never exceeds
  the terminal height; scrolling is implemented within the bound, not by
  removing it.

## Behavior contract

1. **Reachability**: every option line of the active question is reachable by
   scrolling. No option may ever be permanently hidden or sliced mid-block.
2. **No truncation**: question, context, and option text is never ellipsized.
   Content is windowed, not rewritten.
3. **Initial view**: top-aligned (top border, tabs, question start, first
   options) so the recommended/first option is visible immediately; a bottom
   edge indicator (`… +N more · PgUp/PgDn scroll`) tells the user content is
   clipped and how to reach it.
4. **Scroll interaction** (select-mode question tabs and the submit summary
   tab):
   - `PageUp` / `PageDown` page the viewport without moving the selection.
   - `Ctrl+↑` / `Ctrl+↓` scroll one line without moving the selection.
   - `↑` / `↓` selection auto-follows: the viewport nudges so the selected
     option stays visible.
   - Edge indicators: `▲ N more` (dim) replaces the top content line when
     scrolled; `… +N more · PgUp/PgDn scroll` occupies the reserved bottom
     edge row when clipped below, giving way to the bottom border at the end.
5. **Keybinding stability**: `↑`/`↓` select, `Tab`/`←→` tabs, `Enter` confirm,
   `Esc` cancel, `a` auditor toggle all keep working. New keys do not collide.
6. **Input mode**: unchanged — the editor and option hints stay visible
   (tail-keep), because the editor is the active surface.
7. **Proposal confirmation**: keeps its existing segment protection (head +
   tasks + auditor + options within the bound) and its scrollback presentation
   fallback for the sacrificed objective-box middle. No regression.

## Success criteria

1. Audit (TECH.md V1–V7) documents every questionnaire option-hiding vector
   with code location, severity, and the reproduced scenario (long question +
   2 options, 9-line bound).
2. Bounded select-mode questionnaire (repro scenario) shows the full question
   and makes **every option reachable** via scroll — option 2+ visible after
   scrolling; nothing truncated; no option dropped mid-block.
3. Scroll keys work (PageUp/PageDown page, Ctrl+↑/↓ line), selection
   auto-follows, `▲`/`… +N more` indicators show when clipped, footer/edge
   hint advertises the scroll affordance.
4. Churn-guard invariant preserved: fitted dialog never exceeds
   `maxDialogLines` at any scroll position.
5. Regression tests use ANSI-styled lines (mock theme hides this bug class)
   and cover every item above.
6. Full suite + `tsc --noEmit` pass; spec docs per AGENTS.md order.

## Out of scope

- Changing proposal-confirmation segment protection (task lines / auditor
  line) — already fixed and tested.
- Other TUI widgets' scrolling, removing the churn-guard bound, mouse-wheel
  input (pi-tui exposes no wheel key), changing existing keybindings.
