# UI Prototype

> Adapted from `skills/engineering/prototype/UI.md` of mattpocock/skills at d81f3a1 (MIT). The changes are listed in the Package's CREDITS.md.

Generate **several radically different UI variations** on a single route, switchable from a floating bottom bar. The user flips between variants in the browser, picks one (or steals bits from each), then throws the rest away.

If the question is about logic/state rather than what something looks like, this is the wrong branch. Use [LOGIC.md](LOGIC.md).

## When this is the right shape

- "What should this page look like?"
- "I want to see a few options for this dashboard before committing."
- "Try a different layout for the settings screen."

## Two sub-shapes: strongly prefer sub-shape A

A UI prototype is much easier to judge when it's **butting up against the rest of the app**: real header, real sidebar, real data, real density. A throwaway route on its own is a vacuum: every variant looks fine in isolation. Default to sub-shape A whenever there's a plausible existing page to host the variants.

- **Sub-shape A: adjustment to an existing page (preferred).** Variants are rendered **on the same route**, gated by a `?variant=` URL search param. The existing data fetching, params, and auth all stay; only the rendering swaps. Something that doesn't yet have a page but *would naturally live inside one* is still sub-shape A: mount the variants inside the host page.
- **Sub-shape B: a new page (last resort).** Only when the thing genuinely has no existing page to live inside. Create a **throwaway route** following the project's routing convention, named so it's obviously a prototype (include `prototype` in the path or filename), with the same `?variant=` pattern. An empty route hides design problems a populated one would expose, so check first that nothing could host it.

In both sub-shapes the floating bottom bar is identical.

## Process

### 1. State the question and pick N

Default to **3 variants**. More than 5 stops being radically different and starts being noise. Write down the plan in one line, in the prototype's location or a top-of-file comment:

> "Three variants of the settings page, switchable via `?variant=`, on the existing `/settings` route."

### 2. Generate radically different variants

Hold each draft to the page's purpose and the data it has access to, the project's component library / styling system and design tokens, and a clear exported component name (`VariantA`, `VariantB`, `VariantC`). Variants must be **structurally different**: different layout, different information hierarchy, different primary affordance, not just different colours. If two drafts come out too similar, redo one with explicit "do not use a card grid" guidance. Fake data inline with realistic shapes and awkward lengths (the 40-character name, the empty list, the 200-row table). Wire only the interactions the question is about; everything else is inert and visibly so.

### 3. Wire them together

A single switcher on the route:

```tsx
// pseudo-code, adapt to the project's framework
const variant = searchParams.get('variant') ?? 'A';
return (
  <>
    {variant === 'A' && <VariantA {...data} />}
    {variant === 'B' && <VariantB {...data} />}
    {variant === 'C' && <VariantC {...data} />}
    <PrototypeSwitcher variants={['A','B','C']} current={variant} />
  </>
);
```

For sub-shape A keep all the existing data fetching above the switcher; only the rendered subtree changes. For sub-shape B the throwaway route under `/prototype/<name>` mounts the same switcher.

### 4. Build the floating switcher

A small fixed-position bar at the bottom-centre of the screen: a **left arrow** (previous variant, wraps), a **label** with the current key and name (`B (Sidebar layout)`), a **right arrow** (next, wraps). Clicking an arrow updates the URL search param through the framework's router so the variant is shareable and reload-stable; `←` and `→` also cycle, except while an `<input>`, `<textarea>` or `[contenteditable]` is focused. Make it visually distinct from the page, and hidden in production builds (`process.env.NODE_ENV !== 'production'` or equivalent) so a stray prototype merge can't ship it. Put it in one shared component both sub-shapes reuse.

### 5. Hand it over

Surface the URL (and the `?variant=` keys). The interesting feedback is usually **"I want the header from B with the sidebar from C"**, which is the actual design they want.

### 6. Capture the answer and clean up

Once a variant has won, capture the answer (which variant and why), then capture the prototype the way [SKILL.md](SKILL.md) describes. For sub-shape A fold the winner into the existing page; for sub-shape B promote the winner to a real route. Drop the losing variants, the throwaway route and the switcher from main; the full set of variants is the primary source, so it goes to the throwaway branch, not the bin.

## Anti-patterns

- **Variants that differ only in colour or copy.** That's a tweak, not a prototype.
- **Sharing too much code between variants.** A shared `<Header>` is fine; a shared `<Layout>` defeats the point.
- **Wiring variants to real mutations.** Point them at a stub: the question is "what should this look like", not "does the backend work".
- **Promoting the prototype directly to production.** It was written under prototype constraints; rewrite it properly when you fold it in.
