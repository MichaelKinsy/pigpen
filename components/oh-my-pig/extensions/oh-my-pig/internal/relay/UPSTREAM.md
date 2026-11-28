# internal/relay — Upstream Tracking

Upstream release pin: **omp `v19.1.1`** (`31ffee513f`).

## Relay core

| Upstream path (`packages/coding-agent/src/...`) | Upstream lines | Local file(s) | Notes |
|---|---:|---|---|
| `tools/browser/relay/protocol.ts` | 1–344 | `protocol.go` | Wire types (`TabSnapshot`, `RelayRpcRequest`, `RelayToExtMessage`, `ExtToRelayMessage`, `ToNativeHost`, `FromNativeHost`, `OverlayAction`, `OverlayInput`, `PanelCall`, `PanelInstall`, `PanelCollabHost`, `FoundDownload`, `HiddenFrame`), protocol version constants, `IsHiddenFrame`, `IsOmpVersion`, `IsOlderOmpVersion`, `StampRelayExtensionScript`, `ReadRelayExtensionStamp`, `StampRelayExtensionManifest`, `RelayRecheckPath`, `RelayRestartPath`. Wire names (`OMP.downloadPath`, `/omp/...`, `ompRelay...`) stay omp's; colliding OS/host identifiers live in `names.go`. |
| `tools/browser/relay/native-host.ts` | 1–255 | `native_host.go` | `RunNativeHost`, `ServeNativeHost`, `EncodeNativeFrame`, `NativeFrameReader`, `LineFrames` (900 KiB chunking without splitting UTF-8 runes), `ResolveSessionCwd`, `link`/`linked` handshake. |
| `tools/browser/relay/native-relay.ts` | 1–190 | `native_relay.go` | `ServeRelayLink`: leader/follower election (`lead()` in-process bind + `PublishRelayEndpoint`, `follow()` over `ws://127.0.0.1:<port>/ext` with `chrome-extension://<id>` Origin), handover on leader exit (`unlinked` -> re-elect -> `linked`), upgrade restart acceptance (`POST /omp/relay/restart`). |
| `tools/browser/relay/discovery.ts` | 1–42 | `discovery.go`, `discovery_unix.go`, `discovery_windows.go` | `PublishRelayEndpoint` (`<pid>-<port>` under `EndpointsDir()`), `DiscoverRelayEndpoints` (live PID check, newest-first). |
| `tools/browser/relay/server.ts` | 1–263 | `server.go`, `ws.go` | Stdlib RFC 6455 WebSocket server + client (`WSConn`, 256 MiB max payload, shared with `internal/browser/cdp.go`), `GET /json/version` (200 / 503 `RelayUnavailableInfo`), `GET /json` & `/json/list`, `WS /cdp?claim=`, `WS /ext`, `POST /omp/extensions/recheck`, `POST /omp/relay/restart`, exact `Origin` authentication checks (`server.ts:151-185`), 30s WebSocket ping keepalive. |
| `tools/browser/relay/bridge.ts` | 1–2852 | `bridge.go`, `bridge_ext.go`, `bridge_cdp.go` | `RelayBridge`: single `chrome.debugger` attachment per tab multiplexed across downstream CDP clients via minted `ST*`/`SP*` pseudo-sessions; `Target.*`/`Browser.*` emulation; `Browser.close` refusal; `Target.createBrowserContext` refusal; per-session `Runtime` virtualization & context replay; tab-wide switches (`Page.setInterceptFileChooserDialog`, `Emulation.setFocusEmulationEnabled`) serialized across sessions and reset before detach; child targets (OOPIFs/workers); tab claiming & serialized Chrome tab grouping (`group`/`ungroup`, user opt-out); page overlay lifecycle (`mount`/`hide`/`show`/`unmount`, input forwarding excluding `CANCELLED_RELEASE_POINT`, capture/screencast coordination); `OMP.claimTarget`, `OMP.downloadPath`, `OMP.takeHiddenFrames`, `OMP.debuggerDetached`, `OMP.foreignFramesHidden`; multi-instance / `browserSession` isolation & affine reconnect window; extension self-update / `RecheckExtensions` / `busy` deferral hooks. |
| `tools/browser/relay/probe.ts` | 1–186 | `probe.go` | `WaitForRelayExtension`, `RequestRelayRestart`, `RelayExtensionNotice`, `RelayVersionOf`, `RelayCdpClientsOf`. |

