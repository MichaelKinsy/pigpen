# Upstream: internal/ternbrowser

Pinned to omp release tag `v19.1.1` (`31ffee513f`).

## Upstream files followed
- `packages/coding-agent/src/tools/browser/tern/blocks.ts`
- `packages/coding-agent/src/tools/browser/tern/dom.ts`
- `packages/coding-agent/src/tools/browser/tern/keys.ts`
- `packages/coding-agent/src/tools/browser/tern/network-log.ts`
- `packages/coding-agent/src/tools/browser/tern/page-capture.ts` (`TERN_CAPTURE_INSTALLER` embedded verbatim via bun from tag)
- `packages/coding-agent/src/tools/browser/tern/page-kit.ts` (`TERN_KIT_SOURCE`, `TERN_FRAME_ANNOUNCE_SOURCE` embedded verbatim via bun from tag)
- `packages/coding-agent/src/tools/browser/tern/selectors.ts`
- `packages/coding-agent/src/tools/browser/tern/tern-tab.ts`
- `packages/coding-agent/src/tools/browser/tern/user-page.ts`
- `packages/coding-agent/src/tools/browser/pointer-space.ts`
- `packages/coding-agent/src/tools/browser/dialogs.ts`
- `packages/coding-agent/src/tools/browser/select-options.ts`
- `packages/coding-agent/src/tools/browser/storage-state.ts` (`storageStatePath`)
- `packages/coding-agent/src/tools/browser/network.ts` (`globToRegExp`, `urlPattern`, `normalizeAllowedDomains`)
- `packages/coding-agent/test/tools/tern-fake-daemon.ts`
- `packages/coding-agent/test/tools/browser-tern-tab.test.ts`
- `packages/coding-agent/test/tools/browser-tern-adopt.test.ts`
- `packages/coding-agent/test/tools/browser-tern-dom.test.ts`

## Session lifecycle ownership
Session-end cleanup is owned by `internal/browser`'s `lifecycle.OnEnd` hook (`internal/browser/register.go`), which closes every managed tab (including Tern tabs) via `Tab.Close`. `internal/ternbrowser` registers only `browser.RegisterBackend("tern", SharedBackend())` and does not register a duplicate `lifecycle.OnEnd` hook.

## Tern `browser` daemon ops sent (all via typed request structs in `ops.go`)
| Op | Used by |
|---|---|
| `open` | `Backend.Open` / `OpenWithShow` (always at `about:blank` with width/height, optional `show: false` when `Headless` or explicit, then configure, then goto), `OpenUserPage` |
| `close` | `Tab.Close` (owned PiP, detached 5s context), `UserPage.Close`, late-answer cleanup |
| `release` | `Tab.Close` (adopted block, with `scripts: []` fallback on `invalid`), `OpenUserPage` |
| `blocks` | `Backend.ListTargets`, `Backend.adoptBlock`, gone-block check (`translateBlockFailure`), docked-viewport check |
| `state` | `Tab.refreshState` (reads `url`, `title`, `shown`, `width`, `height`, `loading`, `back`, `forward`, `responsive`), `UserPage.Closed` |
| `goto` | `Tab.Goto` (non-matching URL) |
| `nav` | `Tab.Goto` (same-address reload), `back`, `forward`, `reload` |
| `eval` | `Tab.eval` (unwraps `{value}`), `Tab.kit` (isolated world `__ompTernKit`), `Tab.evalUser` (page world), `metrics`, `storage`, `saveState`/`loadState`, `pushState`, `evaluate`, `element.evaluate` |
| `dom` | `observe` (`snapshot`), `target` (`query` + `resolve`), `waitFor*` (`wait`), `markRef`, `frameRootNative` |
| `input` | `click`, `dblclick`, `hover`, `focus`, `check`, `uncheck`, `clickAt`, `mouseMove`, `mouseDown`, `mouseUp`, `wheel`, `scroll`, `drag`, `keyDown`, `keyUp`, `press`, `type`, `fill` |
| `events` | `Tab.pull` (advances cursor by `events[].seq`), adoption `next` cursor skip |
| `scripts` | `Tab.syncScripts` (`[{source, world, frames, at}]` with capture + init scripts + kit/frame-announce), `Tab.Close` fallback |
| `capture` | `Tab.captureBytes` (`{scale, format, quality?, rect?, full?}`) |
| `pdf` | `pdf` (creates parent dirs, writes file, returns path; refuses Chromium-only layout options) |
| `viewport` | `Tab.SetViewport`, `Tab.Reuse`, `emulate` |
| `edit` | `fill` (`selectAll`), `clipboardCopy` (`copy`), `clipboardPaste` (`paste`) |
| `clipboard` | `clipboardRead`, `clipboardWrite` |
| `cookies` | `cookies` (filtered by URL), `clearCookies` (filtered by URL/names), `saveState` (written to `<AgentDir>/browser-state/<sanitized>.json` or requested path) |
| `setCookie` | `setCookies` (wrapped in `{cookie:{...}}`) |
| `deleteCookie` | `clearCookies` |
| `dialog` | `handleDialog` (`{accept, text?}`), `setDialogs` (settles held dialog) |
| `dialogs` | `configureOpen` (always sent), `adoptBlock` (when given), `setDialogs`, `Reuse` |
| `downloads` | `enableDownloads` (`mkdir -p` + default temp dir) |
| `files` | `uploadFile` (waits for `chooser` event, disarms with `paths: null` on fallback to kit `setFiles`) |
| `allow` | `applySettings` (with `NormalizeAllowedDomains`) |
| `agent` | `applySettings`, `emulate` (`userAgent`) |
| `appearance` | `emulate` (`colorScheme`) |
| `credentials` | `emulate` (`credentials`) |
| `insecure` | `applySettings` |

