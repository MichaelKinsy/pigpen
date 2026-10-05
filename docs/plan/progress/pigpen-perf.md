# pigpen-perf: performance and allocations pass over Pigpen

Status: READY. Base: the tree of public PR #1 (`5f8ba23`). Every fix below is its own small signed-off commit,
tests first (a test or benchmark-backed allocation bound that failed before the change), so it can ride into the PR.

## What was measured, and how

- **Machine and binaries.** A 196-CPU linux/amd64 host (Intel Xeon 6746E), Go 1.27.1, `CGO_ENABLED=0`, pig 0.4.0 (`0.4.0+1.0.0`) as the
  baseline with no Piglet. The ten Piglets are built with `npm run build:piglet`. Every component extension is also built **alone**
  in its own one-extension Piglet (`node scripts/perf/build-singles.mjs`, same origin and tool scope the shipping Piglet gives it),
  next to a **control**: a Piglet whose only extension registers nothing (`seed-check`). Whatever the control costs is what PiG
  itself costs for hosting any extension; an extension's own cost is its delta over the control.
- **Method.** `node scripts/perf/measure.mjs startup|idle` (committed). `PIG_TEST_FAUX=1`, `--model test-faux/faux-1`, a fresh
  temporary HOME/PIG_HOME/agent dir for every run, `taskset` to 4 CPUs (the host has 196; without it the Go runtime starts a GC worker
  per CPU and RSS and thread counts mean nothing), binaries measured round-robin, one warm-up round dropped.
  Startup: **20 cold starts** per binary, median and p90 (`--mode print`: `pig -p "What is 20+22?"` to exit; `--mode tmux`: new tmux
  session until the editor footer is drawn, and (added in review) until text typed at that moment is shown, i.e. the prompt accepts input; `--mode trace`: `PIG_STARTUP_TRACE` marks, per-extension spawn/handshake spans).
  Init cost: `GODEBUG=inittrace=1` and the trace marks. Idle: all binaries running in tmux, 30 s settle, RSS/threads/fds/children,
  then CPU ticks and context switches over 60 s, then a SIGQUIT goroutine dump; RSS is the median of four rounds because Go's heap
  makes a single sample vary by about +-5 MiB. Profiles: `PIG_PROFILE=cpu,heap,allocs` on the real binaries (15 to 20 runs merged,
  `go tool pprof`) and `-cpuprofile -memprofile` on the benchmarks.
- **Not measured.** `pig-music` and the port-popular ports are not on this branch (they are not in this tree). The ahp and dirty-repo-guard
  Packages are in no Piglet; they are measured alone because every extension in `components/` has a row.

## Result in one paragraph

Pigpen's own code costs almost nothing at startup: a pprof of the first prompt with `focus=MichaelKinsy/pigpen` is at most 1 ms per
extension (checked for warden, websearch, context-info and ahp), each extension's handshake is 2 ms (websearch 4 ms), init before `main` adds about
2 ms per binary, and the interactive prompt is **drawn** +8 to +10 ms after plain pig's for any extension and +15 to +18 ms for all of batteries,
but it **accepts input** about +100 ms later than plain pig's with any extension (+160 ms with batteries; review measurement below):
the same PiG catalog publication as in print mode runs between drawing the editor and attaching the extensions. Over the no-op
control, Pigpen's extensions add 0 to 3 ms each (batteries: +62 ms for fourteen). Nothing runs at idle: **no socket, child process, inotify/file watcher, timer or
ticker goroutine** in any of the 28 binaries, idle CPU is 0.5 to 0.9 ms/s (plain pig: 0.6), wakeups about 65/s (plain pig: 62, the
TUI's). The expensive things are **PiG's**, not Pigpen's: in print mode every extension (even a no-op) adds 115 ms and 45 MiB of
allocation to the first prompt (the host JSON-encodes the whole model catalog for extensions), and every subscribed event handler
costs about 3.7 ms more in the first turn (the host pushes a full state snapshot per delivered event). Those are PiG's, to be raised with PiG. In Pigpen's own code the pass found and fixed **ten per-call or per-start costs** (regexps compiled per
call, a quadratic string join, a memo keyed by the whole tool input, a table re-parsed per file, a static document re-encoded per
request, log arguments built for a level that drops them), listed with before/after below.

## 1. Startup, idle and memory: every Piglet and every component extension

Print = `pig -p` to exit (includes the first turn). Interactive = tmux, until the prompt is **drawn** (it does not accept input yet
when an extension is present: see "Review re-measurement" below). "Last extension ready" is the
`PIG_STARTUP_TRACE` mark of the last handshake, in ms after pig's flag parsing (process start to flag parsing is another 15 to 19 ms
in every binary, plain pig included). Before / after are the same binaries rebuilt before and after the fixes below; the fixes are
per-call, so startup and idle do not move (differences are noise: p90 is within 2 to 4 ms).

