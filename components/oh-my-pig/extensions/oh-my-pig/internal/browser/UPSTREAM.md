# internal/browser — Upstream Tracking

Upstream release pin: **omp `v19.1.1`** (`31ffee513f`).

## Upstream files followed

| Upstream path (`packages/coding-agent/src/...`) | Local file(s) |
|---|---|
| `tools/browser/tab-protocol.ts` | `backend.go` |
| `tools/browser/tab-call.ts` | `tab_call.go`, `backend.go` |
| `tools/browser.ts` | `resolve.go`, `manager.go`, `register.go` |
| `tools/browser/registry.ts` | `backend.go`, `resolve.go`, `chromium.go` |
| `tools/browser/tab-supervisor.ts` | `manager.go` |
| `tools/browser/settings.ts` | `resolve.go` (`Settings`) |
| `tools/browser/terminal-owner.ts` | `resolve.go` (`OwnsTerminal`) |
| `tools/browser/prelude-definition.ts` | `register.go` |
| `tools/browser/prelude.js` | `assets/prelude.js` (verbatim copy, embedded) |
| `tools/browser/prelude.py` | `assets/prelude.py` (verbatim copy, embedded) |
| `prompts/tools/browser.md` | `assets/browser.md` (verbatim copy, embedded) |
| `prompts/tools/browser-core.md` | `assets/browser-core.md` (verbatim copy, embedded) |
| `prompts/system/browser-user-session.md` | `assets/browser-user-session.md` (verbatim copy, embedded) |
| `prompts/system/browser-relay.md` | `assets/browser-relay.md` (verbatim copy, embedded) |
| `prompts/system/browser-x11.md` | `assets/browser-x11.md` (verbatim copy, embedded) |
| `tools/browser/aria/aria-snapshot.bundle.txt` | `assets/aria-snapshot.bundle.txt` (verbatim copy, embedded) |
| `tools/browser/aria/aria-snapshot.ts` | `pagekit.go`, `patterns.go`, `chromium_capture.go` |
| `tools/browser/snapshot-plus.ts` | `pagekit.go`, `chromium_capture.go` |
| `tools/browser/launch.ts` | `chromium_launch.go`, `proc_unix.go`, `proc_windows.go` |
| `tools/browser/attach.ts` | `cdp.go`, `chromium.go`, `chromium_launch.go` |
| `tools/browser/navigation.ts` | `patterns.go`, `resolve.go`, `chromium_nav.go` |
| `tools/browser/network.ts` | `patterns.go`, `chromium_events.go`, `chromium_capture.go` |
| `tools/browser/query-handlers.ts` | `patterns.go`, `pagekit.go`, `assets/pagekit.js` |
| `tools/browser/queries.ts` | `chromium_query.go` |
| `tools/browser/interactions.ts` | `chromium_input.go`, `assets/pagekit.js` |
| `tools/browser/selector-wait.ts` | `helpers.go`, `chromium_query.go` |
| `tools/browser/op-timeouts.ts` | `tab_call.go`, `chromium_query.go`, `chromium_input.go` |
| `tools/browser/screenshot.ts` | `helpers.go`, `chromium_observe.go` |
| `tools/browser/pointer-space.ts` | `helpers.go`, `chromium_observe.go`, `chromium_dispatch.go` |
| `tools/browser/emulation.ts` | `helpers.go`, `chromium_emulate.go` |
| `tools/browser/dialogs.ts` | `chromium_events.go`, `chromium_dispatch.go` |
| `tools/browser/frames.ts` | `chromium_frames.go` |
| `tools/browser/console-capture.ts` | `chromium_events.go`, `chromium_capture.go` |
| `tools/browser/downloads.ts` | `chromium_events.go`, `chromium_capture.go` |
| `tools/browser/select-options.ts` | `chromium_input.go`, `assets/pagekit.js` |
| `tools/browser/storage-state.ts` | `chromium_capture.go` |
| `tools/browser/init-scripts.ts` | `chromium.go`, `chromium_dispatch.go` |
| `tools/browser/open-options.ts` | `manager.go` |
| `tools/browser/tern/kind.ts` | `resolve.go` |
| `tools/browser/relay/kind.ts` | `resolve.go` |
| `tools/browser/webkit/kind.ts` | `resolve.go` |
| `tools/browser/cmux/rpc.ts` | `resolve.go` |
| `utils/redact-url.ts` | `resolve.go` |

## Tests ported

