# internal/relay — Upstream Test Parity Matrix

Mechanically generated against **omp `v19.1.1`** (`packages/coding-agent/test/`). Verified by `TestUpstreamTestsMatrixValid` in `upstream_matrix_test.go`: every row's Go test file and `Test...` function must exist in the repository.


## `browser-relay-bridge.test.ts` (117 cases: 117 ported, 0 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 184 | enumerates current eligible pages without attaching or claiming tabs | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 213 | groups nothing on hello or tab lifecycle events — only claimed tabs join the omp group | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 221 | never groups from command traffic: a discovery scan sending page commands to every tab is not driving | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 238 | groups exactly the tab a client claims | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 251 | never groups pinned tabs or tabs in a user group, even when claimed | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 260 | does not issue group RPCs when grouping is disabled | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 269 | closes a tab Target.createTarget made for a client that disconnected before it opened | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 284 | auto-claims a tab created through Target.createTarget | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 301 | fails Target.createTarget with Chrome's refusal and closes the tab when the new tab cannot be attached | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 328 | creates a Target.createTarget background tab without selecting it | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 345 | leaves createTab selection to Chrome for Target.createTarget $params | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 359 | never re-groups a tab the user pulled out of the omp group | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 379 | ungroups when the claiming client disconnects, even while another connection still holds sessions | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 398 | ungroups a tab whose claiming client closed while its group RPC was in flight | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 412 | never overlaps group RPCs: a tab claimed mid-flight waits for the pending group | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 430 | regroups claimed tabs after an extension reconnect instead of treating the dissolve as user opt-out | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_01_DiscoveryAndGrouping` |
| 496 | owns an explicitly attached child without arming auto-attach | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 578 | announces a real child target only to the page session that armed auto-attach | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 596 | keeps a child on the first armed session of its connection, even after that session is released | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 618 | revokes automatic children when the last local page is released while another connection remains armed | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 662 | isolates opt-out and disarms Chrome when the final armed holder leaves by %s | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 748 | disarms Chrome after another holder's in-flight enable fails | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 786 | retries a failed disable after its holder leaves by %s | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 824 | rejects a queued auto-attach after its page session is released | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 867 | does not report children to a session whose Target.setAutoAttach failed | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 900 | tells a connection that arms auto-attach late about the children Chrome already reported | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 930 | does not tell a later connection about children that ended with the debugger attachment | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 955 | detaches children from connections still holding the tab when the extension lost its attachment | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 980 | virtualizes Runtime enable state for each pseudo-session | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1052 | keeps a pipelined Runtime.disable authoritative while root enable completes | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1082 | refreshes Runtime contexts after the extension reconnects | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1181 | keeps waiting on %s, whose answer waits on the page, until omp's own client gave up | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1192 | fails a command Chrome answers at once after 20 s, naming the command and the tab | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1204 | keeps a tab Chrome detached on its own adoptable, but bans it after the user cancels the debugging bar | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1253 | tells the driving session why Chrome detached before failing its command in flight | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1283 | neither reports nor re-announces the detach of a tab the relay is closing | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1321 | detaches cleanly on explicit last-session release and permits reattachment | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1355 | serializes immediate reattachment behind the detach RPC and its echo | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1389 | reuses an existing debugger attachment for a second connection | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1411 | keeps the attachment while another connection still holds a session on the tab | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1430 | detaches once the tab session released alongside the page session leaves no holder | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1458 | retracts held sessions when reconnect reattachment fails | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1483 | reconciles a delayed detach after replacement hello still reports the old attachment | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamBridge_AdditionalEdgeCases` |
| 1516 | does not ban a tab when its in-flight attach is interrupted by extension replacement | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1544 | clears an in-flight detach immediately when the extension socket is replaced | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1608 | still fans root Runtime events out to a session that never enabled the domain | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1644 | holds a pipelined duplicate Runtime.enable until the in-flight enable settles | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease` |
| 1671 | fails a pipelined duplicate Runtime.enable when the root enable fails | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1697 | preserves the latest disable when an older and newer enable both fail | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamBridge_AdditionalEdgeCases` |
| 1729 | keeps each browser's tabs listed when two instances connect | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_MultiInstancePageLifecycleAutoAttachAndFrontApp` |
| 1748 | scopes the hello GC to the reconnecting instance | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1763 | replaces the socket, resets runtime state, and keeps the registry when the same instance reconnects | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1799 | routes rpcs to the browser instance that owns the tab | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1826 | opens new tabs in the browser showing the first selected tab, not the one that reconnected last | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1840 | opens new tabs in the browser of a tab the client already drives | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1855 | opens a new tab in the browser of nearTargetId after the user closed that tab | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1875 | waits for the browser of nearTargetId to reconnect instead of opening the tab in another browser | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1900 | opens the tab in another browser once the browser of nearTargetId stays offline past its reconnect window | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1927 | retires a browser's targets once it restarted, so an old target id never names a tab that reuses its id | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1952 | closes a tab Chrome creates after the createTab RPC timed out | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 1975 | forgets a timed-out createTab once its late window passes, so a wedged extension cannot grow the relay | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 2013 | does not report a tab closed while its browser's extension is offline, and closes it once that browser reconnects | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2030 | closes a tab whose close its browser dropped mid-call once that browser reconnects | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2052 | does not close a tab whose id now shows another page when its browser reconnects | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2064 | reports a tab the relay no longer knows as closed | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2072 | falls back to a live instance for browser-wide requests when the last hello goes offline | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2101 | hides tabs of an instance whose socket went offline | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2119 | opens, activates and closes targets a page session asks for in that page's browser | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_MultiInstancePageLifecycleAutoAttachAndFrontApp` |
| 2161 | fails a page session's activate or close of a target the relay does not know | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2219 | hides tabs from legacy extensions while allowing supported browsers through the relay | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_MultiInstancePageLifecycleAutoAttachAndFrontApp` |
| 2240 | eagerly attaches live tabs and retracts discarded tabs | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2271 | announces revived tabs and retracts discarded tabs | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2303 | announces and attaches a tab again once its URL changes after Chrome refused its reattach | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2343 | attaches and groups a tab Chrome refused over another extension's frame once a worker claims it | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2373 | does not reattach an initially discovered tab discarded during attach | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2396 | does not reattach a newly announced tab discarded during attach | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2421 | releases auto-attach promptly and retries after an in-flight attach fails on discard | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamBridge_AdditionalEdgeCases` |
| 2456 | rejects stale explicit attachment requests for discarded targets without an extension RPC | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamBridge_AdditionalEdgeCases` |
| 2483 | announces to discovering clients the tabs a browser brings in its hello | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamBridge_AdditionalEdgeCases` |
| 2505 | reannounces a held tab on revival without attaching it while discarded | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2538 | clears cancelled attach state when a discarded extension reconnects | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_03_CloseQueueFallbackAutoAttach` |
| 2617 | holds the app that was in front at omp's input to a tab in front once that tab's page opens a window | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_MultiInstancePageLifecycleAutoAttachAndFrontApp` |
| 2656 | holds no app in front for a window when $case | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2682 | reads the front app only on omp's inputs that can open a window, not on moves, key-ups or inserted text | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2710 | still forwards omp's input when reading the front app throws, and stops reading it | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2731 | reads no front app while a hold runs, so a window opened after it never holds the browser in front | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2768 | ends a hold at its deadline even while the native hold has not settled | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2803 | keeps a rejected native hold guarded until its original deadline | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2843 | keeps only the newly activated tab active in its window | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_MultiInstancePageLifecycleAutoAttachAndFrontApp` |
| 2890 | keeps interception on while another session on the tab still waits for a chooser | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2906 | switches interception off when its last owner detaches or disconnects without disabling it | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2953 | keeps a later enable when an earlier overlapping enable on the session fails | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 2979 | keeps interception on for an owner whose disable failed while another owner's disable succeeded | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3004 | finishes focus reset before detaching a released tab and re-adopting it | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3048 | keeps a tab focus-emulated while any session asks, and ends it when the last client goes away | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3075 | restores a tab's switches after a service-worker restart drops the attachment | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3092 | brings a surviving attachment's switches to the live sessions' wishes when the extension reconnects | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3120 | switches off what a vanished client left on when the relay restarts under a live extension | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_MultiInstancePageLifecycleAutoAttachAndFrontApp` |
| 3132 | settles a switch whose reply was lost with the extension socket | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3152 | lets the extension empty another extension's frames only in a tab a client claimed | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3174 | takes the claim an older omp worker sends on its page session | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3226 | looks up where Chrome saved a completed download | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3249 | tells an extension installed before download tracking to update, without asking it | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3262 | holds what the extension hid for the worker driving the tab, and past a Chrome detach for the next one | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3295 | keeps each hidden frame once, on one line, and only the latest while no worker takes them | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3373 | mounts the overlay when a client claims a tab, again in each new document, and unmounts it when the last claim goes | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3421 | takes the overlay down when Chrome ends the attachment and mounts it again once the tab is attached again | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3445 | draws only root input Chrome acknowledged from the client that claims the tab | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3508 | draws in the browser of the claimed tab when another browser has a tab with the same id | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3528 | hides the overlay before a capture and shows it after the last of overlapping captures | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3589 | captures anyway once the overlay takes too long to hide | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamBridge_AdditionalEdgeCases` |
| 3614 | keeps the overlay hidden while a screencast runs, until it stops or its session goes | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamBridge_AdditionalEdgeCases` |
| 3663 | sends nothing new to an extension that draws no overlay | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3688 | answers the side panel's calls on the socket that made them | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamBridge_AdditionalEdgeCases` |
| 3775 | defers reloading an outdated extension while it is busy, and reloads it once it is not | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3792 | does not reload an extension that turned busy while its files were written, until it is idle again | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |
| 3807 | starts a new socket of the same browser idle | `internal/relay/bridge_upstream_part2_test.go` :: `TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy` |

