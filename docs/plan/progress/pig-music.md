# pig-music: a full-screen music player for PiG (lane `pig-music`)

Branch `pig-music`, from `porter-verify`. The owner's brief is written against PiG 0.2.0;
this note records what the PiG 0.4.0 source and the Orpheus source actually say. Where they differ from the brief, the
source wins and the difference is listed.

## Milestone 1: findings

### What was read and run

- `pig --version` prints `0.4.0+1.0.0`. `pig version` prints `pig: 0.4.0`, `upstream pi: 1.0.0`, `go: go1.27.1`,
  `platform: linux/amd64`, `build: 76022638c15e2f68f1cee9c6b8984eee1baba620` (the `v0.4.0` tag; 0.4.1 is not used).
- PiG source: the `v0.4.0` tag in a scratch worktree outside this repository. Read: `docs/extension-authoring.md`,
  `docs/extension-api-parity.md`, `docs/extension-runtime-cells.md`, `docs/parity/DIVERGENCES.md` (the brief's
  `DIVERGENCES.md` moved there), `docs/site/docs/extensions.md`, `docs/site/docs/settings.md`, `docs/site/docs/tui.md`,
  `extensions/sdk/` (`context.go`, `extension.go`, `command_options.go`), the host side of `ui.custom`
  (`coding/extension/host/subprocess/ui_bridge.go`, `coding/extension/remote_overlay.go`,
  `internal/codingagent/ext_ui_context.go`, `internal/codingagent/custom_overlay.go`, `tui/overlay_compositor.go`,
  `tui/overlay_spec.go`) and the examples and conformance tests it points at.
- Orpheus: `github.com/Cabritto-Corps/orpheus` at `f3c0966`, cloned to a scratch directory outside this repository (its own git
  history, GPL-3.0 `LICENSE` kept). Nothing from it is in Pigpen.
- Toolchain: Go 1.27.1 present, `ffmpeg` present (`~/.local/bin/ffmpeg`). `mpv` and `yt-dlp` are not installed (see the lane's questions file).

### Answers, from the source

**Command and shortcut registration (Go SDK `extensions/sdk` v0.4.0).**

```go
ext := sdk.New("pig-music")
ext.Command("music", "description", func(ctx sdk.Context, args string) error { ... })    // args is the text after "/music "
ext.RegisterCommand("music", sdk.CommandOptions{Description: "...", GetArgumentCompletions: ..., Handler: ...})
ext.Shortcut("ctrl+shift+m", "description", func(ctx sdk.Context) error { ... })
```

The parity table lists `registerCommand` and `registerShortcut` as complete (the brief's "planned" for shortcuts is out
of date). Commands were run for real (below). **A shortcut was not run**; it is milestone 6 and the host rejects
shortcuts that collide with built-in keys (`TestShortcutsReservedBuiltinIsRejected`).

**Opening a component.** `ctx.Custom(component sdk.RemoteComponent, options any) (any, error)`; options is
`sdk.RemoteOverlayOptions`. The call blocks the command handler until the component completes.

```go
type RemoteComponent interface {
    Render(width int) []string
    HandleInput(data string) (RemoteComponentResult, error) // {Done bool; Value any}
}
type RemoteComponentInvalidator interface{ SetInvalidate(func()) } // optional; nil detaches
type RemoteComponentDisposer interface{ Dispose() }                // optional; runs once
```

The component is constructed and rendered inside the extension process. The host receives only line snapshots
(`ui.custom.render`, tagged with a terminal width and a sequence number), sends back ordered input chunks
(`ui.custom.input`) and receives a close with a result. Print and JSON mode return `nil, nil` without opening anything
(`HasUI()` is false); RPC mode has dialogs but answers `ui.custom` with `no_ui`, so guard on `ctx.Mode() == "tui"`.

**Overlay, and whether it can fill the terminal. It can.** The brief's stop condition ("the Go SDK cannot open a
full-screen component") does not apply. `RemoteOverlayOptions.Overlay: true` with `OverlayOptions` (the serializable
subset of pi-tui's `OverlayOptions`: `Width`, `MinWidth`, `MaxHeight`, `Anchor`, `OffsetX/Y`, `Row`, `Col`, `Margin`,
`NonCapturing`) mounts the component without a frame at upstream's `showOverlay` geometry. Width 100%, MaxHeight 100%,
anchor `top-left` and margin 0 covers the whole terminal; the component then renders at exactly the terminal width.
The legacy fields `Title`, `WidthFraction` and `HeightFraction` make the host draw a titled box one cell inside the
edge and render the component at width-2; pig-snake uses those. The overlay's height is the number of lines the
component returns (capped by `MaxHeight`), so a full-screen layout returns exactly one line per terminal row.

Verified live (both TUI modes, below): the overlay covered the whole 100x30 pane, over the transcript, editor and
footer, with no host frame.

**Terminal size.** Width reaches the component as `Render`'s argument and as `ctx.Width()`. Height is only
`ctx.Height()` (set from the ready payload and `height_change` notifications; 0 until reported). The SDK
re-renders overlays on `width_change` but **not on `height_change`**, so a height-only resize does not redraw a
component by itself. Pig-music's hello polls `ctx.Height()` every 250 ms and requests a render when it changes (the
host-to-SDK `height_change` also exists; nothing in the SDK exposes a callback for it). This is a limit to remember
for the player, not a blocker.

**Input.** Raw terminal chunks as JavaScript UTF-16 strings, one chunk per key press, not parsed key names. The same
key is `"\x1b[A"`, `"\x1bOA"`, a CSI-u sequence (`"\x1b[97;5u"`) or modifyOtherKeys (`"\x1b[27;5;97~"`) depending on the
terminal and on whether the Kitty keyboard protocol is on. Observed in tmux: `alt+x` arrives as `"\x1b[120;3u"` and
`shift+tab` as `"\x1b[9;2u"`, so the Kitty protocol was active. The SDK has no key helper, so the extension decodes
them itself: `components/pig-music/extensions/pig-music/internal/keys` (table-driven, tested against every Orpheus
default key in all four encodings). In fullscreen mode the host runs its own viewport keys (PageUp, PageDown, Home, End,
the wheel, ctrl+shift+f) before the component (`consumeModalHostInput`); in the run below PageUp and Home did arrive at
the component, so which of them the host takes depends on the viewport state. The player should not depend on them.
Kitty key releases are dropped by the host before they reach a remote component (it cannot ask for them, `tui.ShouldDeliverKey`); the decoder still names them.

**Redraw from a timer or goroutine.** Implement `SetInvalidate(func())` on the component. The SDK installs a
non-blocking render callback while the overlay is active and passes nil before `Dispose`. Calling it from any goroutine
is safe; requests are coalesced and the timer-driven frame rate is limited to 16 ms; identical frames are not sent.
Verified: the hello screen redraws once a second with no input.

**Closing, and learning that it closed.** Inside: `HandleInput` returns `RemoteComponentResult{Done: true, Value: v}`.
The caller learns of it because `ctx.Custom` returns `v`; the extension can also be told by the host (cancel, reload,
shutdown), which ends `ctx.Custom` and runs `Dispose` once. For the player this means "hide" is `Done: true` and the
component value (queue, track, position) must live outside the component's `Dispose`; the hello screen demonstrates a
second `/music` showing the earlier state.

**Lifecycle on `/reload` and quit.** `session_shutdown` carries `reason` of `quit`, `reload`, `new`, `resume` or `fork`
and, for the last three, `targetSessionFile` (`SessionShutdownEvent`; Go: `ext.OnSessionShutdown`). The SDK's own
`shutdown` message (`quit`, `reload`, `disable`) is not exposed to extension code: `Run` simply returns after the
handlers stop, so **everything held in extension memory is gone after `/reload`** (verified: the hello screen's key
log is empty after `/reload`). The parity doc says no test asserts the `reload` reason end to end. Consequence for the
player: mpv must outlive the extension process (own session via `Setsid`) and the queue must live in mpv. Whether the
host signals the extension's process group on shutdown was not checked.

### What the brief got wrong or left open

