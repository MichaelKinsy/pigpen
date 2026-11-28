# oh-my-pig (extension)

One Go extension for PiG that carries omp's model-facing surface: its tools, with omp's names, parameter
schemas and descriptions, its prompt, and its commands. It is what the
[`oh-my-pig`](../../piglets/oh-my-pig/README.md) Piglet fuses; that Piglet's
[PARITY.md](../../piglets/oh-my-pig/PARITY.md) lists what is ported and what is not.

It is a port of parts of omp (oh-my-pi) by Can Bölük and Stencil Labs, MIT: see [CREDITS.md](CREDITS.md).
It is not affiliated with them. It needs no Node runtime.

## Layout

`extensions/oh-my-pig/extension.go` lists the areas. Each area is a package under
`extensions/oh-my-pig/internal/` that registers its own tools, commands and hooks, and has an `UPSTREAM.md`
naming the omp files it follows and what it leaves out.

## Use

The extension replaces PiG's built-in tools of the same names, so use it through the Piglet, which keeps
none of the built-ins:

```sh
npm run stage
pig --piglet dist/staged/piglets/oh-my-pig/piglet.yaml
```
