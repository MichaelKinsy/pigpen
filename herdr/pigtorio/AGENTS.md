# AGENTS.md

Rules for agents working on Pigtorio.

- Read `ARCHITECTURE.md` before changing the mod, the engine or the file contracts. Change the contract there in the same change as the code.
- `DESIGN.md` and `PLAN.md` describe the goal. The storyboard text is in `.harness/pigtorio-build/storyboard.txt`.
- Herdr's mod API is `~/.config/herdr-app/AGENTS.md`, `herdr.d.ts` and `gpui-kit.d.ts`. Those files win over memory: there is no DOM, no CSS, no `setTimeout`.
- Never run tests or probes against the real Herdr home (`~/.config/herdr-app`) or the default herdr-core. Use the sandbox (`scripts/sandbox.sh env`), which sets `HERDR_APP_HOME=/tmp/hfarm/home` and a private core.
- Animate natively (animated WebP, `.frames`, `.loop`, `.transition`). Never redraw on a timer for animation.
- Every game verb runs a real Herdr or hc action. Do not fake state in the UI.
- Pigs hold no push credentials. The Barn pushes and opens pull requests only after a person approves.
- Go: module `pigtorio` in `engine/`, go 1.26 floor, standard library only unless a dependency is clearly needed. `go vet ./... && go test ./...` must pass.
- JS: `bun test` in `test/` must pass. `herdr-app check mods/pigtorio` (sandbox home) must pass after every mod change.
- Generated art under `mods/pigtorio/art/` comes from `art/gen.sh` and `engine/cmd/artgen`. Do not hand-edit generated files.
