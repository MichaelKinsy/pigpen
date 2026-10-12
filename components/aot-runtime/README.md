# aot-runtime

The aot TypeScript runtime slot for PiG, written in Go against the public Go extension SDK (experimental). It compiles a session's
TypeScript extensions into one executable in the background and runs it from the next start. It fetches a pinned Pi-Bolt compiler
and verifies its signature and SHA-256; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

It is the member of the [`bench-aot`](../../piglets/bench-aot/piglet.yaml) Piglet, which pi-arena pins by Pigpen commit (and by
pi-in-go.dev version after PiG 0.5.0). The source is PiG's `piglets/aot`; see [CREDITS.md](CREDITS.md).

[MIT](LICENSE), original work by Michael Kinsy.
