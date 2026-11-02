# Pi-opencode-free

Free OpenCode models in [Pi](https://github.com/earendil-works/pi), no API key.

## Why

The Pi CLI already ships an `opencode` provider, but through it the models only run on paid plans. This extension hits the same endpoint and removes the need for a paid plan: it discovers the models advertised as free and sends the OpenCode client headers (`x-opencode-client`, `x-opencode-project`, `User-Agent`) on every request, which authorizes free, keyless usage.

Everything else is Pi's native `openai-completions` engine: streaming, reasoning, tools. Nothing is intercepted or rewritten.

## Install

```bash
pi install npm:pi-opencode-free
```


## Usage

Pick any `(Free)` model in Pi's model selector. The free-model catalog is
managed natively by Pi: it loads instantly from a persisted snapshot and
refreshes itself in the background on interactive startup or when opening
the model selector.

## Issues & contributions

Found a problem? Open an [issue](../../issues/new). Contributions are welcome.

## Development

Uses [Bun](https://bun.sh)

```bash
bun install
bun test src/            # unit tests
bun scripts/smoke-real.ts  # live request against Zen
```

| File | Purpose |
|------|---------|
| `src/discovery.ts` | Free-model discovery (Zen + models.dev enrichment) |
| `src/index.ts` | Native provider registration and snapshot persistence |

## License

[GPL-3.0-or-later](./LICENSE) — © 2026 pi-opencode-free contributors
