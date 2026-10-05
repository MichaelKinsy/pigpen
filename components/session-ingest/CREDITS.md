# Credits

`extensions/session-ingest` is original work by **Michael Kinsy**, moved into
Pigpen from the author's personal PiG configuration and released under Pigpen's
MIT license (see [LICENSE](LICENSE)). It is not derived from another project.

It reads the session file format documented by Pi
(https://github.com/earendil-works/pi, MIT, Mario Zechner); it contains none of
Pi's code.

Changes made when it was moved: ported to PiG's public Go SDK; the `turn`,
`tools` and `stats` modes, which the original advertised but did not implement,
were written; output truncation was made rune-safe; the Next hint appears only
when there is more to read; `query` searches the full text
(`maxCharsPerItem` bounds the excerpts, not the search).
