# pig-music-native: pure-Go playback engine (M4b)

Lane `pig-music-native`, branch `pig-music-native` from `684e445` (M3). A second engine behind the existing
`music.Source` and `music.Player` interfaces that needs no external program on desktop. mpv over IPC stays the default
when healthy; Termux stays mpv-only. Interfaces in `music/types.go` are not changed by this lane.

## Step 1: licence and probe spike (verdict: viable, go on)

Everything below was run on the test machine (Ubuntu 24.04, Go 1.27.1, `CGO_ENABLED=0`) from a scratch module outside the repo
(four small programs: `probe`, `decode`, `play`, `bench`), against live YouTube, no cookies, no
tokens, no PO-token provider. Nothing was installed system-wide (`go-licenses` was built into a temp `GOBIN`).

### Pins

| module | version | licence |
|---|---|---|
| `github.com/colespringer/waxtap/v3` (module path is lower case, `/v3`) | `v3.6.0` (a2144bb, 2026-09-24) | MIT |
| `github.com/colespringer/waxflow` | `v0.0.0-20260923050513-446ca3124d89` (the pseudo-version WaxTap v3.6.0 itself requires; untagged upstream) | MIT |
| `github.com/ebitengine/oto/v3` | `v3.5.1` | Apache-2.0 |
| `github.com/ebitengine/purego` (oto) | `v0.11.0` | Apache-2.0 |
| `github.com/jfreymuth/pulse` (oto, Linux) | `v0.1.3` | MIT |
| `github.com/Microsoft/go-winio` (named pipes, Windows only) | `v0.6.2` | MIT |

"Vendor" is met as: pinned in `go.mod` with `go.sum` hashes, and the report kept here. (Review: the licence texts were not in
the repo at READY; they are now in `components/pig-music/third_party/licenses`, with WaxTap's and WaxFlow's own
`THIRD-PARTY-NOTICES.md`.) A `vendor/`
directory is not used: with one present the Go tool defaults to `-mod=vendor`, and `pig` resolves the extension's SDK
requirement to the version it stages, which a vendor tree would pin out of reach.

### `go-licenses report` (linux/amd64; darwin/arm64 and windows/amd64 are the same minus `jfreymuth/pulse`)

```
github.com/colespringer/waxflow                    MIT
github.com/colespringer/waxlabel (v1.8.0)          MIT
github.com/colespringer/waxtap/v3                  MIT
github.com/dlclark/regexp2/v2                      MIT
github.com/dop251/goja                             MIT
github.com/dop251/goja/ftoa/internal/fast          BSD-3-Clause
github.com/ebitengine/oto/v3                       Apache-2.0
github.com/ebitengine/purego                       Apache-2.0
github.com/go-sourcemap/sourcemap                  BSD-2-Clause
github.com/google/pprof/profile                    Apache-2.0
github.com/jfreymuth/pulse                         MIT (Linux)
github.com/Microsoft/go-winio                      MIT (Windows)
golang.org/x/sys/windows                           BSD-3-Clause (Windows)
golang.org/x/text                                  BSD-3-Clause
google.golang.org/protobuf                         BSD-3-Clause
```

No GPL/LGPL/AGPL. (WaxTap's `go.mod` also lists cobra and pflag; only its `cmd/` imports them, so they are not in the
build graph of a program that imports the library.) Not legal advice; this is what was checked.

### WaxTap probe (21 videos, `Resolve` best audio, then itag 251 and 140 each, 64 KiB range read)

Time-to-URL is `Client.Resolve` wall time on one warm `Client` (first call of a process was 365 ms, the rest 55 to 90 ms).
"range" is `GET <url>&range=0-65535`, the form WaxTap's own downloader uses for googlevideo hosts.

