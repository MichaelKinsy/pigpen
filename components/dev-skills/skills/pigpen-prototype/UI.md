# UI prototypes

The question: what should this look like, out of a genuinely open space?

Build several **radically different** variations, not one design with three
accent colors. Different layout, different information hierarchy, different
interaction model. Three to five variants; fewer hides the space, more blurs
the reactions.

- All variants live on a single route, switched by a URL search parameter,
  with a small floating switcher so the user flips between them in place.
  Obey the project's existing routing convention for where that route goes.
- Use the project's real component library and design tokens where they
  exist, so reactions are about the design and not about unstyled controls.
- Fake the data inline with realistic shapes and awkward lengths: the
  40-character name, the empty list, the 200-row table. A variant that only
  looks right with pretty data has not answered the question.
- Wire only the interactions the question is about. Everything else is inert
  and visibly so.

Show the variants, collect the reaction to each, and converge: often the
answer is one variant's layout with another's detail. Record which variant
won and why on the ticket, then capture and discard per SKILL.md.