### Piglets

| Piglet | print-mode first prompt, median ms (before / after) | print p90 | interactive (tmux) prompt drawn, median | interactive p90 | last extension ready, ms after pig starts | idle RSS MiB (median of 4) | goroutines | live heap MiB | heap allocated MiB | idle CPU ms/s | wakeups/s |
|---|---|---|---|---|---|---|---|---|---|---|---|
| plain pig 0.4.0 (no Piglet) | 45.5 / 44.9 | 47.0 / 46.8 | 66.9 / 67.2 | 69.4 / 70.9 | - | 53.5 (anon 21.0) | 22 | 4.5 | 11.6 | 0.60 | 62 |
| control: one no-op extension | 160.7 / 158.8 | 164.6 / 161.1 | 76.5 / 74.9 | 80.6 / 77.5 | 19 | 64.5 (anon 30.2) | 30 | 8.6 | 56.0 | 0.80 | 66 |
| a2a | 171.0 / 171.5 | 173.3 / 174.0 | 76.6 / 76.8 | 78.8 / 78.8 | 19 | 64.7 (anon 29.9) | 30 | 8.7 | 60.9 | 0.80 | 68 |
| acp | 160.0 / 159.7 | 162.3 / 160.9 | 75.7 / 74.9 | 78.0 / 77.9 | 18 | 68.2 (anon 34.0) | 30 | 9.6 | 58.4 | 0.60 | 65 |
| herdr | 159.1 / 159.1 | 161.4 / 162.0 | 75.1 / 75.0 | 76.9 / 76.8 | 19 | 71.4 (anon 37.3) | 30 | 10.1 | 55.5 | 0.60 | 67 |
| jev | 164.7 / 166.2 | 168.2 / 169.1 | 75.1 / 75.7 | 77.2 / 78.0 | 19 | 66.7 (anon 32.3) | 30 | 8.6 | 61.9 | 0.60 | 63 |
| pig-extension-porter | 161.6 / 162.5 | 163.3 / 165.7 | 77.1 / 78.6 | 81.1 / 83.1 | 21 | 68.3 (anon 33.7) | 30 | 9.1 | 59.2 | 0.60 | 65 |
| pig-games | 160.5 / 162.5 | 162.4 / 165.0 | 74.5 / 76.3 | 77.5 / 78.4 | 19 | 70.4 (anon 36.0) | 38 | 8.1 | 63.1 | 0.50 | 65 |
| pig-porter | 161.3 / 164.0 | 163.7 / 166.5 | 78.0 / 78.6 | 82.3 / 81.5 | 20 | 68.8 (anon 34.3) | 30 | 10.2 | 57.0 | 0.50 | 66 |
| pig-typesafe | 170.7 / 169.6 | 172.7 / 172.6 | 75.6 / 75.9 | 79.7 / 78.2 | 19 | 65.8 (anon 30.9) | 30 | 8.8 | 57.1 | 0.90 | 65 |
| pig-warden | 178.8 / 181.8 | 181.3 / 184.4 | 76.2 / 76.7 | 80.8 / 79.4 | 19 | 64.9 (anon 30.1) | 30 | 8.6 | 61.6 | 0.60 | 65 |
| pig-with-batteries | 278.3 / 285.2 | 283.7 / 291.4 | 82.2 / 84.5 | 85.3 / 87.1 | 24 | 64.8 (anon 27.1) | 110 | 8.9 | 144.7 | 0.60 | 64 |

### Component extensions, each alone in its own Piglet

