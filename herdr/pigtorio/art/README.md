# Pigtorio art

`./gen.sh` regenerates everything below (idempotent) into `mods/pigtorio/art/` and `mods/pigtorio/fonts/`, plus `art/contact.png` (a frame of every new asset on grass at 4x; all Runt frames; hotbar icons shown on their slot).

All files are stored at 4x nearest-neighbour. Display size = file size / 2 (that is 2x the 1x art, in logical px), so pixels stay square when Herdr scales down. Animated assets are lossless looping WebP with a transparent background.

`artgen` lives in `art/artgen/` as its own Go module (`pigtorio-artgen`, stdlib only; `engine/go.mod` did not exist when it was written). It writes 1x frames; `gen.sh` scales and packs them.

| asset (under `mods/pigtorio/art/`) | 1x px | file px | display px | frames |
|---|---|---|---|---|
| `sprites/<skin>/<state>.webp` (15 skins x 14 states, Pigsty's own) | 128x96 | 512x384 | 256x192 | per state, Pigsty timing |
| `sprites/index.json` (skins, states, size) | | | | |
| `props/*.png` (Pigsty props, e.g. `bell.png` 44x36; use as the Bell icon) | per prop | 4x | 2x | 1 |
| `tiles/river.webp` water strip, ripples move down | 18x64 | 72x256 | 36x128 | 8 x 150 ms |
| `tiles/belt-h.webp` belt, stripes scroll right | 32x6 | 128x24 | 64x12 | 6 x 100 ms |
| `tiles/belt-v.webp` belt, stripes scroll down | 6x32 | 24x128 | 12x64 | 6 x 100 ms |
| `ui/barn.png` | 36x34 | 144x136 | 72x68 | 1 |
| `ui/oven.webp` flickering fire | 22x20 | 88x80 | 44x40 | 4 x 140 ms |
| `ui/review.png` | 22x20 | 88x80 | 44x40 | 1 |
| `ui/jobboard.png` | 38x24 | 152x96 | 76x48 | 1 |
| `ui/crate.png` | 8x8 | 32x32 | 16x16 | 1 |
| `ui/crate-burnt.png` | 8x8 | 32x32 | 16x16 | 1 |
| `ui/bridge.png` (planks run across the river) | 24x10 | 96x40 | 48x20 | 1 |
| `ui/logo.png` (green pig head, the header SVG's rects) | 14x13 | 56x52 | 28x26 | 1 |
| `ui/ghost.png` dashed planned-station box | 22x20 | 88x80 | 44x40 | 1 |
| `ui/belt-arrow.png` (white chevron, points right), `ui/belt-arrow-down.png`, `-left`, `-up` (pre-rotated: Herdr cannot rotate) | 8x8 | 32x32 | 16x16 | 1 each |
| `ui/cursor-ok.png` / `ui/cursor-bad.png` build cursor: tile corner brackets, `#4ed1a1` / `#e05a4f`, transparent middle | 16x16 | 64x64 | 32x32 | 1 each |
| `hotbar/{pen,pig,belt,oven,review,barn,jobboard,remove,runt,hand}.png` hotbar icons (remove = red-handled pickaxe, hand = no tool) | 16x16 | 64x64 | 32x32 | 1 each |
| `hotbar/slot.png`, `hotbar/slot-on.png` slot frames (`#1a2620`/`#2b3d33`; selected `#2d4439`/`#4ed1a1`, rounded corners); the icon sits 2 px inside | 20x20 | 80x80 | 40x40 | 1 each |
| `runt/idle.webp` piglet foreman, breathing | 18x18 | 72x72 | 36x36 | 2 x 600 ms |
| `runt/talk.webp` mouth opens, clipboard bobs, "!" mark top-right (gone on the last frame) | 18x18 | 72x72 | 36x36 | 4 x 140 ms |
| `runt/walk-<down\|up\|left\|right>.webp` | 18x18 | 72x72 | 36x36 | 4 x 120 ms |
| `swineherd/idle-<down\|up\|left\|right>.webp` breathing | 18x22 | 72x88 | 36x44 | 2 x 600 ms |
| `swineherd/walk-<down\|up\|left\|right>.webp` | 18x22 | 72x88 | 36x44 | 4 x 120 ms |

Notes:
- The belt periods are 6 px (3+3 stripes), so the tiles repeat seamlessly in time; the 32 px tile length is not a multiple of 6, so tile belts end to end only with a 6-px-aligned length or accept a one-pixel-phase seam, or stretch by cropping to a multiple of 6 (30 px).
- The Swineherd is 16x20 plus a 1 px dark outline margin; feet rest on the bottom row.
- `fonts/Silkscreen-Regular.ttf` (SIL OFL, `fonts/OFL.txt`): PostScript name `Silkscreen-Regular` (name ID 6) present.
- The Runt is 14x15 plus a 1 px outline margin (shorter than the Swineherd), orange hard hat, clipboard in his right hand; feet rest on the bottom body row. The top-right 4x6 of the canvas is reserved for the speech mark.
- Sprite and prop sources are `~/kinsy-utils-tern/pig-stuff/pigstyd/web` (override with `PIGSTY_WEB`).