| Brief | Source (PiG 0.4.0) |
| --- | --- |
| PiG 0.2.0, SDK `v0.2.0` | 0.4.0 (Pi 1.0.0). The SDK is tagged `extensions/sdk/v0.4.0`, resolvable through the Go proxy; pinned in `go.mod`. |
| Overlay is "partial", Go may be behind Node | Go layout options are complete for sizing and positioning. Still open per the parity doc: visibility callbacks, non-capturing input routing, concurrent floating overlays (a second `ctx.Custom` waits behind the first). None is needed by a single full-screen player. |
| `registerShortcut` "planned" | Complete in the parity table; unverified here. |
| `exec` "planned" | Listed complete; not used. The extension spawns mpv and yt-dlp itself. |
| `DIVERGENCES.md` at the root | `docs/parity/DIVERGENCES.md`. |
| `pig extension init` scaffolds | Not used; the layout copies an existing Pigpen Go extension (pig-snake). |
| `pig install <path> --validate-only --json` | Passes for the extension directory (`components/pig-music/extensions/pig-music`). On a Package root, 0.4.0 answers "declares pi.extensions directories with no extension entry file" for every Package here (pig-snake too); `pig package validate components/pig-music` passes. Pigpen's docs already say not to show `--validate-only` on a Package root. |
| Fullscreen not mentioned | Fullscreen is the default TUI mode in 0.4.0. The overlay works the same in `--tui-mode regular` (both run in the integration test). Widgets and footers that survive a fullscreen resize (#122) are not needed by the overlay; the hidden-player footer status in milestone 6 uses `ctx.SetStatus(key, text)`, which the parity table lists as complete. `registerToolRenderer` is irrelevant to the player. |

### Orpheus

- `go.mod`: module `orpheus`, Go 1.26, **Bubble Tea v2** (`charm.land/bubbletea/v2 v2.0.10`), `bubbles/v2`,
  `lipgloss/v2`, `ultraviolet`. In v2 `View()` returns a `tea.View` (`Content` string, `AltScreen`), not a string, and key
  messages are `tea.KeyPressMsg`.
- Layout: `cmd/orpheus/main.go` (402 lines) and `internal/{auth,cache,config,librespot,loader,playbackdomain,spotify,tui}`.
  About 16.5k non-test Go lines. All UI is in `internal/tui` (an `internal` path, so another module cannot import it).
- Root model: the unexported `model` in `internal/tui/app.go`, built by `newModel(ctx, catalog spotify.PlaylistCatalog,
  cfg config.Config, tuiCmdCh chan librespot.TUICommand, contextTracksCh, ldr)` and started by `tui.Run`/`tui.Start`
  with `tea.NewProgram(m)`.
- Backend seam: the model reaches Spotify through (1) `spotify.PlaylistCatalog`, a five-method interface (user
  playlists, saved albums, playlist items, album tracks, context image URL), (2) a command channel
  `chan librespot.TUICommand` (play context, play from track, pause, resume, skip, seek, volume, shuffle, repeat, queue
  remove/reorder/jump, get context tracks) and (3) a one-way `*librespot.PlaybackStateUpdate` channel. That seam is
  narrow and already channel-shaped.
- Coupling: the UI package names Spotify domain structs about 380 times (`spotify.PlaybackStatus` 129,
  `PlaylistSummary` 73, `QueueItem` 58, `PlaylistPage` 46; `librespot.TUICommand` 50; `NormalizeSpotifyId` 25;
  `ContextKindPlaylist/Album` 38). They are plain data, but the UI is written in terms of them, so a YouTube backend either
  fills the same structs (URIs become video and playlist IDs) or the structs are renamed in a large diff.
- Things that cannot run inside a line-snapshot host as they are: `terminal_bg.go` writes OSC 11 straight to
  `os.Stdout`; cover art uses the Kitty graphics protocol and `tea.Raw` (`kitty_overlay.go`, `view_chrome.go`);
  `tea.Quit` on `q`/ctrl+c needs to become "hide"; `View()` forces `AltScreen`.
- Verdict against the brief's stop conditions: the Go SDK **can** open a full-screen component; the Orpheus UI is **not
  too entangled** to separate in principle, but the diff is not small (the data-type coupling above, an `internal`
  package, the three terminal-owning pieces). Whether to fork, and under what licence boundary, is the owner's decision
  and is asked in the lane's questions file. Nothing from Orpheus is imported into Pigpen.

## Milestone 2: hello overlay

`components/pig-music` (Go SDK only, MIT, its own `go.mod` requiring `extensions/sdk v0.4.0`, no Orpheus code).
`/music` opens a full-terminal component that shows the terminal size, echoes each key by decoded name and raw bytes,
redraws from a timer once a second and closes on `q`, Esc or ctrl+c. The state (key log, counters) belongs to the
extension, not the overlay: closing and running `/music` again shows it as it was.

### What works, and how it was verified

Unit and wire tests (`go test -race`, SDK from the v0.4.0 tag through a `go.work`):

- `internal/keys`: every Orpheus default key in legacy, application-cursor, CSI-u and modifyOtherKeys encodings,
  modifiers, lock bits, shifted-key alternates, release events, paste, text, and chunks that cannot be named. A mutation
  of the ctrl-letter mapping fails it.
- The component: one line per terminal row, each exactly the render width, ASCII only, for sizes from 1x1 to 300x2; box
  edges; key log order and cap; closing keys; timer on, off and re-attached; height-change redraw (mutation: removing
  the poll fails it); no goroutine left after detach.
- Through the real SDK and a fake host speaking the wire protocol (`overlayhost_test.go`): the overlay request is
  top-left, 100%/100%, margin 0, with no legacy modal fields; the first snapshot has one line per row at the reported
  width; key echo; `q` and Esc end the command and the notice reports the counts; resize re-renders at the new width and
  height with no snapshot wider than its tagged width; the first timer redraw arrives after about a second; reopening
  shows the earlier keys; print, JSON and RPC modes open nothing and warn.

Real terminal (`components/pig-music/tests/hello.integration.test.mjs`, `PIG_BIN=<pig 0.4.0> npm run
test:pig-music`; detached tmux, hermetic faux provider, temporary HOME, PIG_HOME and agent dir; 16 checks, all passing,
in both `fullscreen` and `regular` TUI modes): the extension loads; nothing starts by itself; `/music` fills a 100x30
pane (box top and bottom edge, no line over 100 cells); keys echo with the right names (`up`, `ctrl+right`, `ctrl+a`,
`space`, `tab`, `shift+tab`, `f5`, `delete`, `\u00e9`); the timer advances by about one per second; a resize to 70x20,
then a height-only change to 70x26, then 100x30 re-lays out within the 250 ms poll with no input; `q`, Esc and ctrl+c
close it and the editor and footer are back; reopening shows the earlier count; a streaming agent turn under the open
overlay finishes (`LIVE-STREAM-24` appears) and the transcript is intact.

Also run: `pig package validate components/pig-music --json` (valid, one extension); `pig install
components/pig-music/extensions/pig-music --validate-only --json` (valid, registers `music`); `/reload` in a live
pane rebuilt the extension and the new build was picked up (and reset the screen's state, as designed); `npm run check`
and `npm test` (117 pass).

### Not tested

- A shortcut, `/music` subcommands, `ctx.SetStatus` while hidden, `session_shutdown` reasons end to end, `stopOnExit`.
- Mouse input and the wheel; terminals other than tmux (xterm-256color), so only the Kitty-protocol encodings tmux
  produced were seen live; the other encodings are covered by the decoder's unit tests only.
- Wide (CJK, emoji) cell widths: the hello screen escapes everything non-ASCII, so one rune is one cell. The player
  will need real width handling.
- PiG 0.4.1, macOS, Windows.
- No audio exists yet; mpv and yt-dlp are not installed.

### Deviations from Pigpen conventions

- `go.mod` pins `extensions/sdk v0.4.0` (the lane's instruction) where the other Pigpen Go extensions say `v0.0.0`;
  `pig` still resolves it to the staged SDK (verified by the validate and live runs), and the repository's go.work
  helpers replace the SDK for every version. There is no `go.sum`.
- `test:pig-music` takes `PIG_BIN` without `requirePig`: `scripts/pig-requirement.json` still says 0.3.1, which a 0.4.0
  binary does not satisfy under its same-minor rule. Raising the requirement is outside this lane.

## Milestone 3: player core, no UI

Owner decisions applied: the owner installed `mpv` (apt, 0.37.0) and the upstream `yt-dlp` binary (2026.08.19, the zipapp
in `/usr/local/bin`); this lane installed nothing. The core was written and tested against a fake mpv socket and
hand-written yt-dlp fixtures first, then run against the real programs (see "Real smoke").

Layout, all inside `components/pig-music/extensions/pig-music` (stdlib only, no new dependency):

- `music/`: `Track`, `Collection`, `State`, the `Source` and `Player` interfaces; `Paths` (runtime directory
  `$XDG_RUNTIME_DIR/pig-music`, else `pig-music` under the agent directory, mode 0700; socket path length checked);
  `Settings` (the extension's own `settings.json`, read only, never written by the core); `CheckDependencies` and
  `MissingError`, which names the missing program and how to install it.
- `mpv/`: a JSON IPC client (request ids, replies matched by id out of order, unsolicited messages as ordered events,
  an unbounded event queue so a consumer may call back into the client) and `Player`: `Attach` reuses a live mpv, removes
  a stale socket and starts mpv with `--idle=yes --no-video --ytdl-format=bestaudio --msg-level=all=warn --quiet
  --input-terminal=no --input-ipc-server=...` in its own session (`Setsid`, stdin detached, stdout and stderr to
  `mpv.log` (0600), startup serialized across processes by a lock file; an mpv that does not open its socket in time, or
  whose start is cancelled, is killed). Every IPC command is bounded by a 5 s default timeout as well as the caller's
  context (changed in review, see below).
  mpv's own playlist is the queue; properties `pause time-pos duration volume playlist playlist-pos media-title
  idle-active` are observed; queue titles come back from a `tracks.json` metadata file (0600, atomic, capped) after a
  reattach. `Replace` loads the track that plays first, so sound starts at once, then builds the queue around it.
- `ytdlp/`: a `Source` that runs `yt-dlp --flat-playlist -J` against YouTube Music's search page and falls back to
  `ytsearchN:`; tolerant JSON parsing (playlist and channel hits, private and duplicate entries and bad IDs dropped);
  stderr becomes the error text; a timeout; output capped at 32 MiB; `--ignore-config`. `Library` and the liked songs
  (`LM`) fail with a message naming the `cookieBrowser` setting; no cookie is read.
- `cli/` and `cmd/pigmusic` (a module of its own, like pig-doctor's command, because PiG rejects a factory and a main
  package in one module): `search`, `play`, `add`, `queue`, `status`, `pause/resume/toggle`, `next`, `prev`, `jump`,
  `remove`, `move`, `seek`, `volume`, `stop`, `check`.

### What works, and how it was verified

`go test -race -count=2 ./...` (SDK v0.4.0 through a `go.work`); `go vet` for linux, darwin and windows:

- Client against a scripted raw socket: out-of-order replies, mpv error strings, events around replies, non-JSON
  lines, a reply-driven callback from an event handler, a drop with commands pending, cancelled commands, a 1.3 MB line.
- Player against an in-memory fake mpv (`internal/mpvfake`): `Replace` for every start position, `Enqueue` into an idle
  and a playing queue, `Move` for all 25 (from, to) pairs against a slice model (mutation: dropping the "before" offset
  fails 10 cases), remove, jump, next, prev (restart past 3 s, previous before it), pause, seek (clamped), volume
  (clamped), subscriptions (current state first, latest wins for a slow reader, closed when mpv goes away), reattach with
  titles from the metadata file, a damaged metadata file, metadata cap, calls before `Attach`.
- Spawn and reattach with the test binary re-executed as "mpv": started with the expected arguments, as session leader
  (`/proc`), still alive and answering after the player closed, a second player attaches to it without starting
  another, `Shutdown` stops it and removes the socket and metadata, a stale socket file is replaced, a missing mpv gives
  `MissingError`, an mpv that exits at once is reported with its log path, one that never opens its socket times out,
  four players started together start exactly one mpv.
- `ytdlp`: the argument lists for search, the fallback and playlists; fallback on failure, empty or unreadable output;
  both failures reported; a missing yt-dlp (relative and absolute) names what to install and skips the fallback;
  timeout; stderr as error; id validation (no argument injection); cookie-gated calls run nothing.
- The command line, one `Run` per call so each is a new "process": search then `play 2`, then `status` and `queue`
  find the same mpv with the same queue and track ("kill the CLI, music continues, start it
  again and it reattaches"); every transport command; errors and exit codes (1 failure, 2 usage); commands that need a
  player do not start one; `stop`; the missing-program message before anything starts; settings paths and a malformed
  settings file.
- A built `pigmusic` run for real with neither program installed: `check` and `search` print the install message and exit
  1, `status` prints `stopped (mpv is not running)`.
- `pig install .../extensions/pig-music --validate-only --json` and `pig package validate` still pass; `npm run check`.

### Real smoke (mpv 0.37.0, yt-dlp 2026.08.19, the test machine)

- `PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvSmoke -v` (real mpv, `--ao=null`, three generated 40 s tones): **passed on
  the first run.** Every behavior the fake models held: `loadfile ... replace/append/append-play`, `playlist-move` with
  "put before" semantics (both directions, playing track unchanged), `playlist-remove`, `playlist-play-index`,
  `playlist-prev`, seek, volume, reattach by a second player with the position having kept moving. At the end of the last
  track mpv goes idle with `playlist-pos` -1, no track, and the queue entries kept, exactly as the fake does.
- `PIG_MUSIC_SMOKE=1 PIG_SDK_DIR=... node components/pig-music/tests/player-core.smoke.mjs` (the built `pigmusic`, real mpv and
  yt-dlp, network, throwaway HOME and runtime directory): **passed.** `check`, `search never gonna give you up`, `play 1`,
  two `status` calls 3 s apart (0:01 then 0:04 of 3:34), `pause`, `resume`, `next`, `prev`, `seek 15`, `volume 50`,
  `queue`, `stop`; one mpv process served every command (same pid throughout), and `stop` ended it. This is the "kill the
  CLI, the music continues, start it again and it reattaches" check against the real thing: each `pigmusic` call is a new
  process.
- **What yt-dlp needed**, as observed: nothing beyond the program and network. Search and playlist listings, and mpv's own
  hook resolving a stream, all worked with no JavaScript runtime and no cookies, using `bestaudio` (m4a, opus and HLS audio
  formats were offered; the list was identical with `--js-runtimes node`). yt-dlp prints a WARNING that no supported
  JavaScript runtime was found, that only deno is enabled by default, that extraction without one "has been deprecated"
  and that some formats may be missing; Node (via mise) and bun were on PATH but "unavailable" to it until named with
  `--js-runtimes RUNTIME[:PATH]`. So a runtime is not needed today; it is the documented future requirement. If it becomes
  one, the player can pass it to mpv's hook with `--ytdl-raw-options`; no setting exists for that yet. Also seen: the
  `zip` build needs Python 3 and runs a listing in about 2.5 s.
- What the real output changed in the code (the recorded fixtures are in `ytdlp/testdata/recorded/`):
  1. YouTube Music's unfiltered search page opens with three album browse entries (no title), so 5 requested gave 2
     tracks. Search now asks the songs section (`...search?q=...#songs`, a yt-dlp extractor feature), which lists only songs.
  2. A songs-section entry carries only `id` and `title`: **no artist, no duration** (so `search` shows a blank duration and
     no artist). `ytsearchN:` and playlist entries carry `channel` and `duration` but `ytsearch` is noisy (hour-long mixes,
     movies). Enriching songs with artist and duration is a milestone 5 item: mpv reports the duration once a track loads;
     `yt-dlp -J` per track works but costs seconds each.
  3. Playlist listings include unavailable videos with a null title; they are dropped.
  4. mpv refuses to seek while a stream is still opening (`error running command`), which a seek right after `prev` hit.
     `SeekRelative` now returns `ErrNotSeekable` before sending it. After `play`, the first status shows `0:00 / 0:00` for
     a second or two while the stream resolves.
  5. `--log-file` records mpv at verbose level whatever `--msg-level` says. The review found it held the signed
     googlevideo stream URLs, including the listener's public IP address; `--log-file` is no longer used (mpv's own
     warnings go to `mpv.log` through stderr; a normal run leaves it empty, a failed track leaves the yt-dlp error).
  6. A live-stream result ("lofi hip hop radio") once failed to start and mpv moved on to the next entry by itself; played
     directly later it worked. Not reproduced; the player reports mpv's state, and a UI should show a skipped track.

### Not tested

- **The yt-dlp fixtures in `ytdlp/testdata/*.json` are hand-written** (private and duplicate entries, playlist and channel
  hits, `track`/`artists`/`album` fields): shapes the short real recordings do not show. They are labelled as such.
- Library and liked songs (cookies; milestone 5), Windows (stubs only compile), PiG 0.4.1, macOS.
- Audible output: `--ao=null` only; the real audio check is on the owner's Mac.

### Review of milestones 1 to 3 (lane rev-pig-music)

Fixed on the review branch, red test first: an mpv that never opened its socket, or whose start was cancelled, was left
running in its own session; a stopped or hung mpv made every `pigmusic` command wait forever (now "mpv did not answer
... within 5s"); an mpv the player started was left idle when attaching to it failed; mpv's verbose `--log-file` held
signed stream URLs and the public IP; a timed-out yt-dlp left its children (a wrapper's program, a JavaScript runtime)
running. Open, not fixed: no yt-dlp version check (an old yt-dlp is not detected; the `#songs` search section needs a
recent extractor); mpv loads the user's `mpv.conf` and its yt-dlp hook reads the user's yt-dlp config, so cookie or
playlist options set there apply to playback. Details in the review verdict.

## Milestone 4: the hosted player UI

`/music` now opens the player; `/music hello` keeps the milestone 2 diagnostic screen.

### What was built

- **`ui/`**: a fresh Bubble Tea v2 model (no Orpheus code, themes or assets; layout and key bindings taken from reading its
  README and screenshot). Three screens (Search, Library, Player), a header with the state and volume, a play bar, a status
  line, key hints and a help screen (`?`). Keys: `space` play/pause, `n`/`p` next/previous, `left`/`right` seek 5 s, `+`/`-`
  volume 5, `tab`/`shift+tab` screens, `/` search (from any screen), `enter` search or play the selected result or jump to the
  selected queue entry, `a` queue a result, `x` remove, `[` `]` reorder, `up`/`down`/`j`/`k`, `q`/`esc`/`ctrl+c` hide. In the
  search input `q` is text, `esc` closes the input and `ctrl+c` still hides. Output is ANSI 16 colours, ASCII markers, text
  widths by cell (CJK checked), exactly the terminal's rows and columns at every size from 1x1; under 24x6 it says
  "terminal too small".
- **`teahost/`**: runs a `tea.Model` without a `tea.Program` (which owns stdin and stdout; the extension owns neither). One
  loop goroutine, each `tea.Cmd` on its own goroutine, `BatchMsg` handled, `tea.Sequence` not supported (the model uses
  neither). `QuitMsg` means hide: the model, its search results and its cursor stay for the next `/music`. No redraw is
  requested while hidden.
- **`input.go`**: PiG's raw input chunks become `tea.KeyPressMsg` through `internal/keys`; mouse reports, focus events and
  Kitty key releases are dropped.
- **`app.go`, `screen.go`**: the extension holds the player and the model across hide and show. On first `/music` it checks
  the programs (a missing one is named with how to install it), attaches to mpv (starting it if needed) and forwards
  `Player.Subscribe` into the model, so the play bar moves while the screen is hidden or not. Hidden means no frames are sent.
  The terminal height is polled, as in milestone 2.
- Dependencies added to the extension's go.mod (all MIT): `charm.land/bubbletea/v2` v2.0.10, `charm.land/lipgloss/v2`
  v2.0.6, `github.com/charmbracelet/x/ansi` v0.11.8 (and their transitive modules); go.mod now says `go 1.26.0` (the
  Bubble Tea module requires a patch version).

### Verified by running

- `go test -race -count=2 ./...` (all packages), `go vet` for linux, darwin and windows, `npm run check`, `npm test`
  (117), `pig install <extension> --validate-only --json`, `pig package validate components/pig-music`.
- Fake-host wire tests with a real `mpv.Player` over an in-process fake mpv: search, play, pause, the play bar advancing
  without input, `q` hiding with no frame sent while hidden, `/music` again showing the same queue, track and the position
  mpv reached meanwhile, resize and height change (no line wider than the width), missing programs reported by name, an mpv
  that will not start reported without a crash.
- Real terminal (tmux, pig 0.4.0, fullscreen), real mpv 0.37.0 with `--ao=null` and real yt-dlp over the network:
  `PIG_BIN=... npm run test:pig-music-player` (6 checks): open, search "never gonna give you up", play, pause (position
  frozen), resume, volume key, resize to 70x20 and back, `q` hides (same mpv pid, session intact), `/music` shows it again
  at a later position. `npm run test:pig-music` (hello screen, 16 checks) still passes.

### Differs from the brief or is not built

- Shuffle (`s`) and repeat (`l`) are not built: the Player has no such operation yet. The settings screen (`o`) is not built
  (settings are the file and environment variables from milestone 3; milestone 6).
- No cover art and no Kitty graphics or OSC queries: the extension cannot write to the terminal, so Now Playing is text.
- Library shows the cookie requirement (milestone 5). A songs search has titles only: no artist or duration yet, so the
  Artist and Len columns are empty for search results (milestone 5 enrichment).
- Page keys, Home/End and the mouse wheel are not bound (the host consumes them in fullscreen at times).
- The search box has no cursor movement or word editing: type, backspace, enter.
- The extension's mpv is not stopped when PiG exits; stopping it and a footer status are milestone 6.

### Not tested

- Terminals other than tmux, mouse input, Kitty keyboard-protocol terminals (decoder is unit tested only), wide characters
  in a real terminal (unit tested), PiG 0.4.1, macOS, Windows, audible output, `/reload` with the new screen open, very
  long sessions (the model is never trimmed: results and queue are small).

## Milestone 4a: `/music setup` and `pigmusic doctor`

Binding input: the owner's research digest (YouTube needs a JavaScript runtime and yt-dlp-ejs for most formats; yt-dlp breaks
and is fixed on its own cadence; licence rules: Pigpen stays MIT, no bundled binaries, no GPL dependencies).

### What was built

- **`doctor/`** (stdlib plus the project's own `ytdlp` runner): `Run(ctx, Config, Env, Options) Report`, every check taking its
  world from `Env` (programs, exec, clock, files, probe), so tests use no network and no real programs.
  - mpv: version >= 0.35 (older fails, a dev build passes).
  - yt-dlp: found (configured path, else the copy pig-music keeps in its data directory, else PATH), version, age and who
    manages it ("self-managed by pig-music", "from a package manager", "installed by you"). Warn after 60 days, fail after 150
    (the first threshold, 30 days, warned on the real newest release, which was 46 days old).
  - JavaScript runtime and yt-dlp-ejs, parsed from `yt-dlp -v` ("JS runtimes:" and "yt_dlp_ejs-"). When yt-dlp lists none the
    doctor looks for Deno in the data directory (>= 2.3, used as `deno:<path>`), then Node (>= 22) and QuickJS-NG (>= 0.12) on
    PATH and re-runs `yt-dlp -v --js-runtimes <it>` to confirm. `jsRuntime` in settings (or `PIG_MUSIC_JS_RUNTIME`) overrides.
    No runtime is a **warning**, not a failure: yt-dlp 2026.08.19 still plays without one (measured in milestone 3). Only the
    probe turns missing formats into a failure.
  - Linux: a PulseAudio or PipeWire socket, `PULSE_SERVER`, or `libasound.so.2`; Termux: `pactl info` (fix text names
    module-aaudio-sink for Android 16); macOS and Windows: not checked. `--ao=` in `PIG_MUSIC_MPV_ARGS` skips it.
  - Probe (`--probe`, and `/music setup`/`/music doctor`): resolves one track with yt-dlp and range-reads 64 KiB of its stream.
  - Native engine: `Env.Native` hook (returns a `*Check`); nothing fills it yet.
  - Every Warn/Fail carries one copy-paste fix per OS (apt, brew, winget, Termux `pkg`). The winget identifiers
    (`shinchiro.mpv`, `yt-dlp.yt-dlp`, `DenoLand.Deno`) have not been run on Windows.
  - `LooksLikeExtractionFailure` and `Explain`: a 403, "Requested format is not available", sign-in or challenge text becomes
    "yt-dlp <version> is old or has no JS runtime: <fix>".
- **`selfmanage/`**: consent-gated download into `<agent dir>/pig-music/bin`. `YtdlpPlan` picks the zipimport `yt-dlp` script
  when Python >= 3.10 is present (public domain) else the platform executable (GPL-3.0-or-later, said in the dialog);
  `DenoPlan` the official Deno zip (MIT). `Install` verifies SHA-256 against the release's `SHA2-256SUMS` (yt-dlp) or
  `.sha256sum` (Deno), writes atomically, leaves the old file on any failure. `Update` runs `yt-dlp -U` on the self-managed
  copy at most daily, and after a 403 at most hourly. No sudo, no silent download, nothing bundled in Pigpen.
- **Wiring**: `pigmusic doctor [--probe]` (exit 1 on failures) and `pigmusic setup` (y/N per download; no answer is no);
  `/music doctor` and `/music setup` (the latter asks with `ctx.Confirm`). The doctor runs before the first `/music` and before
  `pigmusic search`/`play`/`add`; a failure refuses with every failure and its fix, and nothing starts. The resolved yt-dlp,
  `--js-runtimes` for yt-dlp (`ytdlp.Source.JSRuntime`) and `--ytdl-raw-options-append=js-runtimes=<arg>` for mpv are passed on
  (the `-append` form since the M4 review). A
  self-managed yt-dlp is updated in the background at most daily. Search errors that look like a 403 or a missing format
  are reported through `Explain` (and trigger the hourly-capped forced update when self-managed).
- `music.Source` and `music.Player` are unchanged. Additions: `music.Paths.Data`, `music.Settings.JSRuntime`,
  `ytdlp.Source.JSRuntime`.

### Verified by running

- Unit tests: 21 doctor tests (healthy machine; mpv missing, old, dev build, garbage; yt-dlp missing, aged, who manages
  it, self-managed preferred, configured path wins; no runtime, Node on PATH, Node 20 refused, QuickJS 0.11/0.12, Deno in the
  data directory, configured runtime; EJS missing; audio sockets and libasound; probe on/off and 403 explained but a network
  error not blamed on yt-dlp; native hook; `Err` lists failures and not warnings), fake programs on PATH run for real,
  the probe against a local HTTP server (206, 403, resolve failure), 10 selfmanage tests (plan per OS and architecture,
  checksum ok, mismatch leaves the old file and no temp files, no sums entry, 404, zip unpack, update daily and hourly
  limits, consent asked before each download and nothing written on no).
- Fake-host tests of `/music` with fake programs: mpv too old refuses with the fix, an mpv that will not start is reported,
  `/music doctor` explains a probe 403, `/music setup` asks (with URL, licence, checksum and no-sudo text) and writes
  nothing on no.
- `go test -race ./...`, `go vet` for linux, darwin and windows, `npm run check`, `npm test` (117), the hello (16) and
  player (6, real mpv and yt-dlp) terminal tests, `pig package validate`.
- **A real doctor run on this machine** (mpv 0.37.0, yt-dlp 2026.08.19 in /usr/local/bin, Node 24.19.0 through a mise
  shim, no Deno): `pigmusic doctor --probe` printed `ok mpv`, `warn yt-dlp ... 46 days old; installed by you` (the
  warning is gone with the new thresholds), `ok JavaScript runtime: node-24.19.0 (pig-music passes --js-runtimes node)`,
  `ok yt-dlp-ejs: 0.8.0`, `ok audio output: ALSA`, `ok resolve and stream: resolved a track and read 64 KiB of its stream`.
- **A real download** into a throwaway agent directory: `pigmusic setup` answered y: fetched the official zipimport yt-dlp
  (3 MB) from the GitHub release, verified it against the live SHA2-256SUMS, and the next doctor run picked it up as
  "self-managed by pig-music". The directory was deleted. (Deno's `.sha256sum` format was read from the release; a Deno
  download itself was not run.)

### Differs from the brief or is not done

- Setting names are camelCase like the existing ones (`mpvPath`, `ytdlpPath`, `jsRuntime`), not `mpv_path` and so on.
- No JavaScript runtime is a warning, not a failure (see above); the doctor refuses only on failures.
- A 403 that happens inside mpv (stream URL refused after resolving) is not yet mapped: the player has no error channel and
  `music.Player`/`State` were left unchanged. Only listing errors (search, library) and the probe are explained.
  The doctor with `--probe` finds it.
- Deno is not offered when Node is already usable; Node is not downloaded. Termux is directed to `pkg` (no downloads there).
- The checksum comes from the same release page as the file; it detects corruption and a wrong file, not a compromised
  release (the signed `SHA2-256SUMS.sig` is not checked).
- No new Go dependency in this milestone (stdlib only), so no go-licenses report was needed; the earlier Bubble Tea
  modules are MIT.

### Not tested

Windows and macOS (winget and brew fixes, the platform executables), Termux, the Deno download itself, `yt-dlp -U` for real
(the update ran only against a fake runner), the background update after a real 403, a machine with no audio at all, PiG 0.4.1.

### Review of milestones 4 and 4a (lane rev-pig-music-m4)

Merged the milestone 1 to 3 review fixes, then M4a. Fixed on the review branch, red test first:

- A `q` (or Esc) could be lost: the host saw the model's quit only if the quit command's goroutine reached the loop within
  30 ms. teahost now recognises the model's own `tea.Quit` on the loop.
- After mpv went away (`pigmusic stop`, a crash, a kill) `/music` reopened the old screen, still "Playing", on which every key
  said "not attached to mpv", until `/reload`. `/music` now attaches again (starting mpv, or saying why it cannot), and the
  header says "Stopped: mpv is not running" meanwhile.
- Titles, artists, albums, error text and the typed query were drawn with their control characters. The host paints lines as
  given, so an escape sequence from the network (a clipboard write, a window title, a screen clear) or a newline reached the
  terminal or split the layout. They are now replaced.
- yt-dlp read the user's yt-dlp config in mpv's hook (every track), the doctor's `-v` and probe, and `-U`. A
  `--cookies-from-browser` there meant browser cookies (checked: a dead proxy in a throwaway config broke playback before
  the fix and not after). All now run with `--ignore-config`, as the Source always did.
- A network error reading the probe stream printed the signed stream URL with the public IP. It now prints the cause only.
- A download that stops sending held `/music setup` forever. A download that receives nothing for 60 s is now given up.
- The consent text names the checksum file used (Deno's is `<asset>.sha256sum`, not `SHA2-256SUMS`).

Found and left open: the extension's mpv is left running when PiG quits (milestone 6, `stopOnExit`). A 403 inside mpv is
not mapped (stated above). A Piglet Binary with pig-music now needs `PIG_SOURCE_ROOT` (a PiG source checkout): pig 0.4.0's
build from fetched source fails for any Package with third-party Go modules ("updates to go.mod needed"; a2a fails the
same way). Dependency licences were read from each module's LICENSE file (go-licenses is not installed here): all MIT except
`golang.org/x/sys` and `golang.org/x/sync` (BSD-3-Clause). Details in the review verdict.

## Milestone 5, part 1: artist, album and length for search results (no cookies)

The library and liked songs are **not started**: they wait for the owner's answer to Q4 (cookies). This part needs no account.

- **Problem** (measured in milestone 3, re-measured): a songs search lists only an ID and a title. A track's own page gives
  its artist, album and length, but costs about 3 s per track with yt-dlp 2026.08.19 (20 results would add about a minute), so
  nothing is fetched for the whole list.
- **Design**: `music.Enricher` is a new optional interface (`Enrich(ctx, Track) (Track, error)`); `music.Source` is unchanged.
  `ytdlp.Source` implements it with one `yt-dlp --skip-download --no-playlist --print "%(.{id,title,artists,artist,channel,uploader,duration,album})j"`
  call (same `--ignore-config` and `--js-runtimes` as every other call). The artist is `artists`, else `artist`, else the
  channel without YouTube's " - Topic" suffix; what the track already had is kept when yt-dlp has nothing.
  The UI asks for **the row the cursor is on**, once per row per search, when the listing left out its length and artist; the
  answer fills the row (and what play and `a` hand to the player). A failure is shown on the status line, not hidden, and the
  row is not asked again. A stale answer from an earlier search is ignored. The extension's 403 explanation passes through
  the enrichment as well.
- **Verified**: unit tests (ytdlp: fields, fallbacks, nulls, kept fields, errors; ui: first row enriched on arrival, each
  row once, queue and play get the enriched track, failure shown and not looped, stale ignored, sources without Enrich and
  rows that have a length left alone); the real-terminal player test now waits for "Rick Astley" and a 3:xx length on the
  selected result before playing it (real yt-dlp, network): 6 of 6. `go test -race`, vet for three systems, `npm run check`,
  `npm test`, hello (16) after merging the M4 review fixes.
- **Review fix**: at most one enrichment runs at a time (scrolling started one yt-dlp per row passed); when it is back, the
  row the cursor rests on is asked for.
- **Not done / honest limits**: unselected rows stay blank until the cursor reaches them (about 3 s each); `pigmusic search`
  (the command line) does not enrich; the Player's queue still gets its length from mpv once a track loads.

## Milestone 5, part 2: the library and liked songs (owner's answer to Q4: option A)

Built per the owner's answer: `--cookies-from-browser`, library listings only, consent once per browser, the system's default
browser (or the `cookieBrowser` setting), counts-only first real call by the lead on the owner's Mac.

### What was built

- **`cookies/`**: `Detect` asks the OS for the default https handler and maps it to a yt-dlp browser name; it never guesses.
  macOS (since the review): `osascript -l JavaScript` asks NSWorkspace which app opens an https URL; if that fails, the
  LaunchServices secure plist (`plutil -convert json`), where no `https` handler means Safari (macOS's built-in default). Linux:
  `xdg-settings get default-web-browser` (and "no graphical session" without DISPLAY/WAYLAND_DISPLAY). Windows:
  `reg query ...\https\UserChoice /v ProgId`. Termux and other systems: no browser. Supported: brave, chrome, chromium, edge,
  firefox, opera, safari, vivaldi, whale. An unsupported or unreadable default gives a problem text naming what was found and the
  `cookieBrowser` setting to use. `Resolve` prefers the setting (`BROWSER[+KEYRING][:PROFILE][::CONTAINER]`, validated by name),
  else detection with the browser's default profile. `Notes` per browser and system (Chromium Keychain prompt on macOS, Safari
  Full Disk Access, keyring on Linux, locked database on Windows). `Consent` is `<agent dir>/pig-music/cookie-consent.json`,
  mode 0600, **browser names only**, per browser. `Access` ties them: `Spec` returns the flag value or
  `*music.NoBrowserError` / `*music.NeedsConsentError`; it re-resolves on every call, so a changed default browser is a new question.
- **`ytdlp.Source`**: `Cookies` (a function) replaces the old `CookieBrowser` string. `Library` lists the liked songs (`LM`,
  synthesized first) and the account's playlists (`yt-dlp --flat-playlist -J https://www.youtube.com/feed/playlists`); `Tracks`
  lists one collection (`https://music.youtube.com/playlist?list=<id>`). **Only those two calls take `--cookies-from-browser`**;
  search, enrichment, `PlayURL` and mpv's hook never do (tests assert the flag on every recorded call, and the reviewer's mpv test
  asserts it for mpv). No consent or no browser: yt-dlp does not run at all.
- **Library tab** (`ui/library.go`): first visit lists the library; without consent it shows a prompt naming the browser (name
  only) and what is read ("the cookies of chrome (the default browser, com.google.chrome) ... whole cookie store for each library
  request, in memory only; pig-music saves no cookie and never sees one; search, playback and mpv never use cookies"), the
  per-browser note, and `[y]` allow / `[n]` not now. `n` is remembered for the session; `r` asks again. No browser: the reason and the
  setting, no prompt. Errors are shown as yt-dlp said them. A collection opens with enter (esc or backspace go back; esc on the list
  and `q` anywhere hide the player), enter plays from the selected track, `a` queues it. All outside text goes through `clean`.
- **`pigmusic library-counts`**: asks on stdin (y/N) before reading a browser, then prints `playlists: N` and `liked songs: M`,
  never a title. Each half reports its own error (yt-dlp's last lines) so a wrong URL shows up as a message, not as silence.
- **Doctor**: a `library (browser cookies)` check names the browser (name only) and whether consent is recorded, or the problem and
  the setting; a warning, never a failure. It never reads a cookie.
- **Review fixes (rev-pig-music-m5)**: the Linux and Windows mappings are exact (a beta or developer channel, Opera GX, a Flatpak
  or Snap Chromium build is reported, since yt-dlp would read another build's cookies under that name); a yes is recorded for the
  browser the prompt named only (`GrantCookieAccess(ctx, browser)`); library listings get 2 minutes.
- **Consent prompt is in the Library tab, not `ctx.ui confirm`** (differs from the answer): the SDK dialog would open on top of a
  full-screen overlay that is already blocking the command, which I did not want to risk. The prompt carries the same text, once per
  browser. `pigmusic library-counts` asks on stdin.
- No new Go dependency. `music.Source` and `music.Player` are unchanged; added `music.CookieConsenter` and `music.Enricher` (optional)
  and the two error types.

### Verified by running (no real browser, cookie or account anywhere here)

- Unit tests: detection per OS with fake LaunchServices, xdg-settings and registry output (every supported browser, no entry,
  unsupported, tool missing, garbage, headless, Termux/FreeBSD); setting override and validation; consent persistence, privacy and
  per-browser scope; `Access` asks again for another browser; the Source with a recording runner: the flag only on library calls,
  before the URL, nothing runs without consent, errors carry yt-dlp's text; the UI: list, consent prompt flow (y/n/r), no-browser,
  open a collection, play, queue, esc/q, control-character injection, layout at nine sizes with CJK; the doctor check; the CLI with a
  fake yt-dlp that records its arguments (counts printed, no title printed, consent file holds only `{"browsers":["chrome"]}`,
  second run not asked, a refusal reads nothing, no browser names the setting; a search afterwards has no cookie flag).
- `go test -race ./...`, vet for linux/darwin/windows, `npm run check`, `npm test` (117), hello (16) and player (6) terminal tests.
  `pigmusic doctor` on the test machine: `warn library ... no graphical session ... Set "cookieBrowser"`.

### Not verified: what the first real call is for

The two YouTube URLs are read from yt-dlp's source (`feed/library` and `playlist?list=` are supported tabs) but **never run with an
account**: whether `feed/playlists` lists the playlists as entries with an `id` and `title`, what `playlist_count` looks like,
and whether `list=LM` on `music.youtube.com` lists the liked songs are unknown. The fixture JSON is hand-written. If the real
run reports an error or a zero, that is the finding and the URL or the parser changes. Without cookies both fail fast and
clearly (seen in review: `feed/playlists` "HTTP Error 401: Unauthorized", `list=LM` "The playlist does not exist", about 2.5 s
each). Also untested: the `osascript` question and the `plutil` fallback on a real Mac (the lead's first run on the owner's Mac
found the plist without an https handler for a never-changed Safari default, which is what led to the `osascript` question; the
script itself has not been run on a Mac), the Keychain and Full Disk Access prompts (the lead saw Safari's "Operation not
permitted" without Full Disk Access, reported clearly), Windows registry output, and consent through the SDK dialog (not used).

### The exact first real command (for the lead, on the owner's Mac)

```sh
cd <pigpen checkout>/components/pig-music/extensions/pig-music
printf 'go 1.26.0\nuse (\n  <PiG v0.4.0 checkout>/extensions/sdk\n  .\n  ./cmd/pigmusic\n)\n' > /tmp/pm.work
(cd cmd/pigmusic && GOWORK=/tmp/pm.work go build -o /tmp/pigmusic .)
/tmp/pigmusic doctor            # shows "library (browser cookies): <browser> (the default browser, <bundle id>) ..." or why not
/tmp/pigmusic library-counts    # asks y/N, then prints exactly two lines: playlists: N / liked songs: M
```

Needs mpv and yt-dlp installed (they are on the Mac) and YouTube signed in in the default browser. If doctor says macOS records no
default browser (only if `osascript` failed and the plist could not be read), put `{"cookieBrowser":"safari"}` in
`~/.pig/agent/pig-music/settings.json` and run it again (Safari needs Full Disk Access for the terminal). Report only the two counts
and any error text, never contents. Delete `~/.pig/agent/pig-music/cookie-consent.json` to withdraw the consent.

### M5 fixes after the first real run on the owner's Mac (lead's note in the questions file)

- **Default browser on macOS**: a Mac whose default was never changed (Safari) has no https entry in the LaunchServices plist, so
  the plist-only detection said "unknown". Detection now asks the system first (`osascript -l JavaScript`, NSWorkspace
  `URLForApplicationToOpenURL(https://example.com)`, bundle identifier; no cgo) and reads the plist only when that gives nothing
  or fails. An app the system names that yt-dlp cannot read (for example Arc) is reported as unsupported, not replaced by the
  plist's older entry. Red test first; the JXA one-liner is untested on a real Mac from here (the lead's run is the check).
- **Safari and Full Disk Access**: the real run failed with yt-dlp's `[Errno 1] Operation not permitted: ...Cookies.binarycookies`.
  Library errors of that shape now end with the fix: give the terminal Full Disk Access, or choose another browser with the
  `cookieBrowser` setting. The counts are still unobtained; the owner chooses between Full Disk Access and Chrome.

### Review of milestone 5 (lane rev-pig-music-m5)

Fixed on the review branch, red test first:

- **mpv's yt-dlp hook could carry cookies.** mpv read the user's `mpv.conf`; a `ytdl-raw-options=cookies-from-browser=...`
  line there (or an auto profile, script-opts, a user script) reached the hook's yt-dlp for every track, and
  `ignore-config=` does not remove it. Shown with a real mpv and a fake yt-dlp recording its arguments. pig-music's mpv now
  starts with `--no-config`; `PIG_MUSIC_MPV_ARGS` is where an audio device goes.
- **macOS Safari default** (the lead's finding on the owner's Mac; M5c above fixed it in parallel): asked of the system through
  `osascript`; on the merge of M5c the review's version was kept, which also falls back to the plist when the system's answer
  is not a bundle ID (M5c's script unwraps a possibly nil bundle; what JXA prints then was not checked on a Mac) and reads a plist without an https handler as Safari, macOS's
  built-in default, instead of "unknown". M5c's Full Disk Access hint is kept as it was.
- **Mappings that guessed**: Linux desktop entries were matched by substring, so `google-chrome-beta`, `microsoft-edge-dev`,
  `opera-beta`, Flatpak and Snap Chromium builds mapped to the stable browser, whose cookie directory (possibly another account)
  yt-dlp would read; Windows mapped Opera GX to Opera. Now exact; the rest is reported with the setting.
- **A yes for a browser not shown**: `Grant` re-resolved the browser, so a default changed while the prompt was open was granted
  without being named. The yes now carries the browser the prompt named; a different one is asked about.
- **One enrichment per row scrolled past**: holding `j` started a yt-dlp (about 3 s each) for every row. Now one at a time.
- **Library listings had 30 s**: about 0.65 s per 100 entries (measured), so a few thousand liked songs timed out. Now 2 minutes.

End to end with the fused Binary in tmux (throwaway HOME and agent dir, a wrapper yt-dlp that answers cookie calls itself and
passes the rest to the real one): doctor, search, enrichment and mpv's hook ran without `--cookies-from-browser`; only the
`feed/playlists` and `playlist?list=` listings had it, after the `y`. The consent file held `{"browsers":["firefox"]}` (0600).

## Milestone 6: footer status, shortcut, stop, stopOnExit, reload, shuffle, repeat, settings screen

Done and verified (WIP commits 1c52ae2 and e0c05ce, then the M5 review merge 783a19c):

- `lifecycle.go`: `statusText` (`> Title - Artist 1:23/3:33`, `||` when paused, control characters stripped, 60-character title) and
  a `footer` that writes it with `ctx.SetStatus("pig-music", ...)` from player states while the screen is hidden, clears it while the
  screen is open, and restores it on hide. The Context comes from `session_start`, `/music` or `/music stop`; verified in real pig
  0.4.0 that a Context kept after its handler returned still updates the footer, and that a `/reload` restarts the extension, which
  then re-attaches. (Review correction: pig 0.4.0 runs a `/reload` or a session switch as a new generation of the extension's
  factory in the same process, the Go cell runner or, in a Piglet Binary, PiG itself; see "Review of milestone 6".) Setting `footerStatus` (default true) turns it off.
- `alt+m` shortcut (`ext.Shortcut`), same as `/music`. `/music stop` (also when this process never attached; never starts an mpv to
  stop it). `session_shutdown` stops mpv only for reason `quit` (verified reasons: `reload`, `quit`; `new`/`resume`/`fork` per the
  parity docs) and only if `stopOnExit` is true (default). `session_start` attaches to an mpv that is already playing, so the footer
  and `/music` (which now opens on the Player screen via `ui.ShowPlayerMsg`) work after a `/reload` with no `/music` first.
- Tests: `lifecycle_test.go` (fake host + fake mpv: footer while hidden, cleared while open, paused, no control characters,
  `footerStatus:false`, `/music stop`, nothing-to-stop, shutdown reasons, `stopOnExit:false`, reload re-attach, shortcut) and the
  real-terminal test (9 of 9: footer, alt+m, `/reload` keeps the same mpv pid, `/music stop`, `/quit` stops mpv). The fake host now
  records `ui.setStatus`, `ui.confirm`, event handlers (from the register frame) and can emit events and shortcuts.

Added after the owner's test on the Mac (78d5a04): `/music` opened on a Library tab showing the Safari Full Disk Access error, and the
owner read it as "no music option". The screen model persists across hide and show, so a Library tab left with an error stayed on
screen at the next `/music`. Now: `ui.OpenedMsg` (sent when the screen is shown again) moves a Library tab that cannot list anything
(no consent, declined, no browser, load error) to Search when nothing is playing; a working library, a playing track and the other
screens stay as they were, and the error stays on the Library tab. The empty Search screen says how to start ("Press / to search
YouTube Music. Then type a song or artist, press enter to search, and press enter on a result to play it."). Tests first (ui and a
fake-host reopening test that fails without the fix); real-terminal tests still 16/16 and 9/9.

### Shuffle, repeat and the settings screen (done)

- **Shuffle and repeat** are the optional `music.Modes` beside `music.Player` (`SetShuffle`, `SetRepeat`); `music.State` gained
  `Shuffle` and `Repeat` (additive; zero values are off), and `music.RepeatMode` (off, all, one). The mpv Player implements them:
  repeat is mpv's `loop-playlist` (all) and `loop-file` (one), observed so the state follows mpv; shuffle is `playlist-shuffle`
  (the current track keeps playing) and `playlist-unshuffle`. mpv has no property for "shuffled", so the Player remembers it:
  it is forgotten when the extension restarts and a new queue (`Replace`) starts unshuffled. **Turning shuffle off needs mpv 0.37**
  (the doctor's floor is 0.35): on an older mpv the error says so and the queue stays shuffled. Keys `s` and `l` (off, all, one,
  off) on every screen; the Player screen shows `Shuffle: on|off` and `Repeat: off|all|one` only when the player has Modes; a
  player without them says "not available". The fake mpv models the properties, shuffle and unshuffle (and an old mpv without it).
- **Settings screen** (`o`): the two switches `stopOnExit` and `footerStatus`, toggled with space or enter and **saved at once** by
  `music.UpdateSettingsFile`, which keeps every other key, writes atomically with mode 0600, creates the file, and refuses (leaving
  it untouched, naming it) a file that is not a JSON object. A footer switched off is cleared immediately; `stopOnExit` is read at
  shutdown. Read-only lines from the doctor's start-up report (mpv, yt-dlp, JavaScript runtime, library browser; name only).
  esc or `o` closes it, `q` hides the player, other keys do nothing inside. This is the first time the extension writes
  `settings.json`, and only from this explicit action.
- **Verified**: unit tests (music, mpv with the fake, ui at nine sizes, the fake-host flow that writes the file, keeps `unknownKey`
  and `cookieBrowser`, and clears the footer); real terminal (real mpv 0.37): `l`, `s` on and off, `o`, plus everything from the
  earlier M6 list, 9 of 9; hello 16 of 16. `go test -race`, vet for linux, darwin and windows, `npm run check`, `npm test` (117).

### Not tested (M6)

macOS, Windows and Termux; `alt+m` in terminals that do not send Meta as an escape prefix, and a conflict with a built-in shortcut in
another PiG version (the host reports conflicts); turning shuffle off on a real mpv older than 0.37 (modelled by the fake only);
the footer redraw rate of once per second on a slow terminal; stopOnExit through a signal-killed PiG (no `session_shutdown` is
delivered then, and mpv keeps playing); a settings file edited by hand while the screen is open (it is read when the screen opens).

## M6b: artist, album and length everywhere (owner's screenshot from the Mac)

**Bug**: in Up Next, Artist and Len were empty for every queued track, and Now Playing showed only the title.
**Measured** (yt-dlp 2026.08.19, no cookies): a songs search lists only `id` and `title`; a flat playlist listing lists `id`, `title`,
`duration` and `channel` (the parser already maps channel to artist, without " - Topic") but no album. So library tracks were fine
and everything from a search, queued whole, was bare; M5a had enriched only the selected result.
**Fix** (`ui/enrich.go`): the model asks the Source's optional `Enricher` for every track a screen shows that lacks an artist or a
length, **three at a time** (`enrichParallel`; the reviewer's limit of one is replaced, as the owner asked), in the order a person is
likely to look: the playing track first (which also wants its album), then what the current screen is on and around (rotated from
the cursor), then the other screens' lists. Answers are cached by video ID for the life of the model and each ID is asked once;
a failure is remembered (no retry) and reported once on the status line, others continue. Drawing only reads the cache (`fill`),
so nothing runs on the render path, and `Enrich` has no cookie access, so search, queue and library enrichment never use cookies.
Play and queue pass the enriched tracks to the player, so the queue keeps them. mpv's length for the playing track is used at
once; Now Playing shows title, artist, album and `Length m:ss`.
**Verified**: ui tests (every result, playing track first, at most three in flight and the cursor row next, queue rows, Now Playing
fields, mpv's length, listings that already have data are not asked about, cache across searches, played tracks carry the data,
no source calls while drawing, library tracks); the real-terminal player test now waits for four Up Next rows with lengths
(90 s) and Now Playing's artist or length (9 of 9, real yt-dlp over the network).
**Not done**: the cache is in memory (a new session asks again; about 3 s per track, three at a time), and the Player's saved
queue metadata keeps what was enriched before queuing, not what arrives later.

### Review of milestones 6 and 6b (lane rev-pig-music-m6)

Fixed on the review branch, red tests first (verdict: the rev-pig-music-m6 verdict):

- **`pig -p` stopped the music.** Print, JSON and RPC runs load the extension and end with `session_shutdown` reason `quit`;
  seen with real pig 0.4.0 (`pig -p hello` in a second shell stopped the interactive PiG's mpv). Another interactive PiG that never
  opened the player did the same on `/quit`. Now the PiG that last opened the player (`/music`, `alt+m`) owns the music: it writes
  a random token, mode 0600, to `owner` next to mpv's socket, and quit stops mpv only for that token. The token is package state, so
  a `/reload` (same process) keeps it. Not covered: a PiG killed by a signal (no event; the music plays on until `/music stop`).
- **Every `/reload` leaked an mpv connection.** The Go cell runner stays across `/reload` (same pid) and runs a new generation of
  the factory; the old app kept its mpv connection, follower goroutine, model loop and footer Context (seen: one more mpv
  connection per reload). `session_shutdown` now releases them (any reason; mpv keeps playing); seen afterwards: the runner holds
  one mpv connection after three reloads and a `/new`, and the fused Piglet Binary the same.
- **Shuffle stranded tracks.** mpv 0.37's `playlist-shuffle` moves the playing entry to a random place (seen: 3, 2, 1, 1, 7 of 8),
  so with repeat off the queue ended after it. The fake kept it in place, which hid this. Shuffle now moves it first; a real-mpv
  smoke (`PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvShuffle`) checks three rounds and that unshuffle restores the order.
- **`n` on the last track with repeat all** said "end of the queue"; it now wraps, as mpv's `loop-playlist` does.
- **M6b**: a row whose enrichment failed was never asked again (a network blip left a list blank until a restart); a new search
  now asks again for failed rows. An artist YouTube lists twice (seen: `["Vindaloo Singh", "Vindaloo Singh"]`) is shown once.
## Milestone 7a: cover art and a spinning disc

**Built** (`art/`, `ui/anim.go`, `music.Artwork`): the Player screen's left panel shows the track's cover when it has loaded,
else a braille disc that turns while playing; the footer status has a record spinner.
- **Cover art as text, not as an image protocol.** The extension returns lines that the host paints and cannot write to the
  terminal, so Kitty, iTerm2 and sixel images (and tmux passthrough) are out of reach: **SDK gap for the questions file** (an
  image-capable line or component API would be needed; Tern and Ghostty detection is moot without it). The thumbnail is decoded
  with the standard library (JPEG, PNG; WebP is not requested at all) and drawn with half blocks: centre square, box-averaged,
  upper pixel as foreground and lower as background; 24-bit colour for `COLORTERM=truecolor|24bit`, xterm-256 for a `TERM` with
  `256color`, a shade ramp (` ░▒▓█`, no escapes) for `NO_COLOR`, `TERM=dumb` or an unknown terminal. The only escapes ever
  written are colour (SGR) sequences, and a test asserts no Kitty/iTerm2/sixel/APC sequence appears in any mode.
- **Source** (differs from "via yt-dlp metadata" in saving a yt-dlp call): the track's own `ArtURL` when it is an HTTPS `.jpg`,
  `.jpeg` or `.png`, else `https://i.ytimg.com/vi/<id>/mqdefault.jpg` (16:9 without letterbox bars; an art track's centre square is the
  album art). A plain HTTPS GET, no cookies or credentials (the test server rejects any), JPEG/PNG content types only, 2 MiB per
  file, pictures over 4096 px refused before decoding. Cached as `<data dir>/art/<id>.img` (0600, atomic write), 20 MiB total, oldest
  files dropped first, a corrupt file refetched. `coverArt: false` in settings.json leaves only the disc.
- **Disc**: braille, 12 frames, rim, label with a radial mark, two glints on the grooves; it turns about once in two seconds.
- **Animation off the render path**: the model schedules a 160 ms `TickMsg` (`Deps.Tick`, default `tea.Tick`); the chain runs
  only while the track plays, the screen is shown (`ui.ClosedMsg` from `screen.SetInvalidate(nil)`, `OpenedMsg` on show), the
  Player screen is current, a disc (not a loaded cover) is on it and there is room (width >= 60 and >= 4 free rows). Paused,
  stopped, hidden, on another screen or with a cover drawn: no tick, so no redraw. One tick at a time, never a second chain.
  Fetching the cover is a background command; a failure keeps the disc, is not retried and is not shown.
- **Footer**: a spinner `|/-\` replaces `>` while playing, by the whole second of the position (no extra updates); paused is `||`.
**Verified**: `art` tests (colour modes, half-block colours, centre crop, exact widths, ramp, disc size/spin/period, cache:
fetch, reuse across sessions, own thumbnail, webp fallback, size and type limits, cap and eviction, corrupt file, no credentials,
cancelled context), `ui` tests (disc present/absent by size, ticks start/stop/never double, pause/hide/stop/other screen, cover
replaces disc, per-track fetch, late cover ignored, failure falls back, monochrome, no protocol escapes, exact fit at eight
sizes, never fetched while drawing), footer spinner; real-terminal player test 9/9 now also waits for a disc or cover in Now
Playing; a real fetch of a public thumbnail decoded and drew.
**Not tested**: a real terminal's colour output on the Mac (tmux here reports 256 colours), and the cover's look on art
tracks versus videos beyond one sample.

## Milestone 7b: settings from the status line, and the now-playing widget

**SDK finding (to record for the owner)**: `ctx.SetStatus` takes text only, so a status item cannot be interactive, and a custom
footer (`ctx.SetFooter`, which the owner's context-info installs) replaces the built-in footer where statuses render, with no
access to other extensions' statuses (filed with PiG core for 0.4.2). Nothing in the Go SDK lets an extension detect that another
extension replaced the footer. So: the status line stays the compact item (title, spinner, position), `/music settings` is the
interactive part, and the widget is the way out for a replaced footer.
- **`/music settings`** (`ui/quick.go`, `quickport.go`, `ui.QuickModel`): a small centred overlay (64 cells wide, 15 rows; not the
  full player, and it never starts mpv) with the now-playing line and seven rows: Volume (left/right by 5), Shuffle, Repeat
  (off/all/one), Engine (auto/mpv/native), Library browser (system default or chrome, firefox, safari, edge, brave, chromium, opera,
  vivaldi), Now playing line (auto/footer/widget) and Cover art. Up/down (or j/k) choose, left/right/space/enter change, esc or q
  close. Volume, shuffle and repeat act on the running player at once (and say "not playing" when there is none, without driving
  anything); the others are written to `settings.json` atomically (0600, other keys kept, an empty value removes the key). The
  now-playing line moves at once; library browser and cover art say "takes effect after /reload" (they are read when the
  player starts). A failed save or player call is shown and the value does not change. Titles are cleaned of control characters.
- **`nowPlaying`** setting: `auto` | `footer` | `widget` (an invalid value is refused naming the file). `footer` is the M6 status;
  `widget` is `ctx.SetWidget("pig-music", [line])` (one line above the editor, the SDK's widget_push) with the same text and the
  M7a spinner, written while the player is hidden, cleared when the player opens, on `/music stop` and when nothing plays. `auto`
  means `footer` until the SDK can tell a replaced footer; the owner on the Mac sets `widget`. `footerStatus:false` silences both.
  Switching in the settings box clears the old place and writes the new one at once.
- Also: `music.UpdateSettingsValue` (string or bool), `Settings.NowPlaying`/`CoverArt`, the fake host records widget writes.
**Verified**: unit tests for the model (rows, volume clamp, shuffle/repeat, saving with notes, browser cycle both ways, nothing
playing, failures, esc/q, exact fit at seven sizes with a hostile title), extension tests over the fake PiG host (small centred
overlay options, volume of the running player, engine saved with its note and unknown keys kept, now-playing line switched to the
widget at once, widget lifecycle: shown while hidden, cleared on open and on stop, paused mark, default footer with no widget writes,
`footerStatus:false`), settings tests; real-terminal test: `/music settings` opens, left/right change the real mpv volume, q closes
(10/10 in the player suite). **Not tested**: a real context-info footer (not available here), the widget in a real pig (only the
fake host records it; widget_push is documented in the SDK), the Mac's terminal.

## Milestone 7c: quick `/music` commands

`/music play <query|number>`, `pause`, `resume`, `toggle`, `next`, `prev`, `vol <0-100>`, `now`, `queue`, `shuffle on|off`,
`repeat off|one|all` (and the existing `stop`, `settings`, `setup`, `doctor`). They never open the overlay and answer in one
line (`queue`: a heading and up to 8 lines around the current track, with "(N more in the queue)").
- **Shared code**: the slash command runs `cli.Run` in-process (`quickcmd.go`), so it behaves exactly like `pigmusic`: the same
  result cache of the last search, engine choice, the doctor before mpv starts, status line. The command line gained `vol` (for
  `volume`), `now` (for `status`), `shuffle on|off` and `repeat off|one|all`; its status line adds `shuffle` / `repeat all|one`.
  mpv has no property that says a playlist was shuffled, so `now` from a later command shows repeat but not shuffle (the command
  that sets shuffle prints it).
- **`play <number>`** takes the number from the last search, whether it was made with `pigmusic search` or in the player's own
  search (`music.SaveSearch`/`LoadSearch`, used by both). `play` with no argument resumes. After `play`, this PiG claims the music
  (stopOnExit) and attaches so the footer or widget follows.
- **Answers are notifications, not conversation messages**: `ctx.SendMessage` would put the line into the context sent to the
  model. A mistake gives one short line (the first line of the command line's complaint, without its usage text).
- **Argument completion** (`sdk.RegisterCommand` with `GetArgumentCompletions`): the subcommands with a description, then `on|off`,
  `off|one|all` and volume steps after `shuffle`, `repeat`, `vol`. A query after `play` is not completed.
- **`/music like` is not offered**: liking a song writes to the account, a new cookie scope (the library only reads) with no consent
  given, so it is skipped as the task allowed.
**Verified**: command-line tests (aliases, shuffle/repeat with the fake mpv, usage mistakes exit 2, nothing playing exits 1), music
tests for the saved search, extension tests over the fake host (every command, one-line answers, queue list, no overlay opened,
`play 3` after a search in the player, mistakes give one line without usage, completions and queue cutting), real-terminal test
(pause, resume, vol 55, now, queue in a real pig 0.4.0 without opening the player; 11/11). **Not tested**: completions in a real pig
(the SDK is documented to answer `command_argument_completions`; only the function is tested), the native engine through the
slash command, Windows.

### Review of milestones 7a, 7b and 7c (lane rev-pig-music-m7)

Fixed on the review branch, red tests first (verdict: the rev-pig-music-m7 verdict):

- **Quick commands ran the whole doctor first.** In auto mode the command line chose an engine (the doctor: mpv, yt-dlp twice,
  the browser lookup) before it looked for a running player, so every `/music pause` blocked the command for about 2.2 s here
  (seen in real pig). A running mpv or native player is now looked for first; play and add still run the doctor before they
  start anything. Seen afterwards: `/music vol 66` answers in about 160 ms in a fused Piglet Binary.
- **The quick commands followed `engine`, the player does not.** With `"engine": "native"` (offered by `/music settings`, which
  said "takes effect after /reload") `/music play` went to a native player that the overlay, the footer and stopOnExit never
  see, or failed where the player plays. Inside PiG the quick commands now use mpv like the player, and the settings box says
  the engine is for `pigmusic` only.
- **Any other word after `/music` opened the full player**, so `/music pasue` or the skipped `/music like` took over the
  terminal. Such a word now gets one line naming the commands; `/music like` says why it is not offered.
- Hints in quick-command answers named `pigmusic play` / `pigmusic search`; they now name `/music`.
- A thumbnail redirect to plain HTTP was followed; redirects now stay on HTTPS.
- Tests added for two surviving mutants: `/music play` claiming the music (stopOnExit), and the real source path keeping the
  player's last search for `play <number>`.

Seen in a real pig 0.4.0 (tmux, mpv `--ao=null`, a stand-in extension replacing the footer as context-info does): the widget
shows above the editor with the spinner, stands still as `||` when paused, comes back after `/reload`, and clears when mpv goes
away; argument completion for `/music sh` and `/music shuffle ` works.
## Milestone 8a: a palette from the track (vibes)

The overlay is painted with colours from the cover. **Built** (`art/palette.go`, `ui/anim.go`, `ui/view.go`):
- **Extraction** (`art.Extract`): the centre square of the cover (so the black pillars of a 16:9 thumbnail do not count), 32x32
  pixels, near-black pixels ignored unless there are no others, median cut into up to 4 colours, most populous first, deterministic.
  Pure Go, no cgo.
- **Derivation** (`art.Derive`): a background gradient (top: the dominant colour, bottom: the second, or the first darkened), the
  most vivid colour as the accent, white-ish text. **Guaranteed contrast**, WCAG relative luminance: the backgrounds are scaled down
  to at most 0.08 luminance (hue kept), so the text has at least 7:1 against every row of the gradient, and the dim text, the accent
  (lightened toward white as far as needed) and the error colour at least 4.5:1. Property tests run it over 300 random colour
  sets and the awkward ones (all white, all black, pure yellow, greys, nothing) and over 50 random cross-fades at 9 steps each.
- **No cover**: `art.FromTrack` makes a palette from a hash of artist and title (deterministic, distinct across artists, same
  guarantees). A track whose cover will not come (no art source, or the fetch failed) gets that palette; a track whose cover is on
  its way keeps the previous palette until it arrives (so there is no double change).
- **Cross-fade**: 8 steps of 80 ms from what is on screen now (a track change never jumps), mixed with `art.Mix`, which re-applies the
  guarantee at every step. The very first palette, and any change while the screen is hidden, is simply there. The palette is
  worked out in the cover's fetch command, never while drawing. The tick chain runs only during a fade (plus the disc's, M7a).
- **Painting** (`Model.paint`): each line opens with the palette's text colour and its row's background and returns to them after
  every reset inside it; the faint, accent and error styles are swapped for the palette's checked colours (the faint attribute
  would lower contrast by an unknown amount). The title, the disc and the progress bar take the accent. 24-bit colour, xterm-256
  fallback (`RGB.SGR`), nothing in monochrome. Colours are drawn in the overlay's own cells only; no escape other than colour is
  written (a test asserts no OSC, APC, DCS, mode, clear or home sequence, so no OSC 11).
- **Setting** `palette` (default true) in settings.json; off in `NO_COLOR` or a colourless terminal whatever it says. (M8c exposes it in
  `/music settings` with the other vibes settings.)
**Verified**: art tests (contrast formula, extraction, derivation invariants, mix, gradient, escapes), ui tests (every line opens with
readable colours on its row, resets return to the background, widths and heights unchanged at seven sizes in two colour modes,
nothing painted in monochrome or with the switch off, 256-colour indices, first palette at once, cross-fade steps and readability,
a missing cover keeps the metadata palette, hidden screen snaps, accent on bar/title/disc, dim colour instead of faint, no
non-colour escape), extension tests over the fake host (painted with COLORTERM, off with `palette:false`, NO_COLOR, no colour),
real-terminal player test 11/11 (tmux, 256 colours). The real-terminal test now waits a second between quick commands: a slash
command typed while the previous handler is still returning was lost under the new timing. **Not tested**: the look on a real
truecolor terminal on the Mac (colour choices are measured, not seen), light-theme terminals (text colour is set explicitly, so it
should hold).

## Milestone 8b: a beat-reactive pulse from real audio levels

**Real levels, measured not guessed.** New optional `music.Levels` (`SetLevels(on)`, `Level() (RMS, Peak, ok)`, linear 0..1); `Player` and
`Source` are unchanged.
- **mpv** (`mpv/levels.go`): an `astats` lavfi filter (`@pmlv:lavfi=[astats=metadata=1:reset=1:length=0.05:...]`) added with `af add`
  and removed with `af remove`; the loudness of each 50 ms window is read from the `af-metadata/pmlv` property (RMS and peak in dBFS,
  `-inf` for silence, converted to linear and clamped). Probed first on the real mpv 0.37 here, then a smoke test
  (`PIG_MUSIC_SMOKE=1 go test ./mpv -run RealMpvLevels`) plays generated tones with a real mpv (`--ao=null`): a sine of amplitude 0.5
  measured RMS 0.353 and peak 0.4999 (theory 0.354 and 0.5), amplitude 0.05 measured RMS 0.0353 and peak 0.0500, and the filter keeps
  measuring across a new file. Measuring is off until asked: no filter is added otherwise, and it is removed when the pulse stops.
  Latency: the filter sits before mpv's audio buffer, so the meter runs a fraction of a second ahead of what is heard (not
  compensated).
- **native engine** (`native/engine.go`, `proto.go`, `daemon.go`, `client.go`): RMS and peak computed in `pcmRead` from the float PCM
  already decoded, before the volume (so the volume setting does not change the loudness), only while switched on (`cmdMeter`),
  read with `cmdLevel`; a reading older than 300 ms is none (paused, stopped). Tested with a constant-amplitude decoder (exact 0.5),
  a volume change that must not move it, staleness, and the round trip over the daemon socket.
- **The pulse** (`ui/anim.go`): while playing, shown, with colour, the palette and a `Levels` player (and not calm) the model asks
  the player once to measure, ticks every 66 ms (about 15 frames a second, the cap), reads one level per tick off the drawing path,
  and drives three things: the background brightens toward the accent (`art.Palette.Pulsed`: at most 0.025 more luminance, never past
  0.085, so text keeps more than 7:1 at every strength, property-tested), the disc turns up to twice as fast, and a slim `Level` meter
  (14 cells in eighths) shows the loudness. A beat is loudness above the slow average (with a dead band, and the first reading
  is the average), so steady loud music is no beat and the pulse decays; a missing reading lets it die away with no error. Paused,
  stopped, hidden, another player, calm, no colour: no measuring, no ticks, no reads (measuring switched off once).
- **Calm** (`Deps.Calm`, setting `calm`): no motion at all, so no disc turning, no fade (palette changes snap) and no pulse. M8c adds
  the settings screen rows and low-power.
- **Redraws**: the Bubble Tea host now skips the redraw request when an update left the screen exactly as it was.
**Measured CPU on the test machine** (one core = 100%; 12 s per row; real mpv playing a generated tone with a 2 Hz beat, `--ao=null`; the
extension process here is the model, its drawing and the level reads over mpv's socket, plus a 200 ms state poll that the real
host does not do; `PIG_MUSIC_SMOKE=1 PIG_MUSIC_CPU=1 go test . -run MeasurePulseCPU -v`):

| situation | frames/s | extension | mpv |
| --- | --- | --- | --- |
| calm (nothing animates) | 0 | 0.97% | 0.50% |
| palette and disc only (pulse off) | 6.2 | 0.94% | 0.41% |
| pulse on, playing | 14.9 | 2.45% | 0.66% |
| pulse on, paused | 0 | 0.66% | 0.00% |

So the pulse costs about 1.5 points of a core in the extension and about 0.25 in mpv. In a real pig 0.4.0 in tmux (pig plus its
extension, the host's repaint included; the real-terminal test prints it): **13.3% of a core with the player shown and pulsing, 2.9%
with it hidden and the music playing**. Most of the 13% is the host repainting the overlay at 15 frames a second: the pulse is the
heaviest thing the extension does, which is why it is off in calm mode, stops when hidden, and gets a low-power mode in M8c.
**Verified**: art tests (pulsed palettes readable at every strength and within the cap), mpv tests with the fake (filter added once,
removed, linear conversion, silence, clamp, idle) and the real-mpv smoke, native tests, ui tests (measuring on/off transitions, tick
cap, one read per tick, beat and decay, steady loudness, readable at the top of a beat, disc speed, meter, no pulse without
levels/calm/mono/switch, missing readings, exact fit at seven sizes), extension tests over the fake host (the filter is added while
shown and removed on hide; never with calm, `pulse:false`, NO_COLOR or no colour), the host's skip of identical redraws.
**Not tested**: how the pulse looks and feels on the owner's terminal; a native daemon built into a real pig run; the latency of the
mpv filter against the sound (no sound card here).

## Milestone 8c: safety and settings

- **Colours only in the overlay's own cells.** Every painted line opens with its own colours and ends with a reset, so no colour can
  run into the terminal's cells, and a test over all four colour modes with the pulse running asserts that nothing but colour
  sequences is written (no OSC, so no OSC 11, no APC, DCS, mode set, clear, home or bell). The footer status and the widget carry
  no colour at all (plain text; the host styles them).
- **Colour fallbacks**: 24-bit, 256, and now **16 colours** (`art.Color16`: codes 30-37, 90-97 for text, 40-47 only for backgrounds,
  nearest basic colour; chosen from a `TERM` that names colour: xterm-color, linux, screen, tmux, rxvt, ansi, cygwin) and
  monochrome. `NO_COLOR` (any value) and `TERM=dumb` mean monochrome and win over everything: no palette, no pulse, no colour
  escape. The 16-colour contrast cannot be guaranteed (the user's own palette decides what those codes look like); text is bright
  white on the darkest basic background.
- **Tint terminal (OSC 11) is not offered, and why.** The extension cannot write to the terminal: its stdout is the protocol channel
  and the Go SDK has no call for raw terminal output (searched: no OSC, raw write or passthrough). A tint would also have to be
  restored on crash, which an extension cannot promise. So the terminal's own background is never touched, with no opt-in to turn
  on: **SDK gap for PiG core** (a terminal-attribute call the host would restore itself on exit would make the opt-in possible).
- **Settings** (settings.json, and rows in `/music settings`): `palette` (default on), `pulse` (on), `calm` (off), `lowPower` (off);
  they apply to the running player at once (`ui.StyleMsg`; measuring stops when the pulse is switched off). **Calm**: no motion, so no
  disc turning, no pulse, no cross-fade (colours simply change). **Low power**: the pulse at about 6 frames a second, the disc at
  half speed, no cross-fade. The box grew from 15 to 19 rows.
**Verified**: art tests (16-colour mode selection and codes), ui tests (settings change the running model at once, calm mid-fade snaps
and stops ticks, low power slows the pulse, halves the disc and ends the fade, 16-colour lines, no mode writes anything but contained
colours), the settings box rows, an extension test (switching the palette off in the box reaches the running player, saved without a
note), real-terminal player test 12/12. **Not tested**: 16-colour rendering on a real console, a real context-info footer with the
widget.

### Review of milestone 8 (lane rev-pig-music-m8)

Reviewed `860f987..8ad63ac` (bd64ac8 M8a, efa01ca M8b, 8ad63ac M8c); verdict in the lead's tasks directory
(`rev-pig-music-m8-VERDICT.md`). Fixed on `rev-pig-music-m8`, each with a red test first:
- **Contrast in 256 and 16 colours.** The promise was measured on the RGB palette, but a 256-colour terminal draws the nearest
  xterm colour, often much brighter for a dark background (the cube's first step is 95): measured cell by cell, plain text fell to
  4.0:1 (3.2:1 on a beat), and in xterm's default 16 colours a character fell to 1.5:1. Text now takes the nearest drawn colour
  that is no darker and backgrounds the nearest that is no lighter (`RGB.TextSGR`, `RGB.BackSGR`): at worst 4.8:1 in 256 colours
  and 7.5:1 in 16. The cover picture keeps the nearest colours.
- **Frame cap.** A tick that turned the disc and the level reading it asked for each changed the screen, so the host repainted
  twice per tick: with the real teahost and a disc on screen, 26.2 repaints a second at the 15 fps tick and 11.5 in low power
  (stated as about 6). A pulse tick is now one frame: 15.0 and 6.2. With a cover drawn (no disc) the tick changed nothing, so the
  CPU figures above, measured with a cover, were already right: the real-terminal test measured 13.5% at 8ad63ac and 12.3% after.
- **Measuring after a /reload or restart.** session_start builds the player for the footer with no screen open, and the model
  started as shown: it added mpv's astats filter and ticked at 15 frames a second in the background (a real pig 0.4.0 Binary:
  4.5% of a core instead of 1.8%; mpv 1.6% instead of 0.7%). The model now starts hidden until the overlay opens. The disc had
  the same background ticking since M7 whenever it was drawn instead of a cover.
- **The filter left in mpv.** mpv outlives the extension, so one killed while the player was shown left the filter running (seen
  with `kill -9` in the Binary). The player removes a left-over filter when it attaches (one `af remove`).
- **Order of the switch-on and switch-off.** Each command runs in its own goroutine, so a switch-off could overtake the switch-on
  before it and leave the filter on while hidden. The latest wish is now applied under a lock (`levelsSync`).
- **NO_COLOR.** The palette was off, but the accent and error styles still wrote `38;5;5` and `38;5;1`. With NO_COLOR or
  TERM=dumb they are bold now (`art.NoColor`); an unknown terminal keeps the basic accent as before.
- **Calm** now stills the footer status and widget spinner too ("no motion").
- A test-only fix: the mpv unshuffle test failed in 4 of 40 runs under `-race`; it now waits for the restored order.

Checked and fine: pure Go, no cgo, no new dependency (go.mod unchanged); extraction bounded by the cache's 4096 px and 2 MiB caps
(0.45 s for a 4096x4096 picture, 14 ms for a 1280x720 thumbnail, off the drawing path); the cross-fade is one-shot ticks in a
single chain; real levels (re-run on mpv 0.37: RMS 0.353 and peak 0.4999 for a 0.5 sine); no OSC 11 or any set of the terminal's
colours in a recorded `script` of a real pig session (play, show, pause, hide, `kill -9` while shown): only PiG's own `?` queries.
The tint opt-in is not built, which the Go SDK v0.4.0 justifies (no raw terminal output call). mpv 0.37 replaces a filter added
again with the same label (the fake refuses it; `SetLevels` handles both).
Not verified: anything on macOS, Windows or Termux; how the colours look to a person; a real 16-colour console; the native
engine's levels in a real pig run; audio latency against the meter.

## Milestone 9: pig-music in the pig-with-batteries Piglet

- **Selection**: `piglets/pig-with-batteries/piglet.yaml` gained the Package `pig-music: local:../../components/pig-music` (staged from
  `components/`, no sibling paths) and the extension `pig-music` (`origins: [package:pig-music]`, `tools: []`), plus the catalog
  notes, the description, the generated `index.json` entry, the Piglet README row and the release notes (a Piglet row and a
  component row). The README says what it adds, that it is off until `/music`, its dependencies (mpv, yt-dlp, a JS runtime, or the
  native engine) and the consent for library cookies.
- **Off until `/music`**: the extension registers the `/music` command and the `alt+m` shortcut and two lifecycle handlers. At
  session start it reads `settings.json` (absent: nothing) and looks for an mpv socket of its own: no process, no network, no
  files, no footer, no timer. It follows an mpv that is already playing only to keep the footer right after `/reload`.
  **Tested** (`piglets/pig-with-batteries/tests/pig-music.integration.test.mjs`, a real Binary in a tmux pane with `PIG_TEST_FAUX`,
  `PIG_OFFLINE`): spy `mpv`, `yt-dlp`, `deno` and `node` that record every call are never called; the pig process has no child
  process and no socket; no file with music, mpv, yt-dlp, cookie or `.sock` in its name is written under the agent dir, PIG_HOME
  or the runtime dir; the screen shows no music text; idle CPU is 0.00-0.25% of a core; `/music stop` and `/music now` with nothing
  playing start nothing either. (PiG itself opens one TLS connection at start for its version check unless `PIG_OFFLINE=1`; the
  baseline Binary does the same.)
- **Built with pig 0.4.0** (`CGO_ENABLED=0`; the fused-member safety checks accept pig-music); `scripts/pig-requirement.json` is now
  `0.4.0` (it was `0.3.1`; the Piglet build refused a 0.4.0 pig, and the SDK `v0.4.0` extension needs one). `npm test` still passes
  (122 tests, 5 new in `scripts/pig-music-batteries.test.mjs`).
- **Measured on the test machine** (linux/amd64, 5 alternating runs each, fresh HOME, `PIG_OFFLINE=1`, time from launching pig in tmux to the editor
  showing): Binary size **68,173,984 -> 85,709,063 bytes (+17,535,079, +25.7%)**; startup median **216 ms -> 216 ms (delta 0
  ms, runs 210-226)**; resident memory after 3 s **91 MiB -> 104-116 MiB (+13 to +25 MiB, the extra program text and the
  extension's own runtime)**; idle CPU 0.00% in both. `GODEBUG=inittrace=1` puts pig-music's package initialisation (cookies, the
  native resolver, native) at about 1.5 ms and 0.5 MB in total, inside the noise.
- **(Superseded by M9b below: the native engine left the extension, the increase is now +1.06 MB.)** The size was large, so this was an owner question. Where it comes from (probe programs, stripped):
  the SDK alone 2.1 MB; the `native` package alone 16.2 MB (waxflow decoders, waxtap with the goja JavaScript engine, protobuf,
  oto); the whole extension 26 MB. So about 14 MB of the 17.5 MB is the native engine. **And in the batteries Binary the native
  engine cannot play by itself**: its Player attaches to a `pigmusic serve` program that must be installed separately
  (`native.FindServeBinary`: PATH, `PIG_MUSIC_SERVE` or `nativePath`), and only that program decodes. So `engine: auto` falls
  back to native only where `pigmusic` is installed, which the doctor says. Options for the owner: (1) accept +17.5 MB; (2) a
  `nonative` build tag (native Source/serve compiled out of the Piglet, about -14 MB, the engine is then mpv only); (3) a separate
  "batteries + music" Piglet. Not decided here.
- **Real run** (the same test, a built Binary, `PIG_MUSIC_SMOKE=1`): `/music` opens, a search plays through real mpv `--ao=null`,
  `q` hides it and mpv keeps playing, `/reload` keeps the same mpv process, `/quit` stops mpv: 6/6.
**Not tested**: macOS, Windows, arm64 Binaries (only linux/amd64 was built and run, as for the other members); a Binary built
from a published release; the first `/music` doctor/setup consent flow inside the Binary (it is the M4a flow, tested in the
extension tests, not repeated here).

## Milestone 9b: size and memory (owner: "is pig-music optimized as best it could be?")

**1. Profile** (probe program that imports the extension, stripped; `go tool nm -size` by package; `GODEBUG=inittrace=1`):
the extension alone was 26.1 MB, of which the native engine was about 14 MB: WaxTap and its JavaScript engine (goja, regexp2) about
2.4 MB of symbols, WaxFlow's decoders 2.0 MB, waxlabel 1.3 MB, x/text collation 1.5 MB (pulled in by waxlabel), oto and the
PulseAudio client 0.1 MB. Init cost at load: the extension's own packages about 1.3 ms and 0.6 MB (`cookies` 173 KB from one
`regexp.MustCompile` with a `{0,200}` repeat, `native` 117 KB, `ytdlp` 65 KB, `art` 60 KB, `doctor` 32 KB), plus `goja`,
`waxtap` and protobuf registrations (about 1 ms and 0.8 MB).
**2. Architecture.** The extension no longer links the engine. `native` (light: the client Player, the wire protocol, the engine
loop and daemon over the `Decoded`/`Output`/`Opener` interfaces, Source search over plain HTTP, health) keeps no dependency on
WaxTap, WaxFlow or oto; `native/wax` (new, heavy: the WaxTap opener, the range reader, the WaxFlow decoder, the oto output, WaxTap
error classification registered with `native.RegisterClassifier`, the stream probe, `wax.Serve`, `wax.RunHelper`) is imported by
`cmd/pigmusic` only. `cli.Run` gained `Env.Serve` and `Env.Native` hooks (the extension supplies none: `serve` and `native` answer
"run the pigmusic program"). What the extension still needs from WaxTap runs in the pigmusic program as a one-shot helper:
`pigmusic native playlist <id>` (a public playlist for `Source.Tracks`) and `pigmusic native probe <video id>` (the doctor's
stream probe), one JSON document on stdout, output capped at 4 MiB, 75 s timeout, errors redacted of URLs and addresses
(`native.Helper`, tested with the test binary as the fake program: ok, error without leaks, garbage, a 10 MB flood, a hang, a missing
program). The playback path is unchanged: `pigmusic serve` is still the daemon, started by `native.Player`. Search needs no engine
(plain HTTP). Real checks with the built `pigmusic`: `search` (3 results), `native probe` (resolved in 371 ms, 64 KiB read),
`native playlist` (entries), `play` with `PIG_MUSIC_ENGINE=native PIG_MUSIC_NATIVE_OUTPUT=null` (playing, 0:04 / 3:34 five seconds
later), `stop` (daemon gone). A test (`deps_test.go`) fails if the root package, `cli`, `engine` or `native` ever link WaxTap, goja,
WaxFlow, oto or protobuf again.
**3. Lazy until /music**: every package-level `regexp.MustCompile` of the extension is now `internal/lazyre` (compiled on first use,
tested): the extension's init cost fell from about 1.3 ms and 0.6 MB to about 0.2 ms and 4 KB. What is left at load is the SDK, the
Bubble Tea and lipgloss packages' own init (a few tens of KB), and the extension's small tables.
**4. Re-measured** (linux/amd64, `PIG_OFFLINE=1`, fresh HOME per run, alternating runs, the real-terminal test prints them):

| | before M9b | after M9b |
| --- | --- | --- |
| pig-music probe program, stripped | 26.1 MB | 9.5 MB |
| batteries Binary (baseline 68,173,984 bytes) | 85,709,063 (+17.5 MB, +25.7%) | **69,230,752 (+1.06 MB, +1.6%)** |
| batteries startup, median (baseline) | 216 ms (216) | 109 ms (111) in a 9-run pass; 217 (205) in a 5-run pass on a busier machine: **no measurable delta (-2 to +12 ms)** |
| batteries resident after 3 s, vs baseline | +13 to +25 MiB | **-6 to +2 MiB, inside the run-to-run noise** |
| idle CPU | 0.00% | 0.00% |
| the `pigmusic` program, stripped | (one program with everything) | 23.6 MB, holds the engine |

**5. Behaviour**: `engine auto|mpv|native` selection, `/reload` keeps playback, `/quit` stops mpv and Termux staying mpv-only are
unchanged (all tests; the real-terminal player test 12/12, hello 16/16, the batteries test 6/6: nothing before `/music`, then play,
`q`, `/reload`, `/quit`). Finding for the record: the full-screen player has always driven mpv only; the engine choice reaches the
`/music` quick commands and `pigmusic`, so the native code in the extension only ever served the quick commands.
**Not done / follow-up**: the doctor and `/music setup` say how to build `pigmusic` but do not build it themselves (no consent-gated
`go build` yet), and the Package does not carry it as a companion binary (Pigpen carries no binaries; a build step in `npm run
stage` would be the way if the owner wants it). Not tested: darwin and Windows runs of the helper (cross-compiled and vetted for
linux, darwin, windows and android/arm64 only).
### Review of milestone 9 (lane rev-pig-music-m9)

Reviewed `3354073..9fd9d1b` (one commit, 9fd9d1b) on top of the merged rev-pig-music-m8 fixes; verdict in the lead's tasks
directory (`rev-pig-music-m9-VERDICT.md`). Fixed on `rev-pig-music-m9`, each with a red test first:
- **The batteries Binary became dynamically linked.** Before pig-music it was a static executable; with it, `readelf` showed
  `INTERP /lib64/ld-linux-x86-64.so.2` and `NEEDED libdl.so.2, libpthread.so.0, libc.so.6` (strace: the loader opens them at
  every start). Cause: oto's purego (its `internal/fakecgo` turns a `CGO_ENABLED=0` build into a dynamic one), reached through
  `cli` -> `native.Serve`, although the extension never plays audio (`pigmusic serve` does). Such a Binary does not start on
  musl, in a static container or under Termux. The device moved to `native/device`, linked by `cmd/pigmusic` only (blank
  import); a program without it refuses a device output with a message naming `pigmusic serve`. The Binary is static again,
  85,426,336 bytes. Tests: the extension's import graph has no purego/oto/pulse (`go list -deps`); `pigmusic` still has the
  device; the integration test checks the Binary's ELF has no interpreter.
- **The texts promised a native fallback that `/music` does not have.** Inside PiG, `/music` and its quick commands force
  `engine` to mpv (`quickcmd.go` `cliEnv`; the component README says the choice is wired into `pigmusic` only), so the M9 note
  above ("`engine: auto` falls back to native only where `pigmusic` is installed") and the Piglet README / catalog ("or its
  pure-Go native engine", "auto: mpv when healthy, else native") were wrong. **The lead's M9 rule "engine default in
  batteries: auto (mpv when healthy, else native)" is not met**: it is mpv only. Texts corrected; the native engine's code
  (waxtap, goja, waxflow, protobuf) is still in the Binary but unreachable from `/music`.
- **Not quite nothing at start**, now stated: in the TUI, session_start reads `settings.json` and connects to its own mpv
  socket (ENOENT on a fresh machine, seen in strace); if an mpv from an earlier session is already playing (`stopOnExit`
  off, a crash, another PiG) it attaches and shows the footer with no `/music`. That is the M6 behaviour for `/reload`; it is
  documented now, not changed. Print mode makes no such call.
- **Test strength.** The "does nothing" test took snapshots (child processes, `ss -tunap` without unix sockets): a Binary
  whose pig-music ran `true`, or dialled TCP, at session start passed it (both mutations tried). A strace part (skipped
  without strace) now checks every execve, socket and connect from launch; both mutations fail it. `ss` lists unix sockets.
- **The test wrote to `~/.pig`.** Its Binary build ran `pig piglet build` with the real PIG_HOME, which publishes artifacts
  and receipts there; it now uses a throwaway one.

Measured again (linux/amd64, PIG_OFFLINE, PIG_TEST_FAUX, fresh directories): size 68,173,984 -> 85,426,336 bytes (+17.25 MB,
+25.3%) after the device fix (85,717,255 before it); startup delta +1, +4, +14 and +19 ms in four runs of 5 or 11 alternations,
which is inside the method's resolution (a 20 ms screen poll; the medians themselves moved 108-230 ms between runs with the
machine's load); resident +12 to +25 MiB; idle CPU at most one clock tick in 3 s. Size by probe (stripped, standalone): the
extension without `cli`, `engine` and `native` is 5.2 MB, with them 25.8 MB, so most of the +17 MB is native-engine code that
`/music` cannot reach today, which makes the owner's option 2 (compile it out of the Piglet) free of any loss of function now.
Re-run here: the M3 smoke with `pigmusic` (search, play, queue, pause, resume, next, stop; each command a new process that
reattaches; socket 0600 in a 0700 directory; no mpv left after `stop`), and the batteries test 8/8 with `PIG_MUSIC_SMOKE=1`.
yt-dlp 2026.08.19 on the test machine: `JS runtimes: none` on the default PATH (it warns, and still lists format 251 through the
visionos client for the probe track); with node 24 on PATH the doctor passes `js-runtimes=node` to mpv, and yt-dlp solves the
JS challenge with node. Not verified: macOS, Windows, arm64 Binaries; a musl or Termux start (inferred from the ELF headers,
not run); the first-`/music` consent download inside the Binary.

### Merge note: rev-pig-music-m9 with M9b

The review's F1 fix (oto in `native/device`, registered from an `init`) and M9b (oto in `native/wax`, passed in as `ServeDeps`
by `cmd/pigmusic`) solved the same problem two ways. The merge keeps M9b's structure: `native/device` and
`HasDeviceOutput` are gone, `wax.Deps()` supplies the opener and the device, and a program without them refuses a device
output with `errNoEngine` ("use the pigmusic program"). The review's tests stay (the extension's `go list -deps` has no
purego, oto or pulse; the Binary is a static ELF with no PT_INTERP; the strace check; a throwaway PIG_HOME); the two that
named `HasDeviceOutput` now check `ServeDeps` (`native/review_m9_test.go`, `cmd/pigmusic/main_test.go`). Batteries Binary after the merge:
69,230,752 bytes, statically linked, integration test 7/7. F2/F3 texts: the native engine is not in the Binary at all now.

### Review of milestone 9b (lane rev-pig-music-m9b)

Reviewed `38062ff..49e7b8fc` (one commit) after merging rev-pig-music-m9 (above). The verdict is in the lead's tasks directory
(`rev-pig-music-m9b-VERDICT.md`). Fixed on `rev-pig-music-m9b`, each with a red test first:
- **The doctor's build command did not work.** M9b makes `pigmusic` the only way to get the native engine, and its fix text
  (`native.BuildPigmusicHint`) is `go build` in `cmd/pigmusic`. That module had no `go.sum`, so the build failed outside Pigpen's
  generated go.work ("missing go.sum entry" for WaxTap, WaxFlow, oto). It now has one (`go mod tidy`, the same versions as the
  extension). A test checks this offline.
- **The helper protocol was not versioned.** An older `pigmusic` (one with no `native` command) was reported as "exit status
  2". Replies now carry `"version": 1`. A reply with a missing or different version is refused with the program's path and how
  to build the matching one, and an older pigmusic is named as older. A helper that floods is stopped once it passes the 4 MiB
  cap (the pipe closes) and reported as a flood; before, it ran until the 75 s limit. `WaitDelay` bounds a grandchild that
  holds the pipes open.
- **A relative `nativePath` or `PIG_MUSIC_SERVE` resolved against the working directory**, which is the project PiG runs in.
  Such a path is now refused: use an absolute path, or a bare name looked up on PATH (Go's `exec.LookPath` already refuses a
  match in the working directory). `mpvPath`, `ytdlpPath` and `PIG_MUSIC_MPV` still accept relative paths (pre-M9b, left open).
- **The texts promised routes that PiG does not take.** Inside PiG, `/music` and its quick commands force mpv (`quickcmd.go`
  `cliEnv`), so a PiG session never starts `pigmusic serve` or runs the helper. The M9b note above ("the engine choice reaches
  the `/music` quick commands") and the component README ("they speak to `pigmusic serve`", "the doctor says how") were wrong.
  The READMEs are corrected. This note is kept as written; this paragraph corrects it. The helper has no caller in any command:
  `native.Source.Tracks` is reached only for `LM`, which it refuses before the helper, and `engine.NativeHealth(..., probe=true)`
  has no caller. So it runs only by hand (`pigmusic native ...`) and in tests.
- **Test strength.** Two M9b helper tests let mutations through: one removed the output cap, the other dropped the helper's
  own message on failure. Tests now cover both, plus a concurrent first use of `lazyre` (`-race` catches a `sync.Once`
  removed). The required key mutations are caught: mpv request_id matching, seek and next before anything has loaded, the key
  decoder's SS3 branch, yt-dlp's video-ID pattern, the doctor's `JS runtimes` parse, the cookies bundle-ID pattern, and the
  redaction URL pattern.

Re-measured on the test machine (linux/amd64, pig 0.4.0, `CGO_ENABLED=0`, `PIG_SOURCE_ROOT` = a v0.4.0 checkout, throwaway
PIG_HOME):
- Binary size. Baseline (3354073) 68,173,984 bytes and M9b 69,230,752 bytes, both byte-for-byte as claimed (+1,056,768,
  +1.6%). After the review fixes: 69,234,848 bytes. All three are static. `go version -m` lists no WaxTap, WaxFlow, goja,
  regexp2, oto, purego or pulse module.
- Startup and memory, in two passes of 9 alternations. Startup medians were 210 vs 215 ms and 213 vs 222 ms (+5 and +9 ms).
  The claimed absolute 109/111 ms was not reproduced on a busier machine; the "no measurable delta" was. Resident memory was
  -3.6 and -7.8 MiB against the baseline (claimed -6 to +2). Idle CPU was 0.00% in both.
- Init cost (`GODEBUG=inittrace=1`): +14 inits, +1.9 ms and +57 KB against the baseline. pig-music's own packages take about
  0.16 ms.
- Probe program (a blank import of the extension, stripped): 17.3 MB and dynamic before, 5.3 MB and static after.
  `pigmusic` stripped: 23.6 MB, dynamically linked (oto/purego). It is the audio program, so it needs glibc.

Re-run here:
- The batteries Binary test: 8/8 with `PIG_MUSIC_SMOKE=1`, including the strace "nothing until /music" check, play, `q`,
  `/reload` with the same mpv, and `/quit`.
- The player real-terminal test: 12/12. `npm test`: 124 (123 plus the new one). `npm run check` passes.
- `go test -race -count=3` on every package, both after the merge and after the fixes.
- Vet passes for linux, darwin and windows. `CGO_ENABLED=0` builds pass for six targets.
- The live WaxTap tests (`PIG_MUSIC_LIVE=1`) pass.

M3 smoke with the built `pigmusic` and `--ao=null`:
- doctor, search, play, queue, pause, resume, next and stop all work. Each command is a new process that reattaches.
- The socket is mode 0600 in a 0700 directory. No mpv is left after `stop`, and mpv gets `--ytdl-raw-options-append=js-runtimes=node`.
- The native engine works with a null output, both search and play. A `kill -9` of the daemon is reported as "not running",
  and the next play starts a new one.
- A missing `PIG_MUSIC_SERVE` program gets a clear message. An M9 `pigmusic serve` still works, since the serve protocol is
  still 1.
- yt-dlp 2026.08.19, as observed: `JS runtimes: none` on the default PATH. It warns ("only deno is enabled by default"), but
  still selects format 251 (opus) for the test track. With `--js-runtimes node` (node 24.19.0) it does not warn.

Not verified:
- macOS, Windows and arm64 runs.
- The serve-protocol mismatch message (`native/client.go` "stop it and try again"). If the pigmusic found is itself of
  another version, that advice repeats the failure. Left open.

## Milestone 9c: latency (owner: "search, details and my likes take quite some time")

Measured on the test machine (linux/amd64, yt-dlp 2026.08.19, mpv 0.37, `pigmusic doctor --timings`, 10 results for "never gonna give you
up"; the owner's Mac was 2.0 s for a search):

| step | before | after |
| --- | --- | --- |
| `pigmusic search` (mpv engine) | 4.3 s, titles only | **0.5 s**, with artist and length |
| `pigmusic search` (engine auto) | 4.3 s + 2.0 s doctor, twice | 0.5 s |
| details of one track (artist, album, length) | 2.8-3.1 s (yt-dlp watch page, format resolution and JS challenge included) | **0.17-0.30 s** (one InnerTube `next` request) |
| a 10-row search result list filled in | 3 at a time at 2.8 s: about 10 s | none needed: the rows arrive complete |
| the doctor before the first command | 2.06 s (mpv 81 ms, `yt-dlp --version` 811 ms, `yt-dlp -v` 1157 ms, node 14 ms) | unchanged for a command that starts something; skipped for `search` |
| library, liked songs (566) | every call: cookie store decrypted by yt-dlp, all 566 paged before anything shows, no cache | first 50 rows after one short run; more as the cursor nears the end; remembered for the session; `r` asks again. **Not measured on a real account** (no cookies on the test machine, by the owner's rule): tests with a fake yt-dlp only |

**What was built.** (1) `ytdlp.Source.Inner`: YouTube Music's own WEB_REMIX search (the `native.Source` that already existed: one
request, title, artists, album, length, thumbnail, no cookies) is asked first, yt-dlp only when it fails or finds nothing; the
extension, `pigmusic` and the overlay all use it (`native.NewSearcher`; `PIG_MUSIC_SEARCH=ytdlp` turns it off). `pigmusic
search` asks it before the doctor, and in auto mode does not run the engine health check for a search. (2) `native.Source.Enrich`
(`youtubei/v1/next`, fixture `native/testdata/next-song.json`, recorded without any visitor data) fills artist, album, length and
cover for a track, 0.2 s; `ytdlp.Source.Enrich` uses it first and falls back to the watch-page extraction; the UI asks about 30 rows
from the cursor at most (`enrichWindow`) and the cursor moving, in the results, the queue or an opened library list, asks about the
next ones. (3) `ytdlp.Source` remembers the library for the session in memory (`library_cache.go`, nothing written), pages a
collection with `--playlist-items` (`music.TrackPager`, 50 rows, the next page when the cursor is within 10 rows of the end),
`music.Refresher` and the Library tab's `r` forget it; cookies are still read only by library calls and only after the recorded
consent. (5) `pigmusic doctor --timings` lists every program the doctor ran with its time, then times the direct and the yt-dlp way
of a search and of a track's details. Unit tests use recorded fixtures and a fake yt-dlp; none reaches the network (a probe
that logged every direct search during the cli, extension and engine tests found one that did, and the cli rig now sets
`PIG_MUSIC_SEARCH=ytdlp`).

**Doctor in parallel (after READY).** The doctor ran mpv, `yt-dlp --version`, `yt-dlp -v` and, when no runtime was found, `yt-dlp -v`
again with node, one after the other: 2.06 s (2.38 s with node on PATH). They are independent, so they now run together
(`doctor.startVerbose`; the run that names a PATH runtime is started speculatively beside the plain one and used only when the
plain one finds none): **2.38 s -> 1.21 s** (the slowest single run). Same report; `node --version` is now also run when the
plain run would have found a runtime (about 30 ms, in parallel). The `doctor --timings` wall time line replaces a sum.

**Step 4, prefetch: not built; measured.** mpv 0.37's `--prefetch-playlist=yes` does not hide the yt-dlp hook: two tracks started
6 s from the end, the second one began playing about 3.5 s after the first ended with and without the option (4.1 s from
`loadfile` to audio for the first). So the stream of the next track has to be resolved by pig-music itself (yt-dlp `-g -f bestaudio`
or WaxTap in the daemon) and given to mpv instead of the watch URL, which changes what the queue holds (mpv's playlist entries
and `tracks.json` are matched by URL) and needs the resolved URL cached until it expires (~6 h, IP-bound). That is a design of
its own, left for a decision rather than squeezed in.

**Behaviour changes to know**: a search result now has an album and artists where yt-dlp's listing had none; the full-screen
player's Library `enter` queues the rows loaded so far (a page of 50 and any the cursor already fetched), not the whole
collection; `/music` quick commands and `pigmusic` print the same list. **Not tested**: a real signed-in library (timings above are
the fake), macOS and Windows runs, the direct listing when YouTube changes its answer (the yt-dlp fallback is the safety net; a
fixture for the `next` answer must be re-recorded if the layout changes), arm64.

### Review of milestone 9c (lane rev-pig-music-m9c)

Reviewed `4078714f..1d7b00ec` (three commits) and merged rev-pig-music-m9b (its verdict appeared during the review). The
verdict is in the lead's tasks directory (`rev-pig-music-m9c-VERDICT.md`). Fixed on `rev-pig-music-m9c`, each
with a red test first:
- **The remembered library ignored a withdrawn consent and a changed browser.** Before M9c every library call asked the cookie
  access (the consent file, then the browser), so deleting `cookie-consent.json` took effect at once. The session memory
  answered `Library`, `Tracks` and `TracksPage` without that check. Now every library call checks it first (it reads the consent
  file, not the cookies); a failure or another browser/profile forgets the memory, and an answer that was on its way when the
  memory was forgotten is not kept.
- **Every direct request made a new HTTP transport** (`Source.HTTP` nil): a new TCP and TLS handshake per search or details
  request, and an idle connection left behind each time. One client is shared, made on first use. Details: 169 -> 129 ms
  (mean of 8); in `doctor --timings` 274 -> 139-143 ms.
- **A direct search that failed was never named** when the way after it failed too (yt-dlp, or the doctor refusing because mpv
  is missing): the message now adds "YouTube Music's direct search failed first: ...".
- Tests for gaps found by mutation: `r` forgetting opened collections, an overlapping page, a stale page, the queue cursor
  asking about nearby rows, `PIG_MUSIC_SEARCH=ytdlp`.

Re-measured on the test machine (linux/amd64, mpv 0.37.0, yt-dlp 2026.08.19, node 24.19.0 found by the doctor):
- `pigmusic search "never gonna give you up"`: 0.42-0.61 s (six runs), with artist and length. At 4078714f the same command took
  9.7-9.8 s here (auto mode ran the doctor twice, 3.6 s each, then yt-dlp's 2.4 s search).
- `doctor --timings`: direct search 0.44-0.57 s, yt-dlp search 2.35-2.53 s; direct details 0.27 s (0.14 s with the shared
  client), yt-dlp details 2.71-2.95 s. The doctor itself took 3.6-3.7 s here, not 2.06 s: it runs `yt-dlp -v` twice (once
  without a runtime, once with `--js-runtimes node`), and `--timings` marks those runs "failed: exit status 2", which is by
  design for `-v` with no URL.
- The client version (`1.20260901.01.00`) is not a key: on the day of the review YouTube Music answered search with
  `1.20200101.01.00` too (`0.1` gets HTTP 404), and no API key is sent.
- Prefetch (step 4): the mpv manual for `--prefetch-playlist` says "This does not work with URLs resolved by the youtube-dl
  wrapper, and it won't", which matches the 3.5 s the lane measured; a rough re-run here saw about 4-5 s between tracks.
- yt-dlp `--playlist-items` on a public 182-entry playlist: all 2.95 s, items 1-50 2.51 s, 51-100 2.34 s. With cookies each page
  is its own yt-dlp run with a cookie decryption; not measured (no signed-in account here).
- The batteries Binary (pig 0.4.0, `CGO_ENABLED=0`, `PIG_SOURCE_ROOT`, throwaway PIG_HOME): 69,267,616 bytes at 1d7b00ec,
  69,275,808 after the reviews, statically linked; the integration test 7/7 with `PIG_MUSIC_SMOKE=1`. The real-terminal player
  test 12/12. M3 smoke with `pigmusic` and `--ao=null`: search, play, pause, resume, next, queue, stop; no mpv left.

Open (not fixed here; F4, F8 and the m9b F8 advice were fixed afterwards): the Library's `enter` queues only the rows loaded so far (50 of 566 liked songs unless the cursor went
further) and says nothing about it; this is a change from M6 that needs the owner's or the lead's decision. Step 3's "one
yt-dlp run for all playlists (batch URLs)" was not built and is not mentioned above. For a video row whose byline has no artist
link, the second segment ("1.8B views") would be taken as the album (not seen in real answers, which link the artist).

## Milestone 7 plan (historical, built as 7a, 7b and 7c above): art and disc, settings from the status line, quick commands

State at the start of M7: M1 to M6 READY (M6 is d4965cd, including the open-on-Search fix); the owner's Mac runs the library with
`cookieBrowser=chrome` (11 playlists, 566 liked songs). Each of M7a, M7b and M7c is tests first with its own `READY pig-music M7x`.

### M7a design (decided, not built)

- **Cover art without graphics protocols.** The extension returns text lines that the host paints; it cannot write to the terminal,
  so Kitty, iTerm2 and sixel images are out of reach from here (record this as an SDK gap in the questions file: an image-capable
  line or component API would be needed, and tmux needs passthrough anyway). Instead decode the thumbnail with the standard
  library (`image/jpeg`, `image/png`) and draw it with half blocks: centre-crop to a square, scale by box averaging to
  `cols` x `2*rows` pixels, one cell per two vertical pixels (`▀` with the upper pixel as foreground and the lower as background),
  24-bit colour when `COLORTERM` says truecolor or 24bit, xterm-256 when `TERM` has 256color, a luminance ramp (` ░▒▓█`) when
  `NO_COLOR` is set or the terminal is plain. WebP is not decodable with the standard library, so a track whose art is WebP gets the disc.
- **Source of the art**: `Track.ArtURL` when yt-dlp's metadata gave a JPEG or PNG thumbnail (add `thumbnail` to the enrichment print
  template), else `https://i.ytimg.com/vi/<id>/mqdefault.jpg`. This is a plain HTTPS fetch of a public image: no cookies and no
  yt-dlp call (differs from "via yt-dlp metadata" only in saving a 3 s call; say so). New optional interface `music.Artwork`
  (`Image(ctx, Track) (image.Image, error)`) beside Source, implemented by a new `art` package: per-track files under
  `<agent dir>/pig-music/art/<id>.img`, 2 MiB per file, 20 MiB total with oldest-first eviction, HTTPS only, image content types only,
  injected HTTP client (no network in unit tests).
- **Disc**: a braille disc (2x4 dots per cell) with a rotating highlight, 8 to 12 frames, used while no art is loaded, when art
  failed, when art is off (`coverArt:false`, default true) and in narrow or short layouts where no art fits.
- **Animation**: the model owns it. `ui.Deps.Tick` (default `tea.Tick`) schedules a 150 ms `TickMsg`; the chain runs only while a
  track is playing, the screen is shown (new `ui.ClosedMsg` from `screen.SetInvalidate(nil)`, `OpenedMsg` on show) and the Player
  screen is the current tab; paused, stopped or hidden means no ticks and so no redraws. Tests inject a recording `Tick` and send
  `TickMsg` by hand (the rig runs commands inline, so a real tick would loop forever).
- **Footer**: replace the `>` in `statusText` by a one-character spinner (`|/-\`, chosen by the whole seconds of the position, so no
  extra updates) while playing; `||` stays for paused. Update `lifecycle_test.go` and the real-terminal regex.
- **Layout**: on the Player screen's left panel (width 22 to 38) the art or disc sits above the text when at least 4 rows are free,
  size `cols-2` wide and half as many rows; width under 60 or too few rows: text only (no art). Every line stays within the width.

### M7b addition: a one-line widget when the footer is not available (owner finding on the Mac)

The M6 footer status never shows for an owner who runs context-info: its `ctx.SetFooter` replaces the built-in footer, which is
where extension statuses render, and the Go SDK gives a custom footer no access to other extensions' statuses (filed with PiG core
for 0.4.2). Plan, tests first: a setting `nowPlaying` = `auto` | `footer` | `widget`. `footer` is today's `ctx.SetStatus`; `widget` is
`ctx.SetWidget("pig-music", []string{line})` (the SDK's widget_push: one line above the editor, nothing to wait for; cleared with
`SetWidget(key, nil)`), written while the player is hidden, cleared when it is shown, on `/music stop` and when mpv goes away. The
line is the footer line plus the M7a spinner (the "record spinner": a one-character `|/-\` by the seconds of the position, so no
extra updates, and a still frame when paused). `auto` cannot detect another extension's footer (the SDK has no way to read it), so
until the SDK gap is closed `auto` means `footer`; the settings screen gets a row to cycle the three values, and the owner on the
Mac sets `widget`. Record the detection gap in the questions file. A width change must re-push the line (widgets are laid out by the
host; one line is clipped, not wrapped; check with `Context.OnWidthChange`).

### M7b, M7c

Read the lead addition at the end of the lane task file. Notes so far: the SDK's `ctx.SetStatus` takes text only, so a
status item cannot be interactive; plan `/music settings` as a small overlay (the existing settings screen can be reused as its own
overlay) with volume, shuffle, repeat, engine (auto/mpv/native, read-only until the native engine lands) and the library browser,
and record the SDK gap as a question. M7c shares code with the `pigmusic` CLI: move the command logic behind a package both call
(`cli` already has `command` methods), and register `/music play|pause|resume|toggle|next|prev|vol|now|queue|shuffle|repeat`;
`/music like` needs a write to the account (not just reading cookies), so skip it and say so. Check the SDK for command argument
completion before promising it.

### Merge note: rev-pig-music-m9c

Merged after rev-pig-music-m9b (b10d32a0). Both reviews merged the other one first, so the conflicts were in the same docs: the
READMEs keep the M9b review's texts, the progress note keeps one copy of each review and this lane's doctor-in-parallel paragraph.
The review's F1 (remembered library re-checks consent), F2 (one shared HTTP client), F3 (a failed direct search is named) and
its tests are in. **F4 fixed afterwards** (it was a regression of M9c, not a product choice): Library `enter` plays the loaded rows at once, then
fetches the rest 200 at a time and adds them to the queue, as before paging; a later choice of what to play ends it (`ui.Model.queueGen`),
so pages of an earlier collection are never added to a later queue. Tests in `ui/library_page_test.go`. M9d (prefetch) is
not started: the owner has not decided. After the merge: `go test -race` (all packages and `cmd/pigmusic`), vet linux/darwin/windows,
cross builds, `npm test` 124, batteries Binary 69,284,000 bytes static, integration 7/7, player 12/12.

## Handoff (M10b READY; read this first)

Task: the lane task file, last section (M10 a to e; `READY pig-music M10x ...` per milestone; an Opus review lane after each before the next merges). M10a is READY (3faafe58, under
review in rev-pig-music-m10a: merge its fixes with `git merge --no-ff --signoff` when told); M10b is built on top of it (stacked), see "Milestone 10b" at the end of this note.
**Next: M10c (karaoke lyrics, replacing the level bar)** when the lead says go on. Open: real-account Library timings from the owner's Mac; native engine prefetch; the 300 ms gap (0.56 s measured).
Test env as before (`GOWORK=/tmp/pm3.work`).

## Handoff (after milestone 9c; superseded by the section above)

**State.** M9 is merged (4078714f, with the review's fixes; M9b's `native/wax` structure kept over the review's `native/device`).
M9b is under review in `rev-pig-music-m9b`. M9c is READY (1d7b00ec) for steps 1, 2, 3 and 5; **step 4 (prefetch) is not built**:
mpv 0.37's `--prefetch-playlist` measured no gain, so it needs pig-music's own stream resolver (yt-dlp `-g -f bestaudio`, or WaxTap
in the daemon for the native engine) feeding mpv direct URLs, with a URL cache (~6 h, IP-bound) and a change to how the queue
matches mpv's playlist entries to `tracks.json` entries: see "Milestone 9c" in this note. Open question for the lead: build it
(design first) or leave it. The library numbers are fake-yt-dlp only (no cookies on the test machine).
Where things are: the direct search/details client is `native.Source` (`native/source.go`, `native/details.go`), wired as
`ytdlp.Source.Inner` by `native.NewSearcher`; the session library memory and paging are `ytdlp/library_cache.go` with
`music.TrackPager`/`music.Refresher`, used by `ui/library.go` and passed through `guard` in `app.go`; `pigmusic doctor --timings` is
in `cli/cli.go`. Test gotchas: the cli rig sets `PIG_MUSIC_SEARCH=ytdlp` so no unit test reaches the network.

## Handoff (after milestone 9b; M9c next, superseded by the section above)

**State.** M1 to M8 merged with their reviews; M9 (batteries) and M9b (the native engine left the extension) are READY and under
review in `rev-pig-music-m9`. Everything in "Handoff (after milestone 8)" below still holds; additions: the engine code
now lives in `native/wax` (imported by `cmd/pigmusic` only), `deps_test.go` guards that, `internal/lazyre` replaces package-level
regexps, `native.Helper` runs `pigmusic native playlist|probe`, and the batteries real-terminal test is
`npm run test:pig-music-batteries` (needs `PIG_BATTERIES_BINARY`, optional `PIG_BATTERIES_BASELINE`, `PIG_MUSIC_SMOKE=1`; build a
Binary with `CGO_ENABLED=0 npm run build:piglet -- pig-with-batteries --out <file>` and `PIG_BIN`/`PIG_SOURCE_ROOT` set to pig 0.4.0
and its source; `scripts/pig-requirement.json` is 0.4.0 now).

**M9c (latency) plan, from the lead's addition (end of `tasks/pig-music.md`); nothing is built yet.**
Baseline measured on the test machine with the built `pigmusic` (search "never gonna give you up", 10 results): **yt-dlp path 4.2 s and
titles only; the native engine's Go InnerTube search 0.5 s with artist and length** (`PIG_MUSIC_ENGINE=native`).
1. *InnerTube metadata in Go.* `native.Source.Search` (`native/source.go`: WEB_REMIX `youtubei/v1/search`, songs filter, `parseSearch`,
   `trackOf`, `details`) already returns title, artists, album and duration from one request and links no heavy dependency.
   Move it (and its fixtures `native/testdata/search-music.json`) into a neutral package (for example `innertube`) that both the
   yt-dlp Source (`ytdlp/`) and `native` use, make the mpv path search with it (falling back to yt-dlp when it fails), and add
   playlist/continuation paging with recorded fixtures (no cookies for search; the library needs cookies, see 3). Then
   `ui/enrich.go` and `ytdlp/enrich.go` only run for rows the listing lacks (`music.Enricher`, `Track.Duration==0 || no artist`).
   Check `ui/enrich_test.go` and `ytdlp` tests: the per-row enrichment tests assume titles-only searches.
2. *Metadata-only fallback* for rows still lacking: yt-dlp with no format resolution (`--extractor-args "youtube:player_skip=js,configs"`,
   `--skip-download`, or the InnerTube `next`/`player` endpoint), visible rows only, cached on disk by video ID with a size cap
   (the M6b cache is per session only).
3. *Library*: `ytdlp/source.go libraryRun` spawns yt-dlp per call (cookie decrypt each time) and pages 566 likes; batch all
   playlists in one run, show page 1 at once with lazy continuation, session cache with a refresh key, cookies still only on library calls and memory only
   (consent flow is in `cookies/` and `ui/library.go`).
4. *Prefetch*: resolve the next queue item while the current plays (mpv: `--prefetch-playlist=yes` and `--ytdl` hook cache; native: WaxTap ahead in
   the daemon, `native/engine.go` opener); reuse URLs until they expire.
5. Add `pigmusic doctor --timings` (cli/cli.go doctor), nothing on the render path, tests first with fixtures, one real-network timing run
   on the test machine (before/after for each step in this note), progress note section, questions-file post, one `READY pig-music M9c` commit.
**Traps**: the fake mpv/yt-dlp in tests (`internal/mpvfake`, saved yt-dlp JSON); `ui` rig runs commands inline (`defaultTick`);
`GOWORK=/tmp/pm3.work` (recreate if /tmp is gone: `go 1.26.0`, `use` the SDK dir, the extension dir, `./cmd/pigmusic`);
`go test -race` needs CGO_ENABLED unset; the extension must stay free of WaxTap/goja/WaxFlow/oto (`deps_test.go`).

## Handoff (after milestone 8, still valid)

**State.** M1 to M8 are built, each with its own `READY pig-music M<n>` commit on `pig-music`, and the reviews of M1 to
M7 are merged (the M7 review is the latest merge). M8 (`bd64ac8` palette, `efa01ca` pulse, `8ad63ac` safety and settings) is under
Opus review in `rev-pig-music-m8`: merge it when told (`git merge --no-ff --signoff rev-pig-music-m8`, expect
doc conflicts in this note and the README: keep both sides), re-run the gates below, commit, say merged. The owner's Mac runs the
library (`cookieBrowser: chrome`) and should set `"nowPlaying": "widget"` while context-info owns the footer.

**Gates** (all green at the last commit): `go test -race ./...` and vet for linux, darwin and windows in
`components/pig-music/extensions/pig-music` (`GOWORK` pointing at a workspace of `go 1.26.0` that uses the SDK checkout, the extension
and `./cmd/pigmusic`); `CGO_ENABLED=0 go build ./...` for linux amd64 and arm64, darwin arm64, windows amd64, android arm64;
`npm run check`; `npm test` (117); `npm run test:pig-music` (16); `npm run test:pig-music-player` (12; needs `PIG_BIN`, tmux, real
mpv and yt-dlp, network; it also prints CPU numbers). Optional real-mpv checks: `PIG_MUSIC_SMOKE=1 go test ./mpv -run Real -v`;
the CPU table: `PIG_MUSIC_SMOKE=1 PIG_MUSIC_CPU=1 go test . -run MeasurePulseCPU -v`.

**Where things are.** `ui/` (Bubble Tea model: `model.go` update, `view.go` drawing and `paint`, `anim.go` ticks, fades, pulse,
`enrich.go` background enrichment, `quick.go` the `/music settings` box, `library.go`, `settings.go`), `art/` (half-block render,
disc, thumbnail cache, palette with guaranteed contrast, colour modes), `teahost/` (drives a tea.Model without a Program; skips
redraws for identical screens), `mpv/` (player, `levels.go` astats), `native/` (pure-Go engine, daemon, levels), `cli/` (the
`pigmusic` command line; the quick `/music` commands run `cli.Run` in-process), `doctor/`, `selfmanage/`, `cookies/`, `ytdlp/`,
`music/` (types, settings, saved search), root package files `app.go` `extension.go` `lifecycle.go` `quickcmd.go` `quickport.go`.

**Traps.** The ui tests' rig runs commands inline, so the tick is a no-op by default (`defaultTick`): use a recording `Deps.Tick` and
send `TickMsg` by hand. A `Model` is a value; shared state (enrichState, artDrawn) is pointer-held. Fake-host test closures given
to `waitSnapshot`/`waitFor` run under the host mutex. In the real-terminal test wait a second between slash commands (`quick()`).
`ctx.SetStatus` takes text only and a custom footer hides statuses; widgets are `SetWidget(key, []string{line})`. mpv has no
property for "shuffled", so only the process that set shuffle shows it. Never use `pkill -f` with a pattern that matches your own
shell command; the reviewers' pigs run in parallel on this machine, so scope any `pgrep` to this checkout's paths.

**Open questions for the owner, all recorded in the questions file.** No Kitty/iTerm2/sixel images and no OSC 11 tint from an
extension (SDK gaps); `nowPlaying: auto` cannot detect a replaced footer; quick-command answers are notifications, not conversation
messages; `/music like` is not offered (it would write to the account); the library counts and the feed/playlists parsing against a
real account were confirmed by the owner (11 playlists, 566 liked songs) but the parsing is only verified by that count.

**Not tested anywhere.** The look of the palette and pulse on the owner's terminal; 16-colour consoles; Windows and Termux runs;
a real context-info footer with the widget; argument completion in a real pig; `yt-dlp -U` and the Deno download against the
network; PiG killed by a signal (mpv keeps playing); audio-to-meter latency with a real sound card.

**Next.** Wait for the M8 verdict and merge it. Optional, only if the owner asks: a persistent cache of artist and length across
sessions (M6b caches per session), a settings-screen row for `coverArt` is done in `/music settings`, 16-colour contrast on real
consoles, and the native engine through the slash commands on a machine without mpv.

## Handoff (after milestone 4, older)

State: milestones 1 to 4 are committed (M4 is `READY pig-music M4`). Next is **M4a**: `/music setup` and `pigmusic doctor`
(mpv >= 0.35; yt-dlp present, version, age and who manages it; JS runtime Deno >= 2.3 / Node >= 22 / QuickJS-NG >= 0.12 and
yt-dlp-ejs parsed from `yt-dlp -v`; on Linux a PulseAudio/PipeWire socket or libasound.so.2; one probe resolve plus a 64 KB range
read; a hook for the native engine; one message and one copy-paste fix per OS per failure; config `yt_dlp_path`, `js_runtime`,
`mpv_path` and `--ytdl-raw-options=js-runtimes=<detected>` for mpv; consent-gated download of official yt-dlp and Deno into the
extension's own data directory with SHA2-256SUMS verified, never sudo, never silent; `yt-dlp -U` at most daily and after a 403;
a 403 or missing-format error maps to "yt-dlp <version> is old or has no JS runtime: <fix>"; tests first with fake binaries on
PATH; no network in unit tests; one real doctor run recorded). Binding research: the lead's research digest (licence rules:
no bundled binaries, no GPL deps, go-licenses report for any new Go dependency).

Where things are: the extension is `components/pig-music/extensions/pig-music` (packages `music`, `mpv`, `ytdlp`, `ui`,
`teahost`, `internal/keys`, `internal/mpvfake`, `cmd/pigmusic` as its own module). `music.CheckDependencies`/`MissingError`
already name a missing mpv or yt-dlp; M4a extends that into a `doctor` package. `app.ensure` (app.go) is where the doctor must
run on first `/music`. **`music.Source` and `music.Player` must stay stable**: the parallel lane pig-music-native builds the
pure-Go engine behind them; a needed interface change goes to the lead through the questions file.

Test recipe: `GOWORK=/tmp/pm.work go test -race ./...` in the extension directory (`/tmp/pm.work` is `go 1.26.0` with `use` of
the PiG v0.4.0 SDK scratch worktree and the extension; `/tmp/pm3.work` adds `cmd/pigmusic`). Real-terminal tests:
`PIG_BIN=<pig 0.4.0> npm run test:pig-music` (hello, 16 checks) and `npm run test:pig-music-player` (6 checks, real mpv and
yt-dlp over the network).

Traps met: test closures given to `waitSnapshot` run under the fake host's mutex (do not call its other helpers inside); the
redraw callback has its own mutex because the model loop calls it while `ensure` holds the app mutex; `ctx.Custom` blocks the
command, so work done after it runs after the screen closes.

Still open for later: M5 needs the owner's answer on cookies (exact flag and browser) before anything touches a browser;
M6 is the footer status, shortcut, `/music stop`, `stopOnExit`, `session_shutdown`, shuffle, repeat and a settings screen.

## Open items (host changes would be needed, nothing here modifies PiG)

None for milestones 1 to 4. The only friction is the missing height-change redraw, worked around by polling.

## Milestone 10a: instant library and prefetch (owner GO 2026-10-05)

### What was built
- **Signed library** (`account/`, `native/browse.go`, `ytdlp/account.go`): Go InnerTube WEB_REMIX `browse` for the playlists grid (`FEmusic_liked_playlists`),
  a collection (`VL<id>`, `VLLM` = liked songs) and continuations, authenticated with `SAPISIDHASH/SAPISID1PHASH/SAPISID3PHASH` (sha1 of
  `<unix time> <cookie> https://music.youtube.com`; the test vector is sha1sum's, not the code's). The parser walks the document for the row/tile renderers
  and continuation tokens, so one parser reads the first page, a continuation and the library tabs; an unknown shape is `ErrShape`, not an empty library.
  Albums come with the rows (flexColumn 2), no per-row enrichment.
- **Cookie choice (recorded as the task asks)**: yt-dlp, with the consent and browser as today, exports the cookies once per process into an **unlinked file**
  (`ytdlp.ExportCookies`: created, opened, removed from disk at once, handed to yt-dlp as fd 3, `--cookies /dev/fd/3`, pre-seeded with the Netscape header because yt-dlp
  refuses an empty file; the URL is `file:///`, which yt-dlp refuses before any request). A plain pipe was tried and does not work (yt-dlp reads the file first and blocks;
  `/dev/stdout` too). Only google/youtube-domain, unexpired cookies are kept, in an `account.Jar` that has no write method and a `String()` that shows names only.
  No cookie reaches disk, a log or an error (tested: an error line with `SAPISID=...` is masked). No Go reader of browser stores was added (none vetted; macOS Keychain, Chrome v20 on Windows).
  401/403 drops the jar and `ErrAuth` falls back to yt-dlp, for the session until `r`; a network error falls back only that once. Windows: no `/dev/fd`, so the account path is off there (yt-dlp as before).
- **Stale-while-revalidate** (`ytdlp/disk_cache.go`, `music.LibraryCache`, `ui/library.go`): described in the README; file `<data>/library/library.json`, 0600, 1 MiB cap,
  never the browser name or spec (a hash only, so another browser's account is not shown), deleted when consent is withdrawn; the UI keeps the cursor on the same ID when the
  live rows arrive and keeps the cached rows (naming the error) when the live call fails.
- **Prefetch / M9d** (`prefetch/`, `mpv.Config.Prefetch`): `Cache.Warm` runs mpv 0.37's exact hook command for the next queue entry and keeps the JSON (stream URLs, no cookie;
  `<runtime dir>/ytdl`, 0700/0600, 4.5 h, 8 files); `Cache.Shim` is a `/bin/sh` script used as `ytdl_hook-ytdl_path` that answers from it (strict id check, never evaluated) and else
  `exec`s the real yt-dlp unchanged. Queue entries stay plain watch URLs, so `tracks.json` matching and the native engine are untouched. mpv is also told `ytdl_hook-try_ytdl_first=yes`
  (otherwise it fetches the watch page as a file first, 230 ms). `PIG_MUSIC_PREFETCH=off` disables.

### Measured (the test machine, Linux, real mpv 0.37 `--ao=null`, yt-dlp 2026.08.19)
- Gap between two tracks (`PIG_MUSIC_SMOKE=1 go test ./prefetch -run GapBetween -v`): **3.75 s -> 0.56 s** (3.9/0.83 before try_ytdl_first). Target 300 ms **not met**: what is left is googlevideo's first byte (~0.5 s
  after the hook). Closing it needs mpv to open the next stream in advance, which needs a direct-URL queue entry (a change to how entries match `tracks.json`); `--prefetch-playlist=yes` gains nothing with the hook.
- `pigmusic doctor --timings`: stream resolve 3023 ms cold, 4 ms prefetched; search/details unchanged (585/130 ms direct).
- **Library: no real number.** There are no cookies on this machine, so the account path, the cold/warm first paint and the real continuation answers of liked songs were NOT measured. The cache path is
  a file read (the unit tests show the first paint needs no network), the live path is one signed request for the first ~100 rows. The owner's Mac numbers are still wanted.

### Fixtures
`native/testdata/browse-playlist.json`, `browse-playlist-cont.json`: REAL answers for a public playlist, cut, tracking and visitor data removed (grep-checked). `browse-library-playlists.json`: **hand-written**
in the shape of the library grid (no account to record one); the liked-songs rows share the playlist shape (verified on the public playlist) but `FEmusic_liked_videos`/`VLLM` itself was never seen. The fallback to
yt-dlp is the safety net if the real shapes differ.

### Not tested
macOS (`/dev/fd` of an unlinked file, Keychain-backed browsers), Windows, Termux, a real account, real Chrome/Safari cookies, brand accounts (`X-Goog-AuthUser` is 0), the native engine prefetch, tracks that are
unavailable (their rows have no `videoId` and are dropped). Prefetch does not look past the next entry, and Prev does not prefetch the previous one.

## Milestone 10b: rich listings (owner: "you can't see albums on the listing")

### What was built
- **Columns** (`ui/view.go` `columnPlan`): title / artist / album / length share the width: the album column shows from 90 cells (and only when some row has an album), the artist from 50, the title
  keeps the rest and never less than one cell; every row is exactly as wide as the screen at every size tested (20 to 220). The Player screen's queue gets the same table with its own, narrower width, so
  the album shows there only on a very wide terminal. Library rows get their album from the signed `browse` (M10a), not from per-row enrichment; yt-dlp-listed rows still have none (shown without the column).
- **Explicit badge**: `music.Track.Explicit` (additive, `omitempty`, so `tracks.json` still reads), read from YouTube Music's `MUSIC_EXPLICIT_BADGE` in search rows and in browse rows; one `E` column after the title, only when some row in the table is explicit.
- **Albums and artists in search** (`music.Entity`, `music.EntitySearcher`, `native/entities.go`, `ui/entities.go`): a search also asks two filtered searches (albums, artists), made together with the songs search, no cookie. The results line shows
  `1 Songs n  2 Albums n  3 Artists n`; `1/2/3` switch the list, enter opens an album (its tracks in order, each carrying the album's name and the album's artist when the row leaves it out) or an artist (its top songs), enter there plays from the cursor,
  `a` adds one track, backspace goes back to the list with the cursor kept. A failed entity search leaves the songs alone. This is the seed of M10d's pages (no albums/singles/related shelves, no Radio yet).
- yt-dlp-only sources (`PIG_MUSIC_SEARCH=ytdlp`) and test sources have no entities: the screen is as before and `2`/`3` do nothing.

### Verified
- Recorded fixtures (real WEB_REMIX answers, cut, tracking removed): `search-explicit.json`, `search-albums.json`, `search-artists.json`, `browse-album.json`, `browse-artist.json`. Unit tests for badge, entities, album/artist pages, column plan, rendering at 20-220 cells, the keys.
- One live run against YouTube Music (public, no cookie, not committed): 10 entities for "daft punk"; albums open with 10-30 tracks; it found two bugs the fixtures did not show, both fixed with tests: an artist page's "1.2B plays" taken as the album, and album rows with an empty artist line.

### Differs from the brief / not done
- **No thumbnail column** ("if cheap"): it is not cheap inside the overlay (it needs an image protocol per row), so it is not built. 
- Artist top songs have no length until the details enrichment fills it (the page lists none).
- Enter on an artist plays its top songs list, not a full artist page: that is M10d.
- Not tested: a real signed-in Library with albums (the account rows' album column is from the fixture shape), terminals narrower than 20 cells beyond the width invariant, East Asian wide characters in album names beyond `clean`/`pad`.

### Review of milestone 10b (lane rev-pig-music-m10b)
Verdict ACCEPT-WITH-FIXES; findings and file:line in the lane's verdict file. Fixed on the review branch, each with a test that failed first:
- The track table was one cell wider than its room (`columnPlan` counted 11 cells of overhead, the row has 12): the screen's clipping took the last cell of the length (`12:34` read `12:3`).
- `pad` left a wide character cut at a column's edge one cell short, so the badge, artist, album and length of a CJK/emoji row sat one cell left of their headings.
- `/` over an opened album or artist typed blind (the page hid the prompt); now the prompt shows and esc goes back to the page.
- An artist's top songs had no album (it is the fourth column, after the play count); they had no length either, because the enrichment never asked about an opened page's tracks. Both filled now.
- A replaced search, and an album left while loading, kept their requests running (up to 30 s / 2 min 10 s); they are cancelled, and a generation per search and per opening keeps a cancelled answer of the same query or album from winning.
- Bidirectional embeddings/overrides/isolates (U+202A-U+202E, U+2066-U+2069) in a title are dropped by `clean`.
- `native/entities.go` and `prefetch/prefetch.go` (M10a) compiled a regexp at load again: lazyre now, and a test keeps package-level `regexp.MustCompile` out of the module.
Note on the width claim: below 24 cells the screen shows "terminal too small", so "20 cells" is that message. A track of 100 minutes or more still shows `100:0` (the length column is 5 cells; pre-existing).


## Review of milestone 10a (rev-pig-music-m10a)

Adversarial review of `1fb534d3..3faafe58`; every fix has its test, red first. Verdict and findings with file:line:
the review lane's VERDICT file. What changed:
- **Browse parser** (`native/browse.go`): a real playlist answer carries two continuation tokens (the shelf's and the section
  list's, which asks for "related playlists"); the walk took whichever Go's map order met first, so a listing could stop at
  100 rows (6 of 40 parses of a real public playlist) or take carousel rows for tracks. The walk skips carousels and the section
  list's continuations; the fixture keeps the section token it had lost when cut. Tiles linked from their title run only, and
  counts like "1,234 songs", are read; an album (`MPREb_`) is browsed by its own ID. The hand-written grid fixture follows a real
  tile's shape; a recorded signed-out answer (HTTP 200, "Sign in") is checked to fall back, not to show an empty library.
- **Cookies** (`account/jar.go`, `ytdlp/export_unix.go`, `ytdlp/library_cache.go`): only cookies a browser sends to
  music.youtube.com are kept (google.com's SAPISID, with another value, could sign the request and travel with it); on Linux the
  export file is a memfd; yt-dlp's copy of the browser's store goes to a private TMPDIR removed afterwards (a killed run left it in
  /tmp); another browser or withdrawn consent drops the account's cookies (they kept listing, and caching, the first account), and
  withdrawn consent deletes the disk cache on any library call; a failed export turns the account off for the session instead of
  running again before every page.
- **Library** (`account/library.go`, `ytdlp/disk_cache.go`): two callers of one collection no longer both append its next page
  (play-all's background fill and the cursor's pager did); a cache file over the cap is not read.
- **Prefetch** (`app.go`, `prefetch/`, `mpv/`): only the entry still next after the 4 s delay is resolved (five presses of next ran
  five yt-dlp at once); a stream mpv could not open (`end-file` reason `error`, real mpv 0.37 checked) drops its prefetched
  answer; the prefetch passes the doctor's JS runtime as mpv's hook does; a yt-dlp path with a comma reaches mpv (`%n%` form; the
  `\,` escape is not mpv syntax).
- **Tests**: the root package's tests wrote an owner token into the real `$XDG_RUNTIME_DIR/pig-music`; a TestMain isolates them.

Measured by the review on the test machine: gap between tracks (real mpv `--ao=null`, yt-dlp 2026.08.19, `PIG_MUSIC_SMOKE=1`) plain
3.45-4.28 s, prefetched 0.57-0.97 s over 8 runs (2 of the 8 runs had YouTube refuse a stream, once in each mode; the
original commit gave the same 0.76 s on the same minute's network). Cache first paint: a full 1 MiB cache reads in 7-8 ms.
Batteries Binary (pig 0.4.0, `CGO_ENABLED=0`) 69,406,880 bytes static; `npm run test:pig-music-batteries` 7/7 with
`PIG_MUSIC_SMOKE=1` (nothing before /music, strace included). strace of an export from a synthetic Firefox profile: cookie
bytes are written only to the memfd; nothing inherits it.
Still open: real-account numbers and shapes (liked songs, the grid, brand accounts with `X-Goog-AuthUser`), macOS (the export's
unlinked file sits on disk in `$TMPDIR` while yt-dlp runs), the yt-dlp fallback's own library runs still let yt-dlp copy the
cookie store into the default TMPDIR, the native engine's prefetch, the 300 ms target.
