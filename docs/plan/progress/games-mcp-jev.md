# games-mcp-jev: the pig games served over MCP, and Jev playing them

Status: READY. Base: the pigpen-040-fixes review branch.

- Feature: `components/pig-play/libraries/gamemcp`, used by pig-runner and angry-pigs. Opt-in (`/runner mcp on`,
  `/angry-pigs mcp on`, `PIG_GAMES_MCP=1`); 127.0.0.1 only, random port and bearer token; registered with
  `RegisterMcpServer` (description, codemode exposure); stopped on `mcp off`, session end and reload.
- Tools: games_list, game_start, game_state, game_act (optional confidence), game_score. HUD line "AI playing: ...".
- Demo: `demo/jev-plays.js` (README in `demo/`), tested by `scripts/jev-plays.test.mjs` on stubs.
- Checked: `go test -race` of the three modules; `npm run check`, `npm test`, `npm run stage`; the existing 17 mutations per
  game are still killed; a real interactive pig played both games through codemode (classifier replaced by a rule).
  The fused linux/amd64 pig-games Binary builds and runs.
- With a fake classifier the Runner over HTTP makes about 3600 decisions/s (about 530/s under -race).
- Not done, with the evidence in the lane's question file: the codemode `models` helper is undefined in interactive
  sessions of the staged 0.4.0 pig, and the staged pig builds only host-target Binaries (no darwin/arm64).
