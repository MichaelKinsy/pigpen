# Credits

`extensions/context-info` is original work by **Michael Kinsy**, moved into
Pigpen from the author's personal PiG configuration and released under Pigpen's
MIT license (see [LICENSE](LICENSE)). It is not derived from another project.

Changes made when it was moved:

- Ported to PiG's public Go SDK, including its error-returning host reads and
  Pi's nullable context usage (tokens and percent are unknown right after
  compaction).
- The status footer is **off by default** (it replaces PiG's footer). Turn it on
  with the `--context-footer` flag or `/context-footer on`.
- Removed `/prompt-edit`, the session prompt overlay, the deletion of the overlay
  file at session start, and the subagent cost section of `/cost`. They belonged
  to the author's own subagent extension, which is not part of this Package.
- Windows: git is started without a console window.
- `/prompts` no longer fails on an agent file shorter than four bytes, and reads
  frontmatter only when the file starts with it.
