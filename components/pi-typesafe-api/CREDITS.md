# Credits

This Package is a Go port of **pi-typesafe** 0.8.0, https://github.com/DevMortimer/pi-typesafe, by
**Ryan Gapac** (MIT, Copyright (c) 2026 Ryan Gapac). It is an independent project, not affiliated with TypeSafe AI or
the Pi authors, and so is this port.

- Pinned commit: `ed439f834665ad6fc652787f5ba7c23852566d49` (release 0.8.0, "Add Command Code backend and caller-supplied endpoints")
- The unmodified original, with its tests, is vendored in `components/pi-typesafe/port/oracle/`; its license is in `LICENSE`
  of this Package and in `components/pi-typesafe/port/oracle/LICENSE`.
- What is modified: everything. The behavior, wording and structure follow the original; the code is a Go
  implementation over the shared TypeSafe client (`components/typesafe`, a port of the official `@typesafe-ai/sdk`).
  The mapping from each original file to its Go file is in `components/pi-typesafe/port/PORT.md`, with every deliberate difference.

The Go code, scenarios and tests were written for Pigpen by Michael Kinsy.