| Extension | print-mode first prompt, median ms (before / after) | print p90 | interactive (tmux) prompt drawn, median | interactive p90 | last extension ready, ms after pig starts | idle RSS MiB (median of 4) | goroutines | live heap MiB | heap allocated MiB | idle CPU ms/s | wakeups/s |
|---|---|---|---|---|---|---|---|---|---|---|---|
| a2a | 167.7 / 172.8 | 171.1 / 175.9 | 75.3 / 77.1 | 79.5 / 80.1 | 18 | 63.0 (anon 27.9) | 30 | 7.2 | 60.9 | 0.60 | 65 |
| acp | 157.0 / 161.6 | 159.7 / 163.8 | 74.0 / 75.1 | 76.8 / 77.6 | 18 | 69.8 (anon 35.6) | 30 | 8.7 | 59.1 | 0.60 | 65 |
| ahp | 196.2 / 198.0 | 199.5 / 201.5 | 74.0 / 74.7 | 76.9 / 77.9 | 18 | 67.7 (anon 32.9) | 30 | 8.3 | 70.6 | 0.60 | 66 |
| angrypigs | 159.8 / 159.9 | 162.1 / 162.0 | 75.2 / 74.8 | 77.6 / 76.6 | 19 | 66.3 (anon 31.6) | 30 | 7.1 | 58.7 | 0.80 | 65 |
| context-info | 177.9 / 180.8 | 180.7 / 183.4 | 73.8 / 76.5 | 84.1 / 77.8 | 19 | 66.8 (anon 32.3) | 30 | 9.6 | 58.2 | 0.60 | 65 |
| dirty-repo-guard | 159.4 / 159.6 | 161.7 / 162.2 | 75.8 / 75.7 | 79.3 / 77.8 | 19 | 70.9 (anon 37.0) | 30 | 9.6 | 54.5 | 0.80 | 65 |
| extension-equivalence | 159.4 / 161.3 | 161.7 / 162.5 | 75.7 / 75.1 | 78.6 / 77.6 | 19 | 69.6 (anon 35.4) | 30 | 6.8 | 63.4 | 0.60 | 63 |
| herdr | 157.2 / 160.0 | 159.9 / 162.7 | 74.3 / 74.8 | 76.8 / 79.4 | 18 | 70.3 (anon 36.7) | 30 | 7.9 | 55.2 | 0.60 | 62 |
| jev | 166.7 / 165.6 | 168.5 / 167.8 | 76.2 / 75.3 | 78.4 / 78.6 | 19 | 66.6 (anon 31.8) | 30 | 7.3 | 63.4 | 0.80 | 67 |
| pig-doctor | 159.4 / 159.2 | 161.7 / 161.3 | 75.9 / 75.8 | 81.6 / 77.0 | 19 | 66.9 (anon 33.0) | 30 | 9.7 | 58.1 | 0.60 | 65 |
| pigrunner | 156.8 / 161.5 | 161.0 / 163.1 | 74.1 / 76.1 | 77.2 / 80.1 | 19 | 67.7 (anon 33.5) | 29 | 7.9 | 62.0 | 0.60 | 63 |
| pig-snake | 159.2 / 159.2 | 161.6 / 162.7 | 75.1 / 74.9 | 77.8 / 77.6 | 18 | 68.7 (anon 34.2) | 30 | 10.1 | 59.5 | 0.60 | 64 |
| pi-typesafe | 166.5 / 171.3 | 168.5 / 173.9 | 74.3 / 75.9 | 77.1 / 79.3 | 18 | 69.9 (anon 35.2) | 30 | 8.4 | 57.2 | 0.50 | 63 |
| session-ingest | 158.0 / 161.3 | 160.3 / 164.5 | 74.7 / 76.8 | 77.7 / 78.5 | 19 | 68.2 (anon 33.9) | 30 | 9.7 | 56.7 | 0.80 | 64 |
| warden | 178.9 / 179.6 | 181.9 / 181.9 | 75.6 / 76.8 | 79.6 / 79.4 | 18 | 67.5 (anon 32.9) | 30 | 9.7 | 70.8 | 0.60 | 63 |
| websearch | 175.2 / 178.7 | 179.5 / 181.3 | 77.4 / 80.2 | 79.8 / 82.7 | 21 | 70.2 (anon 35.3) | 30 | 8.9 | 61.9 | 0.50 | 64 |


Print-mode delta, after the fixes (median of 20):

