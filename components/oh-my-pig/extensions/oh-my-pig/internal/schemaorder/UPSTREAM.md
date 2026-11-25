# Upstream Tracking: `internal/schemaorder`

Pinned upstream release: **omp `v19.1.1`** (commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`).

## Purpose

Plain Go `map[string]any` JSON Schemas serialize their object keys alphabetically on the wire. In omp (`v19.1.1`), TypeScript / ArkType (`arkToWireSchema`) and hand-built objects (`yield`) preserve JavaScript object key insertion order, and models are sensitive to schema key order as part of the prompt.

`internal/schemaorder` embeds omp `v19.1.1`'s real captured wire `parameters` schemas (`omp_schemas.json`) and exports `Apply(def sdk.ToolDefinition) sdk.ToolDefinition` to convert any tool's `Parameters` into the PiG SDK's ordered form (`~order` on the root map, `*sdk.OrderedObject` on nested objects) recursively without adding, removing, or altering any schema content.

## Upstream Files & Capture Source

All 25 tools registered by `oh-my-pig` were **captured** directly from the installed `omp/19.1.1` binary over a loopback OpenAI-compatible HTTP server (`gen/capture.py`) with 0 network attempts, 0 leaked PIDs, and `PI_NO_INTENT=1`:

| Upstream source | Role |
|---|---|
| `packages/coding-agent/src/tools/index.ts` | Built-in tool registration, `tools.commands` mounting, special tools (`resolve`, `reject`, `propose`) |
| `packages/coding-agent/src/tools/*.ts` | Tool parameter schemas (`read`, `write`, `edit`, `grep`, `glob`, `jfind`, `bash`, `ask`, `todo`, `eval`, `wait`, `yield`, `ast-grep`, `ast-edit`, `resolve`, `checkpoint`, `debug`, `gh`) |
| `packages/coding-agent/src/task/types.ts` | `task` batch/single schema |
| `packages/coding-agent/src/web/search/index.ts` | `web_search` schema |
| `packages/coding-agent/src/lsp/types.ts` | `lsp` schema |
| `packages/coding-agent/src/goals/tools/goal-tool.ts` | `goal` schema |
| `packages/agent/src/agent-loop.ts:1017-1072` | `injectIntentIntoSchema` composition with `internal/intent` |

## Tests

6 Go tests in `schemaorder_test.go` (no upstream test suite exists for this PiG-specific adapter):
- `TestManifestMatchesEmbeddedSchemas` — verifies `omp_schemas.manifest.json` sha256, byte count, pin (`v19.1.1` / `31ffee513f27a01bba7d4dfbe12bae239cdc646a`), tool count (25), and generator files.
- `TestPortSchemasEqualOmpContent` — registers all 14 tool-owning areas + dynamic `yield` through `sdk.Extension.RunWithConn` and compares schema content order-insensitively against omp `v19.1.1`, failing on any unallowlisted or stale diff (`known_content_diffs.json`).
- `TestWireOrderByteIdenticalAfterApply` — asserts wire JSON after `Apply` is byte-identical to omp `v19.1.1` (modulo Go's default `\u003c`/`\u003e`/`\u0026` HTML escapes on `<`/`>`/`&` inside strings) for all 19 content-equal tools, and asserts exact omp key-order prefix + sorted extra keys for the 6 tools with allowlisted content diffs.
- `TestApplyComposesWithIntent` — verifies `intent.Tool(schemaorder.Apply(def))` places `i` first followed by omp's exact property order and preserves root and nested key order across `bash`, `glob`, `read`, `write`, `debug`, `github`, `lsp`, and omit-mode `resolve`.
- `TestApplyEdgeCases` — unknown tools, nil parameters, extra port-only keys sorted after omp keys, `enum` preservation, non-mutation of caller map, `anyOf` branch ordering, and `*sdk.OrderedObject`/`*sdk.TSchema` input (`lsp`).
- `TestApplyConcurrentSafe` — concurrent `Apply` calls verified under `-race`.

## Settings Read

None. `internal/schemaorder` is a pure in-memory transform over embedded `omp_schemas.json` and performs no I/O or settings lookup.

## Not Ported

| Omitted feature | Upstream location | Reason |
|---|---|---|
| Unescaped `<`, `>`, `&` in extension register-frame JSON strings | JS `JSON.stringify` vs Go `sdk.schemaRoot`/`OrderedObject.MarshalJSON` (`json.MarshalJS`) | PiG SDK's `schemaRoot.MarshalJSON` calls `json.MarshalJS` (HTML-escaping `<`, `>`, `&` to `\u003c`, `\u003e`, `\u0026`). Decoded strings and host-to-provider serialization are unaffected; `internal/schemaorder` cannot change the SDK's `schemaRoot` marshaler. |
| Dynamic per-setting schema variants (`task.isolation.enabled`, `task.structured`, `task.steps`, `task.enableEffort`, `task.perCallModel`, `bash` without `pty`, `yield` workpool/custom `outputSchema`) | `task/types.ts`, `tools/bash.ts`, `tools/yield.ts` | `omp_schemas.json` captures each tool's default top-level schema; any extra keys added by non-default variants trail after omp's captured keys in sorted order, and subset keys (like `bash` without `pty`) keep omp's exact relative order. |

## Integrator Wiring

Wrap every registered `sdk.ToolDefinition` inside `schemaorder.Apply` before passing it to `intent.Tool` (and before `e.RegisterTool` / `ctx.RegisterTool`):

```go
e.RegisterTool(intent.Tool(schemaorder.Apply(def)))
```

(Or `ctx.RegisterTool(intent.ToolForContext(ctx, schemaorder.Apply(def)))` for session-start re-registrations.)
