# jev (Piglet)

PiG with the [Jev](../../components/jev/README.md) judge: a typed second opinion on tool calls (`bash`, `write`,
`edit`) before they run and on what `bash` prints, plus the `jev_ask` tool. A Go port of y0usaf's
[pi-jev](https://github.com/y0usaf/pi-jev).

**Off until you turn it on.** `/jev on` shows what leaves the machine and where, then asks. It fails open: an
unavailable judge never blocks a tool call. See the Package README for the surfaces, the two judges (the model PiG
is configured with, or the TypeSafe API), the trust rules and the corrections to the original.

- `piglet.yaml`: authored composition input. It selects the `jev` Package and the shared `typesafe` client library
  Package. Run `npm run stage` before selecting it in PiG.
- Pure Go, so it builds a Piglet Binary (see below).

```sh
npm run stage
pig piglet validate dist/staged/piglets/jev/piglet.yaml
pig --piglet dist/staged/piglets/jev/piglet.yaml
# the Binary (needs the PiG source checkout in PIG_SOURCE_ROOT and Go):
pig piglet build dist/staged/piglets/jev/piglet.yaml --format binary --targets <os>/<arch> --out ./pig-jev
```

`pig-with-batteries` selects the same Package (off by default there too); both Piglets build a Binary.
