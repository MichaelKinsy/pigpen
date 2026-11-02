# Credits

This Package is a Go port of **pi-opencode-free** 0.2.0,
https://github.com/pedrostarkealexis/pi-opencode-free, by **Pedro Alexis**
(GPL-3.0-or-later, Copyright (c) 2026 pi-opencode-free contributors).

- Pinned commit: `0f4c16bade33106847ae4abd8d2be2d1b0058e36` (tag `v0.2.0`).
- The unmodified original, with its tests and README, is vendored in
  `components/pi-opencode-free/port/oracle/`; its license is in `LICENSE` of this
  Package and in `components/pi-opencode-free/port/oracle/LICENSE`.
- What is modified: everything. The Go implementation follows the original's
  discovery rules, provider registration, persisted snapshot lifecycle and
  keyless request headers, but uses PiG's public Go SDK and its own OpenAI
  Chat Completions and Responses stream clients. The mapping from each original
  file to its Go file is in `port/PORT.md`.
- The Go code and tests were written for Pigpen by Yu Li.
