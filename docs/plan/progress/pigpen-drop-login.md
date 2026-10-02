# pigpen-drop-login: Pigpen no longer ships its own sprite login

Status: READY. Base: the games-mcp-jev review branch (`d74647c`). Owner decision 2026-10-01 22:15 MDT: PiG 0.4.0 has a
built-in, extendable sprite login (`/sprite`, additive sprites, header override with `ctx.SetLogin`), so Pigpen drops
`components/pig-login`.

## What changed

- `components/pig-login` is deleted (extension, port record, scenarios, goldens, provenance). No other Package imported it.
- `piglets/pig-games` and `piglets/pig-with-batteries` no longer select it: the `pig-login` Package and the `piglogin`
  extension are gone from both manifests, their READMEs and `catalog.json`; `index.json` is regenerated, and the staged
  Piglets under `dist/staged` no longer carry `packages/pig-login`.
- The `pig-login` row is removed from `ports/ports.json` (the README is regenerated: 24 ports, 18 done); the ports test now
  asserts the row stays out, as it does for MCP.
- Docs: root `README.md`, `RELEASE-NOTES.md` (Packages and Piglets tables, a note that the verified runs predate the
  removal), the roadmap's games table, `pig-play` README, CREDITS, PORT.md and package description, and the porting
  Skill's shared-library example.

## The games against PiG's state file

PiG's built-in login (`coding/piglogin/state.go`, since `2199b94cd`) saves `{"variant":"<id>"}` and a newline, mode 0600, to
`$PIG_HOME/state/pig-standard/login.json` (else `~/.pig/...`), the same path and format `pig-play/libraries/sprite` reads
with `ActiveVariant(ctx.ConfigHome())`. Nothing in the games had to change. New test
`TestGamesReadTheSelectionPiGsBuiltInLoginSaves` (pig-play) writes the file the way PiG does and checks that each of PiG's
ten base sprites is drawn as itself and that an id only PiG knows is drawn as `pig-default`; it fails when the state path
moves.

Terminal check: a `pig-games` Binary (fused `pigrunner` and `angrypigs`, built with a pig from PiG `b76c97f2`, which has
the built-in login) in tmux with a temporary HOME and PIG_HOME. `/sprite list` is PiG's own (ten sprites, one header);
`/sprite set mint` wrote `{"variant":"mint"}` (0600); `/runner` and `/angry-pigs` then drew the mint body colour
(`#C8F0DD`, 19 and 27 cells) and none of `pig-default`'s (`#48A381`).

## Sprites Pigpen has that PiG lacks

None. Pigpen's catalogue is `pig-default`, `pink`, `green`, `mint`, `sandy`, `grey`, `blush`, `lavender`, `cloud`,
`sheriff`; PiG's built-in catalogue has the same ten ids (`coding/piglogin/variants.go` at `2199b94cd`, and the
`sprites-restore` lane adds the character sprites on top). So nothing needs to be contributed through the additive sprite
API.

The API itself is not landed yet: in the PiG sprites-restore lane it is uncommitted work (`ctx.RegisterSprite(sdk.SpriteDefinition{ID,
Name, Tagline, Head, Mascot, Palette})`, host call `ui.registerSprite`; the sprite leaves `/sprite` when its extension
unloads, and a saved id whose extension is not loaded draws `pig-default`).

TODO (follow-up, not in this slice):
- If Pigpen ever draws a sprite of its own, contribute it from a Pigpen extension with `ctx.RegisterSprite` once
  sprites-restore lands, never with a replacement login (`ctx.SetLogin`).
- The games know only the ten base sprites. A user who picks one of PiG's character sprites (`pigrogu`, `darth-vader`,
  `kratos`, `piglet`, `spider-ham`) or an extension's sprite plays as `pig-default`. Drawing those in the games needs either
  their art in `pig-play` or an SDK read of the active sprite definition; neither exists today.

## Checks

`npm run check` (index, quality gates: 31 provenance records, 35 components; Go-only; ports list), `npm test` (117 pass;
the two fewer than before are the per-component checks of the deleted `pig-login`), `npm run validate` with
`PIG_BIN` = a pig 0.3.1+0.99.2 built from PiG `b76c97f2` (every Package, every staged Piglet, every Go extension), and
`go vet` plus `go test -race -count=1` for `pig-play`, `pig-runner`, `angry-pigs` and `pig-snake`.