| id | kind | result | time to URL | 251 range | 140 range |
|---|---|---|---|---|---|
| dQw4w9WgXcQ | music video 3:33 | pass | 365 ms | 200, 64 KiB | 200 |
| 5NV6Rdv1a3I | official audio 4:09 | pass | 63 ms | 200 | 200 |
| MJoSyNdffGo | auto-generated style 3:52 | pass | 55 ms | 200 | 200 |
| XFkzRNyygfk | Radiohead - Creep 3:57 | pass | 83 ms | 200 | 200 |
| DyDfgMOUjCI | bad guy 3:26 | pass | 88 ms | 200 | 200 |
| TUVcZfQe-Kw | Levitating music video 3:51 | pass | 70 ms | 200 | 200 |
| fJ9rUzIMcZQ | Bohemian Rhapsody 5:59 | pass | 78 ms | 200 | 200 |
| 9bZkp7q19f0 | Gangnam Style 4:13 | pass | 87 ms | 200 | 200 |
| kJQP7kiw5Fk | Despacito 4:42 | pass | 76 ms | 200 | 200 |
| hTWKbfoikeg | Smells Like Teen Spirit | pass | 68 ms | 200 | 200 |
| gdZLi9oWNZg | BTS Dynamite | pass | 75 ms | 200 | 200 |
| e-ORhEE9VVg | Blank Space | pass | 86 ms | 200 | 200 |
| OcDiOUQBFd4 | long 23:36 | pass | 61 ms | 200 | 200 |
| X2uCBU0zUvg | long 1:02:19 | pass | 88 ms | 200 | 200 |
| rOjHhS5MtvA | long 1:21:23 | pass | 89 ms | 200 | 200 |
| PUMkA0-n1F0 | long 3:00:20 | pass | 88 ms | 200 | 200 |
| jNQXAC9IJRE | "Me at the zoo", 0:19 | `ErrVideoUnavailable` (status ERROR) | 427 ms | | |
| jfKfPfyJRdk | live stream recording | `ErrVideoUnavailable` (UNPLAYABLE) | 961 ms | | |
| HtVdAasjOgU | age-restricted | `ErrLoginRequired` ("Sign in to confirm your age") | 958 ms | | |
| Tq92D6wQ1mg | age-gated | `ErrLoginRequired` | 1315 ms | | |
| AAAAAAAAAAA | nonexistent | `ErrVideoUnavailable` | 299 ms | | |

16 of 16 playable videos passed (all ranges 200, none 403, no `ErrNeedsPOToken`, no `ErrCipherSolve`, no SABR): the five
age-restricted/unavailable results are the expected failures. Every video offered 5 audio formats (10 for the 3 h mix); both
itag 251 (Opus in WebM) and 140 (AAC-LC in M4A) resolved to direct URLs with a known `ContentLength` on all of them.
Not covered: a short (< 30 s) track, because the one ID tried is gone; a real "- Topic" channel (MJoSyNdffGo is an
auto-generated upload by an artist channel); a live stream that is actually live; regions other than this IP's.

### Decode and playback (WaxFlow over a ranged HTTP reader, then oto)

