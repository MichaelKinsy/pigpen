# bun-runtime

The Bun TypeScript runtime slot for PiG, written in Go against the public Go extension SDK. It installs a pinned Bun release after
checking its OpenPGP signature and SHA-256, and runs TypeScript and JavaScript extensions on it instead of Node.

It is a member of the [`bench-standard`](../../piglets/bench-standard/piglet.yaml) and [`bench-bun`](../../piglets/bench-bun/piglet.yaml)
Piglets, which pi-arena pins by Pigpen commit (and by pi-in-go.dev version after PiG 0.5.0). The source is PiG Standard's
`bunruntime`; see [CREDITS.md](CREDITS.md). `PIG_BIN=/path/to/pig npm run test:go` runs its tests.

[MIT](LICENSE), original work by Michael Kinsy.
