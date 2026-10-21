---
name: pigpen-grilling
description: Grill the user relentlessly about a plan, decision, or idea. Use when the user wants to stress-test their thinking, or uses any 'grill' trigger phrases.
---

Adapted from `skills/productivity/grilling/SKILL.md` of mattpocock/skills at d81f3a1 (MIT).
The changes are listed in the Package's CREDITS.md. This version asks one question at a
time, where the original asks the whole frontier in one numbered round.

Interview the user relentlessly until you reach a shared understanding. Map this as a **design tree**: every decision branches into the decisions that hang off it.

Work the tree **one question at a time**. The **frontier** is every decision whose prerequisites are already settled: the questions you can ask _now_ without guessing at answers you haven't heard yet. Pick the one question on the frontier that most unblocks the rest, ask it, and wait for the answer before the next. Never put more than one substantive decision in one question: a batch lets the load-bearing one hide among the easy ones. If a structured-question tool is available (for example `ask_user_question`), use it; otherwise ask in chat.

Format a question like so:

```
❓ **Q1** - **<question title>**: <question body, might be multiple paragraphs, including multiple choices>

➡️ <your recommended answer, with your reasoning>
```

Present verified context and consequences rather than unexplained labels. When choices are offered, always allow a free-text answer. Propose a concrete criterion to confirm rather than asking an open question.

Each answer reshapes the tree: settled decisions push the frontier outward and unblock questions that depended on them. Recompute the frontier and ask the next question. A question whose answer depends on another question still open belongs to a _later_ turn, not this one.

Finding _facts_ is your job, never the user's. When a question needs a fact from the environment (filesystem, code, tracker, tools), look it up yourself, or dispatch a sub-agent if your setup has them; don't ask the user for anything you could look up, such as a revision, a file path, a retry, or a validation fix. The _decisions_ are the user's: put each to them and wait.

If the project has a glossary (`GLOSSARY.md` or `CONTEXT.md`), use its canonical terms. When the user's answer introduces a conflicting term, stop and resolve the term before continuing.

The session is done when the frontier is empty: every branch of the design tree visited, nothing left silently assumed. Do not act on it until the user confirms you have reached a shared understanding. Record the accepted answers and their rationale where the project keeps decisions.
