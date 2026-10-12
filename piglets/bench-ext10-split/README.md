# bench-ext10-split

A bench Piglet for pi-arena: the ten bench-ext extensions split five Go-fused ([bench-ext10](../../components/bench-ext10/CREDITS.md), members 01-05) and five TypeScript ([bench-ext10-ts](../../components/bench-ext10-ts/CREDITS.md), members 06-10) compiled by the aot runtime ([credit](../../components/aot-runtime/CREDITS.md)). Run with `PIG_TS_RUNTIME=aot`. `scripts/bench-equivalence.mjs` checks it against the TypeScript originals.

Pin by Pigpen commit until PiG 0.5.0 ships (`release.version` is fixed), then by the pi-in-go.dev version. Build: `npm run stage`, then `npm run build:piglet -- bench-ext10-split`.
