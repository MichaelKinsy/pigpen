# PRODUCT — Questionnaire: custom answers must work; all options must be immediately viewable

Date: 2026-08-10
Spec: `specs/2026-08-09-questionnaire-custom-answers-and-options/`

## Problem

The shared goal questionnaire dialog (used by `goal_question`, `goal_questionnaire`,
and the draft/confirm prompt) has two user-facing failures:

1. **Custom answers cannot be written reliably.** Selecting the
   "Write your own answer..." option opens the editor, but the dialog never
   anchors a cursor for text input: the container is not `Focusable`, so the
   embedded `Editor` never receives `focused = true` and emits no
   `CURSOR_MARKER`, and the dialog force-hides the hardware cursor for its
   entire lifetime. Per pi `docs/tui.md` (*Focusable Interface (IME
   Support)* / *Container Components with Embedded Inputs*), a container that
   embeds an `Input`/`Editor` must implement `Focusable` and propagate focus,
   otherwise the hardware cursor is not positioned for IME input. Reported
   symptom: after pressing "Write your own answer...", the agent does not
   accept the user's text input (IME/composed typing, e.g. CJK, cannot
   commit).
2. **Not all options are immediately viewable.** The options list is an
   internal scrollport (from `specs/2026-08-09-questionnaire-options-scroll`):
   with a bounded terminal (e.g. 24 rows, 19-line base frame → 10-line bound),
   only the first 4 of 5 options render; the rest are behind the scroll and
   the user must use arrow keys / PageUp / PageDown to see them. The user
   wants **every option in the initial frame** — no arrow-key scrolling to
   see the full set.

## Product decision (user-selected, via goal_question)

1. **Input**: keep only the "Write your own answer..." option as the custom
   entry affordance (no direct-typing shortcut). Fix the input path so typing
   actually works, including IME/composed input.
2. **Options**: all options always fit in the first view; when question +
   options genuinely exceed the terminal-height bound, the question/context
   section yields first (it remains readable in the agent transcript); the
   dialog frame never exceeds the terminal (churn-guard invariant preserved).

## Behavior contract

1. **Cursor anchoring (input mode)**: the dialog container implements
   `Focusable` and propagates focus to the embedded `Editor` while in input
   mode; the rendered editor emits `CURSOR_MARKER` at the cursor position; the
   hardware cursor is enabled (`setShowHardwareCursor(true)`) while the editor
   is the active surface and disabled again when the dialog returns to
   select mode. ASCII typing lands; IME/composed input (CJK) commits with the
   candidate window anchored.
2. **All options in frame**: for realistic questionnaires every option (plus
   the "Write your own answer..." row when `allowCustom !== false`) renders in
   the initial view — no scrollport over the option list in the common case,
   no arrow keys needed to see the full set.
3. **Overflow policy**: when question + context + all options exceed the
   bound, options keep priority; the question/context section gives way first
   (it stays in the transcript); the dialog still never exceeds
   `maxDialogLines` (churn guard preserved). A dialog-level scroll/indicator
   may remain only as an extreme last resort for pathological content.
4. **No regressions**: proposal-confirmation dialog (`allowCustom: false`),
   multi-question tabs, auditor toggle, and existing keybindings (`↑↓`
   select, `Enter` confirm, `Esc` cancel, `Tab`/`←→` tabs, `a` auditor) all
   keep working.

## Success criteria

1. Repro (TECH.md) documents both defects with code locations and severity:
   (a) input path — missing `Focusable` propagation + force-hidden hardware
   cursor; (b) options viewport — scrollport over the option list at bounded
   heights.
2. Input-mode render emits `CURSOR_MARKER` when the editor is active; hardware
   cursor is on while typing and off otherwise; ASCII typing lands and the
   submitted custom answer reaches the tool result (`wasCustom: true`).
3. With rows=24/baseFrame=19 and 5 options + custom row, ALL six rows are in
   the initial frame; no `PgUp/PgDn` scroll indicator appears for realistic
   content.
4. Overflow case (long question/context, many options, short terminal): all
   options remain in frame; the question/context section yields; dialog never
   exceeds `maxDialogLines`.
5. Proposal-confirmation goldens unchanged; full suite + `tsc --noEmit` pass;
   spec docs per AGENTS.md order.

## Out of scope

- Adding a direct-typing shortcut (user chose option-only).
- Changing pi-tui core (`Editor`, `CURSOR_MARKER`, focus machinery).
- Other TUI widgets' scrolling; mouse-wheel input; new keybindings.
- The "Write your own answer..." option's existence/positioning beyond
  always-visible-when-in-frame.
