# Credits

`extensions/oh-my-pig` is a Go port of parts of **omp** (oh-my-pi), https://github.com/can1357/oh-my-pi, by
**Can Bölük** and **Stencil Labs, Inc.**, itself a fork of **pi** by **Mario Zechner** (MIT; the license is at
[licenses/oh-my-pi-LICENSE](licenses/oh-my-pi-LICENSE), unmodified).

- Original: the TypeScript omp on branch `main`, packages `coding-agent`, `tui`, `agent` and `utils`.
- Commit: `4df653426f40ee1eb78ef408528eb9e6a5275b9d` (2026-11-23).
- What is taken from it: tool names, parameter schemas, tool descriptions and prompt text (copied, as the
  model-facing surface has to be the same to behave the same), and the behavior of each tool, written again
  in Go. Each package under `extensions/oh-my-pig/internal/` has an `UPSTREAM.md` that lists the original
  files it follows and what it leaves out.

The Go code and its tests were written for this Package by Michael Kinsy. It is not affiliated with Stencil
Labs or with omp's authors, and they have not reviewed it.
