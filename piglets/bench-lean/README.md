# bench-lean

The lean bench Piglet: PiG with every built-in extension, extra model API and feature stripped. It exists so pi-arena and other
footprint comparisons can pin one reproducible "stripped PiG" next to PiG Standard.

The manifest uses strip keep mode with three empty lists (`extensions`, `apis`, `features`): nothing of those lists stays, and
IDs a later core adds are stripped too. The built-in tools and slash commands stay, so the scripted model's requests carry the same
tool declarations as the other arms. A strip is opt-in in Pigpen, and this manifest is the opt-in: it names every list.

It has no TypeScript, Go, Rust or Python extension runtime, so `pig -e <file>` fails with a message that names `bench-lean` and the
strip entry; remove the entry from `strip` and rebuild to keep a runtime. Other Pigpen Piglets are not affected.

Sizes of each opt-in entry: [docs/plan/progress/piglets-pigpen.md](../../docs/plan/progress/piglets-pigpen.md).

Build: `npm run stage`, then `npm run build:piglet -- bench-lean` with `PIG_BIN` and `PIG_SOURCE_ROOT` set as the root README says.
Pin by Pigpen commit until PiG 0.5.0 ships.
