# Research note: stretchtext in a terminal agent

What the idea is, where this port's decisions come from, and what is measured. Every claim carries its source; numbers
that are this port's own say how they were measured.

## The idea

- Ted Nelson described stretchtext in unpublished material from 1967 and defined the term in a 1970 article in
  *Computer Decisions*; it expands or contracts text in place, where an ordinary link replaces the page
  ([Wikipedia: Stretchtext](https://en.wikipedia.org/wiki/Stretchtext)). Nelson's own words, as cc-stretch quotes them:
  text that "stretches, becoming longer, with replacement phrases, new details and additional clauses popping into
  place", while "the reader remains oriented" (*Hypertext Note 8*)
  ([cc-stretch README](https://github.com/pro-vi/cc-stretch#a-silly-plugin-about-a-serious-idea)).
- It did not spread because every level had to be authored by hand ("how to author the text in the first place",
  Wadge and schraefel, 1999, quoted in the same README). A language model writes every level in the same reply, which
  is the whole premise of cc-stretch and of pi-stretchtext.
- cc-stretch reports, on 69 real replies from four of its author's coding projects, that the closed form was 17% of
  the words Claude wrote without the plugin and carried about two of every three most important points
  ([cc-stretch README, "What to expect"](https://github.com/pro-vi/cc-stretch#what-to-expect)). That is the author's
  measurement on their own transcripts; this port repeats none of it and claims none of it. The same README states the
  cost side: a reply costs about as many tokens as before.

## The terminal mechanism

- A click on drawn text needs a way to find the stretch again after Markdown has wrapped it. The port uses OSC 8
  hyperlinks: `OSC 8 ; params ; URI ST` makes all following cells a link to the URI until the same sequence with no URI
  closes it, and terminals that do not support it ignore the URI and print the text
  ([Egmont Koblinger's OSC 8 specification](https://gist.github.com/egmontkob/eb114294efbcd5adb1944c9f3cb5feda)).
  The address `stretchtext:<n>` names the stretch; it has no `http` scheme, so Markdown does not make a second link of it.
- The original reads the drawn line under the click and patches `Markdown.prototype.handleMouse` of Pi. PiG has no such
  patch point: it hands the extension the OSC 8 address under a click instead (PiG D193, a scheme claim that takes the
  click from the host's opener). The difference is numbered D2 in [`PORT.md`](PORT.md).
- Redrawing after a click is Pi's `tui.invalidate()` then `tui.requestRender()`: `invalidate` drops every component's
  render cache and `requestRender` schedules the frame (Pi `tui.ts` lines 366 and 990, as PiG's port cites them). PiG
  applies a subprocess extension's Markdown transform asynchronously, so a cache that is not dropped serves the old
  screen: until the host's `invalidate` walked the whole mounted tree, a click changed the extension's state and nothing
  on screen (found by running the extension in a real pig under tmux, fixed in PiG, covered there by a test that fails
  without the fix).

## Words and widths

- The growth animation reveals a long form word by word, so it needs word boundaries. For Thai, Lao, Khmer and
  Burmese a word is not delimited by spaces, and ICU's break iterators use dictionaries for them
  ([ICU User Guide, Boundary Analysis](https://unicode-org.github.io/icu/userguide/boundaryanalysis/#dictionary-based-breakiterator):
  "ICU provides dictionary support for word boundaries in Chinese, Japanese, Thai, Lao, Khmer and Burmese").
  The original calls `Intl.Segmenter`, which runs on Node's ICU; the port uses `components/pitui/wordsegmenter`, the
  ICU 78.3 dictionary engines ported for the shared editor. All 121 growth cases of those scripts match the original frame
  by frame (`TestGrowMatchesOriginal`), measured against Node 24.22.1.

## What the port measures (this repository, imladris, Xeon 6746E, Node 24.22.1, Go 1.26)

`port/perf` runs the same two operations on the same input (a 3.4 KB reply with 24 stretches, a long form of 40
words) in the original (`bench-original.mjs`, Node's type transform, 3 s per benchmark) and in the port
(`go test -bench`, 3 s, count 3):

| Operation | Original (Node) | Port (Go) | Port allocations |
|---|---|---|---|
| Parse the reply | 311 µs | 202 to 212 µs | 274,698 B, 3,461 allocs |
| Grow one long form at ten progress values | 279 µs | 120 to 129 µs | 136,800 B, 979 allocs |

Neither is on a frame path that matters: a reply is parsed once per text and a growth frame runs for about one second
after a click.