72 top-level Go test functions across `browser_test.go`, `review_fixes_test.go`, `review_fixes2_test.go`, `pass2_unit_test.go`, `pass2_real_test.go`, `reload_test.go`, `pass3_real_test.go`, `pass4_test.go`, `pass5_real_test.go`, `pass6_test.go`, and `pass7_test.go`:
- `TestResolveTernKind` / `TestResolveBrowserKind_Table` / `TestResolveWebKitKind` (ported cases from `browser-tern-kind.test.ts`, `browser-relay-kind.test.ts`, `browser-webkit-kind.test.ts`)
- `TestValidateTabCallChain` (ported from `browser-tab-call.test.ts`, including full `Direct helpers:` text check)
- `TestURLPatterns` (ported from `browser-url-patterns.test.ts`, plus `allowed_domains` normalization/validation)
- `TestDocumentationAndGuidance` (ported from `browser-prelude-documentation.test.ts`)
- `TestRegisterAndPreludeExport` / `TestManagerTernFallbackReuseAndIdleClose` / `TestCDPClientAgainstFakeEndpoint`
- Pass-1 through Pass-7 unit regression tests (`review_fixes_test.go`, `review_fixes2_test.go`, `pass2_unit_test.go`, `pass4_test.go`, `pass6_test.go`, `pass7_test.go`): single `configureSession` state parity before/after reattach and fail-closed `Fetch.enable` + `Target.setAutoAttach`, child-target `Fetch.enable` ordering and fail-closed on iframe attach, at-most-once action dispatch on context abort, covered-click `ClickRefusedError` and all omp timeout hint branches (`formatSelectorMatchHint`, refusal + match count, `typingInterruptedError`, probe failure/timeout, `longerBudgetHint`), omp `captureAfterFrame` bounded stalled-renderer screenshot error, per-op timeout keeping tab alive (no `DOM.getDocument` probe), `Page.stopLoading` + bounded `readyState` poll on goto timeout, zero CDP traffic in the requested-navigation window, dropped-reply recovery on read-only queries, isolated-world never falling back to main world, per-session documentation/guidance isolation, stale profile cleanup.
- Opt-in real headless Chromium E2E tests (gated behind `OMP_REAL_BROWSER=1` so default `go test` never touches `$HOME`): `TestRealHeadlessChromiumEndToEnd`, `TestRealHeadlessChromiumActionabilityFramesAndScreenshot`, `TestRealPass2_*`, `TestRealPass3_*`, `TestRealPass4_*`, `TestRealPass5_*`, `TestRealPass6_*`, `TestRealPass7_*`.

## Helper Classification (Read-Only vs Action)

Actions are routed through `evalKitAction` / direct CDP input commands and sent **at most once**; they can never enter `evalKitRead`'s navigation-retry loop (upstream: `tab-worker.ts:3026-3034, 3063-3098`).

| Helper | Read-only or Action | May retry on context loss? |
|---|---|---|
| `url`, `title`, `frames` | Read-only | No (single read behind barrier) |
| `text`, `html`, `value`, `attr`, `count`, `box`, `styles` | Read-only | Yes — `evalKitRead` (max 2 retries, ONLY on navigated-context error with no result received) |
| `isVisible`, `isEnabled`, `isChecked` | Read-only | Yes — `evalKitRead` (max 2 retries, ONLY on navigated-context error) |
| `observe`, `ariaSnapshot`, `extract`, `screenshot` (element clip) | Read-only | Yes — `evalKitRead` / `evalKitReadObjectID` (max 2 retries, ONLY on navigated-context error) |
| `waitFor`, `waitForSelector`, `waitForText`, `waitForUrl` | Read-only polling | Yes — polling loop ignores navigated-context errors until its own timeout |
| `pdf`, `cookies`, `storage`, `console`, `errors`, `requests`, `request`, `downloads`, `allowedDomains`, `dialog`, `devices`, `metrics`, `initScripts` | Read-only | No |
| `click`, `dblclick`, `hover`, `check`, `uncheck` | Action | **NEVER** (selector presence + actionability check may poll before input; mouse click/move dispatched at most once) |
| `fill`, `select`, `focus`, `scroll`, `scrollIntoView`, `highlight` | Action | **NEVER** (`evalKitAction` dispatches expression at most once; never retries after send) |
| `press`, `type`, `keyDown`, `keyUp`, `mouseMove`, `mouseDown`, `mouseUp`, `clickAt`, `wheel`, `drag` | Action | **NEVER** (`press({selector})` focus uses `evalKitAction`; raw input events dispatched at most once) |
| `uploadFile` | Action | **NEVER** (`evalKitReadObjectID` locates element only; `DOM.setFileInputFiles` sent at most once) |
| `goto`, `back`, `forward`, `reload`, `pushState` | Action | **NEVER** |
| `evaluate`, `element.evaluate` | User code (may mutate) | **NEVER** (runs behind navigation barrier once; matches `tab-worker.ts:3366-3373`) |
| `emulate`, `setCookies`, `clearCookies`, `setStorage`, `clearStorage`, `addInitScript`, `removeInitScript`, `setDialogs`, `handleDialog`, `clearConsole`, `clearRequests`, `waitForDownload` | Action / State | **NEVER** |
| Element handle actions (`id(n)` / `ref(eN)` `.click`, `.fill`, `.select`, `.focus`, `.press`, `.type`, `.check`, `.uncheck`, `.hover`, `.scrollIntoView`, `.highlight`, `.uploadFile`, `.evaluate`) | Action | **NEVER** (`callFunctionOnValue` / CDP input sent at most once) |

