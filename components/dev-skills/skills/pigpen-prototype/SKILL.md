---
name: pigpen-prototype
description: Build a throwaway prototype that answers a design question. Use when the user wants to sanity-check whether a state model or logic feels right, or explore what a UI should look like.
---

Adapted from Matt Pocock's `prototype` skill (MIT), condensed; see the Package's
CREDITS.md.

A prototype is **throwaway code that answers a question**. The question decides the
shape, so identify it first, from the prompt, the surrounding code, a decision record,
or by asking:

- "Does this logic or state model feel right?" leads to [LOGIC.md](LOGIC.md): a tiny
  interactive terminal app that pushes the state machine through cases that are hard
  to reason about on paper.
- "What should this look like?" leads to [UI.md](UI.md): several radically different
  variations on one route, switchable at runtime.

If the question is ambiguous and the user is not reachable, pick the branch that
matches the surrounding code (a backend module means logic, a page or component means
UI) and state the assumption at the top of the prototype.

## Rules for both branches

1. **Throwaway from day one, marked as such.** Put the code next to the module or page
   it prototypes so context is obvious, named so a casual reader sees it is not
   production. Follow the project's existing routing and runner conventions; invent
   nothing top-level.
2. **One command to run.** The user starts it without thinking.
3. **No persistence by default.** State lives in memory. If the question is about
   persistence, use a scratch store with a wipe-me name.
4. **Skip the polish.** No tests, no abstractions, no error handling beyond runnable.
   The least code that answers the question. Speed of learning is the whole point.
5. **Surface the state.** After every action, print or render the full relevant state
   so the user sees what changed.
6. **Capture, then discard.** Fold the validated decision into the real code or the
   ticket it answers. Commit the prototype itself to a throwaway branch off the main
   line and leave a pointer from the ticket. If the decision is hard to reverse and
   surprising, record it as a decision record. The main branch keeps only the
   validated decision, never the prototype.
