# Credits

`extensions/angrypigs` is the `angrypigs` extension of **PiG**,
https://github.com/MichaelKinsy/PiG, written by **Michael Kinsy**
(Copyright (c) 2026 Michael Kinsy, MIT; see [LICENSE](LICENSE)), moved into
Pigpen as a Package at the owner's request.

- Original: `piglets/standard/extensions/angrypigs` at commit
  `d86eb93f217e64b655e9ee6f48c93a9afd107697` (game, art, render, particles, component, state and all tests).
- The pig, the pixel rasterizer, the arcade scenery and the terminal plumbing come from the
  shared Package [`pig-play`](../pig-play/CREDITS.md) (formerly `piglets/standard/internal/*` and
  the sprite catalogue of `piglogin`).
- Angry Pigs wears PiG Runner's look (night sky, turf, HUD lines, title cards) through the shared `arcade` library.

What changed: the sprite type and palette come from `pig-play`'s `sprite` library, and the pixel,
arcade and termgame imports point at `pig-play` instead of PiG Standard's `internal/`. Game rules,
art, rendering, key bindings, messages, the saved high score path and every test are unchanged.
[`port/PORT.md`](port/PORT.md) lists the differences and the tests.
