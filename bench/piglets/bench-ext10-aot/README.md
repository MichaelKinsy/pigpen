# bench-ext10-aot

A bench Piglet for pi-arena, the aot arm: the ten bench-ext TypeScript extensions ([credit](../../components/bench-ext10-ts/CREDITS.md)) plus the aot runtime ([credit](../../components/aot-runtime/CREDITS.md)), which compiles them at first start. Run with `PIG_TS_RUNTIME=aot`.

Pin by Pigpen commit until PiG 0.5.0 ships (`release.version` is fixed), then by the pi-in-go.dev version. Build: `npm run stage`, then `npm run build:piglet -- bench-ext10-aot`.
