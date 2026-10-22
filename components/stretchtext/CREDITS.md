# Credits

`extensions/stretchtext` is a Go port of **pi-stretchtext** 0.1.0, https://github.com/MonadicMike/pi-stretchtext, by
**Michal Pikosz** (MIT, Copyright (c) 2026 Michal Pikosz).

- Commit: `c93b06876aefdfab99171145ef1cdf695bb5ada4` ("Prepare the npm package for Pi's gallery").
- The unmodified original (`index.ts`, `src/`, its `README.md`, `package.json`, `tsconfig.json` and `LICENSE`; the demo
  animation is omitted) is kept at [`port/oracle/`](port/oracle) as the equivalence oracle, with its license at
  [`port/oracle/LICENSE`](port/oracle/LICENSE).

The parser (`parse.go`), the growth of a long form around its short form (`grow.go`) and the guide the model is given
(`guide.go`, generated from `src/prompt.ts`) are Go ports of code that pi-stretchtext itself took from **cc-stretch**,
https://github.com/pro-vi/cc-stretch, by **pro-vi** (MIT, Copyright (c) 2026 pro-vi; revision
`910fb99d43bfad23d591b36aebd6c25bd0729081`). The original's `LICENSE` names both copyright holders for those parts, and
this Package's [`LICENSE`](LICENSE) keeps both notices.

The colour conversions in `extensions/stretchtext/colour.go` port the part of pi-tui's `colors.ts` and `oklab.ts` that turns
a colour into an SGR sequence. Oklab is Bjorn Ottosson's colour space (MIT, Copyright (c) 2021 Bjorn Ottosson,
https://bottosson.github.io/posts/colorpicker/).

The Go code, the scenarios, the differential cases and the tests were written for this Package by Michael Kinsy. Behavior,
wording, colours and the order of effects follow the original; where they differ, [port/PORT.md](port/PORT.md) numbers the
difference. Modified paths: none of the original is modified; the port is a separate implementation. The one file generated
from the original is `extensions/stretchtext/guide.go` (`port/gen-guide.mjs`).