## Recovery Mechanisms

| Mechanism | omp counterpart (`v19.1.1`) | Disposition | Notes |
|---|---|---|---|
| Requested-navigation barrier (`mainFrameNavigationRequested` on `Page.frameRequestedNavigation`, cleared on `frameNavigated` / `navigatedWithinDocument` / `frameStoppedLoading` / dismissed `beforeunload`, never on `frameClearedScheduledNavigation`) | `tab-worker.ts:1988-2015, 2256-2261, 3014-3023`; `navigation.ts:106-141` | **Kept** | Sends zero CDP commands while flag is true. |
| `awaitRequestedNavigation` bound (`min(5000ms, perOpMs/2)`) | `tab-worker.ts:387, 3014-3023`; `navigation.ts:106-141` | **Narrowed** (deleted `Page.stopLoading` and error at bound; deleted silent 1.5s `reattachSession`) | Matches omp: clears flag and returns nil so slow navigations (>5s) are never cancelled and the helper's own timeout governs. |
| `sendInContext` 250ms abort on `executionContextDestroyed`/`executionContextsCleared` | None in omp (Puppeteer's `ExecutionContext` rejects via protocol/world disposal; Go CDP client would wait for the full call budget when Chrome silently drops a reply on a cleared context) | **Narrowed** (deleted 5s `navTimer` + `Page.stopLoading`) | Side-effect-free abort only: surfaces Chrome's own `"Execution context was destroyed"` after 250ms; actions never retry on it. |
| `evalKit*` navigation-hop retry loop | `tab-worker.ts:3026-3034, 3063-3067` (`#rereadAfterNavigation`) | **Narrowed** (deleted post-success re-run; split into `evalKitRead` max 2 retries vs `evalKitAction` zero retries) | Only read-only queries may re-read on navigated-context error; actions cannot reach the retry loop. |
| `probeSessionResponsive` (`Target.getTargetInfo`) | None (omp's `#pageClosed` at `tab-worker.ts:2767-2783` only distinguishes closed pages) | **DELETED** | Browser process answers `Target.getTargetInfo` even when the renderer is wedged, so the guard was ineffective. |
| `reattachSession` | `tab-supervisor.ts:1664-1682, 2746-2830`; `tab-worker.ts:2426-2445` (worker replacement on whole-call timeout) | **Narrowed & overlapped** (used ONLY on whole-call timeout in `manager.go:handleCallFailure`) | Attaches the NEW session and runs `configureSession()` (`Fetch.enable` + `Target.setAutoAttach` FIRST, fail-closed) BEFORE detaching the old session, so 0 requests escape `allowed_domains` across reattaches; preserves page JS state and appends omp's `RECYCLE_RESETS` + `longerBudgetHint` + stalled-op clause. |
| `Page.stopLoading` on `goto` / `waitForLifecycle` timeout | `tab-worker.ts:3145-3152, 4366-4380` (`#stopLoading`) | **Kept (narrowed to match omp)** | Sent unguarded best-effort only on navigation timeout; each `document.readyState` poll in `waitForLifecycle` is bounded to 1.5s. |
| Cold-start `Page.enable` retry + target recreation in `openCDPTab` | None in omp | **DELETED** | Removed; `openCDPTab` calls `configureSession` directly and returns any error. |
| Per-op deadlines (`quickOpMs=20s`, `actionOpMs=8s`, `zeroMatchFailFastMs=2s`) | `op-timeouts.ts:20-51`; `tab-worker.ts:2865-2982` | **Kept (exact omp parity; deleted `DOM.getDocument` probe)** | Matches omp's per-op ceilings, selector-bearing labels, `failed fast after 2000ms` zero-match watchdog (inconclusive navigated-context probes never count toward the 2s window or leak raw CDP errors), and all `#runOp` hint branches. Tab is NEVER closed on per-op timeout. |
| Renderer-crash (`Inspector.targetCrashed`) recycling | `tab-worker.ts:2038-2052`; `tab-supervisor.ts:1541-1554` | **Kept** | Fails immediately with explicit message (reminding caller to re-pass `allowed_domains` if set) and closes/removes only the dead tab so sibling tabs and other sessions keep working and `browser.open()` with the same name succeeds. |
| Whole-call timeout handling | `tab-supervisor.ts:1664-1682`; `tab-worker.ts:2611` | **Kept** | Reattaches fresh CDP session via overlapping `reattachSession` (calling `configureSession`), preserves page JS state, reports omp's verbatim `"omp reattached the tab to a new worker without reloading its page."` + stalled-op clause + `RECYCLE_RESETS` + `longerBudgetHint`; releases tab if reattach fails (reminding caller to re-pass `allowed_domains` if set). |
| Headless GPU-ready handshake (`SystemInfo.getInfo` once after `DialCDP`, `opts.Headless` only) | None in omp (omp stalls identically under load on locked macOS displays when a target is created before Chrome's GPU/Viz service finishes initializing) | **Added (port addition, headless launch wait capped at `min(5s, remaining/2)`, floor `500ms`)** | Runs only on headless launches of the port's own Chromium (not headed, connected, or spawned). Prevents Chrome 150's startup race where creating a target before GPU/Viz initialization leaves the tab without a compositor frame sink (stalling `Page.captureScreenshot` and `requestAnimationFrame` indefinitely). |
| `whileDrawing` separate-session screencast + `captureAfterFrame` (`waitForRenderFrame(1500ms)` + `fromSurface:false` 5s fallback) | `screenshot.ts:363-504` | **Ported verbatim (retry deleted)** | Separate CDP session runs `Page.startScreencast`/`stopScreencast`; waits up to 1500ms for a render frame, falls back to `fromSurface:false` capped at 5s, and returns omp's exact named error (`Screenshot failed: the page's renderer stopped drawing ...`) instead of waiting 20s. On owned headless Chrome only, sends `Page.bringToFront` before capture and standalone `mouseMoved`/`hover` so older tabs on a locked macOS display draw and answer input without touching any user-visible tab/window. |

## Chrome Page Wedge Bound

Chrome for Testing 150.0.7871.24 occasionally wedges a page session (dropping a CDP reply) across link-click navigations (~1 in 2000–3000 clicks, reproducible with bare CDP). Matching omp (`tab-worker.ts:2921-2944`, `op-timeouts.ts:20-51`):
- **Bound:** Bounded by the helper's per-op ceiling (`actionOpMs = min(budgetBound, 8000ms)` for actions, `quickOpMs = min(budgetBound, 20000ms)` for reads; shorter cell `timeout` shrinks both via `budgetBound = timeoutMs - 2000ms`). On `goto`, bounded by `budgetBound` with 1.5s `readyState` polls.
- **Tab disposition:** The tab is **kept open** on a per-op timeout (matching omp — there is NO `DOM.getDocument` probe that could mistakenly kill a busy JS handler or a hung navigation). A whole-call timeout (e.g. `evaluate` or an uncapped call exceeding the cell budget) reattaches a fresh CDP session via `reattachSession` without reloading the page; only `Inspector.targetCrashed` or a failed reattach closes the tab (sparing sibling tabs and other sessions).
- **What the model sees:** omp's named per-op timeout with selector label and hint (e.g. `tab.click("#sel") timed out after 8000ms; could not count matches: no answer within 1000ms`), or `"Browser process exited or lost its CDP connection"` if Chrome itself died.

## Locked macOS Display Behaviour (Chrome 150)

When macOS is locked (`CGSSessionScreenIsLocked=Yes`), Chromium's headless compositor exhibits four behaviours measured directly on Chrome 150.0.7871.24 (including with omp `v19.1.1`'s exact flags and Puppeteer `25.3.0` capture path):
1. **Cold-launch GPU/Viz race:** Creating a target before Viz initializes leaves the browser with no active frame sink (omp itself fails 5/5 under 24 hogs with `waitForRenderFrame` timing out at 1.5s and `fromSurface:false` timing out at 5s). Our `SystemInfo.getInfo` barrier once after `DialCDP` eliminates this race.
2. **Locked-display DisplayLink pause after ~0.5s idle:** On a locked macOS display, macOS pauses CVDisplayLink ticks after ~0.5–1s idle — even on a **single active tab**, omp's exact flags stall `requestAnimationFrame` (>1500ms), `captureScreenshot` (even `fromSurface:false` hits its 5s cap), and standalone `mouseMoved`/`hover` (5005–5014ms per call).
3. **Older background tabs stop drawing:** Opening a second tab causes the older tab to stop receiving BeginFrame ticks (both omp and the port without `bringToFront` see 0 screencast frames, `waitForRenderFrame` timing out at 1.5s, `fromSurface:false` timing out at 5s, and `hover` taking 5.0s).
   - **Port additions (documented deviations from omp, darwin headless only):**
     - `--run-all-compositor-stages-before-draw` (added in `buildHeadlessLaunchArgs` on headless `darwin` only) keeps headless BeginFrame pumping on demand after idle without disabling GPU/WebGL. Measured after 1.5s idle under 24 CPU hogs: **8/8 PASS** (26–48ms screenshots on both older and newer tabs, 5–12ms hovers across 3 idle-separated calls; omp exact flags failed 100% on the same probe).
     - `Page.bringToFront` before `captureScreenshotWhileDrawing` and `dispatchMouseMove` for **owned headless** tabs only (`KindHeadless && headless && ownsPage`) restores BeginFrame ticks on older tabs without touching any user-visible, connected, adopted, or headed window.
     - For connected/adopted/headed tabs (where neither the headless launch flag nor `bringToFront` applies), omp's `captureAfterFrame` (`waitForRenderFrame(1500ms)` + `fromSurface:false` 5s cap) fails cleanly in ~6.5s with omp's exact named error (`Screenshot failed: the page's renderer stopped drawing ...`).
4. **500ms input-ack pacing on the active tab:** On a locked display, Chrome's active headless tab incurs a ~500ms compositor-frame wait on `mousePressed` after navigation (~every second click, observed identically in omp/Puppeteer: 501ms `mouseDown` on alternating navigating clicks). Long navigating-click loop tests (`TestRealPass4_F1_NavigatingClickLoop2000Iterations`, `TestRealPass5_F1_NoURLTabNavigatingClickLoop2000Iterations`) accept `OMP_LOOP_ITERS` (default `2000`) to scale iteration count on a locked, loaded machine without weakening any per-iteration assertion.

## Headless Launch Flags (vs omp `launch.ts` + Puppeteer `25.3.0`)

`buildHeadlessLaunchArgs` (`chromium_launch.go`) passes omp's `launch.ts:655-689, 917` flags plus the Puppeteer `25.3.0` `defaultArgs` retained after omp's `stealthIgnoreDefaultArgs` (`launch.ts:72-82, 741-743`) and `patches/puppeteer-core@25.3.0.patch`:
- Included: `--disable-background-networking`, `--disable-background-timer-throttling`, `--disable-backgrounding-occluded-windows`, `--disable-breakpad`, `--disable-crash-reporter`, `--disable-dev-shm-usage`, `--disable-hang-monitor`, `--disable-infobars`, `--disable-prompt-on-repost`, `--disable-renderer-backgrounding`, `--disable-search-engine-choice-screen`, `--disable-sync`, `--export-tagged-pdf`, `--force-color-profile=srgb`, `--generate-pdf-document-outline`, `--no-first-run`, `--no-default-browser-check`, `--password-store=basic`, `--use-mock-keychain`, `--enable-features=PdfOopif`, `--no-sandbox`, `--disable-setuid-sandbox`, `--disable-blink-features=AutomationControlled`, `--allow-file-access-from-files`, `--hide-scrollbars`, `--window-size=W,H`, `--no-startup-window`, `--remote-debugging-port=0`, `--user-data-dir=...`, `--remote-debugging-pipe` (Unix), `--headless=new` + `--mute-audio` (headless mode), `--disable-features=MacAllowBackgroundingRenderProcesses` (darwin), `--run-all-compositor-stages-before-draw` (headless darwin port addition: prevents CVDisplayLink idle freeze on locked macOS displays where omp itself fails `waitForRenderFrame`/`captureScreenshot`/`hover`), `--ignore-certificate-errors` (when `insecure`), `PUPPETEER_PROXY*` flags, `about:blank`.
- Intentionally omitted vs omp's daemon-launched Chrome:
  - `--enable-features=WebMCP,WebMCPTesting,DevToolsWebMCPSupport` — WebMCP (`tab.webmcp*`) is not ported (listed in "Not ported" below).
  - Puppeteer's stealth-suppressed flags (`--enable-automation`, `--disable-extensions`, `--disable-default-apps`, `--disable-component-extensions-with-background-pages`, `--disable-popup-blocking`, `--disable-client-side-phishing-detection`, `--allow-pre-commit-input`, `--disable-ipc-flooding-protection`, `--metrics-recording-only`, and Puppeteer's default `--disable-features=...` list) — omitted to match omp's `stealthIgnoreDefaultArgs` and `ChromeLauncher.js` patch.
## Debug Environment Variables

- `OMP_BROWSER_TRACE_CDP=1`: when set, appends the last 80 entries of the tab's bounded 256-entry CDP wire ring buffer (`cdp_trace.go`) to whole-call timeout error messages for post-mortem diagnosis (off by default).
- `OMP_REAL_BROWSER=1`: opts into real headless Chromium E2E tests during `go test` (off by default).
- `OMP_LOOP_ITERS=<N>`: overrides the iteration count (default `2000`) for the navigating-click loop tests without changing any assertion per iteration.
## Settings

Per-session omp settings are read via the nil-safe package hook `browser.Setting func(ctx sdk.Context, key string) any`, which the integrator assigns from `internal/settings` (falling back to defaults / `DefaultManager().SetSettingsProvider(...)` when `Setting == nil`). All reads happen per call (`Enabled`, `Invoke`, `turn_end`) with no shared cross-session settings cache:

| Key | Default | Honoured |
|---|---|---|
| `browser.enabled` | `true` | Yes (via `browser.Setting(ctx, "browser.enabled")` in `Enabled`) |
| `browser.cdpUrl` | `""` | Yes (via `browser.Setting`, `ResolveBrowserKind`, X11 doc check) |
| `browser.relay` | `false` | Yes (via `browser.Setting`, resolved in `ResolveBrowserKind`; relay backend not ported) |
| `browser.relayUrl` | `""` | Yes (via `browser.Setting`, X11 doc check) |
| `browser.headless` | `true` | Yes (via `browser.Setting`, `--headless=new` vs windowed launch) |
| `browser.cmux` | `true` | Yes (via `browser.Setting`; cmux backend not ported, auto-falls back) |
| `browser.tern` | `true` | Yes (via `browser.Setting`, dispatches to registered `"tern"` backend with fallback) |
| `browser.webkit` | `true` | Yes (via `browser.Setting`; WebKit backend not ported, auto-falls back) |
| `browser.webkitPath` | `""` | Yes (via `browser.Setting` and `PI_BROWSER_WEBKIT_PATH`) |
| `browser.freezeOnTurnEnd` | `true` | Yes (via `browser.Setting` in `turn_end` hook) |
| `browser.idleCloseSec` | `1800` | Yes (via `browser.Setting`, `ReleaseIdleTabsForOwner` on open and `turn_end`; no background timer) |
| `browser.screenshotDir` | `""` | Yes (via `browser.Setting`, `CallContext.ScreenshotDir`) |
| `tools.maxTimeout` | `0` (uses 300s cap) | Yes (via `browser.Setting`, clamps call timeout in `ClampTimeoutMs`) |

Wiring notes:
- Integrator must wire `browser.Setting` to `internal/settings` (e.g. reading `browser.*` and `tools.maxTimeout` for `ctx`). Until wired (`Setting == nil`), defaults apply.
- `PreludeDefinition` sets `eval.Prelude.DocumentationFor` and `GuidanceFor` using `effectiveSettingsForSDK(c, m)` with zero shared cross-session state (zero-arg `Documentation`/`Guidance` use defaults only).
- PiG's `sdk.Context` does not currently expose subagent task depth (`TaskDepth`), so `InvokeRequest.TaskDepth` stays 0 until the integrator/SDK provides a task-depth accessor.
- Tabs are keyed by `(ownerSessionID, name)`, so two concurrent sessions using `"main"` are isolated and cannot collide; as a consequence, a subagent cannot reach its parent's tab by name as it can in omp (`tab-supervisor.ts:465`), and `/new`/`/resume`/`/fork` end the prior session's tabs.
- **`allowed_domains` enforcement coverage (measured on real Chrome 150 against omp `v19.1.1` / Puppeteer `25.3.0`):** `MatchAllowedDomain` matches omp's `#isAllowed` (`network.ts:454-471`: checks HTTP(S)/WS(S) hostnames, permits `about:`, `data:`, `blob:`, `file:`), and CDP `Fetch.enable` + recursive `Target.setAutoAttach` enforces it on the page session and auto-attached child targets:
  - **Enforced (0 hits on disallowed host in both omp and port):** main-frame navigation & redirects, main-frame `pagehide` `fetch(keepalive)`/`sendBeacon`, all same-process subresources (img/script/fetch/XHR/etc.), cross-site iframes (OOPIFs) and nested OOPIFs, dedicated `Worker` (from main frame and from OOPIFs), `data:`/`blob:`/`about:blank` documents, adopted `target` tabs, and continuously across whole-call-timeout reattaches (0 leaked requests in port via overlapping new-session `Fetch.enable` before old-session detach; omp leaks ~1 during worker swap). Fails closed if `Fetch.enable` or `Target.setAutoAttach` fails on the page session or if `Fetch.enable`/`Target.setAutoAttach` fails on an auto-attached `iframe` (holding the child paused and recording an error entry with the frame URL in `tab.errors()`).
  - **NOT enforced (Chrome/Puppeteer `Fetch` limits — also NOT enforced by omp `v19.1.1`, measured identically on Chrome 150):**
    - `WebSocket` (`ws://`/`wss://`) upgrade handshakes (Chrome's `Fetch` domain does not pause WebSocket handshakes; measured 1 hit in both omp and port).
    - `ServiceWorker` fetches (Puppeteer's `TargetManager.js:257-265` silently detaches from `service_worker` targets so they can be destroyed; measured 1 hit in both omp and port).
    - `SharedWorker` fetches (not attached under a page's `Target.setAutoAttach`; measured 1 hit in both omp and port).
    - `target="_blank"` links and user-gesture `window.open(...)` popups (open a new browser page target outside the restricted tab's CDP session; measured 1 hit each in both omp and port).
    - `<script type="speculationrules">` prefetch requests (browser-initiated prefetch outside page `Fetch` interception; measured 1 hit in both omp and port).
    - Unload-time `fetch(..., {keepalive: true})` and `navigator.sendBeacon(...)` fired inside a cross-site iframe's `pagehide` handler on top-level navigation (Chrome detaches the OOPIF target before emitting `Fetch.requestPaused` for those unload requests, so no `Fetch.requestPaused` event is delivered on either the child or page session; measured 2 hits in both omp and port; the same calls from the main frame ARE blocked, 0 hits).
  - **Crash/reopen note:** Because a crashed tab is closed rather than auto-reloaded (see Not ported), reopening the same tab name starts without `allowed_domains` unless the caller passes `allowed_domains` again; crash/release errors explicitly remind the caller to re-pass the tab's `allowed_domains`.
- Default download directories (`$TMPDIR/omp-downloads-<name>-<ns>`) are removed on tab close only when empty (no completed download), matching omp (`downloads.ts:83, 108` never deletes downloaded files on tab close because callers read the returned path after closing or between calls).
## Not ported

| Feature | Reason |
|---|---|
| `relay` backend (Chrome extension native-host bridge) | Not ported in wave 2; requires omp's Chrome extension and native messaging host. Explicit `app: "relay"` returns a clear error suggesting `app: "auto"`, `app: "chromium"`, or a direct CDP URL. |
| `webkit` backend (omp's ABKit `ABHost`/`ABView`) | Not ported in wave 2; requires omp's custom WPE WebKit binary distribution (`~/.o2/lib/abkit`). Automatic opens fall back to Chromium with omp's fallback notice. |
| `cmux` backend (cmux WKWebView surfaces) | Not ported in wave 2; cmux-specific socket RPC. Automatic opens fall back to Chromium with omp's fallback notice. |
| Managed Chromium auto-download (Chrome for Testing ~180 MB) | Not ported in wave 2; `FindChromiumExecutable` searches cached `~/.omp/puppeteer/chrome`, `PUPPETEER_EXECUTABLE_PATH`, and installed Chrome/Chromium/Brave/Edge (on macOS, system Chrome is used as fallback when no cached Chrome for Testing exists; Brave is also accepted). |
| Project-shared Chromium daemon (`shared-daemon.ts`, `orphan-registry.ts`) | Not ported; oh-my-pig launches per-session Chromium processes cleanly reaped on tab close and `lifecycle.OnEnd` (Unix ties process life via `--remote-debugging-pipe`; Windows has no Job Object tie yet). |
| Stealth JS patches (`00_stealth_tampering.txt` .. `13_stealth_worker.txt`) & UA stripping (`launch.ts:27-44`) | Not ported in wave 2; headless Chromium identifies with its default HeadlessChrome UA unless overridden via `user_agent` or `emulate({ userAgent })`. |
| `tab.run(fnOrCode)` with Puppeteer `page`/`browser` runtime | Cannot exist without Node/Bun + Puppeteer. `cdpTab.Run` returns a clear error directing callers to direct `tab.*` helpers and `tab.evaluate(...)`. Note: the verbatim embedded `assets/browser.md` still mentions `tab.run(...)` as omp's docs do. |
| Post-action DOM settle wait before `observe`/`ariaSnapshot` (`tab-worker.ts:3157-3160`) | Not ported; `observe`/`ariaSnapshot` run immediately when called. |
| Cross-origin iframe DOM/frame helpers & child-frame entries inside `observe()` | `allowed_domains` IS enforced inside cross-site iframes (OOPIFs) via `Target.setAutoAttach` + per-child `Fetch.enable`; however, `tab.frames()`, `tab.frame(...)` DOM actions, and child-frame AX merging inside `observe()` cover same-origin iframes only (not OOPIFs). |
| `allowed_domains` on `WebSocket`, `ServiceWorker`, `SharedWorker`, popups (`target=_blank` / `window.open`), `<script type="speculationrules">` prefetch, and OOPIF `pagehide` keepalive/beacon | Not covered by CDP `Fetch` interception on a page/child session (Chrome does not emit `Fetch.requestPaused` for them); omp `v19.1.1` (Puppeteer request interception) has the exact same limits (verified on Chrome 150). |
| Background idle-close timer (`armIdleCloseForOwner`, `tab-supervisor.ts:2366`) | Idle cleanup runs on `turn_end` and subsequent `open` rather than a background timer goroutine. |
| Per-tab / per-session download isolation (`allowAndName` frame attribution, `downloads.ts:96-112`) | CDP `Browser.setDownloadBehavior` is browser-wide on a shared Chromium process, so download events/directories are shared across tabs and sessions sharing that headless process. |
| `tab.traceStart` / `traceStop` / `profileStart` / `profileStop` | Chrome tracing and CPU profiler capture not ported (`metrics()` is ported). |
| `tab.recordStart` / `recordStop` / `recordRestart` / `recording` | Screen video recording (requires ffmpeg/screencast encoder) not ported. |
| `tab.a11y` (axe-core audit) | Requires bundled axe-core script; not ported in wave 2. |
| `tab.diffScreenshot` / `screenshot({annotate, ifChanged, threshold})` | Pixel-diff and overlay annotation helpers not ported; passing `annotate`, `ifChanged`, or `threshold` returns an explicit error. |
| `tab.clipboardRead` / `clipboardWrite` / `clipboardCopy` / `clipboardPaste` | System/page clipboard shims not ported. |
| `tab.saveState` / `loadState` | Cross-origin storage-state file serialization not ported (`cookies`, `setCookies`, `clearCookies`, `storage`, `setStorage`, `clearStorage` are ported). |
| `tab.vitals` / `reactEnable` / `reactTree` / `reactInspect` / `reactRenders` / `reactSuspense` | React DevTools fiber inspection hooks not ported. |
| `tab.webmcpList` / `webmcpInvoke` / `webmcpEvents` | Experimental Chromium WebMCP bindings not ported. |
| `extract` Readability scoring (`@mozilla/readability`) | Uses DOM `article, main, [role=main]` / `document.body` + `htmlToMarkdown` conversion rather than Mozilla Readability's full DOM scorer. |
| Picker-input probe validation in `fill` (`interactions.ts:935-997`) | `fill` enforces editable/enabled/read-only checks and native prototype value setter, without the synthetic `<input>` probe for date/color/range pickers. |
| Auto-reload of a crashed renderer tab (`RENDERER_RELOADED_NOTE`, `tab-supervisor.ts:329, 1541-1554`) | On `Inspector.targetCrashed`, oh-my-pig fails the call with an explicit error (including a reminder to re-pass `allowed_domains` if the tab had an allowlist) and closes/removes the dead tab so the caller reopens it cleanly with `browser.open()`, rather than reattaching a new worker process and auto-reloading the crashed page. Whole-call timeouts DO reattach a fresh CDP session without reloading the page (enabling `Fetch.enable` on the new session before detaching the old one so 0 requests escape), restore all open-time settings via `configureSession`, preserve JS state, and append omp's `RECYCLE_RESETS` + `longerBudgetHint` + stalled-op clause. |
