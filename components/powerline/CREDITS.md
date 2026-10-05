# Credits

`extensions/powerline-footer` is a Go port of **pi-powerline-footer** 0.19.1, https://github.com/nicobailon/pi-powerline-footer,
by **Nico Bailon**.

- License: MIT, **as declared in the original's package.json** (`"license": "MIT"`, `"author": "Nico Bailon"`). The repository
  ships no LICENSE file; the owner ruled that the declaration is the grant, so the notice reads "(c) Nico Bailon, MIT as declared
  in package.json".
- Commit: `859dee671b633fb533b07ceba3e6c1ab1c43360a` (v0.19.1).
- The unmodified original (its tests included, `banner.png` omitted) is kept at [`port/oracle/`](port/oracle) as the equivalence
  oracle; [`port/oracle/LICENSE`](port/oracle/LICENSE) is a notice added by this Package, not part of the original, and it is
  the upstream `licenseFile` that `provenance.json` points to (license `MIT`).

The Go code, the scenarios, the render states and the tests were written for this Package by Michael Kinsy. Wording, layout rules,
presets, colors, icons and behavior follow the original. Modified paths: none of the original is modified; the port is a separate
implementation. `extensions/powerline-footer/tables.go` is generated from the original's `icons.ts`, `presets.ts` and `theme.ts`.
Not ported (see [port/PORT.md](port/PORT.md)): the bash-mode editor, the prompt queue and stash, the welcome header, the working
vibes, quote reply, `/cd`, editor composition and the custom editor.