- Opus/WebM (251) opens in about 0.5 s (one 512 KiB range), 48 kHz stereo float32; AAC-LC/M4A (140) is 44.1 kHz stereo, so
  it needs a 44.1 to 48 kHz resample (WaxFlow's `dsp/resample`, HQ profile; no `beep` needed).
- **Decode CPU, 48 kHz stereo, network excluded** (23:36 track held in memory, one core): Opus 0.57 % of one core at 1x
  realtime (176x faster than realtime); AAC 0.38 % (261x); AAC plus HQ 44.1 to 48 kHz resample 0.77 % (130x). With the
  network and TLS inside the timing: Opus 0.69 %, AAC 0.42 % of realtime.
- **oto with `CGO_ENABLED=0`**: the binary builds with no C toolchain and loads libasound by `purego` at run time. the test machine
  has no PulseAudio/PipeWire server and no sound card, so the Pulse client failed cleanly at `dial unix .../pulse/native` and the
  ALSA fallback at `snd_pcm_open default`, and `oto.NewContext` returned one error naming both. With a private ALSA config
  (`ALSA_CONFIG_PATH` to a file defining `pcm.!default { type null }`, in `/tmp`) the context came up on the ALSA backend,
  oto pulled the whole 236 s Opus track through the WaxFlow reader in about 4 s, 11 353 444 frames, and the player went idle at
  the end. **Pacing was not verified**: ALSA's null device does not block, so it drains at full speed, which shows PCM flows
  but not timing. The pure-Go PulseAudio client reached the dial step only. Real timing and audible output are for the
  owner's machines; the engine therefore counts the position itself (frames handed to oto minus `BufferedSize`) and is
  tested with a paced fake sink.
- **Seeking, a limitation**: WaxFlow does not seek by WebM Cues or the MP4 `sidx`. Both demuxers keep a frame-counted
  index (so a landing is sample-exact) that is built by walking the stream from the start. A seek inside what was already
  read is free (4 min Opus: 55 ms, 4 min AAC: 1 ms from cache). The first seek past what was read costs the bytes up to the
  target, sequentially: 23 min Opus, jump to 10:00 = 22 requests of 512 KiB, 10.5 s; to 20:00 = 11.5 s; 23 min AAC to 10:00
  = 44 requests, 20 s; 1 h 21 min AAC to 10:00 = 151 requests, 70 s; a 1 h 21 min Opus to 10:00 = 9.6 s. Mitigation planned
  in step 3: larger blocks with parallel read-ahead during a walk, the player state `seeking` while it runs, and a
  documented limit; an index snapshot (`container.Indexer`) can persist a built index but does not help the first seek.
  Normal tracks (3 to 6 min) are a few seconds at worst.
- Resolve-to-first-byte is under 1 s; first-block latency per range request was 0.3 to 0.9 s, so a reader needs read-ahead
  to survive block boundaries.

### Verdict

Viable. No stop condition hit: every ordinary track resolved with no token, the two codecs decode with a fraction of a
percent of one core, and oto starts without cgo. Open risks, carried to the next steps and to the follow-ups: forward seek
cost on long tracks, no way to verify real audio timing on this host, WaxTap is one maintainer with 2 stars (it is the
second engine, mpv remains the default), PO-token providers and age-restricted tracks are out of scope by rule.

## Steps 2 to 5: what was built

All of it is inside `components/pig-music/extensions/pig-music` (Go SDK only, no internal imports, no Node, `CGO_ENABLED=0`).
The `music.Source` and `music.Player` interfaces are unchanged; `music.Settings` gained two fields, `engine` and `nativePath`.

| package | what |
|---|---|
| `native` | `Source` (YouTube Music search, WaxTap playlists), `RangeReader`, `WaxOpener`, `Engine`, outputs, the daemon, the `Player` client, `Serve`, health |
| `engine` | `Select` (auto, mpv, native), `NewNative`, `NativeHealth`: the seam the extension and the doctor use |
| `cli` | the engine in `pigmusic`, and `pigmusic serve` |

- **Source** (`native/source.go`). Search is YouTube Music's own unauthenticated WEB_REMIX search with the songs filter (WaxTap
  has no search): a plain POST, no cookie, no credential, no player client. It returns artist, album and duration, which the
  yt-dlp source does not (0.5 s for a search). Playlists use WaxTap `Enumerate`. `Library`, liked songs and album browse IDs
  say plainly that the native engine does not sign in and point at `engine = mpv`. Parsing is tolerant (no ID, bad ID,
  duplicates, no title are dropped) and an unknown response shape is an error, not an empty list. The recorded response is
  `native/testdata/search-music.json`.
- **Errors** (`native/errors.go`). Every WaxTap sentinel maps to an `*Error{Kind, Msg}` with a clear message: needs a PO token,
  extractor needs an update, expired link, age or login, private, members only, region, no audio, live, rate limited, timeout,
  network. `Retryable` says which kinds are worth one more resolve; `Open` retries once.
- **Resolve and stream on the same host** (`native/opener.go`). The daemon resolves with WaxTap and reads the stream itself,
  so the signed URL never leaves the process that is bound to its address. itag 251 first, then 140; no player client is
  named, no PO-token provider, session or cookie is configured. On 403 or 410 the range reader asks for a new link by
  resolving again (the same itag, the same length); two refusals in a row, or eight in total, end in `ErrURLExpired`.
- **Range reader** (`native/rangereader.go`). 256 KiB blocks, adaptive read-ahead (2 blocks, growing to 8 while the demuxer
  keeps waiting, which is what a seek walk does), at most 64 blocks in memory, retries with back-off, `&range=` for googlevideo
  hosts and a Range header elsewhere, 429 as `ErrRateLimited`, an origin that ignores ranges refused at open.
- **Decode** (`native/pcm.go`). WaxFlow demux and decode, mono to stereo, 44.1 kHz to 48 kHz with WaxFlow's HQ resampler (no
  `beep`), a 2048-sample pre-roll after a seek so the filter's start is not heard. Volume is the output's gain on a cubic scale
  like mpv's.
