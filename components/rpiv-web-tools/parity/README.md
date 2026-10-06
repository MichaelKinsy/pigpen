# Differential parity against the oracle

`compare.mjs` runs the **oracle's own TypeScript** (`port/oracle/providers/fetch-helpers.ts`) under Node and writes
`expectations.json`. `extensions/rpiv-web-tools/parity_test.go` reads that file and compares the port byte for byte.

```sh
node parity/compare.mjs          # regenerate the expectations from the oracle
cd extensions/rpiv-web-tools && go test -run TestParity
```

This is deliberately separate from the twins. A twin pins behaviour the upstream *states* in a test title; this pins
behaviour the upstream only *computes*, which is where a port silently diverges. It found one that 272 twins and
`npm run check` all passed: the original decodes numeric HTML entities with `String.fromCharCode`, a UTF-16 code
unit rather than a code point, so `&#128512;` becomes the lone surrogate U+F600 instead of the emoji. The port
reproduces that, and the mask is commented so the next reader does not "fix" it back.

`fixtures.json` holds the inputs both sides see. Add a case there, regenerate, and the Go side compares against the
oracle without touching a test.