## `browser-relay-tab-isolation.test.ts` (13 cases: 6 ported, 7 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 386 | keeps another connection's cross-site frame alive when a page disables auto-attach | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamBridge_ChildTargets` |
| 431 | opens default relay tabs in the background and closes them, leaving borrowed pages open | `internal/browser/relay_backend_test.go` :: `TestRelayTabIsolationAndOwnership` |
| 507 | opens a default tab in the browser of the tab omp used last, not the first selected one | `internal/relay/bridge_upstream_part1_test.go` :: `TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance` |
| 552 | closes an owned tab Chrome refuses to close at first | skipped: Puppeteer supervisor + extension-side `removeTab` retry fixture (`tab-supervisor.ts`); bridge-side `closeOnHello` retry is covered by `internal/relay/bridge_upstream_part2_test.go` (`TestUpstreamBridge_03_CloseQueueFallbackAutoAttach`) |
| 573 | leaves a persist tab open in the user's browser when its session ends and closes the others | `internal/browser/relay_backend_test.go` :: `TestRelayTabIsolationAndOwnership` |
| 605 | closes a persist tab whose close joins its session's end | skipped: Puppeteer supervisor `releaseTab` / `releaseTabsForOwner` concurrent race (`tab-supervisor.ts`); `persist` session-end keep vs close is covered by `internal/browser/relay_backend_test.go` (`TestRelayTabIsolationAndOwnership`) |
| 628 | closes a persist tab when its close joins after the keep decision and browser disconnect | skipped: Puppeteer supervisor `browser.disconnect` hook race (`tab-supervisor.ts`); `persist` session-end keep vs close is covered by `internal/browser/relay_backend_test.go` (`TestRelayTabIsolationAndOwnership`) |
| 661 | empties a password manager's menu only in the tab omp drives | skipped: Puppeteer supervisor + stand-in password-manager Chrome extension fixture; bridge `clearForeignFrames` claim gating is covered by `internal/relay/bridge_upstream_part2_test.go` (`TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy`) |
| 724 | says once what it hid when a submit opens a password manager's save card | skipped: Puppeteer supervisor + stand-in password-manager Chrome extension fixture; bridge `framesHidden`/`OMP.takeHiddenFrames` sanitization & once-drain is covered by `internal/relay/bridge_upstream_part2_test.go` (`TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy`) |
| 788 | closes an owned relay tab whose debugger the user dismissed | skipped: Puppeteer supervisor + extension `dismissDebugger` listener fixture; bridge `canceled_by_user` ban & detach is covered by `internal/relay/bridge_upstream_test.go` (`TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease`) |
| 840 | reports a relay tab's downloads, from its page and a cross-site frame, at the path Chrome saved them to | `internal/browser/relay_real_test.go` :: `TestRealChromeRelayEndToEnd` |
| 885 | reports each relay download's own file when a later download of the same URL finishes first | `internal/browser/relay_backend_test.go` :: `TestRelayTabIsolationAndOwnership` |
| 923 | keeps an owned target alive after a failed recovery init so a retry can adopt it | skipped: Puppeteer `WorkerInitPayload` / `startWorker` recovery harness (`tab-worker.ts` not ported — oh-my-pig uses its own in-process Go CDP driver; none of the port's driver spawns worker processes) |

## `browser-relay-detach.test.ts` (16 cases: 0 ported, 16 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 419 | keeps an open's page unless the open was cancelled (%s) | skipped: Puppeteer tab-supervisor/tab-worker auto-reattach harness (`tab-supervisor.ts` / `tab-worker.ts` not ported); bridge detach + re-adopt is covered by `internal/relay/bridge_upstream_test.go` (`TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease`) and `internal/browser/relay_real_test.go` (`TestRealChromeRelayEndToEnd`); port's CDP driver does not auto-reattach detached relay tabs on open |
| 510 | keeps the recovered tab but fails an open whose navigation never started before its budget ran out | skipped: Puppeteer tab-supervisor/tab-worker budget recovery harness (`tab-supervisor.ts` not ported); no auto-reattach-after-detach in port's Go CDP driver |
| 542 | re-adopts a kept relay tab after release and reads the state left on it | skipped: Puppeteer `releaseTab({ keepPage: true })` + worker re-adoption loop; borrowed tab release without close & re-adopt is covered by `internal/browser/relay_backend_test.go` (`TestRelayTabIsolationAndOwnership`) and `internal/browser/relay_real_test.go` (`TestRealChromeRelayEndToEnd`) |
| 555 | fails the call in flight at once, names the cause, and re-adopts the same page for the next call | skipped: Puppeteer tab-worker `OMP.debuggerDetached` in-flight failure & auto-reattach (`tab-worker.ts` not ported); bridge `OMP.debuggerDetached`-before-error ordering is covered by `internal/relay/bridge_upstream_test.go` (`TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease`); port's CDP driver surfaces tab-closed error (`TestRealChromeRelayEndToEnd` (d)) without auto-reattaching |
| 573 | names the detach to a call running in a cross-site frame, whose session Chrome ends first | skipped: Puppeteer tab-worker cross-site frame `OMP.debuggerDetached` attribution (`tab-worker.ts` not ported); bridge child-session detach propagation is covered by `internal/relay/bridge_upstream_test.go` (`TestUpstreamBridge_ChildTargets`) |
| 591 | aborts the host tool calls the detached call had in flight | skipped: Puppeteer tab-worker `toolBridge.callSessionTool` abort on detach (`tab-worker.ts` not ported; Go CDP driver has no in-worker tool bridge) |
| 622 | re-adopts a tab Chrome detached between calls and runs the next call there | skipped: Puppeteer tab-supervisor between-calls auto-reattach (`tab-supervisor.ts` not ported); bridge non-user detach keeps tab adoptable (`TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease`), but port's Go CDP driver requires explicit `browser.open` after detach |
| 639 | runs a call on the re-adopted tab when Chrome detached omp before its code started | skipped: Puppeteer tab-worker pre-code UA re-apply detach recovery (`tab-worker.ts` not ported; no auto-reattach in port's Go CDP driver) |
| 658 | counts a detach while the call's code is still compiling as before it ran | skipped: Puppeteer inline-worker JS compile-step detach check (`rewriteImports.wrapCode` / `tab-worker.ts` not ported; Go CDP driver has no `tab.run()` JS worker) |
| 694 | closes an inline worker before releasing the tab it cannot re-adopt | skipped: Puppeteer inline-worker close ordering (`tab-worker.ts` not ported; Go CDP driver has no inline JS worker) |
| 720 | keeps a tab Chrome refuses to re-adopt, and re-adopts it at a later call | skipped: Puppeteer tab-supervisor held-out foreign-frame tab retention & auto-reattach (`tab-supervisor.ts` not ported); bridge foreign-frame claim clearance is covered by `internal/relay/bridge_upstream_part2_test.go` (`TestUpstreamBridge_03_CloseQueueFallbackAutoAttach`) |
| 754 | replaces a held-out tab without re-adopting ($label) | skipped: Puppeteer tab-supervisor held-out tab replacement (`tab-supervisor.ts` not ported; no held-out tab state in port's Go CDP driver) |
| 780 | lets go of a tab Chrome keeps omp out of (%s) | skipped: Puppeteer tab-supervisor release of held-out tab (`tab-supervisor.ts` not ported; no held-out tab state in port's Go CDP driver) |
| 803 | still lets go of a tab Chrome refuses for any other reason, until its address changes | skipped: Puppeteer tab-supervisor non-foreign refusal release (`tab-supervisor.ts` not ported); bridge refusal cleared on URL change is covered by `internal/relay/bridge_upstream_part2_test.go` (`TestUpstreamBridge_03_CloseQueueFallbackAutoAttach`) |
| 828 | releases the tab without re-adopting it when the user cancels Chrome's debugging bar | skipped: Puppeteer tab-supervisor `canceled_by_user` worker non-respawn (`tab-supervisor.ts` not ported); bridge `canceled_by_user` ban is covered by `internal/relay/bridge_upstream_test.go` (`TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease`) |
| 854 | reports a cancel that lands during the re-adoption as a cancel | skipped: Puppeteer tab-supervisor `AbortSignal` during worker re-adoption (`tab-supervisor.ts` not ported; no worker re-adoption in port's Go CDP driver) |

## `browser-relay-self-update.test.ts` (25 cases: 24 ported, 1 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 116 | rewrites an older extension that reloads on request, then asks it to reload | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 136 | sends nothing more to a browser it asked to reload while another browser stays connected | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 162 | leaves an extension without the reload capability for the manual update | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 169 | leaves an extension installed by this omp or a newer one alone | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 176 | leaves an extension with no omp version stamp alone | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 183 | leaves an extension whose socket named no Chrome extension id alone | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 189 | waits while a CDP client is connected and updates once the last one leaves | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 207 | waits while one of the extension's tabs holds a debugger attachment | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 214 | does not reload when a CDP client connected while the files were written | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 224 | does not reload when the files were not rewritten | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 233 | waits for a tab create that outlived its timeout, whose late result closes the tab | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 280 | releases an attach the last client left mid-way, then updates | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamSelfUpdate_InstalledFolderAndGuards` |
| 312 | waits out a tab create and its close when the client left mid-create, so the tab omp opened is closed | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 354 | waits for a tab close queued for the next hello, which a reload's new browser session would drop | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 395 | reloads once per version, so an extension that comes back with a version it had is not reloaded again | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 436 | reloads an extension running another build of this omp version, once per running build | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamSelfUpdate_InstalledFolderAndGuards` |
| 454 | leaves an extension running its folder's build alone until a recheck finds the folder changed | `internal/relay/bridge_upstream_test.go` :: `TestUpstreamSelfUpdate_InstalledFolderAndGuards` |
| 469 | waits for the last CDP client to leave before reloading | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 484 | never reloads an extension whose folder omp does not manage | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 495 | matches Chromium's id for a path on macOS and Linux | `internal/relay/install_test.go` :: `TestExtensionIDForPath` |
| 500 | matches Chromium's id for a path on Windows, which hashes UTF-16 and upper-cases the drive letter | `internal/relay/install_test.go` :: `TestExtensionIDForPath` |
| 517 | installs the side panel, worker, content scripts, and icons under Chrome's expected paths | `internal/relay/install_test.go` :: `TestEmbeddedExtensionManifestAndBundle` |
| 565 | repairs a corrupted nested icon even when the installed version matches | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 577 | writes background.js last, so an update that fails midway leaves the old version reported | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamSelfUpdate_AllCases` |
| 601 | replaces a file Windows refuses to rename over, as when Chrome or antivirus holds it | skipped: Windows EPERM rename-over-held-file spy mock (compile-only on Darwin; Windows atomic rename fallback lives in extension_atomic_windows.go) |

## `browser-relay-probe.test.ts` (23 cases: 23 ported, 0 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 46 | gives up at once when nothing is listening, as when Chrome is not running | `internal/relay/server_probe_test.go` :: `TestWaitForRelayExtensionOutcomes` |
| 53 | after a restart, waits for the next relay to come up and say hello | `internal/relay/server_probe_test.go` :: `TestWaitForRelayExtensionOutcomes` |
| 64 | rejects an already-running relay without discarded-tab metadata | `internal/relay/server_probe_test.go` :: `TestWaitForRelayExtensionOutcomes` |
| 81 | reports a stale relay before blaming its extension, even if the capability marker matches | `internal/relay/server_probe_test.go` :: `TestWaitForRelayExtensionOutcomes` |
| 95 | accepts a compatible relay from another OMP version | `internal/relay/server_probe_test.go` :: `TestWaitForRelayExtensionOutcomes` |
| 109 | reports a compatible relay from $version with $clients CDP clients as $outcome | `internal/relay/server_probe_test.go` :: `TestWaitForRelayExtensionOutcomes` |
| 132 | rejects an extension without discarded-tab snapshots, even when it has no tabs | `internal/relay/server_probe_test.go` :: `TestWaitForRelayExtensionOutcomes` |
| 140 | keeps polling a young relay and reports ready once the extension handshakes | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 151 | waits through a relay's brief 503 while its browser links again | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 208 | says nothing for an extension installed by this omp | `internal/relay/server_probe_test.go` :: `TestRelayExtensionNotice` |
| 214 | names the older omp that installed a protocol-compatible extension and how to update it | `internal/relay/server_probe_test.go` :: `TestRelayExtensionNotice` |
| 223 | flags an extension installed before omp stamped its version | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 231 | still fails a stamped extension that lacks the relay protocol instead of only noting it | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 237 | names a stale extension when another browser on the relay is current ($order) | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 248 | names a stale extension beside one whose stamp is not a version ($order) | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 259 | says nothing for an extension whose stamp is not a version, since its age is unknown | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 265 | never quotes an extension stamp that carries more than a version | `internal/relay/server_probe_test.go` :: `TestRelayExtensionNotice` |
| 271 | stops naming a stale browser once it disconnects | `internal/relay/server_probe_test.go` :: `TestRelayExtensionNotice` |
| 282 | keeps naming a connected stale browser after the current one's service worker reconnects | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 291 | says nothing when the relay is too old to report its extension's version | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 305 | gives up on the read when the open's deadline passes instead of waiting out its own timeout | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 315 | never calls a reported version older than an omp version it cannot rank, so restarts leave that relay alone | `internal/relay/protocol_test.go` :: `TestIsOmpVersionAndIsOlderOmpVersion` |
| 320 | still counts an unreported version as older than one it cannot rank | `internal/relay/protocol_test.go` :: `TestIsOmpVersionAndIsOlderOmpVersion` |

## `browser-relay-native-host.test.ts` (8 cases: 4 ported, 4 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 157 | relays rpc lines both ways, chunks oversized ones and reports the child's exit | skipped: side-panel chat (`open` branch + omp RPC v2 child spawning) is out of scope; refusal tested in `internal/relay/native_host_test.go` (`TestNativeHostOpenRefusedAndInvalidFirstMessage`) |
| 191 | stops the child when the extension disconnects | skipped: side-panel chat (`open` branch) is out of scope; refusal tested in `internal/relay/native_host_test.go` (`TestNativeHostOpenRefusedAndInvalidFirstMessage`) |
| 200 | refuses a missing directory without spawning | skipped: side-panel chat (`open` branch) is out of scope; refusal tested in `internal/relay/native_host_test.go` (`TestNativeHostOpenRefusedAndInvalidFirstMessage`) |
| 209 | expands ~ to the home directory | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
| 237 | chat child discovers its browser over the chat port, not the terminal relay | skipped: side-panel chat (`open` branch / per-chat relay server) is out of scope; refusal tested in `internal/relay/native_host_test.go` (`TestNativeHostOpenRefusedAndInvalidFirstMessage`) |
| 253 | terminal relay publishes its assigned port and retires it when Chrome disconnects | `internal/relay/native_host_test.go` :: `TestNativeHostPublishesAndRetiresEndpoint` |
| 267 | serves the relay for the first browser, forwards the next one's, and hands over when the first leaves | `internal/relay/native_host_test.go` :: `TestNativeHostLeaderFollowerAndHandover` |
| 295 | gives way only to a newer omp's restart, and then exits so Chrome starts the next host | `internal/relay/native_host_test.go` :: `TestNativeHostRestartHandshake` |

## `browser-relay-server.test.ts` (5 cases: 5 ported, 0 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 114 | advertises the requested Host authority so a remote Puppeteer client dials the relay | `internal/relay/server_probe_test.go` :: `TestRelayServerDiscoveryAndOriginChecks` |
| 123 | uses the loopback discovery URL when an HTTP/1.0 request has no Host header | `internal/relay/server_probe_test.go` :: `TestRelayServerDiscoveryAndOriginChecks` |
| 129 | uses the loopback discovery URL when Host is empty | `internal/relay/server_probe_test.go` :: `TestRelayServerDiscoveryAndOriginChecks` |
| 135 | uses the loopback discovery URL when Host would produce an unusable WebSocket authority | `internal/relay/server_probe_test.go` :: `TestRelayServerDiscoveryAndOriginChecks` |
| 144 | reports how many CDP clients are connected, both while waiting for the extension and once ready | `internal/relay/server_probe_test.go` :: `TestRelayServerDiscoveryAndOriginChecks` |

## `browser-relay-installs.test.ts` (9 cases: 8 ported, 1 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 51 | uses the same registry path across omp profiles | `internal/relay/install_test.go` :: `TestInstallRegistryAdoptionAndCanonicalAliases` |
| 63 | keeps an existing default install and records path aliases as one canonical custom install | `internal/relay/install_test.go` :: `TestInstallRegistryAdoptionAndCanonicalAliases` |
| 95 | syncs stale files, reports a deleted install without recreating it, and skips an already-synced startup | `internal/relay/install_test.go` :: `TestSyncOverTwoDirsMissingAndRecheck` |
| 160 | publishes every registered stale install before reloading a registered folder's extension, without downgrading newer files | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamInstalls_OutdatedExtensionReload` |
| 189 | rewrites nothing and stays connected for an extension whose ID is no registered folder's | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamInstalls_OutdatedExtensionReload` |
| 214 | reloads a registered extension running another build than its folder holds, without rewriting the folder | `internal/relay/bridge_upstream_extra_test.go` :: `TestUpstreamInstalls_OutdatedExtensionReload` |
| 262 | writes a native host manifest admitting every install's ID to every ${platform} browser location | `internal/relay/install_test.go` :: `TestWriteRelayNativeHostPerOS` |
| 291 | execs a source launcher with quoted arguments and discards Chrome's transport arguments | `internal/relay/install_test.go` :: `TestNativeHostLauncherQuotesAndDiscardsChromeArgs` |
| 310 | invokes the verified new executable to sync and does not fail the update on a nonzero sync exit | skipped: auto-sync hook inside `omp update` (`syncRelayAfterVerifiedUpdate`) is not ported because oh-my-pig has no self-updater |

## `browser-relay-chromium-profiles.test.ts` (1 cases: 1 ported, 0 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 32 | reports per profile whether the unpacked extension is loaded, switched on, and Developer mode | `internal/relay/install_test.go` :: `TestFindUnpackedInstalls` |

## `browser-relay-cli.test.ts` (2 cases: 2 ported, 0 skipped)

| Line | Upstream title | Go test / disposition |
|---:|---|---|
| 160 | stamps the installing omp's version into the extension manifest | `internal/relay/install_test.go` :: `TestWriteRelayExtensionAtomicAndIdempotent` |
| 170 | reports the omp that installed the running script when Chrome restarts onto a newer manifest | `internal/relay/self_update_probe_upstream_test.go` :: `TestUpstreamProbe_RemainingCases` |
