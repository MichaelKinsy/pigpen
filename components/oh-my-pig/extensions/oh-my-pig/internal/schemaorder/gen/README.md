# omp Tool Schema Capture Generator

Captures the exact wire-level `tools[].function.parameters` JSON Schemas advertised by `omp` (`v19.1.1`, commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`) with key order preserved and intent tracing disabled (`PI_NO_INTENT=1`).

## How It Works

`capture.py` runs `omp` offline against a loopback OpenAI-compatible HTTP server (`127.0.0.1:0`), isolated with:
- macOS `sandbox-exec` loopback-only profile (when available),
- a refusing loopback HTTP/HTTPS proxy (records and rejects any outbound attempt),
- all 98 built-in providers disabled in `config.yml`,
- `HOME`, `TMPDIR`, `PI_CODING_AGENT_DIR`, and XDG dirs pointed at a fresh temp directory,
- `ORACLE_RUN_TAG` process-tree reaping by PID on exit.

It runs four sessions with `tools.commands: false` and all optional tools enabled:
1. `all-top-level` (`omp -p --mode json`): captures 21 top-level tools (`read`, `write`, `edit`, `grep`, `glob`, `find`, `bash`, `todo`, `web_search`, `eval`, `task`, `wait`, `ast_grep`, `ast_edit`, `resolve`, `reject`, `checkpoint`, `rewind`, `debug`, `github`, `lsp`).
2. `subagent-yield` (`omp -p --mode json`, `task.structured: true`): spawns a structured subagent with `outputSchema: true` followed by `wait`, capturing `yield` from the child's wire request.
3. `rpc-ui-ask-goal` (`omp --mode rpc-ui`): enables UI (`hasUI=true`) and sends `set_ask_dialog` + `goal` (`op: "create"`), capturing `ask` and `goal`.
4. `pty-plan-mode` (interactive `omp` in a PTY with `plan.defaultOnStartup: true`): enters plan mode at startup and captures `propose`.

## Exact Regeneration Command

From the extension module root (`components/oh-my-pig/extensions/oh-my-pig/`):

```bash
python3 -B internal/schemaorder/gen/capture.py \
  --out /tmp/omp-schema-capture/captured.json \
  --schemas-out internal/schemaorder/omp_schemas.json \
  --manifest-out internal/schemaorder/omp_schemas.manifest.json
```

Requirements:
- `omp` `v19.1.1` on `PATH` (or pass `--omp-bin /path/to/omp`)
- `gh` on `PATH` (so `github` tool passes `github.available()` at startup; no network is used)
- Python 3.10+
