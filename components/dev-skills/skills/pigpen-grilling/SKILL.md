---
name: pigpen-grilling
description: Interview the user relentlessly about a plan, decision, or idea until shared understanding is reached. Use when the user wants their thinking stress-tested or says "grill".
---

Adapted from Matt Pocock's `grilling` skill (MIT); see the Package's CREDITS.md. This
version asks one question at a time.

Interview the user about every aspect of the plan until you reach a shared
understanding. Walk each branch of the decision tree, resolving dependencies between
decisions one by one.

Ask one question at a time and wait for the answer. If a structured-question tool is
available (for example `ask_user_question`), use it; otherwise ask in chat. Never put
more than one substantive decision in one question: a batch lets the load-bearing one
hide among the easy ones.

For each question, give your recommended answer with your reasoning, and present
verified context and consequences rather than unexplained labels. When choices are
offered, always allow a free-text answer. Propose a concrete criterion to confirm
rather than asking an open question.

If a fact can be found in the environment (the filesystem, the code, the tracker),
look it up instead of asking. Decisions belong to the user; facts do not. Never ask
the human for something you can resolve yourself: a revision, a file path, a retry, a
validation fix.

If the project has a glossary (`CONTEXT.md`), use its canonical terms. When the user's
answer introduces a conflicting term, stop and resolve the term before continuing.

Do not act on the plan until the user confirms the understanding is shared. Record the
accepted answers and their rationale where the project keeps decisions.