| Piglet / extension | Δ print vs plain | Δ print vs no-op ext (after) |
|---|---|---|
| plain | +0.0 ms | -113.9 ms |
| a2a | +127.9 ms | +14.0 ms |
| acp | +116.7 ms | +2.8 ms |
| ahp | +153.1 ms | +39.2 ms |
| angrypigs | +115.0 ms | +1.1 ms |
| context-info | +136.0 ms | +22.0 ms |
| dirty-repo-guard | +114.7 ms | +0.8 ms |
| extension-equivalence | +116.5 ms | +2.6 ms |
| herdr | +115.2 ms | +1.3 ms |
| jev | +120.7 ms | +6.8 ms |
| pig-doctor | +114.3 ms | +0.4 ms |
| pigrunner | +116.6 ms | +2.7 ms |
| pig-snake | +114.4 ms | +0.5 ms |
| pi-typesafe | +126.4 ms | +12.5 ms |
| seed-check | +113.9 ms | +0.0 ms |
| session-ingest | +116.4 ms | +2.5 ms |
| warden | +134.7 ms | +20.8 ms |
| websearch | +133.9 ms | +20.0 ms |
| a2a (Piglet) | +126.6 ms | +12.7 ms |
| acp (Piglet) | +114.8 ms | +0.9 ms |
| herdr (Piglet) | +114.2 ms | +0.3 ms |
| jev (Piglet) | +121.3 ms | +7.4 ms |
| pig-extension-porter | +117.6 ms | +3.7 ms |
| pig-games | +117.6 ms | +3.7 ms |
| pig-porter | +119.2 ms | +5.3 ms |
| pig-typesafe | +124.8 ms | +10.8 ms |
| pig-warden | +137.0 ms | +23.1 ms |
| pig-with-batteries | +240.3 ms | +126.4 ms |

### Budgets