Ops defined in `ops.go` but not sent by default: `identity` (WebKit host only; no `IdentitySeed` on `browser.OpenOptions`), `fileAccess` (WebKit host only; no `fileAccess` method in `Tab.Call`).

## Tests ported
- 37 Go test functions in `upstream_test.go` (using upstream-faithful `fake_daemon_test.go`), covering:
  - 27 Go test functions porting upstream cases from `browser-tern-tab.test.ts`, `browser-tern-adopt.test.ts`, and `browser-tern-dom.test.ts` (including multi-case grouped tests covering 38 upstream cases total; remaining 7 upstream cases are Puppeteer-guest-runtime `tab.page.$$`/`page.on`/`tab.run` or scaled-PNG screenshot resizing tests that do not apply because Go has no guest JS `tab.run` runtime or PNG resizer).
  - 10 safety, regression, and wire-shape tests (`S1`, `S2`, `S5`, double-`Register`, `N1`, `N2`, `N3`, `N4`, `N8`, wheel/capture/selector wire shapes).

## Settings / Environment variables
| Key | Default | Honoured |
|---|---|---|
| `browser.tern` | `true` | Checked by `internal/browser` resolver; `NewBackend()` defaults `settingEnabled: true` |
| `PI_BROWSER_TERN` | unset | Yes (`Availability`, via `ternwire.ParseFlag`) |
| `TERN_PANE_SOCKET` | unset | Yes (`Availability`) |
| `TERN_PANE` | unset | Yes (`Availability`) |
| `PIG_CODING_AGENT_DIR` / `sdk.AgentDir()` / `PIG_HOME` | `~/.pig/agent` | Yes (`storageStatePath` default dir `<AgentDir>/browser-state/<name>.json`) |

## Not ported / Upstream refusals
| Feature | Reason |
|---|---|
| `traceStart`, `traceStop`, `profileStart`, `profileStop` | Refused by omp's Tern backend (`the web view exposes no performance-trace/CPU-profiler protocol`). |
| `webmcpList`, `webmcpInvoke`, `webmcpEvents` | Refused by omp's Tern backend (`the Tern web view has no native WebMCP; open the tab with app: "chromium"`). |
| `emulate` (`timezone`, `headers`, `media`, `reducedMotion`, `cpuThrottling != 1`, `network`, mobile/touch viewport) | Refused by omp's Tern backend with upstream error text. |
| `pdf` layout options (`format`, `landscape`, `scale`, `printBackground`, `margin`, `pageRanges`) | Refused by omp's Tern backend with upstream error text. |
| `route` non-fetch/xhr `resourceType` | Refused by omp's Tern backend (`only the page's fetch and xhr requests can be routed`). |
| `tab.run()`, `TernPageFacade` (`page.$$`, `page.on`, `page.mouse`) | No Puppeteer/Node guest JS runtime in Go; `Tab.Run` returns explicit refusal pointing to `tab.evaluate()` and direct helpers. |
| `UserSourceFunction` JS parser | Uses a token-aware JS scanner (skips strings, templates, regex literals, comments, nesting) instead of a full V8/JSC `new Function()` compile; valid JS expressions vs statement lists are handled accurately except exotic ASI multi-line statement lists without semicolons or leading keywords. |
| `requests()` filters (`filter`, `type`, `method`, `status`, `since`) | Only `limit` and `clear` are applied currently (`network.ts:719-728` filter predicates not yet wired). |
| `setCookies()` Cookie-header / cURL-dump string parsing | Accepts cookie object or array of cookie objects only (`storage-state.ts` raw Cookie/cURL parser not ported). |
| `a11y` (axe-core), `extract` (Readability), `diffScreenshot` (PNG pixel diff), `devices` (Puppeteer device list), `vitals`/`react*`, `record*` (ffmpeg) | Upstream relies on npm packages (`axe-core`, `@mozilla/readability`, `puppeteer-core` device catalog) or ffmpeg subprocesses not available under the Go stdlib-only rule; each returns an explicit unsupported error rather than fabricated output. |
| `screenshot` `annotate` / `ifChanged` / image resizing, `ariaSnapshot` `diff` / `urls` post-processing, `waitForNavigation` / `waitForFunction` in `Tab.Call` | Not in `internal/browser`'s `Tab.Call` allowlist or depends on npm/image-diff packages; `Tab.WaitForResponse` is exported for direct callers. |
| `request(id)` response body loading from page capture store | `request(id)` returns the logged `RequestRecord`; page-side body retrieval via `__ompTernCapture.body(id)` is not yet wired into `request(id)` / `harStop()`. |
| Background `events` `wait: true` dialog watch (`#watchEvents`) | Only omp's WebKit host holds `wait: true` (`waitsForEvents = false` on Tern daemon). |
