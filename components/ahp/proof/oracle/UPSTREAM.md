# Oracle: pi-ahp

Unmodified copy of `src/`, `test/` and the package metadata of https://github.com/Qusic/pi-ahp
(Bang Lee, MIT, see `LICENSE`) at commit `4065e98309b03c1d2d916300856c3c21a9c10674`. It is the
equivalence oracle: 430 leaf test cases (`../upstream-tests.json`), each with a Go twin or a named
skipped twin (`../PORT.md`). To run it: install with `pnpm install` in a checkout of upstream, then
`AHP_SPEC_PATH=<agent-host-protocol checkout at 296b25e7b698a4a84a0ee5a28d9573e70048a0bf> node --test test/<file>.test.ts`.