| Budget | Result |
|---|---|
| Startup delta under 10 ms per extension | **Interactive: not met, and not Pigpen's.** The prompt is drawn +8 to +10 ms after plain pig's, but accepts input about +100 ms later for the no-op control and every single extension, +160 ms for batteries (PiG's catalog publication, below). Over the control, warden, jev and pig-typesafe add 0 to 3 ms, batteries' fourteen extensions +62 ms (about 4.4 ms each, PiG's per-extension hosting). Handshake 2 ms each. **Print mode: not met, and not Pigpen's**: the first prompt is +115 ms for the no-op control, the same for every extension (PiG hosts catalog publication, below). Over the control, ahp +35 (no Piglet selects it), warden +18, context-info +17, websearch +14.5 are per-event host costs (3.7 ms per subscribed handler, experiment below); a2a, jev, pi-typesafe +6 to +7; the rest is zero within noise. |
| Idle RSS delta under 5 MiB per extension | **Not resolvable per extension; total is small.** One no-op extension costs +11 MiB RSS over plain pig (anon +9 MiB: the host's catalog and per-extension connection), and adding the other thirteen extensions of batteries costs no measurable more (batteries 64.8 MiB, control 64.5 MiB). A single run varies by +-5 MiB (`rss` columns across four rounds), so per-extension deltas of 1 to 5 MiB are noise. Live heap after one prompt: plain 4.5 MiB, control 8.6 MiB, batteries 8.9 MiB. |
| Idle CPU about 0 | **Met**: 0.5 to 0.9 ms/s for every binary (plain 0.6; 10 ms tick resolution over 60 s). |
| No network or child process at startup unless enabled | **Met**: no socket fd, no child process, no inotify fd, no timer or ticker goroutine in any binary (`fds`/`children`/`timerGoroutines` in `measure.mjs idle`; goroutine dumps checked). Games, a2a and ahp listeners, Jev, warden and websearch are all inert until enabled. |

### Package init (`GODEBUG=inittrace=1 BINARY --version`, median of 7, all packages / Pigpen packages)

Plain pig: 11.7 ms of package init, of which `santhosh-tekuri/jsonschema/v6` is 4.5 ms (PiG's). Every extension alone: 10.3 to 11.5 ms
total (no extra over plain pig's own) and **under 0.2 ms in Pigpen packages**, except:

| binary | Pigpen package init before | after |
|---|---|---|
| warden (and `pig-warden`) | 3.06 ms, 984 KB, 6,974 allocs (about 100 regexps compiled at start) | **1.35 ms**, 145 KB, 308 allocs (regexps compile on first use) |
| websearch | 0.93 ms | unchanged (tables, regexps; under budget) |
| extension-equivalence (`pig-extension-porter`, `pig-porter`) | 1.0 ms | unchanged |
| pig-with-batteries (all fourteen together) | 1.1 ms | unchanged |

Time from process start to "extension ready" is the trace column above: the last handshake completes 18 to 21 ms after pig
starts parsing flags (24 ms for batteries), which includes pig's own startup (services, SDK sync) before any extension is spawned.

## 2. Where the time goes (profiles)

1. **A no-op extension costs +115 ms in print mode (PiG).** CPU profile of 15 runs: 53% of samples under
   `UIBridge.PublishModelCatalog` -> `modelCatalogUpdate` -> `WireModelOperations` -> `modelRegistryState` / `SetModelCatalog`
   (`encoding/json/v2.Marshal` of the whole catalog, `appendCompact`, `stateInString`, then the SDK decoding it again in
   `conn.readLoop`), synchronously on the print path before the first request; the same on 0.4.1. About 45 MiB allocated and 4 MiB
   live heap for it. It is on the interactive path too (corrected in review): pig draws the editor first, then
   `InteractiveMode.attachSubprocess` -> `WireModelOperations` -> `PublishModelCatalog` runs before `session_start`, so the prompt
   accepts input about 100 ms after it is drawn (interactive `PIG_STARTUP_TRACE`: `tui-layout-built` 30 ms, `pre-emit-session-start`
   about 133 ms for the control; 38% of the interactive CPU profile). In print mode `PIG_STARTUP_TRACE` stops at `stdin-read-done`
   (about 27 ms) so its marks do not show it.
2. **Each subscribed event handler costs about 2 to 4 ms in the first turn (PiG).** Experiment: the control extension with 1, 4 and 8
   no-op handlers: 165.8, 174.7 and 190.3 ms against 160.1 (`exp-handlers`, not committed). Review: the control with no-op handlers
   for warden's eight events costs +16.8 ms (2.1 ms each), as much as warden itself (+18.7 ms), so the cost depends on which events
   are subscribed (how often each is delivered in the turn). Profile of ahp/warden/context-info/websearch against the
   control: the delta is `makeEventHandler` -> `Host.pushStateTo` -> `UIBridge.Snapshot` -> JSON marshal of the session state, once per
   delivered event, plus `sdk/json.checkValid` on the extension side. `pprof -focus=MichaelKinsy/pigpen` over the same runs: the
   extensions' own code is 0.02 s over 20 runs. Handlers per extension: ahp (12 events), warden 8, context-info 7, websearch 5,
   herdr 4 (only inside a herdr pane; outside one it registers nothing), jev 2, dirty-repo-guard 2, a2a, pi-typesafe,
   session-ingest, pig-doctor and the games 0.
3. **About 8 goroutines per extension (PiG host connection and SDK reader), 0 timers.** pig 22, any one extension 30,
   pig-games 38, pig-with-batteries 110 (SIGQUIT dumps: `select`/`chan receive` in `extension/host/subprocess` and `extensions/sdk`).
4. **Binary size** is already recorded in RELEASE-NOTES and matches: 63.7 to 65.3 MB, batteries 68.2 MB, against 63.3 MB for pig.

## 3. Hot paths: benchmarks, profiles, fixes

Benchmarks are committed next to the code (`bench_test.go` in each module; `go test -run xxx -bench . -benchmem`). Before is the
commit before the fix, on the same machine (`-cpu` default unless the profile line says 4).

| Component | Operation | Before | After | Profile finding |
|---|---|---|---|---|
| warden | hold check `MatchPatterns`, 3 user rules | 112 us, 16.8 KB, 271 allocs | 97 us, 2.7 KB, 145 allocs | `regexp.Compile` for every user rule on every tool call. Fixed: compiled once per (pattern, case flag). Built-in rules alone: 89 us, 2.7 KB, 145 allocs; 80% of CPU is the 24 built-in regexps' backtracker on the command, not worth rewriting for a per-tool-call cost. |
| warden | `RecordUI`, 3 changed paths, default globs | 539 us, 363 KB, 2835 allocs (368 us at `-cpu 4`) | 97 us, 7 KB, 174 allocs | `globToRegexp` compiled every glob (after brace expansion) per path. Fixed: cached by pattern. |
| jev | gate memo key, 50 KB write | 149 us, 175 KB (81 us at `-cpu 4`) | 89 us, 556 B (88 us at `-cpu 4`) | The key held the whole serialised tool input: marshalled per call and kept in the memo for `cacheSeconds`. Fixed: SHA-256 of the same fields streamed from the encoder; retained key 64 bytes. |
| jev | state document: small call / 50 KB write / does not fit | 4.3 us / 93 us / 2.2 ms (1 MB, 8183 allocs) | unchanged | Worst case is a 200-key object that never fits: `fitState` rebuilds six times. Rare; not changed. |
| typesafe client | request payload, 3 questions | 21 us, 14.2 KB, 206 allocs | 14 us, 7.6 KB, 88 allocs | `bytes.ReplaceAll` (copies even with no match) twice per nested `marshalPlain`, and a fresh encoder per object key. Fixed: return clean bodies as is; quote plain keys directly. |
| typesafe client | `SystemOne` round trip to a local server | 181 us, 36.2 KB, 469 allocs | 148 us, 23.5 KB, 252 allocs | The network dominates. Besides the payload fix above, the debug-log arguments (a redacted headers map, the body, and a JSON parse of the whole response) were built on every call at every log level and then dropped by the level filter. Fixed: built only at debug. |
| websearch | HTML -> Markdown, 40-section page | 5.6 ms, 2.65 MB, 30.4k allocs | 1.3 ms, 0.56 MB, 6.0k allocs | `mdJoin` copied the whole output for every sibling (quadratic; 63% of bytes), `mdEscape` ran 13 regexp replacements per text node (53% of CPU), two regexps compiled per blockquote and per code block. Fixed: tail-trimming buffer, one-pass escape, package-level regexps. Equivalence with the old definitions checked on 20,000 random strings. |
| websearch | parse + readable article | 8.6 ms, 3.0 MB | 4.2 ms, 0.87 MB | the above |
| websearch | SSRF guard per URL | 2.2 us, 17 allocs | unchanged | DNS is the real cost, stubbed here. |
| a2a | Agent Card GET | 14.6 us, 11.4 KB, 67 allocs | 8.4 us, 7.8 KB, 34 allocs | card built and a new handler (which JSON-encodes it) per request. Fixed: cached per advertised URL. |
| a2a | `SendMessage` through the handler | 1.3 ms, 474 KB, 8528 allocs | unchanged | almost all inside a2a-go: the task store deep-copies tasks with `encoding/gob` (`DeepCopy`, 16% of bytes) and rebuilds gob decoders per copy. Library; not changed. `Authenticate` 313 ns, 1 alloc. |
| session-ingest | `read_session` parse, 3 MB / 6000 messages | 63.6 ms, 29.5 MB, 319k allocs | 63.6 ms, 23.4 MB, 307k allocs | each line copied to a string and back; fixed (decode from the scanner buffer). The decode into `map[string]any` dominates. Query 36.6 ms, TOC 0.37 ms. No parsed-session cache: it would hold tens of MB while idle, against the idle-RSS budget. |
| extension-equivalence | `ScanGaps`, one small extension | 1.73 ms, 1.23 MB, 12,088 allocs | 0.44 ms, 39 KB, 350 allocs | `parseSurface` rebuilt the table's rows and a regexp per trigger on every call (twice per file). Fixed: cached by table text. |
| warden | package init (every pig that selects it, on or off) | 3.06 ms, 984 KB, 6,974 allocs | 1.35 ms, 145 KB, 308 allocs | about 100 package-level `regexp.MustCompile` calls ran in `init`. Fixed: `lazyRegexp`, compiled on first use. |
| herdr | reporter start+settle / repeated state / report args | 53 ns, 0 allocs / 26 ns / 650 ns, 4 allocs | - | nothing to fix; the herdr process exec per state change is off the handler path. |
| games | Angry Pigs frame 200x60 / step | 195 us, 12 allocs / 524 ns, 0 | - | CPU is the pixel encoder (`encodeRow`, 69% in PiG Runner); buffers are reused. |
| games | PiG Runner frame / Pig Snake frame 100x30 | 152 us, 4 allocs / 95 us, 4 allocs | - | a frame is drawn only when the picture changed: `Update` and `advance` return false when paused, over or waiting. |
| dirty-repo-guard | decide, clean / 5000 changed files | 9.7 ns / 107 us, 1 alloc | - | the `git status` process dominates. |
| pig-doctor | `Check` (100 or 2000 files per cache dir) | 82 us / 81 us | - | independent of cache size: it does not walk caches. Runs only on `/doctor`. |
| acp (pig-acp) | bash output update, 4 KiB / 256 KiB accumulated | 0.42 us / 8.8 us, 3 allocs | - | pi resends the whole output each update; the diff is a prefix compare. Prompt conversion 1.0 us, `session/update` notification 2.6 us, 12 allocs. |
| ahp | text delta / tool call through the turn mapper | 172 ns, 3 allocs / 6 us, 88 allocs | - | listener off unless configured. |

## 4. What was not fixed, and why

- **PiG hosting costs** (+115 ms and 45 MiB per start in print mode, and about +100 ms before the interactive prompt accepts input,
  for any extension; 2 to 4 ms per subscribed handler per turn;
  8 goroutines and about 10 MiB per extension): in PiG's `coding/extension/host/subprocess`, identical on 0.4.0 and 0.4.1. Pigpen
  cannot change them; to be raised with PiG.
- **Lazy event subscription** for the off-by-default extensions (warden 8 handlers, jev 2, context-info's four footer-only
  handlers, websearch, ahp): the SDK's `OnEvent` can subscribe after connect, so warden could subscribe to its seven non-session
  events only on `/warden enable` (saving about 18 ms on the first prompt while it is off). Not done: every fake host in the
  repository records handlers at registration and none implements `event.subscribe`, the live equivalence scenarios assume the
  handlers exist from session start, and the saving is on a path the host should make cheap for everyone. It is the first thing to do
  if PiG does not fix the per-event push.
- **a2a task store** (gob deep copies, 8.5k allocs per `SendMessage`): inside a2a-go.
- **session-ingest parsed-session cache**: would trade idle memory for repeat-call speed; not worth it for an on-demand tool.
- **pig-acp JSON-RPC write queue** (`jsonrpc.Conn.queue`) is an unbounded slice: a client that stops reading stdout lets it grow.
  ACP has no flow control and dropping messages is a protocol decision, not a performance fix.
- **Game tickers**: while a game overlay is open, its goroutine wakes at the tick rate (30 to 60 Hz) even when paused or over
  (no redraw, no work). Only while the user has a game open; left alone.
- **warden built-in rule regexps** (89 us per bash call) and **jev `fitState`** worst case (2 ms): per tool call, not a regression;
  left as they are.
- **websearch and extension-equivalence package init** (about 1 ms each, regexps and tables compiled at start): under the 10 ms budget; the same lazy approach as warden would apply if wanted.
- **Other platforms**: only linux/amd64 was measured.

## 5. Reproduce

```sh
export PIG_BIN=/path/to/pig-0.4.0 PIG_SOURCE_ROOT=/path/to/PiG-checkout-at-v0.4.0 CGO_ENABLED=0
export PIG_HOME=$(mktemp -d) PIG_CODING_AGENT_DIR=$(mktemp -d)       # pig piglet build keeps receipts and a copy of each Binary there
npm run build:piglet -- pig-warden --out /tmp/perf/pig-warden        # each Piglet
node scripts/perf/build-singles.mjs                                  # dist/perf/bin/<extension>, plus the seed-check control (scratch PiG home)
node scripts/perf/measure.mjs startup --n 20 --cpus 100-103 plain=$PIG_BIN warden=dist/perf/bin/warden ...
node scripts/perf/measure.mjs startup --mode tmux ...                # interactive; --mode trace for PIG_STARTUP_TRACE marks
node scripts/perf/measure.mjs idle --settle 30 --window 60 --out /tmp/perf/dumps plain=$PIG_BIN ...
PIG_PROFILE=cpu,heap,allocs PIG_PROFILE_DIR=/tmp/p taskset -c 100-103 BINARY --model test-faux/faux-1 -p "What is 20+22?"   # with PIG_TEST_FAUX=1
```

`--mode tmux` polls the screen by running `tmux` every 2 ms: `tmux` on PATH must be the program itself, not a version-manager shim
(a 20 ms shim per call added about 25 ms to every interactive number in review).

Benchmarks: from a module directory with the repository's `go.work` (`scripts/go-modules.mjs`), `go test -run xxx -bench . -benchmem`.
Times in section 3 are at the default `-cpu` (GOMAXPROCS 196 on the measurement host), where garbage collection is costlier; at `-cpu 4`
the jev key's time does not improve (81 -> 88 us; the bytes do), and the other rows keep their direction.

## Verification

`npm test` 119/119, `npm run check` (index, quality gates, Go-only, ports list) pass, `npm run test:go-ports` (vet and test of all
modules, 50 packages) pass, all ten Piglets and all seventeen single-extension Piglets rebuild with the fixes.

Review (rev-pigpen-perf) found and fixed: two compiled test binaries committed with the benchmarks (removed; the quality gate now
refuses executables), `test:go-ports -- -race` failing on a websearch allocation bound, `test:port` stopping at jev (a mutation
still looked for the old memo key), build-singles writing `~/.pig`, and the tmux harness removing a run's home while pig was still
writing it (homes left behind in TMPDIR, or the run aborted). After the fixes: `npm test` 122/122, `npm run check`,
`test:go-ports -- -race -count=3` on the touched modules, and `test:port` (32 pass; the 13 live re-runs need PI_BIN or PIG_UPSTREAM) pass.

## Review re-measurement (rev-pigpen-perf)

Same host, pig 0.4.0, `--cpus 100-103`, 20 runs after a warm-up, before = `5f8ba23`, after = this branch; binaries built by the
reviewer. Print and "drawn" agree with the tables above within noise. "Accepts input" is new: the time until text typed when the
footer appears is shown in the editor.

| binary | print median (before / after) | print p90 (after) | tmux drawn (before / after) | tmux accepts input (before / after) | p90 (after) |
|---|---|---|---|---|---|
| plain pig 0.4.0 | 44.8 | 46.2 | 67.2 | 72.2 | 75.3 |
| control (no-op extension) | 156.5 / 156.7 | 159.8 | 72.6 / 74.2 | 170.5 / 171.5 | 174.7 |
| warden alone | 177.5 / 175.5 | 177.9 | 75.4 / 74.1 | 173.1 / 172.3 | 175.8 |
| pig-warden | 178.3 / 178.4 | 180.1 | 78.3 / 75.9 | 176.7 / 173.1 | 176.4 |
| pig-typesafe | 167.3 / 169.3 | 171.0 | 75.9 / 75.4 | 174.1 / 174.8 | 177.8 |
| jev | 164.5 / 164.5 | 167.1 | 74.1 / 76.7 | 172.9 / 174.4 | 178.7 |
| pig-with-batteries | 277.4 / 278.3 | 285.2 | 82.2 / 85.0 | 237.8 / 233.0 | 249.9 |

Package init (median of 9, Pigpen packages): warden 2.85 -> 1.15 ms (1,057 KB, 7,220 allocs -> 167 KB, 555 allocs, typesafe libraries
included), pig-warden 2.85 -> 1.25 ms, batteries 1.10 -> 1.12 ms (it has no warden), pig-typesafe 0.10, jev 0.05; all packages
10.2 ms for plain pig. Idle (30 s settle, 60 s window): no socket, child process, inotify or timer goroutine in plain, control, warden,
pig-warden, pig-typesafe, jev or batteries; idle CPU 0.3 to 0.7 ms/s; 61 to 62 wakeups/s. `strace -f` of batteries from start through
20 s idle (and of a whole print-mode turn) under `PIG_TEST_FAUX=1 PIG_OFFLINE=1`: no `socket`, `connect`, `bind`, `inotify_init`,
`timerfd_create` or child process, only threads (without `PIG_OFFLINE`, PiG's own update and catalog checks connect, in plain pig
too; no Pigpen code reads `PIG_OFFLINE`). Idle RSS varied by +-10 MiB between rounds (batteries 72 to 89 MiB, control 69 to 73) with
no difference between before and after beyond that.
