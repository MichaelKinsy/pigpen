# Logic prototypes

The question: does this state model or algorithm feel right in the hand, not
just on paper?

Build a minimal interactive terminal app around the state machine or core
logic, real enough to poke:

- Model the states and transitions as plainly as the language allows: a
  discriminated union, an enum plus a transition function, a reducer. This
  snippet is the likely survivor; the decision-rich shape may be inlined into
  the resolving ticket, so keep it separable from the harness around it.
- A read-eval loop drives it: print the full current state, list the legal
  actions, apply the chosen one, print the new state. Illegal actions print
  why they are illegal rather than crashing, because the rejected transitions
  are half of what the prototype is checking.
- Seed it with the awkward cases from the discussion: the double-cancel, the
  refund after partial shipment, the concurrent claim. Script them as named
  scenarios the user can replay with one keystroke, so the conversation can
  point at "scenario 3" instead of re-deriving it.
- Keep the vocabulary of the project glossary (`CONTEXT.md`, if any) in state names and actions. A prototype
  that renames the domain mid-flight pollutes the discussion it exists to
  sharpen.

Done when the user has pushed the model through the awkward cases and either
trusts it or has found where it breaks. Record the verdict and the question it
settled on the ticket, then capture and discard per the rules in SKILL.md.
