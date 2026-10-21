# pig-snake

A snake game for PiG where **the head is the PiG pig and every apple it eats adds another pig
head to the line**, so a herd forms behind the leader instead of one pig growing. The leader wears
the sprite you chose with `/sprite`; the pigs that join it come in the other sprite colors, so the
line looks like a herd. Original game, MIT; the pig art and terminal helpers are adapted, with
credit, from PiG ([CREDITS.md](CREDITS.md)).

It follows the conventions of the PiG Standard games (Angry Pigs, PiG Runner): it starts **only
when you type a command**, opens a full-terminal overlay, shows a two-line HUD (score, herd,
high score, mode, hints), and keeps its high score in a small private state file.

```text
/pig-snake        (or /snake)
```

| Key | Action |
|---|---|
| arrows, `w` `a` `s` `d`, `h` `j` `k` `l` | steer (and start, from the title screen) |
| space / enter | start |
| `m` | on the title screen: switch between **walls** (the edge ends the game) and **wrap** (the edge carries you to the opposite side); each mode keeps its own high score |
| `p` | pause / resume |
| `r` | retry after a crash or a full board |
| `q`, Esc | leave; the score is reported and the high score saved |

You can turn twice between two steps (an "up, left" round a corner is not lost), you cannot
reverse into your own herd, and the tail cell is free on the step it moves away (unless you eat).
The herd speeds up with every pig (160 ms per step, 5 ms faster per apple, never faster than
70 ms). Fill the whole board and the herd is complete.

## Terminal sizes

The board and the pigs scale to the terminal: 8, 6 or 4 pixel pigs (two pixel rows per text line),
the largest that leaves a comfortable board, at most 40 x 22 cells. A running game keeps its board
if you resize and shrinks its pigs instead. When even the smallest pigs do not fit, the game
shows a "terminal too small" hint naming the terminal size it needs (at least 34 x 16 cells
including the overlay frame; more for a board chosen in a larger terminal) and **pauses**, so a
resize never kills the herd. Enlarging the window shows the paused game; `p` resumes it. `q`
always works.

## Requirements

- An interactive PiG session (TUI). In print, JSON and RPC mode the command only says so: the host
  cannot show a custom component there.
- A terminal with half-block characters; 24-bit color when advertised, xterm-256 otherwise.
- The extension builds from source on first use (Go toolchain) or fuses into a Piglet Binary.

## Install

```sh
pig install ./components/pig-snake
pig package validate ./components/pig-snake
```

The Package is public (`publishConfig.access: public` in `package.json`; it carries no `private` flag) and ships with the owner's
releases. `pig-with-batteries` selects it: bundling registers two commands and nothing else, so nothing runs until a user types
one. It reaches the shared `pig-play` libraries through the `go.work` in `extensions/pig-snake`, like PiG Runner and Angry Pigs
(a Piglet that selects `pig-snake` selects `pig-play` too).

## The sprite seam

The game draws pigs only through `sprites.Source` (`internal/sprites`): sizes, head art and a palette
per herd member. `extension.go` `newSprites` is the one place that chooses the art. `sprites.Shared` is the source in use: the
colourings, their order (followers cycle through the catalogue after the leader) and the sprite chosen with `/sprite` come from
the shared sprite library, [`pig-play`](../pig-play/README.md) (`libraries/sprite`), and the pixel canvas and terminal plumbing
from `libraries/pixel` and `libraries/termgame`. The heads at 8, 6 and 4 pixels are Pig Snake's own drawings of that pig (the
8x8 one is the Angry Pigs pig unchanged).

## Mouse

In PiG's fullscreen mode (the default; regular mode leaves the mouse to the terminal, as Pi does) a left press on the board
steers the leader toward the pointer, as the arrow key for that direction would: the larger of the horizontal and vertical
distance from the middle of the leader decides. It starts a game on the title screen, turns a running one (never into the herd,
and the two-turn queue applies), and does nothing while the game is paused or over, on the pig itself, on the two HUD lines, for
another button, a release, a drag or the wheel, or when the board does not fit. The keyboard is unchanged.

## The Sheriff

When the sprite chosen with `/sprite` is the Sheriff, the leader wears the Sheriff's hat (Pig Snake's own small drawings at the
three pig sizes, in the Sheriff's brown and gold from the shared catalogue) over the top of its head. The followers never do.

## Not built, and why

- **Mini mode** (the game in a footer widget while you keep typing): `Context.SetWidget` can now show a view (`kit.View`), but a
  widget cannot take keys, and a game you cannot steer is not a game; it needs the host to hand the keyboard to a widget on a key
  (the focus handoff Pi's `ctx.ui.setWidget` has no counterpart for). Recorded as a PiG SDK item.
- **Sound**: an extension can neither write to the terminal (a bell) nor play audio through PiG's SDK, and no upstream game has
  sound to be faithful to. Recorded as a PiG SDK item.

## Evidence

[`port/PORT.md`](port/PORT.md) has the contract table, the twin-test count, the skipped twins,
the proof runs (tests, scenarios under real PiG, mutations, a fused Binary, a real terminal) and
what changed with PiG 0.4.0. Run the tests from the Pigpen root:

```sh
PIG_BIN=/path/to/pig npm run test:go-ports -- -race
```

MIT: see [LICENSE](LICENSE). Written by Michael Kinsy; the adapted PiG files and their unchanged
notice are listed in [CREDITS.md](CREDITS.md).
