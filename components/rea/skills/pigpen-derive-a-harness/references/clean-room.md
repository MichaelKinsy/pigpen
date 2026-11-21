# Writing the piglet

## From the spec, in your own words

- Write `piglets/<name>/` from `spec/` only. Never paste text from `evidence/` into it: not the system prompt, not tool
  descriptions, not skills, not artwork.
- The piglet has its own name, logo and version. These differences are by design; list them once in the spec so that
  later comparisons do not count them again.
- Short interface text that users and scripts depend on (a label, the options of a dialog, an error line) is a separate
  decision. Put it to the user and record what was decided.

## Behavior before appearance

- Build what a thing does before what it shows. A dialog, a status line or a settings row for a function that does not
  exist is worse than its absence: the user believes it.
- Every setting and flag the piglet accepts is read at the place where it takes effect. If it is not going to have an
  effect, do not accept it silently: leave it out, or say when it is set that it has none.
- Never report a state the piglet does not enforce. "Enabled", "restricted", "sandboxed", "signed in" must be true of
  what runs, not of a saved value.

## Safety features

- A safety feature is either enforced or plainly declared absent. This covers command sandboxes, permission modes,
  restricted tool lists, secret handling, offline modes and policy files.
- The piglet never asks the user less than the target does. A place where it would is a defect to fix first, unless the
  evidence shows the target behaves that way; then record it in the spec as a deliberate match.
- A restriction written in configuration (a tool list, a deny rule, variables to keep from child processes) holds in
  every form the configuration format allows and for every process the piglet starts.
- Never change the machine's trust settings (a certificate store, a keychain, a system proxy) for a function the piglet
  does not have.

## One source for each table

When two members need the same list (the commands for the extension that runs them and for the screen that completes
them; the settings for the command line and for the settings page), one of them owns it and the other gets it from
there at run time or at build time. Two hand-kept copies drift, and the drift shows up as the wrong command running.

## Do not fit the piglet to the comparison

- No special case for the comparison's inputs, its directories or its timing. Fix the general behavior.
- When a row cannot match because the target itself varies, change the comparison (see
  [measure.md](measure.md)), and write the change down. Never change the piglet to hit one of two captures.
- A row that only matches because both sides fail the same way is not a feature.

## When PiG cannot do it

Write each host limit down as it is found: what the piglet needs, where in PiG's source the limit is, the smallest
change that would lift it, and what the piglet does meanwhile. Before calling it a bug, check whether PiG behaves as its
own design or its upstream does; that is a feature request, not a defect. If the piglet is built on a patched host, the
patches are part of what you deliver, and you say so.

## If you delegate the building

- Give a builder the behavior and its evidence, not only the rows that fail. A builder who is handed failing rows makes
  the rows pass.
- Read every diff before it is merged. Look for: a check that got weaker, a test that touches the real home or the
  network, a case written for the comparison's script.
- Have someone who built none of it review safety, including what the screen promises and what the piglet writes
  outside its own state directory, not only what the model can reach.
