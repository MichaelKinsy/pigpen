# `oh-my-hermes-pig` Parity Report (`PARITY.md`)

**Status:** Verified (`1/1` oracle case PASS, `0` SDK/fuse gaps, `0` built-in PiG tool leaks, `0` accidental network calls, `0` leaked PIDs)  
**Upstream:** `rlaope/oh-my-hermes` @ `e1c729b799f266891aa5724ecad223d587b21205` (`v5.0.1`, MIT) layered onto `pig-hermes` (`NousResearch/hermes-agent` @ `45e9584556a97f50a3cb6394a6e7f6afa97bb66a`, MIT)  
**Host:** PiG `v0.8.0` (build `73585394f40c`, Go `go1.27.4`)  
**Composition:** `extends: local:../pig-hermes/piglet.yaml` (single fused binary, zero duplicated Hermes code)

## Measured Verification Results

| Check | Command | Result |
|---|---|---|
| Go unit + fakehost tests (`-race`) | `go test -race -count=1 ./...` (`oh-my-hermes`, `pig-hermes`, `pig-hermes-core`) | **PASS** (all 10 packages green, 0 races) |
| Cross-platform static check | `GOOS=linux\|darwin\|windows go vet ./...` | **PASS** |
| Go SDK & fuse-hazard scan | `pigeq gaps --go components/oh-my-hermes/extensions/oh-my-hermes` | **PASS** (`0` gaps, `0` fuse hazards) |
| Fused Piglet binary build | `npm run build:piglet -- oh-my-hermes-pig` | **PASS** (`~/.local/bin/oh-my-hermes-pig`, 62.6 MB fused binary) |
| Hermetic `pigeq llm` oracle proof | `PIG_HERMES_BIN=~/.local/bin/oh-my-hermes-pig python3 -B piglets/oh-my-hermes-pig/proof/oracle.py` | **PASS** (`1/1` case, 7 turns, 6 tool calls, 0 net, 0 leaked PIDs; proves live `omh_todo`↔`todo_list` state sharing across fused extensions) |
| Pigpen repo gates | `npm run check` | **PASS** (46 Packages, 72 provenance records, 92 components) |
