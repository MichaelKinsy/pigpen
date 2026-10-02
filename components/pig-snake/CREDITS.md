# Credits

Pig Snake is an **original game** (rules, renderer, tests and this Package: MIT, Michael Kinsy).
It is marked `origin: "ported"` in `provenance.json` because it adapts code and art from
**PiG**, https://github.com/MichaelKinsy/PiG, by **Michael Kinsy** (MIT), at commit `d86eb93f217e64b655e9ee6f48c93a9afd107697`.

PiG's games are Go extensions that use helper packages under `piglets/standard/internal/`.
A Piglet extension cannot import them (Go `internal` rule, and Pigpen extensions use only the
public SDK), so the pieces Pig Snake needs were **adapted** into `extensions/pig-snake/internal/`.
The upstream files are kept, byte for byte, as evidence under
[`port/upstream/`](port/upstream) (suffix `.go.txt` so they are not compiled) with PiG's license,
with one exception: in `piglogin-variants.go.txt` (upstream blob `9fae5733`) one comment line no
longer names the repository the website artwork came from ("website (the private platform
repository)." now reads "website."), and the default sprite id is `pig-default` where the upstream files
have an internal name (in `piglogin-variants.go.txt`, `angrypigs-game_test.go.txt` and `pigrunner-pigrunner_test.go.txt`; the
same rename is made in the Pigpen copies of those files). Code, colors and every other line are unchanged.

| Pig Snake file | Adapted from (PiG `piglets/standard/`) | What changed |
|---|---|---|
| `internal/pixel/pixel.go`, `text.go` | `internal/pixel/pixel.go`, `text.go` | `Blend`, `BlitMirrored`, `Scale` dropped; the rest unchanged (canvas, half-block encoder, 256-color mapping, `FitLine`, the 3x5 pixel font). |
| `internal/termgame/termgame.go` | `internal/termgame/termgame.go` | Mouse decoding dropped; overlay, viewport and `ParseKey` unchanged. |
| `internal/sprites/sprites.go` | `extensions/angrypigs/art.go` (`pigBall`), `extensions/piglogin/variants.go` and `art.go` | The 8x8 pig is verbatim; the sprite catalogue colors are copied (Sheriff hat art not used); the 6x6 and 4x4 pigs are new drawings of the same pig. |
| `internal/scene/scene.go` | `internal/arcade/arcade.go` and `extensions/pigrunner/render.go` | Same night palette, HUD layout, title cards and 256-color fallback, reimplemented for a snake board. |
| `extension.go`, `component.go`, `state.go` | `extensions/pigrunner/{extension,component,state}.go` | Same conventions (command, full-terminal overlay, fixed clock, strict 0600 state file, `Done` result); different game. |
| tests | `pigrunner/extension_test.go`, `internal/pixel/pixel_test.go`, `internal/termgame/termgame_test.go` | Twins with the same inputs and expectations, listed in `port/PORT.md`. |

The Pig Snake rules, the apple and small-pig art (6x6, 4x4), the herd coloring, the resize and
too-small behavior, and everything under `port/` other than `port/upstream/` were written for this
Package by Michael Kinsy.

## Licenses

Both parts are MIT. The license text that travels with the upstream files is at [`port/upstream/LICENSE`](port/upstream/LICENSE):

```text
MIT License

Copyright (c) 2026 Michael Kinsy
```

(the full text follows in that file). Pigpen's license for the rest is [`LICENSE`](LICENSE); the two
texts are identical.
