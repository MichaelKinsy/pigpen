# Upstream: internal/ternwire

Pinned to omp release tag `v19.1.1` (`31ffee513f`).

## Upstream files followed
- `packages/coding-agent/src/tools/browser/tern/wire.ts`
- `packages/coding-agent/src/tools/browser/tern/kind.ts`
- `packages/utils/src/env.ts` (`parseFlag`)
- `packages/coding-agent/test/tools/tern-fake-daemon.ts`
- `packages/coding-agent/test/tools/browser-tern-wire.test.ts`
- `packages/coding-agent/test/tools/browser-tern-kind.test.ts`
## Tests ported
- 15 tests in `wire_test.go`: 13 upstream cases from `browser-tern-wire.test.ts` and `browser-tern-kind.test.ts` (each with `// upstream:` citation), 1 N5 write-error-fails-client test, plus 1 gated real-Tern daemon test (`OMP_TERN_DAEMON_TEST=1`).

## Settings / Environment variables
| Key | Default | Honoured |
|---|---|---|
| `TERN_PANE_SOCKET` | unset | Yes (`Pane`, `PaneFromEnv`) |
| `TERN_PANE` | unset | Yes (`Pane`, `PaneFromEnv`) |
| `PI_BROWSER_TERN` | `browser.tern` (default `true`) | Yes (`ResolveKind`, exact `parseFlag` truthy table) |

## Not ported
| Feature | Reason |
|---|---|
| Windows named pipe transport | Upstream `wire.ts` uses `net.createConnection({ path })` (Unix socket only). Windows build returns `ErrUnsupported` so auto mode falls back. |
