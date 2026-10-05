# TECH — Questionnaire: custom-answer input + all-options-in-frame

Date: 2026-08-10
Spec: `specs/2026-08-09-questionnaire-custom-answers-and-options/`
File: `extensions/goal-questionnaire.ts`

## V1. Defect: custom-answer text input not accepted (IME / composed input)

### Symptom

After pressing "Write your own answer..." and Enter, the user's text input is
not accepted by the agent — composed/IME input (e.g. CJK) cannot commit.

### Reproduction

Drive the dialog component at rows=24 (mock TUI):

```
ARROW_DOWN ×3 → "Write your own answer..."  → Enter (input mode)
→ render: " Your answer:" present, but:
  - no CURSOR_MARKER ("\u001B_pi:c\u0007") in any rendered line
  - setShowHardwareCursor calls: [false] at open, still [false] in input mode,
    no true until submit()
```

ASCII typing does land at the component level (probe: typing "hello" renders
in the editor), so the dispatch path works; the defect is the missing cursor
anchoring that IME/composed input depends on.

### Root causes (code locations)

1. **Container not `Focusable`** — `runGoalQuestionnaire`'s `ctx.ui.custom`
   factory returns `{ render, invalidate, handleInput }` (~line 831) with no
   `focused` property and no propagation to the embedded `Editor`
   (`new Editor(tui, editorTheme)`, ~line 367). pi-tui's `Editor` only emits
   `CURSOR_MARKER` when `focused === true` (pi-tui `dist/components/editor.js`:
   `const emitCursorMarker = this.focused`). `focused` is only set by the TUI
   when focus moves; inside a custom dialog nothing ever sets it.
2. **Hardware cursor force-hidden for the whole dialog** — `tui.setShowHardwareCursor(false)`
   at open (~line 324) with restore only inside `submit()` (~line 376). While
   typing in input mode the hardware cursor stays suppressed.
3. Per pi `docs/tui.md` (*Focusable Interface (IME Support)*, *Container
   Components with Embedded Inputs*): "When a container component (dialog,
   selector, etc.) contains an `Input` or `Editor` child, the container must
   implement `Focusable` and propagate the focus state to the child.
   Otherwise, the hardware cursor won't be positioned correctly for IME
   input." — exactly the missing piece here.

### Severity

High (user-facing correctness): the primary custom-answer path is unusable for
IME users and gives no cursor feedback for anyone.

## V2. Defect: not all options immediately viewable

### Symptom

At rows=24 / baseFrame=19 (`computeDialogLineLimit` → 10-line bound), a
question with 5 options + the custom row shows only options 1–4 plus
`… +4 more · PgUp/PgDn scroll`; the remaining options (and the "Write your own
answer..." row) are behind the scrollport and require arrow/Page keys.

### Reproduction

```
openQ([{question, options: [5 options]}], 24, 19) → render(100):
  0: border
  1: question
  2: auditor line
  4: > 1. Option one
  6:   2. Option two
  7:   3. Option three
  8:   4. Option four
  9: … +4 more · PgUp/PgDn scroll
```

### Root cause (code location)

`fitDialogViewport` (~line 167) windows ALL dialog lines (question + context +
options + footer) behind `scrollTop`; select-mode question tabs hand it a
`DialogScrollState` (~line 819). The initial view is top-aligned, so the tail
options are clipped by the 10-line bound and reachable only by scrolling
(PageUp/PageDown, Ctrl+↑/↓, or ↑/↓ auto-follow). This is the
`specs/2026-08-09-questionnaire-options-scroll` design (reachability by
scrolling) — the user has since decided the opposite: **all options must be in
the initial frame**; the question/context section yields first.

### Severity

Medium-high UX: forces arrow-key scrolling for every bounded questionnaire;
the custom-answer row (last) is the most likely to be hidden.

## V3. Fix design

### F1 — input-mode cursor anchoring (t2-fix-input)

- Make the returned dialog component implement `Focusable`:
  ```ts
  focused = false;  // set by TUI when focus changes
  set focused(v: boolean) {
      this._focused = v;
      this.editor.focused = v && inputMode;
  }
  ```
  (per docs/tui.md container-with-embedded-input pattern).
- Toggle the hardware cursor with the editor surface: on entering input mode
  call `tui.setShowHardwareCursor(true)`; on leaving (exitEditor / submit /
  answer saved) restore to the dialog's own baseline (off in select mode,
  restored to `wasHardwareCursorShown` on close). Keep the submit() restore.
- The Editor emits `CURSOR_MARKER` automatically once `focused === true`;
  verify via a raw render in tests.
- Keep ASCII dispatch as-is (already works); no direct-typing shortcut
  (user decision).

### F2 — all options in frame; question yields (t3-fix-options)

- In select-mode question tabs, compute a protected region that always
  includes: top border + **every option line** (optionRanges covers them) +
  footer hint + bottom border. Fit the question/context into the remaining
  budget (wrap tighter; if exhausted, the question line yields — it stays in
  the agent transcript; context already has its own scrollback presentation).
- Keep the churn-guard invariant (`<= maxDialogLines`).
- Only when even the full option block cannot fit (pathological: more options
  than terminal lines) fall back to the existing `fitDialogViewport` scroll.
- Input mode, submit tab, and proposal confirmation keep their current fit
  behavior (proposal segments unchanged).

### Tests (tests/goal-questionnaire.test.ts + tui-test-utils)

- Input mode: rendered editor line contains `CURSOR_MARKER` when the
  component is focused; `setShowHardwareCursor(true)` issued on entering input
  mode and reverted on exit; ASCII typing lands; Enter submits a `wasCustom`
  answer.
- Options: rows=24/baseFrame=19, 5 options + custom row → all six option rows
  in the initial frame, no `PgUp/PgDn` indicator; overflow case (long
  question, many options) → options still in frame, question gives way,
  dialog `<= maxDialogLines`; proposal-confirmation goldens unchanged.