### Deliberate differences from omp

- **Per-relay bearer token authentication (`discovery.go`, `discovery_unix.go`, `discovery_windows.go`, `server.go`, `native_relay.go`, `probe.go`, `installs.go`, `internal/browser/relay_backend.go`)**:
  - omp's relay server (`server.ts:151-185`) authenticates only by rejecting requests with an `Origin` header. That stops web pages, but any local process (including another user's on a shared host) could connect to `127.0.0.1:<port>/cdp` and drive the user's signed-in Chrome.
  - Because every HTTP/WebSocket client of oh-my-pig's relay is our own Go code (browser backend, follower native hosts on `/ext`, `sync`'s recheck, probe), the leader generates a 256-bit `crypto/rand` hex token and writes it atomically as the content of its `<pid>-<port>` endpoint file (`mode 0600` inside a `0700` directory on Unix; on Windows, the user's profile ACLs under `%USERPROFILE%\.pig\agent\browser-relay\endpoints` restrict access to the current user).
  - Every relay endpoint (`/json/*`, `/cdp`, `/ext`, `/omp/*`) requires `Authorization: Bearer <token>` (constant-time compare; `401 Unauthorized` without a matching token), **in addition to** omp's `Origin` checks (`403 Forbidden`).
  - `GET /json/version`'s `webSocketDebuggerUrl` never carries the token in the URL.
  - On Unix, clients verify ownership (`uid == os.Getuid()`) and permissions (`mode & 0o077 == 0`) on both the endpoints directory and endpoint file before trusting a token. `browser.relayUrl` pointing at a port without a readable, safely-permissioned endpoint file fails with an explicit error explaining why.
  - In dynamic-port mode (`Port == 0`), a second native host follows a live discovered leader under `EndpointsDir()` over `/ext` with its bearer token and promotes to leader when the first leader exits.

### Tests ported (relay core)

- `protocol_test.go`: version parsing/comparison (`browser-relay-probe.test.ts:314-323`), script/manifest stamps, `FromNativeHost` wire serialization, oversized UTF-8 chunking (`LineFrames`).
- `ws_test.go`: RFC 6455 server/client round-trip (small/medium/large frames), 426 non-upgrade, unmasked client frame rejection, fragmented frames with interleaved ping, oversized payload rejection.
- `server_probe_test.go`: discovery `Host` authority handling, 503/200 `ompRelayCdpClients` tracking, `Origin` header 403 enforcement on `/cdp`, `/ext`, `/omp/extensions/recheck`, `/omp/relay/restart` (`browser-relay-server.test.ts`), `WaitForRelayExtension` outcomes and `RelayExtensionNotice` (`browser-relay-probe.test.ts`).
- `native_host_test.go`: `open` chat refusal, invalid first-message refusal, endpoint publish/retire, leader/follower forwarding & handover, restart handshake, `RunNativeHost` over pipes (`browser-relay-native-host.test.ts`).
- `bridge_test.go`: target discovery & `Browser.close` refusal, tab claiming/grouping/user opt-out/ungroup, `Target.createTarget` background + attach policy refusal, `Runtime` virtualization across two clients, tab-wide switches & focus reset before detach, page overlay mount/hide/show/input, `OMP.downloadPath`, `OMP.takeHiddenFrames`, multi-instance isolation, self-update & `busy` deferral (`browser-relay-bridge.test.ts`, `browser-relay-self-update.test.ts`).
- `internal/browser/relay_backend_test.go`: full-chain integration test with a Go fake extension speaking native-host framing through `ServeNativeHost` -> leader/follower -> `RelayServer` -> `RelayBridge` -> `browser.RelayBackend` / `Manager` (`open`, `goto`, `text`, `click`, `screenshot`, two clients on one tab, second browser following + leader handover + reconnect, tab closed underneath, `Origin` refusal, `insecure` and `downloads` rejections).

## Not Ported

| Upstream feature | Upstream location | Disposition / Reason |
|---|---|---|
| Side-panel chat sessions (`open` native-host branch, RPC v2 child spawning, `rpc_chunk` reassembly) | `relay/native-host.ts:84-109, 255-391` | Out of scope (relay only). `ServeNativeHost` answers `{t: "open"}` with an explicit `{t: "exit", code: null, stderr: "..."}` refusal the extension can display. |
| Side-panel collab room discovery & control links (`collabHosts`, `collabLink`) | `relay/bridge.ts:160-167, 835-847` | Out of scope: PiG / oh-my-pig has no collab server or registry. The `PanelCall` hook is ported so `PanelSources` is testable and `installs` works; default `collabHosts` returns `[]` and `collabLink` returns an explicit refusal error. |
| macOS front-app focus restoration (`macOSFrontApp` via `@oh-my-pi/pi-natives`) | `relay/server.ts:18, 137-139`, `relay/bridge.ts:1593-1635` | `RelayBridge.KeepFrontAppWith(read)` and `Page.windowOpen` hook are ported, but `server.go` leaves it unset (no-op) because oh-my-pig is pure Go stdlib with no cgo / ObjC native addon. |
| Unattended Chrome `Secure Preferences` / `.pak` seed / OSCrypt installer | `relay/chrome/*` | Out of scope (manual "Load unpacked" install only). |
| Extension-side TypeScript tests (`relay-link.test.ts` testing `chrome.runtime.connectNative` in Chrome's worker) | `packages/browser-relay/test/relay-link.test.ts` | Runs inside the extension's TypeScript/Bun worker (not Go); Go-side native host and link behavior is tested in `native_host_test.go` and `internal/browser/relay_backend_test.go`. |
| Automatic re-adoption on `OMP.debuggerDetached` (tab-worker / tab-supervisor recovery) | `tools/browser/tab-worker.ts:2059-2065, 2262`, `tools/browser/tab-supervisor.ts:1555-1621` | `RelayBridge` emits `OMP.debuggerDetached` and retracts/re-announces the tab identically to omp (`bridge_ext.go:886-898`), but omp's `tab-worker`/`tab-supervisor` automatic re-adoption loop is not ported. When Chrome detaches the debugger under an open relay tab (user dismisses Chrome's debugging bar with `canceled_by_user`, or opens DevTools with `replaced_with_devtools`, or another extension's frame causes a detach), the relay session ends cleanly and the next call returns `"No open browser tab named \"<name>\". Open one first with browser.open({ name: \"<name>\", url: ... })."` so the model must reopen the tab explicitly (tested in `internal/browser/relay_backend_test.go:TestRelayDebuggerDetachModelError`). |

## Install and extension

### Upstream files followed (`omp v19.1.1`, commit `31ffee513f27a01bba7d4dfbe12bae239cdc646a`)

| Upstream path | Local file(s) |
|---|---|
| `packages/coding-agent/src/tools/browser/relay/extension-files.ts` | `extension_files.go`, `extension_atomic_unix.go`, `extension_atomic_windows.go` |
| `packages/coding-agent/src/tools/browser/relay/embedded-extension.ts` | `extension_files.go` (`//go:embed extension extension/icons`) |
| `packages/coding-agent/src/tools/browser/relay/installs.ts` | `installs.go`, `installs_pid_unix.go`, `installs_pid_windows.go` |
| `packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts` | `native_host_manifest.go` |
| `packages/coding-agent/src/tools/browser/relay/chromium-profiles.ts` (`chromiumUserDataDirs`, `findUnpackedInstalls`) | `chromium_profiles.go` |
| `packages/coding-agent/src/cli/browser-relay-cli.ts`, `commands/browser-relay.ts`, `cli/command-help.ts` | `../../cli_hook.go`, `../../cli_relay.go` |
| `packages/browser-relay/scripts/build-extension.ts`, `packages/browser-relay/extension/` | `gen/build-extension.ts`, `gen/relay-only.patch`, `extension/`, `extension.manifest.json` |

### Embedded relay-only extension

Built deterministically from omp `v19.1.1` via `bun run internal/relay/gen/build-extension.ts --omp-src <omp-v19.1.1>` after applying `internal/relay/gen/relay-only.patch`. The patch removes the side panel (`panel/`), Teach (`teach/`), schedules (`worker/schedules.ts`), GIF export (`offscreen/`, `worker/gif.ts`), panel handlers/hub (`worker/panel-*.ts`, `worker/rpc-sessions.ts`, `worker/capture.ts`, `worker/side-panel.ts`), and sets oh-my-pig's `NativeHostName` (`dev.pi_in_go.oh_my_pig.browser`), `ExtensionName` (`oh-my-pig`), unofficial attribution (`author` and `description`), `homepage_url`, and icon paths (`icons/oh-my-pig-{16,32,48,128}.png`). omp's icons are not shipped: `build-extension.ts` synthesizes oh-my-pig's own plain geometric PNG icons deterministically in code with no external assets or font dependencies. The upstream MIT `LICENSE` is kept intact, and the wire protocol (`OMP_INSTALL_VERSION`, `OMP_INSTALL_BUILD`, `omp-overlay`, `omp-relay-keepalive`, `omp.*` storage keys, message schemas) is untouched. Because only `background.ts` and `overlay/main.ts` are bundled, the shipped extension has **zero** third-party npm runtime dependencies (no React, `lucide-react`, or `gifenc`).

#### Permissions kept in `extension/manifest.json`

Every kept permission is required by the relay-only code path (line numbers in omp `v19.1.1` `packages/browser-relay/extension/`):

| Permission | Justification (upstream file & line) |
|---|---|
| `debugger` | `worker/relay.ts:154,162` (`chrome.debugger.getTargets`, `sendCommand` for `ownedDebuggerTabs`), `worker/relay-rpc.ts:16,22,29` (`attach`, `detach`, `sendCommand`), `worker/relay-events.ts:34,42` (`onEvent`, `onDetach`). |
| `tabs` | `worker/relay.ts:153` (`chrome.tabs.query`), `worker/relay-rpc.ts:39,46,51,54` (`create`, `remove`, `update`, `ungroup`), `worker/relay-events.ts:49,54,59,69` (`onCreated`, `onUpdated`, `onActivated`, `onRemoved`), `worker/groups.ts:39,75,77,79,81,96,98` (`get`, `query`, `group`, `ungroup`), `worker/overlay-host.ts:43` (`sendMessage`), `worker/tabs.ts:57,72` (`remove`, `update`), `background.ts:102,113` (`onUpdated`, `onRemoved`). |
| `tabGroups` | `worker/groups.ts:69,83,94` (`chrome.tabGroups.query`, `chrome.tabGroups.update`) for the per-window relay tab group and dissolving it on disconnect. |
| `storage` | `worker/identity.ts:19,23,39,43` (`chrome.storage.local` for persistent `relayInstanceId`, `chrome.storage.session` for per-run `relayBrowserSession`), `worker/groups.ts:29,66` (`chrome.storage.session` for `ompGroupTitle`), `worker/session-store.ts:20,30` (`chrome.storage.session` for overlay state across service-worker reap), `overlay/overlay.ts:75,76` (`chrome.storage.onChanged`, `chrome.storage.local.get`). |
| `alarms` | `background.ts:117,118` (`chrome.alarms.create("omp-relay-keepalive", ...)` and `onAlarm`) to revive a reaped MV3 service worker and reconnect the native host port. |
| `scripting` | `worker/overlay-host.ts:54` (`chrome.scripting.executeScript` to inject `overlay.js` on demand), `foreign-frames.ts:101,158` (`chrome.scripting.executeScript` to neutralize and report foreign extension frames blocking `chrome.debugger`). |
| `downloads` | `worker/relay-events.ts:20,29,30` (`chrome.downloads.search`, `onCreated`, `onChanged`) to pair `Page.downloadWillBegin` with Chrome's download items for `findDownload` / `OMP.downloadPath`. |
| `nativeMessaging` | `worker/relay.ts:321` (`chrome.runtime.connectNative(NATIVE_HOST)`) to spawn and communicate with the native messaging host. |
| `host_permissions: ["<all_urls>"]` | Required by `chrome.scripting.executeScript` in `worker/overlay-host.ts:54` and `foreign-frames.ts:101,158` to inject `overlay.js` and sweep foreign frames on arbitrary tabs driven by the relay. |

#### Permissions and manifest fields removed

- `sidePanel`, `side_panel`, `commands.toggle-side-panel` — side panel chat UI is not ported.
- `unlimitedStorage` — only used by panel conversation/shortcut/image storage.
- `notifications` — only used by `worker/schedules.ts` (`chrome.notifications.create`/`onClicked`/`clear`).
- `offscreen` — only used by `worker/gif.ts` (`chrome.offscreen.createDocument`/`closeDocument`).
- `homepage_url: "https://omp.sh"` — replaced with oh-my-pig's repository URL.

### Read-only Chrome profile inspection (`FindUnpackedInstalls`)

During `browser-relay install`, `FindUnpackedInstalls` (`chromium_profiles.go`) inspects Chromium-family user data directories strictly **read-only** (it never creates, modifies, or locks any browser file) to report whether the installed directory is already loaded and whether Developer mode is enabled. For each supported browser (`Google Chrome`, `Google Chrome Beta`, `Google Chrome Dev`, `Google Chrome Canary`, `Chromium`, `Brave`), it reads only:

- `<user-data-dir>/Local State` (profile list under `profile.info_cache`)
- `<user-data-dir>/<profile>/Preferences` (`extensions.settings.<id>` and `extensions.ui.developer_mode`)
- `<user-data-dir>/<profile>/Secure Preferences` (`extensions.settings.<id>` and `extensions.ui.developer_mode`)

Any missing, unreadable (e.g., permission denied), or malformed file is silently skipped without error.

### Not Ported (Install and extension)

| Feature | Upstream file | Reason |
|---|---|---|
| Side panel chat UI, Teach recorder, scheduled shortcuts, GIF export | `packages/browser-relay/extension/{panel,teach,offscreen,worker/{side-panel,panel-handlers,panel-hub,rpc-sessions,capture,schedules,teach,gif}.ts}` | Out of scope (relay only; PiG `--mode rpc` lacks omp RPC v2 commands/events and collab rooms). |
| Unattended Chrome profile installer (`--profile` and automatic `Secure Preferences` / HMAC / OSCrypt rewriting) | `packages/coding-agent/src/tools/browser/relay/chrome/*`, `chromium-profiles.ts` (`installedChromiumBrowsers`, `lastUsedProfile`, `stopBrowser`, `startBrowser`, `seedPaks`), `browser-relay-cli.ts:68-96` | Out of scope (manual "Load unpacked" install only; Go stdlib also cannot decompress brotli/zstd Chrome `.pak` resources for seed recovery). `--profile` is explicitly refused with exit code 2; `--no-chrome` is accepted and is the only install path. |
| Auto-sync hook inside `omp update` | `packages/coding-agent/src/cli/update-cli.ts:1112` | oh-my-pig has no `omp update` self-updater; users run `oh-my-pig browser-relay sync` or `install`. |
| Zstd-compressed embedded extension JSON | `packages/coding-agent/src/tools/browser/relay/extension-files.ts:38-42` | Go standard library has no zstd decoder; the relay-only extension (~86 KiB total across 8 files) is embedded directly via `//go:embed`. |