- **Engine** (`native/engine.go`): the queue, `Replace`, `Enqueue`, `Remove`, `Move`, `Jump`, `Next`, `Prev` (restarts past 3 s),
  pause, seek, volume, with the semantics the mpv Player has (idle at the end of the queue with the queue kept, removing the
  playing track plays the one that takes its place, `ErrEndOfQueue`, `ErrNothingPlaying`, `ErrNotSeekable`, which are the mpv
  package's own sentinels). **Gapless**: the next track is opened while the current one plays, and the reader moves to it
  inside one read, so there is no gap; a skip uses the preloaded track too. A track that fails is skipped and the reason is
  kept (`LastError`, shown by `pigmusic status`). A seek empties the device, shows the target at once and catches up in the
  background; a newer seek supersedes it. Opening a track is bounded (30 s); closing never waits on the network.
- **Outputs** (`output.go`, `output_oto.go`). `oto` through `purego` (PulseAudio/PipeWire in pure Go, ALSA, CoreAudio, WASAPI),
  and `NullOutput`, a paced software device (`PIG_MUSIC_NATIVE_OUTPUT=null`) so that the whole path runs, timing included, with
  no sound hardware. `oto` is behind `!android`: Termux builds, with no native audio.
- **Daemon** (`native/daemon.go`, `serve.go`, `client.go`). `pigmusic serve` listens on `native.sock` in the 0700 runtime
  directory (a named pipe `\\.\pipe\pig-music-<hash>` on Windows, current user only), JSON lines, a version handshake, state
  events pushed to subscribers. The extension's `native.Player` attaches exactly as the mpv Player does: dial, else start
  `pigmusic serve` in its own session (Setsid; `DETACHED_PROCESS` on Windows) under a start lock, and read the live state, so a
  `/reload` finds the same queue, track and position.
- **Lifecycle** (the review's rules, applied). The daemon holds a lock for its whole life: a second daemon exits 0 with
  "already running", and a socket file found while the lock is free is stale and replaced. SIGTERM, SIGINT, SIGHUP or a
  `shutdown` request stop playback, close the device, remove the socket and release the lock; `Shutdown` waits for the process and
  ends it by pid if it does not go. A start that fails or times out kills the daemon it started (process group) and leaves
  nothing behind. An idle daemon (nothing queued, no client) exits after 30 min (`--idle-exit`). Every request is bounded: 5 s on
  the client (`CommandTimeout`), 10 s on the daemon, 10 s to start, 30 s to open a track; at most 16 clients; lines over 8 MiB
  are refused.
- **Logs and messages carry no signed URL and no address.** `native.Redact` removes URLs and IPv4/IPv6 addresses from every
  error message, daemon log line and `LastError`; WaxTap's own logger is off (its debug records carry stream URLs); the daemon
  log (`native.log`) is mode 0600 and holds one line per start, stop and skipped track. Tests feed errors that contain a
  googlevideo URL, a signature and an IP and check none of it reaches an error, the log or `LastError`.
- **Engine selection** (`engine/engine.go`). `engine` in `settings.json`, or `PIG_MUSIC_ENGINE`, is `auto`, `mpv` or `native`.
  Auto uses mpv with yt-dlp when installed and the optional `Deps.MPVHealthy` hook (the M4a doctor's version, JS-runtime and
  probe checks) agrees, else the native engine, and the reason is stated. In auto mode a player that is already running (mpv or
  native) is the one controlled. `mpv` forced with mpv missing stays mpv and warns; it never switches silently. Termux/Android
  (`GOOS=android`, `TERMUX_VERSION`, or a `com.termux` PREFIX) is mpv only, and `engine = native` there fails with the Termux
  message and `pkg install mpv python-yt-dlp nodejs`.
- **Health hook for the doctor** (M4a): `engine.NativeHealth(ctx, settings, deps, probe)` returns a `native.Report` of checks, each
  with a copy-paste fix: platform, audio output (Linux: a PulseAudio/PipeWire socket or `libasound.so.2`), player program
  (`pigmusic`), and, with `probe`, one resolve plus a 64 KiB range read with the time taken. It never opens the audio device
  (oto allows one context per process). `engine.MPVProblem` and `engine.Select` are the other half. `pigmusic check` prints it.

### Using it from the extension (for lane pig-music)

```go
choice, err := engine.Select(ctx, settings, engine.Deps{MPVHealthy: doctorHook})
if choice.Kind == engine.NativeEngine {
    src, player := engine.NewNative(settings, paths, engine.Deps{})   // music.Source, *native.Player
} else { /* the existing mpv.New and ytdlp.Source */ }
```

The extension's own binary cannot be `pigmusic serve`, so the native Player starts a `pigmusic` program: `PIG_MUSIC_SERVE`, then
`nativePath` in settings (the environment wins, as `PIG_MUSIC_ENGINE` does over `engine`), a `pigmusic` beside the extension binary, or
PATH, in that order, with a clear message when none is found.
The installer or Piglet has to ship `pigmusic` (follow-up). `*native.Player` also has `LastError()` for a UI that wants to show
a skipped track.

### What was verified, and how

- `go test -race` over the module (SDK v0.4.0 through a go.work): range reader (both range styles, read-ahead overlap, bounded
  memory, refresh on 403, giving up, retry of 502, 429, ignored ranges, no URL in errors, Close stops fetching, concurrent
  readers), decoding (48 kHz, mono, 44.1 kHz resampled, seeks at both rates, the real Opus/WebM and AAC/M4A fixtures), the engine
  against a scripted decoder and a fake device (contiguous frames across a gapless change, pause, skip, prev, jump, every queue
  edit, seek and a superseded seek, failures skipped and reported, volume, subscription, position from the device buffer, close
  with a track still opening, a jump while a read is stalled, the decoder context outliving `Open`, an open that times out), the
  daemon over a real unix socket (client round trips, sentinels across the wire, reattach, shutdown cleanup, second daemon,
  stale socket, idle exit, malformed and oversized traffic, client limit, no URL in the log, a daemon that stops answering,
  a protocol mismatch), and real processes (the test binary re-executed as `serve`: its own session, outlives the client,
  reattach sees the same queue and a position that kept moving, four parallel starters start one daemon, a failed start
  reports the log and leaks nothing, a hung start is killed, SIGTERM leaves no socket and frees the lock, a hung daemon is ended
  by pid), engine selection (every row above, Termux three ways), health, and the command line against the native engine
  (search, play, status, pause, volume, seek, next, queue, jump, move, add, stop, each as a new process).
- **Live, the test machine, real YouTube, the built `pigmusic`** (throwaway HOME and runtime directory, `PIG_MUSIC_NATIVE_OUTPUT=null`,
  no cookies): `search` 0.5 s; `play` started a track 0.9 s after the command; the position moved at 1.00 s per second (live
  engine test: 0:00.09, 1.09, 2.09 ... 11.1 over 12 s); pause froze it; `seek 60` landed; `next` used the preloaded track; the
  daemon was its own session leader, 46 to 48 MB resident, about 2.7 % of one core in steady state (this includes the software
  clock's 100 Hz tick and a 13-minute track's read-ahead; decode alone measured 0.4 to 0.8 % in step 1); `stop` left no
  process, socket or lock. Kept as `PIG_MUSIC_LIVE=1 go test ./native -run Live`. A real run found one bug the fakes could not:
  `Open` was handed a context that was cancelled when the open returned, so every real track ended at once; fixed, with a test
  whose decoder reads with the context.
- **Real device**: the test machine has no sound server and no card. `pigmusic play` with the real oto output fails in 0.1 s with one
  message naming both backends and the log, and `PIG_MUSIC_NATIVE_OUTPUT=null` as the way to run without sound. With a private ALSA
  `null` PCM the real oto path plays a real track to the end (unpaced).
- **Builds**: `CGO_ENABLED=0 go build` of `pigmusic` for linux/amd64, linux/arm64, darwin/arm64, darwin/amd64 and windows/amd64;
  `go vet` of the whole module for darwin/arm64 and windows/amd64; android/arm64 builds too (oto is behind `!android`).
  `pig install .../extensions/pig-music --validate-only --json` and `pig package validate components/pig-music` pass (pig 0.4.0).
  `go-licenses report` over `cmd/pigmusic` on linux, darwin and windows: only MIT, BSD-2, BSD-3 and Apache-2.0 dependencies (the
  table above plus go-winio and golang.org/x/sys on Windows).

### Not tested

- **Audible output and real timing through a device**: no sound hardware here. The position logic (frames handed to oto minus
  `BufferedSize`) is tested with a fake device and, in real time, with the paced software one; the owner's Mac is the real check.
- **macOS and Windows at run time**: compiled and vetted only. The Windows named pipe (go-winio, its security descriptor),
  `DETACHED_PROCESS` and `pidAlive` have never run. oto on CoreAudio and WASAPI is the library's own claim.
- PulseAudio/PipeWire (no server here); the pure-Go client reached the dial step only.
- A Piglet Binary build with the extension in it (no pig-music Piglet exists on this branch), `npm run check` (the worktree has no
  `node_modules`), PiG 0.4.1. (Review: the review built one with pig 0.4.0, and it fails; see "Review" below.)
- Regions other than this address, YouTube Music search under a changed client version, a live stream that is live.

### Limitations and follow-ups

1. **Forward seek cost on long tracks.** WaxFlow seeks by walking the stream, not by Cues or `sidx` (step 1 numbers). The
   engine shows the target position at once and seeks in the background, with read-ahead growing during the walk; a first
   seek far past the played part of a 20-minute track still takes seconds. Follow-up: an approximate seek through Cues/`sidx`
   (a request upstream or an own index), or persisting `container.Indexer` snapshots.
2. **PO-token providers**: not built, by rule. A track that needs one reports `KindNeedsToken` and points at `engine = mpv`.
3. **Library and liked songs** are mpv-only (they need a signed-in account); the native Source says so.
4. **Search pages**: one page (about 20 songs); no continuation.
5. **Ship `pigmusic`** with the Piglet or installer so the extension can start the daemon; today it needs `nativePath`,
   `PIG_MUSIC_SERVE`, or `pigmusic` on PATH.
6. WaxTap is one maintainer; it is the second engine, mpv stays the default. Pin bumps need the live test and the probe.
7. The 100 ms oto buffer and a 256 KiB first block are untuned; first sound after `play` was 0.9 s on the test machine.

### Process note

The review fixes (`rev-pig-music`) are merged with `git merge --no-ff`. `-S` could not be used: the test machine has no GPG
secret key for Michael Kinsy and no signing is configured, so every commit here (the merge included) carries only
`Signed-off-by` (`-s`). The owner's SSH keys were not touched.

## Review fixes merged (M4, M4a) and how the rules carried over

`rev-pig-music-m4` is merged (`git merge --no-ff --signoff`; conflicts in `cli.go`, `settings.go`, `go.mod`, `go.sum` and the
README were resolved by keeping both sides). Where its rules apply to the native engine:

- **`--ignore-config` (yt-dlp and the mpv ytdl hook)**: nothing to pass, because the native engine runs no yt-dlp and reads no
  user configuration. The one equivalent found is WaxTap's debug dumps: `WAXTAP_DUMP_DIR` and `WAXTAP_SABR_DUMP_DIR` make it write raw
  responses, signed URLs included, to a directory. `native.SanitizeEnvironment` unsets them in the daemon and in the WaxTap client,
  and `CleanEnv` removes them from the environment of the daemon the client starts.
- **Control characters in network text**: `native.Clean` (tabs and line breaks to a space, other controls U+FFFD, bidi overrides dropped) is
  applied to every search and playlist title, artist and album, to `Redact` (so to every error, log line and `LastError`) and to the
  track name in a failure message. The UI's own filter stays the last line of defence.
- **Stall watchdog**: a decoder read that has not returned for `StallTimeout` (45 s) has its track's context cancelled, the track is
  skipped and `LastError` says the stream stalled. Opening a track (30 s), the range reader's requests (20 s, with retries) and
  every client command (5 s) were already bounded.
- With the doctor present, `pigmusic` runs it before an mpv-engine command, and in auto mode its verdict is the `MPVHealthy` hook: an
  mpv or yt-dlp the doctor finds unfit (old, no JavaScript runtime) falls back to the native engine, not only an absent one.
  A forced `engine = mpv` keeps today's behaviour.

Re-run after the merge: `go test -race -count=1 ./...` over the module (all packages, including the UI, doctor and selfmanage packages),
`CGO_ENABLED=0 go build` of `pigmusic` for linux/amd64, linux/arm64, darwin/arm64, darwin/amd64, windows/amd64 and android/arm64, `go vet` for
darwin/arm64 and windows/amd64, `pig install ... --validate-only --json` (valid, registered) and `pig package validate`, and the live
engine test (position 0.12 s, 1.12 s, 2.12 s ... against YouTube).

## How red-green order was kept for steps 2 to 5 (honestly)

The history of this branch has **no separate red commits for steps 2 to 5**, except the one for the review rules above
(`red tests, control characters ... (does not compile)`, then the green commit). In practice, for steps 2 to 5 each file's test was
written in the same working session before its implementation, and I ran it red (a compile failure for the missing symbol, or a
failing assertion) before writing the code: the range reader, the error mapping, redaction, decoding, the engine, the source parser,
the daemon, the client, engine selection and the CLI wiring were all done that way. But the tests and the code of one unit were committed
together, in four large commits, so the repository cannot show the red state, and I did not record the red runs. Several tests
were also corrected after their first run against the code, and not all of those were test mistakes: the live smoke test found a real engine
bug (the decoder's context was cancelled when `Open` returned), and the fix and its regression test are in the same commit. Treat the
claim as "tests first within each unit, red observed, not committed", and ask for a rebuilt history if separate red commits are required.

The review rules above were done properly: red commit `1628b6b` (does not compile), then green. Its fixture contained one malformed JSON document
that was fixed in the green commit.

## Review (rev-pig-music-native): what was checked and changed

Branch `rev-pig-music-native` from `3608028`. Verdict and findings: the review's VERDICT file (not kept in the repository).

- **Piglet Binary (pig 0.4.0, run by the review)**: a scratch Piglet with pig-music as its one extension fails at link with
  `go: updates to go.mod needed`. It fails the same way at `ba3a8a4` (M4) and `427095c` (M4a), and builds at `684e445` (M3). The
  cause is the M4 UI: the fused build compiles the extension inside PiG's own module, whose `go.mod`/`go.sum` pin older lipgloss,
  ansi, ultraviolet, go-colorful and runewidth and do not contain `charm.land/bubbletea/v2` at all; aligning the versions moves
  the error to `missing go.sum entry ... bubbletea`. PiG 0.4.0 accepts only `extensionRealization: fused`. The native engine is not
  the cause: M3 plus the native packages and their `go.mod` requirements builds a Binary (the extension does not import `native`).
- **Licences**: `go-licenses report` re-run (linux/amd64, darwin/arm64, windows/amd64, `CGO_ENABLED=0`): MIT, BSD-2/3, Apache-2.0
  only. The texts are now in `components/pig-music/third_party/licenses`. Not seen by go-licenses: WaxTap links all of WaxFlow's
  codecs through `waxflow/format`, including `codec/wma`, `wmapro` and `wmavoice`, whose tables WaxFlow's own notices say are data
  extracted from FFmpeg (LGPL-2.1-or-later). An owner decision before shipping `pigmusic` binaries.
- **Builds**: `CGO_ENABLED=0` `pigmusic` for darwin/arm64, darwin/amd64, linux/amd64, linux/arm64, windows/amd64, android/arm64;
  oto, purego and pulse are absent from the android graph; `go vet` for darwin, windows, android and linux.
- **Red-green, checked by mutation**: 32 mutations of key fixes (redaction, re-resolve on 403, give-up limits, stale socket,
  single instance, stall watchdog, decoder context, gapless, pause, volume, Termux, timeouts, kill paths, client limit, idle exit).
  The existing tests caught 26 (three of them by hanging until the test time-out, not by failing). Two changed nothing a test
  can see (a second redaction layer, a chmod after open). Four were gaps: three now have tests (daemon file modes, the daemon's
  own time-out, a SABR stream with a URL). The fourth, Serve on GOOS=android, needs an android test run; the Termux environment
  path is now tested.
- **Bugs fixed (red commit, then green)**: a Remove or Move while the playing track was still opening skipped the next track;
  concurrent `Attach` lost its handshake and left a connection that kept the daemon from idling out; `pigmusic serve` and
  `NativeHealth` missed Termux on a linux build; an unbracketed compressed IPv6 address leaked past `Redact`; the daemon's accept
  loop raced its final `WaitGroup.Wait` (found by `-race -count=3`). An mpv test that read state between two property events
  was made to wait for both.
- **Live, the test machine, null output** (throwaway HOME and runtime directory): `PIG_MUSIC_LIVE=1` tests pass (position 0.19, 1.19 ...
  11.2 s over 12 s); `pigmusic` search 0.5 s, play, pause (position frozen), resume, volume, seek, next, a real gapless change
  into a preloaded track, a SIGKILLed daemon's stale socket replaced by the next `play`, `stop` leaving no process or socket; the
  socket, log and lock are 0600 in a 0700 directory; the log has no URL or address; the daemon is its own session leader.
