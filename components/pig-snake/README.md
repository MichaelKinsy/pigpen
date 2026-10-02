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

The Package is `private` until the owner publishes it. `pig-with-batteries` selects it: bundling
registers two commands and nothing else, so nothing runs until a user types one.

## The sprite seam

The game draws pigs only through `sprites.Source` (`internal/sprites`): sizes, head art and a palette
per herd member. `extension.go` `newSprites` is the one place that chooses the art. The shared sprite
package is now [`pig-play`](../pig-play/README.md) (`libraries/sprite`); an adapter implementing `Source`
on top of it would replace `sprites.Builtin` there and nothing else would change. This release still
uses the built-in source. The built-in source
holds the 8x8 pig of Angry Pigs unchanged, two smaller drawings, and the sprite catalogue colors.

## Evidence

[`port/PORT.md`](port/PORT.md) has the contract table, the twin-test count, the skipped twins,
the proof runs (tests, scenarios under real PiG, mutations, a fused Binary, a real terminal) and
what PiG 0.4.0 will change. Run the tests from the Pigpen root:

```sh
PIG_BIN=/path/to/pig npm run test:go-ports -- -race
```

MIT: see [LICENSE](LICENSE). Written by Michael Kinsy; the adapted PiG files and their unchanged
notice are listed in [CREDITS.md](CREDITS.md).
