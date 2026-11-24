# oh-my-pig

An homage to [omp](https://omp.sh) (oh-my-pi, by Can Bölük and Stencil Labs) on PiG: omp's tools, prompt and
commands as a Go extension, drawn natively in [Tern](https://stencil.so/tern) with omp's look.

**Unofficial.** oh-my-pig is not omp, is not affiliated with its authors, and they have not reviewed it. omp
is MIT-licensed; what is taken from it is credited in [CREDITS.md](CREDITS.md).

**Parity is a goal, not a promise.** omp changes daily and builds on native components that PiG does not
have. oh-my-pig follows omp at a pinned commit and ports its core first.
[PARITY.md](PARITY.md) is the list of omp's surface and the state of each item here; nothing is claimed
beyond it.

## What it is

- **A derivative of [pig-tern](../pig-tern/README.md).** The manifest extends pig-tern, and the frontend
  member is pig-tern's with omp's look fixed on: the same roles and node shapes omp sends to Tern, so Tern's
  built-in styles for omp apply. It keeps PiG's own names towards Tern and does not present itself as omp.
  Outside Tern, PiG draws as usual.
- **omp's tools instead of PiG's.** The Piglet keeps none of PiG's built-in tools. The model sees omp's
  tool names, schemas and descriptions from the [`oh-my-pig` extension](../../components/oh-my-pig/README.md).
- **One Go Binary, no Node.**

## Build and run

```sh
npm run stage
pig piglet validate dist/staged/piglets/oh-my-pig/piglet.yaml
PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  pig piglet build dist/staged/piglets/oh-my-pig/piglet.yaml --format binary --out ./oh-my-pig
./oh-my-pig          # in a Tern pane
```

Building needs a Go toolchain and, for pig-tern's member, Stencil's Go SDK for Tern from the Go module proxy.

## Where it stands

At omp `4df65342` (2026-11-23): 10 of omp's 13 default tools (`read`, `write`, `edit`, `bash`, `grep`, `glob`,
`find`, `todo`, `ask`, `web_search`), omp's system prompt, and omp's look in Tern for the transcript and the
composer. Not here: subagents (`task`, `wait`), `eval`, `lsp`, `ast_edit`, the internal URL schemes, omp's modes,
commands, settings and extensions. [PARITY.md](PARITY.md) has every row and how each was compared.

## Status

Source only, no published release. Built and run on darwin/arm64 only.
